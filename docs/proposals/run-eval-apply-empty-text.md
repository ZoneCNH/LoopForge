# run_eval_apply：append_task_prompt 建议全部静默失败，且接口返回 200

> 状态：待维护者确认（未修复）
> 发现日期：2026-11-15
> 影响组件：`internal/runeval`、`internal/httpapi`、`POST /api/runs/{id}/eval/apply`

## 摘要

对一条 `status=succeeded` 的 SOP run 完成质量评分后，调用 `runs_eval_apply` 应用
`append_task_prompt` 类改进建议，接口返回 HTTP 200 与完整 eval 对象，但 **3 条建议全部未应用**：
每条仍为 `status: "pending"`，并新增 `applyError: "append text is empty"`。
目标 Agent 的 `version` 与 `prompt` 均未变化。

评分结论本身有效（84 / 阈值 70 / `passed=true`），但改进建议无法落地，且失败是静默的，
调用方极易把「200 + 完整对象」误判为「优化已生效」。

## 复现步骤

1. 对一条 `succeeded` 的 SOP run 触发评分：`POST /api/runs/{id}/eval`（MCP `runs_eval_start`），等待 `status=succeeded`。
2. `GET /api/runs/{id}/eval` 可见 `result.recommendations[]` 含 `action: "append_task_prompt"` 的条目。
3. 调用 `POST /api/runs/{id}/eval/apply`，body `{"evalId":"<evalId>","recommendationIds":["rec-1","rec-2","rec-3"]}`。
4. 观察响应：HTTP 200 + 完整对象；但每条 rec 仍 `status:"pending"`，新增 `applyError:"append text is empty"`。
5. `GET /api/agents/{targetId}` 复核：`version` 未升位、`prompt` 与评分前逐字一致 → 零改写。

## 根因（已定位到源码）

**1. 评分 prompt 未声明 `text` 字段**
`internal/runeval/prompt.go` · `BuildPrompt`：给模型的 JSON 输出示例里 `recommendations` 是空数组
`[]`，规则只列出 action 取值（`update_node` / `upsert_gate` / `remove_gate` /
`append_task_prompt`），**既没有要求也没有示范 `text` 字段**。而
`domain.RunEvalRecommendation` 是有 `Text string \`json:"text,omitempty"\`` 的。
结果模型只产出 `id / title / target / targetId / action / nodeId`，且 `title` 为空串，
等价的改进内容只写在自由文本 `summary` 里，未结构化到 recommendation。

**2. 解析阶段未做非空校验**
同文件 `ParseModelResult` → `normalizeRecommendation` 只规范化
`ID / Target / Action / Status`（`Status` 默认 `pending`）。append 类建议 `text` 为空时仍被
原样存库并标为可应用的 `pending`，缺陷被推迟到 apply 阶段才暴露。

**3. apply 端取不到正文**
`internal/runeval/apply.go` · `recommendationText` 只接受 `rec.Text`，或
`rec.Set["text"]` / `rec.Set["instructionsDelta"]`；都取不到就返回空串，
`appendAgentText` 随即 `return fmt.Errorf("append text is empty")`。
（`ApplyRecommendation` 中 `agent.append_instructions` 与 `agent.append_task_prompt`
都走 `appendAgentText`。）

**4. handler 吞错后仍返回 200**
`internal/httpapi/run_eval.go` apply handler（约 265–296 行）：
`applyOneRecommendation` 返回 error 时只做
`rec.Result.Recommendations[i].ApplyError = err.Error()` + `changed = true` + `continue`；
循环结束后 `PutRunEval` 落库，最后统一 `c.JSON(http.StatusOK, rec)`。
**全部失败也返回 200**，且响应里 `status` 仍是 `pending`，没有 `appliedCount` /
`failedCount` 之类汇总，客户端无法从状态码或顶层字段判断是否成功。

## 建议修复

1. `BuildPrompt` 的 schema 示例补上 `recommendations` 的字段形状，并增加规则：
   > `append_task_prompt` / `append_instructions` 必须提供非空 `text`；
   > `text` 是要追加到角色 prompt 的正文，不要只给标题。
2. `ParseModelResult` / `normalizeRecommendation` 对 append 类 action 做 `text` 非空校验：
   缺正文时丢弃该建议，或标为「仅查看、不可应用」，不要存成 `pending` 让 apply 时才报错。
3. apply handler 汇总结果：存在失败项时返回 `207` / `409`，或至少在响应中给出
   `appliedCount` / `failedCount`；避免「200 + 全部 pending」的成功语义。

## 验收判据

1. 重跑评分后 `result.recommendations[].text` 非空。
2. apply 后每条 `status = "applied"`、`appliedAt` 有值、`applyError` 为空。
3. `GET /api/agents/{targetId}` 显示 `version` 升位、`updatedAt` 刷新、prompt 末尾确有追加文本。
4. 含失败项时接口不再返回纯 200 成功语义。

## 实测证据（脱敏）

- 评分：84 / 阈值 70，`passed=true`，`model=default`，6 维度加权。
- 3 条建议 `rec-1` / `rec-2` / `rec-3`：`action=append_task_prompt`、`title=""`、
  无 `text`、无 `set`，目标为 test / deliver / review 三个角色。
- apply 响应：200；3 条均 `pending` + `applyError:"append text is empty"`。
- 实时 `agents_get` 复核三个角色：`version:1` 未变、prompt 逐字一致 → 确认零改写。
- 运行环境：内部 LoopForge 实例（实例地址、run/eval/agent id 已省略）。

## 备注

本次未对 Agent 定义做任何手工改写。修复前可暂不重跑评分；如需临时规避，
可按评分 `result.summary` 的兜底建议，由 SOP 维护者在对应角色 prompt 末尾人工追加等价约束。
