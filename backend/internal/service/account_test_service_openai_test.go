//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// --- shared test helpers ---

type queuedHTTPUpstream struct {
	responses []*http.Response
	requests  []*http.Request
	tlsFlags  []bool
}

func (u *queuedHTTPUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return nil, fmt.Errorf("unexpected Do call")
}

func (u *queuedHTTPUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.requests = append(u.requests, req)
	u.tlsFlags = append(u.tlsFlags, profile != nil)
	if len(u.responses) == 0 {
		return nil, fmt.Errorf("no mocked response")
	}
	resp := u.responses[0]
	u.responses = u.responses[1:]
	return resp, nil
}

func newJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// --- test functions ---

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/1/test", nil)
	return c, rec
}

type openAIAccountTestRepo struct {
	mockAccountRepoForGemini
	updatedExtra  map[string]any
	rateLimitedID int64
	rateLimitedAt *time.Time
}

func (r *openAIAccountTestRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updatedExtra = updates
	return nil
}

func (r *openAIAccountTestRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedID = id
	r.rateLimitedAt = &resetAt
	return nil
}

func TestAccountTestService_OpenAISuccessPersistsSnapshotFromHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`))
	resp.Header.Set("x-codex-primary-used-percent", "88")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "604800")
	resp.Header.Set("x-codex-primary-window-minutes", "10080")
	resp.Header.Set("x-codex-secondary-used-percent", "42")
	resp.Header.Set("x-codex-secondary-reset-after-seconds", "18000")
	resp.Header.Set("x-codex-secondary-window-minutes", "300")

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:          89,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4")
	require.NoError(t, err)
	require.NotEmpty(t, repo.updatedExtra)
	require.Equal(t, 42.0, repo.updatedExtra["codex_5h_used_percent"])
	require.Equal(t, 88.0, repo.updatedExtra["codex_7d_used_percent"])
	require.Contains(t, recorder.Body.String(), "test_complete")
}

func TestAccountTestService_OpenAI429PersistsSnapshotWithoutRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached"}}`)
	resp.Header.Set("x-codex-primary-used-percent", "100")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "604800")
	resp.Header.Set("x-codex-primary-window-minutes", "10080")
	resp.Header.Set("x-codex-secondary-used-percent", "100")
	resp.Header.Set("x-codex-secondary-reset-after-seconds", "18000")
	resp.Header.Set("x-codex-secondary-window-minutes", "300")

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:          88,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4")
	require.Error(t, err)
	require.NotEmpty(t, repo.updatedExtra)
	require.Equal(t, 100.0, repo.updatedExtra["codex_5h_used_percent"])
	require.Zero(t, repo.rateLimitedID)
	require.Nil(t, repo.rateLimitedAt)
	require.Nil(t, account.RateLimitResetAt)
}

func TestAccountTestService_SendErrorAndEndDoesNotLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	var buf bytes.Buffer
	origWriter := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(origWriter)
	})

	svc := &AccountTestService{}
	err := svc.sendErrorAndEnd(ctx, "expected business error")
	require.Error(t, err)
	require.Contains(t, recorder.Body.String(), `"type":"error"`)
	require.Contains(t, recorder.Body.String(), `"error":"expected business error"`)
	require.Empty(t, buf.String(), "sendErrorAndEnd should not write logs directly")
}

func TestShouldWarnAccountTestBusinessError(t *testing.T) {
	cases := []struct {
		name      string
		errorType string
		errorMsg  string
		want      bool
	}{
		{name: "openai usage limit type", errorType: "usage_limit_reached", errorMsg: "The usage limit has been reached", want: true},
		{name: "rate limited message", errorType: "", errorMsg: "rate limited by upstream", want: true},
		{name: "forbidden message", errorType: "", errorMsg: "Access forbidden (403): account may be suspended or lack permissions", want: true},
		{name: "unsupported model type", errorType: "unsupported_model", errorMsg: "model not supported", want: true},
		{name: "system timeout", errorType: "", errorMsg: "stream read error: context deadline exceeded", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, shouldWarnAccountTestBusinessError(tc.errorType, tc.errorMsg))
		})
	}
}

func TestAccountTestService_ProcessOpenAIStream_BusinessErrorReturnsWarnClassifiedError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	svc := &AccountTestService{}
	body := strings.NewReader("data: {\"type\":\"error\",\"error\":{\"type\":\"usage_limit_reached\",\"message\":\"The usage limit has been reached\"}}\n\n")

	err := svc.processOpenAIStream(ctx, body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage limit")
	require.Contains(t, recorder.Body.String(), `"type":"error"`)
	require.Contains(t, recorder.Body.String(), `"The usage limit has been reached"`)
}

func TestAccountTestService_ProcessClaudeStream_BusinessMessageUsesClassifier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	svc := &AccountTestService{}
	body := strings.NewReader("data: {\"type\":\"error\",\"error\":{\"message\":\"account requires verification\"}}\n\n")

	err := svc.processClaudeStream(ctx, body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "verification")
	require.Contains(t, recorder.Body.String(), `"type":"error"`)
	require.Contains(t, recorder.Body.String(), `"account requires verification"`)
}

func TestAccountTestService_ProcessGeminiStream_BusinessMessageUsesClassifier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	svc := &AccountTestService{}
	body := strings.NewReader("data: {\"error\":{\"message\":\"quota exhausted for current window\"}}\n\n")

	err := svc.processGeminiStream(ctx, body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "quota exhausted")
	require.Contains(t, recorder.Body.String(), `"type":"error"`)
	require.Contains(t, recorder.Body.String(), `"quota exhausted for current window"`)
}
