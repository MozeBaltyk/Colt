package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/MozeBaltyk/Colt/internal/config"
)

var ErrNotFound = errors.New("repository not found")
var ErrConflict = errors.New("repository already exists")
var ErrRevocationUnsupported = errors.New("provider-side revocation unsupported")

type RevocationOptions struct {
	ClientID     string
	ClientSecret string
}

type Repository struct {
	Name          string
	Namespace     string
	CloneURL      string
	SSHURL        string
	DefaultBranch string
	Visibility    string
}

type Client interface {
	Authenticate(context.Context) (string, error)
	Get(context.Context, string) (*Repository, error)
	List(context.Context) ([]Repository, error)
	Create(context.Context, string, ...string) (*Repository, error)
	Release(context.Context, string, string) error
	Revoke(context.Context, RevocationOptions) error
}

type adapter interface {
	authorize(*http.Request, string)
	isConflict(int, string) bool
	authenticate(context.Context, *client) (string, error)
	get(context.Context, *client, string) (*Repository, error)
	list(context.Context, *client) ([]Repository, error)
	create(context.Context, *client, string, string) (*Repository, error)
	release(context.Context, *client, string, string) error
	revoke(context.Context, *client, RevocationOptions) error
}

type client struct {
	settings config.Provider
	token    string
	http     *http.Client
	adapter  adapter
	authMu   sync.Mutex
	account  string
}

func New(settings config.Provider, token string, hc *http.Client) (Client, error) {
	if token == "" {
		return nil, errors.New("credential is missing or empty")
	}
	var providerAdapter adapter
	switch settings.Type {
	case "github":
		providerAdapter = githubAdapter{}
	case "gitlab":
		providerAdapter = gitlabAdapter{}
	case "gitea":
		providerAdapter = giteaAdapter{}
	case "forgejo":
		providerAdapter = forgejoAdapter{}
	default:
		return nil, fmt.Errorf("unsupported provider type %q", settings.Type)
	}
	if hc == nil {
		hc = &http.Client{}
	}
	copy := *hc
	if copy.Timeout == 0 {
		copy.Timeout = 15 * time.Second
	}
	previous := copy.CheckRedirect
	copy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && (req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Host, via[0].URL.Host)) {
			return errors.New("refusing provider redirect to another host")
		}
		if previous != nil {
			return previous(req, via)
		}
		if len(via) >= 10 {
			return errors.New("too many provider redirects")
		}
		return nil
	}
	return &client{settings: settings, token: token, http: &copy, adapter: providerAdapter}, nil
}

func (c *client) Authenticate(ctx context.Context) (string, error) {
	return c.adapter.authenticate(ctx, c)
}

func (c *client) Get(ctx context.Context, project string) (*Repository, error) {
	return c.adapter.get(ctx, c, project)
}

func (c *client) List(ctx context.Context) ([]Repository, error) {
	return c.adapter.list(ctx, c)
}

func (c *client) Release(ctx context.Context, version, tag string) error {
	return c.adapter.release(ctx, c, version, tag)
}

func (c *client) Create(ctx context.Context, project string, override ...string) (*Repository, error) {
	visibility := c.settings.Visibility
	if len(override) > 0 {
		visibility = override[0]
	}
	return c.adapter.create(ctx, c, project, visibility)
}

func (c *client) Revoke(ctx context.Context, opts RevocationOptions) error {
	return c.adapter.revoke(ctx, c, opts)
}

type accountResponse struct {
	Login    string `json:"login"`
	Username string `json:"username"`
}

func (c *client) authenticate(ctx context.Context, path string) (string, error) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.account != "" {
		return c.account, nil
	}
	var result accountResponse
	if err := c.request(ctx, http.MethodGet, path, nil, &result); err != nil {
		return "", fmt.Errorf("authentication failed: %w", err)
	}
	if result.Login != "" {
		c.account = result.Login
		return c.account, nil
	}
	if result.Username != "" {
		c.account = result.Username
		return c.account, nil
	}
	return "", errors.New("authentication failed: provider response omitted account name")
}

type repositoryResponse struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	FullName      string `json:"full_name"`
	PathNamespace string `json:"path_with_namespace"`
	Owner         struct {
		Login    string `json:"login"`
		Username string `json:"username"`
	} `json:"owner"`
	CloneURL      string `json:"clone_url"`
	SSHURL        string `json:"ssh_url"`
	HTTPURL       string `json:"http_url_to_repo"`
	SSHURLToRepo  string `json:"ssh_url_to_repo"`
	DefaultBranch string `json:"default_branch"`
	Visibility    string `json:"visibility"`
	Private       *bool  `json:"private"`
	Internal      *bool  `json:"internal"`
}

func (c *client) get(ctx context.Context, path string) (*Repository, error) {
	var result repositoryResponse
	err := c.request(ctx, http.MethodGet, path, nil, &result)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("check repository: %w", err)
	}
	return c.repository(result)
}

func (c *client) listRepositories(ctx context.Context, path, pageSizeParameter string) ([]Repository, error) {
	const pageSize = 100
	repositories := make([]Repository, 0)
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	for page := 1; ; page++ {
		var results []repositoryResponse
		target := fmt.Sprintf("%s%s%s=%d&page=%d", path, separator, pageSizeParameter, pageSize, page)
		if err := c.request(ctx, http.MethodGet, target, nil, &results); err != nil {
			return nil, fmt.Errorf("list repositories: %w", err)
		}
		if len(repositories)+len(results) > config.MaxProviderRepositories {
			return nil, fmt.Errorf("provider returned more than %d repositories", config.MaxProviderRepositories)
		}
		for _, result := range results {
			repository, err := c.repository(result)
			if err != nil {
				return nil, err
			}
			repositories = append(repositories, *repository)
		}
		if len(results) < pageSize {
			return repositories, nil
		}
	}
}

func (c *client) create(ctx context.Context, path string, body any) (*Repository, error) {
	var result repositoryResponse
	if err := c.request(ctx, http.MethodPost, path, body, &result); err != nil {
		return nil, fmt.Errorf("create repository: %w", err)
	}
	return c.repository(result)
}

func (c *client) repository(result repositoryResponse) (*Repository, error) {
	clone := result.CloneURL
	if clone == "" {
		clone = result.HTTPURL
	}
	if clone != "" {
		u, err := url.Parse(clone)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("provider returned an unsafe clone URL; expected clean HTTPS")
		}
		if !strings.EqualFold(u.Host, c.settings.Host) {
			return nil, errors.New("provider returned a clone URL for a different host")
		}
		path := strings.TrimPrefix(u.Path, "/")
		if !strings.HasSuffix(path, ".git") {
			return nil, errors.New("provider returned an unsafe clone URL; expected repository .git path")
		}
		path = strings.TrimSuffix(path, ".git")
		parts := strings.Split(path, "/")
		if len(parts) < 2 || !config.ValidProjectName(parts[len(parts)-1]) {
			return nil, fmt.Errorf("provider returned an invalid repository identity for %q", clone)
		}
		name, namespace := parts[len(parts)-1], strings.Join(parts[:len(parts)-1], "/")
		for _, part := range parts[:len(parts)-1] {
			if !config.ValidProjectName(part) {
				return nil, errors.New("provider returned an invalid repository namespace")
			}
		}
		metadataPath := result.FullName
		if result.PathNamespace != "" {
			metadataPath = result.PathNamespace
		}
		if metadataPath != "" && metadataPath != namespace+"/"+name {
			return nil, errors.New("provider returned inconsistent repository identity")
		}
		slug := result.Name
		if c.settings.Type == "gitlab" && result.Path != "" {
			slug = result.Path
		}
		if slug != "" && slug != name {
			return nil, errors.New("provider returned inconsistent repository name")
		}
		owner := result.Owner.Login
		if owner == "" {
			owner = result.Owner.Username
		}
		if owner != "" && c.settings.Type != "gitlab" && !strings.EqualFold(owner, namespace) {
			return nil, errors.New("provider returned inconsistent repository owner")
		}
		result.Name, result.FullName = name, namespace
	}
	ssh := result.SSHURL
	if ssh == "" {
		ssh = result.SSHURLToRepo
	}
	if result.DefaultBranch != "" && !config.ValidBranch(result.DefaultBranch) {
		return nil, errors.New("provider returned an invalid default branch")
	}
	if result.Visibility == "" && result.Internal != nil && *result.Internal {
		result.Visibility = "internal"
	} else if result.Visibility == "" && result.Private != nil {
		if *result.Private {
			result.Visibility = "private"
		} else {
			result.Visibility = "public"
		}
	}
	if result.Visibility != "" && result.Visibility != "private" && result.Visibility != "internal" && result.Visibility != "public" {
		return nil, errors.New("provider returned an invalid repository visibility")
	}
	return &Repository{Name: result.Name, Namespace: result.FullName, CloneURL: clone, SSHURL: ssh, DefaultBranch: result.DefaultBranch, Visibility: result.Visibility}, nil
}

func (c *client) request(ctx context.Context, method, path string, body any, result any) error {
	base, err := url.Parse(strings.TrimRight(c.settings.BaseURL, "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawPath != "" || base.RawQuery != "" || base.Fragment != "" {
		return errors.New("provider base URL must be a clean HTTPS URL")
	}
	target := strings.TrimRight(base.String(), "/") + path
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return fmt.Errorf("build provider request: %w", err)
	}
	c.adapter.authorize(req, c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("provider request failed: %s", redact(err.Error(), c.token))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: HTTP %d", ErrNotFound, resp.StatusCode)
		}
		if c.adapter.isConflict(resp.StatusCode, string(message)) {
			return fmt.Errorf("%w: HTTP %d", ErrConflict, resp.StatusCode)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if result == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result); err != nil {
		return fmt.Errorf("decode provider response: %w", err)
	}
	return nil
}

func redact(value, token string) string {
	if token == "" {
		return value
	}
	return strings.ReplaceAll(value, token, "[REDACTED]")
}
