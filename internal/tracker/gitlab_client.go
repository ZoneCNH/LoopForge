package tracker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// gitlabClient is a minimal GitLab REST v4 client. One instance serves one
// normalized instance key; the project path travels per call.
type gitlabClient struct {
	apiBase   string // https://host/api/v4 (already normalized)
	token     string
	http      *http.Client
	nextPage  string // X-Next-Page of the most recent listPages call
}

func newGitLabClient(instanceKey, token string) *gitlabClient {
	return &gitlabClient{
		apiBase: strings.TrimRight(instanceKey, "/") + "/api/v4",
		token:   token,
		http: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// request performs one API call. projectPath is the URL-escaped
// group/sub/repo path or the numeric project id; suffix carries the
// resource path (e.g. "/issues"). Errors are classified into the provider
// sentinels and never echo the token or the response body.
func (g *gitlabClient) request(method, projectPath, suffix string, query url.Values, input, output any) error {
	if query == nil {
		query = url.Values{}
	}
	u := g.apiBase + "/projects/" + url.PathEscape(strings.Trim(projectPath, "/")) + suffix
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	r, err := http.NewRequest(method, u, body)
	if err != nil {
		return err
	}
	r.Header.Set("Accept", "application/json")
	if input != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if g.token != "" {
		r.Header.Set("PRIVATE-TOKEN", g.token)
	}
	res, err := g.http.Do(r)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return ErrProviderAuth
	case res.StatusCode == http.StatusForbidden:
		return ErrProviderDenied
	case res.StatusCode == http.StatusNotFound:
		return ErrProviderNotFound
	case res.StatusCode == http.StatusConflict:
		return ErrProviderConflict
	case res.StatusCode == http.StatusTooManyRequests:
		return ErrProviderRateLimited
	case res.StatusCode < 200 || res.StatusCode >= 300:
		return fmt.Errorf("%w: HTTP %d", ErrProviderUnavailable, res.StatusCode)
	}
	if output == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 4<<20))
		return err
	}
	return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(output)
}

// listPages iterates pages of a list endpoint. visit is called once per
// page with the decoded JSON array; returning false stops iteration.
func (g *gitlabClient) listPages(projectPath, suffix string, query url.Values, visit func(raw json.RawMessage) (bool, error)) error {
	page := 1
	for page <= 100 {
		q := url.Values{}
		for k, vs := range query {
			q[k] = append([]string(nil), vs...)
		}
		q.Set("page", strconv.Itoa(page))
		if q.Get("per_page") == "" {
			q.Set("per_page", "100")
		}
		var raw json.RawMessage
		// request() decodes into output; for raw page bodies we decode into
		// RawMessage through a wrapper request below.
		if err := g.requestRaw(projectPath, suffix, q, &raw); err != nil {
			return err
		}
		cont, err := visit(raw)
		if err != nil || !cont {
			return err
		}
		// GitLab signals continuation via X-Next-Page; requestRaw stashes it
		// on the client for this call only when non-empty.
		if g.nextPage == "" {
			return nil
		}
		next, err := strconv.Atoi(g.nextPage)
		if err != nil || next <= page {
			return nil
		}
		page = next
	}
	return fmt.Errorf("gitlab list exceeded 100 pages")
}

// nextPage carries the X-Next-Page header of the most recent listPages call.
// It is per-call state on a client that is not shared concurrently.
func (g *gitlabClient) requestRaw(projectPath, suffix string, query url.Values, output *json.RawMessage) error {
	u := g.apiBase + "/projects/" + url.PathEscape(strings.Trim(projectPath, "/")) + suffix
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	r, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	r.Header.Set("Accept", "application/json")
	if g.token != "" {
		r.Header.Set("PRIVATE-TOKEN", g.token)
	}
	res, err := g.http.Do(r)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return ErrProviderAuth
	case res.StatusCode == http.StatusForbidden:
		return ErrProviderDenied
	case res.StatusCode == http.StatusNotFound:
		return ErrProviderNotFound
	case res.StatusCode == http.StatusTooManyRequests:
		return ErrProviderRateLimited
	case res.StatusCode < 200 || res.StatusCode >= 300:
		return fmt.Errorf("%w: HTTP %d", ErrProviderUnavailable, res.StatusCode)
	}
	g.nextPage = res.Header.Get("X-Next-Page")
	body, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	*output = json.RawMessage(body)
	return nil
}
