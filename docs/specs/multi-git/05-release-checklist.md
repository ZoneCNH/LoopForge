# Multi-Git Integration — Release Checklist（MGIT-018 输入）

- 状态：v1 模板；每次 release 候选填写实测结果
- 前置：`00`~`04` 全部文档 + 实现代码合入

---

## G0 — Scope（范围确认）

- [ ] v1 scope 与 `01-architecture-spec.md` §9 一致：gitlab REST adapter + `user_provider_tokens` + `gitlab:` 身份编码 + polling 状态同步 + 契约测试 + 真实 E2E
- [ ] 非目标确认：无 ProviderInstance 表、无 OAuth、无 webhook 入站实装（路由 501）、无 per-instance CA bundle
- [ ] 支持的 GitLab 版本范围已文档化（建议 ≥16.x Free tier）

## G1 — Architecture（架构）

- [ ] `internal/tracker/provider.go` 契约已合入；github/gitlab 均实现 `CollaborationProvider`
- [ ] `providerForProject` dispatch：github 分支与现状逐字等价（golden test 通过）
- [ ] SOP Engine 零 provider 分支：`git diff` 确认 `internal/engine/` 无 `gitlab`/`provider` 条件逻辑
- [ ] gongfeng/tapd MCP 路径未被契约改造破坏
- [ ] repo 层归属符合 AGENTS.md：`user_provider_tokens`→UsersRepo、`external_ref_json`→CatalogRepo、MR 状态→DeliveryRepo
- [ ] handler 层归属符合 AGENTS.md：`providersHandlers`/`gitlabWebhookHandlers` 独立 struct

## G2 — Data（数据）

- [ ] `migrations/027_user_provider_tokens.sql`、`028_repository_external_ref.sql` 已编写并在测试库执行成功
- [ ] 唯一索引 `(user_id, provider_kind, instance_key)` 生效
- [ ] `external_ref_json` 为 NULL 的存量行读路径回退正常（解析 `GongfengProjectID`）
- [ ] 回滚方案：drop 两表/两列的 SQL 已写入 migration 注释或 runbook
- [ ] MySQL 集成测试（`make test-mysql`）通过

## G3 — Git Transport

- [ ] 真实 GitLab 实例 SSH clone/fetch/push 成功（E3）
- [ ] SSH host key 校验开启（`sshHostKeys` known_hosts 路径复用验证）
- [ ] per-user SSH key 注入沙箱正常（`user_git_ssh_keys` → ssh-agent）
- [ ] HTTPS git transport 验证结果记录（若未覆盖，标注已知限制）

## G4 — Collaboration

- [ ] E1~E6 全部通过：connect/discovery/issue sync/MR create/state polling
- [ ] `detailed_merge_status` 全状态映射有契约测试
- [ ] `merged_at` 使用平台权威值（非 poll 时间）
- [ ] Issue `Source="gitlab"`、`ExternalRef` 字段完整、内部 ID `gitlab-<repo>-<iid>` 无冲突

## G5 — Webhook/CI（v1 为 polling 主路径）

- [ ] `syncMRDeliveryStates` gitlab 分支工作：stale 刷新、终态保护、unknown 不降级
- [ ] polling 幂等：重复 tick 不产生重复写/重复通知
- [ ] Pipeline status 映射契约测试通过（E10）
- [ ] webhook 路由存在且返回 501（未实装时不静默 404）
- [ ] 若启用 webhook：伪造 token 拒绝、重复事件幂等、乱序不回退（T3/T8 验证）

## G6 — Security

- [ ] `03-security-threat-model.md` §5 检查单全部勾选
- [ ] token/私钥在 API 响应、日志、sync log、run artifacts、diagnostics 中出现次数 = 0
- [ ] SSRF 用例全拒（T2）
- [ ] 凭据解析无跨用户回退（T4）
- [ ] merge 前 `detailed_merge_status` 重查（T7）

## G7 — Regression

- [ ] `go test ./...` 全绿（含 `internal/tracker` 既有测试）
- [ ] GitHub 全链路手工回归：OAuth → repo → issue sync → PR → merge → delivery refresh
- [ ] gongfeng/TAPD sync 不回归
- [ ] `make coverage-go` ≥85%
- [ ] web/chatbot 既有测试不受影响（若动了前端）

## G8 — E2E

- [ ] `04-acceptance-test-matrix.md` §5 的 E1~E14 全部通过
- [ ] 真实 GitLab 版本/配置/日期记录在 `evidence/multi-git/gitlab-e2e/<date>/`
- [ ] provider_switch_without_sop_change（E13）通过：同 SOP 跑两个 provider 零改动

## G9 — Documentation

- [ ] `docs/deployment.md` 增加 GitLab Self-Managed 接入章节（instance_key、PAT scope、网络要求、已知限制）
- [ ] `docs/api/` OpenAPI 更新（providers/webhook/token 端点）
- [ ] `README` 特性表更新（GitLab 从「仅 SSH」升级为「SSH + API」）
- [ ] CHANGELOG 条目
- [ ] `AGENTS.md` 历史整改记录追加本次 repo/handler 变更（若有新 Repo/handler）

## G10 — Release

- [ ] 版本号确定（建议 minor：新 provider 能力）
- [ ] migration 编号连续无冲突（当前最大 026，新增 027/028）
- [ ] Evidence Manifest 生成（commit sha、测试结果、review 状态）
- [ ] 所有 Required Gate 状态 = passed；任何 failed → 保持 BLOCKED
- [ ] Release Notes 含：新能力、配置项、已知限制、回滚说明

---

## 决策记录归档

| ADR | 决策 | 状态 |
|---|---|---|
| ADR-1 | REST 为主 + MCP 扩展 | accepted |
| ADR-2 | `user_provider_tokens` 新表 | accepted |
| ADR-3 | `GongfengProjectID` 保留 + `external_ref_json` | accepted |
| ADR-4 | polling 默认 + 可选 webhook | accepted |
| D-v1 | ProviderInstance 表不建（instance_key 逻辑实例） | accepted |
| D-v1 | Webhook 入站 v1 不实装（501） | accepted |
| D-v1 | GitLab OAuth v1 不做 | accepted |
