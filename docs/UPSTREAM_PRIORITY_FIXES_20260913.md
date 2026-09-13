# Sub2API 优先更新跟进记录（2026-09-13）

## 范围

本批在 TokenHub 上选择性回移 25 个优先 PR，并适配已有的 WS 断连计费、
图片分项价格和 Claude 冻结身份。按 PR 计数，部分 PR 共同实现一个功能。

| 项目 | 版本或快照 |
| --- | --- |
| 本地基线 | `58e13d54c0ae729ae814ad9fc5069bfa65beb380` |
| 上次上游审计快照 | `98d86915becae9fe9491a91ffc6defd5235c8d2b` |
| 本次上游审计快照 | `bdb42e22f81fcb633ff0a060961211dd2bcb515b` |
| 实施分支 | `codex/sub2api-priority-20260913` |
| 应用版本 | `0.2.1` |

完整评估及延期清单见 [本次更新评估](UPSTREAM_REVIEW_20260913.md)。
上游最新正式发布在审计时仍为 v0.2.4；本批包含该版本之后的 main 提交，
不是完整合并上游版本。本批无数据库迁移、依赖版本升级或连接池默认容量调整。

## 已回移的 25 个 PR

按实际回移顺序列出。每个本地提交均保留 `cherry picked from commit` 来源。

| PR | 上游提交 | 本地提交 | 改动 |
| --- | --- | --- | --- |
| [#6945](https://github.com/Wei-Shaw/sub2api/pull/6945) | `799e9938f` | `5fbd9b3a5` | 从最终可见目录查询单个模型，保留白名单、别名及固定账号约束。 |
| [#6943](https://github.com/Wei-Shaw/sub2api/pull/6943) | `4adcef4ff` | `62ba642db` | Claude 保留客户端 system 缓存断点，覆盖 count_tokens。 |
| [#6904](https://github.com/Wei-Shaw/sub2api/pull/6904) | `685e97a3a` | `5e3fc97bd` | 调度快照保留账号阈值和 Claude 窗口信息。 |
| [#6869](https://github.com/Wei-Shaw/sub2api/pull/6869) | `43f9383d4` | `ca07dc2e2` | 兼容完整 Codex heartbeat 自动化信封。 |
| [#6850](https://github.com/Wei-Shaw/sub2api/pull/6850) | `87e01596b` | `cbc871735` | 监控 SQL 聚合时间桶明确以 UTC 为基准。 |
| [#6924](https://github.com/Wei-Shaw/sub2api/pull/6924) | `2493b0dc3` | `d46abc848` | Codex User-Agent 在 Trim 和身份解析前检查控制字符。 |
| [#6916](https://github.com/Wei-Shaw/sub2api/pull/6916) | `0be30886a` | `242f2575c` | 订阅分配使用用户列表搜索并排除已删除用户。 |
| [#6965](https://github.com/Wei-Shaw/sub2api/pull/6965) | `8f9a9a255` | `795ff066c` | WS 常驻读取上游 ping，剔除失效或脏连接。 |
| [#6960](https://github.com/Wei-Shaw/sub2api/pull/6960) | `3fb03cdac` | `3ef32327d` | WS 会话状态与抢占按 Codex 线程、API Key 和执行范围隔离。 |
| [#6661](https://github.com/Wei-Shaw/sub2api/pull/6661) | `14029e50a` | `47d6a6d87` | Grok 媒体异常路径释放并发槽，视频查询保持原任务账号。 |
| [#6783](https://github.com/Wei-Shaw/sub2api/pull/6783) | `88011a6a2` | `a0c98da5c` | 减少大型 Responses 图片请求的缓冲扩容和重复解析。 |
| [#6971](https://github.com/Wei-Shaw/sub2api/pull/6971) | `7145484fd` | `5fbdb4c5d` | TTFT 详情显示并按首字耗时排序。 |
| [#6964](https://github.com/Wei-Shaw/sub2api/pull/6964) | `310f8b7fa` | `85dba069c` | Responses 转 Chat 保留子智能体 agent_message 正文。 |
| [#6890](https://github.com/Wei-Shaw/sub2api/pull/6890) | `4726bdd08` | `8ad37ab85` | OAuth 生图使用原生 Codex Images 端点，404/405 回退 Responses，支持图片缓存输入分价。 |
| [#7022](https://github.com/Wei-Shaw/sub2api/pull/7022) | `5948988aa` | `04fab9df8` | 避免重复注入 Accept-Encoding。 |
| [#7052](https://github.com/Wei-Shaw/sub2api/pull/7052) | `ba57ea914` | `082a96010` | 临时数据库、网络、429 或 5xx 错误不再误清登录态。 |
| [#7024](https://github.com/Wei-Shaw/sub2api/pull/7024) | `d2067668d` | `4430ff8ab` | 编辑订阅用户搜索词立即清除旧选择，避免误分配。 |
| [#7055](https://github.com/Wei-Shaw/sub2api/pull/7055) | `4ff3e6dfb` | `e4ce0db44` | 兑换后资料刷新失败仍保留兑换成功结果。 |
| [#7049](https://github.com/Wei-Shaw/sub2api/pull/7049) | `387321590` | `a7a24ca94` | WS 连接池变化后唤醒等待者重新选择连接。 |
| [#6995](https://github.com/Wei-Shaw/sub2api/pull/6995) | `eaa4083e2` | `67dbd55fd` | Claude 消息级 output_config 与最终 beta 请求头保持一致。 |
| [#7010](https://github.com/Wei-Shaw/sub2api/pull/7010) | `3a070ec1b` | `bcba2ed40` | OpenAI 资料查询更新客户端指纹并识别 Cloudflare challenge。 |
| [#7050](https://github.com/Wei-Shaw/sub2api/pull/7050) | `67845665d` | `ee270ed18` | 通知邮箱验证后按对象移除条目，避免异步索引竞态。 |
| [#7054](https://github.com/Wei-Shaw/sub2api/pull/7054) | `f0dd49778` | `efca9bbcc` | 用量 CSV 分页导出冻结筛选条件与文件名。 |
| [#7026](https://github.com/Wei-Shaw/sub2api/pull/7026) | `0a378f343` | `4bdbcbdc3` | API Key 配额重置采用后端实际返回的配额及状态。 |
| [#7057](https://github.com/Wei-Shaw/sub2api/pull/7057) | `9383e0b15` | `da0fbdaa1` | Codex 模型目录保留 max_context_window。 |

## TokenHub 适配

### WS 断连后的用量回收

#6965 的常驻 reader 会在等待上下文取消时关闭连接，与本地 HTTP 断连后
继续收集最终 usage 的逻辑冲突。新增仅供 HTTP 到 WS 转发使用的
`ReadMessageForDrain`：客户端取消当前等待时保留连接，再切换到有界后台
上下文继续读取；普通读取的取消以及读取/回收超时仍关闭连接。
v2 转发器也在首次切换回收上下文之后再判断是否将连接标为损坏。

保留默认 900 秒回收预算、`partial result + context.Canceled`、图片数量、
图片尺寸及输入尺寸、BillingModel、RequestedReasoningEffort 和原有用量持久化
上下文。namespace、sticky 和执行范围在身份改写之前计算。池容量系数仍为 1.0。

真实 coder WebSocket 连接回归先复现了完整 usage 被误报 incomplete、
不完整响应丢失图片数量的问题；修复后完整与不完整响应、取消后续读和
回收超时关闭连接均通过。

### 图片端点与缓存计费

同步及异步生图沿用既有鉴权、白名单和任务流程。原生端点的 404/405 回退
Responses；Responses 主模型受套餐限制时直接返回原错误，不误冷却请求的
图片模型。更新原 driver 测试，显式进入 Responses 回退路径并断言真实端点。

目录价格新增独立图片缓存输入价后，渠道/分组覆盖必须清除这项目录价，
使图片缓存 token 使用经营者配置的 `cache_read`。覆盖 flat、interval、
group 和 legacy 取价路径，支持显式 0，不增加数据库定价字段。

回归先复现了自定义价格应收 `$0.00045` 却收 `$0.00017`、配置免费仍收
`$0.00008`，修复后均通过。实际 RecordUsage 回归同时检查图片/文本分项、
倍率后扣款、公开请求模型名、JSONB 图片缓存明细以及共享价格/尺寸 map 不被修改。

### Claude 冻结身份与订阅界面

新增 15 个组合回归确认已有流程兼容新 beta：合并冻结 BetaSet 和新版 beta，
执行 policy drop、账号 header override、请求体净化，再计算 CCH 与最终请求头。
覆盖 messages、count_tokens、API-key override、system TTL 与 4 块缓存上限。
这些检查无需额外修改 Claude 生产逻辑。

订阅搜索测试按本地功能适配：保留用户搜索和选择清理回归，未带入此前未接入的
“用户链接到用量页”测试。保留 TokenHub 品牌、CSV 8 位费用精度及本地模型映射。

## 验证

日志目录：`.cache/upstream-priority-20260913/`（本地忽略产物）。本轮于
2026-09-13 开始，最终静态检查于 2026-09-14 凌晨完成（Asia/Shanghai）。

- 前端全量测试：264 个文件、1,921 项测试通过。
- 前端类型检查和生产构建、Lint 通过。
- 后端 handler、routes、middleware、repository、pkg 定向通过。
- WS、图片/缓存计费、大图请求、异步白名单、Claude/CCH 定向通过。
- 后端全量 `go test -p 2 -tags=unit ./...` 通过；service 包执行 176.619 秒。
- 后端全量 `go test -p 2 -tags=integration ./...` 通过，启用 `CI=true` 确保
  Docker 缺失时不会静默跳过；PostgreSQL/Redis 仓储测试执行 50.088 秒，
  service 包执行 128.436 秒。
- GHCR Compose 配置渲染通过。
- 部署脚本：Apple 脚本语法、Compose security、Gateway env、runtime resources、
  Caddy cache 共 5 项通过。Apple lifecycle 在 Windows Git Bash 因 macOS 专用
  `stat -f '%Lp'` 失败；该检查需由既有 `macos-15` CI 执行，本批未修改 Apple 脚本。
- Go 1.27.0、Linux amd64、CGO 关闭、`embed` 前端资源构建通过。
- 临时 PostgreSQL/Redis/app 容器 smoke 通过：自动初始化、健康检查、公开设置、
  TokenHub 品牌、首页及 JS 资源、upstream warmup 生命周期正常；0.43 秒优雅退出，
  退出码 0，临时容器和网络清理无错误。
- golangci-lint 2.13.0：`run --concurrency=2 --timeout=30m` 通过，`0 issues`。
- `gofmt`、`git diff --check` 通过；25 个 PR 与实际回移提交的记录对应核验通过。

Linux 本地验证产物：`.cache/upstream-priority-20260913/sub2api-linux-amd64`。
SHA-256：`9916a52097645b86cafc12580d0bb38bed9580926db23e2b8124eee43d14ac72`。
构建标识为 `da0fbdaa1-local-adaptations`，包含本记录所述生产适配；最终静态检查的
修正仅涉及测试 helper 的 error 返回位置和连接关闭返回值处理，不影响该产物。

## 未纳入项与发布边界

本次评估中其余 27 个按需/配套 PR、4 个暂缓 PR 尚未实施；上次评估延期的
21 项也继续保留。详细原因见本次评估与
[2026-09-11 跟进记录](UPSTREAM_PRIORITY_FIXES_20260911.md)。

- #7043/#7064 的 WS 容量系数和 mode router 容量行为需结合实际参数另评估。
- #6974 的 DeepSeek 价格与 2026-09-14 12:00（北京时间）切换规则仍未回移。
  若使用 DeepSeek 默认目录价，应在该时点前单独核实经营需要与供应商公告。
- 平台配额清理迁移、新平台、备份代理整包、多实例渠道缓存广播等保留原延期状态。

本次交付为本地分支及提交。后续发布应同步构建前后端，核实 GitHub CI、GHCR
镜像与运行容器提交一致，并检查 WS 空闲/断连用量、原生生图回退及自定义价格。
本地测试使用隔离依赖与模拟上游，不代表真实账号的 Cloudflare 质询、套餐权限
或服务器自动更新已经验证。监控 UTC 修复不会自动重算可能已错位的历史聚合数据。
