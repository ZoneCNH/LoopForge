package domain

import "testing"

func TestNormalizeGitLabInstanceKey(t *testing.T) {
	cases := map[string]string{
		"https://gitlab.example.com":            "https://gitlab.example.com",
		"https://gitlab.example.com/":           "https://gitlab.example.com",
		"HTTPS://GitLab.Example.COM":            "https://gitlab.example.com",
		"https://gitlab.example.com:443":        "https://gitlab.example.com",
		"http://gitlab.internal:8080":           "http://gitlab.internal:8080",
		"http://gitlab.internal:80":             "http://gitlab.internal",
		"https://gitlab.example.com/some/path":  "",
		"https://user:pass@gitlab.example.com":  "",
		"gitlab.example.com":                    "",
		"ftp://gitlab.example.com":              "",
		"":                                      "",
		"https://gitlab.example.com:abc":        "",
	}
	for in, want := range cases {
		if got := NormalizeGitLabInstanceKey(in); got != want {
			t.Errorf("NormalizeGitLabInstanceKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGitLabProjectRefRoundTrip(t *testing.T) {
	cases := [][2]string{
		{"https://gitlab.example.com", "group/repo"},
		{"https://gitlab.example.com", "group/sub/deep/repo"},
		{"http://gitlab.internal:8080", "a/b"},
		{"https://gitlab.example.com", "weird name/路径"},
	}
	for _, c := range cases {
		ref := FormatGitLabProjectRef(c[0], c[1])
		if ref == "" {
			t.Fatalf("FormatGitLabProjectRef(%q, %q) empty", c[0], c[1])
		}
		key, path, ok := ParseGitLabProjectRef(ref)
		if !ok {
			t.Fatalf("ParseGitLabProjectRef(%q) failed", ref)
		}
		if key != NormalizeGitLabInstanceKey(c[0]) {
			t.Errorf("instance key = %q, want %q", key, NormalizeGitLabInstanceKey(c[0]))
		}
		if path != c[1] {
			t.Errorf("path = %q, want %q", path, c[1])
		}
		// Re-encoding the parsed values must be byte-identical.
		if again := FormatGitLabProjectRef(key, path); again != ref {
			t.Errorf("re-encode mismatch: %q != %q", again, ref)
		}
	}
}

func TestParseGitLabProjectRefRejects(t *testing.T) {
	for _, s := range []string{
		"", "github:owner/repo", "gitlab:", "gitlab:abc", "gitlab:/path",
		"gitlab:%zz/x", "gitlab:https:%2F%2Fgitlab.example.com/",
		"gitlab:https:%2F%2Fgitlab.example.com/a/../b",
		"gitlab:not-a-url/x",
	} {
		if _, _, ok := ParseGitLabProjectRef(s); ok {
			t.Errorf("ParseGitLabProjectRef(%q) unexpectedly ok", s)
		}
	}
}

func TestGitLabWebURL(t *testing.T) {
	if got := GitLabWebURL("https://gitlab.example.com/", "/group/repo/"); got != "https://gitlab.example.com/group/repo" {
		t.Fatalf("GitLabWebURL = %q", got)
	}
	if got := GitLabWebURL("not a url", "group/repo"); got != "" {
		t.Fatalf("GitLabWebURL invalid = %q", got)
	}
}

func TestParseGitLabRepoURL(t *testing.T) {
	cases := []struct {
		raw      string
		key      string
		path     string
		refParts bool // projectRef must round-trip through ParseGitLabProjectRef
	}{
		{"https://gitlab.example.com/group/repo", "https://gitlab.example.com", "group/repo", true},
		{"https://gitlab.example.com/group/sub/repo.git", "https://gitlab.example.com", "group/sub/repo", true},
		{"https://gitlab.example.com:8443/g/r/-/tree/main", "https://gitlab.example.com:8443", "g/r", true},
		{"http://gitlab.internal:8080/a/b", "http://gitlab.internal:8080", "a/b", true},
		{"git@gitlab.example.com:group/repo.git", "https://gitlab.example.com", "group/repo", true},
		{"ssh://git@gitlab.example.com:2222/group/repo.git", "https://gitlab.example.com", "group/repo", true},
		{"gitlab+https://gitlab.example.com/group/repo", "https://gitlab.example.com", "group/repo", true},
	}
	for _, c := range cases {
		key, path, ref, ok := ParseGitLabRepoURL(c.raw)
		if !ok {
			t.Fatalf("ParseGitLabRepoURL(%q) failed", c.raw)
		}
		if key != c.key || path != c.path {
			t.Errorf("%q: key=%q path=%q, want %q %q", c.raw, key, path, c.key, c.path)
		}
		if c.refParts {
			k2, p2, ok2 := ParseGitLabProjectRef(ref)
			if !ok2 || k2 != key || p2 != path {
				t.Errorf("%q: ref %q does not round-trip (%q %q %v)", c.raw, ref, k2, p2, ok2)
			}
		}
	}
}

func TestParseGitLabRepoURLRejects(t *testing.T) {
	for _, s := range []string{
		"", "group/repo", "https://gitlab.example.com", "https://gitlab.example.com/",
		"https://gitlab.example.com/../repo", "https://user:pw@gitlab.example.com/g/r",
		"ftp://gitlab.example.com/g/r", "gitlab:not a url",
	} {
		if _, _, _, ok := ParseGitLabRepoURL(s); ok {
			t.Errorf("ParseGitLabRepoURL(%q) unexpectedly ok", s)
		}
	}
}


