package domain

import (
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// GitLabProviderKind is the Provider/ExternalRef kind for GitLab
// self-managed instances.
const GitLabProviderKind = "gitlab"

// gitlabProjectPrefix marks a GongfengProjectID that identifies a GitLab
// repository on a specific self-managed instance:
//
//	gitlab:<escaped-instance-key>/<escaped-full-path>
//
// Both segments are url.PathEscape'd, so neither can contain a literal '/'
// or confuse the separator. The instance key is the normalized base URL
// (scheme://host[:port]); the full path is the GitLab project path
// (group/sub/repo).
const gitlabProjectPrefix = "gitlab:"

// NormalizeGitLabInstanceKey canonicalizes a GitLab base URL into the
// instance key used by gitlab: project references and provider tokens:
// lowercase scheme and host, no userinfo, no path, no trailing slash, and
// no default port. It returns "" for anything that is not a plain
// http(s) base URL.
func NormalizeGitLabInstanceKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return ""
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}
	port := u.Port()
	if port != "" {
		if n, err := strconv.Atoi(port); err == nil {
			def := 443
			if scheme == "http" {
				def = 80
			}
			if n == def {
				port = ""
			}
		}
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return scheme + "://" + host
}

// FormatGitLabProjectRef encodes a GitLab repository identity into the
// GongfengProjectID string form. instanceKey must already be normalized
// (see NormalizeGitLabInstanceKey); fullPath is the GitLab project path.
func FormatGitLabProjectRef(instanceKey, fullPath string) string {
	instanceKey = NormalizeGitLabInstanceKey(instanceKey)
	fullPath = strings.Trim(strings.TrimSpace(fullPath), "/")
	if instanceKey == "" || fullPath == "" {
		return ""
	}
	return gitlabProjectPrefix + url.PathEscape(instanceKey) + "/" + url.PathEscape(fullPath)
}

// ParseGitLabProjectRef decodes a gitlab: project reference back into its
// normalized instance key and unescaped full path. ok is false for any
// other identity form.
func ParseGitLabProjectRef(project string) (instanceKey, fullPath string, ok bool) {
	s := strings.TrimSpace(project)
	if !strings.HasPrefix(s, gitlabProjectPrefix) {
		return "", "", false
	}
	rest := s[len(gitlabProjectPrefix):]
	i := strings.Index(rest, "/")
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	key, err := url.PathUnescape(rest[:i])
	if err != nil {
		return "", "", false
	}
	path, err := url.PathUnescape(rest[i+1:])
	if err != nil {
		return "", "", false
	}
	key = NormalizeGitLabInstanceKey(key)
	path = strings.Trim(path, "/")
	if key == "" || path == "" || strings.Contains(path, "..") {
		return "", "", false
	}
	return key, path, true
}

// IsGitLabProjectRef reports whether project carries the gitlab: identity
// prefix, without validating the payload.
func IsGitLabProjectRef(project string) bool {
	return strings.HasPrefix(strings.TrimSpace(project), gitlabProjectPrefix)
}

// GitLabWebURL builds the browser URL of a GitLab project from its
// normalized instance key and full path.
func GitLabWebURL(instanceKey, fullPath string) string {
	key := NormalizeGitLabInstanceKey(instanceKey)
	path := strings.Trim(strings.TrimSpace(fullPath), "/")
	if key == "" || path == "" {
		return ""
	}
	return key + "/" + path
}

// ParseGitLabRepoURL converts a user-supplied GitLab repository address into
// the canonical gitlab: project reference. Accepted forms:
//
//	https://gitlab.example.com/group/sub/repo[.git]
//	http://gitlab.example.com:8080/group/repo
//	git@gitlab.example.com:group/sub/repo.git
//	ssh://git@gitlab.example.com:2222/group/repo.git
//
// The returned instance key is normalized (no userinfo/path/default port).
// Paths with ".." segments or empty groups are rejected.
func ParseGitLabRepoURL(raw string) (instanceKey, fullPath, projectRef string, ok bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "git clone ")
	s = strings.TrimPrefix(s, "gitlab+")
	s = strings.Trim(s, "\"'")
	if s == "" {
		return "", "", "", false
	}
	if strings.HasPrefix(s, gitlabProjectPrefix) {
		key, path, parsed := ParseGitLabProjectRef(s)
		if !parsed {
			return "", "", "", false
		}
		return key, path, FormatGitLabProjectRef(key, path), true
	}
	var instanceURL, pathPart string
	if strings.HasPrefix(s, "git@") {
		// SCP-like SSH syntax: git@host[:port]/path or git@host:path
		rest := s[len("git@"):]
		sep := strings.IndexAny(rest, ":/")
		if sep <= 0 {
			return "", "", "", false
		}
		hostport, p := rest[:sep], rest[sep+1:]
		p = strings.TrimPrefix(p, "/")
		// SCP-like syntax has no port slot; the API instance is https on the
		// same hostname (non-standard web ports need an https:// URL form).
		host := hostport
		if i := strings.Index(host, ":"); i >= 0 {
			host = host[:i]
		}
		instanceURL = "https://" + host
		pathPart = p
	} else {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", "", "", false
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme != "https" && scheme != "http" && scheme != "ssh" && scheme != "git" {
			return "", "", "", false
		}
		// SSH URLs carry the login in userinfo; the instance key must not.
		if u.User != nil {
			if pw, has := u.User.Password(); u.User.Username() != "git" || has || pw != "" {
				return "", "", "", false
			}
		}
		if scheme == "ssh" || scheme == "git" {
			// Git transport scheme says nothing about the API endpoint; the
			// instance key is the web/API base, assumed https on the same host.
			// Non-standard web ports must be registered via an https:// URL.
			host := u.Hostname()
			if host == "" {
				return "", "", "", false
			}
			instanceURL = "https://" + host
		} else {
			instanceURL = scheme + "://" + u.Host
		}
		pathPart = u.EscapedPath()
		if decoded, err := url.PathUnescape(pathPart); err == nil {
			pathPart = decoded
		}
	}
	fullPath = strings.Trim(strings.TrimSuffix(strings.Trim(pathPart, "/"), ".git"), "/")
	fullPath = strings.TrimSuffix(fullPath, ".git")
	// GitLab sub pages all live under "/-/" (/-/tree, /-/merge_requests, ...);
	// everything before the marker is the project path.
	if i := strings.Index(fullPath, "/-/"); i >= 0 {
		fullPath = fullPath[:i]
	}
	fullPath = strings.Trim(fullPath, "/")
	if fullPath == "" {
		return "", "", "", false
	}
	for _, seg := range strings.Split(fullPath, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", "", "", false
		}
	}
	key := NormalizeGitLabInstanceKey(instanceURL)
	if key == "" {
		return "", "", "", false
	}
	return key, fullPath, FormatGitLabProjectRef(key, fullPath), true
}

// RepositoryExternalRef is the structured provider identity stored in
// repositories.external_ref_json. GongfengProjectID remains the
// compatibility carrier; this record is authoritative for new code.
type RepositoryExternalRef struct {
	ProviderKind   string `json:"providerKind"` // "gitlab"
	InstanceKey    string `json:"instanceKey"`
	ExternalID     string `json:"externalId"` // GitLab numeric project id; "" until resolved
	FullPath       string `json:"fullPath"`
	APIBaseURL     string `json:"apiBaseUrl"` // instanceKey + "/api/v4"
	DefaultBranch  string `json:"defaultBranch,omitempty"`
	SSHCloneURL    string `json:"sshCloneUrl,omitempty"`
	HTTPSCloneURL  string `json:"httpsCloneUrl,omitempty"`
}

// ParseRepositoryExternalRef decodes the external_ref_json column value.
func ParseRepositoryExternalRef(raw string) (RepositoryExternalRef, bool) {
	var ref RepositoryExternalRef
	if strings.TrimSpace(raw) == "" {
		return ref, false
	}
	if err := json.Unmarshal([]byte(raw), &ref); err != nil {
		return RepositoryExternalRef{}, false
	}
	if ref.ProviderKind != GitLabProviderKind || NormalizeGitLabInstanceKey(ref.InstanceKey) == "" || strings.Trim(ref.FullPath, "/") == "" {
		return RepositoryExternalRef{}, false
	}
	ref.InstanceKey = NormalizeGitLabInstanceKey(ref.InstanceKey)
	if ref.APIBaseURL == "" {
		ref.APIBaseURL = ref.InstanceKey + "/api/v4"
	}
	return ref, true
}
