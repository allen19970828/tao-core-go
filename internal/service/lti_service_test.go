package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"tao-core-go/internal/domain/models"
)

func TestLTILifecycle(t *testing.T) {
	db := setupTestDB(t)
	logger, _ := zap.NewDevelopment()

	scoringSvc := NewScoringService()
	webhookSvc, err := NewWebhookService(db, logger, 2, testSecretCipher(t), []string{"hooks.example.com"})
	if err != nil {
		t.Fatalf("create webhook service: %v", err)
	}
	sessionSvc := NewSessionService(db, scoringSvc, webhookSvc)
	ltiSvc := NewLTIService(db, logger, sessionSvc, testSecretCipher(t))

	// 1. Register LTI Platform (e.g. Moodle)
	platform := &models.LTIPlatform{
		Issuer:        "https://moodle.example.com",
		ClientID:      "moodle-client-123",
		KeySetURL:     "https://moodle.example.com/mod/lti/certs.php",
		AuthTokenURL:  "https://moodle.example.com/mod/lti/token.php",
		AuthLoginURL:  "https://moodle.example.com/mod/lti/auth.php",
		ToolLaunchURL: "https://tool.example.com/api/v1/lti/launch",
	}

	if err := ltiSvc.RegisterPlatform(platform); err != nil {
		t.Fatalf("RegisterPlatform failed: %v", err)
	}

	// 2. Initiate Login
	loginURL, err := ltiSvc.InitiateLogin("https://moodle.example.com", "moodle-client-123", "https://tool.example.com/exam/start", "login-hint", "message-hint")
	if err != nil {
		t.Fatalf("InitiateLogin failed: %v", err)
	}

	if loginURL == "" {
		t.Errorf("Expected non-empty login redirect URL")
	}

	// 3. Test non-existent platform login
	_, errNotFound := ltiSvc.InitiateLogin("https://unknown.com", "client-999", "https://tool.example.com/launch", "login-hint", "")
	if errNotFound != ErrPlatformNotFound {
		t.Errorf("Expected ErrPlatformNotFound, got %v", errNotFound)
	}
}

func TestRegisterLTIPlatformEncryptsValidatedPrivateKey(t *testing.T) {
	db := setupTestDB(t)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: mustMarshalPKCS8(t, privateKey)})
	platform := &models.LTIPlatform{
		Issuer: "https://private-key-lms.example.com", ClientID: "private-key-client",
		KeySetURL: "https://private-key-lms.example.com/jwks", AuthTokenURL: "https://private-key-lms.example.com/token",
		AuthLoginURL: "https://private-key-lms.example.com/authorize", ToolLaunchURL: "https://tool.example.com/api/v1/lti/launch",
		KeyID: "tool-key-1", PrivateKey: string(privateKeyPEM),
	}
	service := NewLTIService(db, zap.NewNop(), nil, testSecretCipher(t))
	if err := service.RegisterPlatform(platform); err != nil {
		t.Fatalf("register platform: %v", err)
	}
	if !strings.HasPrefix(platform.PrivateKey, "enc:v1:") || strings.Contains(platform.PrivateKey, "PRIVATE KEY") {
		t.Fatal("expected private key to be encrypted before persistence")
	}
	var stored models.LTIPlatform
	if err := db.First(&stored, "id = ?", platform.ID).Error; err != nil {
		t.Fatalf("load platform: %v", err)
	}
	decrypted, err := testSecretCipher(t).Decrypt(stored.PrivateKey)
	if err != nil || decrypted != string(privateKeyPEM) {
		t.Fatalf("stored private key round trip failed: %v", err)
	}

	tweak := *platform
	tweak.ID = "another"
	tweak.ClientID = "another"
	if err := service.RegisterPlatform(&tweak); err == nil {
		t.Fatal("expected pre-encrypted private key input to be rejected")
	}
}

func mustMarshalPKCS8(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	return encoded
}

func TestLTILaunchVerifiesSignatureClaimsMappingAndReplay(t *testing.T) {
	db := setupTestDB(t)
	logger := zap.NewNop()
	item := models.Item{ID: "lti-item", Title: "LTI Item", Prompt: "?", ItemType: models.ItemTypeSingleChoice, CorrectAnswer: "A", MaxScore: 1}
	seedTestDelivery(t, db, "lti-delivery", item)
	sessionService := NewSessionService(db, NewScoringService(), nil)
	serviceInterface := NewLTIService(db, logger, sessionService, testSecretCipher(t))
	service := serviceInterface.(*ltiService)
	platform := &models.LTIPlatform{
		Issuer: "https://lms.example.com", ClientID: "client-123",
		KeySetURL: "https://lms.example.com/jwks", AuthTokenURL: "https://lms.example.com/token",
		AuthLoginURL: "https://lms.example.com/authorize", ToolLaunchURL: "https://tool.example.com/api/v1/lti/launch",
	}
	if err := service.RegisterPlatform(platform); err != nil {
		t.Fatalf("register platform: %v", err)
	}
	if err := service.RegisterResourceLink(&models.LTIResourceLink{
		PlatformID: platform.ID, DeploymentID: "deployment-1", ResourceLinkID: "resource-1", DeliveryID: "lti-delivery",
	}); err != nil {
		t.Fatalf("register mapping: %v", err)
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	const keyID = "lms-key-1"
	service.jwksCache[platform.KeySetURL] = cachedJWKSet{
		keys: map[string]*rsa.PublicKey{keyID: &privateKey.PublicKey}, expiresAt: time.Now().Add(time.Hour),
	}
	target := "https://tool.example.com/exam/start"
	state, nonce := initiateStateAndNonce(t, service, platform, target)
	token := signedLTIToken(t, privateKey, keyID, platform, target, nonce)

	session, err := service.HandleLaunch(token, state)
	if err != nil {
		t.Fatalf("handle valid launch: %v", err)
	}
	if session.DeliveryID != "lti-delivery" || session.UserID == "lms-user-1" || session.UserID == "" {
		t.Fatalf("unexpected mapped session: %#v", session)
	}
	if _, err := service.HandleLaunch(token, state); err != ErrInvalidLTIState {
		t.Fatalf("expected replayed state to be rejected, got %v", err)
	}

	tamperedState, tamperedNonce := initiateStateAndNonce(t, service, platform, target)
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}
	tamperedToken := signedLTIToken(t, wrongKey, keyID, platform, target, tamperedNonce)
	if _, err := service.HandleLaunch(tamperedToken, tamperedState); err != ErrInvalidLTIToken {
		t.Fatalf("expected invalid signature to be rejected, got %v", err)
	}
}

func initiateStateAndNonce(t *testing.T, service *ltiService, platform *models.LTIPlatform, target string) (string, string) {
	t.Helper()
	loginURL, err := service.InitiateLogin(platform.Issuer, platform.ClientID, target, "login-hint", "message-hint")
	if err != nil {
		t.Fatalf("initiate login: %v", err)
	}
	parsed, err := url.Parse(loginURL)
	if err != nil {
		t.Fatalf("parse login URL: %v", err)
	}
	state, nonce := parsed.Query().Get("state"), parsed.Query().Get("nonce")
	if state == "" || nonce == "" || parsed.Query().Get("login_hint") != "login-hint" {
		t.Fatalf("missing OIDC correlation parameters: %s", loginURL)
	}
	return state, nonce
}

func signedLTIToken(t *testing.T, privateKey *rsa.PrivateKey, keyID string, platform *models.LTIPlatform, target, nonce string) string {
	t.Helper()
	now := time.Now()
	claims := &ltiClaims{
		Nonce: nonce, MessageType: ltiMessageType, Version: ltiVersion, DeploymentID: "deployment-1",
		TargetLinkURI: target, ResourceLink: ltiResourceLinkClaim{ID: "resource-1"},
		Roles: []string{"http://purl.imsglobal.org/vocab/lis/v2/membership#Learner"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: platform.Issuer, Subject: "lms-user-1", Audience: jwt.ClaimStrings{platform.ClientID},
			ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)), IssuedAt: jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = keyID
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign LTI token: %v", err)
	}
	return signed
}
