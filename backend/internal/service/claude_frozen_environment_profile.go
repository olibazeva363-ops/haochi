package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	claudeFrozenEnvironmentProfileExtraKey = "claude_frozen_environment_profile"
	claudeFrozenEnvironmentProfileSchema   = 1
)

// ClaudeFrozenEnvironmentProfile is the account-bound identity used only for
// gateway-generated Claude OAuth traffic. It is persisted in accounts.extra so
// cache expiry, restarts, and downstream client upgrades cannot silently alter it.
type ClaudeFrozenEnvironmentProfile struct {
	Schema                  int                     `json:"schema"`
	Source                  string                  `json:"source"`
	ClientID                string                  `json:"client_id"`
	DeviceID                string                  `json:"device_id"`
	UserAgent               string                  `json:"user_agent"`
	StainlessLang           string                  `json:"stainless_lang"`
	StainlessPackageVersion string                  `json:"stainless_package_version"`
	StainlessOS             string                  `json:"stainless_os"`
	StainlessArch           string                  `json:"stainless_arch"`
	StainlessRuntime        string                  `json:"stainless_runtime"`
	StainlessRuntimeVersion string                  `json:"stainless_runtime_version"`
	BetaSet                 []string                `json:"beta_set"`
	TLSFingerprintEnabled   bool                    `json:"tls_fingerprint_enabled"`
	TLSFingerprintProfileID int64                   `json:"tls_fingerprint_profile_id"`
	TLSFingerprintName      string                  `json:"tls_fingerprint_name,omitempty"`
	TLSFingerprint          *tlsfingerprint.Profile `json:"tls_fingerprint,omitempty"`
	ProxyID                 int64                   `json:"proxy_id,omitempty"`
	ProxyFingerprint        string                  `json:"proxy_fingerprint,omitempty"`
	FrozenAt                time.Time               `json:"frozen_at"`
}

func decodeClaudeFrozenEnvironmentProfile(raw any) (*ClaudeFrozenEnvironmentProfile, error) {
	if raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var profile ClaudeFrozenEnvironmentProfile
	if err := json.Unmarshal(encoded, &profile); err != nil {
		return nil, err
	}
	if err := validateClaudeFrozenEnvironmentProfile(&profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func validateClaudeFrozenEnvironmentProfile(profile *ClaudeFrozenEnvironmentProfile) error {
	if profile == nil || profile.Schema != claudeFrozenEnvironmentProfileSchema {
		return fmt.Errorf("unsupported frozen Claude environment profile schema")
	}
	if profile.Source != "simulated" || profile.FrozenAt.IsZero() {
		return fmt.Errorf("frozen Claude environment profile is not immutable")
	}
	if len(profile.ClientID) != 64 || len(profile.DeviceID) != 64 {
		return fmt.Errorf("frozen Claude environment profile has invalid identity")
	}
	for _, value := range []string{
		profile.UserAgent,
		profile.StainlessLang,
		profile.StainlessPackageVersion,
		profile.StainlessOS,
		profile.StainlessArch,
		profile.StainlessRuntime,
		profile.StainlessRuntimeVersion,
	} {
		if !isSafeFingerprintHeaderValue(value) {
			return fmt.Errorf("frozen Claude environment profile has invalid header value")
		}
	}
	if len(profile.BetaSet) == 0 {
		return fmt.Errorf("frozen Claude environment profile beta set is empty")
	}
	return nil
}

// claudeEnvironmentPresets 是冻结档案创建时可用的 OS/arch 组合。
// 防封角度：全部账号统一 Linux/arm64 会形成群体级统计画像（同一部署的账号
// 在上游看来运行环境完全一致）。UA / node 运行时版本保持一致（与内置
// Node.js 24.x TLS 指纹匹配），仅分散 OS/arch 维度--真实 CLI 用户群
// 也主要是 Linux 与 macOS 的组合。
var claudeEnvironmentPresets = []struct {
	OS   string
	Arch string
}{
	{"Linux", "arm64"},
	{"Linux", "x64"},
	{"Mac OS X", "arm64"},
	{"Mac OS X", "x64"},
}

// claudeEnvironmentPresetForAccount 按账号确定性选择环境组合：同一账号永远
// 得到同一画像（identity 派生自 account.ID/CreatedAt，与 device_id 同源），
// 不同账号近似均匀分散。
func claudeEnvironmentPresetForAccount(account *Account) (os string, arch string) {
	preset := &claudeEnvironmentPresets[0]
	if account != nil && account.ID > 0 {
		seed := stableClaudeFrozenIdentity(account, "environment-preset")
		// 取 SHA256 前 8 字符做模（避开低位规律）
		if idx, err := strconv.ParseUint(seed[:8], 16, 64); err == nil {
			preset = &claudeEnvironmentPresets[idx%uint64(len(claudeEnvironmentPresets))]
		}
	}
	return preset.OS, preset.Arch
}

func newClaudeFrozenEnvironmentProfile(account *Account, tlsProfile *tlsfingerprint.Profile) *ClaudeFrozenEnvironmentProfile {
	clientID := generateClientID()
	deviceID := generateClientID()
	if account != nil && account.ID > 0 {
		clientID = stableClaudeFrozenIdentity(account, "client")
		deviceID = stableClaudeFrozenIdentity(account, "device")
	}
	presetOS, presetArch := claudeEnvironmentPresetForAccount(account)
	profile := &ClaudeFrozenEnvironmentProfile{
		Schema:                  claudeFrozenEnvironmentProfileSchema,
		Source:                  "simulated",
		ClientID:                clientID,
		DeviceID:                deviceID,
		UserAgent:               defaultFingerprint.UserAgent,
		StainlessLang:           defaultFingerprint.StainlessLang,
		StainlessPackageVersion: defaultFingerprint.StainlessPackageVersion,
		StainlessOS:             presetOS,
		StainlessArch:           presetArch,
		StainlessRuntime:        defaultFingerprint.StainlessRuntime,
		StainlessRuntimeVersion: defaultFingerprint.StainlessRuntimeVersion,
		BetaSet:                 append([]string(nil), claude.FullClaudeCodeMimicryBetas()...),
		FrozenAt:                time.Now().UTC(),
	}
	if account != nil {
		profile.TLSFingerprintEnabled = account.IsTLSFingerprintEnabled()
		profile.TLSFingerprintProfileID = account.GetTLSFingerprintProfileID()
		if profile.TLSFingerprintProfileID == -1 {
			// Random TLS selection conflicts with a frozen account identity. Pin the
			// built-in profile without mutating the administrator's account setting.
			profile.TLSFingerprintProfileID = 0
		}
		if account.ProxyID != nil {
			profile.ProxyID = *account.ProxyID
		}
		if account.Proxy != nil {
			sum := sha256.Sum256([]byte(account.Proxy.URL()))
			profile.ProxyFingerprint = hex.EncodeToString(sum[:])
		}
	}
	if tlsProfile != nil {
		profile.TLSFingerprintName = strings.TrimSpace(tlsProfile.Name)
		profile.TLSFingerprint = cloneTLSFingerprintProfile(tlsProfile)
	}
	return profile
}

func stableClaudeFrozenIdentity(account *Account, scope string) string {
	seed := fmt.Sprintf("claude-frozen-environment-v1::%s::%d::%s::%s",
		scope, account.ID, account.CreatedAt.UTC().Format(time.RFC3339Nano), account.GetExtraString("account_uuid"))
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func cloneTLSFingerprintProfile(profile *tlsfingerprint.Profile) *tlsfingerprint.Profile {
	if profile == nil {
		return nil
	}
	clone := *profile
	clone.CipherSuites = append([]uint16(nil), profile.CipherSuites...)
	clone.Curves = append([]uint16(nil), profile.Curves...)
	clone.PointFormats = append([]uint16(nil), profile.PointFormats...)
	clone.SignatureAlgorithms = append([]uint16(nil), profile.SignatureAlgorithms...)
	clone.ALPNProtocols = append([]string(nil), profile.ALPNProtocols...)
	clone.SupportedVersions = append([]uint16(nil), profile.SupportedVersions...)
	clone.KeyShareGroups = append([]uint16(nil), profile.KeyShareGroups...)
	clone.PSKModes = append([]uint16(nil), profile.PSKModes...)
	clone.Extensions = append([]uint16(nil), profile.Extensions...)
	return &clone
}

func (p *ClaudeFrozenEnvironmentProfile) fingerprint() *Fingerprint {
	if p == nil {
		return nil
	}
	return &Fingerprint{
		// RewriteUserID consumes Fingerprint.ClientID as metadata.device_id.
		ClientID:                p.DeviceID,
		UserAgent:               p.UserAgent,
		StainlessLang:           p.StainlessLang,
		StainlessPackageVersion: p.StainlessPackageVersion,
		StainlessOS:             p.StainlessOS,
		StainlessArch:           p.StainlessArch,
		StainlessRuntime:        p.StainlessRuntime,
		StainlessRuntimeVersion: p.StainlessRuntimeVersion,
		UpdatedAt:               p.FrozenAt.Unix(),
	}
}

func (s *GatewayService) getOrCreateClaudeFrozenEnvironmentProfile(ctx context.Context, account *Account) (*ClaudeFrozenEnvironmentProfile, error) {
	if account == nil || !account.IsOAuth() {
		return nil, nil
	}
	if cached, ok := s.claudeFrozenProfiles.Load(account.ID); ok {
		return cached.(*ClaudeFrozenEnvironmentProfile), nil
	}
	key := strconv.FormatInt(account.ID, 10)
	value, err, _ := s.claudeFrozenProfileSF.Do(key, func() (any, error) {
		if cached, ok := s.claudeFrozenProfiles.Load(account.ID); ok {
			return cached.(*ClaudeFrozenEnvironmentProfile), nil
		}
		if account.Extra != nil {
			if raw, exists := account.Extra[claudeFrozenEnvironmentProfileExtraKey]; exists {
				profile, decodeErr := decodeClaudeFrozenEnvironmentProfile(raw)
				if decodeErr != nil {
					return nil, fmt.Errorf("decode frozen Claude environment profile: %w", decodeErr)
				}
				if profile != nil {
					s.claudeFrozenProfiles.Store(account.ID, profile)
					return profile, nil
				}
			}
		}

		tlsProfile := s.resolveTLSProfileForFrozenClaudeAccount(account, nil)
		profile := newClaudeFrozenEnvironmentProfile(account, tlsProfile)
		if err := validateClaudeFrozenEnvironmentProfile(profile); err != nil {
			return nil, err
		}
		if s.accountRepo == nil {
			return nil, fmt.Errorf("persist frozen Claude environment profile: account repository is unavailable")
		}
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
			claudeFrozenEnvironmentProfileExtraKey: profile,
		}); err != nil {
			return nil, fmt.Errorf("persist frozen Claude environment profile: %w", err)
		}
		s.claudeFrozenProfiles.Store(account.ID, profile)
		slog.Info("claude_frozen_environment_profile_created",
			"account_id", account.ID,
			"tls_profile", profile.TLSFingerprintName,
			"proxy_id", profile.ProxyID,
		)
		return profile, nil
	})
	if err != nil {
		return nil, err
	}
	return value.(*ClaudeFrozenEnvironmentProfile), nil
}

func (s *GatewayService) resolveTLSProfileForFrozenClaudeAccount(account *Account, profile *ClaudeFrozenEnvironmentProfile) *tlsfingerprint.Profile {
	if s == nil || s.tlsFPProfileService == nil || account == nil {
		return nil
	}
	if profile == nil {
		if !account.IsTLSFingerprintEnabled() {
			return nil
		}
		if id := account.GetTLSFingerprintProfileID(); id > 0 {
			return s.tlsFPProfileService.GetProfileByID(id)
		}
		return &tlsfingerprint.Profile{Name: "Built-in Default (Node.js 24.x)"}
	}
	if !profile.TLSFingerprintEnabled {
		return nil
	}
	if profile.TLSFingerprint != nil {
		return cloneTLSFingerprintProfile(profile.TLSFingerprint)
	}
	if profile.TLSFingerprintProfileID > 0 {
		if resolved := s.tlsFPProfileService.GetProfileByID(profile.TLSFingerprintProfileID); resolved != nil {
			return resolved
		}
	}
	return &tlsfingerprint.Profile{Name: "Built-in Default (Node.js 24.x)"}
}

func (s *GatewayService) rewriteClaudeFrozenMetadata(ctx context.Context, body []byte, account *Account, profile *ClaudeFrozenEnvironmentProfile) []byte {
	if s == nil || s.identityService == nil || account == nil || profile == nil {
		return body
	}
	accountUUID := account.GetExtraString("account_uuid")
	if accountUUID != "" {
		if rewritten, err := s.identityService.RewriteUserIDWithMasking(ctx, body, account, accountUUID, profile.DeviceID, profile.UserAgent); err == nil {
			return rewritten
		}
		return body
	}

	userID := gjson.GetBytes(body, "metadata.user_id")
	if !userID.Exists() || userID.Type != gjson.String {
		return body
	}
	parsed := ParseMetadataUserID(userID.String())
	if parsed == nil {
		return body
	}
	sessionID := generateUUIDFromSeed(fmt.Sprintf("%d::%s", account.ID, parsed.SessionID))
	rewritten := FormatMetadataUserID(profile.DeviceID, parsed.AccountUUID, sessionID, ExtractCLIVersion(profile.UserAgent))
	if out, err := sjson.SetBytes(body, "metadata.user_id", rewritten); err == nil {
		return out
	}
	return body
}

type claudeFrozenTLSProfileContextKey struct{}

func attachClaudeFrozenTLSProfile(req *http.Request, profile *tlsfingerprint.Profile) *http.Request {
	if req == nil || profile == nil {
		return req
	}
	return req.WithContext(context.WithValue(req.Context(), claudeFrozenTLSProfileContextKey{}, profile))
}

func tlsProfileForRequest(req *http.Request, fallback *tlsfingerprint.Profile) *tlsfingerprint.Profile {
	if req != nil {
		if profile, ok := req.Context().Value(claudeFrozenTLSProfileContextKey{}).(*tlsfingerprint.Profile); ok && profile != nil {
			return profile
		}
	}
	return fallback
}

// observeClaudeFrozenTransport 检测账号当前代理与冻结档案的不一致。
// 处理策略：把档案的 proxy 记录更新为当前值（device/client 身份保持不变，
// 等价于真实用户换了网络），同时将账号临时停调 10 分钟--既给上游一个
// 干净的静默窗口，也阻断「同 device_id 在两个出口 IP 间快速振荡」的
// 典型风控特征。身份由 stableClaudeFrozenIdentity 确定性派生，档案
// 更新不会改变 device_id/client_id。
func (s *GatewayService) observeClaudeFrozenTransport(ctx context.Context, account *Account, profile *ClaudeFrozenEnvironmentProfile) {
	if account == nil || profile == nil {
		return
	}
	var currentProxyID int64
	if account.ProxyID != nil {
		currentProxyID = *account.ProxyID
	}
	currentProxyFingerprint := ""
	if account.Proxy != nil {
		sum := sha256.Sum256([]byte(account.Proxy.URL()))
		currentProxyFingerprint = hex.EncodeToString(sum[:])
	}
	if currentProxyID == profile.ProxyID && currentProxyFingerprint == profile.ProxyFingerprint {
		return
	}

	slog.Warn("claude_frozen_environment_proxy_mismatch",
		"account_id", account.ID,
		"frozen_proxy_id", profile.ProxyID,
		"current_proxy_id", currentProxyID,
	)

	profile.ProxyID = currentProxyID
	profile.ProxyFingerprint = currentProxyFingerprint
	s.claudeFrozenProfiles.Store(account.ID, profile)
	if s.accountRepo != nil {
		if err := s.accountRepo.UpdateExtra(context.WithoutCancel(ctx), account.ID, map[string]any{
			claudeFrozenEnvironmentProfileExtraKey: profile,
		}); err != nil {
			slog.Warn("claude_frozen_environment_proxy_update_failed",
				"account_id", account.ID,
				"error", err,
			)
		}
		until := time.Now().Add(10 * time.Minute)
		reason := fmt.Sprintf("proxy changed (frozen proxy_id %d -> %d); account paused 10m to avoid IP oscillation", profile.ProxyID, currentProxyID)
		if err := s.accountRepo.SetTempUnschedulable(context.WithoutCancel(ctx), account.ID, until, reason); err != nil {
			slog.Warn("claude_frozen_environment_proxy_pause_failed",
				"account_id", account.ID,
				"error", err,
			)
		} else {
			slog.Info("claude_frozen_environment_proxy_paused",
				"account_id", account.ID,
				"until", until.Format(time.RFC3339),
			)
		}
	}
}

func (s *GatewayService) finalizeClaudeFrozenMimicRequest(req *http.Request, profile *ClaudeFrozenEnvironmentProfile, isStream bool, betaHeader string, betaShouldSet bool) error {
	if req == nil || profile == nil {
		return fmt.Errorf("finalize frozen Claude request: request and profile are required")
	}
	authorization := getHeaderRaw(req.Header, "authorization")
	if authorization == "" {
		return fmt.Errorf("finalize frozen Claude request: authorization is required")
	}
	req.Header = make(http.Header, 24)
	setHeaderRaw(req.Header, "authorization", authorization)
	setHeaderRaw(req.Header, "content-type", "application/json")
	setHeaderRaw(req.Header, "anthropic-version", "2023-06-01")
	applyClaudeCodeMimicHeaders(req, isStream)
	if s.identityService != nil {
		s.identityService.ApplyFingerprint(req, profile.fingerprint())
	}
	deleteHeaderAllForms(req.Header, "anthropic-beta")
	if betaShouldSet {
		setHeaderRaw(req.Header, "anthropic-beta", betaHeader)
	}
	deleteHeaderAllForms(req.Header, "traceparent")
	return nil
}
