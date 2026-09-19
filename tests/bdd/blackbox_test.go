//go:build bdd

package bdd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
	"github.com/creack/pty"
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
		if !strings.Contains(output, "Local state: initial commit") {
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

	t.Run("manual login without a tty fails without persistence", func(t *testing.T) {
		dir, configPath := blackboxSandbox(t)
		ctx, cancel := context.WithTimeout(context.Background(), fixture.CommandTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "auth", "login", "github", "personal", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
		cmd.Dir, cmd.Env, cmd.Stdin = dir, blackboxEnv(filepath.Join(dir, "home"), configPath), strings.NewReader("")
		output, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || !strings.Contains(string(output), "interactive terminal") {
			t.Fatalf("error=%v output=%q", err, output)
		}
		for _, path := range []string{configPath, filepath.Join(dir, "credentials")} {
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("unexpected persisted state at %s: %v", path, statErr)
			}
		}
	})
}

// TestBlackboxSecretPromptDoesNotEcho drives the real binary under a
// pseudo-terminal and asserts that a prompted reusable secret never appears in
// the terminal transcript (echo is disabled by the non-echoing reader).
func TestBlackboxSecretPromptDoesNotEcho(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pseudo-terminal test is not supported on Windows")
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

	dir, configPath := blackboxSandbox(t)
	secret := "prompted-fake-secret"

	runCtx, runCancel := context.WithTimeout(context.Background(), fixture.CommandTimeout)
	defer runCancel()
	cmd := exec.CommandContext(runCtx, binary, "auth", "login", "github", "personal",
		"--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
	cmd.Env = blackboxEnv(filepath.Join(dir, "home"), configPath)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	defer ptmx.Close()

	var mu sync.Mutex
	var transcript strings.Builder
	readDone := make(chan struct{})
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				mu.Lock()
				transcript.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				close(readDone)
				return
			}
		}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		seen := strings.Contains(transcript.String(), "Token: ")
		mu.Unlock()
		if seen {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("token prompt not observed; transcript=%q", transcript.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// ponytail: brief delay so the child enters `term.ReadPassword` and disables
	// echo before we type; the race is inherent to PTY testing without a termios
	// probe and 400ms is generous for a local/CI bdd lane.
	time.Sleep(400 * time.Millisecond)
	if _, err := ptmx.WriteString(secret + "\n"); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	select {
	case <-readDone:
	case <-time.After(10 * time.Second):
	}
	_ = cmd.Wait()

	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(transcript.String(), secret) {
		t.Fatalf("prompted secret echoed to terminal transcript:\n%s", transcript.String())
	}
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
		"GITHUB_TOKEN": "", "GITLAB_TOKEN": "", "GITEA_TOKEN": "", "FORGEJO_TOKEN": "",
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
