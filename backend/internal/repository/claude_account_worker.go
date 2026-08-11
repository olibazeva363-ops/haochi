package repository

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	claudeAccountWorkersEnv       = "CLAUDE_ACCOUNT_WORKERS"
	claudeWorkerSharedSecretEnv   = "CLAUDE_WORKER_SHARED_SECRET"
	claudeWorkerAccountIDEnv      = "CLAUDE_ACCOUNT_WORKER_ID"
	claudeWorkerListenEnv         = "CLAUDE_ACCOUNT_WORKER_LISTEN"
	claudeWorkerForwardPath       = "/internal/claude-worker/forward"
	claudeWorkerHealthPath        = "/health"
	claudeWorkerDefaultListenAddr = "0.0.0.0:8090"
	claudeWorkerMaxBodyBytes      = 64 << 20

	claudeWorkerHeaderSecret           = "X-Sub2api-Claude-Worker-Secret"
	claudeWorkerHeaderAccountID        = "X-Sub2api-Claude-Worker-Account-Id"
	claudeWorkerHeaderTargetURL        = "X-Sub2api-Claude-Worker-Target"
	claudeWorkerHeaderTargetMethod     = "X-Sub2api-Claude-Worker-Method"
	claudeWorkerHeaderProxyURL         = "X-Sub2api-Claude-Worker-Proxy"
	claudeWorkerHeaderConcurrency      = "X-Sub2api-Claude-Worker-Concurrency"
	claudeWorkerHeaderTLSProfile       = "X-Sub2api-Claude-Worker-Tls-Profile"
	claudeWorkerHeaderUpstreamProfile  = "X-Sub2api-Claude-Worker-Upstream-Profile"
	claudeWorkerHeaderDisableRedirects = "X-Sub2api-Claude-Worker-Disable-Redirects"
	claudeWorkerHeaderError            = "X-Sub2api-Claude-Worker-Error"
	claudeWorkerHeaderResult           = "X-Sub2api-Claude-Worker-Result"
)

type claudeAccountWorkerEndpoint struct {
	forwardURL string
}

type claudeAccountWorkerRoutingUpstream struct {
	base      service.HTTPUpstream
	endpoints map[int64]claudeAccountWorkerEndpoint
	secret    string
	client    *http.Client
	configErr error
}

// ClaudeAccountWorkerModeEnabled reports whether this process should run as a
// single-account Claude transport worker instead of the full application.
func ClaudeAccountWorkerModeEnabled() bool {
	return strings.TrimSpace(os.Getenv(claudeWorkerAccountIDEnv)) != ""
}

func newClaudeAccountWorkerRoutingUpstream(base service.HTTPUpstream) service.HTTPUpstream {
	raw := strings.TrimSpace(os.Getenv(claudeAccountWorkersEnv))
	if raw == "" {
		return base
	}

	endpoints, err := parseClaudeAccountWorkerEndpoints(raw)
	secret := strings.TrimSpace(os.Getenv(claudeWorkerSharedSecretEnv))
	if err == nil && len(secret) < 24 {
		err = fmt.Errorf("%s must contain at least 24 characters when %s is configured", claudeWorkerSharedSecretEnv, claudeAccountWorkersEnv)
	}
	if err != nil {
		slog.Error("claude_account_worker_config_invalid", "error", err)
	}

	return &claudeAccountWorkerRoutingUpstream{
		base:      base,
		endpoints: endpoints,
		secret:    secret,
		client:    newClaudeAccountWorkerHTTPClient(),
		configErr: err,
	}
}

func newClaudeAccountWorkerHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:          256,
			MaxIdleConnsPerHost:   32,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 5 * time.Minute,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func parseClaudeAccountWorkerEndpoints(raw string) (map[int64]claudeAccountWorkerEndpoint, error) {
	result := make(map[int64]claudeAccountWorkerEndpoint)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		accountRaw, endpointRaw, ok := strings.Cut(item, "=")
		if !ok {
			return nil, fmt.Errorf("invalid Claude worker mapping %q: expected ACCOUNT_ID=URL", item)
		}
		accountID, err := strconv.ParseInt(strings.TrimSpace(accountRaw), 10, 64)
		if err != nil || accountID <= 0 {
			return nil, fmt.Errorf("invalid Claude worker account ID %q", strings.TrimSpace(accountRaw))
		}
		if _, exists := result[accountID]; exists {
			return nil, fmt.Errorf("duplicate Claude worker account ID %d", accountID)
		}

		endpointURL, err := url.Parse(strings.TrimSpace(endpointRaw))
		if err != nil || endpointURL.Host == "" || (endpointURL.Scheme != "http" && endpointURL.Scheme != "https") {
			return nil, fmt.Errorf("invalid Claude worker URL for account %d", accountID)
		}
		if endpointURL.User != nil || endpointURL.RawQuery != "" || endpointURL.Fragment != "" {
			return nil, fmt.Errorf("Claude worker URL for account %d must not contain credentials, query, or fragment", accountID)
		}
		if endpointURL.Path != "" && endpointURL.Path != "/" {
			return nil, fmt.Errorf("Claude worker URL for account %d must not contain a path", accountID)
		}
		endpointURL.Path = claudeWorkerForwardPath
		result[accountID] = claudeAccountWorkerEndpoint{forwardURL: endpointURL.String()}
	}
	if len(result) == 0 {
		return nil, errors.New("no Claude account worker mappings were configured")
	}
	return result, nil
}

func (s *claudeAccountWorkerRoutingUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return s.do(req, proxyURL, accountID, accountConcurrency, nil)
}

func (s *claudeAccountWorkerRoutingUpstream) DoWithTLS(
	req *http.Request,
	proxyURL string,
	accountID int64,
	accountConcurrency int,
	profile *tlsfingerprint.Profile,
) (*http.Response, error) {
	return s.do(req, proxyURL, accountID, accountConcurrency, profile)
}

func (s *claudeAccountWorkerRoutingUpstream) do(
	req *http.Request,
	proxyURL string,
	accountID int64,
	accountConcurrency int,
	profile *tlsfingerprint.Profile,
) (*http.Response, error) {
	if s.configErr != nil {
		return nil, fmt.Errorf("Claude account worker configuration: %w", s.configErr)
	}
	endpoint, routed := s.endpoints[accountID]
	if !routed || !isClaudeAccountWorkerTarget(req) {
		if profile != nil {
			return s.base.DoWithTLS(req, proxyURL, accountID, accountConcurrency, profile)
		}
		return s.base.Do(req, proxyURL, accountID, accountConcurrency)
	}
	if req == nil || req.URL == nil {
		return nil, errors.New("Claude account worker request is empty")
	}

	workerReq, err := http.NewRequestWithContext(req.Context(), http.MethodPost, endpoint.forwardURL, req.Body)
	if err != nil {
		return nil, fmt.Errorf("create Claude account worker request: %w", err)
	}
	workerReq.Header = req.Header.Clone()
	stripClaudeWorkerControlHeaders(workerReq.Header)
	removeHopByHopHeaders(workerReq.Header)
	workerReq.ContentLength = req.ContentLength
	workerReq.Header.Set(claudeWorkerHeaderSecret, s.secret)
	workerReq.Header.Set(claudeWorkerHeaderAccountID, strconv.FormatInt(accountID, 10))
	workerReq.Header.Set(claudeWorkerHeaderTargetURL, req.URL.String())
	workerReq.Header.Set(claudeWorkerHeaderTargetMethod, req.Method)
	workerReq.Header.Set(claudeWorkerHeaderConcurrency, strconv.Itoa(accountConcurrency))
	if proxyURL != "" {
		workerReq.Header.Set(claudeWorkerHeaderProxyURL, base64.RawURLEncoding.EncodeToString([]byte(proxyURL)))
	}
	if profile != nil {
		encodedProfile, marshalErr := json.Marshal(profile)
		if marshalErr != nil {
			return nil, fmt.Errorf("encode Claude worker TLS profile: %w", marshalErr)
		}
		workerReq.Header.Set(claudeWorkerHeaderTLSProfile, base64.RawURLEncoding.EncodeToString(encodedProfile))
	}
	if upstreamProfile := service.HTTPUpstreamProfileFromContext(req.Context()); upstreamProfile != service.HTTPUpstreamProfileDefault {
		workerReq.Header.Set(claudeWorkerHeaderUpstreamProfile, string(upstreamProfile))
	}
	if service.HTTPUpstreamRedirectsDisabled(req.Context()) {
		workerReq.Header.Set(claudeWorkerHeaderDisableRedirects, "true")
	}

	resp, err := s.client.Do(workerReq)
	if err != nil {
		return nil, fmt.Errorf("Claude account worker %d unavailable: %w", accountID, err)
	}
	if workerError := strings.TrimSpace(resp.Header.Get(claudeWorkerHeaderError)); workerError != "" {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = workerError
		}
		return nil, fmt.Errorf("Claude account worker %d failed (%s): %s", accountID, workerError, message)
	}
	if !strings.EqualFold(strings.TrimSpace(resp.Header.Get(claudeWorkerHeaderResult)), "upstream") {
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("Claude account worker %d returned an invalid internal response", accountID)
	}
	resp.Header.Del(claudeWorkerHeaderError)
	resp.Header.Del(claudeWorkerHeaderResult)
	resp.Request = req
	return resp, nil
}

func isClaudeAccountWorkerTarget(req *http.Request) bool {
	if req == nil || req.URL == nil || !strings.EqualFold(req.URL.Scheme, "https") || req.URL.User != nil {
		return false
	}
	if port := req.URL.Port(); port != "" && port != "443" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(req.URL.Hostname(), "."))
	for _, domain := range []string{"anthropic.com", "claude.ai", "claude.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// NewClaudeAccountWorkerServer builds the restricted single-account transport
// process used by generated Compose worker services.
func NewClaudeAccountWorkerServer(cfg *config.Config) (*http.Server, error) {
	accountID, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(claudeWorkerAccountIDEnv)), 10, 64)
	if err != nil || accountID <= 0 {
		return nil, fmt.Errorf("%s must be a positive integer", claudeWorkerAccountIDEnv)
	}
	secret := strings.TrimSpace(os.Getenv(claudeWorkerSharedSecretEnv))
	if len(secret) < 24 {
		return nil, fmt.Errorf("%s must contain at least 24 characters", claudeWorkerSharedSecretEnv)
	}
	listenAddr := strings.TrimSpace(os.Getenv(claudeWorkerListenEnv))
	if listenAddr == "" {
		listenAddr = claudeWorkerDefaultListenAddr
	}
	if _, _, err := net.SplitHostPort(listenAddr); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", claudeWorkerListenEnv, err)
	}

	base := &httpUpstreamService{cfg: cfg, clients: make(map[string]*upstreamClientEntry)}
	handler := newClaudeAccountWorkerHandler(accountID, secret, base)
	return &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}, nil
}

func newClaudeAccountWorkerHandler(accountID int64, secret string, upstream service.HTTPUpstream) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(claudeWorkerHealthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc(claudeWorkerForwardPath, func(w http.ResponseWriter, r *http.Request) {
		handleClaudeAccountWorkerForward(w, r, accountID, secret, upstream)
	})
	return mux
}

func handleClaudeAccountWorkerForward(w http.ResponseWriter, r *http.Request, accountID int64, secret string, upstream service.HTTPUpstream) {
	if r.Method != http.MethodPost {
		writeClaudeWorkerError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	providedSecret := r.Header.Get(claudeWorkerHeaderSecret)
	if len(providedSecret) != len(secret) || subtle.ConstantTimeCompare([]byte(providedSecret), []byte(secret)) != 1 {
		writeClaudeWorkerError(w, http.StatusUnauthorized, "unauthorized", "worker authentication failed")
		return
	}
	requestAccountID, err := strconv.ParseInt(strings.TrimSpace(r.Header.Get(claudeWorkerHeaderAccountID)), 10, 64)
	if err != nil || requestAccountID != accountID {
		writeClaudeWorkerError(w, http.StatusForbidden, "account_mismatch", "worker account mismatch")
		return
	}

	targetURL, err := url.Parse(strings.TrimSpace(r.Header.Get(claudeWorkerHeaderTargetURL)))
	if err != nil || !isClaudeAccountWorkerTarget(&http.Request{URL: targetURL}) {
		writeClaudeWorkerError(w, http.StatusBadRequest, "target_not_allowed", "target is not an allowed Claude endpoint")
		return
	}
	targetMethod := strings.ToUpper(strings.TrimSpace(r.Header.Get(claudeWorkerHeaderTargetMethod)))
	switch targetMethod {
	case http.MethodGet, http.MethodPost, http.MethodHead:
	default:
		writeClaudeWorkerError(w, http.StatusBadRequest, "method_not_allowed", "upstream method is not allowed")
		return
	}
	if r.ContentLength > claudeWorkerMaxBodyBytes {
		writeClaudeWorkerError(w, http.StatusRequestEntityTooLarge, "request_too_large", "worker request body is too large")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, claudeWorkerMaxBodyBytes)
	upstreamCtx := r.Context()
	if profile := service.HTTPUpstreamProfile(strings.TrimSpace(r.Header.Get(claudeWorkerHeaderUpstreamProfile))); profile != service.HTTPUpstreamProfileDefault {
		upstreamCtx = service.WithHTTPUpstreamProfile(upstreamCtx, profile)
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get(claudeWorkerHeaderDisableRedirects)), "true") {
		upstreamCtx = service.WithHTTPUpstreamRedirectsDisabled(upstreamCtx)
	}
	upstreamReq, err := http.NewRequestWithContext(upstreamCtx, targetMethod, targetURL.String(), r.Body)
	if err != nil {
		writeClaudeWorkerError(w, http.StatusBadRequest, "invalid_request", "invalid upstream request")
		return
	}
	upstreamReq.Header = r.Header.Clone()
	stripClaudeWorkerControlHeaders(upstreamReq.Header)
	removeHopByHopHeaders(upstreamReq.Header)
	upstreamReq.ContentLength = r.ContentLength

	proxyURL, err := decodeClaudeWorkerHeader(r.Header.Get(claudeWorkerHeaderProxyURL))
	if err != nil {
		writeClaudeWorkerError(w, http.StatusBadRequest, "invalid_proxy", "invalid worker proxy metadata")
		return
	}
	concurrency, _ := strconv.Atoi(strings.TrimSpace(r.Header.Get(claudeWorkerHeaderConcurrency)))

	var resp *http.Response
	if encodedProfile := strings.TrimSpace(r.Header.Get(claudeWorkerHeaderTLSProfile)); encodedProfile != "" {
		profileJSON, decodeErr := base64.RawURLEncoding.DecodeString(encodedProfile)
		if decodeErr != nil {
			writeClaudeWorkerError(w, http.StatusBadRequest, "invalid_tls_profile", "invalid worker TLS profile")
			return
		}
		var profile tlsfingerprint.Profile
		if unmarshalErr := json.Unmarshal(profileJSON, &profile); unmarshalErr != nil {
			writeClaudeWorkerError(w, http.StatusBadRequest, "invalid_tls_profile", "invalid worker TLS profile")
			return
		}
		resp, err = upstream.DoWithTLS(upstreamReq, proxyURL, accountID, concurrency, &profile)
	} else {
		resp, err = upstream.Do(upstreamReq, proxyURL, accountID, concurrency)
	}
	if err != nil {
		writeClaudeWorkerError(w, http.StatusBadGateway, "upstream_transport", "upstream transport failed")
		return
	}
	if resp == nil {
		writeClaudeWorkerError(w, http.StatusBadGateway, "empty_response", "upstream returned no response")
		return
	}
	defer resp.Body.Close()
	copyClaudeWorkerResponseHeaders(w.Header(), resp.Header)
	w.Header().Set(claudeWorkerHeaderResult, "upstream")
	w.WriteHeader(resp.StatusCode)
	dst := io.Writer(w)
	if flusher, ok := w.(http.Flusher); ok {
		dst = &flushAfterWriteWriter{writer: w, flusher: flusher}
	}
	_, _ = io.Copy(dst, resp.Body)
}

func decodeClaudeWorkerHeader(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func writeClaudeWorkerError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set(claudeWorkerHeaderError, code)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, message)
}

func stripClaudeWorkerControlHeaders(header http.Header) {
	for _, key := range []string{
		claudeWorkerHeaderSecret,
		claudeWorkerHeaderAccountID,
		claudeWorkerHeaderTargetURL,
		claudeWorkerHeaderTargetMethod,
		claudeWorkerHeaderProxyURL,
		claudeWorkerHeaderConcurrency,
		claudeWorkerHeaderTLSProfile,
		claudeWorkerHeaderUpstreamProfile,
		claudeWorkerHeaderDisableRedirects,
		claudeWorkerHeaderError,
		claudeWorkerHeaderResult,
	} {
		header.Del(key)
	}
}

func removeHopByHopHeaders(header http.Header) {
	for _, connectionValue := range header.Values("Connection") {
		for _, token := range strings.Split(connectionValue, ",") {
			if token = strings.TrimSpace(token); token != "" {
				header.Del(token)
			}
		}
	}
	for _, key := range []string{
		"Connection",
		"Proxy-Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	} {
		header.Del(key)
	}
}

func copyClaudeWorkerResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if isClaudeWorkerControlHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	removeHopByHopHeaders(dst)
}

func isClaudeWorkerControlHeader(key string) bool {
	for _, controlKey := range []string{
		claudeWorkerHeaderSecret,
		claudeWorkerHeaderAccountID,
		claudeWorkerHeaderTargetURL,
		claudeWorkerHeaderTargetMethod,
		claudeWorkerHeaderProxyURL,
		claudeWorkerHeaderConcurrency,
		claudeWorkerHeaderTLSProfile,
		claudeWorkerHeaderUpstreamProfile,
		claudeWorkerHeaderDisableRedirects,
		claudeWorkerHeaderError,
		claudeWorkerHeaderResult,
	} {
		if strings.EqualFold(key, controlKey) {
			return true
		}
	}
	return false
}

type flushAfterWriteWriter struct {
	writer  io.Writer
	flusher http.Flusher
}

func (w *flushAfterWriteWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 {
		w.flusher.Flush()
	}
	return n, err
}

// ShutdownClaudeAccountWorker is kept small so the server command can share
// the application's normal graceful-shutdown behavior.
func ShutdownClaudeAccountWorker(ctx context.Context, server *http.Server) error {
	if server == nil {
		return nil
	}
	return server.Shutdown(ctx)
}
