package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealthReadOnlyInspectionAndHostileConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gitEnv(nil)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "--initial-branch=main", "--template=")
	run("config", "--local", "user.name", "Health User")
	run("config", "--local", "user.email", "health@example.invalid")
	run("remote", "add", "origin", "https://example.invalid/team/demo.git")
	state, err := (Native{}).Health(context.Background(), dir)
	if err != nil || state.Name != "Health User" || state.Email != "health@example.invalid" || state.Origin == "" || !state.Clean {
		t.Fatalf("state=%+v error=%v", state, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("config", "--local", "status.showUntrackedFiles", "no")
	state, err = (Native{}).Health(context.Background(), dir)
	if err != nil || state.Clean {
		t.Fatalf("dirty state=%+v error=%v", state, err)
	}
	run("config", "--local", "core.fsmonitor", "malicious-command secret")
	if _, err := (Native{}).Health(context.Background(), dir); err == nil || err.Error() == "malicious-command secret" {
		t.Fatalf("hostile config error=%v", err)
	}
}

func TestHealthRejectsExecutableConfigClassesWithoutExecution(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	for _, tc := range []struct{ name, key, value string }{
		{"hooks", "core.hooksPath", "VALUE"},
		{"fsmonitor", "core.fsmonitor", "VALUE"},
		{"filter", "filter.bad.clean", "VALUE"},
		{"textconv", "diff.bad.textconv", "VALUE"},
		{"include", "include.path", "VALUE"},
		{"credential", "credential.helper", "!VALUE"},
		{"submodule", "submodule.bad.update", "!VALUE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sentinel := filepath.Join(dir, "executed")
			script := filepath.Join(dir, "malicious")
			if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch \""+sentinel+"\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			run := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir, cmd.Env = dir, gitEnv(nil)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v: %s", args, err, out)
				}
			}
			run("init", "--initial-branch=main", "--template=")
			value := strings.ReplaceAll(tc.value, "VALUE", script)
			run("config", "--local", tc.key, value)
			configPath := filepath.Join(dir, ".git", "config")
			before, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := (Native{}).Health(context.Background(), dir); err == nil || strings.Contains(err.Error(), script) {
				t.Fatalf("hostile %s config error=%v", tc.name, err)
			}
			after, err := os.ReadFile(configPath)
			if err != nil || string(after) != string(before) {
				t.Fatalf("config changed: %v", err)
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatalf("hostile %s command executed: %v", tc.name, err)
			}
		})
	}
}

func TestHealthTreatsSubmodulesAsSafelyUninspectable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, gitEnv(nil)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--initial-branch=main", "--template=")
	run("config", "user.name", "Health User")
	run("config", "user.email", "health@example.invalid")
	run("commit", "--allow-empty", "-m", "base")
	head := run("rev-parse", "HEAD")
	run("update-index", "--add", "--cacheinfo", "160000,"+head+",submodule")
	if _, err := (Native{}).Health(context.Background(), dir); err == nil {
		t.Fatal("submodule repository was reported healthy")
	}
}

func TestHealthStreamsLargeIndexAndFindsLateSubmodule(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, gitEnv(nil)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "--initial-branch=main", "--template=")
	run("config", "user.name", "Health User")
	run("config", "user.email", "health@example.invalid")
	run("commit", "--allow-empty", "-m", "base")
	for i := 0; i < 1200; i++ {
		name := filepath.Join(dir, fmt.Sprintf("%04d-%064d", i, i))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	state, err := (Native{}).Health(context.Background(), dir)
	if err != nil || state.Clean {
		t.Fatalf("large dirty repository state=%+v error=%v", state, err)
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir, cmd.Env = dir, gitEnv(nil)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	run("update-index", "--add", "--cacheinfo", "160000,"+strings.TrimSpace(string(out))+",zzzz-submodule")
	if _, err := (Native{}).Health(context.Background(), dir); err == nil {
		t.Fatal("late submodule in a large index was not detected")
	}
}
