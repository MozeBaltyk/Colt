//go:build bdd

package bdd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlackbox(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("native git not on PATH")
	}
	buildDir := t.TempDir()
	binary := filepath.Join(buildDir, "colt")
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
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
	})

	t.Run("offline status", func(t *testing.T) {
		dir, configPath := blackboxSandbox(t)
		output := runBlackbox(t, binary, dir, configPath, "auth", "status", "--offline")
		if output != "no providers configured\n" {
			t.Fatalf("unexpected status output: %q", output)
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
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
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
		if _, replaced := overrides[name]; !replaced {
			env = append(env, entry)
		}
	}
	for name, value := range overrides {
		env = append(env, name+"="+value)
	}
	return env
}
