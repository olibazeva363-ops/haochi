# Claude OAuth 最小身份提示修复（2026-09-22）

当前账号在有可用额度时，不带 Claude Code 身份提示的 Sonnet 4.6 / Opus 4.6 请求收到上游 429。相同账号、Key 和极短正文的对照为：Sonnet 无提示 429 → 加身份句 200 → 去掉再次 429；Opus 加身份句也恢复 200。Haiku 不带提示可用，不能据此推断其他模型也不需要该兼容字段。

用户已明确要求恢复最小身份提示。本次从线上 `34133d4229b009ee9230ba1612d8c7500ec171c5` 创建独立修复，不混入尚未发布的 Sub2API 第一至三批或外部 OAuth 邀请接口。

## 最终行为

仅 Anthropic OAuth / SetupToken 在缺少身份句时，为最终出站请求前置一个 system text 块：

```text
You are Claude Code, Anthropic's official CLI for Claude.
```

- 覆盖 `/v1/messages`、`/v1/messages/count_tokens`、Chat Completions / Responses 转 Claude，以及后台账号连接测试。
- 客户端任一 system 文本行已以此完整句开头时，保留原 system，不重复添加；重试和重复构建请求也不会累积身份块。
- 字符串 system 转为独立客户端文本块，原字符、空白、日期和 HTML 字符保留；数组中的客户端块及其原始表示、顺序、缓存元数据保留。新增身份块不设置 cache_control，不增加缓存断点。
- 用户消息、工具说明、空结果、thinking 历史、billing 文本继续保留。旧的扩展长提示、system 搬入 messages、日期改写、billing attribution 合成、CCH 签名和默认工具说明没有恢复。
- API Key / Vertex / Bedrock 及其他平台不增加这句；历史长提示设置保持退休。本次最小身份兼容不受已退休的长提示开关控制。
- 请求文字增加了这一句，因此已不属于“完全无提示词注入”，会带来少量输入 tokens。

## 验证与发布记录

已完成 42 个新增请求场景与原有 40 个内容保留回归，包括 OAuth/SetupToken、真实客户端/协议转换、去重、API Key/Vertex 隔离和后台测试。另验证 helper 幂等、空/null/string/array、原始文本与大整数保留，以及冻结身份/billing/cache 兼容。

全量集成检查已覆盖：初次执行发现本机 Docker 未启动，以及三项旧的“完全无注入”测试仍使用旧预期。启动 Docker、显式指定 Windows Docker 命名管道并更新身份句预期后，repository、middleware、routes、service 四个受影响包补跑通过，其余包首次通过。客户端 headers、messages、billing 和缓存断言继续保留。

Linux amd64 嵌入前端构建通过，二进制 SHA-256 为 `da72a03d15c6b593cb3975ea5e642dc4d1ddd9cabaf3a3f951c8540191013101`。使用独立 PostgreSQL / Redis / app 容器完成自动初始化、health、TokenHub 公共设置、前端静态资源、warmup 和正常关闭验证；关闭耗时 0.461 秒、退出码 0，隔离资源清理无错误。

全量单元检查的其他包首次通过；更新上述三项旧预期后，service 全包补跑通过（191.066 秒）。最终 golangci-lint 全量检查通过，0 issues。`git diff --check` 通过。

日志在本工作树 `.cache/minimal-identity-20260922/`，不包含凭据。真实上线验收使用不自行携带 system 的极短请求，确认站点自动补入后的结果。

## 实际上线验收

- 代码提交：`523afe6c7ddc7f86d06387cdbc8951475edb1c22`，已推送 main。
- GitHub Actions Deploy Image：`35689457981`，success；Security Scan：`35689457980`，success。
- GitHub 全量 CI 记录：[35689458091](https://github.com/olibazeva363-ops/haochi/actions/runs/35689458091)。
- Watchtower 已自动更新，应用启动时间 `2026-09-22T05:10:27.628168328Z`（北京时间 13:10:27）。
- 实际运行镜像：`ghcr.io/olibazeva363-ops/haochi@sha256:770a0376ebd04928344c52da63e3eba02cc69871c9f228ad65f2288b89ec363e`，OCI revision 与上述代码提交一致。
- 容器 running / healthy，restart_count=0；`/health` 返回 200、`status=ok`。

经公网域名 `https://fangwaizhijing.com/v1/messages` 调用，使用原账号/Key，客户端仅发送 `Reply OK.`、`max_tokens=16`，不发送 system：

| 模型 / 方式 | HTTP | 正常结束 | 输入 / 输出 tokens | 耗时 |
| --- | --- | --- | --- | --- |
| Sonnet 4.6 非流式 | 200 | end_turn | 25 / 5 | 1.060 秒 |
| Opus 4.6 非流式 | 200 | end_turn | 25 / 4 | 1.706 秒 |
| Sonnet 4.6 流式 | 200 | message_stop，无 error 事件 | 25 / 5 | 0.975 秒 |

本次账号的缺失身份提示导致的 429 已在上述三次真实请求中消失。此结果不代表以后所有 429 都与身份提示有关，上游真实额度/速率限制仍按原逻辑处理。以上发布记录后续以文档提交补充，不触发镜像重新构建；线上代码 revision 仍为 `523afe6c7ddc7f86d06387cdbc8951475edb1c22`。

发布沿用 main → GitHub Actions Deploy Image → GHCR latest → Watchtower（60 秒）。无数据库迁移。上线前镜像的可回滚引用为 `ghcr.io/olibazeva363-ops/haochi@sha256:819e41885e5067ab2cde5be9048bf8741943aa70ecf2cee1caaaa43a650ef8cc`；如需回退，在服务器 `/opt/tokenhub` 对 `docker-compose.ghcr.yml` 的 app 服务使用该固定 digest，避免 Watchtower 随 latest 再次更新。
