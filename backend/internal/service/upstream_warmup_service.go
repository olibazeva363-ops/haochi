package service

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// upstreamWarmURL 返回账号预热用的上游 URL。
// 选择原则：真实存在、无需鉴权即快速返回（401/404）、命中真实流量相同的主机，
// 从而让连接进入与对话流量同一个「账号+代理」连接池入口。
func upstreamWarmURL(account *Account) string {
	switch account.Platform {
	case PlatformAnthropic:
		return "https://api.anthropic.com/v1/models"
	case PlatformOpenAI:
		if account.IsOAuth() {
			// Codex OAuth 流量打到 chatgpt.com
			return "https://chatgpt.com/backend-api/codex/models"
		}
		return "https://api.openai.com/v1/models"
	default:
		return ""
	}
}

// UpstreamWarmupService 定期为可调度账号预热上游连接。
//
// 背景：连接池空闲连接在 idle_conn_timeout（默认 90-150s）后被回收，聊天
// 间隔超过该值的下一个请求需要重付 TCP+TLS+代理隧道握手（经代理时通常
// 数百毫秒），直接体现在首字延迟（TTFT）上。本服务每 interval 秒对每个
// 可调度账号发一个免鉴权轻量请求（401/404 即返回），保持连接常热。
// 请求走与真实流量完全相同的 DoWithTLS/Do 调用形态与池键，预热建立的
// 连接会被后续用户请求直接复用。
type UpstreamWarmupService struct {
	accountRepo  AccountRepository
	httpUpstream HTTPUpstream
	tlsFP        *TLSFingerprintProfileService
	cfg          *config.Config
	stopCh       chan struct{}
	stopOnce     sync.Once
	wg           sync.WaitGroup
}

func NewUpstreamWarmupService(
	accountRepo AccountRepository,
	httpUpstream HTTPUpstream,
	tlsFP *TLSFingerprintProfileService,
	cfg *config.Config,
) *UpstreamWarmupService {
	return &UpstreamWarmupService{
		accountRepo:  accountRepo,
		httpUpstream: httpUpstream,
		tlsFP:        tlsFP,
		cfg:          cfg,
		stopCh:       make(chan struct{}),
	}
}

func (s *UpstreamWarmupService) Start() {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.UpstreamWarmup.Enabled {
		slog.Info("upstream_warmup.service_disabled")
		return
	}
	interval := time.Duration(s.cfg.Gateway.UpstreamWarmup.IntervalSeconds) * time.Second
	if interval < 10*time.Second {
		interval = 45 * time.Second
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// 启动即预热一轮，缩短重启后的冷启动窗口
		s.warmOnce(context.Background())
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.warmOnce(context.Background())
			case <-s.stopCh:
				return
			}
		}
	}()
	slog.Info("upstream_warmup.service_started",
		"interval_seconds", int(interval.Seconds()),
		"platforms", s.platforms(),
	)
}

func (s *UpstreamWarmupService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *UpstreamWarmupService) platforms() []string {
	if s.cfg != nil && len(s.cfg.Gateway.UpstreamWarmup.Platforms) > 0 {
		return s.cfg.Gateway.UpstreamWarmup.Platforms
	}
	return []string{PlatformAnthropic, PlatformOpenAI}
}

func (s *UpstreamWarmupService) warmOnce(ctx context.Context) {
	if s.accountRepo == nil || s.httpUpstream == nil {
		return
	}
	platforms := make([]string, 0, len(s.platforms()))
	for _, p := range s.platforms() {
		if p == PlatformAnthropic || p == PlatformOpenAI {
			platforms = append(platforms, p)
		}
	}
	if len(platforms) == 0 {
		return
	}
	accounts, err := s.accountRepo.ListSchedulableByPlatforms(ctx, platforms)
	if err != nil {
		slog.Warn("upstream_warmup.list_failed", "error", err)
		return
	}
	maxAccounts := 0
	if s.cfg != nil {
		maxAccounts = s.cfg.Gateway.UpstreamWarmup.MaxAccounts
	}
	if maxAccounts > 0 && len(accounts) > maxAccounts {
		accounts = accounts[:maxAccounts]
	}

	concurrency := 4
	if s.cfg != nil && s.cfg.Gateway.UpstreamWarmup.Concurrency > 0 {
		concurrency = s.cfg.Gateway.UpstreamWarmup.Concurrency
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	warmed, skipped := 0, 0
	for i := range accounts {
		account := &accounts[i]
		url := upstreamWarmURL(account)
		if url == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if s.warmAccount(account, url) {
				warmed++
			} else {
				skipped++
			}
		}()
	}
	wg.Wait()
	if warmed > 0 || skipped > 0 {
		slog.Debug("upstream_warmup.cycle_completed", "warmed", warmed, "skipped", skipped)
	}
}

func (s *UpstreamWarmupService) warmAccount(account *Account, url string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/json")

	var resp *http.Response
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	// 与真实流量相同的调用形态：Anthropic OAuth 走 TLS 指纹路径（同一池键），
	// 其余走普通路径
	if account.Platform == PlatformAnthropic && account.IsTLSFingerprintEnabled() && s.tlsFP != nil {
		resp, err = s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.tlsFP.ResolveTLSProfile(account))
	} else {
		resp, err = s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	}
	if err != nil {
		// 预热失败不打 Warn：代理抖动/上游拒绝都是常态，下一轮再试
		slog.Debug("upstream_warmup.request_failed", "account_id", account.ID, "error", err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return true
}
