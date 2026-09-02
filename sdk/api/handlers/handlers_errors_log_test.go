package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func findAPIErrorResponseEntry(t *testing.T, hook *logtest.Hook) *log.Entry {
	t.Helper()
	for _, entry := range hook.AllEntries() {
		if strings.HasPrefix(entry.Message, "api error response:") {
			return entry
		}
	}
	return nil
}

func TestWriteErrorResponse_LogsReasonWithRequestIDAndModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hook := logtest.NewGlobal()
	defer hook.Reset()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", nil)
	logging.SetGinRequestID(c, "abcd1234")
	c.Set(requestedModelContextKey, "gpt-5.6-sol")

	handler := NewBaseAPIHandlers(nil, nil)
	handler.WriteErrorResponse(c, &interfaces.ErrorMessage{
		StatusCode: http.StatusBadRequest,
		Error:      errors.New("invalid signature in thinking block"),
	})

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	entry := findAPIErrorResponseEntry(t, hook)
	if entry == nil {
		t.Fatalf("expected an 'api error response' log entry, got %d entries", len(hook.AllEntries()))
	}
	if entry.Level != log.WarnLevel {
		t.Fatalf("level = %s, want warn", entry.Level)
	}
	if got := entry.Data["request_id"]; got != "abcd1234" {
		t.Fatalf("request_id = %v, want abcd1234", got)
	}
	for _, want := range []string{"status=400", "method=POST", "path=/v1/messages", "model=gpt-5.6-sol", "reason=invalid signature in thinking block"} {
		if !strings.Contains(entry.Message, want) {
			t.Fatalf("log message %q does not contain %q", entry.Message, want)
		}
	}
}

func TestWriteErrorResponse_DirectResponseLogsBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hook := logtest.NewGlobal()
	defer hook.Reset()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	handler := NewBaseAPIHandlers(nil, nil)
	handler.WriteErrorResponse(c, &interfaces.ErrorMessage{
		StatusCode:     http.StatusBadRequest,
		DirectResponse: true,
		Body:           []byte("{\n  \"error\": {\"type\": \"invalid_request_error\", \"message\": \"bad field\"}\n}"),
	})

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	entry := findAPIErrorResponseEntry(t, hook)
	if entry == nil {
		t.Fatalf("expected an 'api error response' log entry")
	}
	if !strings.Contains(entry.Message, `reason={ "error": {"type": "invalid_request_error", "message": "bad field"} }`) {
		t.Fatalf("log message %q should carry the whitespace-collapsed body", entry.Message)
	}
	if got := entry.Data["request_id"]; got != "--------" {
		t.Fatalf("request_id placeholder = %v, want --------", got)
	}
}

func TestLogErrorResponse_TruncatesAndSkipsNonErrorStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hook := logtest.NewGlobal()
	defer hook.Reset()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	logErrorResponse(c, http.StatusOK, "should not be logged")
	if entry := findAPIErrorResponseEntry(t, hook); entry != nil {
		t.Fatalf("2xx must not produce an error log entry, got %q", entry.Message)
	}

	long := strings.Repeat("x", maxLoggedErrorReasonLength+100)
	logErrorResponse(c, http.StatusBadGateway, long)
	entry := findAPIErrorResponseEntry(t, hook)
	if entry == nil {
		t.Fatalf("expected an 'api error response' log entry")
	}
	if !strings.HasSuffix(entry.Message, "...(truncated)") {
		t.Fatalf("long reason should be truncated, got %q", entry.Message[len(entry.Message)-40:])
	}
	if strings.Contains(entry.Message, strings.Repeat("x", maxLoggedErrorReasonLength+1)) {
		t.Fatalf("reason exceeds maxLoggedErrorReasonLength")
	}
	if !strings.Contains(entry.Message, "model= ") {
		t.Fatalf("missing model should be logged as empty, got %q", entry.Message)
	}
}

func TestRememberRequestedModel_RoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	ctx := context.WithValue(context.Background(), "gin", c)
	rememberRequestedModel(ctx, "  claude-sonnet-4-5  ")
	if got := requestedModelForLog(c); got != "claude-sonnet-4-5" {
		t.Fatalf("model = %q, want claude-sonnet-4-5", got)
	}

	// No gin context and blank model are both no-ops.
	rememberRequestedModel(context.Background(), "ignored")
	rememberRequestedModel(ctx, "   ")
	if got := requestedModelForLog(c); got != "claude-sonnet-4-5" {
		t.Fatalf("blank model must not overwrite, got %q", got)
	}
	if got := requestedModelForLog(nil); got != "" {
		t.Fatalf("nil context should yield empty model, got %q", got)
	}
}
