package git

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Runner interface {
	Available() error
	Init(context.Context, string) error
	SetIdentity(context.Context, string, string, string) error
	Commit(context.Context, string) (string, error)
	AddOrigin(context.Context, string, string) error
	Push(context.Context, string, string, string, string) error
}

type Native struct{}

func (Native) Available() error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("native git is unavailable; install git and ensure it is on PATH")
	}
	return nil
}

func (n Native) Init(ctx context.Context, dir string) error {
	return n.run(ctx, dir, nil, "init", "--initial-branch=main", "--template=")
}

func (n Native) SetIdentity(ctx context.Context, dir, name, email string) error {
	if err := n.run(ctx, dir, nil, "config", "--local", "user.name", name); err != nil {
		return err
	}
	return n.run(ctx, dir, nil, "config", "--local", "user.email", email)
}

func (n Native) Commit(ctx context.Context, dir string) (string, error) {
	if err := n.run(ctx, dir, nil, "commit", "--allow-empty", "-m", "Initial commit"); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = dir
	cmd.Env = gitEnv(nil)
	output, err := cmd.Output()
	if err != nil {
		return "", errors.New("native git failed to read the initial commit")
	}
	return strings.TrimSpace(string(output)), nil
}

func (n Native) AddOrigin(ctx context.Context, dir, cloneURL string) error {
	return n.run(ctx, dir, nil, "remote", "add", "origin", cloneURL)
}

func (n Native) Push(ctx context.Context, dir, cloneURL, providerType, token string) error {
	username := "x-access-token"
	if providerType == "gitlab" {
		username = "oauth2"
	} else if providerType != "github" {
		return fmt.Errorf("unsupported provider type %q", providerType)
	}
	header := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+token))
	env := []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=http." + cloneURL + ".extraHeader",
		"GIT_CONFIG_VALUE_0=" + header,
		"GIT_CONFIG_KEY_1=http.followRedirects",
		"GIT_CONFIG_VALUE_1=false",
	}
	err := n.run(ctx, dir, env, "push", "--set-upstream", "origin", "HEAD")
	if err != nil {
		return errors.New("native git push failed; check authentication, remote access, and connectivity")
	}
	return nil
}

func (Native) run(ctx context.Context, dir string, extraEnv []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(extraEnv)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		for _, item := range extraEnv {
			if strings.HasPrefix(item, "GIT_CONFIG_VALUE_0=") {
				text = strings.ReplaceAll(text, strings.TrimPrefix(item, "GIT_CONFIG_VALUE_0="), "[REDACTED]")
			}
		}
		if len(text) > 8192 {
			text = text[:8192]
		}
		return fmt.Errorf("native git failed: %s", text)
	}
	return nil
}

func gitEnv(extraEnv []string) []string {
	env := make([]string, 0, len(os.Environ())+len(extraEnv)+2)
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(name, "GIT_") {
			continue
		}
		env = append(env, item)
	}
	env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	env = append(env, extraEnv...)
	return env
}
