package httpapi

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strconv"
	"strings"

	"agentflow/internal/domain"

	"github.com/gin-gonic/gin"
)

// gitlabWebhookHandlers 接收 GitLab Self-Managed 的出站 webhook。v1 只处理
// Merge Request Hook：校验 X-Gitlab-Token 共享密钥后触发一次权威状态回读，
// 实际写库仍由 provider API 完成，因此天然幂等、不依赖事件 payload 状态。
type gitlabWebhookHandlers struct {
	deps *deps
}

const gitlabWebhookSecretEnv = "AGENTFLOW_GITLAB_WEBHOOK_SECRET"

// gitlabMRHookPayload 只解码触发刷新所需的字段；GitLab 其余字段忽略。
type gitlabMRHookPayload struct {
	ObjectKind       string `json:"object_kind"`
	ObjectAttributes struct {
		IID    int    `json:"iid"`
		Action string `json:"action"`
		URL    string `json:"url"`
	} `json:"object_attributes"`
	Project struct {
		PathWithNamespace string `json:"path_with_namespace"`
		WebURL            string `json:"web_url"`
	} `json:"project"`
}

// mergeRequest 处理 POST /api/hooks/gitlab。未配置
// AGENTFLOW_GITLAB_WEBHOOK_SECRET 时端点整体不可用（404），避免暴露存在性。
func (h *gitlabWebhookHandlers) mergeRequest(c *gin.Context) {
	secret := strings.TrimSpace(os.Getenv(gitlabWebhookSecretEnv))
	if secret == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	got := c.GetHeader("X-Gitlab-Token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid webhook token"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	var payload gitlabMRHookPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook payload"})
		return
	}
	if payload.ObjectKind != "merge_request" {
		c.JSON(http.StatusOK, gin.H{"status": "ignored", "reason": "unsupported object kind"})
		return
	}
	if payload.ObjectAttributes.IID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing merge request iid"})
		return
	}
	// 以 project.web_url 重建规范化 gitlab: 引用；与仓库注册时的 instance
	// key 归一化规则一致（scheme+host[:port]，无路径）。
	_, _, ref, ok := domain.ParseGitLabRepoURL(payload.Project.WebURL)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"status": "ignored", "reason": "unrecognized project url"})
		return
	}
	known := false
	for _, repo := range h.deps.repo.Repositories() {
		if repo.GongfengProjectID == ref {
			known = true
			break
		}
	}
	if !known {
		c.JSON(http.StatusOK, gin.H{"status": "ignored", "reason": "project not registered"})
		return
	}
	if h.deps.sync == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "tracker service unavailable"})
		return
	}
	if err := h.deps.sync.RefreshGitLabDelivery(h.deps.repo, ref, payload.ObjectAttributes.IID); err != nil {
		// 返回 5xx 让 GitLab 按既定策略重试；状态写入失败不应静默吞掉。
		c.JSON(http.StatusInternalServerError, gin.H{"error": "refresh failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "refreshed", "project": ref, "iid": strconv.Itoa(payload.ObjectAttributes.IID)})
}
