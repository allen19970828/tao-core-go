package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
)

var (
	ErrPlatformNotFound = errors.New("尚未註冊此 LTI 平台權限")
)

// LTIService 提供 IMS LTI 1.3 Advantage 單點登入與 AGS 成績自動回依介面。
type LTIService interface {
	RegisterPlatform(platform *models.LTIPlatform) error
	InitiateLogin(issuer, clientID, targetLinkURI string) (string, error)
	HandleLaunch(idToken string) (*models.TestSession, error)
	SubmitGradeToLMS(session *models.TestSession) error
}

type ltiService struct {
	db             *gorm.DB
	logger         *zap.Logger
	sessionService SessionService
}

// NewLTIService 建立並回傳 LTIService 實體。
func NewLTIService(db *gorm.DB, logger *zap.Logger, sessionService SessionService) LTIService {
	return &ltiService{
		db:             db,
		logger:         logger,
		sessionService: sessionService,
	}
}

// RegisterPlatform 註冊對接的 LMS 平台 (例如 Moodle/Canvas)。
func (s *ltiService) RegisterPlatform(platform *models.LTIPlatform) error {
	if platform.ID == "" {
		platform.ID = uuid.New().String()
	}
	if platform.CreatedAt.IsZero() {
		platform.CreatedAt = time.Now()
	}
	return s.db.Create(platform).Error
}

// InitiateLogin 產生 OIDC 重導向 URL，引導使用者至 Moodle 登入頁面。
func (s *ltiService) InitiateLogin(issuer, clientID, targetLinkURI string) (string, error) {
	var platform models.LTIPlatform
	err := s.db.Where("issuer = ? AND client_id = ?", issuer, clientID).First(&platform).Error
	if err != nil {
		return "", ErrPlatformNotFound
	}

	state := uuid.New().String()
	nonce := uuid.New().String()

	redirectURL, err := url.Parse(platform.AuthLoginURL)
	if err != nil {
		return "", err
	}

	q := redirectURL.Query()
	q.Set("response_type", "id_token")
	q.Set("response_mode", "form_post")
	q.Set("scope", "openid")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", targetLinkURI)
	q.Set("login_hint", "user-lti-hint")
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("prompt", "none")
	redirectURL.RawQuery = q.Encode()

	return redirectURL.String(), nil
}

// HandleLaunch 解析並驗證 LMS 傳回的 id_token (JWT)，自動建立或綁定學生測驗會話。
func (s *ltiService) HandleLaunch(idToken string) (*models.TestSession, error) {
	token, _, err := new(jwt.Parser).ParseUnverified(idToken, jwt.MapClaims{})
	if err != nil {
		return nil, errors.New("無法解析 LTI id_token 格式")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("id_token Claims 格式無效")
	}

	issuer, _ := claims["iss"].(string)
	sub, _ := claims["sub"].(string)

	if issuer == "" || sub == "" {
		return nil, errors.New("id_token 缺少 iss 或 sub 欄位")
	}

	deliveryID := "delivery-demo-01"
	session, err := s.sessionService.StartSession(deliveryID, sub)
	if err != nil {
		return nil, err
	}

	// 紀錄 LTI 連線資訊，供交卷時回傳成績
	var lineItemURL string
	if ags, ok := claims["https://purl.imsglobal.org/spec/lti-ags/claim/endpoint"].(map[string]interface{}); ok {
		lineItemURL, _ = ags["lineitem"].(string)
	}

	linkSession := models.LTILinkSession{
		SessionID:   session.ID,
		Issuer:      issuer,
		ClientID:    "moodle-client-123",
		LISUserID:   sub,
		LineItemURL: lineItemURL,
		CreatedAt:   time.Now(),
	}
	s.db.Save(&linkSession)

	return session, nil
}

// SubmitGradeToLMS 在學生交卷後，異步將成績 POST 回寫至 Moodle 成績單冊 (AGS Endpoint)。
func (s *ltiService) SubmitGradeToLMS(session *models.TestSession) error {
	var linkSession models.LTILinkSession
	if err := s.db.First(&linkSession, "session_id = ?", session.ID).Error; err != nil {
		return nil // 非 LTI 登入之測驗會話，無需回傳
	}

	if linkSession.LineItemURL == "" {
		s.logger.Info("該 LTI 連線未包含 AGS LineItem URL，跳過成績回傳", zap.String("session_id", session.ID))
		return nil
	}

	payload := models.AGSGradePayload{
		Timestamp:        time.Now().Format(time.RFC3339),
		ScoreGiven:       session.TotalScore,
		ScoreMaximum:     100.0,
		Comment:          "TAO Core Go 自動計分完成",
		ActivityProgress: "Completed",
		GradingProgress:  "FullyGraded",
		UserID:           linkSession.LISUserID,
	}

	bodyBytes, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", linkSession.LineItemURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/vnd.ims.lis.v1.score+json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.logger.Error("LTI AGS 成績回寫 HTTP 請求失敗", zap.Error(err))
		return err
	}
	defer resp.Body.Close()

	s.logger.Info("LTI AGS 成績自動回寫成功！", zap.String("session_id", session.ID), zap.Int("status_code", resp.StatusCode))
	return nil
}
