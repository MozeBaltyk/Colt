package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Runner interface {
	Available() error
	Init(context.Context, string) error
	Clone(context.Context, string, string, string, string, string) error
	SetIdentity(context.Context, string, string, string) error
	Commit(context.Context, string) (string, error)
	AddOrigin(context.Context, string, string) error
	ConfigureCredentialHelper(context.Context, string, string, string, string, string) error
	Push(context.Context, string, string) error
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

func (n Native) Clone(ctx context.Context, cloneURL, destination, username, alias, project string) error {
	args := []string{"clone", "--", cloneURL, destination}
	if strings.HasPrefix(cloneURL, "https://") {
		helper, err := credentialHelper(alias, project)
		if err != nil {
			return err
		}
		args = append([]string{"-c", "http.followRedirects=false", "-c", "credential.helper=" + helper, "-c", "credential.useHttpPath=true"}, args...)
		if ca := os.Getenv("SSL_CERT_FILE"); ca != "" {
			args = append([]string{"-c", "http.sslCAInfo=" + ca}, args...)
		}
		if username != "" {
			if strings.ContainsAny(username, "\r\n") {
				return errors.New("refusing unsafe Git credential username")
			}
			args = append([]string{"-c", "credential." + cloneURL + ".username=" + username}, args...)
		}
	} else if !strings.HasPrefix(cloneURL, "git@") {
		return errors.New("unsupported Git transport")
	}
	if err := n.run(ctx, "", []string{"GIT_TERMINAL_PROMPT=0"}, args...); err != nil {
		return errors.New("native git clone failed; check authentication, remote access, and connectivity")
	}
	return nil
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

func (n Native) ConfigureCredentialHelper(ctx context.Context, dir, cloneURL, username, alias, project string) error {
	helper, err := credentialHelper(alias, project)
	if err != nil {
		return err
	}
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

func credentialHelper(alias, project string) (string, error) {
	safe := func(value string) bool {
		return value != "" && strings.IndexFunc(value, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-')
		}) < 0
	}
	if !safe(alias) || !safe(project) {
		return "", errors.New("refusing unsafe provider or repository scope for Git credential helper")
	}
	return "!colt git-credential --provider " + alias + " --repository " + project, nil
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

func (n Native) Push(ctx context.Context, dir, cloneURL string) error {
	if !strings.HasPrefix(cloneURL, "https://") && !strings.HasPrefix(cloneURL, "git@") {
		return errors.New("unsupported Git transport")
	}
	env := []string{"GIT_TERMINAL_PROMPT=0"}
	if ca := os.Getenv("SSL_CERT_FILE"); ca != "" {
		env = append(env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.sslCAInfo", "GIT_CONFIG_VALUE_0="+ca)
	}
	err := n.run(ctx, dir, env, "-c", "http.followRedirects=false", "push", "--set-upstream", "origin", "HEAD")
	if err != nil {
		if strings.HasPrefix(cloneURL, "git@") {
			return errors.New("native git push failed; check SSH access and connectivity")
		}
		return errors.New("native git push failed; check authentication, remote access, and connectivity")
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
