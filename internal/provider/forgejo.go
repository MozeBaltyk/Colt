package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type forgejoAdapter struct{}

func (forgejoAdapter) isConflict(status int, message string) bool {
	return status == http.StatusConflict || status == http.StatusUnprocessableEntity || status == http.StatusBadRequest && strings.Contains(strings.ToLower(message), "taken")
}

func (forgejoAdapter) authorize(req *http.Request, token string) {
	req.Header.Set("Authorization", "token "+token)
}

func (forgejoAdapter) authenticate(ctx context.Context, c *client) (string, error) {
	return c.authenticate(ctx, "/api/v1/user")
}

func (forgejoAdapter) get(ctx context.Context, c *client, project string) (*Repository, error) {
	return c.get(ctx, "/api/v1/repos/"+url.PathEscape(c.settings.Namespace)+"/"+url.PathEscape(project))
}

func (forgejoAdapter) list(ctx context.Context, c *client) ([]Repository, error) {
	var results []repositoryResponse
	if err := c.request(ctx, http.MethodGet, "/api/v1/user/repos", nil, &results); err != nil {
		return nil, fmt.Errorf("list repositories: %w", err)
	}
	repos := make([]Repository, 0, len(results))
	for _, r := range results {
		repo, err := c.repository(r)
		if err != nil {
			return nil, err
		}
		repos = append(repos, *repo)
	}
	return repos, nil
}

func (forgejoAdapter) create(ctx context.Context, c *client, project, visibility string) (*Repository, error) {
	account, err := c.Authenticate(ctx)
	if err != nil {
		return nil, err
	}
	path := "/api/v1/user/repos"
	if !strings.EqualFold(account, c.settings.Namespace) {
		path = "/api/v1/orgs/" + url.PathEscape(c.settings.Namespace) + "/repos"
	}
	return c.create(ctx, path, map[string]any{"name": project, "private": visibility == "private"})
}

func (forgejoAdapter) revoke(context.Context, *client, RevocationOptions) error {
	return ErrRevocationUnsupported
}

func (forgejoAdapter) release(ctx context.Context, c *client, project, version string) error {
	path := "/api/v1/repos/" + url.PathEscape(c.settings.Namespace) + "/" + url.PathEscape(project) + "/releases"
	return c.request(ctx, http.MethodPost, path, map[string]any{"tag_name": version, "title": version, "body": ""}, nil)
}
