# Multi-Git Integration — Security Threat Model（MGIT-016 输入）

- 状态：v1 草案
- 前置：`00-current-state-audit.md` §3/§4、`02-provider-contracts.md` §6/§8
- 范围：GitLab Provider 集成新增攻击面；GitHub 既有面只做回归确认

---

## 1. 资产清单

| 资产 | 现状存储 | v1 变化 |
|---|---|---|
| GitLab PAT/project/group token | — | `user_provider_tokens`（MySQL 明文 + 应用层脱敏） |
| GitLab webhook secret | — | env 或实例配置（v1 简化） |
| SSH 私钥 | `user_git_ssh_keys` + ssh-agent 注入 | 不变 |
| GitHub token | `users.git_token` 明文 | 不变（遗留风险，见 R-legacy） |
| 仓库代码/CI 产物 | 沙箱 workspace | 不变 |

## 2. 威胁矩阵

| ID | 威胁 | 场景 | 控制 | 验证 |
|---|---|---|---|---|
| T1 | Token 泄漏 | API 响应、日志、错误消息、run artifacts、`external_ref_json` | ① token 只存 `secret_ref` 行引用，API 只出 `token_hint`（末 4 位）；② 错误消息禁含 token/body 原文（provider-contracts §3）；③ `PublicRepository`/`PublicUser` 脱敏路径回归测试；④ diagnostics secrets 列表加入新 token 来源 | 契约测试 + grep 审计 + 手工 API 响应检查 |
| T2 | SSRF | Provider Instance `api_base_url` 指向内网/元数据服务 | ① instance_key 仅管理员可配（v1 无用户自助 URL 输入）；② `ValidateRemoteURL` 同级校验：禁 userinfo、scheme 仅 http/https；③ 明确 `allow_private_network` 策略位（v1 env 开关 `AGENTFLOW_GITLAB_ALLOW_PRIVATE=1`，默认对私网 IP 拒绝）；④ 重定向不跟随（对齐 `githubClient.CheckRedirect=ErrUseLastResponse`） | 单测：169.254.169.254、127.0.0.1、userinfo URL 全部拒绝 |
| T3 | Webhook 伪造/重放 | 伪造 `X-Gitlab-Token` 触发 MR 状态篡改 → 影响 delivery 判定 | ① 恒定时间比较 `subtle.ConstantTimeCompare`；② 幂等键 `(instance_key, event_uuid)` 去重；③ 事件只写 `mr_delivery_states`，不触发 agent 执行；④ merged 终态不被 opened 覆盖（乱序保护） | 单测伪造 token/重复事件/乱序事件 |
| T4 | 凭据越权 | 用户 A 用用户 B 的 token 操作 B 名下仓库 | ① token 解析顺序固定：发起用户 → 项目 Owner → 拒绝；② `user_provider_tokens` 查询强制 `user_id` 维度，无跨用户回退；③ 项目成员角色校验沿用现有 `ProjectMember`（maintainer/member/viewer） | 集成测试：非成员用户 token 不可访问项目仓库 |
| T5 | 凭据注入沙箱 | Agent/仓库脚本读取 token | ① API token **不进** `RunCredentials`/`CommandSpec`（git transport 用 SSH key 或 https credential helper，与 API token 分离）；② token 不写 workspace 文件、不进 `run-context.json`；③ `repositories.go:170` 的 payload 清洗模式对新字段同样生效 | 代码审查 + run 快照序列化测试断言无 token 字段 |
| T6 | TLS 降级 | 私有 GitLab 自签证书诱导关验证 | ① 默认 `InsecureSkipVerify=false`；② 私有 CA 走系统 trust store / `SSL_CERT_FILE`，v1 不支持 per-instance CA bundle（记录为已知限制 R-2）；③ `http://` instance_key 需 `allow_private_network` 显式开启 | 单测：默认配置拒绝自签 |
| T7 | MR 状态误判 | `detailed_merge_status` 语义差异导致越权 merge | ① merge 前重查 detailed_merge_status，`checking/unchecked` → `ErrConflict`；② `not_approved`/`policies_denied`/`ci_must_pass` → `blocked`，禁止 merge；③ merged 是终态 | fake-server 返回各状态断言 merge 被拒 |
| T8 | 事件洪泛 | webhook/轮询被刷导致 DB/外部 API 压力 | ① webhook body ≤1MB、速率沿用现有 HTTP 限流；② polling 沿用 `MRDeliveryStatesNeedingRefresh(staleBefore)` 增量 + `beginSync` 防重入；③ GitLab API 429 → `ErrRateLimited` + 退避 | 负载/重试测试 |
| T9 | 身份混淆 | `gitlab:` 编码与 `github:`/gongfeng ID 冲突 | ① `ParseGitLabProjectRef` 只在 `gitlab:` 前缀命中；② instance_key 含 `:` `/` 必须转义（contracts §1.1）；③ `external_ref_json.ExternalID` 数字 id 优先于 path | 编解码 round-trip 单测 + 前缀冲突用例 |
| T10 | 存量回归 | github:/gongfeng/tapd 路径被 dispatch 改动破坏 | ① `providerForProject` github 分支逐字等价；② 现有 tracker 测试全绿为合入门槛 | `go test ./internal/tracker/` + CI |

## 3. 已知限制（v1 接受）

- **R-legacy**：`users.git_token` 明文存储是既有风险，本 spec 不扩大也不收敛；GitLab token 不落该字段。
- **R-1**：per-instance CA bundle、mTLS client cert 不支持（GitLab 双向 TLS 实例无法接入，列入 v1.1）。
- **R-2**：webhook secret v1 存 env/实例配置，不支持 per-instance 独立 secret 轮换 UI。
- **R-3**：GitLab OAuth 不做（ADR 范围外）；token 过期靠 `last_validated_at` + `AUTH_FAILED` 状态提示人工更新。
- **R-4**：Agent 沙箱内执行 `git push` 走 SSH/HTTPS credential，与 API token 完全隔离——仓库脚本即使被攻破也拿不到 GitLab API token（T5 的正面论证）。

## 4. 审计日志要求

以下操作必须写审计事件（复用现有 sync log / 通知通道，不新建表）：

- token 创建/更新/吊销（记 user_id、instance_key、credential_type、token_hint，**不记 token**）
- provider validate 调用结果（success/failure 分类，不记响应 body）
- webhook 验证失败（记 instance_key + source IP，不记 token/header）
- merge 操作发起与结果（MR iid、merge_status、结果状态）

## 5. 上线前安全检查单（Release Gate G6 输入）

- [ ] `gitlab:` 编码/解码 fuzz round-trip 无 panic、无凭据泄漏
- [ ] 全部 sentinel error 路径消息 grep 不含 `token`/`PRIVATE-TOKEN`/`Bearer`
- [ ] SSRF 用例：`http://169.254.169.254/`、`https://127.0.0.1/`、`https://user:pass@gitlab.x/` 均被拒
- [ ] webhook：错误 token → 401 且响应不区分「实例不存在」与「token 错误」（防枚举）
- [ ] token 出现在：API 响应、sync log、run artifacts、diagnostics 导出 → 全部为零
- [ ] `detailed_merge_status=checking` 时 merge 调用 → ErrConflict，无实际 merge
- [ ] GitHub/gongfeng/tapd 既有测试套件 100% 通过
