package git

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestINIT_007ProviderSpecificPushCredentialIsProcessScopedAndRedacted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed fake git is Unix-only")
	}
	for _, tc := range []struct{ providerType, username string }{{"github", "x-access-token"}, {"gitlab", "oauth2"}} {
		t.Run(tc.providerType, func(t *testing.T) {
			bin := t.TempDir()
			script := filepath.Join(bin, "git")
			contents := `#!/bin/sh
case "$*" in *test-secret*) echo "token leaked in arguments"; exit 2;; esac
test "$*" = "push --set-upstream origin HEAD" || exit 8
test "$GIT_CONFIG_COUNT" = 2 || exit 3
test "$GIT_CONFIG_KEY_0" = "http.https://example.test/team/demo.git.extraHeader" || exit 4
test "$GIT_CONFIG_KEY_1" = "http.followRedirects" || exit 5
test "$GIT_CONFIG_VALUE_1" = false || exit 6
test "$GIT_CONFIG_VALUE_0" = "$WANT_HEADER" || exit 7
echo "$GIT_CONFIG_VALUE_0"
exit 1
`
			if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			t.Setenv("WANT_HEADER", "Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte(tc.username+":test-secret")))
			err := (Native{}).Push(context.Background(), t.TempDir(), "https://example.test/team/demo.git", tc.providerType, "test-secret")
			if err == nil {
				t.Fatal("fake push unexpectedly succeeded")
			}
			if strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "Authorization: Basic") || !strings.Contains(err.Error(), "check authentication") {
				t.Fatalf("push error exposed credentials: %v", err)
			}
		})
	}
}

func TestCORE_CREDENTIAL_002InheritedGitExecPathCannotStealPushCredential(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed credential helper is Unix-only")
	}
	dir := t.TempDir()
	native := Native{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := native.Init(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := native.SetIdentity(ctx, dir, "Test", "test@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Commit(ctx, dir); err != nil {
		t.Fatal(err)
	}
	const remote = "https://127.0.0.1:1/team/demo.git"
	if err := native.AddOrigin(ctx, dir, remote); err != nil {
		t.Fatal(err)
	}
	helperDir := t.TempDir()
	stolen := filepath.Join(t.TempDir(), "stolen")
	helper := filepath.Join(helperDir, "git-remote-https")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s' \"$GIT_CONFIG_VALUE_0\" > \"$STEAL_PATH\"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_EXEC_PATH", helperDir)
	t.Setenv("STEAL_PATH", stolen)
	if err := native.Push(ctx, dir, remote, "github", "test-secret"); err == nil {
		t.Fatal("push unexpectedly succeeded")
	}
	if _, err := os.Stat(stolen); !os.IsNotExist(err) {
		t.Fatalf("inherited GIT_EXEC_PATH helper invoked: %v", err)
	}
}

func TestCORE_GIT_008CredentialHelperIsLocalAndPreservesExistingHelpers(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	native := Native{}
	ctx := context.Background()
	if err := native.Init(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := native.run(ctx, dir, nil, "config", "--local", "--add", "credential.helper", "existing-helper"); err != nil {
		t.Fatal(err)
	}
	if err := native.ConfigureCredentialHelper(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := native.ConfigureCredentialHelper(ctx, dir); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "git", "config", "--local", "--get-all", "credential.helper")
	cmd.Dir, cmd.Env = dir, gitEnv(nil)
	output, err := cmd.Output()
	if err != nil || string(output) != "existing-helper\n!colt git-credential\n" {
		t.Fatalf("helpers=%q error=%v", output, err)
	}
	cmd = exec.CommandContext(ctx, "git", "config", "--local", "--get", "credential.useHttpPath")
	cmd.Dir, cmd.Env = dir, gitEnv(nil)
	if output, err = cmd.Output(); err != nil || string(output) != "true\n" {
		t.Fatalf("credential.useHttpPath=%q error=%v", output, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gitconfig")); !os.IsNotExist(err) {
		t.Fatalf("global config changed: %v", err)
	}
}

func TestCORE_GIT_010SSHPushPreservesGitAndSSHEnvironmentWithoutInjectingAuth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed fake git is Unix-only")
	}
	bin := t.TempDir()
	script := filepath.Join(bin, "git")
	contents := `#!/bin/sh
test "$*" = "push --set-upstream origin HEAD" || exit 2
test "$GIT_SSH" = "/user/ssh-wrapper" || exit 3
test "$GIT_SSH_COMMAND" = "ssh -F user-config" || exit 4
test "$GIT_SSH_VARIANT" = "ssh" || exit 5
test "$GIT_CONFIG_GLOBAL" = "/user/global.gitconfig" || exit 6
test "$GIT_CONFIG_SYSTEM" = "/user/system.gitconfig" || exit 7
test "$GIT_CONFIG_COUNT" = 1 || exit 8
test "$GIT_CONFIG_KEY_0" = "core.sshCommand" || exit 9
test "$GIT_CONFIG_VALUE_0" = "ssh inherited" || exit 10
test -z "$GIT_CONFIG_KEY_1" || exit 11
case "$(set)" in *must-not-be-injected*) exit 12;; esac
`
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("GIT_SSH", "/user/ssh-wrapper")
	t.Setenv("GIT_SSH_COMMAND", "ssh -F user-config")
	t.Setenv("GIT_SSH_VARIANT", "ssh")
	t.Setenv("GIT_CONFIG_GLOBAL", "/user/global.gitconfig")
	t.Setenv("GIT_CONFIG_SYSTEM", "/user/system.gitconfig")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.sshCommand")
	t.Setenv("GIT_CONFIG_VALUE_0", "ssh inherited")
	t.Setenv("GIT_CONFIG_KEY_1", "")
	if err := (Native{}).Push(context.Background(), t.TempDir(), "git@example.test:team/demo.git", "github", "must-not-be-injected"); err != nil {
		t.Fatal(err)
	}
}
