package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestGitEnvStripsGitVariablesCaseInsensitively(t *testing.T) {
	t.Setenv("Git_SSH_COMMAND", "ssh inherited")
	for _, item := range gitEnv(nil) {
		name, _, _ := strings.Cut(item, "=")
		if strings.EqualFold(name, "Git_SSH_COMMAND") {
			t.Fatalf("gitEnv retained %q", item)
		}
	}
}

func TestTransientHelperUsesCurrentExecutableWhilePersistentHelperUsesPATH(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	transient, err := transientCredentialHelper("work", "demo")
	if err != nil {
		t.Fatal(err)
	}
	persistent, err := credentialHelper("work", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(executable) || transient != "!'"+executable+"' git-credential --provider work --repository demo" || strings.Contains(transient, "!colt ") {
		t.Fatalf("transient helper %q does not use current executable %q", transient, executable)
	}
	if persistent != "!colt git-credential --provider work --repository demo" {
		t.Fatalf("persistent helper = %q", persistent)
	}
}

func TestCloneUsesCredentialHelperWithoutReceivingASecret(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed fake git is Unix-only")
	}
	bin := t.TempDir()
	script := filepath.Join(bin, "git")
	helper, err := transientCredentialHelper("work", "demo")
	if err != nil {
		t.Fatal(err)
	}
	contents := `#!/bin/sh
test "$*" = "$WANT_ARGS" || exit 2
test -z "$GIT_CONFIG_COUNT$GIT_CONFIG_KEY_0$GIT_CONFIG_VALUE_0" || exit 3
`
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("SSL_CERT_FILE", "/tmp/test-ca.pem")
	t.Setenv("WANT_ARGS", "-c credential.https://example.test/team/demo.git.username=alice -c http.sslCAInfo=/tmp/test-ca.pem -c http.followRedirects=false -c credential.helper="+helper+" -c credential.useHttpPath=true clone -- https://example.test/team/demo.git destination")
	if err := (Native{}).Clone(context.Background(), "https://example.test/team/demo.git", "destination", "alice", "work", "demo"); err != nil {
		t.Fatal(err)
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
	if err := native.Push(ctx, dir, remote, "work", "demo"); err == nil {
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
	if err := native.ConfigureCredentialHelper(ctx, dir, "https://gitlab.example/team/demo.git", "gitlabuser", "work", "demo"); err != nil {
		t.Fatal(err)
	}
	if err := native.ConfigureCredentialHelper(ctx, dir, "", "", "work", "demo"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "git", "config", "--local", "--get-all", "credential.helper")
	cmd.Dir, cmd.Env = dir, gitEnv(nil)
	output, err := cmd.Output()
	if err != nil || string(output) != "existing-helper\n!colt git-credential --provider work --repository demo\n" {
		t.Fatalf("helpers=%q error=%v", output, err)
	}
	cmd = exec.CommandContext(ctx, "git", "config", "--local", "--get", "credential.useHttpPath")
	cmd.Dir, cmd.Env = dir, gitEnv(nil)
	if output, err = cmd.Output(); err != nil || string(output) != "true\n" {
		t.Fatalf("credential.useHttpPath=%q error=%v", output, err)
	}
	cmd = exec.CommandContext(ctx, "git", "config", "--local", "--get", "credential.https://gitlab.example/team/demo.git.username")
	cmd.Dir, cmd.Env = dir, gitEnv(nil)
	if output, err = cmd.Output(); err != nil || strings.TrimSpace(string(output)) != "gitlabuser" {
		t.Fatalf("credential.username=%q error=%v", output, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gitconfig")); !os.IsNotExist(err) {
		t.Fatalf("global config changed: %v", err)
	}
}

func TestGiteaCredentialHelperStoresOnlyAuthenticatedUsername(t *testing.T) {
	dir := t.TempDir()
	native := Native{}
	if err := native.Init(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	const remote = "https://code.example/alice/demo.git"
	if err := native.ConfigureCredentialHelper(context.Background(), dir, remote, "alice", "work", "demo"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "config", "--local", "--get", "credential."+remote+".username")
	cmd.Dir, cmd.Env = dir, gitEnv(nil)
	if output, err := cmd.Output(); err != nil || string(output) != "alice\n" {
		t.Fatalf("Gitea username=%q error=%v", output, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil || strings.Contains(string(data), "test-secret") {
		t.Fatalf("unsafe local config: %v %s", err, data)
	}
}

func TestCORE_GIT_010SSHPushStripsGitControlsAndPreservesSSHAgentWithoutInjectingAuth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed fake git is Unix-only")
	}
	bin := t.TempDir()
	script := filepath.Join(bin, "git")
	contents := `#!/bin/sh
test "$*" = "-c http.followRedirects=false push --set-upstream origin HEAD" || exit 2
test -z "$GIT_SSH" || exit 3
test -z "$GIT_SSH_COMMAND" || exit 4
test -z "$GIT_SSH_VARIANT" || exit 5
test "$GIT_CONFIG_GLOBAL" = "/dev/null" || exit 6
test -z "$GIT_CONFIG_SYSTEM" || exit 7
test "$GIT_CONFIG_NOSYSTEM" = 1 || exit 8
test -z "$GIT_CONFIG_COUNT" || exit 9
test -z "$GIT_CONFIG_KEY_0" || exit 10
test -z "$GIT_CONFIG_VALUE_0" || exit 11
test "$SSH_AUTH_SOCK" = "/agent/socket" || exit 12
case "$(set)" in *must-not-be-injected*) exit 13;; esac
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
	t.Setenv("SSH_AUTH_SOCK", "/agent/socket")
	if err := (Native{}).Push(context.Background(), t.TempDir(), "git@example.test:team/demo.git", "work", "demo"); err != nil {
		t.Fatal(err)
	}
}

func TestCORE_GIT_011LsRemoteUsesBoundedNoninteractiveTransientAuthentication(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed fake git is Unix-only")
	}
	for _, tc := range []struct {
		name, origin, wantSSH string
	}{
		{"https", "https://example.test/team/demo.git", ""},
		{"ssh", "git@example.test:team/demo.git", "ssh -oBatchMode=yes -oStrictHostKeyChecking=yes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			script := filepath.Join(bin, "git")
			wantArgs := "ls-remote -- " + tc.origin
			if tc.name == "https" {
				helper, err := transientCredentialHelper("work", "demo")
				if err != nil {
					t.Fatal(err)
				}
				wantArgs = "-c http.followRedirects=false -c credential.helper= -c credential.helper=" + helper + " -c credential.useHttpPath=true " + wantArgs
			}
			contents := "#!/bin/sh\n" +
				"test \"$*\" = \"$WANT_ARGS\" || exit 2\n" +
				"test \"$GIT_TERMINAL_PROMPT\" = 0 || exit 3\n" +
				"test \"$GIT_SSH_COMMAND\" = \"" + tc.wantSSH + "\" || exit 4\n" +
				"case \"$* $GIT_SSH_COMMAND\" in *transport-test-secret*) exit 5;; esac\n"
			if tc.name == "https" {
				contents += "test \"$TRANSPORT_TOKEN\" = transport-test-secret || exit 6\n"
			}
			if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			t.Setenv("WANT_ARGS", wantArgs)
			t.Setenv("TRANSPORT_TOKEN", "transport-test-secret")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := (Native{}).LsRemote(ctx, t.TempDir(), tc.origin, "work", "demo", []string{"TRANSPORT_TOKEN"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSSHProbeExcludesProviderTokensAndPreservesAgent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed fake git is Unix-only")
	}
	bin := t.TempDir()
	script := filepath.Join(bin, "git")
	contents := `#!/bin/sh
test -z "$CUSTOM_PROVIDER_TOKEN" || exit 2
test -z "$GITHUB_TOKEN" || exit 3
test "$SSH_AUTH_SOCK" = /agent/socket || exit 4
`
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("CUSTOM_PROVIDER_TOKEN", "custom-secret")
	t.Setenv("GITHUB_TOKEN", "conventional-secret")
	t.Setenv("SSH_AUTH_SOCK", "/agent/socket")
	if err := (Native{}).LsRemote(context.Background(), t.TempDir(), "git@example.test:team/demo.git", "work", "demo", []string{"CUSTOM_PROVIDER_TOKEN", "GITHUB_TOKEN"}); err != nil {
		t.Fatal(err)
	}
}
