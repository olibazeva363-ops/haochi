package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// --- fakes ---------------------------------------------------------------

type fakeProbeLister struct {
	accounts []Account
	err      error
	calls    int
}

func (f *fakeProbeLister) ListRateLimitedAccountsForProbe(_ context.Context, _ []string, _ time.Duration, _ int) ([]Account, error) {
	f.calls++
	return f.accounts, f.err
}

type fakeProbeTester struct {
	mu      sync.Mutex
	results map[int64]*ScheduledTestResult
	errs    map[int64]error
	calls   []int64
}

func (f *fakeProbeTester) RunTestBackground(_ context.Context, accountID int64, _ string) (*ScheduledTestResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, accountID)
	if f.errs != nil {
		if err, ok := f.errs[accountID]; ok {
			return nil, err
		}
	}
	if f.results != nil {
		return f.results[accountID], nil
	}
	return nil, nil
}

func (f *fakeProbeTester) calledIDs() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int64, len(f.calls))
	copy(out, f.calls)
	return out
}

type fakeProbeRecoverer struct {
	mu     sync.Mutex
	calls  []int64
	result *SuccessfulTestRecoveryResult
	err    error
}

func (f *fakeProbeRecoverer) RecoverAccountAfterSuccessfulTest(_ context.Context, accountID int64) (*SuccessfulTestRecoveryResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, accountID)
	return f.result, f.err
}

func (f *fakeProbeRecoverer) calledIDs() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int64, len(f.calls))
	copy(out, f.calls)
	return out
}

func newTestProbeRunner(
	lister rateLimitProbeCandidateLister,
	tester rateLimitProbeTester,
	recoverer rateLimitProbeRecoverer,
	cfg config.RateLimitProbeConfig,
) *RateLimitProbeRunnerService {
	return &RateLimitProbeRunnerService{
		lister:    lister,
		tester:    tester,
		recoverer: recoverer,
		cfg:       cfg,
		lastProbe: make(map[int64]time.Time),
		stopCh:    make(chan struct{}),
		nowFn:     time.Now,
	}
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// --- tests ---------------------------------------------------------------

// 探测成功 -> 触发恢复；探测失败的账号不恢复。
func TestRateLimitProbe_runOnce_recoversOnlyOnSuccess(t *testing.T) {
	lister := &fakeProbeLister{accounts: []Account{{ID: 1}, {ID: 2}}}
	tester := &fakeProbeTester{results: map[int64]*ScheduledTestResult{
		1: {Status: "success"},
		2: {Status: "failed"},
	}}
	recoverer := &fakeProbeRecoverer{result: &SuccessfulTestRecoveryResult{ClearedRateLimit: true}}

	runner := newTestProbeRunner(lister, tester, recoverer, config.RateLimitProbeConfig{
		Enabled:                true,
		MaxWorkers:             2,
		ReprobeIntervalMinutes: 30,
	})

	runner.runOnce()

	probed := tester.calledIDs()
	if !containsID(probed, 1) || !containsID(probed, 2) {
		t.Fatalf("expected both accounts probed, got %v", probed)
	}
	recovered := recoverer.calledIDs()
	if len(recovered) != 1 || recovered[0] != 1 {
		t.Fatalf("expected only account 1 recovered, got %v", recovered)
	}
}

// 探测报错的账号不会触发恢复。
func TestRateLimitProbe_probeOne_testerErrorSkipsRecover(t *testing.T) {
	lister := &fakeProbeLister{accounts: []Account{{ID: 7}}}
	tester := &fakeProbeTester{errs: map[int64]error{7: errors.New("boom")}}
	recoverer := &fakeProbeRecoverer{result: &SuccessfulTestRecoveryResult{ClearedRateLimit: true}}

	runner := newTestProbeRunner(lister, tester, recoverer, config.RateLimitProbeConfig{
		Enabled:    true,
		MaxWorkers: 1,
	})

	runner.runOnce()

	if got := recoverer.calledIDs(); len(got) != 0 {
		t.Fatalf("expected no recovery on tester error, got %v", got)
	}
}

// reprobeInterval 节流：同一账号在窗口内只被选中一次，窗口过后才可再探。
func TestRateLimitProbe_selectDue_throttlesWithinReprobeInterval(t *testing.T) {
	runner := newTestProbeRunner(nil, nil, nil, config.RateLimitProbeConfig{
		ReprobeIntervalMinutes: 30,
	})

	base := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cur := base
	runner.nowFn = func() time.Time { return cur }

	candidates := []Account{{ID: 1}, {ID: 2}}

	first := runner.selectDue(candidates)
	if len(first) != 2 {
		t.Fatalf("first pass: expected 2 due, got %d", len(first))
	}

	// 立即再次选取：全部被节流。
	if again := runner.selectDue(candidates); len(again) != 0 {
		t.Fatalf("second pass within window: expected 0 due, got %d", len(again))
	}

	// 推进超过 reprobeInterval：重新可探。
	cur = base.Add(31 * time.Minute)
	if third := runner.selectDue(candidates); len(third) != 2 {
		t.Fatalf("third pass after window: expected 2 due, got %d", len(third))
	}
}

// selectDue 跳过非法 ID。
func TestRateLimitProbe_selectDue_skipsInvalidID(t *testing.T) {
	runner := newTestProbeRunner(nil, nil, nil, config.RateLimitProbeConfig{ReprobeIntervalMinutes: 30})
	due := runner.selectDue([]Account{{ID: 0}, {ID: -1}, {ID: 5}})
	if len(due) != 1 || due[0].ID != 5 {
		t.Fatalf("expected only account 5, got %v", due)
	}
}

// 禁用时 Start 为 noop：后台循环不启动，lister 不会被调用。
func TestRateLimitProbe_Start_disabledIsNoop(t *testing.T) {
	lister := &fakeProbeLister{accounts: []Account{{ID: 1}}}
	runner := newTestProbeRunner(lister, &fakeProbeTester{}, &fakeProbeRecoverer{}, config.RateLimitProbeConfig{
		Enabled:         false,
		IntervalSeconds: 1,
	})

	runner.Start()
	defer runner.Stop()

	time.Sleep(50 * time.Millisecond)
	if lister.calls != 0 {
		t.Fatalf("expected no candidate listing when disabled, got %d calls", lister.calls)
	}
}

// pruneLastProbe 清理过期节流记录。
func TestRateLimitProbe_pruneLastProbe(t *testing.T) {
	runner := newTestProbeRunner(nil, nil, nil, config.RateLimitProbeConfig{ReprobeIntervalMinutes: 30})
	base := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cur := base
	runner.nowFn = func() time.Time { return cur }

	runner.lastProbe[1] = base                       // 新
	runner.lastProbe[2] = base.Add(-90 * time.Minute) // 老（> 2*reprobe=60m）

	cur = base
	runner.pruneLastProbe()

	if _, ok := runner.lastProbe[1]; !ok {
		t.Fatalf("expected fresh entry 1 retained")
	}
	if _, ok := runner.lastProbe[2]; ok {
		t.Fatalf("expected stale entry 2 pruned")
	}
}
