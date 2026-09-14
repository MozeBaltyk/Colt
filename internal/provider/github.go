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

	"github.com/MozeBaltyk/Colt/internal/credential"
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

func (githubAdapter) list(ctx context.Context, c *client) ([]Repository, error) {
	var results []repositoryResponse
	if err := c.request(ctx, http.MethodGet, "/user/repos", nil, &results); err != nil {
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

func (githubAdapter) create(ctx context.Context, c *client, project, visibility string) (*Repository, error) {
	account, err := c.Authenticate(ctx)
	if err != nil {
		return nil, err
	}
	path := "/user/repos"
	if !strings.EqualFold(account, c.settings.Namespace) {
		path = "/orgs/" + url.PathEscape(c.settings.Namespace) + "/repos"
	}
	return c.create(ctx, path, map[string]any{"name": project, "private": visibility == "private"})
}

func (githubAdapter) revoke(ctx context.Context, c *client, opts RevocationOptions) error {
	if opts.ClientID == "" || opts.ClientSecret == "" || strings.ContainsAny(opts.ClientID+opts.ClientSecret, "\r\n") {
		return ErrRevocationUnsupported
	}
	data, err := json.Marshal(map[string]string{"access_token": c.token})
	if err != nil {
		return errors.New("encode GitHub revocation request")
	}
	target := strings.TrimRight(c.settings.BaseURL, "/") + "/applications/" + url.PathEscape(opts.ClientID) + "/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, bytes.NewReader(data))
	if err != nil {
		return errors.New("build GitHub revocation request")
	}
	req.SetBasicAuth(opts.ClientID, opts.ClientSecret)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("GitHub revocation request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnprocessableEntity {
		return errors.New("GitHub did not accept the app credentials for this token")
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("GitHub revocation failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (githubAdapter) release(ctx context.Context, c *client, project, version string) error {
	path := "/repos/" + url.PathEscape(c.settings.Namespace) + "/" + url.PathEscape(project) + "/releases"
	return c.request(ctx, http.MethodPost, path, map[string]any{"tag_name": version, "name": version, "body": ""}, nil)
}

const (
	githubDeviceBaseURL = "https://github.com"
	githubClientID      = "Ov23liERaF30k06vMN8L"
)

var githubDeviceWait func(context.Context, time.Duration) error

// AuthorizeGitHubDevice performs GitHub's OAuth device flow without launching
// a browser or exposing the resulting reusable token.
func AuthorizeGitHubDevice(ctx context.Context, hc *http.Client, out io.Writer) (string, error) {
	return authorizeGitHubDevice(ctx, hc, githubDeviceBaseURL, out, githubDeviceWait)
}

func authorizeGitHubDevice(ctx context.Context, hc *http.Client, baseURL string, out io.Writer, wait func(context.Context, time.Duration) error) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme != "https" || base.Host != "github.com" || base.Path != "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("GitHub device authorization endpoint must be https://github.com")
	}
	if hc == nil {
		hc = &http.Client{}
	}
	httpClient := *hc
	if httpClient.Timeout == 0 {
		httpClient.Timeout = 15 * time.Second
	}
	previous := httpClient.CheckRedirect
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Host, "github.com") {
			return errors.New("refusing GitHub device authorization redirect to another host")
		}
		if previous != nil {
			return previous(req, via)
		}
		if len(via) >= 10 {
			return errors.New("too many GitHub device authorization redirects")
		}
		return nil
	}
	form := url.Values{"client_id": {githubClientID}, "scope": {"repo"}}
	var code struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := postGitHubDevice(ctx, &httpClient, baseURL, "/login/device/code", form, &code); err != nil {
		return "", err
	}
	if code.DeviceCode == "" || code.UserCode == "" || strings.ContainsAny(code.DeviceCode+code.UserCode, "\r\n") || code.ExpiresIn <= 0 || code.ExpiresIn > 3600 || code.Interval <= 0 || code.Interval > 60 {
		return "", errors.New("GitHub returned an invalid device authorization response")
	}
	verification, err := url.Parse(code.VerificationURI)
	if err != nil || verification.Scheme != "https" || verification.Host != "github.com" || verification.Path != "/login/device" || verification.User != nil || verification.RawQuery != "" || verification.Fragment != "" {
		return "", errors.New("GitHub returned an unsafe device verification URI")
	}
	if _, err := fmt.Fprintf(out, "Authorize Colt with GitHub.\n\nOpen: %s\nCode: %s\n\nWaiting for authorization...\n", verification.String(), code.UserCode); err != nil {
		return "", errors.New("display GitHub device authorization")
	}
	deadline := time.Now().Add(time.Duration(code.ExpiresIn) * time.Second)
	interval := time.Duration(code.Interval) * time.Second
	for {
		if err := sleepGitHubDevice(ctx, interval, wait); err != nil {
			return "", errors.New("GitHub device authorization canceled")
		}
		if time.Now().After(deadline) {
			return "", errors.New("GitHub device authorization expired")
		}
		var token struct {
			AccessToken string `json:"access_token"`
			TokenType   string `json:"token_type"`
			Error       string `json:"error"`
			Refresh     string `json:"refresh_token"`
			ExpiresIn   int    `json:"expires_in"`
		}
		poll := url.Values{
			"client_id":   {githubClientID},
			"device_code": {code.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}
		if err := postGitHubDevice(ctx, &httpClient, baseURL, "/login/oauth/access_token", poll, &token); err != nil {
			return "", err
		}
		switch token.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "access_denied":
			return "", errors.New("GitHub device authorization was rejected")
		case "expired_token":
			return "", errors.New("GitHub device authorization expired")
		case "incorrect_client_credentials", "incorrect_device_code", "unsupported_grant_type", "device_flow_disabled":
			return "", errors.New("GitHub device authorization configuration was rejected")
		case "":
		default:
			return "", errors.New("GitHub device authorization failed")
		}
		if token.AccessToken == "" || !strings.EqualFold(token.TokenType, "bearer") || credential.ValidateBearerToken(token.AccessToken) != nil {
			return "", errors.New("GitHub returned an invalid device access token")
		}
		if token.Refresh != "" || token.ExpiresIn != 0 {
			return "", errors.New("GitHub returned an expiring token that Colt cannot refresh; use a non-expiring OAuth app configuration")
		}
		return token.AccessToken, nil
	}
}

func postGitHubDevice(ctx context.Context, hc *http.Client, baseURL, path string, form url.Values, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("build GitHub device authorization request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return errors.New("GitHub device authorization request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
		return fmt.Errorf("GitHub device authorization failed: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result); err != nil {
		return errors.New("decode GitHub device authorization response")
	}
	return nil
}

func sleepGitHubDevice(ctx context.Context, duration time.Duration, wait func(context.Context, time.Duration) error) error {
	if wait != nil {
		return wait(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
