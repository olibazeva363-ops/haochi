# Claude 单账号 Worker 模式

该模式把选中 Claude 账号后的 Anthropic HTTPS/TLS 执行放到固定账号容器中。主网关继续负责账号选择、会话粘性、并发限制、额度判断、429 冷却、计费和自动切号。

```text
客户端 -> 主网关 -> 账号选择 -> claude-worker-账号ID -> Anthropic
```

## 行为

- 一个 Worker 容器只接受一个固定账号 ID。
- 主网关和 Worker 使用共享密钥认证，Worker 不暴露宿主机端口。
- Worker 只允许访问 Anthropic/Claude 官方 HTTPS 域名和 443 端口。
- 账号配置的 HTTP/SOCKS 代理和 TLS Profile 会传给固定 Worker 执行。
- 上游状态码、响应头和 SSE 流返回主网关，现有 401/403/429/5xx 处理和自动切号保持不变。
- 未配置 Worker 的账号继续使用主网关本地上游连接。
- Bedrock、Vertex 和自定义 Claude Base URL 默认不进入 Worker。

单独使用容器不会产生不同公网 IP。同一服务器上的 Worker 仍共享服务器出口；需要独立出口时，应给相应账号配置独立代理。

## 生成 Compose 文件

先在 `deploy/.env` 中写入随机共享密钥：

```bash
CLAUDE_WORKER_SHARED_SECRET=使用-openssl-rand-hex-32-生成的值
```

然后按数据库中的 Claude 账号 ID 生成覆盖文件：

```bash
chmod +x deploy/generate-claude-workers.sh
./deploy/generate-claude-workers.sh "12,18,27"
```

检查并启动：

```bash
docker compose \
  -f deploy/docker-compose.ghcr.yml \
  -f deploy/docker-compose.claude-workers.yml \
  config

docker compose \
  -f deploy/docker-compose.ghcr.yml \
  -f deploy/docker-compose.claude-workers.yml \
  up -d
```

生成文件会给主网关配置：

```text
CLAUDE_ACCOUNT_WORKERS=12=http://claude-worker-12:8090,18=http://claude-worker-18:8090
```

每个 Worker 使用同一应用镜像，并通过以下变量进入轻量模式：

```text
CLAUDE_ACCOUNT_WORKER_ID=12
CLAUDE_ACCOUNT_WORKER_LISTEN=0.0.0.0:8090
CLAUDE_WORKER_SHARED_SECRET=...
```

## 验证

```bash
docker compose \
  -f deploy/docker-compose.ghcr.yml \
  -f deploy/docker-compose.claude-workers.yml \
  ps

docker logs --tail 50 tokenhub-claude-worker-12
```

主网关选中账号 `12` 时，请求进入 `tokenhub-claude-worker-12`。如果该账号返回明确的额度耗尽或 429，主网关沿用现有 Failover 状态机标记账号并选择下一个可用账号；流式内容已经发送后不会切号。
