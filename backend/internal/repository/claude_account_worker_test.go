package repository

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const claudeWorkerTestSecret = "0123456789abcdef0123456789abcdef"

type claudeWorkerUpstreamCall struct {
	request     *http.Request
	body        []byte
	proxyURL    string
	accountID   int64
	concurrency int
	profile     *tlsfingerprint.Profile
}

type claudeWorkerUpstreamStub struct {
	calls    []claudeWorkerUpstreamCall
	response *http.Response
	err      error
}

func (s *claudeWorkerUpstreamStub) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	return s.record(req, proxyURL, accountID, concurrency, nil)
}

func (s *claudeWorkerUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return s.record(req, proxyURL, accountID, concurrency, profile)
}

func (s *claudeWorkerUpstreamStub) record(req *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	s.calls = append(s.calls, claudeWorkerUpstreamCall{
		request:     req,
		body:        body,
		proxyURL:    proxyURL,
		accountID:   accountID,
		concurrency: concurrency,
		profile:     profile,
	})
	if s.response == nil && s.err == nil {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	}
	return s.response, s.err
}

func TestParseClaudeAccountWorkerEndpoints(t *testing.T) {
	endpoints, err := parseClaudeAccountWorkerEndpoints("12=http://claude-worker-12:8090, 18=https://worker.example")
	require.NoError(t, err)
	require.Equal(t, "http://claude-worker-12:8090/internal/claude-worker/forward", endpoints[12].forwardURL)
	require.Equal(t, "https://worker.example/internal/claude-worker/forward", endpoints[18].forwardURL)

	_, err = parseClaudeAccountWorkerEndpoints("12=http://worker,12=http://other")
	require.ErrorContains(t, err, "duplicate")
	_, err = parseClaudeAccountWorkerEndpoints("bad")
	require.ErrorContains(t, err, "ACCOUNT_ID=URL")
	_, err = parseClaudeAccountWorkerEndpoints("12=http://worker/base")
	require.ErrorContains(t, err, "must not contain a path")
}

func TestClaudeAccountWorkerTargetAllowlist(t *testing.T) {
	for _, rawURL := range []string{
		"https://api.anthropic.com/v1/messages",
		"https://console.anthropic.com/v1/oauth",
		"https://claude.ai/api/usage",
	} {
		req, err := http.NewRequest(http.MethodPost, rawURL, nil)
		require.NoError(t, err)
		require.True(t, isClaudeAccountWorkerTarget(req), rawURL)
	}
	for _, rawURL := range []string{
		"http://api.anthropic.com/v1/messages",
		"https://api.anthropic.com:8443/v1/messages",
		"https://evil-anthropic.com/v1/messages",
		"https://api.anthropic.com.attacker.example/v1/messages",
		"https://attacker@api.anthropic.com/v1/messages",
		"https://127.0.0.1/v1/messages",
		"https://example.com/v1/messages",
	} {
		req, err := http.NewRequest(http.MethodPost, rawURL, nil)
		require.NoError(t, err)
		require.False(t, isClaudeAccountWorkerTarget(req), rawURL)
	}
}

func TestClaudeAccountWorkerRoutesFixedAccountAndPreservesUpstreamResponse(t *testing.T) {
	workerUpstream := &claudeWorkerUpstreamStub{response: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"Retry-After":  []string{"37"},
		},
		Body: io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"rate_limit_error"}}`)),
	}}
	workerServer := httptest.NewServer(newClaudeAccountWorkerHandler(42, claudeWorkerTestSecret, workerUpstream))
	t.Cleanup(workerServer.Close)

	base := &claudeWorkerUpstreamStub{}
	router := &claudeAccountWorkerRoutingUpstream{
		base: base,
		endpoints: map[int64]claudeAccountWorkerEndpoint{
			42: {forwardURL: workerServer.URL + claudeWorkerForwardPath},
		},
		secret: claudeWorkerTestSecret,
		client: newClaudeAccountWorkerHTTPClient(),
	}
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[]}`)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.anthropic.com/v1/messages?beta=true", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer TOKEN")

	resp, err := router.DoWithTLS(req, "socks5://proxy.example:1080", 42, 3, &tlsfingerprint.Profile{Name: "claude-node"})
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	responseBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.Equal(t, "37", resp.Header.Get("Retry-After"))
	require.JSONEq(t, `{"type":"error","error":{"type":"rate_limit_error"}}`, string(responseBody))
	require.Empty(t, base.calls)

	require.Len(t, workerUpstream.calls, 1)
	call := workerUpstream.calls[0]
	require.Equal(t, int64(42), call.accountID)
	require.Equal(t, 3, call.concurrency)
	require.Equal(t, "socks5://proxy.example:1080", call.proxyURL)
	require.Equal(t, "claude-node", call.profile.Name)
	require.Equal(t, "https://api.anthropic.com/v1/messages?beta=true", call.request.URL.String())
	require.True(t, service.HTTPUpstreamRedirectsDisabled(call.request.Context()))
	require.Equal(t, "Bearer TOKEN", call.request.Header.Get("Authorization"))
	require.Empty(t, call.request.Header.Get(claudeWorkerHeaderSecret))
	require.Equal(t, body, call.body)
}

func TestClaudeAccountWorkerAuthenticatesAndValidatesBeforeForwarding(t *testing.T) {
	for _, tc := range []struct {
		name, secret, target string
		wantStatus           int
	}{
		{"missing secret", "", "https://api.anthropic.com/v1/messages", http.StatusUnauthorized},
		{"wrong secret", strings.Repeat("x", len(claudeWorkerTestSecret)), "https://api.anthropic.com/v1/messages", http.StatusUnauthorized},
		{"private target", claudeWorkerTestSecret, "https://127.0.0.1/admin", http.StatusBadRequest},
		{"deceptive host", claudeWorkerTestSecret, "https://api.anthropic.com.attacker.example/v1/messages", http.StatusBadRequest},
		{"target credentials", claudeWorkerTestSecret, "https://attacker@api.anthropic.com/v1/messages", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &claudeWorkerUpstreamStub{}
			req := httptest.NewRequest(http.MethodPost, claudeWorkerForwardPath, strings.NewReader(`{}`))
			req.Header.Set(claudeWorkerHeaderSecret, tc.secret)
			req.Header.Set(claudeWorkerHeaderAccountID, "42")
			req.Header.Set(claudeWorkerHeaderTargetURL, tc.target)
			req.Header.Set(claudeWorkerHeaderTargetMethod, http.MethodPost)
			response := httptest.NewRecorder()

			newClaudeAccountWorkerHandler(42, claudeWorkerTestSecret, upstream).ServeHTTP(response, req)

			require.Equal(t, tc.wantStatus, response.Code)
			require.Empty(t, upstream.calls)
		})
	}
}

func TestClaudeAccountWorkerLeavesUnmappedAndNonClaudeRequestsOnBaseUpstream(t *testing.T) {
	base := &claudeWorkerUpstreamStub{}
	router := &claudeAccountWorkerRoutingUpstream{
		base: base,
		endpoints: map[int64]claudeAccountWorkerEndpoint{
			42: {forwardURL: "http://unused"},
		},
		secret: claudeWorkerTestSecret,
		client: http.DefaultClient,
	}

	unmapped, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(`{}`))
	require.NoError(t, err)
	resp, err := router.Do(unmapped, "", 43, 1)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	nonClaude, err := http.NewRequest(http.MethodPost, "https://example.com/v1/messages", strings.NewReader(`{}`))
	require.NoError(t, err)
	resp, err = router.Do(nonClaude, "", 42, 1)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Len(t, base.calls, 2)
}

func TestClaudeAccountWorkerRejectsWrongAccount(t *testing.T) {
	workerUpstream := &claudeWorkerUpstreamStub{}
	server := httptest.NewServer(newClaudeAccountWorkerHandler(42, claudeWorkerTestSecret, workerUpstream))
	t.Cleanup(server.Close)

	req, err := http.NewRequest(http.MethodPost, server.URL+claudeWorkerForwardPath, strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set(claudeWorkerHeaderSecret, claudeWorkerTestSecret)
	req.Header.Set(claudeWorkerHeaderAccountID, "43")
	req.Header.Set(claudeWorkerHeaderTargetURL, "https://api.anthropic.com/v1/messages")
	req.Header.Set(claudeWorkerHeaderTargetMethod, http.MethodPost)
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Equal(t, "account_mismatch", resp.Header.Get(claudeWorkerHeaderError))
	require.Empty(t, workerUpstream.calls)
}

func TestClaudeAccountWorkerInternalClientDoesNotFollowRedirects(t *testing.T) {
	targetCalled := false
	forwardedSecret := ""
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalled = true
		forwardedSecret = r.Header.Get(claudeWorkerHeaderSecret)
		w.Header().Set(claudeWorkerHeaderResult, "upstream")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)

	router := &claudeAccountWorkerRoutingUpstream{
		base: &claudeWorkerUpstreamStub{},
		endpoints: map[int64]claudeAccountWorkerEndpoint{
			42: {forwardURL: server.URL + "/redirect"},
		},
		secret: claudeWorkerTestSecret,
		client: newClaudeAccountWorkerHTTPClient(),
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(`{}`))
	require.NoError(t, err)
	_, err = router.Do(req, "", 42, 1)
	require.ErrorContains(t, err, "invalid internal response")
	require.False(t, targetCalled)
	require.Empty(t, forwardedSecret)
}

func TestClaudeAccountWorkerProviderRedirectsCannotBeEnabledByRequest(t *testing.T) {
	upstream := &claudeWorkerUpstreamStub{response: &http.Response{
		StatusCode: http.StatusTemporaryRedirect,
		Header:     http.Header{"Location": []string{"https://127.0.0.1/private"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}}
	req := httptest.NewRequest(http.MethodPost, claudeWorkerForwardPath, strings.NewReader(`{}`))
	req.Header.Set(claudeWorkerHeaderSecret, claudeWorkerTestSecret)
	req.Header.Set(claudeWorkerHeaderAccountID, "42")
	req.Header.Set(claudeWorkerHeaderTargetURL, "https://api.anthropic.com/v1/messages")
	req.Header.Set(claudeWorkerHeaderTargetMethod, http.MethodPost)
	req.Header.Set(claudeWorkerHeaderDisableRedirects, "false")
	response := httptest.NewRecorder()

	newClaudeAccountWorkerHandler(42, claudeWorkerTestSecret, upstream).ServeHTTP(response, req)

	require.Len(t, upstream.calls, 1)
	require.True(t, service.HTTPUpstreamRedirectsDisabled(upstream.calls[0].request.Context()))
	require.Equal(t, http.StatusTemporaryRedirect, response.Code)
}

var _ service.HTTPUpstream = (*claudeWorkerUpstreamStub)(nil)
var _ service.HTTPUpstream = (*claudeAccountWorkerRoutingUpstream)(nil)
