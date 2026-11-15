package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/tracker"

	"github.com/gin-gonic/gin"
)

// providerTokens 管理当前登录用户的 Provider API 凭据（GitLab Self-Managed
// 等）。列表只返回描述信息；secret 仅在校验与同步路径内部读取。
func (h *usersHandlers) providerTokens(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "valid user id is required"})
		return
	}
	if user, ok := h.requestUser(c.Request, false); !ok || user.ID != id {
		c.JSON(http.StatusForbidden, gin.H{"error": "只能管理当前登录用户的 Provider 凭据"})
		return
	}
	if h.repo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "存储不可用"})
		return
	}
	switch c.Request.Method {
	case http.MethodPut:
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		var input struct {
			ProviderKind   string     `json:"providerKind"`
			InstanceKey    string     `json:"instanceKey"`
			CredentialType string     `json:"credentialType"`
			Token          string     `json:"token"`
			ScopesSummary  string     `json:"scopesSummary"`
			ExpiresAt      *time.Time `json:"expiresAt"`
		}
		if !decode(c, &input) {
			return
		}
		kind := strings.ToLower(strings.TrimSpace(input.ProviderKind))
		if kind != domain.ProviderTokenGitLab {
			c.JSON(http.StatusBadRequest, gin.H{"error": "providerKind 目前仅支持 gitlab"})
			return
		}
		instance := domain.NormalizeGitLabInstanceKey(input.InstanceKey)
		if instance == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "instanceKey 必须是 GitLab 实例的 http(s) 基础地址，如 https://gitlab.example.com"})
			return
		}
		token := strings.TrimSpace(input.Token)
		if token == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "token 不能为空"})
			return
		}
		saved, err := h.repo.SaveProviderToken(id, kind, instance, input.CredentialType, token, input.ScopesSummary, input.ExpiresAt)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, saved)
	case http.MethodDelete:
		if err := h.repo.DeleteProviderToken(id, c.Param("tokenID")); err != nil {
			respond(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	default:
		tokens, err := h.repo.ProviderTokenDescriptions(id)
		if err != nil {
			respond(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, tokens)
	}
}

type providerTokenValidateResult struct {
	Valid  bool   `json:"valid"`
	Login  string `json:"login,omitempty"`
	Error  string `json:"error,omitempty"`
}

// validateProviderToken 校验一条已保存（或请求体临时提供）的 GitLab 凭据：
// 对 {instance}/api/v4/user 发起 GET，200 视为有效并回写校验状态。
func (h *usersHandlers) validateProviderToken(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, providerTokenValidateResult{Error: "valid user id is required"})
		return
	}
	current, ok := h.requestUser(c.Request, false)
	if !ok || current.ID != id {
		c.JSON(http.StatusForbidden, providerTokenValidateResult{Error: "只能校验当前登录用户的凭据"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var input struct {
		ProviderKind string `json:"providerKind"`
		InstanceKey  string `json:"instanceKey"`
		Token        string `json:"token"`
	}
	if !decode(c, &input) {
		return
	}
	kind := strings.ToLower(strings.TrimSpace(input.ProviderKind))
	instance := domain.NormalizeGitLabInstanceKey(input.InstanceKey)
	if kind != domain.ProviderTokenGitLab || instance == "" {
		c.JSON(http.StatusBadRequest, providerTokenValidateResult{Error: "providerKind=gitlab 且 instanceKey 为有效 http(s) 基础地址"})
		return
	}
	token := strings.TrimSpace(input.Token)
	persisted := false
	if token == "" {
		if secret, hit := h.repo.ProviderTokenSecret(id, kind, instance); hit {
			token, persisted = secret, true
		}
	}
	if token == "" {
		c.JSON(http.StatusOK, providerTokenValidateResult{Error: "未找到已保存的凭据，请先保存 token"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	res := verifyGitLabToken(ctx, instance, token)
	if persisted {
		status := domain.ProviderTokenAuthFailed
		if res.Valid {
			status = domain.ProviderTokenActive
		}
		_ = h.repo.MarkProviderTokenStatus(id, kind, instance, status, time.Now().UTC())
	}
	c.JSON(http.StatusOK, res)
}

// verifyGitLabToken 调用 GitLab REST /user 校验 PAT 有效性。只回传 login，
// 错误信息不携带 token 或响应体。
func verifyGitLabToken(ctx context.Context, instanceKey, token string) providerTokenValidateResult {
	u := strings.TrimRight(instanceKey, "/") + "/api/v4/user"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return providerTokenValidateResult{Error: "invalid verification request"}
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Accept", "application/json")
	client := *tracker.GitLabAPIHTTPClient()
	client.Timeout = 10 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return providerTokenValidateResult{Error: fmt.Sprintf("无法连接 GitLab 实例（%s）", instanceKey)}
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var user struct {
			Username string `json:"username"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&user) != nil || user.Username == "" {
			return providerTokenValidateResult{Error: "GitLab 返回内容无法解析"}
		}
		return providerTokenValidateResult{Valid: true, Login: user.Username}
	case http.StatusUnauthorized, http.StatusForbidden:
		return providerTokenValidateResult{Error: "令牌无效、已过期或权限不足"}
	default:
		return providerTokenValidateResult{Error: fmt.Sprintf("GitLab 返回 HTTP %d", resp.StatusCode)}
	}
}
