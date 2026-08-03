package service

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// rateLimitProbeCandidateLister 是半开重探 runner 对账号仓库的最小依赖：
// 只需要「取出仍在限流冷却中的候选账号」这一只读能力。
type rateLimitProbeCandidateLister interface {
	ListRateLimitedAccountsForProbe(ctx context.Context, platforms []string, minCooldownRemaining time.Duration, limit int) ([]Account, error)
}

// rateLimitProbeTester 对单个账号发起一次带外探测请求（复用账号连通性测试）。
type rateLimitProbeTester interface {
	RunTestBackground(ctx context.Context, accountID int64, modelID string) (*ScheduledTestResult, error)
}

// rateLimitProbeRecoverer 在探测成功后清除账号的可恢复限流状态并触发出池。
type rateLimitProbeRecoverer interface {
	RecoverAccountAfterSuccessfulTest(ctx context.Context, accountID int64) (*SuccessfulTestRecoveryResult, error)
}

// RateLimitProbeRunnerService 周期性地对「仍在限流冷却中」的账号发起带外探测
// （half-open re-probe）：若探测成功，说明上游冷却其实已解除（常见于冷却时间过于
// 保守、或偶发 429），立即清除限流并让账号提前出池，避免额度没用完却长时间被封。
//
// 关键设计：
//   - 带外探测：使用账号连通性测试而非真实用户流量，被封账号不会进入调度候选，
//     因此不改动调度选取逻辑、不会把用户请求路由到仍受限的账号，零调度风险。
//   - 内存节流：每个账号在 reprobeInterval 内至多探测一次，成本可控。
//   - 无 leader lock：单实例部署足够；多实例场景最坏是重复探测，因探测幂等且被
//     节流，无副作用（与 ScheduledTestRunner 一致）。
type RateLimitProbeRunnerService struct {
	lister    rateLimitProbeCandidateLister
	tester    rateLimitProbeTester
	recoverer rateLimitProbeRecoverer
	cfg       config.RateLimitProbeConfig

	mu        sync.Mutex
	lastProbe map[int64]time.Time // 账号 -> 上次探测时间，内存节流

	stopCh    chan struct{}
	wg        sync.WaitGroup
	startOnce sync.Once
	stopOnce  sync.Once

	// nowFn 便于测试注入时间。
	nowFn func() time.Time
}

// NewRateLimitProbeRunnerService 构造半开重探 runner。
func NewRateLimitProbeRunnerService(
	lister rateLimitProbeCandidateLister,
	tester rateLimitProbeTester,
	recoverer rateLimitProbeRecoverer,
	cfg *config.Config,
) *RateLimitProbeRunnerService {
	var probeCfg config.RateLimitProbeConfig
	if cfg != nil {
		probeCfg = cfg.Gateway.Scheduling.RateLimitProbe
	}
	return &RateLimitProbeRunnerService{
		lister:    lister,
		tester:    tester,
		recoverer: recoverer,
		cfg:       probeCfg,
		lastProbe: make(map[int64]time.Time),
		stopCh:    make(chan struct{}),
		nowFn:     time.Now,
	}
}

func (s *RateLimitProbeRunnerService) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

func (s *RateLimitProbeRunnerService) interval() time.Duration {
	if s.cfg.IntervalSeconds > 0 {
		return time.Duration(s.cfg.IntervalSeconds) * time.Second
	}
	return 60 * time.Second
}

func (s *RateLimitProbeRunnerService) reprobeInterval() time.Duration {
	if s.cfg.ReprobeIntervalMinutes > 0 {
		return time.Duration(s.cfg.ReprobeIntervalMinutes) * time.Minute
	}
	return 30 * time.Minute
}

func (s *RateLimitProbeRunnerService) minCooldownRemaining() time.Duration {
	if s.cfg.MinCooldownRemainingMinutes > 0 {
		return time.Duration(s.cfg.MinCooldownRemainingMinutes) * time.Minute
	}
	return 0
}

func (s *RateLimitProbeRunnerService) maxWorkers() int {
	if s.cfg.MaxWorkers > 0 {
		return s.cfg.MaxWorkers
	}
	return 5
}

func (s *RateLimitProbeRunnerService) batchLimit() int {
	if s.cfg.BatchLimit > 0 {
		return s.cfg.BatchLimit
	}
	return 200
}

// Start 启动后台循环。未启用或依赖缺失时直接返回（noop）。
func (s *RateLimitProbeRunnerService) Start() {
	if s == nil {
		return
	}
	if !s.cfg.Enabled {
		logger.LegacyPrintf("service.ratelimit_probe", "[RateLimitProbe] disabled")
		return
	}
	if s.lister == nil || s.tester == nil || s.recoverer == nil {
		logger.LegacyPrintf("service.ratelimit_probe", "[RateLimitProbe] not started: missing dependencies")
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.loop()
		logger.LegacyPrintf("service.ratelimit_probe",
			"[RateLimitProbe] started (interval=%s reprobe=%s min_cooldown_remaining=%s max_workers=%d platforms=%v)",
			s.interval(), s.reprobeInterval(), s.minCooldownRemaining(), s.maxWorkers(), s.cfg.Platforms)
	})
}

// Stop 优雅停止后台循环。
func (s *RateLimitProbeRunnerService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		logger.LegacyPrintf("service.ratelimit_probe", "[RateLimitProbe] stop timed out")
	}
}

func (s *RateLimitProbeRunnerService) loop() {
	defer s.wg.Done()
	timer := time.NewTimer(s.interval())
	defer timer.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-timer.C:
			s.runOnce()
			timer.Reset(s.interval())
		}
	}
}

// runOnce 执行一轮探测：取候选 -> 节流过滤 -> 并发带外探测 -> 成功即恢复。
func (s *RateLimitProbeRunnerService) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	candidates, err := s.lister.ListRateLimitedAccountsForProbe(ctx, s.cfg.Platforms, s.minCooldownRemaining(), s.batchLimit())
	if err != nil {
		logger.LegacyPrintf("service.ratelimit_probe", "[RateLimitProbe] list candidates error: %v", err)
		return
	}

	due := s.selectDue(candidates)
	if len(due) == 0 {
		return
	}
	logger.LegacyPrintf("service.ratelimit_probe", "[RateLimitProbe] probing %d/%d rate-limited accounts", len(due), len(candidates))

	sem := make(chan struct{}, s.maxWorkers())
	var wg sync.WaitGroup
	for _, acc := range due {
		select {
		case <-s.stopCh:
			wg.Wait()
			return
		default:
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(accountID int64) {
			defer wg.Done()
			defer func() { <-sem }()
			s.probeOne(ctx, accountID)
		}(acc.ID)
	}
	wg.Wait()

	s.pruneLastProbe()
}

// selectDue 返回本轮应探测的账号：距上次探测已超过 reprobeInterval 的候选。
// 同时把这些账号的 lastProbe 更新为当前时间，兼作单发去重（同一轮不重复）。
func (s *RateLimitProbeRunnerService) selectDue(candidates []Account) []Account {
	now := s.now()
	reprobe := s.reprobeInterval()
	s.mu.Lock()
	defer s.mu.Unlock()
	due := make([]Account, 0, len(candidates))
	for i := range candidates {
		acc := candidates[i]
		if acc.ID <= 0 {
			continue
		}
		if last, ok := s.lastProbe[acc.ID]; ok && now.Sub(last) < reprobe {
			continue
		}
		s.lastProbe[acc.ID] = now
		due = append(due, acc)
	}
	return due
}

func (s *RateLimitProbeRunnerService) probeOne(ctx context.Context, accountID int64) {
	result, err := s.tester.RunTestBackground(ctx, accountID, "")
	if err != nil {
		logger.LegacyPrintf("service.ratelimit_probe", "[RateLimitProbe] account=%d probe error: %v", accountID, err)
		return
	}
	if result == nil || result.Status != "success" {
		// 仍受限：保持冷却，等待下一轮（受 reprobeInterval 节流）。
		return
	}

	recovery, err := s.recoverer.RecoverAccountAfterSuccessfulTest(ctx, accountID)
	if err != nil {
		logger.LegacyPrintf("service.ratelimit_probe", "[RateLimitProbe] account=%d recover error: %v", accountID, err)
		return
	}
	if recovery != nil && recovery.ClearedRateLimit {
		logger.LegacyPrintf("service.ratelimit_probe", "[RateLimitProbe] account=%d recovered: cleared rate-limit early via probe", accountID)
	}
}

// pruneLastProbe 清理长时间未再出现的账号节流记录，避免 map 无限增长。
func (s *RateLimitProbeRunnerService) pruneLastProbe() {
	now := s.now()
	ttl := 2 * s.reprobeInterval()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, last := range s.lastProbe {
		if now.Sub(last) > ttl {
			delete(s.lastProbe, id)
		}
	}
}
