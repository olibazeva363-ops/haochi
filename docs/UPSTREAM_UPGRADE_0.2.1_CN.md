# TokenHub 升级到 Sub2API 0.2.1

## 版本与范围

- 官方基线：[Wei-Shaw/sub2api v0.2.1](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.1)，[ab99d56e9626e6cd731592dae8553c9758a0efa2](https://github.com/Wei-Shaw/sub2api/commit/ab99d56e9626e6cd731592dae8553c9758a0efa2)，对应发布后的版本号同步提交。
- 原本地提交：`8215c36b9`；先完成原有 reasoning effort 超限策略合并，形成 `be143e40b` 检查点。
- 升级分支：`codex/upgrade-sub2api-0.2.1-20260906`。
- 原项目以源代码快照导入官方 v0.1.170，未保留 Git 祖先。此次补记经树差异核实的官方祖先，再整合完整上游历史；后续可以按正常共同祖先合并。

此次引入整个官方版本，包括 Astra / Ultrafast、模型能力同步、固定账号模型目录、WS 历史正文共享、失败会话释放、模型映射调度、请求 ID 持久化、金额量化、统一价格展示、价格覆盖文件与热重载、支付宝补偿、精简账号列表、监控 V2、国产模型额度管理和插件宿主。

监控 V2、插件、模型目录等依照上游的配置和权限生效，不自动启用所有可选功能。

## 保留的本地能力

- TokenHub 默认品牌，禁止用官方自动更新覆盖定制二进制。
- Claude SK 批量导入、转换 Cookie 管理、401 凭据恢复，以及 Claude 凭据 JSON 导入和重新授权。
- Claude 按账号独立 worker、固定账号环境配置、默认 RPM 管理。
- 冷却账号主动探测与近期活跃账号连接预热；后台任务保留完整启动和关闭接线。
- 原 Anthropic 接近用量上限保护迁到上游统一调度阈值：旧配置仅提供未配置时的默认值，显式平台设置和账号覆盖优先。
- 自定义 GHCR 构建、Compose、Nginx 和 Claude worker 部署入口。

官方已吸收的备份互斥锁、分卷备份、取消处理、Fast 计费和 Codex 身份修复采用新版实现，避免两套逻辑重复生效。

整合时同时修复连接预热的取消与并发计数、账号环境档案的并发更新和持久化失败重试，以及 Windows 安装插件时未关闭 ZIP 文件导致重命名失败的问题。相关回归检查纳入后端测试。

Claude 转换入口共用同源重定向限制，避免 SK 或转换 Cookie 被转发到配置目标之外；worker 两段请求均禁止自动重定向，保持已校验的目标边界。

## 构建环境

- Go 1.27.0，与 `backend/go.mod`、CI 和 Docker 构建镜像一致。
- Node.js 24；pnpm 9.15.9，Docker 与 CI 构建环境统一；初次本地验证也覆盖了 Node.js 22。
- golangci-lint 2.13.0，与 CI 配置一致。
- PostgreSQL 与 Redis 的集成验证使用隔离测试容器，不连接已有业务库。

前端：`pnpm install --frozen-lockfile`、`pnpm build`、`pnpm test:run`。

后端：`go test -tags=unit ./...`、`go test -tags=integration ./...`；构建嵌入前端的发布程序使用 `go build -tags embed ./cmd/server`。

Ent schema 与该官方提交完全一致，保留官方生成产物；Wire 依赖接线根据本地后台服务重新生成。生成器与编译器不要同时写读同一目录，Windows 文件映射会阻止生成器覆盖文件。

## 数据库升级

本地检查点包含 244 个 SQL 迁移，官方总计 281 个。历史 SQL 内容保持不变，新增 37 个；迁移按完整文件名和校验和识别，数字前缀相同的不同文件不需要改号。原未完成合并前为 243 个。

- 一并采用官方迁移 runner 的历史校验和兼容规则和非事务索引失败清理。
- 迁移 220 清理非 Grok、非复合组的旧视频价格，先保存在 `groups_video_price_backup_220`，Grok 和复合组价格保留。
- 迁移 225 仅对已开启相关模式、但 seed 缺失或无效的 OpenAI OAuth 账号补 seed。
- 新监控默认仍为 V1，V2 需要显式开启。

新增集成回归从冻结的 244 个旧迁移建立测试库，写入余额、站点配置、SK、Codex 配置和视频价格，再升级到完整迁移并重复执行，检查余额、凭据、价格备份、配置与幂等性。

## 验证结果

- Node.js 24.20.0 / pnpm 9.15.9：冻结锁文件安装、类型检查和生产构建通过；Vitest 257 个文件、1,871 项测试通过。
- 前端 ESLint：0 错误、0 警告。
- 后端完整单元测试：57 个测试包通过；完整集成测试：51 个测试包通过。单元 / 集成测试分别有 17 / 18 项依照仓库现有平台、外部依赖或占位条件跳过，未计为已验证。
- 收尾修复后，服务、接口和仓库三个包的 451 个定向测试 / 子用例通过，覆盖转换重定向、SK 恢复、账号环境缓存、worker、预热及 OAuth 刷新。
- golangci-lint 2.13.0 全量检查通过：0 issues。未关闭现有检查器；合法代理调用按实际配置、鉴权和目标校验边界逐点标注。
- 旧数据库升级回归：244 → 281 个迁移，数据保留、视频价格备份和重复迁移检查通过。
- Linux amd64 嵌入前端构建通过。在独立 PostgreSQL 18.1 / Redis 8.4 测试容器中，自动初始化、`/health`、公开设置、TokenHub 默认站点名、首页和 JS 资源均通过检查。
- Linux 程序 SIGTERM 后 0.734 秒正常退出，退出码 0；连接预热服务和全部清理步骤完成。测试容器及网络均已清理。
- Compose 安全配置、网关环境变量、运行资源路径和 Caddy 缓存规则检查通过；Claude worker / Apple Container 脚本语法检查通过。Apple Container 的完整功能测试需要 macOS，本机未执行。

Linux 验证程序 SHA-256：`f52d4f089bcfe95c08e96f74335f9d383c6e8f0ed8267c937bd569d6573640cb`。验证日志保存在本机 `.cache/upstream-upgrade-20260906/`。

## 发布与回退

本次只在升级分支整合，不推送、不替换运行中的容器。仓库原有 `deploy-image.yml` 在推送 main 时发布 GHCR `latest`；部署 Compose 中的 Watchtower 可能随后拉取更新，因此推送 main 应作为发布操作处理。

部署前保存数据库备份和当前镜像摘要。数据库迁移提交后，回退旧镜像不等于回退数据库；需要结合数据库备份或迁移 220 的价格备份处理。

代码备份分支：`codex/backup-before-sub2api-021-20260906`。原未完成合并的 47 个工作区文件、索引和补丁存于本机 `.cache/upstream-upgrade-20260906/`，不提交仓库。
