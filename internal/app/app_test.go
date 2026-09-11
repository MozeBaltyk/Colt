package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MozeBaltyk/Colt/internal/config"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
)

func appProvider() config.Provider {
	return config.Provider{Type: "gitlab", Host: "gitlab.com", BaseURL: "https://gitlab.com", Namespace: "team", Visibility: "private", GitName: "Colt Tester", GitEmail: "colt@example.com", TokenEnv: "COLT_TEST_TOKEN"}
}

func writeAppConfig(t *testing.T, path string, p config.Provider) {
	t.Helper()
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": p}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COLT_TEST_TOKEN", "test-secret-token")
}

func execute(t *testing.T, a *App, args ...string) (string, error) {
	t.Helper()
	cmd := a.Root()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return output.String(), err
}

func TestAuthHelpUsesLoginAndStatus(t *testing.T) {
	a := &App{}
	authHelp, err := execute(t, a, "auth", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"login", "status", "Configuration is stored in the OS user configuration directory",
		"Show configured providers", "$XDG_CONFIG_HOME/colt/config.yaml",
		"~/.config/colt/config.yaml", "COLT_CONFIG overrides the complete path",
	} {
		if !strings.Contains(authHelp, want) {
			t.Fatalf("auth help missing %q:\n%s", want, authHelp)
		}
	}
	if strings.Contains(authHelp, "os.UserConfigDir") {
		t.Fatalf("auth help exposes implementation detail:\n%s", authHelp)
	}
	for _, old := range []string{"\n  add ", "\n  list "} {
		if strings.Contains(authHelp, old) {
			t.Fatalf("auth help advertises old command %q:\n%s", old, authHelp)
		}
	}

	loginHelp, err := execute(t, a, "auth", "login", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"colt auth login <github|gitlab> <alias>",
		"repository owner: GitHub username or organization; GitLab group or subgroup/full path",
	} {
		if !strings.Contains(loginHelp, want) {
			t.Fatalf("login help missing %q:\n%s", want, loginHelp)
		}
	}

	statusHelp, err := execute(t, a, "auth", "status", "--help")
	if err != nil || !strings.Contains(statusHelp, "colt auth status") {
		t.Fatalf("status help = %q, %v", statusHelp, err)
	}
	for _, old := range []string{"add", "list"} {
		if _, err := execute(t, a, "auth", old); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("old auth command %q error = %v", old, err)
		}
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestINIT_001_002_004_005CORE_IDENTITY_001LocalInitWithRealGit(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "config.yaml")
	writeAppConfig(t, configPath, appProvider())
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("GIT_AUTHOR_NAME", "Wrong Global Author")
	t.Setenv("GIT_AUTHOR_EMAIL", "wrong@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Wrong Global Committer")
	t.Setenv("GIT_COMMITTER_EMAIL", "wrong@example.com")
	a := &App{ConfigPath: configPath, WorkDir: root, Git: gitnative.Native{}, NewClient: func(config.Provider, string) (provider.Client, error) {
		t.Fatal("local init called provider client")
		return nil, nil
	}}
	output, err := execute(t, a, "init", "demo", "--local")
	if err != nil {
		t.Fatalf("init error = %v; output=%s", err, output)
	}
	dir := filepath.Join(root, "demo")
	if got := gitOutput(t, dir, "rev-list", "--count", "HEAD"); got != "1" {
		t.Fatalf("commit count = %q", got)
	}
	if got := gitOutput(t, dir, "config", "--local", "user.name"); got != "Colt Tester" {
		t.Fatalf("user.name = %q", got)
	}
	if got := gitOutput(t, dir, "config", "--local", "user.email"); got != "colt@example.com" {
		t.Fatalf("user.email = %q", got)
	}
	if got := gitOutput(t, dir, "log", "-1", "--format=%an <%ae>|%cn <%ce>"); got != "Colt Tester <colt@example.com>|Colt Tester <colt@example.com>" {
		t.Fatalf("initial commit identity = %q", got)
	}
	if got := gitOutput(t, dir, "remote"); got != "" {
		t.Fatalf("remotes = %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".gitconfig")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("global git config was created: %v", err)
	}
}

func TestINIT_003CORE_SAFETY_001RejectsNonEmptyDestination(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	writeAppConfig(t, configPath, appProvider())
	destination := filepath.Join(root, "demo")
	os.Mkdir(destination, 0o755)
	userFile := filepath.Join(destination, "keep.txt")
	os.WriteFile(userFile, []byte("keep"), 0o600)
	runner := &fakeGit{}
	a := testApp(configPath, root, runner, &fakeClient{})
	_, err := execute(t, a, "init", "demo", "--local")
	data, _ := os.ReadFile(userFile)
	if err == nil || string(data) != "keep" || len(runner.calls) != 1 || runner.calls[0] != "available" {
		t.Fatalf("err=%v data=%q calls=%v", err, data, runner.calls)
	}
}

func TestINIT_001CORE_GIT_001GitUnavailableBeforeMutation(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	writeAppConfig(t, configPath, appProvider())
	runner := &fakeGit{availableErr: errors.New("native git is unavailable; install git")}
	a := testApp(configPath, root, runner, &fakeClient{})
	_, err := execute(t, a, "init", "demo", "--local")
	if err == nil || !strings.Contains(err.Error(), "install git") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "demo")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination mutated: %v", statErr)
	}
}

func TestCORE_CREDENTIAL_003MissingTokenFailsBeforeRequestOrMutation(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	p := appProvider()
	p.TokenEnv = "COLT_DEFINITELY_MISSING_TOKEN"
	if err := config.Save(configPath, config.Config{Providers: map[string]config.Provider{"work": p}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(p.TokenEnv, "")
	runner := &fakeGit{}
	clientCalls := 0
	a := &App{ConfigPath: configPath, WorkDir: root, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
		clientCalls++
		return &fakeClient{}, nil
	}}
	_, err := execute(t, a, "init", "demo")
	if err == nil || !strings.Contains(err.Error(), p.TokenEnv) || clientCalls != 0 || len(runner.calls) != 0 {
		t.Fatalf("error=%v client calls=%d git calls=%v", err, clientCalls, runner.calls)
	}
	if _, statErr := os.Stat(filepath.Join(root, "demo")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination mutated: %v", statErr)
	}
}

func TestINIT_006_007RemoteWorkflow(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	writeAppConfig(t, configPath, appProvider())
	runner := &fakeGit{}
	client := &fakeClient{getErr: provider.ErrNotFound, created: &provider.Repository{CloneURL: "https://gitlab.com/team/demo.git"}}
	a := testApp(configPath, root, runner, client)
	output, err := execute(t, a, "init", "demo")
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := "available,init,identity,commit,origin:https://gitlab.com/team/demo.git,push:https://gitlab.com/team/demo.git"
	if strings.Join(runner.calls, ",") != wantCalls || client.gets != 1 || client.creates != 1 || !strings.Contains(output, "namespace team") {
		t.Fatalf("calls=%v gets=%d creates=%d output=%q", runner.calls, client.gets, client.creates, output)
	}
	if runner.pushType != "gitlab" || runner.pushToken != "test-secret-token" {
		t.Fatal("push did not receive selected provider type and process credential")
	}
}

func TestINIT_008KnownAndRacingConflictsAreNotAdopted(t *testing.T) {
	for _, tc := range []struct {
		name      string
		client    *fakeClient
		wantCalls string
	}{
		{"known", &fakeClient{found: &provider.Repository{CloneURL: "https://gitlab.com/team/demo.git"}}, "available"},
		{"race", &fakeClient{getErr: provider.ErrNotFound, createErr: provider.ErrConflict}, "available,init,identity,commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "config.yaml")
			writeAppConfig(t, configPath, appProvider())
			runner := &fakeGit{}
			a := testApp(configPath, root, runner, tc.client)
			_, err := execute(t, a, "init", "demo")
			if err == nil || strings.Join(runner.calls, ",") != tc.wantCalls || strings.Contains(strings.Join(runner.calls, ","), "origin") {
				t.Fatalf("error=%v calls=%v", err, runner.calls)
			}
			if tc.name == "race" && (!strings.Contains(err.Error(), "authoritative") || !strings.Contains(err.Error(), "local state preserved")) {
				t.Fatalf("race error is not actionable: %v", err)
			}
		})
	}
}

func TestINIT_009CORE_FAILURE_001PushFailurePreservesAndReportsState(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	writeAppConfig(t, configPath, appProvider())
	runner := &fakeGit{pushErr: errors.New("push denied")}
	client := &fakeClient{getErr: provider.ErrNotFound, created: &provider.Repository{CloneURL: "https://gitlab.com/team/demo.git"}}
	a := testApp(configPath, root, runner, client)
	_, err := execute(t, a, "init", "demo")
	if err == nil {
		t.Fatal("push failure succeeded")
	}
	message := err.Error()
	for _, want := range []string{"partial failure at push", "local state preserved", "remote state: created", "git push --set-upstream origin HEAD", "push denied"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q missing %q", message, want)
		}
	}
	if strings.Contains(message, "test-secret-token") || !strings.Contains(strings.Join(runner.calls, ","), "origin:") {
		t.Fatalf("unsafe or incomplete partial state: %v calls=%v", err, runner.calls)
	}
}

func TestCORE_PROVIDER_002AuthReplacementFailurePreservesConfig(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	personal := appProvider()
	personal.Default = true
	work := appProvider()
	work.Namespace = "old-team"
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": personal, "work": work}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COLT_TEST_TOKEN", "test-secret-token")
	before, _ := os.ReadFile(path)
	a := &App{ConfigPath: path, Git: &fakeGit{}, NewClient: func(config.Provider, string) (provider.Client, error) {
		return &fakeClient{authErr: errors.New("authentication failed")}, nil
	}}
	_, err := execute(t, a, "auth", "login", "gitlab", "work", "--replace", "--default", "--host", "new.example", "--base-url", "https://new.example", "--namespace", "new-team", "--git-name", "New", "--git-email", "new@example.com", "--token-env", "COLT_TEST_TOKEN")
	after, _ := os.ReadFile(path)
	if err == nil || !strings.Contains(err.Error(), "authentication failed") || string(before) != string(after) {
		t.Fatalf("error=%v prior config changed", err)
	}
}

func TestCORE_PROVIDER_002DefaultMovesAtomicallyIncludingReplace(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "login", true: "replace"}[replace], func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.yaml")
			personal := appProvider()
			personal.Default = true
			providers := map[string]config.Provider{"personal": personal}
			if replace {
				providers["work"] = appProvider()
			}
			if err := config.Save(path, config.Config{Providers: providers}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("COLT_TEST_TOKEN", "test-secret-token")
			a := &App{ConfigPath: path, Git: &fakeGit{}, NewClient: func(config.Provider, string) (provider.Client, error) {
				return &fakeClient{account: "alice"}, nil
			}}
			args := []string{"auth", "login", "gitlab", "work", "--default", "--namespace", "new-team", "--git-name", "New", "--git-email", "new@example.com", "--token-env", "COLT_TEST_TOKEN"}
			if replace {
				args = append(args, "--replace")
			}
			if _, err := execute(t, a, args...); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(path)
			if err != nil || cfg.Providers["personal"].Default || !cfg.Providers["work"].Default {
				t.Fatalf("defaults after update = %#v, %v", cfg.Providers, err)
			}
		})
	}
}

func TestCORE_PROVIDER_001_002AuthLoginNoninteractiveAndNoTokenPersistence(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	t.Setenv("CUSTOM_TOKEN", "never-persist-this-token")
	a := &App{ConfigPath: path, Git: &fakeGit{}, NewClient: func(config.Provider, string) (provider.Client, error) {
		return &fakeClient{account: "alice"}, nil
	}}
	output, err := execute(t, a, "auth", "login", "gitlab", "work", "--host", "gitlab.example", "--base-url", "https://gitlab.example", "--namespace", "platform", "--visibility", "internal", "--git-name", "Alice", "--git-email", "alice@example.com", "--token-env", "CUSTOM_TOKEN", "--default")
	if err != nil || !strings.Contains(output, "authenticated as alice") {
		t.Fatalf("error=%v output=%q", err, output)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "never-persist-this-token") || !strings.Contains(string(data), "token_env: CUSTOM_TOKEN") {
		t.Fatalf("unsafe config: %s", data)
	}
	if _, err := execute(t, a, "auth", "login", "gitlab", "work", "--host", "gitlab.example", "--base-url", "https://gitlab.example", "--namespace", "platform", "--git-name", "Alice", "--git-email", "alice@example.com", "--token-env", "CUSTOM_TOKEN"); err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("duplicate alias error = %v", err)
	}
}

func TestCORE_PROVIDER_005CORE_CREDENTIAL_002ProviderStatusOfflineAndUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	personal := config.Provider{Type: "github", Host: "github.com", BaseURL: "https://api.github.com", Namespace: "octocat", Visibility: "public", GitName: "Private Name", GitEmail: "private@example.com", TokenEnv: "UNSET_PERSONAL_TOKEN", Default: true}
	work := appProvider()
	work.Host, work.BaseURL, work.Namespace, work.Visibility = "gitlab.example", "https://gitlab.example/private-api", "platform/team", "internal"
	work.GitName, work.GitEmail, work.TokenEnv = "Work Secret", "work-secret@example.com", "UNSET_WORK_TOKEN"
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": work, "personal": personal}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(personal.TokenEnv, "")
	t.Setenv(work.TokenEnv, "")
	before, _ := os.ReadFile(path)
	runner := &fakeGit{}
	a := &App{ConfigPath: path, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
		t.Fatal("provider client called in offline mode")
		return nil, nil
	}}
	output, err := execute(t, a, "auth", "status", "--offline")
	after, _ := os.ReadFile(path)
	want := "personal (default)\n  GitHub · github.com\n  Namespace:   octocat\n  Git name:    Private Name\n  Git email:   private@example.com\n  Connection:  not checked\nwork\n  GitLab · gitlab.example\n  Namespace:   platform/team\n  Git name:    Work Secret\n  Git email:   work-secret@example.com\n  Connection:  not checked\n"
	if err != nil || output != want || len(runner.calls) != 0 || !bytes.Equal(before, after) {
		t.Fatalf("error=%v output=%q git calls=%v config changed=%t", err, output, runner.calls, !bytes.Equal(before, after))
	}
	for _, omitted := range []string{"UNSET_PERSONAL_TOKEN", "UNSET_WORK_TOKEN", "private-api", "public", "internal"} {
		if strings.Contains(output, omitted) {
			t.Fatalf("output %q disclosed %q", output, omitted)
		}
	}
}

func TestCORE_PROVIDER_005AuthStatusOnlineShowsAccountAndConnection(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	p := appProvider()
	p.Default = true
	if err := config.Save(configPath, config.Config{Providers: map[string]config.Provider{"work": p}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COLT_TEST_TOKEN", "test-secret-token")
	a := &App{ConfigPath: configPath, WorkDir: root, Git: &fakeGit{}, NewClient: func(config.Provider, string) (provider.Client, error) {
		return &fakeClient{account: "alice"}, nil
	}}
	output, err := execute(t, a, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	want := "work (default)\n  GitLab · gitlab.com\n  Account:     alice\n  Namespace:   team\n  Git name:    Colt Tester\n  Git email:   colt@example.com\n  Connection:  ✓ connected\n"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

func TestCORE_PROVIDER_005AuthStatusCredentialsMissing(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	p := appProvider()
	if err := config.Save(configPath, config.Config{Providers: map[string]config.Provider{"work": p}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COLT_TEST_TOKEN", "")
	a := &App{ConfigPath: configPath, WorkDir: root, Git: &fakeGit{}, NewClient: func(config.Provider, string) (provider.Client, error) {
		return &fakeClient{account: "alice"}, nil
	}}
	output, err := execute(t, a, "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	want := "work\n  GitLab · gitlab.com\n  Connection:  ✗ credentials missing\n"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

func TestCORE_PROVIDER_005StatusWithEmptyOrMissingConfig(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "empty"}[empty], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if empty {
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			output, err := execute(t, &App{ConfigPath: path}, "auth", "status")
			if err != nil || output != "no providers configured\n" {
				t.Fatalf("error=%v output=%q", err, output)
			}
		})
	}
}

func TestCORE_PROVIDER_005StatusRespectsPathError(t *testing.T) {
	want := errors.New("config path unavailable")
	_, err := execute(t, &App{pathErr: want}, "auth", "status")
	if !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
}

type fakeGit struct {
	calls        []string
	availableErr error
	pushErr      error
	pushType     string
	pushToken    string
}

func (g *fakeGit) Available() error                   { g.calls = append(g.calls, "available"); return g.availableErr }
func (g *fakeGit) Init(context.Context, string) error { g.calls = append(g.calls, "init"); return nil }
func (g *fakeGit) SetIdentity(context.Context, string, string, string) error {
	g.calls = append(g.calls, "identity")
	return nil
}
func (g *fakeGit) Commit(context.Context, string) (string, error) {
	g.calls = append(g.calls, "commit")
	return "deadbeef", nil
}
func (g *fakeGit) AddOrigin(_ context.Context, _, url string) error {
	g.calls = append(g.calls, "origin:"+url)
	return nil
}
func (g *fakeGit) Push(_ context.Context, _, url, providerType, token string) error {
	g.calls = append(g.calls, "push:"+url)
	g.pushType = providerType
	g.pushToken = token
	return g.pushErr
}

type fakeClient struct {
	account         string
	authErr, getErr error
	createErr       error
	found, created  *provider.Repository
	gets, creates   int
}

func (f *fakeClient) Authenticate(context.Context) (string, error) { return f.account, f.authErr }
func (f *fakeClient) Get(context.Context, string) (*provider.Repository, error) {
	f.gets++
	return f.found, f.getErr
}
func (f *fakeClient) Create(context.Context, string) (*provider.Repository, error) {
	f.creates++
	return f.created, f.createErr
}

func testApp(path, workDir string, runner *fakeGit, client *fakeClient) *App {
	return &App{ConfigPath: path, WorkDir: workDir, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) { return client, nil }}
}
