package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	"github.com/MozeBaltyk/Colt/internal/provider"
)

func TestInteractiveRequiredInputsAndNoninteractiveErrors(t *testing.T) {
	t.Run("login prompt order and defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		t.Setenv("GITHUB_TOKEN", "prompt-test-token")
		a := &App{ConfigPath: path, IsTerminal: func() bool { return true }, NewClient: func(p config.Provider, token string) (provider.Client, error) {
			if p.Visibility != "private" || p.Transport != "" || token != "prompt-test-token" {
				t.Fatalf("provider=%+v token=%q", p, token)
			}
			return &fakeClient{account: "octocat"}, nil
		}}
		output, err := executeInput(t, a, "github\npersonal\noctocat\nTest User\ntest@example.com\n", "auth", "login")
		if err != nil {
			t.Fatal(err)
		}
		want := "provider: alias: namespace: git-name: git-email: "
		if !strings.HasPrefix(output, want) || strings.Contains(output, "visibility:") || strings.Contains(output, "transport:") {
			t.Fatalf("output=%q", output)
		}
	})

	t.Run("noninteractive lists all missing input and does not mutate", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		store := &recordingStore{credentials: map[string]credential.Credential{}}
		clientCalls := 0
		a := &App{ConfigPath: path, Credentials: store, IsTerminal: func() bool { return true }, NewClient: func(config.Provider, string) (provider.Client, error) {
			clientCalls++
			return &fakeClient{}, nil
		}}
		output, err := executeInput(t, a, "ignored\n", "--noninteractive", "auth", "login")
		for _, want := range []string{"provider", "alias", "--namespace", "--git-name", "--git-email"} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error=%v missing %q", err, want)
			}
		}
		if output != "" || clientCalls != 0 || store.gets != 0 || store.puts != 0 {
			t.Fatalf("output=%q clients=%d store=%#v", output, clientCalls, store)
		}
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("config mutated: %v", statErr)
		}
	})

	t.Run("init and logout prompt for positional values", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "config.yaml")
		p := appProvider()
		p.Default = true
		writeAppConfig(t, path, p)
		runner := &fakeGit{}
		a := &App{ConfigPath: path, WorkDir: root, Git: runner, IsTerminal: func() bool { return true }}
		output, err := executeInput(t, a, "demo\n", "init", "--local")
		if err != nil || !strings.HasPrefix(output, "project: ") || len(runner.calls) == 0 {
			t.Fatalf("error=%v output=%q calls=%v", err, output, runner.calls)
		}
		output, err = executeInput(t, a, "work\n", "auth", "logout")
		if err != nil || !strings.HasPrefix(output, "alias: ") {
			t.Fatalf("error=%v output=%q", err, output)
		}
	})

	t.Run("explicit invalid project is not prompted", func(t *testing.T) {
		a := &App{IsTerminal: func() bool { return true }}
		output, err := executeInput(t, a, "replacement\n", "init", "")
		if err == nil || !strings.Contains(err.Error(), "invalid project") || !strings.Contains(output, "Preflight: failed") {
			t.Fatalf("error=%v output=%q", err, output)
		}
	})

	t.Run("non-TTY init and logout fail before mutation", func(t *testing.T) {
		runner := &fakeGit{}
		store := &recordingStore{credentials: map[string]credential.Credential{}}
		a := &App{ConfigPath: filepath.Join(t.TempDir(), "config.yaml"), Git: runner, Credentials: store}
		for _, args := range [][]string{{"init", "--local"}, {"auth", "logout"}} {
			output, err := executeInput(t, a, "ignored\n", args...)
			if err == nil || !strings.Contains(err.Error(), "missing required input") || output != "" || len(runner.calls) != 0 || store.gets != 0 || store.puts != 0 {
				t.Fatalf("args=%v error=%v output=%q git=%v store=%#v", args, err, output, runner.calls, store)
			}
		}
	})
}
