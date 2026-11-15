# Multi-Git Integration — Current-State Audit（MGIT-001 初稿）

- 审计对象：LoopForge `main` @ `29fce4c`（fork `ZoneCNH/LoopForge`，领先上游 2 个提交）
- 审计日期：2026-11-15
- 审计范围：`internal/tracker`、`internal/gitwork`、`internal/repo`、`internal/httpapi`、`internal/engine`、`internal/domain`、`migrations/`、`docs/`
- 方法：源码直读 + 符号级核验；每条结论标注 `CONFIRMED | NOT_IMPLEMENTED | PARTIAL | UNKNOWN | BLOCKED` 并附文件证据

---

## 1. Provider 身份与仓库建模

| # | 结论 | 状态 | 证据 |
|---|------|------|------|
| 1.1 | 外部仓库身份寄存在 `Repository.GongfengProjectID`（JSON `gongfengProjectId`），GitHub 仓库用 `github:owner/repo` 前缀编码 | CONFIRMED | `internal/domain/project.go:74`、`internal/domain/public_repository.go:17,40` |
| 1.2 | `Repository.Provider` 字段存在，但唯一常量是 `RepositoryProviderSSH = "ssh"`；语义是「仅 Git transport、无 hosting API adapter」，不是 provider registry | CONFIRMED | `internal/domain/project.go:66-67` |
| 1.3 | `Provider` 字段的实际取值分布、谁在写入、`!= "ssh"` 的分支含义，未见集中定义——属于隐式约定 | PARTIAL | `internal/engine/integrate.go:161` 以 `rc.Provider != domain.RepositoryProviderSSH` 判断是否走 MR 交付 |
| 1.4 | `github:` 前缀判断散落多处：`tracker/merge_mr.go:29,140`、`tracker/mr_delivery_sync.go:119,157,220`、`domain/delivery_mr.go:31`、`domain/mr.go:36`、`domain/public_repository.go:17,40` | CONFIRMED（耦合点清单） | 见左 |
| 1.5 | 没有 `ProviderInstance`、`RepositoryConnection`、`Credential` 独立实体；仓库↔外部平台的绑定就是 `GongfengProjectID` 字符串 + 项目归属 | NOT_IMPLEMENTED | `internal/domain/project.go:69-87` |

**审计含义**：Spec §6 的 `ExternalRepositoryID{ProviderInstanceID, ProviderKind, ExternalID, FullPath}` 需要新模型；现有 `GongfengProjectID` 是「单字符串多义」字段（工蜂项目 ID、`github:owner/repo`、TAPD 项目、甚至 GitLab 将来都可能塞进去），是最大改造点之一。

## 2. Tracker / 协作 API 层

| # | 结论 | 状态 | 证据 |
|---|------|------|------|
| 2.1 | `internal/tracker.Service` 是 2089 行单一 struct，管理 gongfeng/TAPD 两条 **MCP 通道**（`s.gongfeng`、`s.tapd`，类型 `*mcpclient.Client`） | CONFIRMED | `internal/tracker/service.go:20-41`、`internal/tracker/mcp_client.go:12` |
| 2.2 | Tracker 客户端按 `kind` 字符串 + `projectID` 分发：`trackerClientForProject(kind, projectID)`，kind ∈ {gongfeng, tapd}；项目级凭据走「项目 Owner/责任人的 MCP server 资产 + Authorization header」 | CONFIRMED | `internal/tracker/service.go:127-178`、`internal/tracker/catalog.go:79-129` |
| 2.3 | MCP 绑定来源三级回退：catalog HTTP MCP asset → `AGENTFLOW_MCP_TOKEN` env → `~/.codebuddy/mcp.json` OAuth token | CONFIRMED | `internal/tracker/catalog.go:20-74` |
| 2.4 | **GitHub 不走 MCP**：`tracker/github.go` 是独立的 REST client（`api.github.com`，Bearer token，30s 超时，`X-GitHub-Api-Version: 2022-11-28`），能力面为 issues 拉取、PR 查找/创建/合并、PR 状态刷新 | CONFIRMED | `internal/tracker/github.go:18-258` |
| 2.5 | GitHub token 来源：项目 Owner 的 `user.GitToken`（按 `repo.GongfengProjectID == "github:..."` 反查 repository→project→owner） | CONFIRMED | `internal/tracker/github.go:29-42` |
| 2.6 | 工蜂（Gongfeng）MR 操作走 MCP tool：`search_merge_request`、`create_merge_request`、`merge_merge_request`、`get_commit_refs` 等，返回 payload 形状不稳定需 `visitGongfengMRPayload` 多层解码 | CONFIRMED | `internal/tracker/merge_mr.go:65-115`、`merge_mr.go:199-303` |
| 2.7 | TAPD 同步是另一套 MCP + 业务逻辑（stories/bugs/workspace/iteration），与 gongfeng 并列在同一 `Service` | CONFIRMED | `internal/tracker/service.go:294+`、`tapd_*.go` |
| 2.8 | **不存在 Provider 接口/Registry**：Spec §3.2 的 `RepositoryProvider`/`IssueProvider`/`ChangeRequestProvider` 契约在代码中无对应物；能力分发靠 `strings.HasPrefix(project, "github:")` 与 `kind` 字符串 | CONFIRMED（缺口） | 全仓检索无 `ProviderRegistry`/`ChangeRequest` 类型 |
| 2.9 | Agent 侧另有 GitHub 工具面：`internal/httpapi/agent_tool_github*.go`（PR 创建/patch/评论等），属 agent tool 通道，与 tracker.Service 平行 | CONFIRMED | `internal/httpapi/agent_tool_github_pr.go` 等 |

**审计含义**：这是 Spec §11.1 问题 6 的答案——Issue/MR **部分**由 tracker 管理，但 GitHub 是 REST 直连、工蜂/TAPD 是 MCP，三条异构路径。GitLab 接入有两条候选路线（见 §7 决策点）。

## 3. GitHub 连接与凭据

| # | 结论 | 状态 | 证据 |
|---|------|------|------|
| 3.1 | GitHub OAuth 用 **Device Flow**（内置 OAuth App，scope=`repo`），token 存 `users.git_token` | CONFIRMED | `internal/httpapi/github_auth.go:195-305` |
| 3.2 | PAT 手工录入也写同一字段：`UpdateUserCredentials(GitToken)` | CONFIRMED | `internal/repo/users.go:272-277`、`internal/repo/users.go:338` |
| 3.3 | `users.git_token` 是 MySQL `text` 列**明文存储**；无 Secret Manager / secret_ref 抽象 | CONFIRMED | `internal/repo/models.go:37`、`internal/repo/users.go:26` |
| 3.4 | API 输出侧有脱敏：`PublicUser` 清空 `GitToken`；diagnostics 对 secrets 做过滤 | CONFIRMED | `internal/domain/user.go:85`、`internal/httpapi/assistant_diagnostics.go:413` |
| 3.5 | Token 连通性验证：`GET/POST /api/users/:id/git-token/verify`，要求 scope `read_repository`+`write_repository` | CONFIRMED | `internal/httpapi/git_token_verify.go:27,41`、`server.go:277-278` |
| 3.6 | GitHub MCP header 绑定：`runtime.BindGitHubMCPHeaders(bundle, user.GitToken, true)`——GitHub token 同时供 MCP 通道使用 | CONFIRMED | `internal/httpapi/assistant_mcp.go:382` |
| 3.7 | 另有 `users.gongfeng_refresh_token`（工蜂 OAuth refresh token）与 per-user SSH key 表 `user_git_ssh_keys` | CONFIRMED | `internal/domain/user.go:99`、`internal/repo/users.go:45` |

**审计含义**：Spec §5.2 的 `secret_ref`/Secret Manager 模型当前不存在，凭据就是用户表字段。GitLab PAT 可以最小成本复用 `git_token` 语义，但多实例 GitLab 需要「一个用户对多个 Provider Instance 各有 token」——现有 `git_token` 单字段不够，需新表。

## 4. Git Transport

| # | 结论 | 状态 | 证据 |
|---|------|------|------|
| 4.1 | Git 操作全部走 Git CLI（`internal/gitwork`），无 libgit2/go-git | CONFIRMED | `internal/gitwork/{git,remote,ssh,workbranch,integrate}.go` |
| 4.2 | SSH transport 已支持任意 host:port：`SSHRemote` 解析 `ssh://`/SCP 语法，`SSHHostScope` 接受 `gitlab.com` 或 `host:2222` | CONFIRMED | `internal/gitwork/ssh_remote.go:10-60` |
| 4.3 | SSH 私钥 per-user 存储（`user_git_ssh_keys`），经 in-process ssh-agent 注入沙箱；host key 校验走 known_hosts 收集（`sshHostKeys`） | CONFIRMED | `internal/gitwork/ssh.go:56,143,292-305`、`internal/httpapi/git_ssh_keys.go` |
| 4.4 | `ValidateRemoteURL`/`IsSSHReference`/`StripURLCredentials` 已处理 URL 凭据剥离与 scheme 校验 | CONFIRMED | `internal/gitwork/remote.go:20`、`internal/domain/project.go:95` |
| 4.5 | Clone URL 解析：`ParseRepositoryURL` 识别 `github:owner/repo`、工蜂 legacy ID、HTTPS/SSH | CONFIRMED | `internal/domain/project.go:159-237`、`internal/domain/clone_url.go` |
| 4.6 | GitLab HTTPS clone/push：文档声明支持，代码层面 `ValidateRemoteURL` 未见 host 白名单——理论上任意 HTTPS git host 可用，但无 GitLab 专属路径 | PARTIAL | `internal/gitwork/remote.go`、`docs/deployment.md` |

**审计含义**：Spec §3.2-A 的 `GitTransport` 接口**不需要新建**——现有 gitwork 已是事实上的 transport 层，GitLab 的 Clone/Push/SSH 今天就能用。缺口只在「Provider Instance 级网络/凭据配置」（私有 CA、自定义 known_hosts、出站策略），这些目前是用户级/环境级而非实例级。

## 5. Webhook / 事件 / CI

| # | 结论 | 状态 | 证据 |
|---|------|------|------|
| 5.1 | **没有任何入站 Webhook 端点**：`internal/httpapi/server.go` 路由表无 webhook；全仓 `webhook` 仅出现于测试文件 | NOT_IMPLEMENTED | `server.go:240-570` 路由全量核对 |
| 5.2 | MR/PR 状态同步是**轮询对账模型**：`StartMRStateSync` 每 5 分钟跑 `syncMRDeliveryStates`——artifact 发现 → promote MR 发现 → work-commit 落地检测 → stale 刷新 | CONFIRMED | `internal/tracker/mr_delivery_sync.go:12-148` |
| 5.3 | 对账含幂等保护：`syncing` bool 防重入、`deliveryReconcileOnce` 一次性 reconcile、`landedOnDefault` sync.Map 缓存、`MRDeliveryStatesNeedingRefresh(staleBefore)` 增量 | CONFIRMED | `mr_delivery_sync.go:83-148`、`service.go:271-291` |
| 5.4 | CI/Pipeline：`Repository` 有 `PipelineProjectID`/`PipelineDefID`/`PipelineURLPattern` 字段（偏 TAPD/工蜂 pipeline 链接语义），无 GitHub Checks API、无 GitLab Pipeline API 抽象 | PARTIAL | `internal/domain/project.go:81-83` |
| 5.5 | 状态归一化已存在雏形：`normalizeMRState`、`gongfengStateToStatus`、issue `Status`/`CloseSource`；但无统一 `DomainEvent`/事件溯源 | PARTIAL | `internal/tracker/mr_state_lookup.go`、`service.go` |

**审计含义**：Spec §7 的 Webhook 框架是**全新建**（无历史包袱），但也意味着 GitLab MVP 可以纯 polling 复用现有 `mr_delivery_sync` 模式，把 GitLab 分支加进 `syncMRDeliveryStates` 的 provider 分发即可——这是工作量最小的路径。Webhook 列 P1 增强完全合理。

## 6. Delivery / SOP 集成面

| # | 结论 | 状态 | 证据 |
|---|------|------|------|
| 6.1 | Engine 通过函数注入接 tracker：`e.MergeIntoBranch = syncSvc.EnsureAndMergeBranchMR`、`e.FindBranchMR = syncSvc.FindBranchMR`（`server.go:180-181`） | CONFIRMED | `internal/engine/engine.go:49-58` |
| 6.2 | `EnsureAndMergeBranchMR` 内部 `github:` 前缀分发到 `githubMergeBranch`（REST），否则走 gongfeng MCP | CONFIRMED | `internal/tracker/merge_mr.go:28-31` |
| 6.3 | `integrate.go:161`：仅当 `Provider != "ssh"` 且 `GongfengProjectID != ""` 才走 MR 合入基线；SSH-only 仓库回退直推 | CONFIRMED | `internal/engine/integrate.go:161-163` |
| 6.4 | DeliveryRepo 持久化 `mr_delivery_states`（project+iid+state+target+merged_at），Repo 层归 `DeliveryRepo` | CONFIRMED | `internal/repo/delivery*.go`、AGENTS.md repo 表 |
| 6.5 | MR 链接发现依赖 **run artifacts 里的文本链接**（`DiscoverMRDeliveryStatesFromArtifacts`）+ `FindBranchMR` 权威查询兜底 | CONFIRMED | `mr_delivery_sync.go:101`、`engine/lifecycle.go:169-171` |
| 6.6 | SOP Engine 本身无 provider 概念：节点只看到 repository context（clone URL、分支、work branch）；provider 差异全部下沉在 tracker.Service | CONFIRMED | `internal/domain/project.go:116-139`、`internal/engine/integrate.go` |

**审计含义**：Spec §8「SOP 零 provider 分支」的现状**已经满足**——瓶颈不在 engine，在 tracker.Service 的分发方式（字符串前缀）和凭据模型。GitLab 适配的理想落点是把 `EnsureAndMergeBranchMR`/`FindBranchMR`/`pullIssues`/`refreshDelivery` 这组方法后面的实现换成 provider dispatch，而不是动 engine。

## 7. 关键决策点（审计新发现，Spec 未覆盖）

### D1：GitLab API 走 REST 直连还是 MCP？

现有三条 tracker 路径并存：

| 路径 | 用于 | 传输 |
|---|---|---|
| `githubClient` | GitHub issues/PR/merge/delivery | REST 直连 |
| `s.gongfeng` MCPClient | 工蜂 MR/Issue | MCP tool |
| `s.tapd` MCPClient | TAPD stories/bugs | MCP tool |

- **REST 直连**：与 `githubClient` 同构，GitLab REST v4 文档成熟，Spec §5.3 的 API 映射表可直接落地。需要新写 ~500-800 行 adapter。
- **MCP 通道**：复用 `bindProjectUserTrackerMCP` 的「项目 Owner 凭据」模型，若团队已有 GitLab MCP server 可零开发接入；但 MCP tool 契约由外部 server 定义，状态语义不可控，与 Spec「归一化状态」目标冲突。

**建议**：首期 REST 直连（确定性、可测试、契约可控），MCP 作为用户自定义扩展通道保留。

### D2：凭据模型——`git_token` 单字段不够

GitLab Self-Managed 是**多实例**场景（`gitlab.a.com`、`gitlab.b.com` 各自 token），而 `users.git_token` 是单值且语义偏 GitHub。需要：

- 新表 `provider_credentials`（user_id × provider_instance_id × credential_type × secret）
- 或退一步：`user_provider_tokens`（user_id × provider_kind × instance_base_url → token）

Spec §6.3 的 `Credential` 实体方向正确，但「Secret Manager」在自托管单实例部署里过重——MySQL 加密列 + 应用层脱敏即可满足 v1，secret_ref 抽象留接口。

### D3：`GongfengProjectID` 字段的演进

该字段名已成历史包袱（承载 github:/gitlab:/tapd:/gongfeng 多种身份）。选项：

- **A（推荐）**：保留字段做兼容层，新增 `external_ref_json` 或 provider 维度表，读路径逐步迁移——符合 AGENTS.md「合并视图契约」风格。
- **B**：直接改名 `external_project_id`——破坏面太大（API、前端、run 快照、migrations 全部涉及），不建议 v1 做。

### D4：Webhook vs Polling

现有系统无 webhook 基础设施，MR 状态全靠 5min 轮询。GitLab MVP 走 polling（复用 `syncMRDeliveryStates` 扩展 provider 分支）可把 MGIT-011 从 P0 降为 P1；Webhook 留到多实例/实时性需求出现时再做。Spec 已允许此降级（§7.4 polling fallback），建议在 `01-architecture-spec.md` 中显式记录该决策。

## 8. Spec 假设核验总表

| Spec §2.2 审计问题 | 核验结果 |
|---|---|
| `internal/tracker` 是否已有稳定接口 | PARTIAL——有 Service 但无 Provider 接口；GitHub=REST、gongfeng/tapd=MCP 三条异构路径 |
| Repository/Credential/Delivery 模型是否允许 Provider Instance 多实例化 | NOT_IMPLEMENTED——无 ProviderInstance/Credential 实体；`GongfengProjectID` 单字符串 |
| GitHub OAuth 与 PAT 是否同一 Credential 生命周期 | CONFIRMED——同写 `users.git_token`，OAuth device flow 与手工 PAT 无差别 |
| PR/MR 状态是否硬编码 GitHub 语义 | PARTIAL——`github:` 前缀散落 6+ 处；gongfeng 状态经 `normalizeMRState` 归一，GitHub 直接映射 `opened/merged/closed` |
| Webhook 是否有统一入口/签名/幂等/重试 | NOT_IMPLEMENTED——完全没有 webhook 代码 |
| CI 状态是否已从 GitHub 抽象 | NOT_IMPLEMENTED——仅 `PipelineProjectID/DefID/URLPattern` 链接字段，无 checks/pipeline API |
| Clone URL 是否允许 HTTPS/SSH/自定义域名 | CONFIRMED——`ValidateRemoteURL`+`IsSSHReference`+SSHRemote 解析已支持任意 host |
| 项目权限模型能否限制 Provider Instance/Repository 访问 | PARTIAL——项目成员角色存在（maintainer/member/viewer），但无 provider 维度授权 |
| 数据库迁移策略是否支持可回滚增量 | CONFIRMED——`migrations/` 手动 SQL + gorm AutoMigrate 增量 DDL |

## 9. 对 Spec 的修正建议

1. **§3.2 接口设计需修正**：鉴于 gongfeng/tapd 走 MCP 而 GitHub 走 REST，「Provider Adapter」不应假设统一传输层。建议契约定义在「领域对象」层（Issue/ChangeRequest/Pipeline），传输层允许 REST 或 MCP 双实现。
2. **§11.3 目录建议需对齐 AGENTS.md**：`internal/delivery/` 不存在且 delivery 归 `DeliveryRepo`；`internal/provider/` 可行但建议先落 `internal/tracker/gitlab/` + `internal/tracker/provider.go`（契约），registry 后置。
3. **MGIT-011（Webhook）降级**：v1 纯 polling，复用 `mr_delivery_sync` 模式；webhook 列入 v1.1。
4. **MGIT-005（认证）需扩展**：除 PAT 外要覆盖「同一用户多 GitLab 实例多 token」的数据模型，这超出 Spec §6.3 单 Credential 实体的描述。
5. **新增 MGIT-019（建议）**：GitLab Pipeline/CI 状态映射——Spec 有 MGIT-012 但低估了 `PipelineProjectID` 等遗留字段的语义冲突。

## 10. 审计结论

- Git Transport 层：**已就绪**，GitLab SSH/HTTPS clone/push 今天即可用，无需改造。
- Collaboration 层：**三缺**——无 Provider 接口、无 GitLab adapter、无 webhook/CI 抽象。
- 凭据层：**半缺**——用户级 token 模型存在但单实例语义，需多实例扩展。
- 最小可行路径：在 `tracker.Service` 内做 provider dispatch（`github:` 前缀 → 显式 kind），新增 `gitlabClient` REST adapter 复用 `githubClient` 模式，MR delivery sync 增加 gitlab 分支走 polling。预计核心代码 ~1000-1500 行 + 测试。

---

*本文档为 MGIT-001 初稿，所有 `CONFIRMED` 结论均附源码路径；`PARTIAL`/`UNKNOWN` 项需在 MGIT-002 ADR 前补充验证。*
