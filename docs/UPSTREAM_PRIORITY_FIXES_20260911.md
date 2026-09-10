# Sub2API upstream priority fixes - 2026-09-11

## Scope

This update selectively backports nine upstream fixes onto the TokenHub fork. It
does not merge the complete Sub2API `v0.2.4` release.

| Reference | Revision |
|-----------|----------|
| Fork baseline | `origin/main` at `af05ad110c2af5989db5b5bf778d253b4b0399f6` |
| Upstream snapshot | `upstream/main` at `98d86915becae9fe9491a91ffc6defd5235c8d2b` |
| Upstream release | `v0.2.4` at `5de5e2bed035d43591a2e10e51f420ef6a84eb98` |
| Integration branch | `codex/sub2api-0.2.4-priority-20260911` |
| Application version | `0.2.1` (intentionally unchanged) |

No database migration is added by this selective update.

## Included upstream changes

| PR | Upstream commit | Local commit | Change |
|----|-----------------|--------------|--------|
| `#6754` | `b6384452347fb6d240fe25dbfca202ca77e6acfb` | `6768115e0` | Cancel the upstream stream before closing a client request. |
| `#6814` | `1923d1c27d411beb5ad8c1fb875900e06c8ec11c` | `a5aca5e31` | Avoid the go-redis 9.22 nil-context pool panic. |
| `#6819` | `ae4cc14b280e91f407b12a141c768d88d6c535ab` | `368578b37` | Honor registration entry visibility settings. |
| `#6838` | `4e9b01fd59b6621c4e02e5ab44dc93ce52c8534d` | `f4c90be55` | Probe Claude Code accounts with `max_tokens=1`. |
| `#6372` | `e2fd418a964206c374586740025bade1d5493a07` | `27c7ad03f` | Avoid cooling OpenAI OAuth accounts for transient 429 responses when fallback is disabled. |
| `#6434` | `43569bb44c3eea843cec5fcacf0688961cd97f6b` | `1b4727a71` | Drain an upstream WebSocket after the downstream client disconnects so final usage can still be billed. |
| `#6424` | `95acbf1f031a291335c60515ee368014139da113` | `05e0ce545` | Bound database system-log growth and add scheduled cleanup controls. |
| `#6839` | `d9efbf8229c955e252138d10a279725f6a691fbb` | `73f238080` | Pass through Grok `external_web_access`. |
| `#6874` | `5de5e2bed035d43591a2e10e51f420ef6a84eb98` | `a7b694f93` | Add Image 2.5 Flare/Sunburst models and fix image generation through OpenAI OAuth accounts. |

## TokenHub compatibility work

### WebSocket disconnect accounting

- A disconnected client can return a partial result together with
  `context.Canceled`; the result is no longer discarded or sent through the HTTP
  fallback path.
- The partial result preserves the requested reasoning effort, image count,
  image output sizes, billing model, requested image size and image input size.
- Usage persistence runs with its own bounded background context, so downstream
  cancellation cannot cancel the accounting write.
- The existing maximum 900-second upstream drain window is retained so a final
  usage event can still be collected.

### OpenAI OAuth 429 handling

- An ordinary transient 429 does not cool the account when 429 fallback is
  disabled.
- Explicit 5-hour or 7-day quota exhaustion at 100% still blocks scheduling.
- When upstream supplies a reset time, the block is persisted until that time.
  Without a reset time, the existing short in-memory pause is used and no
  fabricated persistent reset timestamp is stored.
- Spark model cooldowns, `Retry-After`, HTML 403 handling and local quota
  protection keep their existing behavior.

### Image 2.5

- Synchronous and asynchronous image endpoints accept Flare, Sunburst and their
  dated snapshots when the group allowlist explicitly contains the requested
  model. An older allowlist still rejects the new models.
- Chat Completions rejects image-only models before account scheduling.
- TokenHub billing keeps image input and image output tokens in separate price
  components for every new Image 2.5 model.
- A plan-gated 400 for the Responses controller model is returned immediately.
  It does not rotate through accounts or incorrectly cool the requested image
  model.

### Deployment and log retention

The GHCR Compose deployment now exposes these defaults:

```env
SUB2API_IMAGES_MAIN_MODEL=gpt-5.6-luna
OPS_CLEANUP_SYSTEM_LOG_RETENTION_DAYS=30
```

`deploy/config.example.yaml` also documents the `ops.cleanup` baseline. Saved
admin settings override the deployment baseline. In particular, an existing
`settings.ops_advanced_settings.cleanup_enabled=false` value continues to keep
scheduled cleanup disabled and must be checked before rollout.

Setting `persist_access_logs=false` only stops successful `http.access` entries
from being written to `ops_system_logs`. Warning/error logs, audit logs, file
logs and `usage_logs` remain enabled.

## Not included

The `v0.2.3..upstream/main` release window contains 30 merged PRs. This update
includes nine and defers 21. Representative deferred groups are:

| Area | PRs | Reason for deferral |
|------|-----|---------------------|
| Optional platforms and large features | `#6758`, `#6488`, `#6759` | MiniMax, Grok media eligibility and Monitor V2 ranking change broad platform, settings or schema behavior and need separate rollout review. |
| Proxy-chain behavior | `#6810`, `#6811`, `#6815`, `#6816`, `#6836` | Date validation, PATCH semantics, self-references and fallback behavior should be tested as one coordinated proxy update. |
| Runtime, multi-instance and network behavior | `#6235`, `#6320`, `#6281` | Redis cache broadcasting, persisted cooldown coordination and HTTP/2 keepalive touch TokenHub scheduling or transport behavior. `#6320` also overlaps the 429 runtime-block code changed in this update. |
| Admin and presentation changes | `#6659`, `#6798`, `#6760`, `#6762`, `#6764`, `#6706`, `#6812` | These do not affect the gateway fixes prioritized in this update. |
| Environment-specific or locally equivalent fixes | `#6513`, `#6775` | Apple Container networking is outside the current GHCR/Linux deployment; the Windows ZIP handle fix already has a local equivalent in `181a7d6a7`. |

The upstream `VERSION=0.2.4` change, sponsor metadata and migrations belonging
to deferred features are also excluded. Future audits must carry these deferred
PRs forward while separately comparing commits newer than the upstream snapshot
recorded above.

## Validation

- Frontend: `npm run test:run` (260 files, 1,894 tests), `npm run build`, and
  `npm run lint:check`.
- Backend: targeted service/handler/config/repository tests, followed by
  `go test -p 2 -tags=unit ./...` and `go test -p 2 -tags=integration ./...`.
- Deployment: GHCR Compose rendering.
- Source hygiene: `gofmt` and `git diff --check`.
