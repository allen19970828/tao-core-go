package handler

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"tao-core-go/internal/domain/models"
	"tao-core-go/internal/middleware"
	"tao-core-go/internal/service"
)

type sessionStub struct {
	userID string
	err    error
}

func (stub *sessionStub) StartSession(deliveryID, userID string) (*models.TestSession, error) {
	stub.userID = userID
	return &models.TestSession{ID: "session-1", DeliveryID: deliveryID, UserID: userID, Status: models.SessionStatusInProgress}, stub.err
}
func (stub *sessionStub) SaveResponse(sessionID, userID, itemID, response string) (*models.ItemResponse, error) {
	stub.userID = userID
	return &models.ItemResponse{ID: "response-1", SessionID: sessionID, ItemID: itemID, ResponseData: response}, stub.err
}
func (stub *sessionStub) SubmitSession(sessionID, userID string) (*models.TestSession, error) {
	stub.userID = userID
	return &models.TestSession{ID: sessionID, UserID: userID, Status: models.SessionStatusCompleted}, stub.err
}
func (stub *sessionStub) GetSession(sessionID, userID string) (*models.TestSession, error) {
	stub.userID = userID
	return &models.TestSession{ID: sessionID, UserID: userID, Status: models.SessionStatusInProgress}, stub.err
}

type webhookStub struct{ err error }

func (*webhookStub) Dispatch(string, interface{}) {}
func (stub *webhookStub) RegisterConfig(config *models.WebhookConfig) (string, error) {
	config.ID = "webhook-1"
	return "0123456789abcdef0123456789abcdef", stub.err
}
func (stub *webhookStub) GetWebhookLogs() ([]models.WebhookLog, error) {
	return []models.WebhookLog{{ID: "log-1", IsSuccess: true}}, stub.err
}

func TestSessionHandlerValidationAndServiceErrors(t *testing.T) {
	session := &sessionStub{err: service.ErrSessionNotFound}
	webhook := &webhookStub{err: errors.New("webhook failure")}
	handler := NewSessionHandler(session, webhook)
	router := authenticatedRouter()
	router.POST("/start", handler.StartSession)
	router.POST("/sessions/:id/response", handler.SaveResponse)
	router.POST("/sessions/:id/submit", handler.SubmitSession)
	router.GET("/sessions/:id", handler.GetSession)
	router.POST("/webhooks", handler.RegisterWebhook)
	router.GET("/webhooks", handler.GetWebhookLogs)

	tests := []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, "/start", `{`, http.StatusBadRequest},
		{http.MethodPost, "/start", `{"delivery_id":"delivery"}`, http.StatusNotFound},
		{http.MethodPost, "/sessions/one/response", `{`, http.StatusBadRequest},
		{http.MethodPost, "/sessions/one/response", `{"item_id":"item","response_data":"A"}`, http.StatusNotFound},
		{http.MethodPost, "/sessions/one/submit", ``, http.StatusNotFound},
		{http.MethodGet, "/sessions/one", ``, http.StatusNotFound},
		{http.MethodPost, "/webhooks", `{`, http.StatusBadRequest},
		{http.MethodPost, "/webhooks", `{"event":"session.completed","url":"https://hooks.example.com"}`, http.StatusBadRequest},
		{http.MethodGet, "/webhooks", ``, http.StatusInternalServerError},
	}
	for _, testCase := range tests {
		request := httptest.NewRequest(testCase.method, testCase.path, strings.NewReader(testCase.body))
		if testCase.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != testCase.status {
			t.Fatalf("%s %s: expected %d, got %d", testCase.method, testCase.path, testCase.status, response.Code)
		}
	}
}

func authenticatedRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(context *gin.Context) {
		context.Set("user_id", "verified-user")
		context.Next()
	})
	return router
}

func TestSessionHandlersUseAuthenticatedIdentity(t *testing.T) {
	stub := &sessionStub{}
	handler := NewSessionHandler(stub, &webhookStub{})
	router := authenticatedRouter()
	router.POST("/start", handler.StartSession)
	router.POST("/sessions/:id/response", handler.SaveResponse)
	router.POST("/sessions/:id/submit", handler.SubmitSession)
	router.GET("/sessions/:id", handler.GetSession)
	router.POST("/webhooks", handler.RegisterWebhook)
	router.GET("/webhooks", handler.GetWebhookLogs)

	tests := []struct {
		method, path, body string
		wantStatus         int
	}{
		{http.MethodPost, "/start", `{"delivery_id":"delivery-1","user_id":"attacker"}`, http.StatusOK},
		{http.MethodPost, "/sessions/session-1/response", `{"item_id":"item-1","response_data":"A"}`, http.StatusOK},
		{http.MethodPost, "/sessions/session-1/submit", ``, http.StatusOK},
		{http.MethodGet, "/sessions/session-1", ``, http.StatusOK},
		{http.MethodPost, "/webhooks", `{"event":"session.completed","url":"https://hooks.example.com","signing_secret":"0123456789abcdef0123456789abcdef"}`, http.StatusCreated},
		{http.MethodGet, "/webhooks", ``, http.StatusOK},
	}
	for _, testCase := range tests {
		request := httptest.NewRequest(testCase.method, testCase.path, strings.NewReader(testCase.body))
		if testCase.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != testCase.wantStatus {
			t.Fatalf("%s %s: expected %d, got %d (%s)", testCase.method, testCase.path, testCase.wantStatus, response.Code, response.Body.String())
		}
	}
	if stub.userID != "verified-user" {
		t.Fatalf("handler trusted request identity instead of JWT context: %q", stub.userID)
	}
}

func TestSessionHandlersRejectMissingIdentityAndMapDomainErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/start", NewSessionHandler(&sessionStub{}, nil).StartSession)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/start", strings.NewReader(`{"delivery_id":"delivery-1"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing identity to return 401, got %d", response.Code)
	}

	for expectedStatus, domainErrors := range map[int][]error{
		http.StatusNotFound:            {service.ErrSessionNotFound, service.ErrDeliveryNotFound},
		http.StatusForbidden:           {service.ErrDeliveryForbidden},
		http.StatusConflict:            {service.ErrDeliveryClosed, service.ErrMaxAttempts, service.ErrSessionCompleted, service.ErrSessionNotActive, service.ErrSessionExpired},
		http.StatusBadRequest:          {service.ErrItemNotInDelivery},
		http.StatusInternalServerError: {errors.New("database unavailable")},
	} {
		for _, domainError := range domainErrors {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			writeSessionError(context, domainError)
			if recorder.Code != expectedStatus {
				t.Fatalf("error %v: expected %d, got %d", domainError, expectedStatus, recorder.Code)
			}
		}
	}
}

type proctorStub struct {
	recordedUser string
	err          error
}

func (stub *proctorStub) RecordEvent(_ *models.ProctorEvent, userID string) error {
	stub.recordedUser = userID
	return stub.err
}
func (stub *proctorStub) GetSessionProctorLog(string) ([]models.ProctorEvent, error) {
	return []models.ProctorEvent{{ID: "event-1", EventType: models.ProctorEventTabSwitch}}, stub.err
}
func (stub *proctorStub) GetProctorAnalytics(sessionID string) (*models.ProctorAnalyticsSummary, error) {
	return &models.ProctorAnalyticsSummary{SessionID: sessionID, RiskLevel: "LOW"}, stub.err
}

func TestProctorHandlerValidationAndErrors(t *testing.T) {
	stub := &proctorStub{err: service.ErrSessionNotActive}
	handler := NewProctorHandler(stub)
	router := authenticatedRouter()
	router.POST("/sessions/:id/event", handler.RecordEvent)
	router.GET("/sessions/:id/log", handler.GetProctorLog)
	router.GET("/sessions/:id/analytics", handler.GetProctorAnalytics)
	for _, testCase := range []struct{ method, path, body string }{
		{http.MethodPost, "/sessions/one/event", `{`},
		{http.MethodPost, "/sessions/one/event", `{"event_type":"TAB_SWITCH"}`},
		{http.MethodGet, "/sessions/one/log", ""},
		{http.MethodGet, "/sessions/one/analytics", ""},
	} {
		request := httptest.NewRequest(testCase.method, testCase.path, strings.NewReader(testCase.body))
		if testCase.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code < 400 {
			t.Fatalf("expected %s %s to fail, got %d", testCase.method, testCase.path, response.Code)
		}
	}
}

func TestProctorHandlers(t *testing.T) {
	stub := &proctorStub{}
	handler := NewProctorHandler(stub)
	router := authenticatedRouter()
	router.POST("/sessions/:id/event", handler.RecordEvent)
	router.GET("/sessions/:id/log", handler.GetProctorLog)
	router.GET("/sessions/:id/analytics", handler.GetProctorAnalytics)
	for _, testCase := range []struct{ method, path, body string }{
		{http.MethodPost, "/sessions/session-1/event", `{"event_type":"TAB_SWITCH","duration_seconds":2}`},
		{http.MethodGet, "/sessions/session-1/log", ""},
		{http.MethodGet, "/sessions/session-1/analytics", ""},
	} {
		request := httptest.NewRequest(testCase.method, testCase.path, strings.NewReader(testCase.body))
		if testCase.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code < 200 || response.Code >= 300 {
			t.Fatalf("%s %s failed: %d %s", testCase.method, testCase.path, response.Code, response.Body.String())
		}
	}
	if stub.recordedUser != "verified-user" {
		t.Fatalf("proctor handler used wrong identity: %q", stub.recordedUser)
	}
}

type ltiStub struct{ err error }

const validPlatformJSON = `{"issuer":"https://lms.example.com","client_id":"client","keyset_url":"https://lms.example.com/jwks","auth_token_url":"https://lms.example.com/token","auth_login_url":"https://lms.example.com/authorize","tool_launch_url":"https://tool.example.com/api/v1/lti/launch"}`

func (stub *ltiStub) RegisterPlatform(*models.LTIPlatform) error         { return stub.err }
func (stub *ltiStub) RegisterResourceLink(*models.LTIResourceLink) error { return stub.err }
func (stub *ltiStub) InitiateLogin(string, string, string, string, string) (string, error) {
	return "https://lms.example.com/authorize", stub.err
}
func (stub *ltiStub) HandleLaunch(string, string) (*models.TestSession, error) {
	return &models.TestSession{ID: "session-1", UserID: "lti-user", Status: models.SessionStatusInProgress}, stub.err
}

func TestLTIHandlerValidationAndErrors(t *testing.T) {
	jwtConfig := middleware.JWTConfig{Secret: "handler-test-secret-with-at-least-32-bytes", Issuer: "tool", Audience: "exam"}
	stub := &ltiStub{err: service.ErrInvalidLTIToken}
	handler := NewLTIHandler(stub, jwtConfig, time.Hour)
	router := gin.New()
	router.POST("/platforms", handler.RegisterPlatform)
	router.POST("/links", handler.RegisterResourceLink)
	router.GET("/login", handler.InitiateLogin)
	router.POST("/login", handler.InitiateLogin)
	router.POST("/launch", handler.HandleLaunch)
	tests := []struct {
		method, path, body, contentType string
		status                          int
	}{
		{http.MethodPost, "/platforms", `{`, "application/json", http.StatusBadRequest},
		{http.MethodPost, "/platforms", `{}`, "application/json", http.StatusBadRequest},
		{http.MethodPost, "/platforms", validPlatformJSON, "application/json", http.StatusInternalServerError},
		{http.MethodPost, "/links", `{`, "application/json", http.StatusBadRequest},
		{http.MethodPost, "/links", `{}`, "application/json", http.StatusBadRequest},
		{http.MethodGet, "/login", "", "", http.StatusBadRequest},
		{http.MethodGet, "/login?iss=lms&client_id=client", "", "", http.StatusInternalServerError},
		{http.MethodPost, "/login", url.Values{"iss": {"lms"}, "client_id": {"client"}}.Encode(), "application/x-www-form-urlencoded", http.StatusInternalServerError},
		{http.MethodPost, "/launch", "", "application/x-www-form-urlencoded", http.StatusBadRequest},
		{http.MethodPost, "/launch", url.Values{"id_token": {"token"}, "state": {"state"}}.Encode(), "application/x-www-form-urlencoded", http.StatusUnauthorized},
		{http.MethodPost, "/launch", "id_token=" + strings.Repeat("x", maxLTIFormSize) + "&state=state", "application/x-www-form-urlencoded", http.StatusRequestEntityTooLarge},
	}
	for _, testCase := range tests {
		request := httptest.NewRequest(testCase.method, testCase.path, strings.NewReader(testCase.body))
		if testCase.contentType != "" {
			request.Header.Set("Content-Type", testCase.contentType)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != testCase.status {
			t.Fatalf("%s %s: expected %d, got %d", testCase.method, testCase.path, testCase.status, response.Code)
		}
	}

	stub.err = service.ErrResourceLinkMissing
	request := httptest.NewRequest(http.MethodPost, "/launch", strings.NewReader(url.Values{"id_token": {"token"}, "state": {"state"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected missing mapping to return 403, got %d", response.Code)
	}

	invalidTokenHandler := NewLTIHandler(&ltiStub{}, middleware.JWTConfig{}, time.Hour)
	invalidRouter := gin.New()
	invalidRouter.POST("/launch", invalidTokenHandler.HandleLaunch)
	request = httptest.NewRequest(http.MethodPost, "/launch", strings.NewReader(url.Values{"id_token": {"token"}, "state": {"state"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response = httptest.NewRecorder()
	invalidRouter.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected token issuance failure to return 500, got %d", response.Code)
	}
}
func (*ltiStub) SubmitGradeToLMS(*models.TestSession) error { return nil }

func TestLTIHandlers(t *testing.T) {
	jwtConfig := middleware.JWTConfig{Secret: "handler-test-secret-with-at-least-32-bytes", Issuer: "tool", Audience: "exam"}
	handler := NewLTIHandler(&ltiStub{}, jwtConfig, time.Hour)
	router := gin.New()
	router.POST("/platforms", handler.RegisterPlatform)
	router.POST("/links", handler.RegisterResourceLink)
	router.GET("/login", handler.InitiateLogin)
	router.POST("/launch", handler.HandleLaunch)

	tests := []struct {
		method, path, body, contentType string
		status                          int
	}{
		{http.MethodPost, "/platforms", validPlatformJSON, "application/json", http.StatusCreated},
		{http.MethodPost, "/links", `{"platform_id":"one","deployment_id":"two","resource_link_id":"three","delivery_id":"four"}`, "application/json", http.StatusCreated},
		{http.MethodGet, "/login?iss=https%3A%2F%2Flms.example.com&client_id=client&target_link_uri=https%3A%2F%2Ftool.example.com&login_hint=hint", "", "", http.StatusFound},
		{http.MethodPost, "/launch", url.Values{"id_token": {"signed"}, "state": {"state"}}.Encode(), "application/x-www-form-urlencoded", http.StatusOK},
	}
	for _, testCase := range tests {
		request := httptest.NewRequest(testCase.method, testCase.path, strings.NewReader(testCase.body))
		if testCase.contentType != "" {
			request.Header.Set("Content-Type", testCase.contentType)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != testCase.status {
			t.Fatalf("%s %s: expected %d, got %d (%s)", testCase.method, testCase.path, testCase.status, response.Code, response.Body.String())
		}
		if testCase.path == "/launch" && !strings.Contains(response.Body.String(), "access_token") {
			t.Fatal("expected verified LTI launch to issue a scoped access token")
		}
	}
}

type qtiStub struct{ err error }

func (stub *qtiStub) ImportQTIPackage(path, _ string) ([]*models.Item, error) {
	if path == "" {
		return nil, errors.New("missing temp file")
	}
	return []*models.Item{{ID: "item-1", Title: "Imported", CorrectAnswer: "A"}}, stub.err
}

func TestQTIHandlerRejectsInvalidUploadsAndServiceErrors(t *testing.T) {
	for name, testCase := range map[string]struct {
		filename string
		stub     *qtiStub
		expected int
	}{
		"wrong extension": {"package.txt", &qtiStub{}, http.StatusBadRequest},
		"service failure": {"package.zip", &qtiStub{err: errors.New("bad package")}, http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			body := new(bytes.Buffer)
			writer := multipart.NewWriter(body)
			file, err := writer.CreateFormFile("file", testCase.filename)
			if err != nil {
				t.Fatalf("create upload: %v", err)
			}
			_, _ = file.Write([]byte("data"))
			_ = writer.Close()
			router := gin.New()
			router.POST("/import", NewQTIHandler(testCase.stub, t.TempDir()).ImportQTIPackage)
			request := httptest.NewRequest(http.MethodPost, "/import", body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != testCase.expected {
				t.Fatalf("expected %d, got %d", testCase.expected, response.Code)
			}
		})
	}
	router := gin.New()
	router.POST("/import", NewQTIHandler(&qtiStub{}, t.TempDir()).ImportQTIPackage)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/import", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected missing multipart upload to return 400, got %d", response.Code)
	}
}

func TestQTIHandlerAcceptsZIPAndHidesCorrectAnswer(t *testing.T) {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	file, err := writer.CreateFormFile("file", "package.zip")
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	_, _ = file.Write([]byte("test ZIP bytes"))
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	router := gin.New()
	router.POST("/import", NewQTIHandler(&qtiStub{}, t.TempDir()).ImportQTIPackage)
	request := httptest.NewRequest(http.MethodPost, "/import", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || strings.Contains(response.Body.String(), "correct_answer") {
		t.Fatalf("unexpected QTI response: %d %s", response.Code, response.Body.String())
	}
}

type exportStub struct{ err error }

func (stub *exportStub) ExportDeliveryResultsCSV(_ string, writer io.Writer) error {
	if stub.err != nil {
		return stub.err
	}
	_, err := io.WriteString(writer, "SessionID,UserID\n")
	return err
}

func TestResultsHandlerSanitizesDownloadFilename(t *testing.T) {
	if safeFilenamePart("bad\r\nX-Injected: yes") != "bad__X-Injected__yes" {
		t.Fatalf("unsafe filename normalization: %q", safeFilenamePart("bad\r\nX-Injected: yes"))
	}
	router := gin.New()
	router.GET("/deliveries/:id", NewResultsHandler(&exportStub{}).ExportResultsCSV)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/deliveries/delivery-1", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Disposition"), "delivery-1") {
		t.Fatalf("unexpected export response: %d %#v", response.Code, response.Header())
	}
}

func TestResultsHandlerServiceError(t *testing.T) {
	router := gin.New()
	router.GET("/deliveries/:id", NewResultsHandler(&exportStub{err: errors.New("export failed")}).ExportResultsCSV)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/deliveries/one", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected export failure to return 500, got %d", response.Code)
	}
}
