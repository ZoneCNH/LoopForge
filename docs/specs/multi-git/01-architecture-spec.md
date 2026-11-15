# Multi-Git Integration — Architecture Spec / ADR（MGIT-002）

- 状态：已评审决策（2026-11-15，决策人：仓库所有者）
- 前置：`00-current-state-audit.md`（MGIT-001，main @ 29fce4c）
- 约束：遵守根 `AGENTS.md` 的 repo/handler 归属规则；GitHub 既有路径零回归

---

## 1. 决策记录（ADR）

### ADR-1：GitLab 传输层——REST 为主，MCP 为扩展

**决策**：GitLab Collaboration Provider 首期实现 REST v4 直连 adapter（与 `tracker/github.go` 同构）；MCP 通道保留为用户自定义扩展，不作为 GitLab 一等路径。

**理由**：
- MCP tool 契约由外部 server 定义，状态语义不可控，与「归一化状态 + UNKNOWN fail-closed」目标冲突。
- REST v4 文档稳定、可 fake-server 契约测试，符合本仓测试文化。
- 「两者都支持」的落地方式：领域契约（§3）不绑定传输；`gitlabClient`（REST）是内置实现，用户若自行配置 GitLab MCP server 仍可走既有 `bindProjectUserTrackerMCP` 通道，但那是 catalog 能力，不是 Provider Adapter 的组成部分。

### ADR-2：凭据模型——新表 `user_provider_tokens`

**决策**：新增 `user_provider_tokens` 表，按 `(user_id, provider_kind, instance_key)` 唯一存 token；MySQL 存储 + 应用层脱敏，不引入外部 Secret Manager。

**理由**：
- GitLab Self-Managed 是多实例场景，`users.git_token` 单值语义偏 GitHub，不可复用。
- Spec §6.3 的 `secret_ref`/Secret Manager 在自托管单实例部署里过重；`secret_ref` 保留为字段语义（v1 恒为 `db:` 内置），未来可平滑切外部密钥管理。
- 与 `user_git_ssh_keys` 表同构（per-user × per-scope 凭据），风格一致。

**表结构（草案）**：

```text
user_provider_tokens
- id            bigint PK
- user_id       bigint  (FK users.id)
- provider_kind varchar(32)   -- 'gitlab'（预留 'github'/'gitea'）
- instance_key  varchar(255)  -- 规范化 base_url，如 https://gitlab.example.com
- credential_type varchar(32) -- 'pat' | 'project_token' | 'group_token'
- secret_ref    varchar(512)  -- v1: 'db:<row_id>'；预留外部 secret manager
- token_hint    varchar(32)   -- 末尾 4 位脱敏标识，用于 UI 展示
- scopes_summary varchar(255)
- expires_at    datetime NULL
- last_validated_at datetime NULL
- status        varchar(32)   -- active | auth_failed | revoked
- created_at / updated_at
UNIQUE (user_id, provider_kind, instance_key)
```

凭据解析顺序（GitLab API 调用时）：请求用户 token → 项目 Owner token → 平台级只读 token（可选）。与现有 `trackerClientForProject` 的「Owner/责任人」模式同构。

### ADR-3：仓库身份——`GongfengProjectID` 保留兼容，新增 `external_ref`

**决策**：`repositories.gongfeng_project_id` 继续承载外部身份字符串（新增 `gitlab:<instance_key>/<url-encoded-path>` 编码）；同时在 `repositories` 增加 `external_ref_json` 列存结构化身份 `{providerKind, instanceKey, externalId, fullPath, apiBaseUrl}`。

**理由**：
- `GongfengProjectID` 渗透面太大（API DTO、前端、run 快照 `RepositoryContext`、migrations、`github:` 字符串判断 6+ 处），v1 改名破坏面不可接受。
- `external_ref_json` 提供结构化身份供新代码使用；旧代码继续读字符串字段，读路径逐步迁移。
- 唯一性：新代码以 `(provider_kind, instance_key, external_id)` 为准；`external_id` 优先 GitLab 数字 project id，路径变更不失效。

### ADR-4：事件同步——polling 默认 + 可选 webhook 回调

**决策**：v1 状态同步以 polling 为主（扩展 `syncMRDeliveryStates` 的 provider 分发）；GitLab Provider Instance 可选配置 webhook URL，平台暴露 `POST /api/webhooks/gitlab/:instanceKey` 接收入站事件。

**理由**：
- 现有系统零 webhook 基础设施；MR 状态本来就是 5min 轮询对账，延迟可接受。
- 私有 GitLab 常无法回调公网 webhook（Spec §7.4），polling 是必选兜底。
- Webhook 实现按 Spec §7 的最小集：`X-Gitlab-Token` 常量时间比较 + 事件幂等键 `(instanceKey, eventUUID/X-Gitlab-Event-UUID)` + 入队后快速返回；签名体系按 GitLab 实际机制（共享 token，非 HMAC）。
- Polling 与 webhook 双通道写同一 `mr_delivery_states`，以 `checked_at`/`merged_at` 做乱序保护。

---

## 2. 目标架构（落地版）

```text
internal/tracker/
  service.go            -- Service 增加 provider dispatch（不新增顶层目录）
  github.go             -- 现有 githubClient，不动
  gitlab/
    client.go           -- gitlabClient：REST v4、分页、token 注入、错误分类
    types.go            -- gitlabProject/gitlabIssue/gitlabMR/gitlabPipeline DTO
    issues.go           -- Issue CRUD
    merge_requests.go   -- MR CRUD + merge + approval summary
    pipelines.go        -- Pipeline/commit status
    webhook.go          -- X-Gitlab-Token 验证 + 事件归一化
    state.go            -- GitLab state → 归一化状态映射（含 UNKNOWN）
  provider.go           -- CollaborationProvider 契约（领域对象级，非传输级）

internal/repo/
  provider_tokens.go    -- user_provider_tokens CRUD（UsersRepo 还是新 ProviderTokensRepo 待定，见 §4）
  repositories.go       -- external_ref_json 读写

internal/httpapi/
  providers.go          -- Provider Instance 管理 API（新 handler struct：providersHandlers）
  gitlab_webhook.go     -- webhook 入口（providersHandlers 或独立 webhookHandlers）
  sync.go / tracker.go  -- 扩展现有 sync handlers 的 provider 维度

migrations/
  027_user_provider_tokens.sql
  028_repository_external_ref.sql
```

**与 Spec §11.3 的差异**（已按 AGENTS.md 修正）：
- 不新建 `internal/provider/`、`internal/webhook/`、`internal/ci/`、`internal/delivery/` 顶层目录——repo/handler 归属规则下，provider 契约放 `internal/tracker`（协作 API 本来就是它的职责），webhook 端点挂 `providersHandlers`/`syncHandlers` 领域下。
- `Delivery` 状态机不改：复用 `mr_delivery_states` + `DeliveryRepo`，只加 gitlab provider 值。

## 3. 领域契约（Provider Adapter 边界）

契约定义在**领域对象**层，不假设传输（REST/MCP 均可实现）：

```go
// internal/tracker/provider.go
type CollaborationProvider interface {
    Kind() string // "github" | "gitlab"
    // Issue
    GetIssue(ctx, repo ExternalRepoRef, iid int) (domain.Issue, error)
    ListIssues(ctx, repo ExternalRepoRef, q IssueQuery) ([]domain.Issue, error)
    CreateIssue(ctx, repo ExternalRepoRef, in CreateIssueInput) (domain.Issue, error)
    // Change Request（PR/MR 统一）
    FindChangeRequestByBranch(ctx, repo ExternalRepoRef, sourceBranch string) (domain.DeliveryMR, bool, error)
    CreateChangeRequest(ctx, repo ExternalRepoRef, in CreateChangeRequestInput) (domain.DeliveryMR, error)
    MergeChangeRequest(ctx, repo ExternalRepoRef, in MergeInput) (domain.DeliveryMR, error)
    // 状态查询（polling 用）
    GetChangeRequestState(ctx, repo ExternalRepoRef, iid int) (MRState, error)
    // 能力声明
    Capabilities() ProviderCapabilities
}

type ExternalRepoRef struct {
    ProviderKind string // "github" | "gitlab" | "gongfeng" | "tapd"
    InstanceKey  string // gitlab: 规范化 base_url；github/gongfeng: ""
    ExternalID   string // gitlab: 数字 project id；github: owner/repo
    FullPath     string // group/sub/repo
}
```

`Service` 的现有方法签名不变（engine 注入点不动），内部把 `github:` 前缀判断替换为 `providerFor(project)` dispatch：

```go
func (s *Service) providerFor(project string) CollaborationProvider {
    if strings.HasPrefix(project, "github:") { return s.githubProvider }
    if ref, ok := parseGitLabRef(project); ok { return s.gitlabProvider.ForInstance(ref.InstanceKey) }
    return s.gongfengProvider // MCP，现状不变
}
```

`GongfengProjectID` 值编码：`gitlab:<instance_key>/<url-encoded-full-path>`，例如 `gitlab:https%3A%2F%2Fgitlab.example.com/group/sub/repo`。解析集中在 `internal/domain`（与 `ParseRepositoryURL` 同层），前后端共用。

## 4. Repo 层归属（AGENTS.md 对齐）

| 新实体 | 归属 | 说明 |
|---|---|---|
| `user_provider_tokens` | **UsersRepo** | 与 credentials 同域（users.go/credentials 已在此）；若后续 provider token 膨胀再拆 `ProviderTokensRepo` |
| `repositories.external_ref_json` | **CatalogRepo** | repositories 已属 CatalogRepo |
| MR delivery 状态 | **DeliveryRepo** | 不新增字段，只新增 provider 值 `gitlab` |
| webhook 事件幂等表 `webhook_events` | **DeliveryRepo** 或新 `WebhookRepo` | 视事件是否关联 delivery；建议先挂 DeliveryRepo |

Handler 层：新增 `providersHandlers`（集成管理 API）+ `gitlabWebhookHandlers`（入站 webhook，无登录态、签名验证）；均挂 `Server` 指针装配。

## 5. 数据模型增量

```text
-- 027_user_provider_tokens.sql
CREATE TABLE user_provider_tokens (...);  -- 见 ADR-2

-- 028_repository_external_ref.sql
ALTER TABLE repositories ADD COLUMN external_ref_json TEXT NULL;
-- 存量 github:/gongfeng 行不回填；新代码读不到 external_ref 时回退解析 GongfengProjectID
```

`domain.Repository` 增加 `ExternalRef *RepositoryExternalRef`（json 内嵌，API 输出时展开或保持 json 字段——待 02-provider-contracts 定）。

## 6. GitLab API 映射（v1 范围）

| 能力 | GitLab REST v4 | 优先级 |
|---|---|---|
| Project Discovery | `GET /projects`（membership/搜索） | P0 |
| Project Metadata | `GET /projects/:id` | P0 |
| Branches/Commits/Compare | `GET /projects/:id/repository/{branches,commits/:sha,compare}` | P0 |
| Issues CRUD | `GET/POST/PUT /projects/:id/issues[/:iid]` | P0 |
| MR CRUD | `GET/POST/PUT /projects/:id/merge_requests[/:iid]` | P0 |
| MR Merge | `PUT .../merge`（先查 `detailed_merge_status`） | P0 |
| MR Approvals | `GET .../merge_requests/:iid/approvals` | P1 |
| Pipelines | `GET /projects/:id/pipelines?sha=` | P1 |
| Webhooks（入站） | `POST /api/webhooks/gitlab/:instanceKey` | P1 |
| OAuth | — | P2（v1 只 PAT/token） |

状态映射原则：未知值一律 `UNKNOWN`，禁止默认成功。`detailed_merge_status` 各值（`mergeable`/`broken_status`/`checking`/`ci_must_pass` 等）映射到归一化 mergeable_state。

## 7. 安全基线（承接 Spec §10，按本仓实际）

- Token 存 `user_provider_tokens.secret_ref`（v1 `db:` 行引用）；API 输出只给 `token_hint`。
- GitLab base_url 规范化：禁 userinfo、默认 `https://`、私网地址需管理员配置 `allow_private_network`（Provider Instance 级策略，v1 可先全局 env 开关）。
- TLS：默认验证；私有 CA 走 `SSL_CERT_FILE`/系统 trust store，v1 不做 per-instance CA bundle（列为已知限制）。
- SSH：复用 `gitwork` 现有 host key 校验与 per-user key 注入，不新建机制。
- Webhook：`X-Gitlab-Token` 恒定时间比较；幂等键 `(instance_key, X-Gitlab-Event-UUID)`；入站 body 限 1MB；不返回签名细节。
- SSRF：Provider Instance 的 `api_base_url` 由管理员配置（非用户自由输入 URL）；v1 UI 标注「仅管理员可配」。

## 8. 兼容与回归

- GitHub 路径零改动：`github:` 前缀判断保留，`providerFor` 中 github 分支行为与现状逐字等价。
- 工蜂/TAPD MCP 路径零改动：`providerFor` 兜底返回现有 MCP client。
- `GongfengProjectID` 字符串格式不变：`gitlab:` 前缀是新增，不影响存量解析。
- 回归测试：现有 `tracker` 全部测试必须原样通过；新增 gitlab 契约测试复用同一 `CollaborationProvider` 契约跑 fake-server。

## 9. 里程碑映射（对 Spec §12 的修正）

| Spec Issue | 本 ADR 后的落点 | 变化 |
|---|---|---|
| MGIT-003 Domain Contract | `internal/tracker/provider.go` | 契约收敛为单接口，传输无关 |
| MGIT-004 Persistence | `027/028` 两个 migration | 无 ProviderInstance 表——v1 用 `instance_key`（base_url）作逻辑实例标识，实例管理 UI 后置 |
| MGIT-005 Auth | `user_provider_tokens` + `providersHandlers` | 覆盖多实例 |
| MGIT-011 Webhook | P1，polling 先行 | 状态同步 v1 纯 polling |
| MGIT-013 UI | 简化：先做「仓库绑定 gitlab: 身份 + token 配置」最小页 | Provider Instance 管理页降级为 P2 |

**v1 范围收敛**：不做 ProviderInstance 实体表、不做集成管理向导、不做 OAuth、不做 webhook 入站（端点保留路由但 handler 返回 501）。核心交付 = gitlab REST adapter + token 表 + `gitlab:` 身份编码 + polling 状态同步 + 契约测试 + 真实 GitLab E2E。

## 10. 待办（进入 MGIT-003+）

- [ ] `02-provider-contracts.md`：`CollaborationProvider` 各方法的输入输出、错误分类（`ErrCapabilityUnsupported` 等）、分页契约
- [ ] `03-security-threat-model.md`：按 §7 展开威胁项与验证用例
- [ ] `04-acceptance-test-matrix.md`：契约测试 + fake-server + 真实 GitLab E2E 清单
- [ ] GitLab Self-Managed 测试实例 provisioning（Day 1 清单项）
