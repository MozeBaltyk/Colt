package provider

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

type giteaAdapter struct{}

func (giteaAdapter) isConflict(status int, _ string) bool {
	return status == http.StatusConflict || status == http.StatusUnprocessableEntity
}

func (giteaAdapter) authorize(req *http.Request, token string) {
	req.Header.Set("Authorization", "token "+token)
}

func (giteaAdapter) authenticate(ctx context.Context, c *client) (string, error) {
	return c.authenticate(ctx, "/api/v1/user")
}

func (giteaAdapter) get(ctx context.Context, c *client, project string) (*Repository, error) {
	return c.get(ctx, "/api/v1/repos/"+url.PathEscape(c.settings.Namespace)+"/"+url.PathEscape(project))
}

func (giteaAdapter) create(ctx context.Context, c *client, project string) (*Repository, error) {
	account, err := c.Authenticate(ctx)
	if err != nil {
		return nil, err
	}
	path := "/api/v1/user/repos"
	if !strings.EqualFold(account, c.settings.Namespace) {
		path = "/api/v1/orgs/" + url.PathEscape(c.settings.Namespace) + "/repos"
	}
	return c.create(ctx, path, map[string]any{"name": project, "private": c.settings.Visibility == "private"})
}
