# Multi-Git Integration — Acceptance Test Matrix（MGIT-015 输入）

- 状态：v1
- 分层：Unit → Contract（fake-server）→ Integration → Real E2E → Security → Regression → Operational
- 原则：Mock/fake-server 不能替代真实 GitLab Self-Managed E2E（Spec §0.1/§13.2）

---

## 1. 测试环境基线

### 1.1 真实 GitLab Self-Managed（必须）

| 项 | 要求 |
|---|---|
| 版本 | 记录具体版本（建议 ≥16.x）；Free tier 即可，Premium-only API（如 approvals 细粒度）标 `UNKNOWN` |
| 拓扑 | 独立测试实例或容器化（`gitlab/gitlab-ce` docker）；记录部署方式 |
| 数据 | 独立 Group `loopforge-e2e`、Project `demo`、Protected branch `main` |
| 凭据 | 最小 scope PAT（`api` + `write_repository` 按实测调整）；测试用户 SSH key |
| CI | 至少一个 `.gitlab-ci.yml` pipeline（`test` job） |
| Webhook | v1 可选；若启用，指向测试实例 `/api/webhooks/gitlab/<key>` |
| 记录 | `gitlab-version.txt`、Runner 版本、测试日期写入 evidence |

### 1.2 Fake-server（契约测试用）

`internal/tracker/gitlab/testdata/` 或 httptest server，按需返回：project/issue/MR/pipeline JSON、分页 header、错误 status。与 `github_live_test.go`/`github_test.go` 的注入模式对齐（`githubAPIBase` 字段已留测试注入点，gitlabClient 同样设计 `apiBase` 可注入）。

## 2. 单元测试清单

| # | 用例 | 断言 |
|---|---|---|
| U1 | `FormatGitLabProjectRef`/`ParseGitLabProjectRef` round-trip | 含 `:`/`/`/unicode 的 instance_key 与 full_path 无损 |
| U2 | 编码边界 | `gitlab:` 前缀精确匹配；`gitlabx:`/`gitlab:` 空路径拒绝 |
| U3 | instance_key 规范化 | 大小写 host、默认端口、尾斜杠归一 |
| U4 | state 映射表 | contracts §4.1-4.4 全部行 + 未知值 → unknown/UNKNOWN |
| U5 | `detailed_merge_status` 映射 | `checking`/`unchecked` → unknown；`not_approved` → blocked |
| U6 | sentinel error 分类 | 401→ErrAuthentication、403→ErrPermissionDenied、404→ErrNotFound、409→ErrConflict、500/timeout→ErrUnavailable |
| U7 | 错误消息脱敏 | 构造含 token 的响应，断言 error.Error() 不含 token |
| U8 | 凭据解析顺序 | 请求用户 token 优先；缺失回退 Owner；均无 → ErrAuthentication |
| U9 | `IsSafeGongfengProjectID` 扩展 | `gitlab:` 编码值通过，非法值仍拒绝 |
| U10 | webhook token 比较 | 错误长度/内容 token 恒定时间拒绝 |

## 3. 契约测试清单（同一 suite 跑 github + gitlab fake）

按 `02-provider-contracts.md` §10 的 9 组用例实现；GitHub 侧用现有行为做 baseline，GitLab 侧全部为新增。关键断言：

- `FindChangeRequestByBranch`：多 MR 候选时选「最新非 closed」，与 `pickBranchMR` 语义一致
- `MergeChangeRequest`：`sha` 不匹配 → ErrConflict；`detailed_merge_status=checking` → ErrConflict 不发 merge 请求
- `GetChangeRequestState`：`merged` 返回权威 `merged_at`（非 poll 时间，对齐 `gongfengMRMergedAt` 语义）
- `ListIssues`：读 `X-Next-Page` 判定分页，不用条数猜测

## 4. 集成测试（repo + service 层）

| # | 用例 | 断言 |
|---|---|---|
| I1 | `providerForProject` dispatch | `github:`/`gitlab:`/gongfeng/未知前缀各走正确分支；github 行为与现状逐字等价（golden test） |
| I2 | `pullGitHubIssues` → `pullProviderIssues` 重构 | GitHub issue 导入结果不变（`gh-<repo>-<n>` ID、SourceContentHash、CloseSource） |
| I3 | `syncMRDeliveryStates` gitlab 分支 | fake gitlab client 刷新 `mr_delivery_states`；stale/终态/unknown 行为正确 |
| I4 | `user_provider_tokens` CRUD | 唯一约束 `(user_id, kind, instance_key)`；API 输出无 secret |
| I5 | `Repository.ExternalRef`（payload JSON） | 写入/读取/回退解析 `GongfengProjectID`；catalog 重建不信任客户端值 |
| I6 | webhook 端点（v1 已实装） | 正确 token + 合法事件 → 200 且状态更新；错误 token → 401；重复投递幂等（权威回读，无需 event_uuid 去重） |

## 5. 真实 E2E 清单（G8 必过）

| # | 场景 | 步骤 | 通过标准 |
|---|---|---|---|
| E1 | Provider Connect/Validate | 配置 instance_key + PAT → validate | 返回 capabilities + 项目可见性，无 token 泄漏 |
| E2 | Repository Discovery | 列出测试 group 项目 | 数字 id/path/default_branch 正确 |
| E3 | SSH Clone/Fetch/Push | 沙箱内 clone `git@gitlab:group/demo.git` → 分支 → push | 成功；host key 校验开启 |
| E4 | Issue Sync | GitLab 建 issue → sync 拉取 | `gitlab-<repo>-<iid>` 落库，ExternalRef 正确 |
| E5 | Issue Write-back | LoopForge issue 关联 GitLab issue → 状态回写 | GitLab 侧 state 更新 |
| E6 | MR Create | run work branch → `FindChangeRequestByBranch`/`CreateChangeRequest` | MR 创建，source/target 正确 |
| E7 | MR State Polling | merge MR → 等 ≤5min+ε | `mr_delivery_states` → merged，`merged_at` 权威值 |
| E8 | Merge Gate | `detailed_merge_status` 非 mergeable（如未 approve/pipeline failed）→ merge 调用 | 被拒，状态 blocked/unknown，不产生 merge |
| E9 | Approval | required approval 未满足 → merge | 被拒（Free tier 若无 approval 规则，记录 capability 差异） |
| E10 | Pipeline | push 触发 pipeline → `ListPipelines` | status 映射正确；failed pipeline → delivery 不标 merged-ready |
| E11 | Credential Expired | revoke PAT → sync/validate | `AUTH_FAILED` 状态，不无限重试，日志无 token |
| E12 | Network Failure | GitLab 宕机/断网 → 操作 | `ErrUnavailable`，重试退避，恢复后自愈 |
| E13 | provider_switch_without_sop_change | 同一 SOP 先跑 github 仓库再跑 gitlab 仓库 | SOP 定义零改动，仅 repository binding 不同 |
| E14 | Evidence | 全程 artifact/sync log/run 记录 | 无 token/私钥；MR iid、sha、状态可追溯 |

## 6. 回归测试（G7）

- `go test ./internal/tracker/ ./internal/repo/ ./internal/httpapi/ -count=1` 全绿
- GitHub OAuth device flow → git_token → issue sync → PR merge → delivery refresh 全链路手工回归一次
- gongfeng/TAPD sync 回归（若环境不可得，至少 `service_test.go`/`catalog_test.go` 单测全绿）

## 7. Operational 场景

| 场景 | 预期 |
|---|---|
| GitLab 版本低于支持范围 | validate 返回 capability 差异 + 文档化最低版本 |
| 项目 path rename | `external_id` 数字 id 继续工作；FullPath 更新 |
| Instance key host 变更（域名迁移） | 视为新 instance；旧 token 不自动迁移，需重新配置 |
| Token 轮换 | 更新 `user_provider_tokens` → `last_validated_at` 刷新，旧 token 立即失效 |
| Webhook 不可达（私网） | polling 兜底，状态延迟 ≤ poll interval |

## 8. Evidence 记录要求

每个 E2E 场景输出：

```text
evidence/multi-git/gitlab-e2e/
  <date>/
    gitlab-version.txt        # 版本 + edition + 关键配置
    env.md                    # 实例 URL（脱敏）、group/project、token scope（不记 token）
    <scenario>.log            # 操作日志
    <scenario>.json           # API 响应/状态快照（脱敏后）
    screenshots/              # UI 验证（如有）
```

## 9. 覆盖率门槛

- `internal/tracker/gitlab/`：≥85%（对齐 `make coverage-go`）
- `internal/tracker/provider.go` + dispatch：≥90%（分支密度高）
- `internal/repo` 新增文件：≥85%
- 新增 httpapi handler：关键路径（validate/webhook/token CRUD）必须有测试
