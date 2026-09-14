package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type Runner interface {
	Available() error
	Origin(context.Context, string) (string, error)
	LsRemote(context.Context, string, string, string, string, []string) error
	Init(context.Context, string) error
	Clone(context.Context, string, string, string, string, string) error
	SetIdentity(context.Context, string, string, string) error
	Commit(context.Context, string) (string, error)
	AddOrigin(context.Context, string, string) error
	ConfigureCredentialHelper(context.Context, string, string, string, string, string) error
	Push(context.Context, string, string, string, string) error
	CreateTag(context.Context, string, string) error
	TagExists(context.Context, string, string) error
}

type Native struct{}

func (Native) Available() error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("native git is unavailable; install git and ensure it is on PATH")
	}
	return nil
}

func (Native) Origin(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "config", "--get", "remote.origin.url")
	cmd.Dir, cmd.Env = dir, gitEnv(nil)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", errors.New("native git failed to inspect origin")
	}
	return strings.TrimSpace(string(output)), nil
}

func (n Native) LsRemote(ctx context.Context, dir, origin, alias, project string, tokenEnvNames []string) error {
	args := []string{"ls-remote", "--", origin}
	env := []string{"GIT_TERMINAL_PROMPT=0"}
	ssh := false
	if strings.HasPrefix(origin, "https://") {
		helper, err := transientCredentialHelper(alias, project)
		if err != nil {
			return err
		}
		args = append([]string{"-c", "http.followRedirects=false", "-c", "credential.helper=", "-c", "credential.helper=" + helper, "-c", "credential.useHttpPath=true"}, args...)
		if ca := os.Getenv("SSL_CERT_FILE"); ca != "" {
			args = append([]string{"-c", "http.sslCAInfo=" + ca}, args...)
		}
	} else if strings.HasPrefix(origin, "git@") {
		ssh = true
		env = append(env, "GIT_SSH_COMMAND=ssh -oBatchMode=yes -oStrictHostKeyChecking=yes")
	} else {
		return errors.New("unsupported Git transport")
	}
	var err error
	if ssh {
		err = n.runWithEnv(ctx, dir, gitEnvWithout(env, tokenEnvNames), args...)
	} else {
		err = n.run(ctx, dir, env, args...)
	}
	if err != nil {
		return errors.New("origin is unreachable; check repository access and connectivity")
	}
	return nil
}

func (n Native) Init(ctx context.Context, dir string) error {
	return n.run(ctx, dir, nil, "init", "--initial-branch=main", "--template=")
}

func (n Native) Clone(ctx context.Context, cloneURL, destination, username, alias, project string) error {
	args := []string{"clone", "--", cloneURL, destination}
	if strings.HasPrefix(cloneURL, "https://") {
		helper, err := transientCredentialHelper(alias, project)
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
		if strings.HasPrefix(cloneURL, "https://") && credentialHelperNotFound(err) {
			return fmt.Errorf("Git credential helper not found: %w", err)
		}
		return fmt.Errorf("native git clone failed: %w", err)
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
	if cloneURL != "" {
		if err := n.configureScopedCredentialHelper(ctx, dir, cloneURL, helper); err != nil {
			return err
		}
		return n.configureCredentialScope(ctx, dir, cloneURL, username)
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

func (n Native) configureScopedCredentialHelper(ctx context.Context, dir, cloneURL, helper string) error {
	key := "credential." + cloneURL + ".helper"
	cmd := exec.CommandContext(ctx, "git", "config", "--local", "--get-all", key)
	cmd.Dir, cmd.Env = dir, gitEnv(nil)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return errors.New("native git failed to read repository-local credential helpers")
		}
	}
	existing := make([]string, 0)
	for _, value := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if value != "" && value != helper {
			existing = append(existing, value)
		}
	}
	// An empty first value resets lower-priority helpers for this URL. Colt is
	// first, while pre-existing URL-specific helpers remain available as fallback.
	if err := n.run(ctx, dir, nil, "config", "--local", "--replace-all", key, ""); err != nil {
		return err
	}
	if err := n.run(ctx, dir, nil, "config", "--local", "--add", key, helper); err != nil {
		return err
	}
	for _, value := range existing {
		if err := n.run(ctx, dir, nil, "config", "--local", "--add", key, value); err != nil {
			return err
		}
	}
	return nil
}

func SafeIdentifier(value string) bool {
	return value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-')
	}) < 0
}

func credentialHelper(alias, project string) (string, error) {
	if !SafeIdentifier(alias) || !SafeIdentifier(project) {
		return "", errors.New("refusing unsafe provider or repository scope for Git credential helper")
	}
	return "!colt git-credential --provider " + alias + " --repository " + project, nil
}

func transientCredentialHelper(alias, project string) (string, error) {
	if _, err := credentialHelper(alias, project); err != nil {
		return "", err
	}
	executable, err := os.Executable()
	if err != nil {
		return "", errors.New("locate Colt executable for Git credential helper")
	}
	quoted := "'" + strings.ReplaceAll(executable, "'", `'"'"'`) + "'"
	return "!" + quoted + " git-credential --provider " + alias + " --repository " + project, nil
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

func (n Native) Push(ctx context.Context, dir, cloneURL, alias, project string) error {
	if !strings.HasPrefix(cloneURL, "https://") && !strings.HasPrefix(cloneURL, "git@") {
		return errors.New("unsupported Git transport")
	}
	args := []string{"-c", "http.followRedirects=false", "push", "--set-upstream", "origin", "HEAD"}
	env := []string{"GIT_TERMINAL_PROMPT=0"}
	if strings.HasPrefix(cloneURL, "https://") {
		helper, err := transientCredentialHelper(alias, project)
		if err != nil {
			return err
		}
		args = append([]string{"-c", "credential.helper=", "-c", "credential.helper=" + helper, "-c", "credential.useHttpPath=true"}, args...)
		if ca := os.Getenv("SSL_CERT_FILE"); ca != "" {
			args = append([]string{"-c", "http.sslCAInfo=" + ca}, args...)
		}
	}
	err := n.run(ctx, dir, env, args...)
	if err != nil {
		if strings.HasPrefix(cloneURL, "https://") && credentialHelperNotFound(err) {
			return fmt.Errorf("Git credential helper not found: %w", err)
		}
		return fmt.Errorf("native git push failed: %w", err)
	}
	return nil
}

func (Native) TagExists(ctx context.Context, dir, tag string) error {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "refs/tags/"+tag)
	cmd.Dir = dir
	cmd.Env = gitEnv(nil)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tag %q does not exist locally", tag)
	}
	return nil
}

func (Native) CreateTag(ctx context.Context, dir, tag string) error {
	cmd := exec.CommandContext(ctx, "git", "tag", "-a", tag, "-m", tag)
	cmd.Dir = dir
	cmd.Env = gitEnv(nil)
	return cmd.Run()
}

func credentialHelperNotFound(err error) bool {
	text := strings.ToLower(err.Error())
	helper := strings.Contains(text, "colt") || strings.Contains(text, "git-credential")
	return helper && (strings.Contains(text, ": not found") || strings.Contains(text, "no such file or directory") || strings.Contains(text, "not a git command"))
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
	return gitEnvWithout(extraEnv, nil)
}

func gitEnvWithout(extraEnv, excluded []string) []string {
	env := make([]string, 0, len(os.Environ())+len(extraEnv)+2)
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			continue
		}
		blocked := false
		for _, excludedName := range excluded {
			if name == excludedName || runtime.GOOS == "windows" && strings.EqualFold(name, excludedName) {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		env = append(env, item)
	}
	env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	env = append(env, extraEnv...)
	return env
}
