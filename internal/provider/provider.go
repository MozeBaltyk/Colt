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

type client struct {
	settings config.Provider
	token    string
	http     *http.Client
}

func New(settings config.Provider, token string, hc *http.Client) (Client, error) {
	if token == "" {
		return nil, errors.New("credential is missing or empty")
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
	return &client{settings: settings, token: token, http: &copy}, nil
}

func (c *client) Authenticate(ctx context.Context) (string, error) {
	path := "/user"
	if c.settings.Type == "gitlab" {
		path = "/api/v4/user"
	}
	var result struct {
		Login    string `json:"login"`
		Username string `json:"username"`
	}
	if err := c.request(ctx, http.MethodGet, path, nil, &result); err != nil {
		return "", fmt.Errorf("authentication failed: %w", err)
	}
	if result.Login != "" {
		return result.Login, nil
	}
	if result.Username != "" {
		return result.Username, nil
	}
	return "", errors.New("authentication failed: provider response omitted account name")
}

func (c *client) Get(ctx context.Context, project string) (*Repository, error) {
	var path string
	if c.settings.Type == "github" {
		path = "/repos/" + url.PathEscape(c.settings.Namespace) + "/" + url.PathEscape(project)
	} else {
		path = "/api/v4/projects/" + url.PathEscape(c.settings.Namespace+"/"+project)
	}
	var result struct {
		CloneURL     string `json:"clone_url"`
		SSHURL       string `json:"ssh_url"`
		HTTPURL      string `json:"http_url_to_repo"`
		SSHURLToRepo string `json:"ssh_url_to_repo"`
	}
	err := c.request(ctx, http.MethodGet, path, nil, &result)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("check repository: %w", err)
	}
	return c.repository(result.CloneURL, result.HTTPURL, result.SSHURL, result.SSHURLToRepo)
}

func (c *client) Create(ctx context.Context, project string) (*Repository, error) {
	if c.settings.Type == "github" {
		account, err := c.Authenticate(ctx)
		if err != nil {
			return nil, err
		}
		path := "/user/repos"
		if !strings.EqualFold(account, c.settings.Namespace) {
			path = "/orgs/" + url.PathEscape(c.settings.Namespace) + "/repos"
		}
		body := map[string]any{"name": project, "private": c.settings.Visibility == "private"}
		var result struct {
			CloneURL string `json:"clone_url"`
			SSHURL   string `json:"ssh_url"`
		}
		if err := c.request(ctx, http.MethodPost, path, body, &result); err != nil {
			return nil, fmt.Errorf("create repository: %w", err)
		}
		return c.repository(result.CloneURL, "", result.SSHURL, "")
	}
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
	body := map[string]any{"name": project, "namespace_id": namespace.ID, "visibility": c.settings.Visibility}
	var result struct {
		HTTPURL      string `json:"http_url_to_repo"`
		SSHURLToRepo string `json:"ssh_url_to_repo"`
	}
	if err := c.request(ctx, http.MethodPost, "/api/v4/projects", body, &result); err != nil {
		return nil, fmt.Errorf("create repository: %w", err)
	}
	return c.repository("", result.HTTPURL, "", result.SSHURLToRepo)
}

func (c *client) repository(first, second, sshFirst, sshSecond string) (*Repository, error) {
	clone := first
	if clone == "" {
		clone = second
	}
	if clone != "" {
		u, err := url.Parse(clone)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("provider returned an unsafe clone URL; expected clean HTTPS")
		}
		if !strings.EqualFold(u.Host, c.settings.Host) {
			return nil, errors.New("provider returned a clone URL for a different host")
		}
	}
	ssh := sshFirst
	if ssh == "" {
		ssh = sshSecond
	}
	return &Repository{CloneURL: clone, SSHURL: ssh}, nil
}

func (c *client) request(ctx context.Context, method, path string, body any, result any) error {
	base, err := url.Parse(strings.TrimRight(c.settings.BaseURL, "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return errors.New("provider base URL must use HTTPS")
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
	if c.settings.Type == "github" {
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	} else {
		req.Header.Set("PRIVATE-TOKEN", c.token)
	}
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
		if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusUnprocessableEntity || resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(string(message)), "taken") {
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
