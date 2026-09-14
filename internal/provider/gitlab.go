package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type gitlabAdapter struct{}

func (gitlabAdapter) isConflict(status int, message string) bool {
	return status == http.StatusConflict || status == http.StatusUnprocessableEntity || status == http.StatusBadRequest && strings.Contains(strings.ToLower(message), "taken")
}

func (gitlabAdapter) authorize(req *http.Request, token string) {
	req.Header.Set("PRIVATE-TOKEN", token)
}

func (gitlabAdapter) authenticate(ctx context.Context, c *client) (string, error) {
	return c.authenticate(ctx, "/api/v4/user")
}

func (gitlabAdapter) get(ctx context.Context, c *client, project string) (*Repository, error) {
	return c.get(ctx, "/api/v4/projects/"+url.PathEscape(c.settings.Namespace+"/"+project))
}

func (gitlabAdapter) create(ctx context.Context, c *client, project, visibility string) (*Repository, error) {
	var namespace struct {
		ID       int64  `json:"id"`
		FullPath string `json:"full_path"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/v4/namespaces/"+url.PathEscape(c.settings.Namespace), nil, &namespace); err != nil {
		return nil, fmt.Errorf("resolve namespace %q: %w", c.settings.Namespace, err)
	}
	if namespace.ID <= 0 || namespace.FullPath != c.settings.Namespace {
		return nil, fmt.Errorf("resolve namespace %q: provider returned a different namespace", c.settings.Namespace)
	}
	return c.create(ctx, "/api/v4/projects", map[string]any{"name": project, "namespace_id": namespace.ID, "visibility": visibility})
}

func (gitlabAdapter) revoke(context.Context, *client, RevocationOptions) error {
	return ErrRevocationUnsupported
}
