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
	ConfigureCredentialHelper(context.Context, string, string, string) error
	Push(context.Context, string, string, string, string, string) error
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

func (n Native) ConfigureCredentialHelper(ctx context.Context, dir, cloneURL, username string) error {
	const helper = "!colt git-credential"
	cmd := exec.CommandContext(ctx, "git", "config", "--local", "--get-all", "credential.helper")
	cmd.Dir = dir
	cmd.Env = gitEnv(nil)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return errors.New("native git failed to read repository-local credential helpers")
		}
	}
	for _, configured := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if configured == helper {
			return n.configureCredentialScope(ctx, dir, cloneURL, username)
		}
	}
	if err := n.run(ctx, dir, nil, "config", "--local", "--add", "credential.helper", helper); err != nil {
		return err
	}
	return n.configureCredentialScope(ctx, dir, cloneURL, username)
}

func (n Native) configureCredentialScope(ctx context.Context, dir, cloneURL, username string) error {
	if err := n.run(ctx, dir, nil, "config", "--local", "credential.useHttpPath", "true"); err != nil {
		return err
	}
	if username == "" {
		return nil
	}
	if strings.ContainsAny(username, "\r\n") {
		return errors.New("refusing unsafe Git credential username")
	}
	return n.run(ctx, dir, nil, "config", "--local", "credential."+cloneURL+".username", username)
}

func (n Native) Push(ctx context.Context, dir, cloneURL, providerType, username, token string) error {
	if strings.HasPrefix(cloneURL, "git@") {
		if err := n.run(ctx, dir, nil, "push", "--set-upstream", "origin", "HEAD"); err != nil {
			return errors.New("native git push failed; check SSH access and connectivity")
		}
		return nil
	}
	if !strings.HasPrefix(cloneURL, "https://") {
		return errors.New("unsupported Git transport")
	}
	gitUsername := "x-access-token"
	if providerType == "gitlab" {
		gitUsername = "oauth2"
	} else if providerType == "gitea" || providerType == "forgejo" {
		if username == "" || strings.ContainsAny(username, "\r\n") {
			return errors.New("Gitea/Forgejo Git authentication requires a safe authenticated username")
		}
		gitUsername = username
	} else if providerType != "github" {
		return fmt.Errorf("unsupported provider type %q", providerType)
	}
	header := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(gitUsername+":"+token))
	env := []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=http." + cloneURL + ".extraHeader",
		"GIT_CONFIG_VALUE_0=" + header,
		"GIT_CONFIG_KEY_1=http.followRedirects",
		"GIT_CONFIG_VALUE_1=false",
	}
	if ca := os.Getenv("SSL_CERT_FILE"); ca != "" {
		env[1] = "GIT_CONFIG_COUNT=3"
		env = append(env, "GIT_CONFIG_KEY_2=http.sslCAInfo", "GIT_CONFIG_VALUE_2="+ca)
	}
	err := n.run(ctx, dir, env, "push", "--set-upstream", "origin", "HEAD")
	if err != nil {
		return fmt.Errorf("native git push failed; check authentication, remote access, and connectivity: %w", err)
	}
	return nil
}

func (Native) run(ctx context.Context, dir string, extraEnv []string, args ...string) error {
	return (Native{}).runWithEnv(ctx, dir, gitEnv(extraEnv), args...)
}

func (Native) runWithEnv(ctx context.Context, dir string, env []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		for _, item := range env {
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
		if strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			continue
		}
		env = append(env, item)
	}
	env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	env = append(env, extraEnv...)
	return env
}
