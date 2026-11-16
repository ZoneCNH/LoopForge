# Multi-Git Integration — Provider Contracts（MGIT-003）

- 状态：契约草案 v1，供 `internal/tracker/gitlab/` 实现直接使用
- 前置：`00-current-state-audit.md`、`01-architecture-spec.md`（ADR-1~4 已定）
- 设计原则：契约定义在领域对象层，不绑定传输（REST/MCP 均可实现）；所有外部状态映射 fail-closed（未知 → `UNKNOWN`，禁止默认成功）

---

## 1. 仓库身份编码

### 1.1 `GongfengProjectID` 字符串格式

```text
gitlab:<instance_key>/<url-encoded-full-path>
```

- `instance_key`：Provider Instance 的规范化 base_url，**原样保留 scheme+host**，去尾斜杠；例如 `https://gitlab.example.com`、`http://gitlab.internal:8080`
- `full-path`：GitLab 项目 path（`group/sub/repo`），整体 `url.PathEscape`；解析端必须 `PathUnescape`
- 例：`gitlab:https%3A%2F%2Fgitlab.example.com/group%2Fsub%2Frepo`
  - 即 `gitlab:` + escape(instance_key) + `/` + escape(full_path)
  - instance_key 本身含 `:` `/` 必须转义，否则无法与 full_path 分界——**编码规则：整个 `instance_key/full_path` 拼接后整体 `url.PathEscape`，前缀 `gitlab:` 不参与编码**

```go
// internal/domain/repository_ref.go（新增）
func FormatGitLabProjectRef(instanceKey, fullPath string) string
func ParseGitLabProjectRef(project string) (instanceKey, fullPath string, ok bool)
```

- `github:`、工蜂数字 ID、TAPD 项目 ID 的既有解析不受影响；`ParseGitLabProjectRef` 只在 `gitlab:` 前缀时返回 ok。
- `IsSafeGongfengProjectID`（`internal/domain/project.go:288`）需扩展白名单允许 `gitlab:` 前缀，否则仓库保存路径会拒绝。

### 1.2 `RepositoryExternalRef`（内嵌 repositories payload JSON）

> as-built：无独立 `external_ref_json` 列；`domain.Repository.ExternalRef` 随仓库 payload JSON 持久化，API 输出同字段。

```go
// domain.RepositoryExternalRef —— Repository.ExternalRef 的反序列化目标
type RepositoryExternalRef struct {
    ProviderKind  string `json:"providerKind"`            // "gitlab"
    InstanceKey   string `json:"instanceKey"`             // 同 §1.1
    ExternalID    string `json:"externalId"`              // GitLab 数字 project id（字符串形态）
    FullPath      string `json:"fullPath"`                // group/sub/repo
    APIBaseURL    string `json:"apiBaseUrl"`              // instance_key + "/api/v4"
    DefaultBranch string `json:"defaultBranch,omitempty"`
    SSHCloneURL   string `json:"sshCloneUrl,omitempty"`   // git@host:group/repo.git
    HTTPSCloneURL string `json:"httpsCloneUrl,omitempty"`
}
```

解析优先级：`Repository.ExternalRef` 有效 → 用结构化字段；否则回退 `ParseGitLabProjectRef(GongfengProjectID)`，`ExternalID` 留空时 GitLab adapter 首次调用 `GET /projects/:fullPath` 回填（path 可能 rename，以数字 id 为准——Spec §5.3 要求）。catalog 写路径一律由服务端从规范化 ref 重建 ExternalRef，客户端提交值不可信。

## 2. CollaborationProvider 契约

```go
// internal/tracker/provider.go
package tracker

// ProviderKind 复用字符串常量，避免新类型系统
type ProviderKind string

const (
    ProviderGitHub ProviderKind = "github"
    ProviderGitLab ProviderKind = "gitlab"
)

// ExternalRepoRef 标识一个外部仓库实例（不含本地 Repository.id）
type ExternalRepoRef struct {
    Kind        ProviderKind
    InstanceKey string // gitlab: base_url；其他: ""
    ExternalID  string // gitlab: 数字 id；github: "owner/repo"
    FullPath    string // gitlab: group/sub/repo；github: owner/repo
    WebURL      string // 展示用
}

// CollaborationProvider 是平台无关的协作 API 契约。
// 实现方：githubProvider（包装现有 githubClient）、gitlabProvider、gongfeng/tapd 暂不实现
// （MCP 路径保持现状，不强制套契约）。
type CollaborationProvider interface {
    Kind() ProviderKind

    // Repository
    GetProject(ctx context.Context, ref ExternalRepoRef) (ProviderProject, error)
    ListProjects(ctx context.Context, q ProjectQuery) ([]ProviderProject, error)
    Compare(ctx context.Context, ref ExternalRepoRef, base, head string) (ProviderCompare, error)

    // Issue
    ListIssues(ctx context.Context, ref ExternalRepoRef, q IssueQuery) (IssuePage, error)
    GetIssue(ctx context.Context, ref ExternalRepoRef, iid int) (domain.Issue, error)
    CreateIssue(ctx context.Context, ref ExternalRepoRef, in CreateIssueInput) (domain.Issue, error)
    UpdateIssueState(ctx context.Context, ref ExternalRepoRef, iid int, state string) error
    AddIssueComment(ctx context.Context, ref ExternalRepoRef, iid int, body string) error

    // Change Request（PR/MR 统一）
    FindChangeRequestByBranch(ctx context.Context, ref ExternalRepoRef, sourceBranch, targetBranch string) (domain.DeliveryMR, bool, error)
    GetChangeRequest(ctx context.Context, ref ExternalRepoRef, iid int) (ChangeRequestDetail, error)
    CreateChangeRequest(ctx context.Context, ref ExternalRepoRef, in CreateChangeRequestInput) (domain.DeliveryMR, error)
    MergeChangeRequest(ctx context.Context, ref ExternalRepoRef, iid int, in MergeInput) error

    // 状态查询（polling 对账用）
    GetChangeRequestState(ctx context.Context, ref ExternalRepoRef, iid int) (MRStateSnapshot, error)

    // CI（P1；未实现返回 ErrCapabilityUnsupported）
    ListPipelines(ctx context.Context, ref ExternalRepoRef, sha string) ([]ProviderPipeline, error)

    Capabilities() ProviderCapabilities
}
```

### 2.1 输入输出类型

```go
type IssueQuery struct {
    State   string // "opened" | "closed" | "all"（归一化后映射各平台）
    Page    int
    PerPage int // 上限 100
}

type IssuePage struct {
    Items      []domain.Issue
    NextPage   int // 0 表示无下一页
    HasMore    bool
}

type CreateIssueInput struct {
    Title       string
    Description string
    Labels      []string
}

type CreateChangeRequestInput struct {
    SourceBranch string
    TargetBranch string
    Title        string
    Description  string
    Draft        bool
}

type MergeInput struct {
    Method  string // "merge" | "squash" | "rebase"；空 = 平台默认
    SHA     string // 乐观锁：head sha，不匹配则失败（对齐 githubClient 现有 sha 参数）
    Message string // merge commit message
}

type MRStateSnapshot struct {
    State         string    // 归一化：opened | merged | closed | reopened | unknown
    RawState      string    // 平台原始 state，审计用
    TargetBranch  string
    MergeStatus   string    // 平台原始 detailed_merge_status / mergeable_state
    MergedAt      time.Time // 平台权威 merged_at，零值 = 未合并
    CheckedAt     time.Time // 本次查询时间
}

type ChangeRequestDetail struct {
    domain.DeliveryMR
    Draft           bool
    MergeStatus     string // 归一化：mergeable | blocked | unknown
    ApprovalSummary *ApprovalSummary // nil = 平台无此能力或未查
    PipelineSummary *PipelineSummary // nil = 无 CI 信息
}

type ApprovalSummary struct {
    RequiredApprovals int
    ApprovedBy        int
    State             string // approved | changes_requested | review_required | unknown
}

type PipelineSummary struct {
    State string // pending | running | passed | failed | canceled | skipped | unknown
    WebURL string
    SHA    string
}

type ProviderProject struct {
    ExternalID   string // 数字 id（字符串）
    FullPath     string
    WebURL       string
    SSHCloneURL  string
    HTTPSCloneURL string
    DefaultBranch string
}

type ProviderCompare struct {
    Ahead  int
    Behind int
    Files  int
}

type ProviderPipeline struct {
    ID        int
    Status    string // GitLab status 原样
    WebURL    string
    SHA       string
    Ref       string
}

type ProviderCapabilities struct {
    Issues         bool
    ChangeRequests bool
    Merge          bool
    Approvals      bool
    Pipelines      bool
    Webhooks       bool
    OAuth          bool
}
```

## 3. 错误分类

```go
var (
    ErrCapabilityUnsupported = errors.New("capability unsupported")   // 携带 capability name
    ErrAuthentication        = errors.New("provider authentication failed")
    ErrPermissionDenied      = errors.New("provider permission denied")
    ErrNotFound              = errors.New("external object not found")
    ErrConflict              = errors.New("external object conflict")   // e.g. merge sha mismatch
    ErrRateLimited           = errors.New("provider rate limited")
    ErrUnavailable           = errors.New("provider unavailable")
)
```

规则：
- HTTP 401/403 → `ErrAuthentication`/`ErrPermissionDenied`；连接失败/5xx/超时 → `ErrUnavailable`；404 → `ErrNotFound`；409 → `ErrConflict`。
- 错误消息**不得**包含 token、完整 URL userinfo、请求/响应 body 原文（body 可能含敏感字段）。日志只记 status code + endpoint path。
- `Service` 层把这些错误映射到 `domain.SyncLog` 条目与 connection status（`AUTH_FAILED`/`NETWORK_UNREACHABLE`/`PERMISSION_DENIED`，见 Spec §6.6）。

## 4. 状态映射表（fail-closed）

### 4.1 GitLab MR state → 归一化

| GitLab `state` | `MRStateSnapshot.State` | `DeliveryMR.State` |
|---|---|---|
| `opened` | `opened` | `opened` |
| `merged` | `merged` | `merged` |
| `closed` | `closed` | `closed` |
| `locked` | `unknown` | `unknown` |
| 其他/空 | `unknown` | `unknown` |

`reopened`：GitLab MR 无 reopened state（reopen 后 state=opened）；如 API 返回则映射 `opened`。

### 4.2 GitLab `detailed_merge_status` → `MergeStatus`

| detailed_merge_status | MergeStatus |
|---|---|
| `mergeable` | `mergeable` |
| `checking` / `unchecked` / `preparing` | `unknown`（异步检查中，禁止当 mergeable） |
| `ci_must_pass` / `discussions_not_resolved` / `blocked_status` / `broken_status` / `not_approved` / `external_status_checks` / `requested_changes` / `draft_status` / `policies_denied` 等 | `blocked` |
| 其他/空 | `unknown` |

Merge 执行前必须重查 `detailed_merge_status`；`checking`/`unchecked` 时返回 `ErrConflict` 让调用方重试，不得直接 merge。

### 4.3 GitLab Pipeline status → `PipelineSummary.State`

| GitLab `status` | 归一化 |
|---|---|
| `created`/`waiting_for_resource`/`preparing`/`pending` | `pending` |
| `running` | `running` |
| `success` | `passed` |
| `failed` | `failed` |
| `canceled`/`skipped`/`manual` | `canceled`/`skipped`/`skipped` |
| 其他 | `unknown` |

### 4.4 GitLab Issue state → `domain.Issue.Status`（对齐 `gongfengStateToStatus`）

| GitLab | Issue.Status | CloseSource |
|---|---|---|
| `opened` | `todo` | — |
| `closed` | `closed` | `external` |
| 其他 | `backlog` | — |

`Issue.Source = "gitlab"`，`ExternalRef{Source:"gitlab", ExternalID:<numeric id>, ExternalIID:<iid>, ExternalURL:<web_url>, ExternalState:<raw state>, ExternalType:"issue"}`。Issue 内部 ID 沿用现有约定：`gitlab-<repositoryID>-<iid>`（对齐 `gh-<repoID>-<number>`，`github.go:218`）。

## 5. 分页契约

- GitLab REST v4 用 `page`/`per_page` + `X-Next-Page`/`X-Total` header；adapter 必须读 header 判定 `HasMore`，不能只看返回条数（末页恰好满页时会误判）。
- `per_page` 上限 100；`IssueQuery.Page` 从 1 开始；超过 100 页返回 error（对齐 `github.go:242` 的保护）。
- Project path 需 `url.PathEscape`（GitLab 接受 `group%2Fsub%2Frepo` 作为 `:id`）。

## 6. 凭据解析顺序

```text
GitLab API 调用（项目级操作）：
  1. 请求发起用户（responsible user / issue assignee）的 user_provider_tokens
     匹配 (provider_kind='gitlab', instance_key=ref.InstanceKey)
  2. 项目 Owner 的同键 token
  3. 无 → ErrAuthentication（"未配置 GitLab 凭据"），不回退明文 git_token
```

- `instance_key` 匹配前先规范化（lowercase host、去默认端口、去尾斜杠）。
- Token 注入方式：`PRIVATE-TOKEN` header（PAT/project token）或 `Authorization: Bearer`（OAuth token，v1 不用但保留）。不用 query param（会进 URL 日志）。
- Git transport（clone/push）凭据不走本契约：SSH 走 `user_git_ssh_keys` + ssh-agent（现有）；HTTPS 走 credential helper（现有 `bindCommandGitSSH`/git credential 流程，需确认是否覆盖 http host——列入 MGIT-007 验证项）。

## 7. Service 集成点（签名不变）

```go
// internal/tracker/service.go 内部改造，公开方法签名全部保留：
func (s *Service) providerForProject(project string) providerResolver
// 返回 (CollaborationProvider, ExternalRepoRef, error)；
// github: → githubProvider；gitlab: → gitlabProvider.ForInstance(instanceKey, tokenResolver)；
// 其他 → gongfeng MCP（现状，不实现 CollaborationProvider，走旧路径）

// 以下现有方法内部改走 provider dispatch，签名不变：
func (s *Service) EnsureAndMergeBranchMR(project, src, tgt, title, desc string) (string, error)
func (s *Service) FindBranchMR(project, src string) (domain.DeliveryMR, bool, error)
func (s *Service) pullGitHubIssues(...)   // → pullProviderIssues(provider, ref, store, repo)
func (s *Service) refreshGitHubDelivery(...) // → refreshProviderDelivery(...)
```

`github:` 前缀分支改为 `providerForProject` 的第一 case，行为逐字等价（回归测试锁定）。

## 8. Webhook 事件契约（P1，v1 路由保留 501）

```go
// POST /api/webhooks/gitlab/:instanceKey
type GitLabWebhookEvent struct {
    EventUUID   string // X-Gitlab-Event-UUID header
    EventType   string // X-Gitlab-Event header: Merge Request Hook / Issue Hook / Pipeline Hook / Push Hook
    InstanceKey string // path param，须与 token 所属实例匹配
    ProjectID   int    // body.project.id（数值）
    ProjectPath string // body.project.path_with_namespace
    ObjectKind  string // "merge_request" | "issue" | "pipeline" | "push"
    ObjectIID   int    // MR/Issue 的 iid；pipeline 用 id
    RawState    string // object_attributes.state / status
    OccurredAt  time.Time
}
```

- 验证：`X-Gitlab-Token` 与该 instance 配置的 webhook secret 恒定时间比较；secret 存 `user_provider_tokens` 同级配置（v1 简化：平台级 env `AGENTFLOW_GITLAB_WEBHOOK_SECRET_<hash(instanceKey)>` 或 instance 配置表——具体存储在 04-acceptance-test-matrix 前定）。
- 幂等键：`(instance_key, event_uuid)`；无 UUID 时 `sha256(instance_key + event_type + project_id + object_iid + raw_state + occurred_at_minute)`。
- 处理：归一化为 `MRStateSnapshot`/issue state 后走与 polling 相同的 `UpdateMRDeliveryState` 路径；不在 handler 内做重活。
- 乱序保护：仅当事件 `OccurredAt`/`UpdatedAt` 新于 `mr_delivery_states.checked_at` 对应状态时才覆盖；merged 是终态不被 opened 覆盖。

## 9. Capability 与降级

```go
// GitLab Provider v1 Capabilities：
ProviderCapabilities{
    Issues: true, ChangeRequests: true, Merge: true,
    Approvals: true,   // approvals API 可用但版本差异大 → 返回 UNKNOWN 兜底
    Pipelines: true,   // P1
    Webhooks: false,   // v1 入站 501
    OAuth: false,
}
```

调用方必须检查 `Capabilities()` 或处理 `ErrCapabilityUnsupported`；不得根据 `Kind()=="gitlab"` 推断能力（Spec §4 要求运行时能力查询）。

## 10. 契约测试清单（MGIT-015 输入）

对 `CollaborationProvider` 同一组测试跑 github fake-server + gitlab fake-server：

1. `FindChangeRequestByBranch`：open/merged/closed/不存在/多候选选最新
2. `CreateChangeRequest`：source/target/draft/title/body 映射
3. `MergeChangeRequest`：sha 乐观锁、merge_method、detailed_merge_status=checking → ErrConflict
4. `GetChangeRequestState`：全 state 映射表 + unknown fail-closed
5. `ListIssues`：分页 X-Next-Page、state 过滤、PR/MR 混入排除（GitLab issues API 不含 MR，无需过滤但测试锁定）
6. `CreateIssue`/`UpdateIssueState`/`AddIssueComment`
7. `ListPipelines`：sha 过滤、status 映射
8. 错误分类：401/403/404/409/500/timeout → 对应 sentinel error
9. 凭据解析：instance_key 规范化匹配、Owner 回退、无 token → ErrAuthentication
