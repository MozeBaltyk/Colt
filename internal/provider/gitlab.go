package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type gitlabAdapter struct{}

type gitlabNamespace struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	FullPath string `json:"full_path"`
}

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

func (gitlabAdapter) resolveNamespace(ctx context.Context, c *client) (gitlabNamespace, error) {
	var ns gitlabNamespace
	if err := c.request(ctx, http.MethodGet, "/api/v4/namespaces/"+url.PathEscape(c.settings.Namespace), nil, &ns); err != nil {
		return ns, fmt.Errorf("resolve namespace %q: %w", c.settings.Namespace, err)
	}
	if ns.ID <= 0 || ns.FullPath != c.settings.Namespace {
		return ns, fmt.Errorf("resolve namespace %q: provider returned a different namespace", c.settings.Namespace)
	}
	return ns, nil
}

func (gitlabAdapter) list(ctx context.Context, c *client) ([]Repository, error) {
	ns, err := (gitlabAdapter{}).resolveNamespace(ctx, c)
	if err != nil {
		return nil, err
	}
	path := "/api/v4/groups/" + strconv.FormatInt(ns.ID, 10) + "/projects"
	if ns.Kind == "user" {
		path = "/api/v4/users/" + strconv.FormatInt(ns.ID, 10) + "/projects"
	}
	return c.listRepositories(ctx, path, "per_page")
}

func (gitlabAdapter) create(ctx context.Context, c *client, project, visibility string) (*Repository, error) {
	ns, err := (gitlabAdapter{}).resolveNamespace(ctx, c)
	if err != nil {
		return nil, err
	}
	return c.create(ctx, "/api/v4/projects", map[string]any{"name": project, "namespace_id": ns.ID, "visibility": visibility})
}

func (gitlabAdapter) revoke(context.Context, *client, RevocationOptions) error {
	return ErrRevocationUnsupported
}

func (gitlabAdapter) release(ctx context.Context, c *client, project, version string) error {
	path := "/api/v4/projects/" + url.PathEscape(c.settings.Namespace+"/"+project) + "/releases"
	return c.request(ctx, http.MethodPost, path, map[string]any{"tag_name": version, "name": version, "description": ""}, nil)
}
