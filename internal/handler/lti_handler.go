package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"tao-core-go/internal/domain/models"
	"tao-core-go/internal/middleware"
	"tao-core-go/internal/service"
)

// LTIHandler 處理 IMS LTI 1.3 Advantage 單點登入與 Launch 的 HTTP 控制器。
type LTIHandler struct {
	ltiService service.LTIService
	jwtConfig  middleware.JWTConfig
	tokenTTL   time.Duration
}

const maxLTIFormSize = 64 << 10

type RegisterPlatformRequest struct {
	Issuer        string `json:"issuer" binding:"required,max=255"`
	ClientID      string `json:"client_id" binding:"required,max=255"`
	KeySetURL     string `json:"keyset_url" binding:"required,max=500"`
	AuthTokenURL  string `json:"auth_token_url" binding:"required,max=500"`
	AuthLoginURL  string `json:"auth_login_url" binding:"required,max=500"`
	ToolLaunchURL string `json:"tool_launch_url" binding:"required,max=500"`
	KeyID         string `json:"key_id" binding:"omitempty,max=255"`
	PrivateKey    string `json:"private_key" binding:"omitempty,max=16384"`
}

// NewLTIHandler 建立並回傳 LTIHandler 實體。
func NewLTIHandler(ltiService service.LTIService, jwtConfig middleware.JWTConfig, tokenTTL time.Duration) *LTIHandler {
	return &LTIHandler{
		ltiService: ltiService,
		jwtConfig:  jwtConfig,
		tokenTTL:   tokenTTL,
	}
}

func (h *LTIHandler) RegisterResourceLink(c *gin.Context) {
	var link models.LTIResourceLink
	if err := c.ShouldBindJSON(&link); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.ltiService.RegisterResourceLink(&link); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, link)
}

// RegisterPlatform 處理 POST /api/v1/lti/platforms
// 註冊外部 LMS 平台 (例如 Moodle/Canvas) 的 OAuth2 / OIDC 設定。
func (h *LTIHandler) RegisterPlatform(c *gin.Context) {
	var request RegisterPlatformRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	platform := models.LTIPlatform{
		Issuer: request.Issuer, ClientID: request.ClientID, KeySetURL: request.KeySetURL,
		AuthTokenURL: request.AuthTokenURL, AuthLoginURL: request.AuthLoginURL,
		ToolLaunchURL: request.ToolLaunchURL, KeyID: request.KeyID, PrivateKey: request.PrivateKey,
	}

	if err := h.ltiService.RegisterPlatform(&platform); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, platform)
}

// InitiateLogin 處理 GET/POST /api/v1/lti/login
// LTI 1.3 OIDC 認登發起：產生 state 與 nonce，並 302 重導向至 Moodle 進行使用者身份驗證。
func (h *LTIHandler) InitiateLogin(c *gin.Context) {
	if c.Request.Method == http.MethodPost && !parseLTIForm(c) {
		return
	}
	issuer := c.Query("iss")
	clientID := c.Query("client_id")
	targetLinkURI := c.Query("target_link_uri")
	loginHint := c.Query("login_hint")
	messageHint := c.Query("lti_message_hint")

	if issuer == "" {
		issuer = c.PostForm("iss")
		clientID = c.PostForm("client_id")
		targetLinkURI = c.PostForm("target_link_uri")
		loginHint = c.PostForm("login_hint")
		messageHint = c.PostForm("lti_message_hint")
	}

	if issuer == "" || clientID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 LTI 登入必要參數: iss 或 client_id"})
		return
	}

	redirectURL, err := h.ltiService.InitiateLogin(issuer, clientID, targetLinkURI, loginHint, messageHint)
	if err != nil {
		if errors.Is(err, service.ErrPlatformNotFound) || errors.Is(err, service.ErrInvalidLTIRequest) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "LTI 登入請求無效"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "無法發起 LTI 登入"})
		return
	}

	c.Redirect(http.StatusFound, redirectURL)
}

// HandleLaunch 處理 POST /api/v1/lti/launch
// 接收 Moodle 傳回的 id_token JWT，驗證學生身份並開啟測驗會話。
func (h *LTIHandler) HandleLaunch(c *gin.Context) {
	if !parseLTIForm(c) {
		return
	}
	idToken := c.PostForm("id_token")
	state := c.PostForm("state")
	if idToken == "" || state == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 id_token 或 state"})
		return
	}

	session, err := h.ltiService.HandleLaunch(idToken, state)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidLTIState), errors.Is(err, service.ErrInvalidLTIToken), errors.Is(err, service.ErrPlatformNotFound):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "LTI launch 驗證失敗"})
		case errors.Is(err, service.ErrResourceLinkMissing), errors.Is(err, service.ErrDeliveryForbidden):
			c.JSON(http.StatusForbidden, gin.H{"error": "LTI resource link 不允許開啟此測驗"})
		case errors.Is(err, service.ErrDeliveryClosed), errors.Is(err, service.ErrMaxAttempts):
			c.JSON(http.StatusConflict, gin.H{"error": "測驗目前不可開啟"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "無法建立 LTI 測驗會話"})
		}
		return
	}
	accessToken, err := middleware.GenerateJWT(session.UserID, []string{"STUDENT"}, h.jwtConfig, h.tokenTTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "無法簽發測驗存取令牌"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "LTI 1.3 認證成功，測驗會話已建立",
		"session":      session,
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   int(h.tokenTTL.Seconds()),
	})
}

func parseLTIForm(c *gin.Context) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxLTIFormSize)
	if err := c.Request.ParseForm(); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "LTI form body 過大"})
			return false
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "LTI form body 無效"})
		return false
	}
	return true
}
