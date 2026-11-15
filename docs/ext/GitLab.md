# LoopForge Multi-Git Integration v1.0
## GitHub + GitLab Self-Managed 统一代码交付平台：可执行项目 Spec

- 文档版本：1.0.0
- 状态：执行基线（Audit-first；代码级结论须以目标分支实测为准）
- 目标仓库：`Tencent/LoopForge`
- 首期 Provider：GitHub.com、GitLab Self-Managed
- 目标：同一套 SOP、Agent Role、Delivery Gate 和 Evidence 流程支持两种 Git 平台
- 非目标：首期支持 Gitea、Gogs、Bitbucket；重写 SOP Engine；更换 Agent Runtime；将 GitLab 专有逻辑散落到业务层

---

# 0. 执行摘要

LoopForge 已经能够通过 Git SSH 对 GitLab 等 Git Host 执行仓库检出和分支推送。GitLab Issue、Merge Request（MR）等协作自动化仍需要 Hosting API Token 或 MCP。因此，当前缺口不是基础 Git Transport，而是完整的 Provider Integration：连接管理、项目发现、Issue/MR 生命周期、Webhook、Pipeline 状态、权限诊断、统一交付状态和端到端验收。

**核心决策：采用 Git Transport 与 Collaboration Provider 分离的多 Provider 架构。**

1. Git CLI 负责 Clone、Fetch、Branch、Checkout、Commit、Push、Tag、Diff。
2. Provider Adapter 负责 Repository、Issue、Pull Request/Merge Request、Review、Webhook、CI/Pipeline 等平台 API。
3. SOP Engine 只消费平台无关的 Delivery Contract，不出现 `if provider == gitlab` 业务分支。
4. GitHub 现有路径必须保持兼容；GitLab Self-Managed 作为首个新增 Provider。
5. 一个 Repository Connection 绑定一个 Provider Instance；一个 Provider Instance 可连接多个项目。
6. 凭据只通过 Secret Reference 注入，不在数据库、日志、Git Remote URL 或 Evidence 中保存明文。
7. 以真实 GitLab Self-Managed E2E 为发布必要条件，Mock 不能替代。

## 0.1 Release Definition

当且仅当以下链路在 GitHub 和 GitLab Self-Managed 均通过验收，才能标记 `RELEASED`：

`Connect → Validate → Discover Repository → Clone → Create Branch → Commit → Push → Create Issue/Link Goal → Create PR/MR → Observe CI → Review → Approval → Merge → Verify Merge → Evidence`

同一 SOP 不需要针对 GitHub/GitLab 修改节点逻辑或 Agent Prompt。允许 Provider Adapter 在能力差异处返回明确的 capability 和 unsupported reason。

---

# 1. 问题本质与基本约束

## 1.1 两个独立问题

### Git Transport

负责代码对象与远端仓库交互：

- Clone / Fetch
- Branch / Checkout
- Commit / Push
- Tag
- Diff / Compare
- Remote URL 解析

### Development Collaboration

负责平台协作对象：

- Repository / Project Discovery
- Issue / Comment
- PR / MR
- Review / Approval
- Merge
- Webhook
- CI / Pipeline
- Release / Tag metadata

Git SSH 可用不代表 API 可用；API 可用不代表 Runner 能访问 Git SSH；Webhook 能到达不代表回调签名正确。必须分层验证。

## 1.2 不可违反的约束

- Provider-specific API 不进入 SOP Engine。
- Provider-specific 字段不泄漏到 Agent Prompt 的通用业务语义中。
- 不把 GitLab `project_id`、`issue iid`、`MR iid` 当成内部主键。
- 不把 GitLab MR 与 GitHub PR 的审批语义假设为完全相同。
- 不在 URL、日志、错误消息、事件 Payload、Evidence 中输出 Token 或私钥。
- 不关闭 SSH Host Key Verification。
- 不允许 Agent 自动绕过 Branch Protection、Required Approval 或 Release Gate。
- 不允许 Webhook 事件直接触发无幂等保护的重复交付。
- GitHub 既有用户、OAuth、Repository、Delivery 行为必须有回归测试。
- 私有 GitLab 的 API Base URL、Web URL、Git SSH 地址和网络出口必须分别配置和诊断。

---

# 2. 事实、假设与待验证项

## 2.1 当前已知事实

- LoopForge 是自托管 coding-agent delivery workspace，具备 Chat、SOP Engine、Agent/Script/Human Gate/Sub-SOP、Docker sandbox、运行日志和 artifacts。
- README 描述 GitLab、Gitea、Gogs 等 Git Host 可通过个人 SSH Key 检出和推送分支。
- Issue 与 PR/MR 自动化需要 Hosting API Token 或单独配置 MCP。
- 仓库文档将平台扩展定位为 Adapter 接入既有用户、Token、Repository 和 Delivery 契约的方式。
- 仓库 API 文档包含协作与需求、能力注册、编排运行、外部同步等领域。

## 2.2 必须通过源码审计确认的假设

以下均不得直接当成已实现能力：

- `internal/tracker` 是否已抽象出足够稳定的 Tracker 接口。
- Repository、Credential、Delivery 的持久化模型是否允许 Provider Instance 多实例化。
- GitHub OAuth Token 与手工 PAT 是否走同一 Credential 生命周期。
- PR/MR 状态是否被硬编码为 GitHub PR 语义。
- Webhook 是否已有统一入口、签名验证、事件幂等和重试机制。
- CI 状态是否已经从 GitHub Actions 抽象出来。
- Repository Clone URL 是否允许 HTTPS、SSH 与自定义 GitLab 域名。
- 项目权限模型是否能限制用户只能访问授权的 Provider Instance 和 Repository。
- 现有数据库迁移策略是否支持可回滚的增量迁移。

## 2.3 审计输出

创建并维护以下文件：

- `docs/specs/multi-git/00-current-state-audit.md`
- `docs/specs/multi-git/01-architecture-spec.md`
- `docs/specs/multi-git/02-provider-contracts.md`
- `docs/specs/multi-git/03-security-threat-model.md`
- `docs/specs/multi-git/04-acceptance-test-matrix.md`
- `docs/specs/multi-git/05-release-checklist.md`

每条审计结论必须标注：
`CONFIRMED | NOT_IMPLEMENTED | PARTIAL | UNKNOWN | BLOCKED`

每个 `CONFIRMED` 结论都要附源码路径、符号/函数、测试或运行证据。不得用 README 描述代替源码证据。

---

# 3. 目标架构

## 3.1 逻辑结构

```text
                    LoopForge UI / HTTP API / MCP
                                |
                      Integration Manager
                                |
                       Provider Registry
                                |
             +------------------+------------------+
             |                  |                  |
        Git Transport     Collaboration        Event Ingress
         (Git CLI)          Provider             Webhook
             |                  |                  |
       SSH / HTTPS       +------+-------+      Verify
             |           |              |      Normalize
        GitHub/GitLab  GitHub Adapter  GitLab Adapter
                         |              |
                    GitHub API      GitLab REST API
                         |              |
                    PR / Issue      MR / Issue / Pipeline
             |                  |                  |
             +------------------+------------------+
                                |
                       Delivery Contract
                                |
                    SOP Engine / Delivery State
                                |
                  Evidence / Audit / Notifications
```

## 3.2 Provider 分层

### A. Git Transport

不需要为 GitHub 和 GitLab 各自实现一套 Git 命令。优先复用现有 Git CLI 执行路径。

建议抽象：

```go
type GitTransport interface {
    Clone(ctx context.Context, req CloneRequest) error
    Fetch(ctx context.Context, req FetchRequest) error
    CreateBranch(ctx context.Context, req BranchRequest) error
    Checkout(ctx context.Context, req CheckoutRequest) error
    Commit(ctx context.Context, req CommitRequest) (CommitResult, error)
    Push(ctx context.Context, req PushRequest) error
    Diff(ctx context.Context, req DiffRequest) (DiffResult, error)
    Compare(ctx context.Context, req CompareRequest) (CompareResult, error)
}
```

注意：以上为目标契约草案。必须先审计当前 Git 执行代码，再决定是否新增接口或仅为现有实现补充配置能力。

### B. Repository Provider

```go
type RepositoryProvider interface {
    GetRepository(ctx context.Context, id ExternalRepositoryID) (Repository, error)
    ListRepositories(ctx context.Context, q RepositoryQuery) (Page[Repository], error)
    GetDefaultBranch(ctx context.Context, id ExternalRepositoryID) (Branch, error)
    ListBranches(ctx context.Context, id ExternalRepositoryID) (Page[Branch], error)
    GetCommit(ctx context.Context, id ExternalRepositoryID, sha string) (Commit, error)
    Compare(ctx context.Context, id ExternalRepositoryID, base, head string) (CompareResult, error)
}
```

### C. Issue Provider

```go
type IssueProvider interface {
    GetIssue(ctx context.Context, repo ExternalRepositoryID, id ExternalObjectID) (Issue, error)
    ListIssues(ctx context.Context, repo ExternalRepositoryID, q IssueQuery) (Page[Issue], error)
    CreateIssue(ctx context.Context, repo ExternalRepositoryID, in CreateIssueInput) (Issue, error)
    UpdateIssue(ctx context.Context, repo ExternalRepositoryID, id ExternalObjectID, in UpdateIssueInput) (Issue, error)
    AddIssueComment(ctx context.Context, repo ExternalRepositoryID, id ExternalObjectID, body string) error
}
```

### D. Change Request Provider

```go
type ChangeRequestProvider interface {
    GetChangeRequest(ctx context.Context, repo ExternalRepositoryID, id ExternalObjectID) (ChangeRequest, error)
    ListChangeRequests(ctx context.Context, repo ExternalRepositoryID, q ChangeRequestQuery) (Page[ChangeRequest], error)
    CreateChangeRequest(ctx context.Context, repo ExternalRepositoryID, in CreateChangeRequestInput) (ChangeRequest, error)
    UpdateChangeRequest(ctx context.Context, repo ExternalRepositoryID, id ExternalObjectID, in UpdateChangeRequestInput) (ChangeRequest, error)
    MergeChangeRequest(ctx context.Context, repo ExternalRepositoryID, id ExternalObjectID, in MergeInput) (MergeResult, error)
}
```

### E. Review / CI Capability

Review 和 CI 不应被假设为每个平台都完全一致。首期可以由 Change Request Provider 暴露只读汇总，再逐步拆分独立接口。

```go
type ReviewProvider interface {
    GetReviewSummary(ctx context.Context, repo ExternalRepositoryID, id ExternalObjectID) (ReviewSummary, error)
}

type CIProvider interface {
    ListChecks(ctx context.Context, repo ExternalRepositoryID, commitSHA string) ([]CheckRun, error)
    GetPipeline(ctx context.Context, repo ExternalRepositoryID, pipelineID string) (Pipeline, error)
}
```

### F. Webhook Provider

```go
type WebhookProvider interface {
    Verify(ctx context.Context, headers http.Header, body []byte, secret Secret) error
    Normalize(ctx context.Context, headers http.Header, body []byte) (DomainEvent, error)
}
```

Webhook Adapter 只负责验证与规范化；队列、幂等、重试和业务触发由平台公共事件层负责。

## 3.3 Provider Registry

建议使用显式注册，不使用散落的字符串判断：

```go
type ProviderKind string

const (
    ProviderGitHub ProviderKind = "github"
    ProviderGitLab ProviderKind = "gitlab"
)

type ProviderRegistry interface {
    Register(kind ProviderKind, factory ProviderFactory) error
    Get(instanceID string) (ProviderBundle, error)
}
```

`ProviderBundle` 由 Repository、Issue、Change Request、Review、CI、Webhook 能力组成。某个 Provider 未实现的能力必须返回标准 `ErrCapabilityUnsupported`，并附带机器可读的 capability name。

---

# 4. Provider Capability 模型

每个 Provider Instance 应暴露能力清单：

| Capability | GitHub | GitLab Self-Managed | 首期要求 |
|---|---:|---:|---|
| Repository Discovery | 是 | 是 | P0 |
| SSH Clone/Push | 是 | 是 | P0 |
| HTTPS Clone/Push | 是 | 是 | P1 |
| Branch/Commit/Compare | 是 | 是 | P0 |
| Issue Read/Create/Update | 是 | 是 | P0 |
| PR/MR Read/Create/Update | 是 | 是 | P0 |
| Merge | 是 | 是 | P0 |
| Review Summary | 是 | 是 | P1 |
| Required Approval Detail | 是 | 是 | P1 |
| Webhook Verify/Normalize | 是 | 是 | P1 |
| CI / Pipeline Status | 是 | 是 | P1 |
| Release API | 是 | 是 | P2 |
| OAuth Login | 是 | 可选 | P2 |
| PAT / Access Token | 是 | 是 | P0 |

Capability 必须是运行时可查询的能力，而不是仅根据 Provider 类型推断。不同 GitLab 版本、管理员设置、许可证层级和 Token Scope 可能改变实际能力。

---

# 5. GitLab Self-Managed 接入规格

## 5.1 Provider Instance 配置

建议字段：

```yaml
provider:
  kind: gitlab
  name: engineering-gitlab
  base_url: https://gitlab.example.internal
  api_base_url: https://gitlab.example.internal/api/v4
  web_base_url: https://gitlab.example.internal
  git_transport:
    preferred: ssh
    ssh_host: gitlab.example.internal
    ssh_port: 22
    https_enabled: true
  auth:
    method: personal_access_token
    secret_ref: secret://loopforge/providers/engineering-gitlab/token
  network:
    connect_timeout: 5s
    request_timeout: 20s
    allow_private_network: true
  tls:
    ca_bundle_ref: secret://loopforge/providers/engineering-gitlab/ca
    verify: true
```

要求：

- `base_url` 必须规范化，禁止 URL userinfo。
- `api_base_url` 默认由 `base_url + /api/v4` 推导，但允许显式配置。
- 私有 CA 必须可配置；默认验证 TLS。
- SSH Host Key 必须验证；支持管理员维护 known_hosts。
- 禁止任意用户输入 URL 后由服务端无约束请求，防止 SSRF。
- 允许私网地址必须由管理员明确配置网络策略。
- Provider Instance 必须绑定租户/项目授权范围。

## 5.2 认证方式

首期优先级：

1. Personal Access Token（PAT）
2. Project Access Token
3. Group Access Token
4. OAuth2（后续阶段）

PAT Scope 应按最小权限配置。典型需求包括 API 读写和仓库读写；实际 Scope 必须依据使用的 API、Git Transport 方式和目标 GitLab 版本逐项验证。不要无条件要求管理员级权限。

Credential 规则：

- Token 存 Secret Manager；数据库只保存 `secret_ref`、类型、创建者、过期时间、Scope 摘要、最后验证时间。
- 创建、更新、撤销都写审计事件。
- Token 失效后连接状态变为 `AUTH_FAILED`，不无限重试。
- 日志只显示凭据类型和末尾脱敏标识，不显示完整 Token。
- 不把 Token 拼入 Clone URL、Remote URL 或命令行参数。
- 优先通过环境变量、credential helper 或短生命周期凭证文件注入 Git 子进程。
- 私钥通过 Secret Manager/SSH Agent 注入，禁止持久化到工作区 artifacts。

## 5.3 API 映射

GitLab REST API v4 常用资源：

| LoopForge 能力 | GitLab API | 说明 |
|---|---|---|
| Get Project | `GET /projects/:id` | `:id` 可为数字 ID 或 URL-encoded path |
| List Projects | `GET /projects` | 受 Token 用户可见范围约束 |
| List Issues | `GET /projects/:id/issues` | 项目级 Issue |
| Get Issue | `GET /projects/:id/issues/:issue_iid` | 使用项目内 IID |
| Create Issue | `POST /projects/:id/issues` | 标题、描述、Labels 等 |
| Update Issue | `PUT /projects/:id/issues/:issue_iid` | 更新状态、标签等 |
| List MRs | `GET /projects/:id/merge_requests` | 支持状态过滤 |
| Get MR | `GET /projects/:id/merge_requests/:mr_iid` | 使用项目内 IID |
| Create MR | `POST /projects/:id/merge_requests` | source/target branch |
| Update MR | `PUT /projects/:id/merge_requests/:mr_iid` | 标题、描述、目标分支等 |
| Merge MR | `PUT /projects/:id/merge_requests/:mr_iid/merge` | 必须检查合并条件 |
| Get Pipeline | `GET /projects/:id/pipelines/:pipeline_id` | Pipeline 状态 |
| List Pipelines | `GET /projects/:id/pipelines` | 按 ref/SHA 过滤 |
| Get Commit | `GET /projects/:id/repository/commits/:sha` | Commit 元数据 |
| Compare | `GET /projects/:id/repository/compare` | base/head 比较 |
| Create Webhook | `POST /projects/:id/hooks` | 仅在平台代管 Webhook 时需要 |

重要语义：

- GitLab `project_id` 是项目标识；项目路径可能变更。
- `issue_iid`、`mr_iid` 是项目内编号；不能与数据库主键或全局 ID 混用。
- API 可能返回分页 Header；Adapter 必须完整处理分页。
- GitLab 状态、Merge Status、Detailed Merge Status 和 Approval 规则依版本与配置变化。
- 不能把 `merge_status=can_be_merged` 简化为所有保护规则均已满足。
- MR Approval API 与实际审批策略需要按支持的 GitLab 版本做兼容性测试。

官方 API 文档：
- https://docs.gitlab.com/api/rest/
- https://docs.gitlab.com/api/merge_requests/
- https://docs.gitlab.com/api/issues/
- https://docs.gitlab.com/api/projects/
- https://docs.gitlab.com/api/pipelines/
- https://docs.gitlab.com/user/project/integrations/webhooks/

## 5.4 网络拓扑

私有部署最常见的失败不是 API 代码，而是 LoopForge Server/Worker 无法访问 GitLab。

必须分别验证：

- LoopForge API → GitLab HTTPS API
- Worker/Sandbox → GitLab SSH
- Worker/Sandbox → GitLab HTTPS Git（如果启用）
- GitLab → LoopForge Webhook URL
- LoopForge → Secret Manager
- Worker → Model Provider

不能只在 Web/API 容器内测试连接；必须在实际执行 Git Clone 的 Worker/Sandbox 网络命名空间中测试。

---

# 6. 统一领域数据模型

以下是目标模型，不应未经审计就直接新增重复表。先映射现有 schema，再以最小迁移方式演进。

## 6.1 ProviderInstance

```text
ProviderInstance
- id: internal UUID
- kind: github | gitlab
- name
- base_url
- api_base_url
- web_base_url
- auth_method
- secret_ref
- capabilities_json
- status
- last_verified_at
- created_by
- created_at
- updated_at
```

## 6.2 RepositoryConnection

```text
RepositoryConnection
- id: internal UUID
- provider_instance_id
- external_repository_id
- external_repository_path
- display_name
- web_url
- clone_ssh_url
- clone_https_url
- default_branch
- selected_transport
- project_id
- status
- last_verified_at
```

## 6.3 Credential

```text
Credential
- id
- provider_instance_id
- credential_type
- secret_ref
- scopes_summary
- expires_at
- last_validated_at
- created_by
- revoked_at
```

数据库不保存 Secret 明文。`Credential` 与 `RepositoryConnection` 分离，避免一个凭据只能绑定一个仓库。

## 6.4 ChangeRequest

```text
ChangeRequest
- id: internal UUID
- provider_instance_id
- repository_connection_id
- external_id
- external_iid
- number
- title
- description
- source_branch
- target_branch
- source_commit_sha
- state
- draft
- mergeable_state
- review_state
- web_url
- raw_provider_state
- created_at
- updated_at
- merged_at
```

GitHub PR 和 GitLab MR 统一映射到 ChangeRequest。外部原始字段保留在受控的 raw payload 存储或字段中，避免丢失平台信息。

## 6.5 Domain Identity

```text
ExternalRepositoryID {
  ProviderInstanceID string
  ProviderKind       string
  ExternalID         string
  FullPath           string
}
```

唯一性建议：`(provider_instance_id, external_id)`。不能只用 `full_path`，因为项目路径可重命名；也不能只用 `external_id`，因为不同 GitLab 实例的 ID 可能冲突。

## 6.6 状态归一化

统一状态建议：

- ChangeRequest：`OPEN | DRAFT | CLOSED | MERGED | UNKNOWN`
- Review：`NOT_REVIEWED | REVIEW_REQUIRED | APPROVED | CHANGES_REQUESTED | UNKNOWN`
- CI：`PENDING | RUNNING | PASSED | FAILED | CANCELED | SKIPPED | UNKNOWN`
- Connection：`CONNECTED | DEGRADED | AUTH_FAILED | NETWORK_UNREACHABLE | PERMISSION_DENIED | WEBHOOK_FAILED | UNKNOWN`

必须同时保存：
1. 归一化状态；
2. Provider 原始状态；
3. 状态映射版本；
4. 最近一次同步时间。

`UNKNOWN` 是合法状态，不能将未知值默认映射成成功。

---

# 7. Webhook 与事件可靠性

## 7.1 事件链路

```text
GitHub / GitLab
      |
Webhook Gateway
      |
Provider Signature Verification
      |
Event Normalizer
      |
Idempotency Store
      |
Durable Queue
      |
Event Consumer
      |
Delivery / SOP / Evidence / Notification
```

## 7.2 DomainEvent

```go
type DomainEvent struct {
    ID                 string
    ProviderInstanceID string
    ProviderKind       ProviderKind
    EventType          string
    ExternalEventID    string
    Repository         ExternalRepositoryID
    ObjectType         string
    ObjectID           string
    OccurredAt         time.Time
    ReceivedAt         time.Time
    PayloadRef         string
    SchemaVersion      int
}
```

## 7.3 必须实现的可靠性规则

- GitHub 与 GitLab 各自的签名/Secret 验证。
- 使用恒定时间比较验证 Secret。
- 事件幂等键：Provider Instance + External Event ID；无 Event ID 时使用稳定 Payload Hash 与对象版本组合。
- 重复事件不得重复创建 Delivery、Issue、MR 或 Evidence。
- 事件入队后再返回成功响应；不能在 HTTP Handler 中执行长时间 Agent 工作。
- 失败重试采用指数退避和上限。
- 超过重试上限进入 DLQ，并支持人工重放。
- 处理乱序事件：使用对象更新时间/版本或重新拉取权威资源状态。
- 周期性 Reconciliation：对未闭合 Delivery 和 MR/PR 状态进行对账。
- 保存事件来源、接收时间、验证结果、处理结果和关联 Delivery。
- Webhook endpoint 不应暴露凭据；错误响应不泄漏签名或 Token。

## 7.4 私有部署额外风险

- GitLab 无法访问公网 Webhook URL。
- 内网 DNS 与证书链不受信任。
- 反向代理丢失原始请求头。
- 多个 GitLab Instance 共享同一个 Webhook Secret。
- 用户配置恶意 Webhook URL 引发 SSRF。
- 重放攻击或伪造事件触发 Agent 执行。

应提供管理员可选的 Polling/Reconciliation 模式，Webhook 不可达时仍能检测状态变化，但要设置 API Rate Limit 和轮询间隔。

---

# 8. Delivery 与 SOP 集成

## 8.1 平台无关交付状态机

```text
CREATED
  -> REQUIREMENT_LINKED
  -> SPEC_READY
  -> PLAN_READY
  -> IMPLEMENTING
  -> TESTING
  -> EVIDENCE_READY
  -> REVIEWING
  -> CHANGE_REQUEST_OPEN
  -> CI_PENDING
  -> CI_PASSED
  -> APPROVAL_PENDING
  -> APPROVED
  -> MERGING
  -> MERGED
  -> RELEASE_VERIFIED
```

失败或人工退回路径：

```text
BLOCKED | FAILED | CHANGES_REQUESTED | CANCELED | UNKNOWN
```

状态迁移必须由事件和明确的 Gate 触发，不能仅凭 Agent 文本宣称成功。

## 8.2 SOP 标准链

1. Requirement：记录原始需求和验收目标。
2. Clarify：消除关键未知项。
3. Spec：生成唯一 Goal/Spec 文档。
4. Acceptance：定义可验证的验收条件。
5. Design：确定架构和接口。
6. Plan：形成有依赖关系的任务计划。
7. Tasks：生成可追踪的 Issue/Task。
8. Worktree：在独立工作树与分支中实施；禁止直接在 `main` 开发。
9. Code：Agent 执行代码变更。
10. Test：运行自动化测试与安全检查。
11. Evidence：收集命令、日志、测试结果、Commit SHA、差异摘要。
12. Review：由独立角色审查。
13. PR/MR：通过 Provider Adapter 创建 Change Request。
14. CI：等待平台 Pipeline/Checks。
15. Human Approval：人工审批。
16. Merge：通过平台 API 执行，或由授权用户执行。
17. Release：验证 Tag、Release、部署或发布记录。
18. Evidence Finalization：固化完整交付证据。

## 8.3 Gate 规则

- Gate 1：Issue/Requirement/Goal 关联完整。
- Gate 2：Spec、Acceptance、Design、Plan 存在且版本一致。
- Gate 3：分支与 Worktree 隔离，禁止在 `main` 上直接开发。
- Gate 4：测试成功，失败项有明确豁免记录。
- Gate 5：Evidence 完整且可复现。
- Gate 6：Reviewer 与 Implementer 角色隔离。
- Gate 7：PR/MR 已创建且目标分支正确。
- Gate 8：Required CI 全部成功。
- Gate 9：Required Approval 满足平台规则。
- Gate 10：Merge 后 Commit SHA、目标分支和最终状态验证通过。
- Gate 11：Release 证据、版本号和 Changelog 一致。

Agent 只能请求 Gate 通过；Gate 由确定性检查或有权限的人执行。

---

# 9. UI / API 规格

## 9.1 集成管理页面

建议入口：`Settings → Integrations → Git Providers`

功能：

- Provider Instance 列表。
- 新建 GitHub / GitLab Self-Managed 连接。
- 连接健康状态。
- 凭据配置和轮换。
- Git Transport 选择。
- Repository Discovery 与授权绑定。
- Webhook 状态和最近事件。
- Pipeline 状态检查。
- 连接诊断报告。
- 禁用、撤销和删除连接。

## 9.2 GitLab 连接向导

1. 输入 Instance Name。
2. 输入 GitLab Base URL。
3. 选择 PAT / Project Token / Group Token。
4. 通过 Secret 安全输入 Token。
5. 测试 HTTPS API 连通性。
6. 验证权限和可见项目范围。
7. 测试 Worker 到 GitLab 的 SSH/HTTPS Git Transport。
8. 选择 Repository。
9. 测试 Clone/Fetch/Push 权限（Push 测试应使用临时分支并自动清理）。
10. 配置 Webhook 或启用 Polling。
11. 运行端到端诊断。
12. 保存连接。

## 9.3 健康诊断结果

每项检查返回：

```json
{
  "check": "git_transport_ssh",
  "status": "passed",
  "checked_at": "RFC3339",
  "latency_ms": 123,
  "message": "SSH authentication succeeded",
  "remediation": null
}
```

不得在 `message` 中返回 Token、私钥、完整命令行 Secret 或敏感 URL userinfo。

## 9.4 API 端点

最终命名必须服从现有 HTTP API 规范；以下为建议语义：

- `GET /api/providers`
- `POST /api/providers`
- `GET /api/providers/:id`
- `PATCH /api/providers/:id`
- `POST /api/providers/:id/validate`
- `POST /api/providers/:id/credentials/rotate`
- `POST /api/providers/:id/credentials/revoke`
- `GET /api/providers/:id/repositories`
- `POST /api/repository-connections`
- `POST /api/repository-connections/:id/validate`
- `GET /api/repository-connections/:id/capabilities`
- `GET /api/repository-connections/:id/events`
- `POST /api/webhooks/:provider/:instanceKey`

必须先检查现有 API 路由，避免重复造轮子。Webhook 路由需使用不可猜测的 Instance Key 或受控映射，但不能将该 Key 当作唯一认证机制。

---

# 10. 安全威胁模型

| 威胁 | 风险 | 控制 |
|---|---|---|
| PAT 泄漏 | 仓库与项目被接管 | Secret Manager、最小权限、脱敏、轮换 |
| SSH 私钥泄漏 | 仓库被未授权写入 | Secret 注入、短期凭证、禁止写入 artifacts |
| SSRF | 内网服务被探测/访问 | URL allowlist、DNS/IP 检查、出站网络策略 |
| Webhook 伪造 | 未授权触发交付 | 签名验证、幂等、重放防护 |
| 恶意仓库脚本 | Sandbox/宿主机被攻破 | 隔离 Runner、最小权限、禁止不可信 Docker Socket |
| Agent 越权 Merge | 绕过审批 | Merge Gate、独立权限、保护分支 |
| 日志泄漏 | Token/代码敏感信息暴露 | 结构化日志、敏感字段过滤 |
| GitLab 私有 CA 被绕过 | MITM | 默认 TLS 验证、管理员 CA 配置 |
| 错误租户访问 | 仓库数据泄漏 | Provider/Repository 授权校验 |
| 事件重复或乱序 | 重复交付/状态回退 | Idempotency、版本检查、Reconciliation |

额外要求：LoopForge 默认 Docker Socket Sandbox 的信任边界必须纳入部署评估。不能把 GitLab Provider 隔离误认为执行环境隔离。面向不可信多租户时，必须先建立更强的 Runner/Sandbox 隔离。

---

# 11. 仓库审计与实施顺序

## 11.1 第一阶段：只读审计，不写代码

从仓库当前主线/目标版本检查：

- `internal/tracker/**`
- `internal/repo/**`
- `internal/httpapi/**`
- `internal/runtime/**`
- `internal/runner/**`
- `internal/engine/**`
- `migrations/**`
- `web/**`
- `docs/**`
- GitHub OAuth / Token / Repository / Delivery 相关实现
- Git Clone / Push / Worktree / Credential 注入代码
- Webhook / CI / PR 状态同步代码

审计问题：

1. Provider 身份是否已存在？
2. GitHub 逻辑与业务层耦合点有哪些？
3. 哪些接口可以复用？
4. 数据库是否以 GitHub 专有字段为主？
5. 是否存在 `github:` 这样的身份编码？其兼容语义是什么？
6. PR/Issue 是否由 `internal/tracker` 统一管理？
7. API Token 的存储、读取、脱敏、撤销路径是什么？
8. Git 命令运行在哪个容器/Worker？
9. Webhook 入口和签名校验在哪里？
10. 测试是否有 GitHub Fake Server、Integration Test 或真实 E2E？

输出 `00-current-state-audit.md` 与 ADR。未完成审计前，不创建大量新目录或迁移表。

## 11.2 第二阶段：最小架构改造

原则：先扩展既有模块，不做机械目录搬迁。

- 优先扩展现有 `internal/tracker`。
- 如果 Repository Provider 已有位置，则复用；没有再新增最小接口。
- Git Transport 继续使用现有 Git CLI 逻辑。
- 新增 GitLab Adapter。
- Provider Instance 与 Credential 生命周期独立。
- 把平台特有状态映射放在 Adapter。
- 把统一状态机放在 Delivery Domain。

## 11.3 推荐目录（仅在审计确认缺失时新增）

```text
internal/
  provider/
    types.go
    registry.go
    capability.go
  tracker/
    contract.go
    github/
    gitlab/
  repository/
    contract.go
    github/
    gitlab/
  webhook/
    normalize.go
    github/
    gitlab/
  ci/
    contract.go
    github/
    gitlab/
  delivery/
    state.go
    transition.go
    evidence.go
```

不要为了追求目录整齐，把已有稳定实现整体搬迁。迁移应单独 PR，并提供兼容测试。

---

# 12. GitHub Issues / PR 执行计划

建议在 `Tencent/LoopForge` 创建一个 Epic/Project：

**Epic:** `Multi-Git Integration v1.0`

## 12.1 Issue 列表

### MGIT-001 — Current-State Audit
- 审计 GitHub 连接、Repository、Tracker、Delivery、Git Transport、Webhook、CI。
- 输出源码路径、符号、调用链和测试证据。
- 验收：关键未知项全部标记状态；无未经验证的架构假设。

### MGIT-002 — Provider Architecture ADR
- 定义 Provider Instance、Provider Registry、Capability、Transport/Collaboration 分层。
- 对比“扩展 tracker”与“新增 provider package”的迁移成本。
- 验收：架构决策经 Review；GitHub 兼容方案明确。

### MGIT-003 — Domain Contract
- 定义 ExternalRepositoryID、Issue、ChangeRequest、ReviewSummary、Pipeline、DomainEvent。
- 定义状态映射与 Unsupported Capability。
- 验收：契约测试覆盖 GitHub 和 GitLab Adapter。

### MGIT-004 — Provider Instance Persistence
- 设计数据库迁移。
- Provider Instance、Credential、Repository Connection 分离。
- 验收：升级、回滚/恢复策略、索引、唯一性与数据迁移测试通过。

### MGIT-005 — GitLab Authentication
- PAT 首期。
- Secret Reference、安全存储、权限验证、过期和撤销诊断。
- 验收：Token 不出现在日志、DB 明文、URL、错误消息和 artifacts。

### MGIT-006 — GitLab Repository Adapter
- Project Discovery、Project Metadata、Branches、Commits、Compare。
- 验收：分页、路径编码、项目重命名、权限不足、网络失败测试通过。

### MGIT-007 — GitLab Git Transport
- SSH Clone/Fetch/Branch/Commit/Push。
- HTTPS 可作为后续/可选路径。
- 验收：实际 Worker/Sandbox 完成真实私有仓库操作；Host Key 校验开启。

### MGIT-008 — GitLab Issue Adapter
- Issue Read/Create/Update/Comment。
- 验收：IID 映射正确；分页和权限失败语义正确。

### MGIT-009 — GitLab MR Adapter
- MR Read/Create/Update/Merge。
- Draft、source/target branch、merge conflict、protected branch。
- 验收：真实 GitLab MR 生命周期 E2E 通过。

### MGIT-010 — Review and Approval
- 归一化 Review Summary。
- 检查 Required Approval 与审批用户信息。
- 验收：不能将未知审批状态映射为 Approved；不满足审批时 Merge 必须失败。

### MGIT-011 — Webhook Adapter
- GitLab Secret Token/签名验证（按 GitLab 支持机制实现）。
- Event Normalizer、Idempotency、Retry、DLQ。
- 验收：伪造事件拒绝；重复事件只处理一次；乱序事件不回退状态。

### MGIT-012 — GitLab Pipeline Adapter
- Pipeline/Commit 状态同步。
- 验收：Pending/Running/Success/Failed/Canceled 映射正确。

### MGIT-013 — Integration UI
- Provider Instance 管理、Repository Discovery、Health Diagnostics。
- 验收：错误信息可操作；敏感字段脱敏；权限范围明确。

### MGIT-014 — GitHub Regression
- 验证现有 OAuth、Repository、Issue/PR、Delivery、Webhook、CI 路径。
- 验收：原有行为无回归。

### MGIT-015 — Provider Contract and E2E Suite
- 共享契约测试。
- GitHub SaaS + GitLab Self-Managed 真实集成环境。
- 验收：P0/P1 测试全部通过。

### MGIT-016 — Security Review
- Token、SSH、SSRF、Webhook、Sandbox、Tenant Boundary。
- 验收：无未处理 Critical/High 风险。

### MGIT-017 — Documentation and Operations
- GitLab 安装配置、权限、网络、证书、Webhook、排障和升级指南。
- 验收：新管理员可按文档独立完成接入。

### MGIT-018 — Release Gate
- 汇总测试、Evidence、兼容性、安全和升级结果。
- 验收：所有 Required Gate 通过，版本和 Changelog 一致。

## 12.2 依赖顺序

```text
MGIT-001
   |
MGIT-002
   |
MGIT-003
   |
MGIT-004 ---- MGIT-005
   |              |
   +---- MGIT-006 +---- MGIT-007
                  |
        MGIT-008 + MGIT-009
                         |
                MGIT-010 + MGIT-011 + MGIT-012
                         |
                    MGIT-013
                         |
               MGIT-014 + MGIT-015
                         |
                    MGIT-016
                         |
                    MGIT-017
                         |
                    MGIT-018
```

## 12.3 Agent Teams

| 角色 | 职责 | 权限边界 |
|---|---|---|
| Architect | 审计、ADR、接口和状态模型 | 不直接合并 |
| GitLab Adapter Engineer | GitLab API Adapter | 不改 SOP Engine |
| Git Transport Engineer | SSH/HTTPS、Worktree、凭据注入 | 不读取 Secret 明文到日志 |
| Data Engineer | Migration、Repository、Credential 模型 | 不修改 Provider API 语义 |
| Frontend Engineer | Provider UI、诊断向导 | 不处理 Token 持久化 |
| QA/E2E Engineer | Contract、真实 GitLab E2E | 独立于实现角色 |
| Security Reviewer | Threat Model、凭据、SSRF、Webhook | 独立 Review |
| Release Manager | Evidence、Gate、Changelog、Tag | 不豁免失败 Gate |

Agent 不应同时承担实现和最终独立审查。每个 Issue 必须有目标、验收条件、依赖、Evidence 路径和回滚方案。

---

# 13. 测试策略与 Agent Eval

## 13.1 测试分层

1. Unit：状态映射、URL 规范化、IID/ID 映射、错误转换。
2. Contract：同一组契约测试运行 GitHub 与 GitLab Adapter。
3. Integration：Fake Server/HTTP Fixture 验证 API 行为。
4. E2E：真实 GitLab Self-Managed 测试实例。
5. Security：Secret Redaction、SSRF、Webhook Forgery、权限边界。
6. Regression：GitHub 既有能力。
7. Operational：GitLab 网络不可达、Token 过期、证书错误、Webhook 失败、Pipeline 超时。

## 13.2 最小真实 E2E 环境

建议在隔离测试环境部署一个受支持版本的 GitLab Self-Managed：

- 测试 Group
- 测试 Project
- 测试 PAT（最小 Scope）
- SSH Deploy Key 或测试用户 SSH Key
- Protected Branch
- 至少一个 CI Pipeline
- Webhook 指向测试 LoopForge
- 可重建、可销毁的测试数据

必须记录 GitLab 版本、配置、License/Tier（若影响功能）、Runner 版本和测试日期。不能只在 GitLab SaaS 上测试后宣称 Self-Managed 已支持。

## 13.3 测试矩阵

| 场景 | GitHub | GitLab Self-Managed |
|---|---:|---:|
| Provider Connect / Validate | Required | Required |
| Credential Expired | Required | Required |
| Repository Discovery | Required | Required |
| SSH Clone / Fetch / Push | Required | Required |
| Branch / Commit / Compare | Required | Required |
| Issue CRUD | Required | Required |
| PR/MR Create / Update | Required | Required |
| Review Summary | Required | Required |
| Required Approval Gate | Required | Required |
| Merge Allowed / Denied | Required | Required |
| Webhook Signature | Required | Required |
| Duplicate Webhook | Required | Required |
| Out-of-order Webhook | Required | Required |
| CI / Pipeline Status | Required | Required |
| Network Failure / Recovery | Required | Required |
| Token Revocation | Required | Required |
| Evidence Finalization | Required | Required |

## 13.4 Agent Eval 场景

至少建立以下 Eval：

- `provider_connect_success`
- `provider_auth_failure_no_secret_leak`
- `gitlab_private_ssh_clone_push`
- `repository_path_rename`
- `issue_iid_mapping`
- `mr_create_and_update`
- `mr_required_approval_block`
- `mr_merge_after_approval`
- `webhook_duplicate_idempotency`
- `webhook_forgery_rejection`
- `pipeline_failure_blocks_release`
- `github_regression`
- `provider_switch_without_sop_change`
- `credential_rotation`
- `worker_network_unreachable`

每个 Eval 保存 Input、Expected State、Observed State、Evidence、Model/Agent Version、Tool Trace、Failure Classification。Agent 不能仅通过自然语言报告判定通过。

---

# 14. Evidence 与 Release Gate

## 14.1 每个 Issue 的 Evidence

```text
evidence/
  multi-git/
    MGIT-001/
      audit.md
      source-map.json
      test-results/
      review.md
    MGIT-009/
      api-contract.json
      e2e-report.json
      gitlab-version.txt
      mr-lifecycle.log
      screenshots/
```

Evidence 不应包含 Token、SSH 私钥、真实用户数据或不必要的仓库内容。

## 14.2 Evidence Manifest

```json
{
  "goal": "multi-git-integration-v1",
  "issue": "MGIT-009",
  "repository": "Tencent/LoopForge",
  "commit_sha": "<full-sha>",
  "provider": "gitlab",
  "provider_instance": "test-gitlab",
  "tests": [
    {
      "name": "mr_lifecycle_e2e",
      "status": "passed",
      "artifact": "e2e-report.json"
    }
  ],
  "review": {
    "status": "approved",
    "reviewer": "<reviewer-id>"
  },
  "created_at": "<RFC3339>"
}
```

占位符必须由实际流水线生成，禁止手工伪造。

## 14.3 Release Gate

### G0 — Scope
- Scope、Non-goals、支持版本明确。

### G1 — Architecture
- ADR 批准。
- Provider/Transport/Delivery 分层清晰。
- 无 SOP Engine 平台分支。

### G2 — Data
- Migration 成功。
- 唯一性、索引、数据迁移、备份恢复验证通过。

### G3 — Git Transport
- GitHub 与 GitLab Worker 实际 Clone/Push 成功。
- SSH Host Key 校验开启。

### G4 — Collaboration
- Issue、PR/MR 生命周期通过。

### G5 — Webhook/CI
- 签名、幂等、重试、Pipeline 状态通过。

### G6 — Security
- 无未处理 Critical/High。
- Token/私钥泄漏测试为零。
- SSRF 与租户隔离测试通过。

### G7 — Regression
- GitHub 既有能力全部通过。

### G8 — E2E
- 真实 GitLab Self-Managed 全链路通过。

### G9 — Documentation
- 部署、连接、权限、故障排查、升级和回滚文档完成。

### G10 — Release
- Changelog、版本号、镜像、迁移说明、Release Notes、Evidence Manifest 一致。

任何 Required Gate 失败，状态保持 `BLOCKED`，不得标记 `RELEASED`。

---

# 15. 1 / 7 / 30 / 90 天执行计划

## Day 1：建立可信基线

- [ ] 固定 LoopForge 上游版本和目标 commit。
- [ ] 完成 MGIT-001 源码审计。
- [ ] 画出现有 GitHub Connect → Repository → Git → Tracker → Delivery 调用链。
- [ ] 列出 GitHub 专有字段与硬编码位置。
- [ ] 创建 Multi-Git ADR。
- [ ] 建立真实 GitLab Self-Managed 测试环境。
- [ ] 定义 P0/P1 能力与不支持能力。
- [ ] 形成风险清单和任务依赖图。

**Day 1 输出：** 审计报告、ADR 草案、接口草案、E2E 环境、Issues。

## Day 7：GitLab MVP

- [ ] Provider Instance 配置与持久化。
- [ ] PAT 安全存储与验证。
- [ ] GitLab Project Discovery。
- [ ] Worker/Sandbox SSH Clone/Fetch/Push。
- [ ] GitLab Issue Read/Create/Update。
- [ ] GitLab MR Create/Get/Update。
- [ ] GitHub Contract Tests 不回归。
- [ ] 最小 Provider UI 与诊断报告。

**Day 7 目标：** 可以从 LoopForge 选择 GitLab 私有仓库，运行 SOP，推送分支并创建 MR。此时不宣称完整 RELEASED。

## Day 30：完整协作闭环

- [ ] MR Review / Approval Summary。
- [ ] Merge Gate 与 Merge API。
- [ ] GitLab Webhook 验证与幂等。
- [ ] Pipeline 状态同步。
- [ ] Reconciliation / Retry / DLQ。
- [ ] Provider Capability Registry。
- [ ] GitHub + GitLab 共享 Contract Tests。
- [ ] 真实 GitLab Self-Managed E2E。
- [ ] Security Review。
- [ ] 文档与运维手册。

**Day 30 目标：** GitHub 与 GitLab 使用同一 SOP 完成 Issue → MR/PR → CI → Approval → Merge。

## Day 90：生产化与扩展

- [ ] 多 GitLab Instance 管理。
- [ ] Credential Rotation / Revocation。
- [ ] GitLab OAuth（如确有用户场景）。
- [ ] GitLab 版本兼容矩阵。
- [ ] 事件恢复、重放、DLQ 运维界面。
- [ ] 性能、限流、故障注入测试。
- [ ] 部署安全基线与多租户边界评估。
- [ ] Release Gate 自动化。
- [ ] 发布 Multi-Git Integration v1.0。

---

# 16. 关键指标

| 指标 | 目标 |
|---|---:|
| GitHub 核心流程回归 | 0 |
| GitLab P0 Contract Tests | 100% |
| GitLab MR 生命周期 E2E | 100% |
| SOP 对 Provider 的条件分支 | 0 |
| Token/私钥明文日志 | 0 |
| 重复 Webhook 导致重复交付 | 0 |
| 未审批保护分支 Merge | 0 |
| Evidence 可追溯率 | 100% |
| Provider Health Check 覆盖 | 100% |
| Required Gate 自动化率 | 尽可能 100% |
| 未知 Provider 状态误判为成功 | 0 |

指标必须由测试报告、审计日志或运行数据产生，不接受 Agent 自评数字。

---

# 17. 风险与决策记录

## R1：把 Git Transport 与 API Provider 混为一谈
- 影响：SSH 可用但 MR/Issue 不可用，用户误以为集成完成。
- 对策：分别展示 API、Git SSH、Webhook、CI 健康状态。

## R2：GitLab 版本差异
- 影响：Approval、Merge Status、Pipeline 字段差异。
- 对策：定义支持版本范围；Adapter 做能力探测；未知状态 fail-closed。

## R3：私有网络无法回调
- 影响：Webhook 不可达。
- 对策：管理员可配置内网回调或 Polling/Reconciliation。

## R4：现有 GitHub 逻辑耦合过深
- 影响：重构范围膨胀、回归风险高。
- 对策：先审计调用链；最小接口抽象；每一步保持 GitHub 回归测试。

## R5：Credential 注入与 Sandbox 边界
- 影响：仓库脚本或 Agent 读取凭据。
- 对策：短期凭据、最小权限、工作区外注入、任务完成撤销；不可信代码使用强隔离 Runner。

## R6：平台状态语义不等价
- 影响：错误判定 Approved 或 Mergeable。
- 对策：保留原始状态、Capability 和 UNKNOWN；关键 Gate 直接查询权威 API。

## R7：过早支持所有 Git Provider
- 影响：抽象复杂、测试矩阵失控。
- 对策：首期仅 GitHub + GitLab Self-Managed；第三个 Provider 出现真实需求后再扩展。

---

# 18. 不做什么

- 不复制一套 GitLab 专用 SOP。
- 不在 Agent Prompt 中写死 GitHub/GitLab API 调用。
- 不在 SOP Engine 中添加大量 Provider `switch`。
- 不把 GitLab 项目路径作为唯一身份。
- 不把 MR IID 当数据库 ID。
- 不把 GitLab API Token 放入 Git Remote URL。
- 不默认使用管理员 PAT。
- 不关闭 TLS 或 SSH Host Key 验证。
- 不因 Clone/Push 成功就宣布 GitLab 集成完成。
- 不使用 Mock E2E 替代真实 Self-Managed GitLab。
- 不在没有审计前大规模搬迁 `internal/tracker`。
- 不为抽象而抽象地实现所有未来 Provider。

---

# 19. 最终推荐路径

**采用“先审计、再抽象、后适配”的增量演进路线。**

1. 审计 LoopForge 当前 GitHub、Tracker、Repository、Credential、Delivery、Webhook 和 Git Transport。
2. 定义最小平台无关契约，先确保现有 GitHub Adapter 满足该契约。
3. 建立 Provider Instance 与 Credential 分离的配置模型。
4. 实现 GitLab PAT + Project Discovery + SSH Transport。
5. 实现 Issue 与 MR 生命周期。
6. 实现 Approval、Webhook、Pipeline 和 Reconciliation。
7. 用真实 GitLab Self-Managed 完成 E2E。
8. 通过 Security、Regression、Evidence、Release Gate。
9. 发布 v1.0，再评估 GitLab OAuth、GHE 和其他 Git Provider。

**不要以“支持 GitLab Clone”作为完成标准。唯一有效的完成标准是：同一套 LoopForge SOP 能够在 GitHub 与 GitLab Self-Managed 上独立完成、验证并审计完整的软件交付闭环。**

---

# 附录 A：执行约定

- 所有代码变更必须关联 `MGIT-*` Issue。
- 每个 Issue 必须有 Acceptance Criteria 和 Evidence。
- 禁止直接在 `main` 开发。
- 使用独立 Git Worktree；建议统一放在 `.worktree/workspaces/`。
- 一个组件/项目只维护一份主 Goal/Spec，避免重复拆分文档。
- 每个 PR 只解决一个可验收的变更单元。
- Schema/Contract 变更必须更新版本与兼容性说明。
- 失败 Gate 不得通过人工修改状态绕过。
- Release 前验证 Changelog、版本号、Migration、镜像和 Evidence 一致。
- Agent 实现者与最终 Reviewer 分离。
- 任何无法验证的能力标记为 `UNKNOWN`，不能写成 `SUPPORTED`。

# 附录 B：实施时的源码核验命令

在 LoopForge 仓库根目录运行：

```bash
git status --short
git rev-parse HEAD
git branch --show-current

rg -n "github|GitHub|tracker|pull.?request|merge.?request|webhook|pipeline|clone_url|ssh_url" \
  internal web migrations docs

rg -n "Clone|Fetch|Push|Checkout|Worktree|credential|token|secret" \
  internal

find internal -maxdepth 3 -type d | sort
find migrations -maxdepth 2 -type f | sort
```

随后为每个关键调用链记录：

```text
Entry Point
  -> Service
  -> Domain Contract
  -> Provider Adapter
  -> External API / Git CLI
  -> Persistence
  -> Evidence / Tests
```

命令输出和审计结果应进入 `00-current-state-audit.md`，不要只把搜索结果贴进文档。

# 附录 C：官方参考

- LoopForge Repository: https://github.com/Tencent/LoopForge
- LoopForge README: https://github.com/Tencent/LoopForge/blob/main/README.md
- LoopForge Architecture: https://github.com/Tencent/LoopForge/blob/main/docs/architecture.md
- LoopForge API Docs: https://github.com/Tencent/LoopForge/tree/main/docs/api
- GitLab REST API: https://docs.gitlab.com/api/rest/
- GitLab Projects API: https://docs.gitlab.com/api/projects/
- GitLab Issues API: https://docs.gitlab.com/api/issues/
- GitLab Merge Requests API: https://docs.gitlab.com/api/merge_requests/
- GitLab Pipelines API: https://docs.gitlab.com/api/pipelines/
- GitLab Webhooks: https://docs.gitlab.com/user/project/integrations/webhooks/

> 本文是可执行项目基线，不是对 LoopForge 当前源码实现状态的虚构审计报告。MGIT-001 必须先核验真实代码、现有迁移和 API，再将目标契约映射到实际文件与符号。
