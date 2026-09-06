package service

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type warmupLifecycleAccountRepo struct {
	AccountRepository
	list func(context.Context) ([]Account, error)
}

func (r *warmupLifecycleAccountRepo) ListSchedulableByPlatforms(ctx context.Context, _ []string) ([]Account, error) {
	return r.list(ctx)
}

type warmupLifecycleUpstream struct {
	HTTPUpstream
	do        func(*http.Request) (*http.Response, error)
	doWithTLS func(*http.Request, *tlsfingerprint.Profile) (*http.Response, error)
}

func (u *warmupLifecycleUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.do(req)
}

func (u *warmupLifecycleUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.doWithTLS(req, profile)
}

type warmupEOFObservedBody struct {
	io.ReadCloser
	readEOF *atomic.Bool
}

func (b *warmupEOFObservedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.readEOF.Store(true)
	}
	return n, err
}

func TestUpstreamWarmupUsesProviderTransportProfile(t *testing.T) {
	for _, tc := range []struct {
		name        string
		platform    string
		accountType string
		wantHost    string
		wantProfile HTTPUpstreamProfile
		wantTLS     bool
	}{
		{
			name: "openai_oauth", platform: PlatformOpenAI, accountType: AccountTypeOAuth,
			wantHost: "chatgpt.com", wantProfile: HTTPUpstreamProfileOpenAI,
		},
		{
			name: "openai_api_key", platform: PlatformOpenAI, accountType: AccountTypeAPIKey,
			wantHost: "api.openai.com", wantProfile: HTTPUpstreamProfileOpenAI,
		},
		{
			name: "anthropic_api_key", platform: PlatformAnthropic, accountType: AccountTypeAPIKey,
			wantHost: "api.anthropic.com", wantProfile: HTTPUpstreamProfileDefault,
		},
		{
			name: "anthropic_oauth_tls", platform: PlatformAnthropic, accountType: AccountTypeOAuth,
			wantHost: "api.anthropic.com", wantProfile: HTTPUpstreamProfileDefault, wantTLS: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 42, Platform: tc.platform, Type: tc.accountType, Concurrency: 2}
			calls := 0
			checkRequest := func(req *http.Request, profile *tlsfingerprint.Profile) (*http.Response, error) {
				calls++
				// This context value selects the same protocol-specific connection
				// pool used by normal OpenAI forwarding and passthrough requests.
				require.Equal(t, tc.wantProfile, HTTPUpstreamProfileFromContext(req.Context()))
				require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()), "warmup must stay on the original host")
				require.Equal(t, tc.wantHost, req.URL.Host)
				require.Equal(t, tc.wantTLS, profile != nil, "Anthropic's TLS fingerprint path must be preserved")
				require.Empty(t, req.Header.Get("Authorization"), "warmup must remain credential-free")
				require.Empty(t, req.Header.Get("x-api-key"))
				return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader("{}"))}, nil
			}
			upstream := &warmupLifecycleUpstream{
				do:        func(req *http.Request) (*http.Response, error) { return checkRequest(req, nil) },
				doWithTLS: checkRequest,
			}
			svc := NewUpstreamWarmupService(nil, upstream, &TLSFingerprintProfileService{}, &config.Config{})

			require.True(t, svc.warmAccount(context.Background(), account, upstreamWarmURL(account)))
			require.Equal(t, 1, calls)
		})
	}
}

func TestUpstreamWarmupKeepsHTTP1ConnectionAfterLargeErrorBody(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var redirectedRequests, connections atomic.Int64
			redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				redirectedRequests.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer redirectTarget.Close()
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.Header().Set("Location", redirectTarget.URL)
					w.WriteHeader(status)
					_, _ = io.WriteString(w, strings.Repeat("x", 5<<10))
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Errorf("flush warmup response: %v", err)
						return
					}
					// Exceed Go 1.27's short Close-time automatic-drain window.
					// Warmup should explicitly wait for EOF rather than rely on it.
					time.Sleep(100 * time.Millisecond)
					_, _ = io.WriteString(w, strings.Repeat("x", 3<<10))
					return
				}
				_, _ = io.WriteString(w, strings.Repeat("x", 8<<10))
			}))
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}
			server.Start()
			defer server.Close()
			transport := &http.Transport{MaxIdleConnsPerHost: 1}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport}
			var readEOF atomic.Bool
			upstream := &warmupLifecycleUpstream{do: func(req *http.Request) (*http.Response, error) {
				// Honor HTTPUpstream's redirect-policy contract while exercising an
				// actual transport; importing its repository implementation would cycle.
				warmClient := *client
				if HTTPUpstreamRedirectsDisabled(req.Context()) {
					warmClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
				}
				resp, err := warmClient.Do(req)
				if err == nil {
					resp.Body = &warmupEOFObservedBody{ReadCloser: resp.Body, readEOF: &readEOF}
				}
				return resp, err
			}}
			svc := NewUpstreamWarmupService(nil, upstream, nil, &config.Config{})
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 2}

			require.True(t, svc.warmAccount(context.Background(), account, server.URL))
			require.True(t, readEOF.Load(), "warmup must consume EOF before reporting success, independently of Close-time automatic drain")

			// A subsequent request must use the warmed TCP connection, not merely
			// report a successful HTTP status/transport call from the warmup request.
			var reused atomic.Bool
			trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused.Store(info.Reused) }}
			ctx := httptrace.WithClientTrace(context.Background(), trace)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader("{}"))
			require.NoError(t, err)
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			_, err = io.Copy(io.Discard, resp.Body)
			require.NoError(t, err)
			require.Equal(t, 1, resp.ProtoMajor)
			require.True(t, reused.Load(), "an error body larger than 4KiB must not discard the warmed connection")
			require.Equal(t, int64(1), connections.Load())
			require.Zero(t, redirectedRequests.Load(), "a redirect must not move warmup to another host")
		})
	}
}

func TestUpstreamWarmupDoesNotReportIncompleteResponsesAsWarmed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "body_exceeds_limit",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, strings.Repeat("x", 2*warmupMaxResponseBytes))
			},
		},
		{
			name: "server_closes_connection",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Connection", "close")
				_, _ = io.WriteString(w, "{}")
			},
		},
		{
			name: "truncated_response_body",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "100")
				_, _ = io.WriteString(w, "partial")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()
			transport := &http.Transport{}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport}
			upstream := &warmupLifecycleUpstream{do: client.Do}
			svc := NewUpstreamWarmupService(nil, upstream, nil, &config.Config{})
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 2}

			require.False(t, svc.warmAccount(context.Background(), account, server.URL))
		})
	}
}

func warmupLifecycleAccounts(count int) []Account {
	now := time.Now()
	accounts := make([]Account, count)
	for i := range accounts {
		accounts[i] = Account{ID: int64(i + 1), Platform: PlatformOpenAI, Type: AccountTypeAPIKey, LastUsedAt: &now, Concurrency: 1}
	}
	return accounts
}

func TestUpstreamWarmupIncludesAllRecentlyUsedOpenAIAccountsEachCycle(t *testing.T) {
	const recentAccounts = 32
	accounts := warmupLifecycleAccounts(recentAccounts)
	for i := range accounts {
		if i%2 == 0 {
			accounts[i].Type = AccountTypeOAuth
		}
	}
	stale := time.Now().Add(-3 * time.Hour)
	accounts = append(accounts,
		Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth, LastUsedAt: &stale},
		Account{ID: 101, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	)
	repo := &warmupLifecycleAccountRepo{list: func(context.Context) ([]Account, error) { return accounts, nil }}
	var requests atomic.Int64
	upstream := &warmupLifecycleUpstream{do: func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}}
	svc := NewUpstreamWarmupService(repo, upstream, nil, &config.Config{})

	for range 2 {
		require.Equal(t, recentAccounts, svc.warmOnce(context.Background()), "positive OpenAI account IDs must not be randomly skipped")
	}
	require.Equal(t, int64(2*recentAccounts), requests.Load(), "unused/stale accounts must remain excluded")
}

func TestUpstreamWarmupStopCancelsRequestAndQueuedAccounts(t *testing.T) {
	started := make(chan struct{}, 8)
	requestCanceled := make(chan struct{}, 8)
	abort := make(chan struct{})
	var requests atomic.Int64
	repo := &warmupLifecycleAccountRepo{list: func(context.Context) ([]Account, error) {
		return warmupLifecycleAccounts(8), nil
	}}
	upstream := &warmupLifecycleUpstream{do: func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		started <- struct{}{}
		select {
		case <-req.Context().Done():
			requestCanceled <- struct{}{}
			return nil, req.Context().Err()
		case <-abort:
			return nil, context.Canceled
		}
	}}
	cfg := &config.Config{}
	cfg.Gateway.UpstreamWarmup.Enabled = true
	cfg.Gateway.UpstreamWarmup.Concurrency = 1
	svc := NewUpstreamWarmupService(repo, upstream, nil, cfg)
	t.Cleanup(func() {
		close(abort)
		svc.Stop()
	})
	svc.Start()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("warmup did not start")
	}
	// Starting twice must not create another cycle while the first is blocked.
	svc.Start()
	stopped := make(chan struct{})
	go func() {
		svc.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop waited for upstream timeout instead of canceling the cycle")
	}
	require.Len(t, requestCanceled, 1)
	require.Equal(t, int64(1), requests.Load(), "queued accounts must not start after Stop")
	svc.Start()
	require.Equal(t, int64(1), requests.Load(), "a stopped service cannot restart")
}

func TestUpstreamWarmupStopCancelsAccountQuery(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	abort := make(chan struct{})
	repo := &warmupLifecycleAccountRepo{list: func(ctx context.Context) ([]Account, error) {
		close(started)
		select {
		case <-ctx.Done():
			close(canceled)
			return nil, ctx.Err()
		case <-abort:
			return nil, context.Canceled
		}
	}}
	cfg := &config.Config{}
	cfg.Gateway.UpstreamWarmup.Enabled = true
	svc := NewUpstreamWarmupService(repo, &warmupLifecycleUpstream{}, nil, cfg)
	t.Cleanup(func() {
		close(abort)
		svc.Stop()
	})
	svc.Start()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("account query did not start")
	}
	stopped := make(chan struct{})
	go func() {
		svc.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not cancel the account query")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("account query context remained active")
	}
}

func TestUpstreamWarmupCountsConcurrentCompletions(t *testing.T) {
	const accounts, concurrency = 128, 4
	started := make(chan struct{}, accounts)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var requests, active, peak atomic.Int64
	repo := &warmupLifecycleAccountRepo{list: func(context.Context) ([]Account, error) {
		return warmupLifecycleAccounts(accounts), nil
	}}
	upstream := &warmupLifecycleUpstream{do: func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		inflight := active.Add(1)
		defer active.Add(-1)
		for previous := peak.Load(); inflight > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, inflight) {
				break
			}
		}
		started <- struct{}{}
		<-release
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}}
	cfg := &config.Config{}
	cfg.Gateway.UpstreamWarmup.Concurrency = concurrency
	svc := NewUpstreamWarmupService(repo, upstream, nil, cfg)
	result := make(chan int, 1)
	done := make(chan struct{})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		<-done
	})
	go func() {
		defer close(done)
		result <- svc.warmOnce(context.Background())
	}()
	for range concurrency {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("warmup did not fill its concurrency window")
		}
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case count := <-result:
		require.Equal(t, accounts, count)
	case <-time.After(3 * time.Second):
		t.Fatal("warmup cycle did not finish")
	}
	require.Equal(t, int64(accounts), requests.Load())
	require.Equal(t, int64(concurrency), peak.Load())
}
