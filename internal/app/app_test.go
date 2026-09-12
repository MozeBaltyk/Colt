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
	"github.com/MozeBaltyk/Colt/internal/credential"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
)

func appProvider() config.Provider {
	return config.Provider{Type: "gitlab", Host: "gitlab.com", BaseURL: "https://gitlab.com", Namespace: "team", Visibility: "private", GitName: "Colt Tester", GitEmail: "colt@example.com", Auth: config.Auth{Source: "env", TokenEnv: "COLT_TEST_TOKEN"}}
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

func TestAuthHelpUsesLoginLogoutAndStatus(t *testing.T) {
	a := &App{}
	authHelp, err := execute(t, a, "auth", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"login", "logout", "status", "Configuration is stored in the OS user configuration directory",
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

type recordingStore struct {
	credentials map[string]credential.Credential
	gets        int
	puts        int
	deletes     []string
	deleteErr   error
}

func (s *recordingStore) Get(string) (credential.Credential, error) {
	s.gets++
	return credential.Credential{}, errors.New("unexpected credential read")
}
func (s *recordingStore) Put(string, credential.Credential) error {
	s.puts++
	return errors.New("unexpected credential write")
}
func (s *recordingStore) Delete(id string) error {
	s.deletes = append(s.deletes, id)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if _, ok := s.credentials[id]; !ok {
		return credential.ErrNotFound
	}
	delete(s.credentials, id)
	return nil
}

func storedProvider(alias string) config.Provider {
	p := config.Provider{Type: "github", Host: "github.com", BaseURL: "https://api.github.com", Namespace: "octocat", Visibility: "private", GitName: "Test", GitEmail: "test@example.com"}
	p.Auth = config.Auth{Source: "stored", CredentialID: p.Host + "/" + alias}
	return p
}

func TestCORE_PROVIDER_008LogoutDeletesExactStoredIDOffline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := storedProvider("personal")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	store := &recordingStore{credentials: map[string]credential.Credential{
		"github.com/personal": {Kind: "bearer_token", Secret: "selected-secret"},
		"github.com/work":     {Kind: "bearer_token", Secret: "neighbor-secret"},
	}}
	t.Setenv("GITHUB_TOKEN", "environment-secret")
	runner := &fakeGit{}
	clientCalls := 0
	a := &App{ConfigPath: path, Credentials: store, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
		clientCalls++
		return nil, errors.New("offline")
	}}
	output, err := execute(t, a, "auth", "logout", "personal")
	after, _ := os.ReadFile(path)
	if err != nil || strings.Join(store.deletes, ",") != "github.com/personal" || len(store.credentials) != 1 || store.credentials["github.com/work"].Secret != "neighbor-secret" {
		t.Fatalf("error=%v deletes=%v credentials=%v", err, store.deletes, store.credentials)
	}
	if clientCalls != 0 || len(runner.calls) != 0 || !bytes.Equal(before, after) {
		t.Fatalf("client calls=%d git calls=%v config changed=%t", clientCalls, runner.calls, !bytes.Equal(before, after))
	}
	if !strings.Contains(output, "removed stored credential github.com/personal") || !strings.Contains(output, "GITHUB_TOKEN may still provide credentials") || strings.Contains(output, "environment-secret") {
		t.Fatalf("unsafe or unclear output: %q", output)
	}
}

func TestCORE_PROVIDER_008LogoutEnvironmentAndMissingStoredAreNoOps(t *testing.T) {
	t.Run("environment", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		p := appProvider()
		p.Auth.TokenEnv = "LOGOUT_TOKEN"
		if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": p}}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LOGOUT_TOKEN", "do-not-print")
		store := &recordingStore{credentials: map[string]credential.Credential{}}
		output, err := execute(t, &App{ConfigPath: path, Credentials: store}, "auth", "logout", "work")
		if err != nil || len(store.deletes) != 0 || os.Getenv("LOGOUT_TOKEN") != "do-not-print" || !strings.Contains(output, "LOGOUT_TOKEN may still provide credentials") || strings.Contains(output, "do-not-print") {
			t.Fatalf("error=%v deletes=%v output=%q", err, store.deletes, output)
		}
	})
	t.Run("missing stored credential", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": storedProvider("personal")}}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GITHUB_TOKEN", "")
		store := &recordingStore{credentials: map[string]credential.Credential{}}
		output, err := execute(t, &App{ConfigPath: path, Credentials: store}, "auth", "logout", "personal")
		if err != nil || strings.Join(store.deletes, ",") != "github.com/personal" || !strings.Contains(output, "nothing changed") || !strings.Contains(output, "GITHUB_TOKEN may still provide credentials") {
			t.Fatalf("error=%v deletes=%v output=%q", err, store.deletes, output)
		}
	})
}

func TestCORE_PROVIDER_008LogoutInvalidConfigOrAliasDoesNotMutate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, string)
		alias string
		want  string
	}{
		{"unknown alias", func(t *testing.T, path string) {
			if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": storedProvider("personal")}}); err != nil {
				t.Fatal(err)
			}
		}, "work", "unknown provider alias"},
		{"malformed config", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("providers: [\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "personal", "parse config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			tc.setup(t, path)
			store := &recordingStore{credentials: map[string]credential.Credential{"github.com/personal": {Kind: "bearer_token", Secret: "keep"}}}
			runner := &fakeGit{}
			clientCalls := 0
			a := &App{ConfigPath: path, Credentials: store, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
				clientCalls++
				return nil, errors.New("unexpected provider call")
			}}
			_, err := execute(t, a, "auth", "logout", tc.alias)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
			if store.gets != 0 || store.puts != 0 || len(store.deletes) != 0 || len(store.credentials) != 1 || clientCalls != 0 || len(runner.calls) != 0 {
				t.Fatalf("mutation after validation failure: store=%#v clients=%d git=%v", store, clientCalls, runner.calls)
			}
		})
	}
}

func TestCORE_PROVIDER_008LogoutRejectsUnsafeOperations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": storedProvider("personal")}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"unavailable", credential.ErrStoreUnavailable, "storage unavailable"},
		{"unsafe", errors.New("backend detail containing secret-value"), "storage failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := execute(t, &App{ConfigPath: path, Credentials: &recordingStore{deleteErr: tc.err}}, "auth", "logout", "personal")
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("error=%v", err)
			}
		})
	}
	store := &recordingStore{credentials: map[string]credential.Credential{"github.com/personal": {Kind: "bearer_token", Secret: "keep"}}}
	_, err := execute(t, &App{ConfigPath: path, Credentials: store}, "auth", "logout", "personal", "--revoke")
	if err == nil || !strings.Contains(err.Error(), "--revoke is not supported") || len(store.deletes) != 0 {
		t.Fatalf("revoke error=%v deletes=%v", err, store.deletes)
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

func TestINIT_005LocalInitDoesNotReadProviderCredentialOrConstructClient(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	p := storedProvider("work")
	if err := config.Save(configPath, config.Config{Providers: map[string]config.Provider{"work": p}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(configPath)
	store := &recordingStore{credentials: map[string]credential.Credential{}}
	runner := &fakeGit{}
	clientCalls := 0
	a := &App{ConfigPath: configPath, WorkDir: root, Credentials: store, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
		clientCalls++
		return nil, errors.New("unexpected provider client")
	}}
	if _, err := execute(t, a, "init", "demo", "--local"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(configPath)
	if store.gets != 0 || store.puts != 0 || len(store.deletes) != 0 || clientCalls != 0 {
		t.Fatalf("credential/provider activity: store=%#v clients=%d", store, clientCalls)
	}
	if got := strings.Join(runner.calls, ","); got != "available,init,identity,commit" {
		t.Fatalf("Git calls = %q", got)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("configuration changed")
	}
}

func TestINIT_001RuntimeWorkdirAndDestinationPreflightDoNotReadCredentials(t *testing.T) {
	for _, name := range []string{"runtime", "workdir", "destination"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "config.yaml")
			p := storedProvider("work")
			if err := config.Save(configPath, config.Config{Providers: map[string]config.Provider{"work": p}}); err != nil {
				t.Fatal(err)
			}
			beforeConfig, _ := os.ReadFile(configPath)
			store := &recordingStore{credentials: map[string]credential.Credential{}}
			runner := &fakeGit{}
			workDir := root
			if name == "runtime" {
				runner.availableErr = errors.New("native git unavailable")
			}
			if name == "workdir" {
				workDir = filepath.Join(root, "missing")
			}
			if name == "destination" {
				destination := filepath.Join(root, "demo")
				if err := os.Mkdir(destination, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(destination, "keep.txt"), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			clientCalls := 0
			a := &App{ConfigPath: configPath, WorkDir: workDir, Credentials: store, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
				clientCalls++
				return &fakeClient{}, nil
			}}
			_, err := execute(t, a, "init", "demo")
			afterConfig, _ := os.ReadFile(configPath)
			if err == nil || store.gets != 0 || store.puts != 0 || len(store.deletes) != 0 || clientCalls != 0 || strings.Join(runner.calls, ",") != "available" || !bytes.Equal(beforeConfig, afterConfig) {
				t.Fatalf("error=%v store=%#v clients=%d git=%v config changed=%t", err, store, clientCalls, runner.calls, !bytes.Equal(beforeConfig, afterConfig))
			}
			if name == "destination" {
				data, readErr := os.ReadFile(filepath.Join(root, "demo", "keep.txt"))
				if readErr != nil || string(data) != "keep" {
					t.Fatalf("destination changed: data=%q error=%v", data, readErr)
				}
			} else if _, statErr := os.Lstat(filepath.Join(workDir, "demo")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("destination changed: %v", statErr)
			}
		})
	}
}

func TestINIT_001MalformedSelectedProviderFailsBeforePreflightEffects(t *testing.T) {
	for _, tc := range []struct {
		name, field, want string
	}{
		{"missing identity", "git_name: ''", "git_name is required"},
		{"invalid provider", "type: gitea", "type must be github or gitlab"},
		{"unsupported transport", "transport: ftp", "transport must be https or ssh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "config.yaml")
			raw := "providers:\n  work:\n    type: gitlab\n    host: gitlab.com\n    base_url: https://gitlab.com\n    namespace: team\n    visibility: private\n    git_name: Test\n    git_email: test@example.com\n    auth:\n      source: env\n    default: true\n"
			switch tc.field {
			case "git_name: ''":
				raw = strings.Replace(raw, "git_name: Test", tc.field, 1)
			case "type: gitea":
				raw = strings.Replace(raw, "type: gitlab", tc.field, 1)
			case "transport: ftp":
				raw = strings.Replace(raw, "git_email: test@example.com", "git_email: test@example.com\n    "+tc.field, 1)
			}
			if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(configPath)
			store := &recordingStore{credentials: map[string]credential.Credential{}}
			runner := &fakeGit{}
			clientCalls := 0
			a := &App{ConfigPath: configPath, WorkDir: root, Credentials: store, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
				clientCalls++
				return &fakeClient{}, nil
			}}
			_, err := execute(t, a, "init", "demo")
			after, _ := os.ReadFile(configPath)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if len(runner.calls) != 0 || clientCalls != 0 || store.gets != 0 || store.puts != 0 || len(store.deletes) != 0 || !bytes.Equal(before, after) {
				t.Fatalf("preflight effects: git=%v clients=%d store=%#v config changed=%t", runner.calls, clientCalls, store, !bytes.Equal(before, after))
			}
			if _, statErr := os.Lstat(filepath.Join(root, "demo")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("destination changed: %v", statErr)
			}
		})
	}
}

func TestINIT_007ProgrammaticUnsupportedTransportIsRejected(t *testing.T) {
	p := appProvider()
	p.Transport = "ftp"
	if _, err := initTransport(p); err == nil || !strings.Contains(err.Error(), "unsupported transport") {
		t.Fatalf("error = %v", err)
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

func TestINIT_001RemoteDestinationFailureSkipsProviderClientAndMutation(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	writeAppConfig(t, configPath, appProvider())
	destination := filepath.Join(root, "demo")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(destination, "keep.txt")
	if err := os.WriteFile(userFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeConfig, _ := os.ReadFile(configPath)
	runner := &fakeGit{}
	clientCalls := 0
	a := &App{ConfigPath: configPath, WorkDir: root, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
		clientCalls++
		return &fakeClient{}, nil
	}}
	_, err := execute(t, a, "init", "demo")
	afterConfig, _ := os.ReadFile(configPath)
	data, _ := os.ReadFile(userFile)
	if err == nil || clientCalls != 0 || strings.Join(runner.calls, ",") != "available" || string(data) != "keep" || !bytes.Equal(beforeConfig, afterConfig) {
		t.Fatalf("error=%v clients=%d git=%v data=%q config changed=%t", err, clientCalls, runner.calls, data, !bytes.Equal(beforeConfig, afterConfig))
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
	p.Auth.TokenEnv = "COLT_DEFINITELY_MISSING_TOKEN"
	if err := config.Save(configPath, config.Config{Providers: map[string]config.Provider{"work": p}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(p.Auth.TokenEnv, "")
	runner := &fakeGit{}
	clientCalls := 0
	a := &App{ConfigPath: configPath, WorkDir: root, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
		clientCalls++
		return &fakeClient{}, nil
	}}
	_, err := execute(t, a, "init", "demo")
	if err == nil || !strings.Contains(err.Error(), p.Auth.TokenEnv) || clientCalls != 0 || strings.Join(runner.calls, ",") != "available" {
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
	wantCalls := "available,init,identity,commit,origin:https://gitlab.com/team/demo.git,helper,push:https://gitlab.com/team/demo.git"
	if strings.Join(runner.calls, ",") != wantCalls || client.gets != 1 || client.creates != 1 || !strings.Contains(output, "namespace team") {
		t.Fatalf("calls=%v gets=%d creates=%d output=%q", runner.calls, client.gets, client.creates, output)
	}
	if runner.pushType != "gitlab" || runner.pushToken != "test-secret-token" {
		t.Fatal("push did not receive selected provider type and process credential")
	}
}

func TestCORE_GIT_003RejectsUnexpectedProviderRepository(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	writeAppConfig(t, configPath, appProvider())
	runner := &fakeGit{}
	client := &fakeClient{getErr: provider.ErrNotFound, created: &provider.Repository{CloneURL: "https://gitlab.com/other/demo.git"}}
	_, err := execute(t, testApp(configPath, root, runner, client), "init", "demo")
	if err == nil || !strings.Contains(err.Error(), "different authority or repository") || strings.Contains(strings.Join(runner.calls, ","), "origin") {
		t.Fatalf("error=%v calls=%v", err, runner.calls)
	}
}

func TestINIT_007SSHRemoteWorkflowUsesNoHTTPAuthentication(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	p := appProvider()
	p.Transport = "ssh"
	writeAppConfig(t, configPath, p)
	runner := &fakeGit{}
	client := &fakeClient{getErr: provider.ErrNotFound, created: &provider.Repository{
		CloneURL: "https://gitlab.com/team/demo.git",
		SSHURL:   "git@gitlab.com:team/demo.git",
	}}
	_, err := execute(t, testApp(configPath, root, runner, client), "init", "demo")
	if err != nil {
		t.Fatal(err)
	}
	want := "available,init,identity,commit,origin:git@gitlab.com:team/demo.git,push:git@gitlab.com:team/demo.git"
	if strings.Join(runner.calls, ",") != want || runner.pushToken != "" {
		t.Fatalf("calls=%v push token=%q", runner.calls, runner.pushToken)
	}
}

func TestCORE_GIT_003SSHRepositoryValidationIsExact(t *testing.T) {
	p := appProvider()
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"git@gitlab.com:team/demo.git", true},
		{"alice@gitlab.com:team/demo.git", false},
		{"git@evil.example:team/demo.git", false},
		{"git@gitlab.com:other/demo.git", false},
		{"git@gitlab.com:team/other.git", true},
		{"git@gitlab.com:team/demo", false},
		{"ssh://git@gitlab.com/team/demo.git", false},
	} {
		project, ok := cleanSSHRepository(tc.url, p)
		if ok != tc.want || ok && project != strings.TrimSuffix(strings.TrimPrefix(tc.url, "git@gitlab.com:team/"), ".git") {
			t.Fatalf("cleanSSHRepository(%q) = %q, %v", tc.url, project, ok)
		}
	}
}

func TestCORE_GIT_003InvalidSelectedSSHURLFailsWithCreatedState(t *testing.T) {
	for _, sshURL := range []string{
		"",
		"alice@gitlab.com:team/demo.git",
		"git@evil.example:team/demo.git",
		"git@gitlab.com:other/demo.git",
		"git@gitlab.com:team/other.git",
	} {
		t.Run(sshURL, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.yaml")
			p := appProvider()
			p.Transport = "ssh"
			writeAppConfig(t, path, p)
			runner := &fakeGit{}
			client := &fakeClient{getErr: provider.ErrNotFound, created: &provider.Repository{CloneURL: "https://gitlab.com/team/demo.git", SSHURL: sshURL}}
			_, err := execute(t, testApp(path, root, runner, client), "init", "demo")
			if err == nil || !strings.Contains(err.Error(), "remote state: created") || strings.Contains(strings.Join(runner.calls, ","), "origin:") {
				t.Fatalf("error=%v calls=%v", err, runner.calls)
			}
		})
	}
}

func TestCORE_GIT_005_006_007CredentialHelperProtocol(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := config.Provider{Type: "github", Host: "github.com", BaseURL: "https://api.github.com", Namespace: "team", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "env", TokenEnv: "COLT_HELPER_TOKEN"}}
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COLT_HELPER_TOKEN", "helper-secret")
	a := &App{ConfigPath: path}
	for _, tc := range []struct {
		name, operation, input, want string
	}{
		{"match", "get", "protocol=https\nhost=github.com\npath=team/demo.git\n\n", "username=x-access-token\npassword=helper-secret\n\n"},
		{"wrong protocol", "get", "protocol=http\nhost=github.com\npath=team/demo.git\n\n", ""},
		{"wrong host", "get", "protocol=https\nhost=evil.example\npath=team/demo.git\npassword=do-not-log\n\n", ""},
		{"wrong repository", "get", "protocol=https\nhost=github.com\npath=other/demo.git\n\n", ""},
		{"store no-op", "store", "protocol=https\nhost=github.com\npath=team/demo.git\npassword=unknown-secret\n\n", ""},
		{"erase no-op", "erase", "protocol=https\nhost=github.com\npath=team/demo.git\n\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := a.Root()
			var stdout bytes.Buffer
			cmd.SetIn(strings.NewReader(tc.input))
			cmd.SetOut(&stdout)
			cmd.SetErr(&stdout)
			cmd.SetArgs([]string{"git-credential", tc.operation})
			err := cmd.ExecuteContext(context.Background())
			if err != nil || stdout.String() != tc.want {
				t.Fatalf("error=%v stdout=%q", err, stdout.String())
			}
		})
	}
	cmd := a.Root()
	var stdout bytes.Buffer
	cmd.SetIn(strings.NewReader("protocol=https\nhost=github.com\npath=team/demo.git\n\n"))
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"git-credential", "get"})
	if err := cmd.ExecuteContext(context.Background()); err != nil || !strings.Contains(stdout.String(), "password=helper-secret") {
		t.Fatalf("credential changed after store/erase: error=%v stdout=%q", err, stdout.String())
	}
	cmd = a.Root()
	cmd.SetIn(strings.NewReader("password=diagnostic-secret\nmalformed\n"))
	cmd.SetArgs([]string{"git-credential", "get"})
	if err := cmd.ExecuteContext(context.Background()); err == nil || strings.Contains(err.Error(), "diagnostic-secret") {
		t.Fatalf("malformed request diagnostic leaked a secret: %v", err)
	}
	for _, secret := range []string{"secret\nusername=attacker", "secret\rpassword=attacker"} {
		t.Setenv("COLT_HELPER_TOKEN", secret)
		cmd = a.Root()
		stdout.Reset()
		cmd.SetIn(strings.NewReader("protocol=https\nhost=github.com\npath=team/demo.git\n\n"))
		cmd.SetOut(&stdout)
		cmd.SetArgs([]string{"git-credential", "get"})
		if err := cmd.ExecuteContext(context.Background()); err != nil || stdout.Len() != 0 {
			t.Fatalf("unsafe resolved credential produced protocol output: error=%v stdout=%q", err, stdout.String())
		}
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
	output, err := execute(t, a, "auth", "login", "gitlab", "work", "--host", "gitlab.example", "--base-url", "https://gitlab.example", "--namespace", "platform", "--visibility", "internal", "--git-name", "Alice", "--git-email", "alice@example.com", "--token-env", "CUSTOM_TOKEN", "--transport", "ssh", "--default")
	if err != nil || !strings.Contains(output, "authenticated as alice") {
		t.Fatalf("error=%v output=%q", err, output)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "never-persist-this-token") || !strings.Contains(string(data), "token_env: CUSTOM_TOKEN") || !strings.Contains(string(data), "transport: ssh") {
		t.Fatalf("unsafe config: %s", data)
	}
	if _, err := execute(t, a, "auth", "login", "gitlab", "work", "--host", "gitlab.example", "--base-url", "https://gitlab.example", "--namespace", "platform", "--git-name", "Alice", "--git-email", "alice@example.com", "--token-env", "CUSTOM_TOKEN"); err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("duplicate alias error = %v", err)
	}
}

func TestCORE_PROVIDER_005CORE_CREDENTIAL_002ProviderStatusOfflineAndUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	personal := config.Provider{Type: "github", Host: "github.com", BaseURL: "https://api.github.com", Namespace: "octocat", Visibility: "public", GitName: "Private Name", GitEmail: "private@example.com", Auth: config.Auth{Source: "env", TokenEnv: "UNSET_PERSONAL_TOKEN"}, Default: true}
	work := appProvider()
	work.Host, work.BaseURL, work.Namespace, work.Visibility = "gitlab.example", "https://gitlab.example/private-api", "platform/team", "internal"
	work.GitName, work.GitEmail, work.Auth.TokenEnv = "Work Secret", "work-secret@example.com", "UNSET_WORK_TOKEN"
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": work, "personal": personal}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(personal.Auth.TokenEnv, "")
	t.Setenv(work.Auth.TokenEnv, "")
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

func TestStoredCredentialResolvesFromSiblingFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.yaml")
	p := config.Provider{Type: "github", Host: "github.com", BaseURL: "https://api.github.com", Namespace: "octocat", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "stored", CredentialID: "github.com/personal"}}
	if err := config.Save(configPath, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	store := credential.NewFileStore(credential.DefaultFilePath(configPath))
	if err := store.Put(p.Auth.CredentialID, credential.Credential{Kind: "bearer_token", Secret: "stored-test-secret"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "")
	before, _ := os.ReadFile(configPath)
	gotToken := ""
	a := &App{ConfigPath: configPath, Credentials: store, NewClient: func(_ config.Provider, token string) (provider.Client, error) {
		gotToken = token
		return &fakeClient{account: "octocat"}, nil
	}}
	output, err := execute(t, a, "auth", "status")
	after, _ := os.ReadFile(configPath)
	if err != nil || gotToken != "stored-test-secret" || !strings.Contains(output, "connected") || strings.Contains(output, "stored-test-secret") || !bytes.Equal(before, after) {
		t.Fatalf("error=%v token=%q output=%q config changed=%t", err, gotToken, output, !bytes.Equal(before, after))
	}
	cmd := a.Root()
	var helperOut bytes.Buffer
	cmd.SetIn(strings.NewReader("protocol=https\nhost=github.com\npath=octocat/demo.git\n\n"))
	cmd.SetOut(&helperOut)
	cmd.SetArgs([]string{"git-credential", "get"})
	if err := cmd.ExecuteContext(context.Background()); err != nil || helperOut.String() != "username=x-access-token\npassword=stored-test-secret\n\n" {
		t.Fatalf("stored helper error=%v output=%q", err, helperOut.String())
	}
}

func TestProductionDefaultDoesNotEnablePlaintextFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.yaml")
	t.Setenv("COLT_CONFIG", configPath)
	t.Setenv("GITHUB_TOKEN", "")
	p := config.Provider{Type: "github", Host: "github.com", BaseURL: "https://api.github.com", Namespace: "octocat", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "stored", CredentialID: "github.com/personal"}}
	if err := config.Save(configPath, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	if err := credential.NewFileStore(credential.DefaultFilePath(configPath)).Put(p.Auth.CredentialID, credential.Credential{Kind: "bearer_token", Secret: "must-not-be-read"}); err != nil {
		t.Fatal(err)
	}
	clientCalls := 0
	a := New()
	if _, ok := a.Credentials.(credential.DisabledStore); !ok {
		t.Fatalf("production credential store = %T, want disabled", a.Credentials)
	}
	a.NewClient = func(config.Provider, string) (provider.Client, error) {
		clientCalls++
		return &fakeClient{}, nil
	}
	output, err := execute(t, a, "auth", "status")
	if err != nil || clientCalls != 0 || !strings.Contains(output, "credential storage failure") || strings.Contains(output, "credentials missing") {
		t.Fatalf("error=%v client calls=%d output=%q", err, clientCalls, output)
	}
}

func TestLiveStatusDistinguishesUnsafeStorageFromMissingCredentials(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.yaml")
	t.Setenv("GITHUB_TOKEN", "")
	p := config.Provider{Type: "github", Host: "github.com", BaseURL: "https://api.github.com", Namespace: "octocat", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "stored", CredentialID: "github.com/personal"}}
	if err := config.Save(configPath, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	store := credential.NewFileStore(credential.DefaultFilePath(configPath))
	if err := store.Put(p.Auth.CredentialID, credential.Credential{Kind: "bearer_token", Secret: "unsafe-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(credential.DefaultFilePath(configPath), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := execute(t, &App{ConfigPath: configPath, Credentials: store}, "auth", "status")
	if err != nil || !strings.Contains(output, "credential storage failure") || strings.Contains(output, "credentials missing") || strings.Contains(output, "unsafe-secret") {
		t.Fatalf("error=%v output=%q", err, output)
	}
}

type countingStore struct{ gets int }

func (s *countingStore) Get(string) (credential.Credential, error) {
	s.gets++
	return credential.Credential{}, credential.ErrNotFound
}
func (*countingStore) Put(string, credential.Credential) error { return nil }
func (*countingStore) Delete(string) error                     { return nil }

func TestOfflineStatusDoesNotReadCredentialStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := config.Provider{Type: "github", Host: "github.com", BaseURL: "https://api.github.com", Namespace: "octocat", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "stored", CredentialID: "github.com/personal"}}
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	store := &countingStore{}
	output, err := execute(t, &App{ConfigPath: path, Credentials: store}, "auth", "status", "--offline")
	if err != nil || store.gets != 0 || !strings.Contains(output, "not checked") {
		t.Fatalf("error=%v store reads=%d output=%q", err, store.gets, output)
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
func (g *fakeGit) ConfigureCredentialHelper(context.Context, string) error {
	g.calls = append(g.calls, "helper")
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
