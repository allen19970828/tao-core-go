package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"tao-core-go/internal/domain/models"
	"tao-core-go/internal/service"
)

// LTIHandler 處理 IMS LTI 1.3 Advantage 單點登入與 Launch 的 HTTP 控制器。
type LTIHandler struct {
	ltiService service.LTIService
}

// NewLTIHandler 建立並回傳 LTIHandler 實體。
func NewLTIHandler(ltiService service.LTIService) *LTIHandler {
	return &LTIHandler{
		ltiService: ltiService,
	}
}

// RegisterPlatform 處理 POST /api/v1/lti/platforms
// 註冊外部 LMS 平台 (例如 Moodle/Canvas) 的 OAuth2 / OIDC 設定。
func (h *LTIHandler) RegisterPlatform(c *gin.Context) {
	var platform models.LTIPlatform
	if err := c.ShouldBindJSON(&platform); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
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
	issuer := c.Query("iss")
	clientID := c.Query("client_id")
	targetLinkURI := c.Query("target_link_uri")

	if issuer == "" {
		issuer = c.PostForm("iss")
		clientID = c.PostForm("client_id")
		targetLinkURI = c.PostForm("target_link_uri")
	}

	if issuer == "" || clientID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 LTI 登入必要參數: iss 或 client_id"})
		return
	}

	redirectURL, err := h.ltiService.InitiateLogin(issuer, clientID, targetLinkURI)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, redirectURL)
}

// HandleLaunch 處理 POST /api/v1/lti/launch
// 接收 Moodle 傳回的 id_token JWT，驗證學生身份並開啟測驗會話。
func (h *LTIHandler) HandleLaunch(c *gin.Context) {
	idToken := c.PostForm("id_token")
	if idToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 id_token 認證令牌"})
		return
	}

	session, err := h.ltiService.HandleLaunch(idToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "LTI 1.3 認證成功，測驗會話已建立",
		"session": session,
	})
}
