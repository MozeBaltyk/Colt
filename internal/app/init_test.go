package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
)

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
				destination := filepath.Join(root, "octocat", "demo")
				if err := os.Mkdir(filepath.Dir(destination), 0o755); err != nil {
					t.Fatal(err)
				}
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
				data, readErr := os.ReadFile(filepath.Join(root, "octocat", "demo", "keep.txt"))
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
		{"invalid provider", "type: bitbucket", "type must be github, gitlab, gitea or forgejo"},
		{"unsupported transport", "transport: ftp", "transport must be https or ssh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "config.yaml")
			raw := "providers:\n  work:\n    type: gitlab\n    host: gitlab.com\n    base_url: https://gitlab.com\n    namespace: team\n    visibility: private\n    git_name: Test\n    git_email: test@example.com\n    auth:\n      source: env\n    default: true\n"
			switch tc.field {
			case "git_name: ''":
				raw = strings.Replace(raw, "git_name: Test", tc.field, 1)
			case "type: forgejo":
				raw = strings.Replace(raw, "type: gitlab", tc.field, 1)
			case "type: bitbucket":
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
	if _, err := InitTransport(p, "", ""); err == nil || !strings.Contains(err.Error(), "unsupported transport") {
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
	destination := filepath.Join(root, "team", "demo")
	if err := os.MkdirAll(destination, 0o755); err != nil {
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
	wantCalls := "available,clone:https://gitlab.com/team/demo.git:" + filepath.Join(root, "team", "demo") + ",identity,commit,helper,push:https://gitlab.com/team/demo.git"
	if strings.Join(runner.calls, ",") != wantCalls || client.gets != 1 || client.creates != 1 || !strings.Contains(output, "namespace team") {
		t.Fatalf("calls=%v gets=%d creates=%d output=%q", runner.calls, client.gets, client.creates, output)
	}
	if !strings.Contains(output, filepath.Join("team", "demo")) {
		t.Fatalf("output does not preserve namespace/project path: %q", output)
	}
	position := 0
	for _, step := range []string{"Preflight", "Resolve provider API credential", "Look up remote repository", "Create remote repository", "Clone remote repository", "Set repository-local identity", "Create initial commit", "Configure HTTPS credential helper", "Push initial commit"} {
		next := strings.Index(output[position:], "✓ "+step+": succeeded")
		if next < 0 {
			t.Fatalf("ordered step %q missing from %q", step, output)
		}
		position += next + len(step)
	}
}

func TestRemoteInitDefaultsToHomeAndSupportsExplicitDestination(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want func(string) string
	}{
		{"default home", []string{"init", "demo"}, func(home string) string { return filepath.Join(home, "team", "demo") }},
		{"relative custom destination", []string{"init", "demo", "--destination", "custom/demo"}, func(home string) string { return filepath.Join(home, "custom", "demo") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if len(tc.args) > 2 {
				if err := os.Mkdir(filepath.Join(home, "custom"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			writeAppConfig(t, path, appProvider())
			runner := &fakeGit{}
			client := &fakeClient{getErr: provider.ErrNotFound, created: &provider.Repository{CloneURL: "https://gitlab.com/team/demo.git"}}
			a := &App{ConfigPath: path, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) { return client, nil }}
			if len(tc.args) > 2 {
				a.WorkDir = home // test seam for the process current directory
			}
			if _, err := execute(t, a, tc.args...); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(runner.calls, ","); !strings.Contains(got, "clone:https://gitlab.com/team/demo.git:"+tc.want(home)) {
				t.Fatalf("calls=%q", got)
			}
		})
	}
}

func TestRemoteDestinationConflictsAndPathSafety(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	writeAppConfig(t, path, appProvider())
	for _, tc := range []struct {
		name, destination, want string
		prepare                 func(*testing.T, string)
	}{
		{"local conflict", "custom/demo", "only to remote", func(*testing.T, string) {}},
		{"missing parent", "missing/demo", "inspect destination parent", func(*testing.T, string) {}},
		{"symlink parent", "custom/demo", "parent is not an ordinary directory", func(t *testing.T, root string) {
			if err := os.Symlink(t.TempDir(), filepath.Join(root, "custom")); err != nil {
				t.Fatal(err)
			}
		}},
		{"non-empty destination", "custom/demo", "non-empty", func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, "custom", "demo"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "custom", "demo", "keep"), []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink destination", "custom/demo", "not an empty ordinary directory", func(t *testing.T, root string) {
			if err := os.Mkdir(filepath.Join(root, "custom"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), filepath.Join(root, "custom", "demo")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caseRoot := t.TempDir()
			tc.prepare(t, caseRoot)
			runner := &fakeGit{}
			clientCalls := 0
			a := &App{ConfigPath: path, WorkDir: caseRoot, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
				clientCalls++
				return &fakeClient{}, nil
			}}
			args := []string{"init", "demo", "--destination", tc.destination}
			if tc.name == "local conflict" {
				args = append(args, "--local")
			}
			_, err := execute(t, a, args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) || clientCalls != 0 {
				t.Fatalf("error=%v clients=%d", err, clientCalls)
			}
		})
	}
}

func TestGitHubCanonicalOwnerCaseIsAcceptedOnlyForGitHub(t *testing.T) {
	for _, tc := range []struct {
		name, providerType, raw string
		valid                   func(string, config.Provider) (string, bool)
		want                    bool
	}{
		{"GitHub HTTPS", "github", "https://github.com/MozeBaltyk/demo.git", cleanHTTPSRepository, true},
		{"GitHub SSH", "github", "git@github.com:MozeBaltyk/demo.git", cleanSSHRepository, true},
		{"GitLab HTTPS", "gitlab", "https://gitlab.com/MozeBaltyk/demo.git", cleanHTTPSRepository, false},
		{"GitLab SSH", "gitlab", "git@gitlab.com:MozeBaltyk/demo.git", cleanSSHRepository, false},
		{"GitHub wrong host", "github", "https://evil.example/MozeBaltyk/demo.git", cleanHTTPSRepository, false},
		{"GitHub wrong repository", "github", "https://github.com/MozeBaltyk/other.git", cleanHTTPSRepository, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := appProvider()
			p.Type, p.Host, p.Namespace = tc.providerType, map[string]string{"github": "github.com", "gitlab": "gitlab.com"}[tc.providerType], "mozebaltyk"
			project, ok := tc.valid(tc.raw, p)
			if ok != tc.want || ok && project == "" {
				t.Fatalf("validation = %q, %v", project, ok)
			}
			if tc.name == "GitHub wrong repository" && project != "other" {
				t.Fatalf("project = %q", project)
			}
		})
	}

	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	p := storedProvider("personal")
	p.Namespace = "mozebaltyk"
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "test-token")
	runner := &fakeGit{}
	client := &fakeClient{getErr: provider.ErrNotFound, created: &provider.Repository{CloneURL: "https://github.com/MozeBaltyk/demo.git"}}
	if _, err := execute(t, testApp(path, root, runner, client), "init", "demo"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteInitPreservesNestedNamespaceAndRejectsSymlinkEscape(t *testing.T) {
	t.Run("nested namespace", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "config.yaml")
		p := appProvider()
		p.Namespace = "platform/tools"
		writeAppConfig(t, path, p)
		runner := &fakeGit{}
		client := &fakeClient{getErr: provider.ErrNotFound, created: &provider.Repository{CloneURL: "https://gitlab.com/platform/tools/demo.git"}}
		if _, err := execute(t, testApp(path, root, runner, client), "init", "demo"); err != nil {
			t.Fatal(err)
		}
		want := "clone:https://gitlab.com/platform/tools/demo.git:" + filepath.Join(root, "platform", "tools", "demo")
		if !strings.Contains(strings.Join(runner.calls, ","), want) {
			t.Fatalf("calls=%v, want %q", runner.calls, want)
		}
	})

	t.Run("symlink namespace", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "config.yaml")
		writeAppConfig(t, path, appProvider())
		if err := os.Symlink(t.TempDir(), filepath.Join(root, "team")); err != nil {
			t.Fatal(err)
		}
		runner := &fakeGit{}
		clientCalls := 0
		a := &App{ConfigPath: path, WorkDir: root, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
			clientCalls++
			return &fakeClient{}, nil
		}}
		if _, err := execute(t, a, "init", "demo"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("error=%v", err)
		}
		if clientCalls != 0 || strings.Join(runner.calls, ",") != "available" {
			t.Fatalf("client calls=%d Git calls=%v", clientCalls, runner.calls)
		}
	})
}

func TestGiteaRemoteWorkflowUsesAuthenticatedAccountForGitHTTPS(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	p := config.Provider{Type: "gitea", Host: "code.example", BaseURL: "https://code.example", Namespace: "team", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "env", TokenEnv: "GITEA_TEST_TOKEN"}}
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": p}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITEA_TEST_TOKEN", "gitea-test-secret")
	runner := &fakeGit{}
	client := &fakeClient{account: "alice", getErr: provider.ErrNotFound, created: &provider.Repository{CloneURL: "https://code.example/team/demo.git"}}
	if _, err := execute(t, testApp(path, root, runner, client), "init", "demo"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(runner.calls, ","); !strings.Contains(got, "clone:https://code.example/team/demo.git:"+filepath.Join(root, "team", "demo")) {
		t.Fatalf("calls=%q", got)
	}
}

func TestCORE_GIT_003RejectsUnexpectedProviderRepository(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	writeAppConfig(t, configPath, appProvider())
	runner := &fakeGit{}
	client := &fakeClient{getErr: provider.ErrNotFound, created: &provider.Repository{CloneURL: "https://gitlab.com/other/demo.git"}}
	_, err := execute(t, testApp(configPath, root, runner, client), "init", "demo")
	if err == nil || !strings.Contains(err.Error(), "partial failure at validate remote repository") || strings.Contains(strings.Join(runner.calls, ","), "origin") {
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
	want := "available,clone:git@gitlab.com:team/demo.git:" + filepath.Join(root, "team", "demo") + ",identity,commit,push:git@gitlab.com:team/demo.git"
	if strings.Join(runner.calls, ",") != want {
		t.Fatalf("calls=%v", runner.calls)
	}
}

func TestCORE_GIT_003SSHRepositoryValidationIsExact(t *testing.T) {
	p := appProvider()
	for _, tc := range []struct {
		url, project string
		want         bool
	}{
		{"git@gitlab.com:team/demo.git", "demo", true},
		{"alice@gitlab.com:team/demo.git", "", false},
		{"git@evil.example:team/demo.git", "", false},
		{"git@gitlab.com:other/demo.git", "", false},
		{"git@gitlab.com:team/other.git", "other", true},
		{"git@gitlab.com:team/demo", "", false},
		{"ssh://git@gitlab.com/team/demo.git", "demo", true},
		{"ssh://git@gitlab.com:2222/team/demo.git", "demo", true},
		{"ssh://alice@gitlab.com/team/demo.git", "", false},
		{"ssh://git@evil.example/team/demo.git", "", false},
	} {
		project, ok := cleanSSHRepository(tc.url, p)
		if ok != tc.want || ok && project != tc.project {
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
		{"repeated field tolerated", "get", "protocol=https\nhost=github.com\nhost=github.com\npath=team/demo.git\n\n", "username=x-access-token\npassword=helper-secret\n\n"},
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

func TestCloneCredentialHelperDisambiguatesSharedHostAndNamespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := appProvider()
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p, "work": p}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COLT_TEST_TOKEN", "shared-helper-secret")
	a := &App{ConfigPath: path}
	request := "protocol=https\nhost=gitlab.com\npath=team/demo.git\n\n"
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"unscoped remains ambiguous", []string{"git-credential", "get"}, false},
		{"selected repository", []string{"git-credential", "--provider", "work", "--repository", "demo", "get"}, true},
		{"wrong repository", []string{"git-credential", "--provider", "work", "--repository", "other", "get"}, false},
		{"unknown alias", []string{"git-credential", "--provider", "missing", "get"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := a.Root()
			var output bytes.Buffer
			cmd.SetIn(strings.NewReader(request))
			cmd.SetOut(&output)
			cmd.SetArgs(tc.args)
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(output.String(), "password=shared-helper-secret"); got != tc.want {
				t.Fatalf("credential returned=%t", got)
			}
		})
	}
}

func TestPutCredentialConcurrentCreateAndRollbackOwnership(t *testing.T) {
	store := &recordingStore{credentials: map[string]credential.Credential{}}
	type result struct {
		rollback func() error
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, secret := range []string{"first", "second"} {
		go func(secret string) {
			<-start
			created := credential.Credential{Kind: "bearer_token", Secret: secret}
			err := store.Create("github.com/personal", created)
			var rollback func() error
			if err == nil {
				rollback = func() error {
					_, rErr := store.DeleteIf("github.com/personal", created)
					return rErr
				}
			}
			results <- result{rollback, err}
		}(secret)
	}
	close(start)
	first, second := <-results, <-results
	var winner, loser result
	if first.err == nil {
		winner, loser = first, second
	} else {
		winner, loser = second, first
	}
	if winner.err != nil || winner.rollback == nil || !errors.Is(loser.err, credential.ErrAlreadyExists) || loser.rollback != nil {
		t.Fatalf("results: first=%v second=%v", first.err, second.err)
	}
	if _, err := store.Get("github.com/personal"); err != nil {
		t.Fatalf("losing enrollment removed winner: %v", err)
	}
	if err := winner.rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("github.com/personal"); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("winner rollback left credential: %v", err)
	}
}

func TestPutCredentialRollbackDoesNotDeleteReplacement(t *testing.T) {
	store := credential.NewMemoryStore()
	created := credential.Credential{Kind: "bearer_token", Secret: "created"}
	rollback := func() error {
		_, rErr := store.DeleteIf("github.com/personal", created)
		return rErr
	}
	err := store.Create("github.com/personal", created)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("github.com/personal", credential.Credential{Kind: "bearer_token", Secret: "replacement"}); err != nil {
		t.Fatal(err)
	}
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("github.com/personal")
	if err != nil || got.Secret != "replacement" {
		t.Fatalf("replacement was removed: credential=%+v error=%v", got, err)
	}
}

func TestGiteaAndForgejoCredentialHelperUsername(t *testing.T) {
	for _, providerType := range []string{"gitea", "forgejo"} {
		t.Run(providerType, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			p := config.Provider{Type: providerType, Host: "code.example", BaseURL: "https://code.example", Namespace: "team", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "env", TokenEnv: "HELPER_TOKEN"}}
			if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": p}}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HELPER_TOKEN", "helper-secret")
			for _, tc := range []struct{ name, username, want string }{
				{"request username", "alice", "alice"},
				{"namespace fallback", "", "team"},
				{"unsafe username", "alice\rinjected", ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					cmd := (&App{ConfigPath: path}).Root()
					var output bytes.Buffer
					cmd.SetIn(strings.NewReader("protocol=https\nhost=code.example\npath=team/demo.git\nusername=" + tc.username + "\n\n"))
					cmd.SetOut(&output)
					cmd.SetArgs([]string{"git-credential", "get"})
					want := ""
					if tc.want != "" {
						want = "username=" + tc.want + "\npassword=helper-secret\n\n"
					}
					if err := cmd.ExecuteContext(context.Background()); err != nil || output.String() != want {
						t.Fatalf("helper error=%v output=%q", err, output.String())
					}
				})
			}
		})
	}
}

func TestINIT_008KnownAndRacingConflictsAreNotAdopted(t *testing.T) {
	for _, tc := range []struct {
		name      string
		client    *fakeClient
		wantCalls string
	}{
		{"known", &fakeClient{found: &provider.Repository{CloneURL: "https://gitlab.com/team/demo.git"}}, "available"},
		{"race", &fakeClient{getErr: provider.ErrNotFound, createErr: provider.ErrConflict}, "available"},
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
	secret := "ghp_do-not-print"
	runner := &fakeGit{pushErr: errors.New("remote hung up; Authorization: Bearer " + secret)}
	client := &fakeClient{getErr: provider.ErrNotFound, created: &provider.Repository{CloneURL: "https://gitlab.com/team/demo.git"}}
	a := testApp(configPath, root, runner, client)
	output, err := execute(t, a, "init", "demo")
	if err == nil {
		t.Fatal("push failure succeeded")
	}
	if !ErrorReported(err) {
		t.Fatalf("failure was not marked as already reported: %v", err)
	}
	for _, want := range []string{"✓ Create initial commit: succeeded", "✓ Configure HTTPS credential helper: succeeded", "✗ Push initial commit: failed", "initial commit deadbeef preserved", "Remote state: created", "Cause: connectivity failure", "git push --set-upstream origin HEAD"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q missing %q", output, want)
		}
	}
	if strings.Contains(output, secret) || strings.Contains(output, "Authorization") || strings.Contains(err.Error(), secret) || !strings.Contains(strings.Join(runner.calls, ","), "clone:") {
		t.Fatalf("unsafe or incomplete partial state: %v output=%q calls=%v", err, output, runner.calls)
	}
}

func TestINIT_011ReportColorAndOrderedApplicableSteps(t *testing.T) {
	for _, tc := range []struct {
		name, noColor  string
		terminal, ansi bool
	}{
		{name: "TTY", terminal: true, ansi: true},
		{name: "TTY NO_COLOR", terminal: true, noColor: "1"},
		{name: "non-TTY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.yaml")
			writeAppConfig(t, path, appProvider())
			t.Setenv("NO_COLOR", tc.noColor)
			if tc.name != "TTY NO_COLOR" {
				os.Unsetenv("NO_COLOR")
			}
			a := testApp(path, root, &fakeGit{}, &fakeClient{})
			a.IsOutputTerminal = func(io.Writer) bool { return tc.terminal }
			output, err := execute(t, a, "init", "demo", "--local")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output, "\x1b[") != tc.ansi {
				t.Fatalf("ANSI=%t output=%q", tc.ansi, output)
			}
			plain := strings.NewReplacer("\x1b[32m", "", "\x1b[0m", "").Replace(output)
			want := "Initialize demo\n" +
				"  ✓ Preflight: succeeded\n" +
				"  ✓ Initialize local repository: succeeded\n" +
				"  ✓ Set repository-local identity: succeeded\n" +
				"  ✓ Create initial commit: succeeded\n" +
				"  Provider: work · namespace team\n" +
				"  Local state: initial commit deadbeef preserved at " + filepath.Join(root, "demo") + "\n" +
				"  Remote state: not applicable\n"
			if plain != want || strings.Contains(plain, "Clone") || strings.Contains(plain, "Push") {
				t.Fatalf("output=%q want=%q", plain, want)
			}
		})
	}
}

func TestINIT_011CauseClassification(t *testing.T) {
	for _, tc := range []struct{ evidence, cause, advice, transport, step string }{
		{"git-credential-colt: not found", "Git credential helper unavailable", "Colt executable", "https", "Configure HTTPS credential helper"},
		{"Permission denied (publickey)", "SSH public-key authentication denied", "SSH key", "ssh", "Push initial commit"},
		{"Host key verification failed", "SSH host-key verification failed", "known_hosts", "ssh", "Push initial commit"},
		{"HTTP 401: Bad credentials", "provider authentication rejected", "re-authenticate", "https", "Authenticate provider API"},
		{"fatal: unable to access: Could not resolve host", "connectivity failure", "connectivity", "https", "Push initial commit"},
		{"fatal: server certificate verification failed. CAfile: none", "TLS certificate verification failed", "CA bundle", "https", "Clone remote repository"},
		{"push rejected for an unspecified reason", "unknown failure", "preserved state", "", "Push initial commit"},
	} {
		cause, advice := initCause(errors.New(tc.evidence), tc.transport, tc.step)
		if cause != tc.cause || !strings.Contains(advice, tc.advice) {
			t.Fatalf("evidence=%q cause=%q advice=%q", tc.evidence, cause, advice)
		}
	}
	cause, advice := initCause(errors.New("absolute transient helper returned exit 1"), "https", "Push initial commit")
	if cause != "unknown failure" || strings.Contains(strings.ToLower(advice), "path") {
		t.Fatalf("unsupported PATH claim: cause=%q advice=%q", cause, advice)
	}
}

func TestINIT_011CloneFailureMarksLaterStepsSkipped(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	writeAppConfig(t, path, appProvider())
	secret := "secret-clone-evidence"
	runner := &fakeGit{cloneErr: errors.New("Permission denied (publickey); Authorization: " + secret)}
	client := &fakeClient{getErr: provider.ErrNotFound, created: &provider.Repository{CloneURL: "https://gitlab.com/team/demo.git"}}
	output, err := execute(t, testApp(path, root, runner, client), "init", "demo")
	if err == nil {
		t.Fatal("clone failure succeeded")
	}
	for _, want := range []string{
		"✗ Clone remote repository: failed",
		"! Set repository-local identity: skipped",
		"! Create initial commit: skipped",
		"! Configure HTTPS credential helper: skipped",
		"! Push initial commit: skipped",
		"Cause: SSH public-key authentication denied",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q missing %q", output, want)
		}
	}
	if strings.Contains(output, secret) || strings.Contains(output, "Authorization") || strings.Contains(err.Error(), secret) {
		t.Fatalf("secret leaked: error=%v output=%q", err, output)
	}
}

func TestINIT_010VisibilityOverrideIsRequestLocal(t *testing.T) {
	for _, tc := range []struct{ configured, override string }{{"private", "public"}, {"public", "private"}, {"public", ""}} {
		t.Run(tc.configured+"/"+tc.override, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.yaml")
			p := appProvider()
			p.Default, p.Visibility = true, tc.configured
			writeAppConfig(t, path, p)
			before, _ := os.ReadFile(path)
			client := &fakeClient{getErr: provider.ErrNotFound, created: &provider.Repository{CloneURL: "https://gitlab.com/team/demo.git"}}
			args := []string{"init", "demo"}
			if tc.override != "" {
				args = append(args, "--visibility", tc.override)
			}
			_, err := execute(t, testApp(path, root, &fakeGit{}, client), args...)
			after, _ := os.ReadFile(path)
			want := tc.override
			if want == "" {
				want = tc.configured
			}
			if err != nil || client.visibility != want || string(after) != string(before) {
				t.Fatalf("error=%v visibility=%q config changed=%v", err, client.visibility, string(after) != string(before))
			}
		})
	}
}

func TestINIT_010InvalidVisibilityFailsBeforeAccessOrMutation(t *testing.T) {
	for _, args := range [][]string{{"init", "demo", "--visibility", "internal"}, {"init", "demo", "--local", "--visibility", "public"}} {
		runner := &fakeGit{}
		store := &recordingStore{credentials: map[string]credential.Credential{}}
		clients := 0
		a := &App{ConfigPath: filepath.Join(t.TempDir(), "missing.yaml"), Git: runner, Credentials: store, NewClient: func(config.Provider, string) (provider.Client, error) {
			clients++
			return &fakeClient{}, nil
		}}
		if _, err := execute(t, a, args...); err == nil || len(runner.calls) != 0 || store.gets != 0 || clients != 0 {
			t.Fatalf("args=%v error=%v git=%v store gets=%d clients=%d", args, err, runner.calls, store.gets, clients)
		}
	}
}
