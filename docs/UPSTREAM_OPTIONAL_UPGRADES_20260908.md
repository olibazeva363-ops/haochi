# TokenHub 按需补丁与配套升级

## 范围

- 日期：2026-09-08。
- 分支：`codex/sub2api-priority-fixes-20260908`。
- 本阶段基线：`44280114c`，已完成第一批 19 项优先补丁。
- 本阶段接入剩余 7 项按需补丁、3 项配套升级，以及 2 项必要的模型发现前置补丁。
- 两阶段共完成原清单 29 项，实际摘取 31 个上游 PR。保持选择性回移，`VERSION` 仍为 `0.2.1`。
- 第一阶段记录见 [UPSTREAM_PRIORITY_FIXES_20260908.md](UPSTREAM_PRIORITY_FIXES_20260908.md)。其中“没有数据库迁移”和“按需候选未纳入”描述的是第一阶段截止时的状态。

## 上游来源

| 类别 | PR | 上游合并提交 | 本地摘取提交 | 内容 |
| --- | --- | --- | --- | --- |
| 按需 | [#6637](https://github.com/Wei-Shaw/sub2api/pull/6637) | `b9ebc8f20` | `66965c50b` | Gemini Flash thinking 档位别名使用一致的计费价格。 |
| 按需 | [#6705](https://github.com/Wei-Shaw/sub2api/pull/6705) | `c05bc4d3c` | `a90d45592` | 用量页 API Key 筛选加载全部密钥。 |
| 按需 | [#6429](https://github.com/Wei-Shaw/sub2api/pull/6429) | `2b1e5e7f5` | `b59bfeaf3` | 补 GLM 5.3 Flash 兜底定价。 |
| 按需 | [#6700](https://github.com/Wei-Shaw/sub2api/pull/6700) | `3eb5a84ea` | `91e8f14ec` | DeepSeek 账号成本与用户计费使用同一峰谷时间。 |
| 按需 | [#6673](https://github.com/Wei-Shaw/sub2api/pull/6673) | `88b697d5d` | `f1266005f` | pg_dump 备份持有数据库迁移锁，关闭读取器时释放。 |
| 按需 | [#6738](https://github.com/Wei-Shaw/sub2api/pull/6738) | `66cd8a72b` | `2b10c2675` | 区间倍率展示与 1 小时缓存写入价格配套修复。 |
| 按需 | [#6767](https://github.com/Wei-Shaw/sub2api/pull/6767) | `14e0a49e1` | `f169149b5` | Ollama Cloud DeepSeek 输出 token 上限及 Anthropic Bearer 认证兼容。 |
| 配套 | [#6658](https://github.com/Wei-Shaw/sub2api/pull/6658) | `f8351e9e3` | `c7698389a` | 分组模型白名单同时约束模型目录和实际请求。 |
| 配套 | [#6772](https://github.com/Wei-Shaw/sub2api/pull/6772) | `b439dddfd` | `0d0d762e9` | 账号测试使用真实模型发现结果及显示名称。 |
| 配套 | [#6791](https://github.com/Wei-Shaw/sub2api/pull/6791) | `8fa67d477` | `05d2359e2` | 修复白名单迁移已登记但实际数据库列不一致的状态。 |
| 前置 | [#6662](https://github.com/Wei-Shaw/sub2api/pull/6662) | `a3aa9bae6` | `cc0f947ee` | 固定账号的标准模型目录与 Codex 目录共享发现和缓存策略。 |
| 前置 | [#6680](https://github.com/Wei-Shaw/sub2api/pull/6680) | `eba85dc47` | `8039b107b` | 混合映射与未映射账号时补齐模型目录。 |

每个摘取提交均通过 `cherry picked from commit` 保留完整上游 SHA。

## 本地适配

- 图片同步生成、编辑和异步提交在解析默认模型后检查白名单，禁止省略 `model` 绕过默认 `gpt-image-2` 的准入限制。检查 `clientRequestedModel`，保留 composite 公开别名；被拒绝请求不访问上游、不创建异步任务，并记录运维拒绝原因。
- Grok Realtime 在补全默认 `grok-voice-latest` 后、连接上游 WebSocket 前检查白名单。TTS、STT 和独立搜索等不提供模型选择的专用入口保持原行为，不把内部选号名称或计费标签当作新的准入模型。
- 文本接口要求 `model` 非空时同步拒绝纯空白字符串，防止 Chat Completions 的空格模型经 Codex 规范化回退为默认模型。合法请求的原模型名保持不变。
- 本地 Claude 点号别名会被规范为官方日期模型名。白名单保留去掉 `-thinking` 后的原公开名及规范名，支持后缀大小写匹配，避免合法旧客户端被误拦。
- Ollama 请求体上限调整位于 beta 净化后、CCH 签名前，保留冻结环境、metadata、最终请求头和 TLS 绑定。
- 固定账号模型发现保留上一批 Astra Ultra、多代理元数据、图片输入能力和 GPT-6 提示词；增加公开别名与共享缓存隔离的组合回归。
- 补齐白名单非法通配符的中英文错误键，清理弹窗重开时的输入草稿和错误状态。
- 刷新 Wire 生成代码，接入备份数据库连接和模型发现服务，保留本地预热、冷却探测、SK 恢复、worker 和清理流程。
- 未带入无关的 #5717 简易模式分组限制或 #6688 模型路由功能。

## 数据库与行为变化

新增 `235_group_model_allowlist.sql` 和 `236_group_model_allowlist_repair.sql`，本地迁移文件总数由 281 增至 283。正常升级将 `groups.models_list_config` 改名为 `groups.model_allowlist`，JSON 配置原样保留。

认证快照版本从 23 升至 24，旧缓存会被拒绝并重新加载，避免使用缺少新白名单字段的旧快照。

**原来已启用的“模型展示列表”会开始限制实际请求。** 客户端请求的公开模型名未命中白名单时返回 404；管理端不允许新建“启用但列表为空”的配置。历史禁用或默认空配置保持不限制。手工写入的历史启用空列表则拒绝所有非空模型。

启用固定账号模型发现时，目录优先使用指定账号的真实结果；成功返回的空目录是有效结果，不自动补成默认目录。未配置固定账号时沿用原有发现回退。

236 支持旧列单独存在、两列并存和两列均不存在的修复；两列并存时仅在新列为空对象时回填旧配置，并保留旧列。它跟随 `search_path` 定位目标表。但 235 仍假定 `public` schema，因此不能把 236 的单独修复能力理解为整个首次升级链支持自定义 schema；本项目部署使用 `public`。

## 验证

- 前端全量测试：259 个文件、1,886 项通过。随后补充弹窗和翻译回归，最终相关 2 个文件、24 项通过（含 5 项新增用例）。
- 前端最终 `npm run build`（类型检查及生产构建）和 `npm run lint:check` 均通过。构建保留仓库原有的大分包及 Browserslist 数据更新提示。
- 后端 Go 1.27.0：最终 `go test -p 2 -json -tags=unit ./...` 通过，58 个测试包、10,586 个顶层测试通过，17 个现有条件跳过的顶层测试未计入。
- 后端 `go test -p 2 -json -tags=integration ./...` 全量通过，52 个测试包、6,339 个顶层测试通过，18 个现有条件跳过的顶层测试未计入。
- 数据库专项 7 个顶层测试通过：从冻结的 281 项迁移清单升级至 283 项、235 已登记但列名回退、重复启动、API Key 鉴权投影、订阅和分组关联、本地 SK/冻结配置、余额及价格保留均覆盖；原 pre-0.2.1 升级回归继续通过。
- golangci-lint 2.13.0：`0 issues`。
- Linux amd64 嵌入前端的发布程序构建通过；隔离 PostgreSQL/Redis 下健康检查、公开设置、TokenHub 品牌、首页资源和预热启动通过，5.736 秒正常退出，退出码 0，测试容器和网络清理无错误。
- Linux 程序 SHA-256：`302c7efd6212ceb86f74ce8ad4812aa4eac8f8d61ff2fa4a9b3fa67defc9ae51`。

本机没有 C 编译器，未执行 Go race 检查；未调用真实供应商进行收费请求验证。所有容器验证使用隔离测试资源，没有修改业务容器。

日志目录：`.cache/optional-upstream-20260908/`。最终结果主要对应 `backend-unit-accepted.jsonl`、`backend-integration-final.jsonl`、`backend-migration-focused.jsonl`、`frontend-unit.log`、`frontend-allowlist-accepted.log`、`frontend-build-final.log`、`frontend-lint.log`、`backend-lint.log`、`backend-linux-build.log` 和 `linux-smoke.log`。早期失败日志保留用于追溯本地别名适配及测试断言修正。

## 发布与回滚

本次完成本地代码整合、测试与发布程序验证，不推送、不部署、不替换运行中的业务容器。

正式部署应将本批前后端作为同一版本发布，并保存升级前的数据库备份与旧程序。迁移包含数据库列名及 API 字段变化，旧程序无法直接读取新列；回滚不能只替换二进制，需要匹配的数据库恢复或经过验证的反向迁移方案。数据库备份恢复会舍弃备份之后的数据，应在发布流程中安排写入窗口。不要随意改写已经执行过的迁移文件或 checksum。
