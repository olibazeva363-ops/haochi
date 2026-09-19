package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/gin-gonic/gin"
)

type anthropicMetadataPayload struct {
	UserID string `json:"user_id"`
}

// replaceModelInBody 替换请求体中的model字段
// 优先使用定点修改，尽量保持客户端原始字段顺序。
func (s *GatewayService) replaceModelInBody(body []byte, newModel string) []byte {
	return ReplaceModelInBody(body, newModel)
}

type claudeOAuthNormalizeOptions struct {
	injectMetadata bool
	metadataUserID string
}

func marshalAnthropicMetadata(userID string) ([]byte, error) {
	return json.Marshal(anthropicMetadataPayload{UserID: userID})
}

func setJSONValueBytes(body []byte, path string, value any) ([]byte, bool) {
	next, err := sjson.SetBytes(body, path, value)
	if err != nil {
		return body, false
	}
	return next, true
}

func setJSONRawBytes(body []byte, path string, raw []byte) ([]byte, bool) {
	next, err := sjson.SetRawBytes(body, path, raw)
	if err != nil {
		return body, false
	}
	return next, true
}

func deleteJSONPathBytes(body []byte, path string) ([]byte, bool) {
	next, err := sjson.DeleteBytes(body, path)
	if err != nil {
		return body, false
	}
	return next, true
}

func ensureClaudeOAuthMetadataUserID(body []byte, userID string) ([]byte, bool) {
	if strings.TrimSpace(userID) == "" {
		return body, false
	}

	metadata := gjson.GetBytes(body, "metadata")
	if !metadata.Exists() || metadata.Type == gjson.Null {
		raw, err := marshalAnthropicMetadata(userID)
		if err != nil {
			return body, false
		}
		return setJSONRawBytes(body, "metadata", raw)
	}

	trimmedRaw := strings.TrimSpace(metadata.Raw)
	if strings.HasPrefix(trimmedRaw, "{") {
		existing := metadata.Get("user_id")
		if existing.Exists() && existing.Type == gjson.String && existing.String() != "" {
			return body, false
		}
		return setJSONValueBytes(body, "metadata.user_id", userID)
	}

	raw, err := marshalAnthropicMetadata(userID)
	if err != nil {
		return body, false
	}
	return setJSONRawBytes(body, "metadata", raw)
}

func normalizeClaudeOAuthRequestBody(body []byte, modelID string, opts claudeOAuthNormalizeOptions) ([]byte, string) {
	if len(body) == 0 {
		return body, modelID
	}

	out := body
	modified := false

	rawModel := gjson.GetBytes(out, "model")
	if rawModel.Exists() && rawModel.Type == gjson.String {
		normalized := claude.NormalizeModelID(rawModel.String())
		if normalized != rawModel.String() {
			if next, ok := setJSONValueBytes(out, "model", normalized); ok {
				out = next
				modified = true
			}
			modelID = normalized
		}
	}

	// 确保 tools 字段存在（即使为空数组）
	if !gjson.GetBytes(out, "tools").Exists() {
		if next, ok := setJSONRawBytes(out, "tools", []byte("[]")); ok {
			out = next
			modified = true
		}
	}

	if opts.injectMetadata && opts.metadataUserID != "" {
		if next, changed := ensureClaudeOAuthMetadataUserID(out, opts.metadataUserID); changed {
			out = next
			modified = true
		}
	}

	// temperature：真实 Claude Code CLI 总是发送 temperature（默认 1，客户端可覆盖）。
	// 之前的实现直接 delete 会导致 payload 缺字段，与真实 CLI 字节级不一致。
	// 策略：客户端传了什么就透传；没传则补默认 1。
	if !gjson.GetBytes(out, "temperature").Exists() {
		if next, ok := setJSONValueBytes(out, "temperature", 1); ok {
			out = next
			modified = true
		}
	}

	// max_tokens：真实 CLI 的默认值是 128000。缺失时补齐以对齐指纹。
	if !gjson.GetBytes(out, "max_tokens").Exists() {
		if next, ok := setJSONValueBytes(out, "max_tokens", 128000); ok {
			out = next
			modified = true
		}
	}

	// context_management：thinking.type 为 enabled/adaptive 时，真实 CLI 会自动
	// 附带 {"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}。
	// 客户端显式传了就透传；否则按 CLI 行为补齐。
	//
	// 注：本函数不按 model 名决定是否保留 context_management。“最终 beta
	// header 不含 context-management-2025-06-27 时 strip 字段”的能力维度
	// 对称约束由 sanitizeAnthropicBodyForBetaTokens 在 buildUpstreamRequest /
	// buildCountTokensRequest 层统一执行，与 Bedrock 路径的
	// sanitizeBedrockFieldsForBetaTokens 对称。
	if !gjson.GetBytes(out, "context_management").Exists() {
		thinkingType := gjson.GetBytes(out, "thinking.type").String()
		if thinkingType == "enabled" || thinkingType == "adaptive" {
			const cmDefault = `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`
			if next, ok := setJSONRawBytes(out, "context_management", []byte(cmDefault)); ok {
				out = next
				modified = true
			}
		}
	}

	// tool_choice：与 Parrot 对齐，不再无条件删除。
	// - 客户端传了 {"type":"tool","name":"X"} → 保留结构，name 由
	//   applyToolNameRewriteToBody 同步映射为假名
	// - 其他形态（auto/any/none）原样透传
	// 如果 body 里完全没有 tools（空数组），tool_choice 没意义时才删除
	if !gjson.GetBytes(out, "tools").IsArray() || len(gjson.GetBytes(out, "tools").Array()) == 0 {
		if gjson.GetBytes(out, "tool_choice").Exists() {
			if next, ok := deleteJSONPathBytes(out, "tool_choice"); ok {
				out = next
				modified = true
			}
		}
	}

	if !modified {
		return body, modelID
	}

	return out, modelID
}

func (s *GatewayService) buildOAuthMetadataUserID(parsed *ParsedRequest, account *Account, fp *Fingerprint, c *gin.Context) string {
	if parsed == nil || account == nil {
		return ""
	}
	if parsed.MetadataUserID != "" {
		return ""
	}

	userID := strings.TrimSpace(account.GetClaudeUserID())
	if userID == "" && fp != nil {
		userID = fp.ClientID
	}
	if userID == "" {
		// Fall back to a random, well-formed client id so we can still satisfy
		// Claude Code OAuth requirements when account metadata is incomplete.
		userID = generateClientID()
	}

	// session_id 用"会话级稳定种子"派生（账号 + 客户端区分因子 + 首条 user 文本）：
	// 随对话在尾部追加 messages 时保持不变，贴近真实 CC 进程级稳定的 session_id。
	// 不复用 GenerateSessionHash —— 后者是粘性路由键、按设计逐轮变化（见其测试）。
	var firstUserText string
	if parsed.Body != nil {
		firstUserText = extractFirstUserText(parsed.Body.Bytes())
	}
	discriminator := claudeOAuthSessionDiscriminator(c, parsed.SessionContext, fp)
	seed := buildStableSessionSeed(account.ID, discriminator, firstUserText)
	sessionID := generateSessionUUID(seed)

	// 根据指纹 UA 版本选择输出格式
	var uaVersion string
	if fp != nil {
		uaVersion = ExtractCLIVersion(fp.UserAgent)
	}
	accountUUID := strings.TrimSpace(account.GetExtraString("account_uuid"))
	return FormatMetadataUserID(userID, accountUUID, sessionID, uaVersion)
}

// applyClaudeCodeOAuthMimicryToBody adapts OAuth transport fields without
// injecting, relocating, or rewriting system and message instructions.
func (s *GatewayService) applyClaudeCodeOAuthMimicryToBody(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	model string,
) []byte {
	if account == nil || !account.IsOAuth() || len(body) == 0 {
		return body
	}

	normalizeOpts := claudeOAuthNormalizeOptions{}

	if s.identityService != nil && c != nil && c.Request != nil {
		if fp, err := s.identityService.GetOrCreateFingerprint(ctx, account.ID, c.Request.Header); err == nil && fp != nil {
			mimicMPT := false
			if s.settingService != nil {
				_, mimicMPT, _ = s.settingService.GetGatewayForwardingSettings(ctx)
			}
			if !mimicMPT {
				if uid := s.buildOAuthMetadataUserIDFromBody(ctx, account, fp, body, c); uid != "" {
					normalizeOpts.injectMetadata = true
					normalizeOpts.metadataUserID = uid
				}
			}
		}
	}

	body, _ = normalizeClaudeOAuthRequestBody(body, model, normalizeOpts)

	// Phase D+E+F: messages cache 策略 + 工具名混淆 + tools[-1] 断点
	// 对齐 Parrot transform_request 里剩余的字段级改写。顺序有语义约束：
	//   1) messages cache：仅在配置开启时清除客户端断点并注入代理断点
	//   2) tool rewrite：最后改 tools[*].name / tool_choice.name 并在 tools[-1]
	//      上打断点；mapping 存入 gin.Context 供响应侧 bytes.Replace 还原。
	body = s.rewriteMessageCacheControlIfEnabled(ctx, body)

	if rw := buildToolNameRewriteFromBody(body); rw != nil {
		body = applyToolNameRewriteToBody(body, rw)
		if c != nil {
			c.Set(toolNameRewriteKey, rw)
		}
	} else {
		body = applyToolsLastCacheBreakpoint(body)
	}

	return body
}

// buildOAuthMetadataUserIDFromBody 是 buildOAuthMetadataUserID 的变体，
// 适用于调用方手上没有 ParsedRequest 的场景（如 OpenAI 协议兼容层）。
//
// 与 buildOAuthMetadataUserID 的唯一区别：
//   - session hash 从 body 本体按同样规则重算，而不是读取 ParsedRequest 缓存值。
//   - 如果 body 里已经存在 metadata.user_id，则返回空（由 ensureClaudeOAuthMetadataUserID
//     自行决定是否覆盖）。
func (s *GatewayService) buildOAuthMetadataUserIDFromBody(
	ctx context.Context,
	account *Account,
	fp *Fingerprint,
	body []byte,
	c *gin.Context,
) string {
	_ = ctx
	if account == nil {
		return ""
	}
	if existing := gjson.GetBytes(body, "metadata.user_id").String(); existing != "" {
		return ""
	}

	userID := strings.TrimSpace(account.GetClaudeUserID())
	if userID == "" && fp != nil {
		userID = fp.ClientID
	}
	if userID == "" {
		userID = generateClientID()
	}

	// 与 buildOAuthMetadataUserID 一致：用会话级稳定种子，避免整 body 哈希导致
	// 每轮（甚至每个 token 变化）都重算出不同的 session_id。
	clientDiscriminator := claudeOAuthSessionDiscriminator(c, nil, fp)
	seed := buildStableSessionSeed(account.ID, clientDiscriminator, extractFirstUserText(body))
	sessionID := generateSessionUUID(seed)

	var uaVersion string
	if fp != nil {
		uaVersion = ExtractCLIVersion(fp.UserAgent)
	}
	accountUUID := strings.TrimSpace(account.GetExtraString("account_uuid"))
	return FormatMetadataUserID(userID, accountUUID, sessionID, uaVersion)
}

// buildStableSessionSeed 为伪装路径合成的 metadata.user_id session_id 生成"会话级稳定"种子。
//
// 真实 Claude Code 的 session_id 是进程级随机 UUID，在一段会话内跨请求保持不变。无状态代理
// 无法恢复该值，这里用"会话内不变的锚点"近似：账号 ID + 客户端区分因子 + 首条 user 消息文本。
// 对话在尾部追加 messages 时这三者都不变，因此 generateSessionUUID(seed) 跨轮稳定。
//
// 注意：粘性路由键 GenerateSessionHash 按设计逐轮变化（见其测试），本函数与之独立、互不影响。
// accountID 恒存在，故 seed 永不为空 —— 输出始终是确定性 UUID，而非随机值。
func buildStableSessionSeed(accountID int64, clientDiscriminator, firstUserText string) string {
	var b strings.Builder
	_, _ = b.WriteString(strconv.FormatInt(accountID, 10))
	_, _ = b.WriteString("::")
	_, _ = b.WriteString(clientDiscriminator)
	_, _ = b.WriteString("::")
	_, _ = b.WriteString(firstUserText)
	return b.String()
}

// sessionContextDiscriminator 把请求上下文（客户端 IP / 归一化 UA / API Key ID）拼成
// 一个跨客户端的区分因子，避免不同用户的相同首条消息派生出相同 session_id。
func sessionContextDiscriminator(sc *SessionContext) string {
	if sc == nil {
		return ""
	}
	if sc.APIKeyID > 0 {
		return "api-key:" + strconv.FormatInt(sc.APIKeyID, 10)
	}
	if ua := NormalizeSessionUserAgent(sc.UserAgent); ua != "" {
		return "client:" + ua
	}
	return "ip:" + strings.TrimSpace(sc.ClientIP)
}

func claudeOAuthSessionDiscriminator(c *gin.Context, sc *SessionContext, fp *Fingerprint) string {
	if c != nil && c.Request != nil {
		if sessionID := strings.TrimSpace(getHeaderRaw(c.Request.Header, "X-Claude-Code-Session-Id")); isSafeFingerprintHeaderValue(sessionID) {
			return hashClaudeOAuthDiscriminator("session", sessionID)
		}
	}
	if stable := sessionContextDiscriminator(sc); stable != "" && stable != "ip:" {
		return stable
	}
	if c != nil {
		if value, exists := c.Get("api_key"); exists {
			if apiKey, ok := value.(*APIKey); ok && apiKey != nil && apiKey.ID > 0 {
				return "api-key:" + strconv.FormatInt(apiKey.ID, 10)
			}
		}
		if c.Request != nil {
			credential := strings.TrimSpace(getHeaderRaw(c.Request.Header, "authorization"))
			if len(credential) >= 7 && strings.EqualFold(credential[:7], "Bearer ") {
				credential = strings.TrimSpace(credential[7:])
			}
			if credential == "" {
				credential = strings.TrimSpace(getHeaderRaw(c.Request.Header, "x-api-key"))
			}
			if credential != "" {
				return hashClaudeOAuthDiscriminator("api-key", credential)
			}
		}
	}
	if fp != nil && fp.ClientID != "" {
		return "device:" + fp.ClientID
	}
	return sessionContextDiscriminator(sc)
}

func hashClaudeOAuthDiscriminator(kind, value string) string {
	sum := sha256.Sum256([]byte(value))
	return kind + ":" + fmt.Sprintf("%x", sum[:])
}

// GenerateSessionUUID creates a deterministic UUID4 from a seed string.
func GenerateSessionUUID(seed string) string {
	return generateSessionUUID(seed)
}

func generateSessionUUID(seed string) string {
	if seed == "" {
		return uuid.NewString()
	}
	hash := sha256.Sum256([]byte(seed))
	bytes := hash[:16]
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}

type cacheControlPath struct {
	path string
	log  string
}

func collectCacheControlPaths(body []byte) (invalidThinking []cacheControlPath, messagePaths []string, toolPaths []string, systemPaths []string) {
	system := gjson.GetBytes(body, "system")
	if system.IsArray() {
		sysIndex := 0
		system.ForEach(func(_, item gjson.Result) bool {
			if item.Get("cache_control").Exists() {
				path := fmt.Sprintf("system.%d.cache_control", sysIndex)
				if item.Get("type").String() == "thinking" {
					invalidThinking = append(invalidThinking, cacheControlPath{
						path: path,
						log:  "[Warning] Removed illegal cache_control from thinking block in system",
					})
				} else {
					systemPaths = append(systemPaths, path)
				}
			}
			sysIndex++
			return true
		})
	}

	messages := gjson.GetBytes(body, "messages")
	if messages.IsArray() {
		msgIndex := 0
		messages.ForEach(func(_, msg gjson.Result) bool {
			content := msg.Get("content")
			if content.IsArray() {
				contentIndex := 0
				content.ForEach(func(_, item gjson.Result) bool {
					if item.Get("cache_control").Exists() {
						path := fmt.Sprintf("messages.%d.content.%d.cache_control", msgIndex, contentIndex)
						if item.Get("type").String() == "thinking" {
							invalidThinking = append(invalidThinking, cacheControlPath{
								path: path,
								log:  fmt.Sprintf("[Warning] Removed illegal cache_control from thinking block in messages[%d].content[%d]", msgIndex, contentIndex),
							})
						} else {
							messagePaths = append(messagePaths, path)
						}
					}
					contentIndex++
					return true
				})
			}
			msgIndex++
			return true
		})
	}

	tools := gjson.GetBytes(body, "tools")
	if tools.IsArray() {
		toolIndex := 0
		tools.ForEach(func(_, tool gjson.Result) bool {
			if tool.Get("cache_control").Exists() {
				toolPaths = append(toolPaths, fmt.Sprintf("tools.%d.cache_control", toolIndex))
			}
			toolIndex++
			return true
		})
	}

	return invalidThinking, messagePaths, toolPaths, systemPaths
}

// enforceCacheControlLimit 强制执行 cache_control 块数量限制（最多 4 个）
// 超限时优先移除工具断点，再移除 messages 断点，最后才移除 system 断点。
func enforceCacheControlLimit(body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	invalidThinking, messagePaths, toolPaths, systemPaths := collectCacheControlPaths(body)
	out := body
	modified := false

	// 先清理 thinking 块中的非法 cache_control（thinking 块不支持该字段）
	for _, item := range invalidThinking {
		if !gjson.GetBytes(out, item.path).Exists() {
			continue
		}
		next, ok := deleteJSONPathBytes(out, item.path)
		if !ok {
			continue
		}
		out = next
		modified = true
		logger.LegacyPrintf("service.gateway", "%s", item.log)
	}

	count := len(messagePaths) + len(toolPaths) + len(systemPaths)
	if count <= maxCacheControlBlocks {
		if modified {
			return out
		}
		return body
	}

	// 超限：优先从 tools 中移除，再从 messages 中移除，最后才从 system 中移除。
	remaining := count - maxCacheControlBlocks
	for i := len(toolPaths) - 1; i >= 0 && remaining > 0; i-- {
		path := toolPaths[i]
		if !gjson.GetBytes(out, path).Exists() {
			continue
		}
		next, ok := deleteJSONPathBytes(out, path)
		if !ok {
			continue
		}
		out = next
		modified = true
		remaining--
	}

	for _, path := range messagePaths {
		if remaining <= 0 {
			break
		}
		if !gjson.GetBytes(out, path).Exists() {
			continue
		}
		next, ok := deleteJSONPathBytes(out, path)
		if !ok {
			continue
		}
		out = next
		modified = true
		remaining--
	}

	for i := len(systemPaths) - 1; i >= 0 && remaining > 0; i-- {
		path := systemPaths[i]
		if !gjson.GetBytes(out, path).Exists() {
			continue
		}
		next, ok := deleteJSONPathBytes(out, path)
		if !ok {
			continue
		}
		out = next
		modified = true
		remaining--
	}

	if modified {
		return out
	}
	return body
}

// injectAnthropicCacheControlTTL1h 将已有 ephemeral cache_control 块的 ttl 强制写为 1h。
// 仅修改已经存在的 cache_control，不新增缓存断点。
func injectAnthropicCacheControlTTL1h(body []byte) []byte {
	return forceEphemeralCacheControlTTL(body, cacheTTLTarget1h)
}

func forceEphemeralCacheControlTTL(body []byte, ttl string) []byte {
	if len(body) == 0 || ttl == "" {
		return body
	}
	out := body
	var paths []string
	addPath := func(path string, value gjson.Result) {
		cc := value.Get("cache_control")
		if !cc.Exists() || cc.Get("type").String() != "ephemeral" {
			return
		}
		if cc.Get("ttl").String() == ttl {
			return
		}
		paths = append(paths, path+".cache_control.ttl")
	}

	if topCC := gjson.GetBytes(body, "cache_control"); topCC.Exists() && topCC.Get("type").String() == "ephemeral" && topCC.Get("ttl").String() != ttl {
		paths = append(paths, "cache_control.ttl")
	}

	system := gjson.GetBytes(body, "system")
	if system.IsArray() {
		idx := -1
		system.ForEach(func(_, block gjson.Result) bool {
			idx++
			addPath(fmt.Sprintf("system.%d", idx), block)
			return true
		})
	}

	messages := gjson.GetBytes(body, "messages")
	if messages.IsArray() {
		msgIdx := -1
		messages.ForEach(func(_, msg gjson.Result) bool {
			msgIdx++
			content := msg.Get("content")
			if !content.IsArray() {
				return true
			}
			contentIdx := -1
			content.ForEach(func(_, block gjson.Result) bool {
				contentIdx++
				addPath(fmt.Sprintf("messages.%d.content.%d", msgIdx, contentIdx), block)
				return true
			})
			return true
		})
	}

	tools := gjson.GetBytes(body, "tools")
	if tools.IsArray() {
		idx := -1
		tools.ForEach(func(_, tool gjson.Result) bool {
			idx++
			addPath(fmt.Sprintf("tools.%d", idx), tool)
			return true
		})
	}

	for _, path := range paths {
		if next, err := sjson.SetBytes(out, path, ttl); err == nil {
			out = next
		}
	}
	return out
}

func (s *GatewayService) shouldInjectAnthropicCacheTTL1h(ctx context.Context, account *Account) bool {
	if account == nil || !account.IsAnthropicOAuthOrSetupToken() || s == nil || s.settingService == nil {
		return false
	}
	return s.settingService.IsAnthropicCacheTTL1hInjectionEnabled(ctx)
}

// systemHasBillingAttributionBlock recognizes caller-provided Claude Code billing
// attribution for transport compatibility without modifying the system prompt.
func systemHasBillingAttributionBlock(body []byte) bool {
	system := gjson.GetBytes(body, "system")
	if !system.IsArray() {
		return false
	}
	found := false
	system.ForEach(func(_, item gjson.Result) bool {
		text := item.Get("text").String()
		if strings.HasPrefix(text, claudeCodeBillingHeaderPrefix) &&
			strings.Contains(text, claudeCodeEntrypointMarker) {
			found = true
			return false
		}
		return true
	})
	return found
}
