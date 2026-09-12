package provider

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

type githubAdapter struct{}

func (githubAdapter) isConflict(status int, _ string) bool {
	return status == http.StatusConflict || status == http.StatusUnprocessableEntity
}

func (githubAdapter) authorize(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

func (githubAdapter) authenticate(ctx context.Context, c *client) (string, error) {
	return c.authenticate(ctx, "/user")
}

func (githubAdapter) get(ctx context.Context, c *client, project string) (*Repository, error) {
	return c.get(ctx, "/repos/"+url.PathEscape(c.settings.Namespace)+"/"+url.PathEscape(project))
}

func (githubAdapter) create(ctx context.Context, c *client, project string) (*Repository, error) {
	account, err := c.Authenticate(ctx)
	if err != nil {
		return nil, err
	}
	path := "/user/repos"
	if !strings.EqualFold(account, c.settings.Namespace) {
		path = "/orgs/" + url.PathEscape(c.settings.Namespace) + "/repos"
	}
	return c.create(ctx, path, map[string]any{"name": project, "private": c.settings.Visibility == "private"})
}
