package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
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
	do func(*http.Request) (*http.Response, error)
}

func (u *warmupLifecycleUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.do(req)
}

func warmupLifecycleAccounts(count int) []Account {
	now := time.Now()
	accounts := make([]Account, count)
	for i := range accounts {
		// A zero ID bypasses the production jitter so every test candidate is due.
		accounts[i] = Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, LastUsedAt: &now, Concurrency: 1}
	}
	return accounts
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
