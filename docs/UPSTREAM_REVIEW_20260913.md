# Sub2API 最新更新跟进评估（2026-09-13）

## 对比范围

- 检查日期：2026-09-13，Asia/Shanghai。
- 用户仓库：`olibazeva363-ops/haochi`，当前 `origin/main = 58e13d54c0ae729ae814ad9fc5069bfa65beb380`；本地 HEAD 相同。
- 上次已审计上游快照：`98d86915becae9fe9491a91ffc6defd5235c8d2b`。
- 最新上游：`bdb42e22f81fcb633ff0a060961211dd2bcb515b`，合并时间 2026-09-12 20:13:03 +08:00；已通过直接 GitHub refs 与 upstream fetch 交叉核实。
- GitHub 最新正式发布仍是 [v0.2.4](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.4)，发布时间 2026-09-09。下列是正式版之后合入 main 的改动，不能称为已发布 v0.2.5。
- [本轮上游差异](https://github.com/Wei-Shaw/sub2api/compare/98d86915becae9fe9491a91ffc6defd5235c8d2b...bdb42e22f81fcb633ff0a060961211dd2bcb515b)：56 个 first-parent 已合并 PR，357 个文件。
- 评估结果：**优先 25 项、按需/配套 27 项、暂缓 4 项**。按 PR 计数；同一功能可能需要数个 PR，不能把这些数字当作独立功能数量。
- 本轮是源码差异及已有测试内容审计，没有实施回移、运行新测试或核实服务器启用的平台和运行参数。上一批 9 项更新已在用户 main 中，不重复计数。

## 优先跟进：25 项

| PR / 上游提交 | 更新 | 实际收益 | 本地适配与边界 |
| --- | --- | --- | --- |
| [#6965](https://github.com/Wei-Shaw/sub2api/pull/6965) / `8f9a9a255` | WS 空闲连接与重试 | 常驻读取上游 ping，剔除失效/脏连接，重试换新连接。 | 与 #6960、#7049 组成连接池修复包；保留 900 秒断连用量回收、partial result 和取消语义。 |
| [#6960](https://github.com/Wei-Shaw/sub2api/pull/6960) / `3fb03cdac` | WS 多智能体隔离 | 按 thread、API Key 和执行范围隔离，减少父子智能体、后台 memory/guardian 请求互相抢占。 | 保留本地 namespace、粘性规则、图片计费元数据和断连用量回收。 |
| [#7049](https://github.com/Wei-Shaw/sub2api/pull/7049) / `387321590` | WS 排队唤醒 | 任一连接释放或建连完成后重新选择，避免有空闲连接却仍等超时。 | 回归取消、强亲和和排队时长；建议按 #6965 → #6960 → #7049 整合。 |
| [#7057](https://github.com/Wei-Shaw/sub2api/pull/7057) / `9383e0b15` | Codex 最大上下文 | 完整传递 max_context_window，避免目录将最大上下文缩成默认窗口。 | 保留固定账号目录、模型映射和长上下文计费；旧快照仍保守回退。 |
| [#6964](https://github.com/Wei-Shaw/sub2api/pull/6964) / `310f8b7fa` | Codex 子智能体消息桥接 | Responses 转 Chat 时保留 agent_message 任务正文。 | 兼容 provider 将正文放在 encrypted_content 的情况；保留工具调用和多模态处理。 |
| [#6869](https://github.com/Wei-Shaw/sub2api/pull/6869) / `43f9383d4` | Codex 自动化信封 | 兼容 heartbeat 中成对的 current_time_iso、instructions 字段。 | 保留旧格式及 call_id、previous_response_id 等校验。 |
| [#6924](https://github.com/Wei-Shaw/sub2api/pull/6924) / `2493b0dc3` | Codex User-Agent 校验 | 在 Trim 前拒绝控制字符，不合法动态 UA 回退内置身份。 | 本地仍先 Trim；保留身份归一化规则。 |
| [#6945](https://github.com/Wei-Shaw/sub2api/pull/6945) / `799e9938f` | 单模型查询接口 | 新增 /v1/models/:model 与 /models/:model，供客户端查询单个可见模型。 | 必须基于最终目录执行白名单、别名和 pinned 检查，不能复用模型集合 ETag。 |
| [#7022](https://github.com/Wei-Shaw/sub2api/pull/7022) / `5948988aa` | 压缩请求头兼容 | 修正 Accept-Encoding 的 wire casing，避免 Go 重复注入 gzip。 | 局部修复，保留真实转发头回归。 |
| [#7010](https://github.com/Wei-Shaw/sub2api/pull/7010) / `3a070ec1b` | OpenAI 账号资料查询 | 更新客户端指纹并识别 Cloudflare challenge，改善隐私设置和订阅资料查询的诊断。 | 对 CF 403 有针对性；实际恢复情况仍需以账号查询结果验证。 |
| [#6783](https://github.com/Wei-Shaw/sub2api/pull/6783) / `88011a6a2` | 大图请求内存 | 减少大型 Responses 图片请求的缓冲扩容、JSON 全量解码和重复字符串复制。 | 保留请求大小限制、压缩解码、重复键语义及工具/推理参数规范化；没有本地性能实测。 |
| [#6890](https://github.com/Wei-Shaw/sub2api/pull/6890) / `4726bdd08` | OAuth 原生生图 | 已知 image 1.5/2/2.5 模型走 Codex Images 原生生成/编辑端点，404/405 回退 Responses。 | 独立适配：同步/异步白名单、composite 别名、任务生命周期、断连 usage、图片缓存输入分价要贯通；此 PR 无迁移。 |
| [#6943](https://github.com/Wei-Shaw/sub2api/pull/6943) / `4adcef4ff` | Claude system 缓存断点 | 关闭 system 注入时保留客户端 system[].cache_control，count_tokens 同步修复。 | 同时执行 4 块缓存断点上限；保留冻结环境、最终 beta 净化和 CCH 签名顺序。 |
| [#6995](https://github.com/Wei-Shaw/sub2api/pull/6995) / `eaa4083e2` | Claude 中途 output_config | 兼容消息级 output_config 及相应 beta，避免部分客户端会话中途 400。 | 按最终请求头决定保留或清理消息级字段；不删除顶层 output_config 或正文。 |
| [#6661](https://github.com/Wei-Shaw/sub2api/pull/6661) / `14029e50a` | Grok 媒体并发槽 | 资格拒绝、断连等路径释放 slot，视频查询固定原任务账号。 | 确保仅释放一次，保留本地视频后计费、429 和利润门处理；不依赖媒体资格 UI 大功能。 |
| [#7052](https://github.com/Wei-Shaw/sub2api/pull/7052) / `ba57ea914` | 登录态稳定性 | 数据库临时错误不再伪装成用户不存在；刷新遇到断网、429、5xx 时保留登录凭据。 | 与本地旧会话保护兼容，需前后端同步更新。 |
| [#6904](https://github.com/Wei-Shaw/sub2api/pull/6904) / `685e97a3a` | 调度阈值缓存 | 调度快照保留账号阈值、Claude 共享窗口及专属窗口字段。 | 修复候选调度错误回退平台阈值；上线确认缓存刷新。 |
| [#6850](https://github.com/Wei-Shaw/sub2api/pull/6850) / `87e01596b` | 监控 UTC 聚合 | SQL 时间桶 origin 明确为 UTC，避免非 UTC 数据库会话下聚合与 Go 截断不一致。 | 无迁移；如历史数据已有错位，需另查是否要重算受影响时间桶。 |
| [#7026](https://github.com/Wei-Shaw/sub2api/pull/7026) / `0a378f343` | API Key 配额重置显示 | 采用后端返回的配额和状态，防止重置后仍显示耗尽或更新错选中 Key。 | 前端局部修复，不改变额度规则。 |
| [#7054](https://github.com/Wei-Shaw/sub2api/pull/7054) / `f0dd49778` | 用量 CSV 导出一致性 | 开始导出时冻结筛选和文件名，避免分页期间切换筛选造成数据混合。 | 保留 CSV 转义和现有 8 位成本导出精度。 |
| [#6971](https://github.com/Wei-Shaw/sub2api/pull/6971) / `7145484fd` | 首字耗时详情 | 运维 TTFT 弹窗显示并按 first_token_ms 排序，修复拿总耗时排首字问题。 | 前后端字段和排序一同接入。 |
| [#7024](https://github.com/Wei-Shaw/sub2api/pull/7024) / `d2067668d` | 订阅分配目标 | 编辑搜索词立即清空旧用户选择，避免 debounce 间隙误分配。 | 与 #6916 一起回归搜索、选择、提交。 |
| [#6916](https://github.com/Wei-Shaw/sub2api/pull/6916) / `0be30886a` | 订阅用户搜索 | 使用用户列表搜索，修复依赖用量搜索造成部分用户找不到。 | 保留模型广场、订阅授权和本地用户搜索行为。 |
| [#7055](https://github.com/Wei-Shaw/sub2api/pull/7055) / `4ff3e6dfb` | 兑换成功反馈 | 兑换成功后的用户资料刷新失败只显示刷新警告，不误报兑换失败。 | 保留已经成功的兑换结果和订阅刷新逻辑。 |
| [#7050](https://github.com/Wei-Shaw/sub2api/pull/7050) / `67845665d` | 余额通知邮箱竞态 | 校验完成按邮箱条目对象移除，避免并行操作使索引变化后删错条目。 | 仅修前端异步状态，不改变邮箱验证接口。 |

## 按需和配套：27 项

| PR / 上游提交 | 更新 | 实际收益 | 本地适配与边界 |
| --- | --- | --- | --- |
| [#7064](https://github.com/Wei-Shaw/sub2api/pull/7064) / `bdb42e22f` | 新版 WS 池容量 | mode router v2 的 ctx_pool 遵循既有动态系数和硬上限。 | 先查实际启用模式和显式参数；系数影响池连接数，不提升在飞请求并发。 |
| [#7043](https://github.com/Wei-Shaw/sub2api/pull/7043) / `2a4f3d1a7` | WS 默认容量系数 | OAuth/API Key 默认系数从 1 调到 5。 | 会增加上游连接和资源占用；与 #7064 配套评估，不能当无行为变化的小修。 |
| [#5833](https://github.com/Wei-Shaw/sub2api/pull/5833) / `a59f0c7fa` | Fast 缺省档位策略 | 全局策略可按 user/model/scope 匹配 missing service_tier。 | 本地分组 ForceOpenAIFast 已能强制缺省请求为 priority；新增价值是全局细粒度规则。 |
| [#7011](https://github.com/Wei-Shaw/sub2api/pull/7011) / `f78c4b241` | compact 默认模型 | 默认 gpt-5.4 改为 gpt-5.5。 | 在用 compact 且账号支持 5.5 时跟；显式配置优先，并检查 TokenHub GHCR 模板。 |
| [#6988](https://github.com/Wei-Shaw/sub2api/pull/6988) / `642d20b8a` | 模型显示名称 | 目录和账号测试保留上游 display_name。 | 低成本体验配套，保留本地 OAuth 图片模型选项和映射。 |
| [#6989](https://github.com/Wei-Shaw/sub2api/pull/6989) / `623c32e39` | OpenAI 套餐标签 | 区分 Pro 20x/5x、Business Standard/Premium。 | 展示和手工选项改进，不等于新增套餐调度能力；保留原始凭据。 |
| [#6929](https://github.com/Wei-Shaw/sub2api/pull/6929) / `6206ce940` | Gemini 错误观测 | 识别原生 HTTP 200 内错误、空流和内容过滤，正确进入 Ops。 | 经营 Gemini 时建议跟；主要修观测，不是自动重试，也不把客户端 HTTP 200 改成失败码。 |
| [#6575](https://github.com/Wei-Shaw/sub2api/pull/6575) / `6254aba5e` | Antigravity Gemini 3.7/3.8 | 补默认目录、映射和离线兜底价格。 | 本地已有思考档位别名计价归一化，但并非整项已具备；按账号能力和分组白名单开放。 |
| [#6968](https://github.com/Wei-Shaw/sub2api/pull/6968) / `40cf3f50a` | DeepSeek 图片输入声明 | Codex 目录正确声明 vision-exp 的 image 输入能力。 | 在用 DeepSeek 视觉时跟；保留显式 text-only 和混合账号能力交集约束。 |
| [#6974](https://github.com/Wei-Shaw/sub2api/pull/6974) / `fd300ab6f` | DeepSeek V4.1 Flash 计价 | 更新默认价格及 2026-09-14 北京时间 12:00 后 Pro 按 Flash 价计费的时点。 | 经营 DeepSeek 则提升为优先；保留 PricingAt、历史补账与自定义售价，另回归 TokenHub 图片分价和倍率。 |
| [#7001](https://github.com/Wei-Shaw/sub2api/pull/7001) / `c7ed61424` | 全平台 Token 统计 | 移除仅统计 gpt 前缀的过滤，覆盖其它平台及此前漏掉的 o3。 | 运维展示小改动，继续按平台和分组过滤。 |
| [#6969](https://github.com/Wei-Shaw/sub2api/pull/6969) / `75b7dd1e0` | 峰谷配置错误提示 | 无效峰谷配置返回 400 和明确错误码。 | 本地已有校验；这项主要改善反馈，并非新增计费验证。 |
| [#6917](https://github.com/Wei-Shaw/sub2api/pull/6917) / `e71d291b3` | 监控地址路径前缀 | 支持 /anthropic、/v1 等 base path，避免重复拼 /v1。 | 带路径的中转地址有直接收益；保留公网解析与 URL 校验。 |
| [#6843](https://github.com/Wei-Shaw/sub2api/pull/6843) / `8efe2fd8c` | 成本展示精度 | 用量浮层由 6 位增至 8 位，减少小费用看成 0。 | 本地 CSV 已是 8 位；此项只改展示，不改变扣费。 |
| [#6845](https://github.com/Wei-Shaw/sub2api/pull/6845) / `0aac71c6e` | 错误详情可读性 | 运维详情优先展示时间和错误摘要。 | 纯界面配套，其它表格维持现有顺序。 |
| [#6986](https://github.com/Wei-Shaw/sub2api/pull/6986) / `dfa83fbe9` | 站点充值/订阅入口模式 | 支持仅充值、仅订阅或两者同时展示。 | 单独功能包，涉及 46 文件；隐藏入口不会停止已有订阅扣费或关闭订阅 API。 |
| [#7023](https://github.com/Wei-Shaw/sub2api/pull/7023) / `f2b51e3b2` | 资料/密码错误提示 | 使用统一 API 错误提取，显示实际失败原因。 | 本地仍只读 detail；可随前端修复一起跟。 |
| [#7025](https://github.com/Wei-Shaw/sub2api/pull/7025) / `329641a86` | 代理部分导入刷新 | 导入部分成功时关闭弹窗也刷新列表。 | 无需为此带入整包代理关系修改。 |
| [#7053](https://github.com/Wei-Shaw/sub2api/pull/7053) / `99b93b298` | 代理筛选分页 | 切换协议/状态后回到第一页。 | 避免高页码下筛选后看不到实际存在的数据。 |
| [#7012](https://github.com/Wei-Shaw/sub2api/pull/7012) / `e67ffda7a` | 易支付渠道代码 | 允许 upstreamType 含点号。 | 仅使用带点号的上游通道代码时需要，前后端校验同步。 |
| [#6973](https://github.com/Wei-Shaw/sub2api/pull/6973) / `9c30951e4` | 续费弹窗滚动 | 套餐较多时弹窗内容可滚动。 | 页面小修，本地仍旧布局。 |
| [#6915](https://github.com/Wei-Shaw/sub2api/pull/6915) / `0116c5a1e` | 异步任务计数文案 | “已处理”改为“异步已处理”。 | 只改说明，不改变异步执行或统计。 |
| [#6912](https://github.com/Wei-Shaw/sub2api/pull/6912) / `67d3a896b` | 订阅兑换码期限 | 前端输入上限 365 天放宽至 36500 天。 | 只在需要长期订阅码时有价值，不自动修改已有订阅。 |
| [#6913](https://github.com/Wei-Shaw/sub2api/pull/6913) / `324a47e2b` | 批量删除用户入口 | 管理端新增选择后批量删除、分别反馈成功和失败。 | 属于可选管理功能，按实际需要决定；本轮不会执行任何删除。 |
| [#6847](https://github.com/Wei-Shaw/sub2api/pull/6847) / `f4dd88b00` | 自定义页面按钮显隐 | 可隐藏嵌入页面的外部打开按钮。 | 与上次延期的 #6706 可拖拽按钮有关，选择性接入需适配现有组件。 |
| [#6499](https://github.com/Wei-Shaw/sub2api/pull/6499) / `264cbbec2` | 初始化数据库连接 | 先连配置数据库，只有数据库不存在才尝试 postgres bootstrap。 | 外部托管 PostgreSQL 或无维护库权限时实用；现有正常部署不是紧急项。 |
| [#7006](https://github.com/Wei-Shaw/sub2api/pull/7006) / `bae37a00f` | 初始化测试清理 | 补关闭测试连接的错误处理。 | 主要是 #6499 的测试/静态检查配套，不是独立用户功能。 |

## 暂缓：4 项

| PR / 上游提交 | 更新 | 实际收益 | 本地适配与边界 |
| --- | --- | --- | --- |
| [#6954](https://github.com/Wei-Shaw/sub2api/pull/6954) / `2dff7af0f` | 平台配额存储重构 | 无任何限额的配额行不再保留，flusher 改为只更新既有行。 | 含 DELETE 迁移；清空限额会放弃该平台累计配额量，重新设置从新窗口起算，须单独审计数据与语义。 |
| [#6747](https://github.com/Wei-Shaw/sub2api/pull/6747) / `cdb5cfaf6` | OpenCode Go/Zen 新平台 | 独立平台、协议分流、额度窗口、管理界面与监控接线。 | 108 文件和 238 迁移，并关联此前未纳入的 MiniMax 237；不是普通 OpenCode 客户端兼容补丁。 |
| [#7062](https://github.com/Wei-Shaw/sub2api/pull/7062) / `749cd7c35` | MiniMax 监控校验 | 允许 MiniMax provider 进入监控和探活校验。 | 当前尚未接入 MiniMax 大功能，暂不单独摘取。 |
| [#6902](https://github.com/Wei-Shaw/sub2api/pull/6902) / `501cc19d0` | Apple Container 自更新 | 改进 Apple 容器部署升级流程。 | 当前 TokenHub 使用 GHCR/Linux 部署，这项不适用。 |

## DeepSeek 时间敏感项

#6974 的上游代码引用 [DeepSeek 2026-09-10 公告](https://api-docs.deepseek.com/news/news260910)，将 Flash 默认输入/输出/缓存读取价格从每百万 token 的 $0.22 / $0.66 / $0.007 改为 $0.15 / $0.60 / $0.003，并加入 **2026-09-14 北京时间 12:00** 起 Pro 请求按 Flash 默认价结算的规则。

本地目前仍是旧默认价。若经营 DeepSeek，建议将此项从按需提升为本批优先，优先数即为 26；分组/渠道自定义售价不应被覆盖。这里确认的是上游代码的规则与本地差异，尚未独立核实供应商最新公告或真实账单。

## 上次延期项目复查

上次 `v0.2.3..v0.2.4` 的 21 项延期记录继续保留在 [2026-09-11 升级记录](UPSTREAM_PRIORITY_FIXES_20260911.md)，不混入本轮新增 56 项。

- **代理整包 #6810/#6811/#6815/#6816/#6836**：本地仍有部分更新清空遗漏字段、备份代理自引用方向、只允许一次过期回退等旧逻辑。若使用备份代理，仍值得成组跟进；需要保留 TokenHub 的冻结代理、探测失效、账号绑定和生成代码。新增的 #7025/#7053 只是导入刷新和分页小修，不能替代这组后端修复。
- **#6320 持久化冷却同步**：不直接套用。上游会在持久化冷却字段为空时清除内存 block，这与本地 9 月 11 日保留的“明确额度耗尽、缺少 reset 时短暂内存暂停”冲突。先区分状态来源再适配。
- **#6281 HTTP/2 保活**：本地 OpenAI HTTP/2 已有 15 秒 idle + 15 秒 ping timeout。上游会改为 10 秒 + 5 秒并增加 LongStream profile，但最新代码中该 profile 尚无生产入口设置调用；不能将其描述成所有平台新增长流保活。它与本轮 WS ping #6965 不是同一问题。
- **#6235 渠道缓存广播**：多实例环境仍有价值；本地尚无 Redis 渠道缓存广播，单实例下不是本批重点。需要适配本地 Wire 接线。
- **#6812 模型广场订阅可见性**：本地仍只使用普通授权集合，没将有效订阅并入可见集合。若用户已订阅却在广场看不到专属组，可以单独补。
- **#6758 MiniMax 及其它可选功能**：继续按业务需要评估；新增 #6747/#7062 不能绕过此前平台接入决策。
- **#6775 Windows ZIP 句柄**：上次已核实核心修复有本地等效，不重复当作缺失项。其余旧管理端、展示和环境功能继续参考原延期表。

## 建议实施顺序与验证重点

1. 先做登录、请求头、Codex 小范围兼容、调度快照和后台状态修复。
2. 单独整合 WS `#6965 → #6960 → #7049`，验证多智能体隔离、空闲 ping、队列唤醒、取消和完整/不完整用量回收。容量 `#7043/#7064` 另按实际配置接入。
3. 单独整合生图 `#6783/#6890`，保留异步任务、白名单、公开模型名和图片输入/缓存输入/输出的分项计价；验证原生端点及 404/405 回退。
4. Claude `#6943/#6995` 保持冻结身份、最终 beta/header override、请求体净化与 CCH 签名的既有顺序。
5. 若有 DeepSeek 业务，优先加入 `#6974`，覆盖切换前后、历史 PricingAt、自定义价格和峰谷倍率。
6. 选定范围后再运行定向回归、全量前后端检查和部署构建；此次审查不代表兼容性已经通过执行验证。
