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

type Repository struct {
	CloneURL string
	SSHURL   string
}

type Client interface {
	Authenticate(context.Context) (string, error)
	Get(context.Context, string) (*Repository, error)
	Create(context.Context, string) (*Repository, error)
}

type adapter interface {
	authorize(*http.Request, string)
	isConflict(int, string) bool
	authenticate(context.Context, *client) (string, error)
	get(context.Context, *client, string) (*Repository, error)
	create(context.Context, *client, string) (*Repository, error)
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

func (c *client) Create(ctx context.Context, project string) (*Repository, error) {
	return c.adapter.create(ctx, c, project)
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
	CloneURL     string `json:"clone_url"`
	SSHURL       string `json:"ssh_url"`
	HTTPURL      string `json:"http_url_to_repo"`
	SSHURLToRepo string `json:"ssh_url_to_repo"`
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
	}
	ssh := result.SSHURL
	if ssh == "" {
		ssh = result.SSHURLToRepo
	}
	return &Repository{CloneURL: clone, SSHURL: ssh}, nil
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
