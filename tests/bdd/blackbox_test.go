//go:build bdd

package bdd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
)

func TestBlackbox(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("native git not on PATH")
	}
	buildDir := t.TempDir()
	binary := filepath.Join(buildDir, "colt")
	ctx, cancel := context.WithTimeout(context.Background(), fixture.CommandTimeout)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/colt")
	build.Dir = repoRoot()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build colt: %v: %s", err, output)
	}

	t.Run("init local", func(t *testing.T) {
		dir, configPath := blackboxSandbox(t)
		config := "providers:\n  work:\n    type: gitlab\n    host: gitlab.com\n    base_url: https://gitlab.com\n    namespace: example-ns\n    visibility: private\n    git_name: Example User\n    git_email: user@example.invalid\n    auth:\n      source: env\n      token_env: COLT_BDD_TOKEN\n    default: true\n"
		if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		output := runBlackbox(t, binary, dir, configPath, "init", "demo", "--local")
		if !strings.Contains(output, "initialized demo") {
			t.Fatalf("unexpected init output: %q", output)
		}
		if _, err := os.Stat(filepath.Join(dir, "demo", ".git")); err != nil {
			t.Fatalf("local repository missing: %v", err)
		}
		repository := filepath.Join(dir, "demo")
		for _, check := range []struct {
			name string
			args []string
			want string
		}{
			{"commit count", []string{"rev-list", "--count", "HEAD"}, "1"},
			{"local name", []string{"config", "--local", "user.name"}, "Example User"},
			{"local email", []string{"config", "--local", "user.email"}, "user@example.invalid"},
			{"commit identity", []string{"log", "-1", "--format=%an <%ae>|%cn <%ce>"}, "Example User <user@example.invalid>|Example User <user@example.invalid>"},
			{"no origin", []string{"remote"}, ""},
		} {
			t.Run(check.name, func(t *testing.T) {
				if got := runBlackboxGit(t, repository, configPath, check.args...); got != check.want {
					t.Fatalf("git %s = %q, want %q", strings.Join(check.args, " "), got, check.want)
				}
			})
		}
	})

	t.Run("offline status", func(t *testing.T) {
		dir, configPath := blackboxSandbox(t)
		output := runBlackbox(t, binary, dir, configPath, "auth", "status", "--offline")
		if output != "no providers configured\n" {
			t.Fatalf("unexpected status output: %q", output)
		}
	})

	t.Run("invalid command exits with a useful error", func(t *testing.T) {
		dir, configPath := blackboxSandbox(t)
		ctx, cancel := context.WithTimeout(context.Background(), fixture.CommandTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "not-a-command")
		cmd.Dir, cmd.Env = dir, blackboxEnv(filepath.Join(dir, "home"), configPath)
		output, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || !strings.Contains(string(output), "unknown command") {
			t.Fatalf("error=%v output=%q", err, output)
		}
	})
}

func blackboxSandbox(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "home"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, filepath.Join(dir, "config.yaml")
}

func runBlackbox(t *testing.T, binary, dir, configPath string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fixture.CommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = blackboxEnv(filepath.Join(dir, "home"), configPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("colt %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func runBlackboxGit(t *testing.T, dir, configPath string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fixture.CommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir, cmd.Env = dir, blackboxEnv(filepath.Join(filepath.Dir(configPath), "home"), configPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func blackboxEnv(home, configPath string) []string {
	overrides := map[string]string{
		"HOME": home, "COLT_CONFIG": configPath, "COLT_BDD_TOKEN": "bdd-fake-secret",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0",
		"HTTP_PROXY": "http://127.0.0.1:1", "HTTPS_PROXY": "http://127.0.0.1:1", "ALL_PROXY": "http://127.0.0.1:1",
		"http_proxy": "http://127.0.0.1:1", "https_proxy": "http://127.0.0.1:1", "all_proxy": "http://127.0.0.1:1",
		"NO_PROXY": "localhost,127.0.0.1", "no_proxy": "localhost,127.0.0.1",
	}
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		_, replaced := overrides[name]
		if !replaced && !strings.HasPrefix(name, "GIT_") {
			env = append(env, entry)
		}
	}
	for name, value := range overrides {
		env = append(env, name+"="+value)
	}
	return env
}
