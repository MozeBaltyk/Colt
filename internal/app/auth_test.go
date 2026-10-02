package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	"github.com/MozeBaltyk/Colt/internal/provider"
)

func TestInvalidExplicitLoginInputPrecedesPromptsAndMissingInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"provider", []string{"auth", "login", "bitbucket"}},
		{"empty provider", []string{"auth", "login", ""}},
		{"empty alias", []string{"auth", "login", "github", ""}},
		{"visibility", []string{"auth", "login", "github", "personal", "--visibility", "internal"}},
		{"transport", []string{"auth", "login", "github", "personal", "--transport", "ftp"}},
		{"credential", []string{"auth", "login", "github", "personal", "--credential", "file"}},
		{"token environment", []string{"auth", "login", "github", "personal", "--token-env", "BAD-NAME"}},
		{"namespace", []string{"auth", "login", "github", "personal", "--namespace", "../bad"}},
		{"host", []string{"auth", "login", "github", "personal", "--host", "https://github.com"}},
		{"base URL", []string{"auth", "login", "github", "personal", "--base-url", "http://api.github.com"}},
		{"git email", []string{"auth", "login", "github", "personal", "--git-email", "not-an-email"}},
	}
	for _, tc := range tests {
		for _, noninteractive := range []bool{false, true} {
			name := map[bool]string{false: "interactive", true: "noninteractive"}[noninteractive]
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "config.yaml")
				store := &recordingStore{credentials: map[string]credential.Credential{}}
				clients := 0
				a := &App{ConfigPath: path, Credentials: store, IsTerminal: func() bool { return true }, NewClient: func(config.Provider, string) (provider.Client, error) {
					clients++
					return &fakeClient{}, nil
				}}
				args := append([]string(nil), tc.args...)
				if noninteractive {
					args = append([]string{"--noninteractive"}, args...)
				}
				output, err := executeInput(t, a, "replacement\nvalues\n", args...)
				if err == nil || strings.Contains(err.Error(), "missing required input") || output != "" || clients != 0 || store.gets != 0 || store.puts != 0 {
					t.Fatalf("error=%v output=%q clients=%d store=%#v", err, output, clients, store)
				}
				if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("config mutated: %v", statErr)
				}
			})
		}
	}
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
		"colt auth login <github|gitlab|gitea|forgejo> <alias>",
		"repository owner: GitHub/Gitea/Forgejo user or organization; GitLab group or subgroup/full path",
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
}

func TestCORE_PROVIDER_009LogoutRevokeOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		revokeErr   error
		deleteErr   error
		wantErr     bool
		wantRevokes int
		remoteMsg   string
		localMsg    string
		localGone   bool
	}{
		{"supported", nil, nil, false, 1, "✓ remote credential revoked", "removed stored credential github.com/personal", true},
		{"failed", errors.New("remote revocation failed"), nil, true, 1, "! remote revocation failed", "removed stored credential github.com/personal", true},
		{"unsupported", provider.ErrRevocationUnsupported, nil, false, 1, "provider-side revocation unsupported", "removed stored credential github.com/personal", true},
		{"local delete failure", nil, errors.New("credential delete failed"), true, 1, "✓ remote credential revoked", "! local credential removal failed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": storedProvider("personal")}}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GITHUB_TOKEN", "")
			store := &recordingStore{credentials: map[string]credential.Credential{
				"github.com/personal": {Kind: "bearer_token", Secret: "revoke-test-secret"},
			}, deleteErr: tc.deleteErr}
			client := &fakeClient{revokeErr: tc.revokeErr}
			a := &App{ConfigPath: path, Credentials: store, NewClient: func(config.Provider, string) (provider.Client, error) {
				return client, nil
			}}
			output, err := execute(t, a, "auth", "logout", "personal", "--revoke")
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%v output=%q", err, tc.wantErr, output)
			}
			if client.revokes != tc.wantRevokes {
				t.Fatalf("revokes=%d want=%d", client.revokes, tc.wantRevokes)
			}
			for _, want := range []string{tc.remoteMsg, tc.localMsg} {
				if want != "" && !strings.Contains(output, want) {
					t.Fatalf("output=%q missing %q", output, want)
				}
			}
			if strings.Contains(output, "revoke-test-secret") {
				t.Fatalf("output leaked the credential: %q", output)
			}
			_, remainingErr := store.Get("github.com/personal")
			if tc.localGone {
				if !errors.Is(remainingErr, credential.ErrNotFound) {
					t.Fatalf("credential not removed: %v", remainingErr)
				}
			} else if remainingErr != nil {
				t.Fatalf("credential unexpectedly removed: %v", remainingErr)
			}
		})
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

func TestManualLoginAuthenticatesBeforeSecurePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("GITHUB_TOKEN", "")
	store := &recordingStore{credentials: map[string]credential.Credential{}}
	authenticated := false
	a := &App{
		ConfigPath:  path,
		Credentials: store,
		IsTerminal:  func() bool { return true },
		ReadToken:   func() (string, error) { return "manual-secret", nil },
		NewClient: func(p config.Provider, token string) (provider.Client, error) {
			if p.Auth.Source != "stored" || token != "manual-secret" || store.puts != 0 {
				t.Fatalf("provider=%+v token=%q puts=%d", p, token, store.puts)
			}
			authenticated = true
			return &fakeClient{account: "octocat"}, nil
		},
	}
	output, err := execute(t, a, "auth", "login", "github", "personal", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
	if err != nil || !authenticated || store.puts != 1 || store.credentials["github.com/personal"].Secret != "manual-secret" || strings.Contains(output, "manual-secret") {
		t.Fatalf("error=%v authenticated=%t store=%#v output=%q", err, authenticated, store, output)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.Providers["personal"].Auth != (config.Auth{Source: "stored", CredentialID: "github.com/personal"}) {
		t.Fatalf("config=%+v error=%v", cfg, err)
	}
}

func TestManualLoginFailureAndNonTTYDoNotPersist(t *testing.T) {
	args := []string{"auth", "login", "github", "personal", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com"}
	t.Run("authentication failure", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		t.Setenv("GITHUB_TOKEN", "")
		store := &recordingStore{credentials: map[string]credential.Credential{}}
		a := &App{ConfigPath: path, Credentials: store, IsTerminal: func() bool { return true }, ReadToken: func() (string, error) { return "manual-secret", nil }, NewClient: func(config.Provider, string) (provider.Client, error) {
			return &fakeClient{authErr: errors.New("authentication failed")}, nil
		}}
		if _, err := execute(t, a, args...); err == nil || store.puts != 0 {
			t.Fatalf("error=%v store=%#v", err, store)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("config created: %v", err)
		}
	})
	t.Run("non tty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		t.Setenv("GITHUB_TOKEN", "")
		clientCalls := 0
		a := &App{ConfigPath: path, Credentials: credential.NewMemoryStore(), NewClient: func(config.Provider, string) (provider.Client, error) {
			clientCalls++
			return &fakeClient{}, nil
		}}
		if _, err := execute(t, a, args...); err == nil || !strings.Contains(err.Error(), "interactive terminal") || clientCalls != 0 {
			t.Fatalf("error=%v client calls=%d", err, clientCalls)
		}
	})
	t.Run("manual token CRLF", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		t.Setenv("GITHUB_TOKEN", "")
		clientCalls := 0
		store := &recordingStore{credentials: map[string]credential.Credential{}}
		a := &App{ConfigPath: path, Credentials: store, IsTerminal: func() bool { return true }, ReadToken: func() (string, error) { return "bad\nsecret", nil }, NewClient: func(config.Provider, string) (provider.Client, error) {
			clientCalls++
			return &fakeClient{}, nil
		}}
		if _, err := execute(t, a, args...); err == nil || !strings.Contains(err.Error(), "no CR or LF") || clientCalls != 0 || store.puts != 0 {
			t.Fatalf("error=%v clients=%d store=%#v", err, clientCalls, store)
		}
	})
}

func TestExplicitTokenEnvMissingNeverPromptsInteractively(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("EXPLICIT_TOKEN", "")
	prompts, confirmations, clientCalls := 0, 0, 0
	store := &recordingStore{credentials: map[string]credential.Credential{}}
	a := &App{
		ConfigPath: path, Credentials: store,
		ReadToken:        func() (string, error) { prompts++; return "manual-secret", nil },
		ConfirmPlaintext: func() (bool, error) { confirmations++; return true, nil },
		NewClient:        func(config.Provider, string) (provider.Client, error) { clientCalls++; return &fakeClient{}, nil },
	}
	_, err := execute(t, a, "auth", "login", "github", "personal", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com", "--token-env", "EXPLICIT_TOKEN")
	if err == nil || !strings.Contains(err.Error(), "EXPLICIT_TOKEN is missing or empty") || prompts != 0 || confirmations != 0 || clientCalls != 0 || store.gets != 0 || store.puts != 0 {
		t.Fatalf("error=%v prompts=%d confirmations=%d clients=%d store=%#v", err, prompts, confirmations, clientCalls, store)
	}
}

func TestManualLoginRefusesCredentialOverwriteAndUnsafeReplacement(t *testing.T) {
	t.Run("explicit environment replacement never inherits stored auth", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": storedProvider("personal")}}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GITHUB_TOKEN", "environment-secret")
		store := &recordingStore{credentials: map[string]credential.Credential{"github.com/personal": {Kind: "bearer_token", Secret: "stored-secret"}}}
		a := &App{ConfigPath: path, Credentials: store, NewClient: func(p config.Provider, token string) (provider.Client, error) {
			if p.Auth != (config.Auth{Source: "env"}) || token != "environment-secret" {
				t.Fatalf("provider=%+v token=%q", p, token)
			}
			return &fakeClient{account: "octocat"}, nil
		}}
		_, err := execute(t, a, "auth", "login", "github", "personal", "--replace", "--credential", "env", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
		cfg, loadErr := config.Load(path)
		if err != nil || loadErr != nil || cfg.Providers["personal"].Auth != (config.Auth{Source: "env"}) || store.gets != 0 || store.puts != 0 || store.credentials["github.com/personal"].Secret != "stored-secret" {
			t.Fatalf("error=%v load=%v config=%+v store=%#v", err, loadErr, cfg, store)
		}
	})

	t.Run("missing explicit environment replacement does not fall back to stored", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": storedProvider("personal")}}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GITHUB_TOKEN", "")
		store := &recordingStore{credentials: map[string]credential.Credential{"github.com/personal": {Kind: "bearer_token", Secret: "stored-secret"}}}
		clients := 0
		a := &App{ConfigPath: path, Credentials: store, NewClient: func(config.Provider, string) (provider.Client, error) {
			clients++
			return &fakeClient{}, nil
		}}
		_, err := execute(t, a, "auth", "login", "github", "personal", "--replace", "--credential", "env", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
		cfg, loadErr := config.Load(path)
		if err == nil || loadErr != nil || cfg.Providers["personal"].Auth.Source != "stored" || clients != 0 || store.gets != 0 || store.puts != 0 {
			t.Fatalf("error=%v load=%v config=%+v clients=%d store=%#v", err, loadErr, cfg, clients, store)
		}
	})

	t.Run("pre-existing target", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		t.Setenv("GITHUB_TOKEN", "")
		store := &recordingStore{credentials: map[string]credential.Credential{"github.com/personal": {Kind: "bearer_token", Secret: "keep"}}}
		a := &App{ConfigPath: path, Credentials: store, IsTerminal: func() bool { return true }, ReadToken: func() (string, error) { return "new-secret", nil }, NewClient: func(config.Provider, string) (provider.Client, error) {
			return &fakeClient{account: "octocat"}, nil
		}}
		_, err := execute(t, a, "auth", "login", "github", "personal", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
		if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") || store.credentials["github.com/personal"].Secret != "keep" || store.puts != 0 {
			t.Fatalf("error=%v store=%#v", err, store)
		}
	})

	t.Run("pre-existing fallback target", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Chmod(root, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "config.yaml")
		secure := credential.NewMemoryStore()
		fallback := credential.NewFileStore(credential.DefaultFilePath(path))
		if err := fallback.Put("github.com/personal", credential.Credential{Kind: "bearer_token", Secret: "keep"}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GITHUB_TOKEN", "")
		a := &App{ConfigPath: path, Credentials: secure, FallbackCredentials: fallback, IsTerminal: func() bool { return true }, ReadToken: func() (string, error) { return "new-secret", nil }, NewClient: func(config.Provider, string) (provider.Client, error) {
			return &fakeClient{account: "octocat"}, nil
		}}
		_, err := execute(t, a, "auth", "login", "github", "personal", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
		got, getErr := fallback.Get("github.com/personal")
		if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") || getErr != nil || got.Secret != "keep" {
			t.Fatalf("error=%v fallback=%+v get=%v", err, got, getErr)
		}
		if _, secureErr := secure.Get("github.com/personal"); !errors.Is(secureErr, credential.ErrNotFound) {
			t.Fatalf("secure target created: %v", secureErr)
		}
	})

	t.Run("environment-backed replacement cannot create stored state", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": appProvider()}}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GITHUB_TOKEN", "")
		t.Setenv("COLT_TEST_TOKEN", "")
		prompts := 0
		a := &App{ConfigPath: path, Credentials: credential.NewMemoryStore(), ReadToken: func() (string, error) { prompts++; return "new", nil }}
		_, err := execute(t, a, "auth", "login", "github", "personal", "--replace", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
		if err == nil || !strings.Contains(err.Error(), "not allowed with --replace") || prompts != 0 {
			t.Fatalf("error=%v prompts=%d", err, prompts)
		}
	})

	t.Run("stored replacement cannot change reference", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		old := config.Provider{Type: "gitlab", Host: "old.example", BaseURL: "https://old.example", Namespace: "team", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "stored", CredentialID: "old.example/work"}}
		if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": old}}); err != nil {
			t.Fatal(err)
		}
		a := &App{ConfigPath: path, Credentials: credential.NewMemoryStore()}
		_, err := execute(t, a, "auth", "login", "gitlab", "work", "--replace", "--host", "new.example", "--base-url", "https://new.example", "--namespace", "team", "--git-name", "Test", "--git-email", "test@example.com")
		if err == nil || !strings.Contains(err.Error(), "would orphan") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("same stored reference is reused without overwrite", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": storedProvider("personal")}}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GITHUB_TOKEN", "")
		store := &recordingStore{credentials: map[string]credential.Credential{"github.com/personal": {Kind: "bearer_token", Secret: "old-secret"}}}
		a := &App{ConfigPath: path, Credentials: store, ReadToken: func() (string, error) { t.Fatal("manual prompt called"); return "", nil }, NewClient: func(_ config.Provider, token string) (provider.Client, error) {
			if token != "old-secret" {
				t.Fatalf("token=%q", token)
			}
			return &fakeClient{account: "octocat"}, nil
		}}
		_, err := execute(t, a, "auth", "login", "github", "personal", "--replace", "--namespace", "new-namespace", "--git-name", "Test", "--git-email", "test@example.com")
		cfg, loadErr := config.Load(path)
		if err != nil || loadErr != nil || cfg.Providers["personal"].Auth.CredentialID != "github.com/personal" || cfg.Providers["personal"].Namespace != "new-namespace" || store.puts != 0 || len(store.deletes) != 0 {
			t.Fatalf("error=%v load=%v config=%+v store=%#v", err, loadErr, cfg, store)
		}
	})
}

func TestManualLoginPlaintextRequiresConsentAndRollsBackOnConfigFailure(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "accepted"}[accepted], func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "config.yaml")
			fallback := credential.NewFileStore(credential.DefaultFilePath(path))
			t.Setenv("GITHUB_TOKEN", "")
			a := &App{ConfigPath: path, Credentials: credential.DisabledStore{}, FallbackCredentials: fallback,
				IsTerminal: func() bool { return true },
				ReadToken:  func() (string, error) { return "manual-secret", nil }, ConfirmPlaintext: func() (bool, error) { return accepted, nil },
				NewClient: func(config.Provider, string) (provider.Client, error) { return &fakeClient{account: "octocat"}, nil }}
			output, err := execute(t, a, "auth", "login", "github", "personal", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
			if accepted {
				got, getErr := fallback.Get("github.com/personal")
				if err != nil || getErr != nil || got.Secret != "manual-secret" || !strings.Contains(output, "plaintext protected only") {
					t.Fatalf("error=%v get=%v output=%q", err, getErr, output)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "not persisted") {
					t.Fatalf("error=%v", err)
				}
				if _, statErr := os.Stat(fallback.Path); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("fallback created: %v", statErr)
				}
			}
		})
	}

	t.Run("config save rollback", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "config.yaml")
		t.Setenv("GITHUB_TOKEN", "")
		store := &recordingStore{credentials: map[string]credential.Credential{}}
		a := &App{ConfigPath: path, Credentials: store, IsTerminal: func() bool { return true }, ReadToken: func() (string, error) { return "manual-secret", nil }, NewClient: func(config.Provider, string) (provider.Client, error) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			return &fakeClient{account: "octocat"}, nil
		}}
		_, err := execute(t, a, "auth", "login", "github", "personal", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
		if err == nil || !strings.Contains(err.Error(), "config was not changed") || len(store.credentials) != 0 || store.puts != 1 || len(store.deletes) != 1 {
			t.Fatalf("error=%v store=%#v", err, store)
		}
	})

	t.Run("same-reference replacement save failure does not mutate credential", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": storedProvider("personal")}}); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(path)
		t.Setenv("GITHUB_TOKEN", "")
		store := &recordingStore{credentials: map[string]credential.Credential{"github.com/personal": {Kind: "bearer_token", Secret: "old-secret"}}}
		a := &App{
			ConfigPath: path, Credentials: store,
			ReadToken:  func() (string, error) { t.Fatal("manual prompt called"); return "", nil },
			NewClient:  func(config.Provider, string) (provider.Client, error) { return &fakeClient{account: "octocat"}, nil },
			SaveConfig: func(string, config.Config) error { return errors.New("save failed") },
		}
		_, err := execute(t, a, "auth", "login", "github", "personal", "--replace", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
		after, _ := os.ReadFile(path)
		if err == nil || store.credentials["github.com/personal"].Secret != "old-secret" || store.puts != 0 || !bytes.Equal(before, after) {
			t.Fatalf("error=%v store=%#v config changed=%t", err, store, !bytes.Equal(before, after))
		}
	})
}

func TestManualLoginReportsUncertainPersistenceWithoutConfigMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("GITHUB_TOKEN", "")
	store := &recordingStore{credentials: map[string]credential.Credential{}, putErr: credential.ErrPersistenceUncertain, deleteErr: errors.New("delete uncertain")}
	a := &App{ConfigPath: path, Credentials: store, IsTerminal: func() bool { return true }, ReadToken: func() (string, error) { return "manual-secret", nil }, NewClient: func(config.Provider, string) (provider.Client, error) {
		return &fakeClient{account: "octocat"}, nil
	}}
	_, err := execute(t, a, "auth", "login", "github", "personal", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
	if err == nil || !strings.Contains(err.Error(), "persistence outcome is uncertain") || !strings.Contains(err.Error(), "github.com/personal") {
		t.Fatalf("error=%v", err)
	}
	if len(store.deletes) != 0 {
		t.Fatalf("uncertain create triggered unsafe rollback: deletes=%v", store.deletes)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("config changed: %v", statErr)
	}
}

func TestGiteaLoginUsesHostDefaultsAndConventionalToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("GITEA_TOKEN", "gitea-login-secret")
	var gotProvider config.Provider
	var gotToken string
	a := &App{ConfigPath: path, NewClient: func(p config.Provider, token string) (provider.Client, error) {
		gotProvider, gotToken = p, token
		return &fakeClient{account: "alice"}, nil
	}}
	_, err := execute(t, a, "auth", "login", "gitea", "work", "--host", "code.example.com", "--namespace", "team", "--visibility", "public", "--git-name", "Alice", "--git-email", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if gotProvider.Type != "gitea" || gotProvider.BaseURL != "https://code.example.com" || gotProvider.Visibility != "public" || gotToken != "gitea-login-secret" {
		t.Fatalf("provider=%+v token selected=%t", gotProvider, gotToken == "gitea-login-secret")
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("Load() = %v", err)
	}
}

func TestForgejoLoginUsesHostDefaultsAndConventionalToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("FORGEJO_TOKEN", "forgejo-login-secret")
	var gotProvider config.Provider
	var gotToken string
	a := &App{ConfigPath: path, NewClient: func(p config.Provider, token string) (provider.Client, error) {
		gotProvider, gotToken = p, token
		return &fakeClient{account: "alice"}, nil
	}}
	_, err := execute(t, a, "auth", "login", "forgejo", "work", "--host", "code.example.com", "--namespace", "team", "--visibility", "public", "--git-name", "Alice", "--git-email", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if gotProvider.Type != "forgejo" || gotProvider.BaseURL != "https://code.example.com" || gotToken != "forgejo-login-secret" {
		t.Fatalf("provider=%+v token selected=%t", gotProvider, gotToken == "forgejo-login-secret")
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
	want := "personal (default)\n  GitHub · github.com\n  Namespace:   octocat\n  Auth source: env\n  Git name:    Private Name\n  Git email:   private@example.com\n  Connection:  not checked\n  Transport:   not checked\nwork\n  GitLab · gitlab.example\n  Namespace:   platform/team\n  Auth source: env\n  Git name:    Work Secret\n  Git email:   work-secret@example.com\n  Connection:  not checked\n  Transport:   not checked\n"
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
	want := "work (default)\n  GitLab · gitlab.com\n  Credential:  environment\n  Account:     alice\n  Namespace:   team\n  Git name:    Colt Tester\n  Git email:   colt@example.com\n  Connection:  ✓ connected\n  Transport:   not checked\n"
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

func TestProductionDefaultReadsConsentCreatedPlaintextFallback(t *testing.T) {
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
	if _, ok := a.Credentials.(*credential.SecureStore); !ok || a.FallbackCredentials == nil {
		t.Fatalf("production credential stores = %T, %v", a.Credentials, a.FallbackCredentials)
	}
	// Do not touch the developer/CI OS keyring from a unit test.
	a.Credentials = credential.DisabledStore{}
	a.NewClient = func(_ config.Provider, token string) (provider.Client, error) {
		clientCalls++
		if token != "must-not-be-read" {
			t.Fatalf("fallback token not resolved")
		}
		return &fakeClient{}, nil
	}
	output, err := execute(t, a, "auth", "status")
	if err != nil || clientCalls != 1 || !strings.Contains(output, "connected") || strings.Contains(output, "must-not-be-read") {
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
	want := "work\n  GitLab · gitlab.com\n  Connection:  ✗ credentials missing\n  Transport:   not checked\n"
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

func TestCredentialStoredReadsEnvTokenAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := config.Provider{Type: "github", Host: "github.com", BaseURL: "https://api.github.com", Namespace: "octocat", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "env"}}
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "stored-env-token")
	store := credential.NewMemoryStore()
	a := &App{ConfigPath: path, Credentials: store, NewClient: func(_ config.Provider, token string) (provider.Client, error) {
		if token != "stored-env-token" {
			t.Fatalf("token=%q", token)
		}
		return &fakeClient{account: "octocat"}, nil
	}}
	output, err := execute(t, a, "auth", "login", "github", "personal", "--credential", "stored", "--replace", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
	if err != nil {
		t.Fatalf("error=%v", err)
	}
	cred, getErr := store.Get("github.com/personal")
	if getErr != nil || cred.Secret != "stored-env-token" {
		t.Fatalf("credential not persisted: getErr=%v secret=%q", getErr, cred.Secret)
	}
	if !strings.Contains(output, "octocat") {
		t.Fatalf("output=%q", output)
	}
}

func TestCredentialStoredUnsetExplicitTokenEnvUsesDeviceFlow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := config.Provider{Type: "github", Host: "github.com", BaseURL: "https://api.github.com", Namespace: "octocat", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "env"}}
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("GITHUB_TOKEN")
	store := &recordingStore{credentials: map[string]credential.Credential{}}
	a := &App{
		ConfigPath: path, Credentials: store, IsTerminal: func() bool { return true },
		AuthorizeGitHubDevice: func(_ context.Context, out io.Writer) (string, error) {
			fmt.Fprint(out, "Open: https://github.com/login/device\nCode: TEST-CODE\n")
			return "device-token", nil
		},
		NewClient: func(_ config.Provider, token string) (provider.Client, error) {
			if token != "device-token" || store.puts != 0 {
				t.Fatalf("token=%q puts=%d", token, store.puts)
			}
			return &fakeClient{account: "octocat"}, nil
		},
	}
	output, err := execute(t, a, "auth", "login", "github", "personal", "--credential", "stored", "--replace", "--token-env", "MISSING_TOKEN", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com", "--transport", "ssh")
	if err != nil {
		t.Fatal(err)
	}
	if got, getErr := store.Get("github.com/personal"); getErr != nil || got.Secret != "device-token" {
		t.Fatalf("credential=%+v error=%v", got, getErr)
	}
	cfg, loadErr := config.Load(path)
	if loadErr != nil || cfg.Providers["personal"].Transport != "ssh" || !strings.Contains(output, "TEST-CODE") || strings.Contains(output, "device-token") {
		t.Fatalf("config=%+v load=%v output=%q", cfg, loadErr, output)
	}
}

func TestCredentialStoredDeviceFlowFailureDoesNotMutate(t *testing.T) {
	args := []string{"auth", "login", "github", "personal", "--credential", "stored", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com"}
	for _, tc := range []struct {
		name     string
		terminal bool
		extra    []string
		want     string
	}{
		{name: "non-TTY", want: "interactive terminal"},
		{name: "flag", terminal: true, extra: []string{"--noninteractive"}, want: "interactive terminal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			t.Setenv("GITHUB_TOKEN", "")
			store := &recordingStore{credentials: map[string]credential.Credential{}}
			authorizeCalls, clientCalls := 0, 0
			a := &App{ConfigPath: path, Credentials: store, IsTerminal: func() bool { return tc.terminal }, NewClient: func(config.Provider, string) (provider.Client, error) {
				clientCalls++
				return &fakeClient{}, nil
			}}
			a.AuthorizeGitHubDevice = func(context.Context, io.Writer) (string, error) {
				authorizeCalls++
				return "device-token", nil
			}
			_, err := execute(t, a, append(tc.extra, args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) || authorizeCalls != 0 || clientCalls != 0 || store.puts != 0 {
				t.Fatalf("error=%v authorize=%d clients=%d store=%#v", err, authorizeCalls, clientCalls, store)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("config mutated: %v", statErr)
			}
		})
	}
}

func TestReplaceInheritsStoredAuthAndUsesDeviceFlow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": storedProvider("personal")}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("COLT_GITHUB_CLIENT_ID", "ignored")
	store := &recordingStore{credentials: map[string]credential.Credential{}}
	authorizeCalls := 0
	a := &App{
		ConfigPath: path, Credentials: store, IsTerminal: func() bool { return true },
		AuthorizeGitHubDevice: func(context.Context, io.Writer) (string, error) {
			authorizeCalls++
			return "device-token", nil
		},
		NewClient: func(p config.Provider, token string) (provider.Client, error) {
			if p.Auth.Source != "stored" || token != "device-token" || store.puts != 0 {
				t.Fatalf("provider=%+v token=%q puts=%d", p, token, store.puts)
			}
			return &fakeClient{account: "octocat"}, nil
		},
	}
	_, err := execute(t, a, "auth", "login", "github", "personal", "--replace", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
	got, getErr := store.Get("github.com/personal")
	if err != nil || getErr != nil || got.Secret != "device-token" || authorizeCalls != 1 {
		t.Fatalf("error=%v credential=%+v get=%v authorize=%d", err, got, getErr, authorizeCalls)
	}
}

func TestCredentialStoredInvalidEnvTokenRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := config.Provider{Type: "github", Host: "github.com", BaseURL: "https://api.github.com", Namespace: "octocat", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "env"}}
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "bad\ntoken")
	store := credential.NewMemoryStore()
	a := &App{ConfigPath: path, Credentials: store, NewClient: func(_ config.Provider, _ string) (provider.Client, error) {
		t.Fatal("client should not be called")
		return nil, nil
	}}
	_, err := execute(t, a, "auth", "login", "github", "personal", "--credential", "stored", "--replace", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "invalid") {
		t.Fatalf("error=%v", err)
	}
	if _, getErr := store.Get("github.com/personal"); !errors.Is(getErr, credential.ErrNotFound) {
		t.Fatalf("credential was persisted: %v", getErr)
	}
}

func TestCredentialInvalidValueRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := config.Provider{Type: "github", Host: "github.com", BaseURL: "https://api.github.com", Namespace: "octocat", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "env"}}
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	a := &App{ConfigPath: path, NewClient: func(_ config.Provider, _ string) (provider.Client, error) {
		t.Fatal("client should not be called")
		return nil, nil
	}}
	_, err := execute(t, a, "auth", "login", "github", "personal", "--credential", "invalid", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
	if err == nil || !strings.Contains(err.Error(), "env or stored") {
		t.Fatalf("error=%v", err)
	}
}

func TestCredentialEnvNeverPromptsOrPersists(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	store := &recordingStore{credentials: map[string]credential.Credential{}}
	prompts, clients := 0, 0
	a := &App{ConfigPath: filepath.Join(t.TempDir(), "config.yaml"), Credentials: store, IsTerminal: func() bool { return true }, ReadToken: func() (string, error) {
		prompts++
		return "manual-token", nil
	}, NewClient: func(config.Provider, string) (provider.Client, error) {
		clients++
		return &fakeClient{}, nil
	}}
	_, err := execute(t, a, "auth", "login", "github", "personal", "--credential", "env", "--namespace", "octocat", "--git-name", "Test", "--git-email", "test@example.com")
	if err == nil || prompts != 0 || clients != 0 || store.puts != 0 {
		t.Fatalf("error=%v prompts=%d clients=%d store=%#v", err, prompts, clients, store)
	}
}

func TestCORE_PROVIDER_005StatusRespectsPathError(t *testing.T) {
	want := errors.New("config path unavailable")
	_, err := execute(t, &App{pathErr: want}, "auth", "status")
	if !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
}

func TestStatusPassesConfiguredTokenEnvironmentNamesToSSHProbe(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	personal := storedProvider("personal")
	personal.Transport = "ssh"
	personal.Auth = config.Auth{Source: "env", TokenEnv: "CUSTOM_PROVIDER_TOKEN"}
	forge := config.Provider{Type: "forgejo", Host: "forge.example", BaseURL: "https://forge.example", Namespace: "team", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "env"}}
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": personal, "forge": forge}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CUSTOM_PROVIDER_TOKEN", "secret")
	runner := &fakeGit{origin: "git@github.com:octocat/demo.git"}
	if _, err := execute(t, testApp(path, root, runner, &fakeClient{account: "octocat"}), "auth", "status", "personal"); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, name := range runner.lsRemoteTokenEnvNames {
		got[name] = true
	}
	for _, want := range []string{"CUSTOM_PROVIDER_TOKEN", "GITHUB_TOKEN", "FORGEJO_TOKEN"} {
		if !got[want] {
			t.Fatalf("token environment names = %v, missing %s", runner.lsRemoteTokenEnvNames, want)
		}
	}
}
