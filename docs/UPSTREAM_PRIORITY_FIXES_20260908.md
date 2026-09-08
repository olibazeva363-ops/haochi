# TokenHub 定向跟进 Sub2API 优先补丁

后续按需项和配套升级已另行接入，见 [第二阶段记录](UPSTREAM_OPTIONAL_UPGRADES_20260908.md)。下文保留第一阶段截止时的范围与验证结果。

## 版本与范围

- 整合日期：2026-09-08。
- 独立分支：`codex/sub2api-priority-fixes-20260908`。
- 原本地提交：`263773e458c9e6c801deed99c67ea779a6468e64`，已包含官方 v0.2.1 与本地连接预热修复。
- 本次仅摘取下列 19 个上游 PR，并适配本地 Claude 固定环境逻辑。版本标记保持 `0.2.1`，不是完整升级到官方 `0.2.3`。
- 没有新增或修改数据库 schema 迁移。

## 上游补丁

表中 SHA 为官方仓库合并提交；本地摘取提交通过 `cherry picked from commit` 保留来源。

| PR | 上游 SHA | 用途 |
| --- | --- | --- |
| [#6690](https://github.com/Wei-Shaw/sub2api/pull/6690) | `5485f368b29d05adb95a00f71801c7c23d8f48af` | 保留 Codex OAuth 请求中的 Astra `reasoning.mode`。 |
| [#6718](https://github.com/Wei-Shaw/sub2api/pull/6718) | `561fc1c3e455e405af4d604339e8e9df4f044445` | 保留 Astra Ultra 工作流模型元数据。 |
| [#6678](https://github.com/Wei-Shaw/sub2api/pull/6678) | `cbb4b7e53e4bd7e94f09a72325f27250b0360e5c` | 修复官方 Astra 模型缓存中过期的输入模态信息。 |
| [#6743](https://github.com/Wei-Shaw/sub2api/pull/6743) | `8193b80a59ada85d8cdff4f1e47a9be48fca6292` | 修正 GPT-6 Astra 提示词选择。 |
| [#6687](https://github.com/Wei-Shaw/sub2api/pull/6687) | `9578ddbddd863185503bb9a4abf811ccf888a348` | 保留具名独立 Responses 输入，兼容 Codex 原生委派。 |
| [#6629](https://github.com/Wei-Shaw/sub2api/pull/6629) | `8116d235ef62dc1d88e7a2a9603058e87e368cea` | 从工具参数完成事件中保留完整函数参数。 |
| [#6733](https://github.com/Wei-Shaw/sub2api/pull/6733) | `43491f67a15f2675ea19cf49dd9774a6e1a0b29b` | 保留 Codex `allowed_tools` 工具选择约束。 |
| [#5453](https://github.com/Wei-Shaw/sub2api/pull/5453) | `959cbe3d87186c80b36a2b8bde44559bf7da17e3` | WebSocket 透传后续轮次重新申请并发槽位。 |
| [#6708](https://github.com/Wei-Shaw/sub2api/pull/6708) | `22c16d0517d1511c95678e5cb6fcf24cc257453b` | WebSocket 透传后续轮次在账号额度耗尽后恢复。 |
| [#6704](https://github.com/Wei-Shaw/sub2api/pull/6704) | `f09a5b602e57dc712a1ee8df20f569769308e778` | 隔离 HTTP bridge 会话，修复并发连接抢占与状态串用。 |
| [#6711](https://github.com/Wei-Shaw/sub2api/pull/6711) | `f8e1e7fed64bd2966199b86f4f34e69b0517a572` | 模型不存在响应触发故障切换。 |
| [#6736](https://github.com/Wei-Shaw/sub2api/pull/6736) | `81c0f11d7a8634e2fd2b04deb6dbb5465e59ea66` | 记录 OpenAI 流式失败的上游诊断信息。 |
| [#6481](https://github.com/Wei-Shaw/sub2api/pull/6481) | `787a6a33df3c7a7d17563d1e9d61e3d4800e38e7` | 抬升 Claude CLI 内置版本及存量指纹版本下限，修复 Fable 版本门槛错误。 |
| [#6677](https://github.com/Wei-Shaw/sub2api/pull/6677) | `2cc1e7ef900962735992c42e351430abe3a0603e` | 支持 thinking block binding beta，按最终 beta 清理不受支持的字段。 |
| [#6484](https://github.com/Wei-Shaw/sub2api/pull/6484) | `c0420e2b8a45598c39f957dd7e5ce6c88ac1c814` | 将 Fable `credits_required` 限制保持在模型范围，避免整账号停调。 |
| [#6702](https://github.com/Wei-Shaw/sub2api/pull/6702) | `e274de45b784fc39bac880a57ec61c4b1e361cd3` | 缓存断点将消息字符串转为文本块时使用合法 JSON 转义。 |
| [#6670](https://github.com/Wei-Shaw/sub2api/pull/6670) | `ce328fb37a55ee8a28d8a5c07c59e3e2250a0e5b` | 支付订单履约与兑换码限制隔离。 |
| [#6669](https://github.com/Wei-Shaw/sub2api/pull/6669) | `abf70750fdb697be3e87a59ff19a45f88e0bb570` | 兑换码失败计数采用固定 10 分钟窗口。 |
| [#6726](https://github.com/Wei-Shaw/sub2api/pull/6726) | `dc46daa690d8a9bfb3718772a7b6b29909ce3a24` | 推理强度映射目标支持拒绝，可按模型和请求档位设置。 |

## 本地适配

本地 Claude 固定环境账号优先从 `accounts.extra` 或内存读取冻结档案，会绕过官方普通指纹缓存的版本升级。此次在冻结档案读取路径补充相同版本下限：只升级 User-Agent 的 CLI 版本，保留 client/device 身份、SDK、TLS、代理及冻结时间。

更新复用现有 singleflight 与代理更新锁，克隆档案后持久化，成功才替换缓存；失败保留旧快照供后续重试。新增回归覆盖旧档案读取、只升不降、写入失败重试、并发与代理更新，以及最终请求头和计费 `cc_version` 一致。

Claude SK 导入和 401 恢复、账号 worker、固定环境、CCH 签名、冷却探测、连接预热及 TokenHub 部署定制继续保留。

协议转换复查发现 #6629 的补参数逻辑在最终工具结果压缩或重排时可能按位置匹配到其他调用。此次优先按 `call_id` 匹配，双方 ID 均存在但不一致时禁止位置回退；仅缺 ID 时保留原有回退。新增 6 个场景，修复前 4 个失败，修复后整个协议转换包通过。

#6743 附带的模型目录路由测试依赖未纳入的 #6688；此次保留本地 5 参数接口及对应测试，不引入该路由功能。另补 Messages 转 OpenAI 两条路径的实际拒绝回归，验证旧上限拒绝和新增映射拒绝均返回 403，并且不访问上游。

## 验证状态

- 前端 Node.js `22.23.2`：`npm run test:run` 通过，共 257 个文件、1,872 项测试。
- 前端 `npm run build` 通过，包含类型检查与生产构建。
- 前端 `npm run lint:check` 通过，无错误或警告。
- 后端 Go `1.27.0`：最终 `go test -p 2 -json -tags=unit ./...` 通过，57 个测试包、10,442 个顶层测试通过；`go test -p 2 -json -tags=integration ./...` 通过，51 个测试包。两项检查均覆盖最后补充的协议转换修复。
- 单元 / 集成测试分别有 17 / 18 个顶层测试因仓库现有平台、外部依赖或占位条件跳过，不计为已验证。兑换码固定窗口的 `TestRedeemCacheSuite` 已在独立 Redis 中实际通过。
- golangci-lint `2.13.0` 全量检查通过：`0 issues`。
- Linux amd64 嵌入前端的发布程序构建通过。在独立 PostgreSQL / Redis 容器中，自动初始化、健康检查、公开设置、TokenHub 默认品牌、首页资源和连接预热启动均通过。程序 1.431 秒正常退出，退出码 0，测试容器与网络清理无错误。
- Linux 验证程序 SHA-256：`b3f9cd3a46e38b65e2df859f9ac9f7fd7986fa31a061c0bb0bb9951da57f4d77`。
- 本地验证日志目录：`.cache/prioritized-upstream-20260908/`。

本机未提供 C 编译器，未执行 Go race 检查。Astra 提示词文件按上游原文保留，包括原有行尾空格。

## 未纳入与发布

其余按需候选，包括计费展示、备份和 Ollama，以及分组模型白名单与其数据库修复，未合入本批次。现有分组模型列表配置不因此变为强制调用白名单。

本次工作保留在上述独立分支，不推送、不部署，也不替换运行中的服务。
