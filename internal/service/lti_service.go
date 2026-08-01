package service

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
	"tao-core-go/internal/security"
)

const (
	ltiMessageType = "LtiResourceLinkRequest"
	ltiVersion     = "1.3.0"
	ltiScoreScope  = "https://purl.imsglobal.org/spec/lti-ags/scope/score"
)

var (
	ErrPlatformNotFound    = errors.New("尚未註冊此 LTI 平台")
	ErrInvalidLTIState     = errors.New("LTI state 無效、已過期或已使用")
	ErrInvalidLTIToken     = errors.New("LTI id_token 驗證失敗")
	ErrInvalidLTIRequest   = errors.New("LTI 登入請求無效")
	ErrResourceLinkMissing = errors.New("LTI resource link 尚未對應到測驗發布")
)

type LTIService interface {
	RegisterPlatform(platform *models.LTIPlatform) error
	RegisterResourceLink(link *models.LTIResourceLink) error
	InitiateLogin(issuer, clientID, targetLinkURI, loginHint, messageHint string) (string, error)
	HandleLaunch(idToken, state string) (*models.TestSession, error)
	SubmitGradeToLMS(session *models.TestSession) error
}

type ltiResourceLinkClaim struct {
	ID string `json:"id"`
}

type ltiAGSEndpointClaim struct {
	LineItem string   `json:"lineitem"`
	Scopes   []string `json:"scope"`
}

type ltiClaims struct {
	Nonce         string               `json:"nonce"`
	MessageType   string               `json:"https://purl.imsglobal.org/spec/lti/claim/message_type"`
	Version       string               `json:"https://purl.imsglobal.org/spec/lti/claim/version"`
	DeploymentID  string               `json:"https://purl.imsglobal.org/spec/lti/claim/deployment_id"`
	TargetLinkURI string               `json:"https://purl.imsglobal.org/spec/lti/claim/target_link_uri"`
	ResourceLink  ltiResourceLinkClaim `json:"https://purl.imsglobal.org/spec/lti/claim/resource_link"`
	Roles         []string             `json:"https://purl.imsglobal.org/spec/lti/claim/roles"`
	AGS           ltiAGSEndpointClaim  `json:"https://purl.imsglobal.org/spec/lti-ags/claim/endpoint"`
	jwt.RegisteredClaims
}

type cachedJWKSet struct {
	keys      map[string]*rsa.PublicKey
	expiresAt time.Time
}

type ltiService struct {
	db             *gorm.DB
	logger         *zap.Logger
	sessionService SessionService
	cipher         *security.SecretCipher
	cacheMu        sync.RWMutex
	jwksCache      map[string]cachedJWKSet
}

func NewLTIService(db *gorm.DB, logger *zap.Logger, sessionService SessionService, cipher *security.SecretCipher) LTIService {
	return &ltiService{
		db: db, logger: logger, sessionService: sessionService, cipher: cipher,
		jwksCache: make(map[string]cachedJWKSet),
	}
}

func (s *ltiService) RegisterPlatform(platform *models.LTIPlatform) error {
	platform.Issuer = strings.TrimRight(strings.TrimSpace(platform.Issuer), "/")
	platform.ClientID = strings.TrimSpace(platform.ClientID)
	if platform.Issuer == "" || platform.ClientID == "" {
		return errors.New("LTI issuer 與 client_id 不可留白")
	}
	for _, raw := range []string{platform.Issuer, platform.KeySetURL, platform.AuthTokenURL, platform.AuthLoginURL, platform.ToolLaunchURL} {
		parsed, err := parseHTTPSURL(raw)
		if err != nil {
			return err
		}
		policy, err := security.NewOutboundPolicy([]string{parsed.Hostname()})
		if err != nil {
			return err
		}
		if _, err := policy.ValidateURL(raw); err != nil {
			return fmt.Errorf("LTI URL 安全驗證失敗: %w", err)
		}
	}
	if strings.HasPrefix(platform.PrivateKey, "enc:v1:") {
		return errors.New("LTI private key 必須以 PEM 明文提交，服務會在寫入前加密")
	}
	if platform.PrivateKey != "" {
		if strings.TrimSpace(platform.KeyID) == "" {
			return errors.New("啟用 LTI AGS private key 時必須提供 key_id")
		}
		privateKey, err := parseRSAPrivateKey(platform.PrivateKey)
		if err != nil {
			return err
		}
		if privateKey.N.BitLen() < 2048 || privateKey.N.BitLen() > 8192 || privateKey.E < 3 || privateKey.E%2 == 0 {
			return errors.New("LTI RSA private key 必須介於 2048 到 8192 bits")
		}
		if err := privateKey.Validate(); err != nil {
			return errors.New("LTI RSA private key 驗證失敗")
		}
		encrypted, err := s.cipher.Encrypt(platform.PrivateKey)
		if err != nil {
			return err
		}
		platform.PrivateKey = encrypted
	}
	if platform.ID == "" {
		platform.ID = uuid.New().String()
	}
	if platform.CreatedAt.IsZero() {
		platform.CreatedAt = time.Now()
	}
	return s.db.Create(platform).Error
}

func (s *ltiService) RegisterResourceLink(link *models.LTIResourceLink) error {
	if strings.TrimSpace(link.PlatformID) == "" || strings.TrimSpace(link.DeploymentID) == "" ||
		strings.TrimSpace(link.ResourceLinkID) == "" || strings.TrimSpace(link.DeliveryID) == "" {
		return errors.New("LTI resource link mapping 欄位不可留白")
	}
	if len(link.PlatformID) > 36 || len(link.DeliveryID) > 36 || len(link.DeploymentID) > 255 || len(link.ResourceLinkID) > 255 {
		return errors.New("LTI resource link mapping 欄位超過長度限制")
	}
	if err := s.db.First(&models.LTIPlatform{}, "id = ?", link.PlatformID).Error; err != nil {
		return ErrPlatformNotFound
	}
	if err := s.db.First(&models.Delivery{}, "id = ?", link.DeliveryID).Error; err != nil {
		return ErrDeliveryNotFound
	}
	if link.ID == "" {
		link.ID = uuid.New().String()
	}
	link.CreatedAt = time.Now()
	return s.db.Create(link).Error
}

func (s *ltiService) InitiateLogin(issuer, clientID, targetLinkURI, loginHint, messageHint string) (string, error) {
	if len(issuer) > 255 || len(clientID) > 255 || len(targetLinkURI) > 500 || len(loginHint) > 4096 || len(messageHint) > 4096 {
		return "", fmt.Errorf("%w: 參數超過長度限制", ErrInvalidLTIRequest)
	}
	var platform models.LTIPlatform
	if err := s.db.Where("issuer = ? AND client_id = ?", strings.TrimRight(issuer, "/"), clientID).First(&platform).Error; err != nil {
		return "", ErrPlatformNotFound
	}
	if strings.TrimSpace(loginHint) == "" {
		return "", fmt.Errorf("%w: 缺少 login_hint", ErrInvalidLTIRequest)
	}
	if !sameOrigin(targetLinkURI, platform.ToolLaunchURL) {
		return "", fmt.Errorf("%w: target_link_uri 不屬於已註冊工具來源", ErrInvalidLTIRequest)
	}

	state := uuid.NewString()
	nonce := uuid.NewString()
	now := time.Now()
	stateRecord := models.LTIOIDCState{
		State: state, Nonce: nonce, Issuer: platform.Issuer, ClientID: platform.ClientID,
		TargetLinkURI: targetLinkURI, LTIMessageHint: messageHint, ExpiresAt: now.Add(5 * time.Minute), CreatedAt: now,
	}
	if err := s.db.Create(&stateRecord).Error; err != nil {
		return "", err
	}
	_ = s.db.Where("expires_at < ?", now).Delete(&models.LTIOIDCState{}).Error

	redirectURL, _ := url.Parse(platform.AuthLoginURL)
	query := redirectURL.Query()
	query.Set("response_type", "id_token")
	query.Set("response_mode", "form_post")
	query.Set("scope", "openid")
	query.Set("client_id", platform.ClientID)
	query.Set("redirect_uri", platform.ToolLaunchURL)
	query.Set("login_hint", loginHint)
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("prompt", "none")
	if messageHint != "" {
		query.Set("lti_message_hint", messageHint)
	}
	redirectURL.RawQuery = query.Encode()
	return redirectURL.String(), nil
}

func (s *ltiService) HandleLaunch(idToken, state string) (*models.TestSession, error) {
	var oidcState models.LTIOIDCState
	now := time.Now()
	if err := s.db.Where("state = ? AND used_at IS NULL AND expires_at > ?", state, now).First(&oidcState).Error; err != nil {
		return nil, ErrInvalidLTIState
	}
	var platform models.LTIPlatform
	if err := s.db.Where("issuer = ? AND client_id = ?", oidcState.Issuer, oidcState.ClientID).First(&platform).Error; err != nil {
		return nil, ErrPlatformNotFound
	}

	kid, err := tokenKeyID(idToken)
	if err != nil {
		return nil, ErrInvalidLTIToken
	}
	key, err := s.signingKey(context.Background(), &platform, kid)
	if err != nil {
		s.logger.Warn("LTI JWKS 取得或解析失敗", zap.Error(err))
		return nil, ErrInvalidLTIToken
	}
	claims := &ltiClaims{}
	token, err := jwt.ParseWithClaims(idToken, claims, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodRS256 || token.Header["kid"] != kid {
			return nil, ErrInvalidLTIToken
		}
		return key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}), jwt.WithIssuer(platform.Issuer),
		jwt.WithAudience(platform.ClientID), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(30*time.Second))
	if err != nil || !token.Valid || claims.Subject == "" || claims.IssuedAt == nil || claims.Nonce != oidcState.Nonce ||
		claims.MessageType != ltiMessageType || claims.Version != ltiVersion || claims.DeploymentID == "" ||
		claims.ResourceLink.ID == "" || claims.TargetLinkURI != oidcState.TargetLinkURI || len(claims.Roles) == 0 {
		return nil, ErrInvalidLTIToken
	}

	var mapping models.LTIResourceLink
	if err := s.db.Where("platform_id = ? AND deployment_id = ? AND resource_link_id = ?", platform.ID,
		claims.DeploymentID, claims.ResourceLink.ID).First(&mapping).Error; err != nil {
		return nil, ErrResourceLinkMissing
	}
	if claims.AGS.LineItem != "" {
		if !containsString(claims.AGS.Scopes, ltiScoreScope) {
			return nil, ErrInvalidLTIToken
		}
		allowed := hostnames(platform.Issuer, platform.AuthTokenURL)
		policy, policyErr := security.NewOutboundPolicy(allowed)
		if policyErr != nil {
			return nil, ErrInvalidLTIToken
		}
		if _, policyErr = policy.ValidateURL(claims.AGS.LineItem); policyErr != nil {
			return nil, ErrInvalidLTIToken
		}
	}

	result := s.db.Model(&models.LTIOIDCState{}).
		Where("state = ? AND used_at IS NULL AND expires_at > ?", state, now).
		Update("used_at", now)
	if result.Error != nil || result.RowsAffected != 1 {
		return nil, ErrInvalidLTIState
	}

	userID := ltiUserID(platform.Issuer, claims.Subject)
	session, err := s.sessionService.StartSession(mapping.DeliveryID, userID)
	if err != nil {
		return nil, err
	}
	link := models.LTILinkSession{
		SessionID: session.ID, PlatformID: platform.ID, Issuer: platform.Issuer, ClientID: platform.ClientID,
		LISUserID: claims.Subject, DeploymentID: claims.DeploymentID, ResourceLinkID: claims.ResourceLink.ID,
		LineItemURL: claims.AGS.LineItem, CreatedAt: time.Now(),
	}
	if err := s.db.Save(&link).Error; err != nil {
		return nil, err
	}
	return session, nil
}

func (s *ltiService) SubmitGradeToLMS(session *models.TestSession) error {
	var link models.LTILinkSession
	if err := s.db.First(&link, "session_id = ?", session.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if link.LineItemURL == "" {
		return nil
	}
	var platform models.LTIPlatform
	if err := s.db.First(&platform, "id = ?", link.PlatformID).Error; err != nil {
		return err
	}
	privateKeyPEM, err := s.cipher.Decrypt(platform.PrivateKey)
	if err != nil {
		return err
	}
	privateKey, err := parseRSAPrivateKey(privateKeyPEM)
	if err != nil {
		return err
	}
	accessToken, err := s.fetchAGSAccessToken(&platform, privateKey)
	if err != nil {
		return err
	}
	maximum, err := s.deliveryMaximumScore(session.DeliveryID)
	if err != nil {
		return err
	}
	payload := models.AGSGradePayload{
		Timestamp: time.Now().UTC().Format(time.RFC3339), ScoreGiven: session.TotalScore, ScoreMaximum: maximum,
		Comment: "TAO Core Go 自動計分完成", ActivityProgress: "Completed", GradingProgress: "FullyGraded", UserID: link.LISUserID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	scoreURL := strings.TrimRight(link.LineItemURL, "/") + "/scores"
	policy, client, err := safeClient(scoreURL, hostnames(platform.Issuer, platform.AuthTokenURL))
	if err != nil {
		return err
	}
	validated, err := policy.ValidateURL(scoreURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, validated.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/vnd.ims.lis.v1.score+json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("LTI AGS 回傳失敗: HTTP %d", resp.StatusCode)
	}
	s.logger.Info("LTI AGS 成績回寫成功", zap.String("session_id", session.ID), zap.Int("status_code", resp.StatusCode))
	return nil
}

func (s *ltiService) fetchAGSAccessToken(platform *models.LTIPlatform, key *rsa.PrivateKey) (string, error) {
	now := time.Now()
	assertionClaims := jwt.RegisteredClaims{
		Issuer: platform.ClientID, Subject: platform.ClientID, Audience: jwt.ClaimStrings{platform.AuthTokenURL},
		ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)), IssuedAt: jwt.NewNumericDate(now), ID: uuid.NewString(),
	}
	assertion := jwt.NewWithClaims(jwt.SigningMethodRS256, assertionClaims)
	assertion.Header["kid"] = platform.KeyID
	signed, err := assertion.SignedString(key)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {signed},
		"scope":                 {ltiScoreScope},
	}
	policy, client, err := safeClient(platform.AuthTokenURL, hostnames(platform.AuthTokenURL))
	if err != nil {
		return "", err
	}
	validated, err := policy.ValidateURL(platform.AuthTokenURL)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, validated.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("LTI OAuth token 取得失敗: HTTP %d", resp.StatusCode)
	}
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&tokenResponse); err != nil || tokenResponse.AccessToken == "" {
		return "", errors.New("LTI OAuth token 回應無效")
	}
	return tokenResponse.AccessToken, nil
}

func (s *ltiService) signingKey(ctx context.Context, platform *models.LTIPlatform, kid string) (*rsa.PublicKey, error) {
	s.cacheMu.RLock()
	cached, ok := s.jwksCache[platform.KeySetURL]
	s.cacheMu.RUnlock()
	if ok && time.Now().Before(cached.expiresAt) {
		if key := cached.keys[kid]; key != nil {
			return key, nil
		}
		return nil, errors.New("JWKS 找不到指定 kid")
	}
	policy, client, err := safeClient(platform.KeySetURL, hostnames(platform.KeySetURL))
	if err != nil {
		return nil, err
	}
	validated, err := policy.ValidateURL(platform.KeySetURL)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, validated.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS HTTP %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	setBody, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(setBody) > 1<<20 {
		return nil, errors.New("JWKS 回應超過大小限制")
	}
	if err := json.Unmarshal(setBody, &set); err != nil {
		return nil, err
	}
	if len(set.Keys) > 100 {
		return nil, errors.New("JWKS key 數量超過限制")
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, jwk := range set.Keys {
		if jwk.Kty != "RSA" || jwk.Kid == "" || (jwk.Alg != "" && jwk.Alg != "RS256") || (jwk.Use != "" && jwk.Use != "sig") {
			continue
		}
		key, err := rsaKey(jwk.N, jwk.E)
		if err == nil {
			keys[jwk.Kid] = key
		}
	}
	s.cacheMu.Lock()
	s.jwksCache[platform.KeySetURL] = cachedJWKSet{keys: keys, expiresAt: time.Now().Add(15 * time.Minute)}
	s.cacheMu.Unlock()
	if key := keys[kid]; key != nil {
		return key, nil
	}
	return nil, errors.New("JWKS 找不到指定 kid")
}

func tokenKeyID(raw string) (string, error) {
	token, _, err := jwt.NewParser(jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()})).ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		return "", err
	}
	kid, _ := token.Header["kid"].(string)
	if kid == "" || token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
		return "", errors.New("id_token 缺少有效 kid/alg")
	}
	return kid, nil
}

func rsaKey(encodedN, encodedE string) (*rsa.PublicKey, error) {
	if len(encodedN) > 1368 {
		return nil, errors.New("JWK modulus 超過 8192 bits")
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(encodedN)
	if err != nil || len(nBytes) == 0 {
		return nil, errors.New("JWK modulus 無效")
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(encodedE)
	if err != nil || len(eBytes) == 0 || len(eBytes) > 4 {
		return nil, errors.New("JWK exponent 無效")
	}
	var exponent int
	for _, value := range eBytes {
		exponent = exponent<<8 + int(value)
	}
	modulus := new(big.Int).SetBytes(nBytes)
	if modulus.BitLen() < 2048 || modulus.BitLen() > 8192 || exponent < 3 || exponent%2 == 0 {
		return nil, errors.New("JWK exponent 無效")
	}
	return &rsa.PublicKey{N: modulus, E: exponent}, nil
}

func parseRSAPrivateKey(value string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, errors.New("LTI private key PEM 無效")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("LTI private key 無法解析")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("LTI private key 必須是 RSA")
	}
	return key, nil
}

func (s *ltiService) deliveryMaximumScore(deliveryID string) (float64, error) {
	var result struct{ Total float64 }
	err := s.db.Model(&models.TestItem{}).
		Select("COALESCE(SUM(items.max_score * test_items.weight), 0) AS total").
		Joins("JOIN items ON items.id = test_items.item_id").
		Joins("JOIN test_sections ON test_sections.id = test_items.section_id").
		Joins("JOIN deliveries ON deliveries.test_id = test_sections.test_id").
		Where("deliveries.id = ?", deliveryID).Scan(&result).Error
	if err != nil {
		return 0, err
	}
	if result.Total <= 0 {
		return 0, errors.New("測驗最高分設定無效")
	}
	return result.Total, nil
}

func safeClient(rawURL string, allowedHosts []string) (*security.OutboundPolicy, *http.Client, error) {
	policy, err := security.NewOutboundPolicy(allowedHosts)
	if err != nil {
		return nil, nil, err
	}
	if _, err := policy.ValidateURL(rawURL); err != nil {
		return nil, nil, err
	}
	return policy, policy.HTTPClient(10 * time.Second), nil
}

func parseHTTPSURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("LTI URL 必須是有效且不含使用者資訊的 HTTPS URL")
	}
	return parsed, nil
}

func sameOrigin(first, second string) bool {
	a, errA := parseHTTPSURL(first)
	b, errB := parseHTTPSURL(second)
	return errA == nil && errB == nil && strings.EqualFold(a.Host, b.Host)
}

func hostnames(rawURLs ...string) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, raw := range rawURLs {
		if parsed, err := parseHTTPSURL(raw); err == nil {
			host := strings.ToLower(parsed.Hostname())
			if _, exists := seen[host]; !exists {
				seen[host] = struct{}{}
				result = append(result, host)
			}
		}
	}
	return result
}

func ltiUserID(issuer, subject string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + subject))
	return "lti:" + base64.RawURLEncoding.EncodeToString(sum[:])
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
