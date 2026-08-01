package e2e_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"tao-core-go/internal/middleware"
)

const maxResponseBody = 2 << 20

type testEnvironment struct {
	baseURL string
	client  *http.Client
	jwt     middleware.JWTConfig
}

func loadEnvironment(t *testing.T) testEnvironment {
	t.Helper()
	rawBaseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("E2E_BASE_URL")), "/")
	if rawBaseURL == "" {
		t.Skip("E2E_BASE_URL is not set; process-level acceptance tests are opt-in")
	}
	parsed, err := url.Parse(rawBaseURL)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		t.Fatalf("E2E_BASE_URL must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		t.Fatalf("E2E_BASE_URL must not contain credentials, query parameters, or fragments")
	}
	if parsed.Scheme != "https" && !loopbackHost(parsed.Hostname()) && !envBool("E2E_ALLOW_HTTP") {
		t.Fatalf("plaintext E2E_BASE_URL is allowed only for loopback targets or when E2E_ALLOW_HTTP=true")
	}
	return testEnvironment{
		baseURL: rawBaseURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		jwt: middleware.JWTConfig{
			Secret:   os.Getenv("E2E_JWT_SECRET"),
			Issuer:   os.Getenv("E2E_JWT_ISSUER"),
			Audience: os.Getenv("E2E_JWT_AUDIENCE"),
		},
	}
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func envBool(name string) bool {
	value, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv(name)))
	return value
}

func (environment testEnvironment) token(t *testing.T, envName, userID string, roles []string) string {
	t.Helper()
	if supplied := strings.TrimSpace(os.Getenv(envName)); supplied != "" {
		return supplied
	}
	if err := middleware.ValidateJWTConfig(environment.jwt); err != nil {
		t.Fatalf("%s is unset and E2E JWT signing configuration is invalid: %v", envName, err)
	}
	token, err := middleware.GenerateJWT(userID, roles, environment.jwt, 15*time.Minute)
	if err != nil {
		t.Fatalf("generate %s: %v", envName, err)
	}
	return token
}

func (environment testEnvironment) request(t *testing.T, method, path, token, contentType string, body io.Reader) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, environment.baseURL+path, body)
	if err != nil {
		t.Fatalf("create %s %s request: %v", method, path, err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := environment.client.Do(request)
	if err != nil {
		t.Fatalf("execute %s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	if len(responseBody) > maxResponseBody {
		t.Fatalf("%s %s response exceeded %d bytes", method, path, maxResponseBody)
	}
	return response, responseBody
}

func assertStatus(t *testing.T, response *http.Response, body []byte, expected int) {
	t.Helper()
	if response.StatusCode != expected {
		t.Fatalf("expected HTTP %d, got %d: %s", expected, response.StatusCode, strings.TrimSpace(string(body)))
	}
}

func decodeObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatalf("decode JSON response: %v (%s)", err, string(body))
	}
	return object
}

func TestRuntimeReadinessAndSecurityBoundaries(t *testing.T) {
	environment := loadEnvironment(t)
	studentToken := environment.token(t, "E2E_STUDENT_TOKEN", "e2e-boundary-student", []string{"STUDENT"})
	adminToken := environment.token(t, "E2E_ADMIN_TOKEN", "e2e-boundary-admin", []string{"ADMIN"})

	response, body := environment.request(t, http.MethodGet, "/health", "", "", nil)
	assertStatus(t, response, body, http.StatusOK)
	if decodeObject(t, body)["status"] != "UP" {
		t.Fatalf("health endpoint did not report UP: %s", body)
	}
	for header, expected := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if response.Header.Get(header) != expected {
			t.Fatalf("%s: expected %q, got %q", header, expected, response.Header.Get(header))
		}
	}
	if !strings.Contains(response.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("health response is missing the expected CSP")
	}

	response, body = environment.request(t, http.MethodGet, "/ready", "", "", nil)
	assertStatus(t, response, body, http.StatusOK)
	if decodeObject(t, body)["status"] != "READY" {
		t.Fatalf("readiness endpoint did not report READY: %s", body)
	}

	response, body = environment.request(t, http.MethodGet, "/metrics", "", "", nil)
	assertStatus(t, response, body, http.StatusUnauthorized)
	response, body = environment.request(t, http.MethodGet, "/metrics", studentToken, "", nil)
	assertStatus(t, response, body, http.StatusForbidden)
	response, body = environment.request(t, http.MethodGet, "/metrics", adminToken, "", nil)
	assertStatus(t, response, body, http.StatusOK)

	response, body = environment.request(t, http.MethodGet, "/uploads/media/not-found.png", "", "", nil)
	assertStatus(t, response, body, http.StatusUnauthorized)
	response, body = environment.request(t, http.MethodGet, "/api/v1/lti/login", "", "", nil)
	assertStatus(t, response, body, http.StatusBadRequest)

	oversized := `{"delivery_id":"` + strings.Repeat("x", (1<<20)+1) + `"}`
	response, body = environment.request(t, http.MethodPost, "/api/v1/sessions/start", studentToken, "application/json", strings.NewReader(oversized))
	assertStatus(t, response, body, http.StatusRequestEntityTooLarge)

	response, body = environment.request(t, http.MethodPost, "/api/v1/webhooks/configs", adminToken, "application/json", strings.NewReader(
		`{"event":"session.completed","url":"https://127.0.0.1/callback","signing_secret":"0123456789abcdef0123456789abcdef"}`,
	))
	assertStatus(t, response, body, http.StatusBadRequest)

	zipBody, contentType := svgQTIPackage(t)
	response, body = environment.request(t, http.MethodPost, "/api/v1/items/import-qti", adminToken, contentType, zipBody)
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		t.Fatalf("QTI package containing SVG was accepted: %d %s", response.StatusCode, body)
	}
}

func svgQTIPackage(t *testing.T) (*bytes.Reader, string) {
	t.Helper()
	archive := new(bytes.Buffer)
	zipWriter := zip.NewWriter(archive)
	entry, err := zipWriter.Create("media/active.svg")
	if err != nil {
		t.Fatalf("create SVG ZIP entry: %v", err)
	}
	if _, err := io.WriteString(entry, `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`); err != nil {
		t.Fatalf("write SVG ZIP entry: %v", err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("close QTI ZIP: %v", err)
	}

	multipartBody := new(bytes.Buffer)
	multipartWriter := multipart.NewWriter(multipartBody)
	file, err := multipartWriter.CreateFormFile("file", "active-content.zip")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := file.Write(archive.Bytes()); err != nil {
		t.Fatalf("write multipart ZIP: %v", err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatalf("close multipart request: %v", err)
	}
	return bytes.NewReader(multipartBody.Bytes()), multipartWriter.FormDataContentType()
}

func TestFullAssessmentJourney(t *testing.T) {
	environment := loadEnvironment(t)
	deliveryID := strings.TrimSpace(os.Getenv("E2E_DELIVERY_ID"))
	itemID := strings.TrimSpace(os.Getenv("E2E_ITEM_ID"))
	answer := os.Getenv("E2E_ITEM_RESPONSE")
	if deliveryID == "" || itemID == "" || answer == "" {
		t.Skip("E2E_DELIVERY_ID, E2E_ITEM_ID, and E2E_ITEM_RESPONSE are required for the stateful assessment journey")
	}

	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	studentToken := environment.token(t, "E2E_STUDENT_TOKEN", "e2e-student-"+runID, []string{"STUDENT"})
	otherToken := environment.token(t, "E2E_OTHER_STUDENT_TOKEN", "e2e-other-"+runID, []string{"STUDENT"})
	adminToken := environment.token(t, "E2E_ADMIN_TOKEN", "e2e-admin-"+runID, []string{"ADMIN"})

	startPayload := fmt.Sprintf(`{"delivery_id":%q}`, deliveryID)
	response, body := environment.request(t, http.MethodPost, "/api/v1/sessions/start", studentToken, "application/json", strings.NewReader(startPayload))
	assertStatus(t, response, body, http.StatusOK)
	session := decodeObject(t, body)
	sessionID, _ := session["id"].(string)
	if sessionID == "" || session["status"] != "IN_PROGRESS" {
		t.Fatalf("start session returned an invalid session: %s", body)
	}

	response, repeatedStartBody := environment.request(t, http.MethodPost, "/api/v1/sessions/start", studentToken, "application/json", strings.NewReader(startPayload))
	assertStatus(t, response, repeatedStartBody, http.StatusOK)
	if decodeObject(t, repeatedStartBody)["id"] != sessionID {
		t.Fatalf("repeated start did not return the active session")
	}

	response, body = environment.request(t, http.MethodGet, "/api/v1/sessions/"+sessionID, otherToken, "", nil)
	assertStatus(t, response, body, http.StatusNotFound)
	response, body = environment.request(t, http.MethodPost, "/api/v1/sessions/"+sessionID+"/response", studentToken, "application/json", strings.NewReader(
		`{"item_id":"not-in-delivery","response_data":"A"}`,
	))
	assertStatus(t, response, body, http.StatusBadRequest)

	response, body = environment.request(t, http.MethodPost, "/api/v1/sessions/"+sessionID+"/proctor/event", studentToken, "application/json", strings.NewReader(
		`{"event_type":"TAB_SWITCH","duration_seconds":2,"details":"automated staging acceptance"}`,
	))
	assertStatus(t, response, body, http.StatusCreated)

	answerPayload, err := json.Marshal(map[string]string{"item_id": itemID, "response_data": answer})
	if err != nil {
		t.Fatalf("marshal answer payload: %v", err)
	}
	response, body = environment.request(t, http.MethodPost, "/api/v1/sessions/"+sessionID+"/response", studentToken, "application/json", bytes.NewReader(answerPayload))
	assertStatus(t, response, body, http.StatusOK)

	response, body = environment.request(t, http.MethodPost, "/api/v1/sessions/"+sessionID+"/submit", studentToken, "", nil)
	assertStatus(t, response, body, http.StatusOK)
	completed := decodeObject(t, body)
	if completed["status"] != "COMPLETED" {
		t.Fatalf("submitted session is not completed: %s", body)
	}
	if expected := strings.TrimSpace(os.Getenv("E2E_EXPECTED_SCORE")); expected != "" {
		want, err := strconv.ParseFloat(expected, 64)
		if err != nil {
			t.Fatalf("E2E_EXPECTED_SCORE is invalid: %v", err)
		}
		got, _ := completed["total_score"].(float64)
		if got != want {
			t.Fatalf("expected score %.2f, got %.2f", want, got)
		}
	}

	response, body = environment.request(t, http.MethodPost, "/api/v1/sessions/"+sessionID+"/submit", studentToken, "", nil)
	assertStatus(t, response, body, http.StatusOK)
	response, body = environment.request(t, http.MethodPost, "/api/v1/sessions/"+sessionID+"/response", studentToken, "application/json", bytes.NewReader(answerPayload))
	assertStatus(t, response, body, http.StatusConflict)

	response, body = environment.request(t, http.MethodGet, "/api/v1/sessions/"+sessionID+"/proctor/analytics", adminToken, "", nil)
	assertStatus(t, response, body, http.StatusOK)
	analytics := decodeObject(t, body)
	if analytics["session_id"] != sessionID {
		t.Fatalf("analytics returned the wrong session: %s", body)
	}

	response, body = environment.request(t, http.MethodGet, "/api/v1/deliveries/"+url.PathEscape(deliveryID)+"/results/csv", adminToken, "", nil)
	assertStatus(t, response, body, http.StatusOK)
	if !strings.Contains(response.Header.Get("Content-Type"), "text/csv") || !bytes.Contains(body, []byte(sessionID)) {
		t.Fatalf("CSV export did not include the completed session")
	}
}
