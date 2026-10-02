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

func TestCORE_GIT_011StatusTransportIsIndependentAndAuthorityMatched(t *testing.T) {
	for _, tc := range []struct {
		name, origin, transport       string
		authErr                       error
		probeErr                      error
		missingCredential             bool
		wantConnection, wantTransport string
		wantProbe                     bool
	}{
		{"https success", "https://github.com/octocat/demo.git", "https", nil, nil, false, "✓ connected", "HTTPS · ✓ Git authentication/connectivity and read access confirmed", true},
		{"ssh despite API failure", "git@github.com:octocat/demo.git", "ssh", errors.New("authentication failed"), nil, false, "✗ authentication failed", "SSH · ✓ Git authentication/connectivity and read access confirmed", true},
		{"ssh despite missing API credential", "git@github.com:octocat/demo.git", "ssh", nil, nil, true, "✗ credentials missing", "SSH · ✓ Git authentication/connectivity and read access confirmed", true},
		{"probe failure independent", "https://github.com/octocat/demo.git", "https", nil, errors.New("denied"), false, "✓ connected", "HTTPS · origin unreachable", true},
		{"wrong authority", "https://example.org/octocat/demo.git", "https", nil, nil, false, "✓ connected", "not checked", false},
		{"wrong namespace", "https://github.com/other/demo.git", "https", nil, nil, false, "✓ connected", "not checked", false},
		{"origin with userinfo", "https://attacker@github.com/octocat/demo.git", "https", nil, nil, false, "✓ connected", "not checked", false},
		{"authoritative origin using other transport", "git@github.com:octocat/demo.git", "https", nil, nil, false, "✓ connected", "SSH · ✓ Git authentication/connectivity and read access confirmed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.yaml")
			p := storedProvider("personal")
			p.Transport = tc.transport
			if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			store := &recordingStore{credentials: map[string]credential.Credential{"github.com/personal": {Kind: "bearer_token", Secret: "transport-test-secret"}}}
			if tc.missingCredential {
				store.credentials = map[string]credential.Credential{}
			}
			runner := &fakeGit{origin: tc.origin, lsRemoteErr: tc.probeErr}
			a := &App{ConfigPath: path, WorkDir: root, Credentials: store, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
				return &fakeClient{account: "octocat", authErr: tc.authErr}, nil
			}}
			output, err := execute(t, a, "auth", "status")
			after, _ := os.ReadFile(path)
			probed := strings.Contains(strings.Join(runner.calls, "\n"), "ls-remote:")
			if err != nil || !strings.Contains(output, tc.wantConnection) || !strings.Contains(output, tc.wantTransport) || probed != tc.wantProbe || strings.Contains(output, "transport-test-secret") || string(after) != string(before) {
				t.Fatalf("error=%v output=%q calls=%v config changed=%v", err, output, runner.calls, string(after) != string(before))
			}
		})
	}
}

func TestCORE_GIT_011ExplicitRepositoryUsesAuthoritativeMetadataForEveryProvider(t *testing.T) {
	for _, tc := range []struct {
		providerType, host, namespace, transport, httpsURL, sshURL, want string
	}{
		{"github", "github.com", "octocat", "https", "https://github.com/octocat/demo.git", "git@github.com:octocat/demo.git", "https://github.com/octocat/demo.git"},
		{"gitlab", "gitlab.example", "platform/tools", "ssh", "https://gitlab.example/platform/tools/demo.git", "git@gitlab.example:platform/tools/demo.git", "git@gitlab.example:platform/tools/demo.git"},
		{"gitea", "code.example", "octocat", "https", "https://code.example/octocat/demo.git", "git@code.example:octocat/demo.git", "https://code.example/octocat/demo.git"},
		{"forgejo", "forge.example", "team", "ssh", "https://forge.example/team/demo.git", "git@forge.example:team/demo.git", "git@forge.example:team/demo.git"},
	} {
		t.Run(tc.providerType, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.yaml")
			baseURL := "https://" + tc.host
			if tc.providerType == "github" {
				baseURL = "https://api.github.com"
			}
			p := config.Provider{Type: tc.providerType, Host: tc.host, BaseURL: baseURL, Namespace: tc.namespace, Visibility: "private", Transport: tc.transport, GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "env", TokenEnv: "STATUS_TOKEN"}}
			if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": p}}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("STATUS_TOKEN", "explicit-status-secret")
			runner := &fakeGit{}
			client := &fakeClient{account: "user", found: &provider.Repository{CloneURL: tc.httpsURL, SSHURL: tc.sshURL}}
			output, err := execute(t, testApp(path, root, runner, client), "auth", "status", "work", "--repository", "demo")
			calls := strings.Join(runner.calls, ",")
			if err != nil || client.gets != 1 || calls != "ls-remote:"+tc.want+":work:demo" || !strings.Contains(output, "Connection:  ✓ connected") || !strings.Contains(output, "read access confirmed (clone/fetch/pull); push/write not checked") || strings.Contains(output, "explicit-status-secret") {
				t.Fatalf("error=%v gets=%d git=%q output=%q", err, client.gets, calls, output)
			}
		})
	}
}

func TestCORE_GIT_011ExplicitRepositoryFailuresAreSafeAndIndependent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		client  *fakeClient
		want    string
		apiFail bool
	}{
		{"metadata unavailable", &fakeClient{account: "octocat", getErr: errors.New("response included secret-status-value")}, "metadata unavailable", false},
		{"wrong authority", &fakeClient{account: "octocat", found: &provider.Repository{CloneURL: "https://evil.example/other/wrong.git"}}, "unexpected clone target", false},
		{"API failure skips metadata", &fakeClient{authErr: errors.New("authentication failed")}, "authentication failed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.yaml")
			p := storedProvider("personal")
			if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GITHUB_TOKEN", "secret-status-value")
			runner := &fakeGit{}
			output, err := execute(t, testApp(path, root, runner, tc.client), "auth", "status", "personal", "--repository", "demo")
			if err != nil || !strings.Contains(output, tc.want) || strings.Contains(output, "secret-status-value") || len(runner.calls) != 0 || tc.apiFail && tc.client.gets != 0 || !tc.apiFail && !strings.Contains(output, "Connection:  ✓ connected") {
				t.Fatalf("error=%v gets=%d git=%v output=%q", err, tc.client.gets, runner.calls, output)
			}
		})
	}
}

func TestCORE_GIT_011RepositoryOptionValidationPrecedesReads(t *testing.T) {
	for _, args := range [][]string{
		{"auth", "status", "--repository", "demo"},
		{"auth", "status", "work", "--repository", "demo", "--offline"},
		{"auth", "status", "work", "--repository", "../demo"},
	} {
		store := &countingStore{}
		runner := &fakeGit{}
		clients := 0
		_, err := execute(t, &App{ConfigPath: filepath.Join(t.TempDir(), "missing.yaml"), Credentials: store, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
			clients++
			return &fakeClient{}, nil
		}}, args...)
		if err == nil || store.gets != 0 || len(runner.calls) != 0 || clients != 0 {
			t.Fatalf("args=%v error=%v store=%d git=%v clients=%d", args, err, store.gets, runner.calls, clients)
		}
	}
}

func TestCORE_GIT_011AliasWithoutRepositoryUsesMatchingCurrentOrigin(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	p := storedProvider("personal")
	p.Auth = config.Auth{Source: "env", TokenEnv: "ALIAS_STATUS_TOKEN"}
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p, "duplicate": p}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALIAS_STATUS_TOKEN", "alias-status-secret")
	runner := &fakeGit{origin: "https://github.com/octocat/demo.git"}
	output, err := execute(t, testApp(path, root, runner, &fakeClient{account: "octocat"}), "auth", "status", "personal")
	if err != nil || !strings.Contains(strings.Join(runner.calls, ","), "ls-remote:https://github.com/octocat/demo.git:personal:demo") || strings.Contains(output, "duplicate") || strings.Contains(output, "alias-status-secret") {
		t.Fatalf("error=%v calls=%v output=%q", err, runner.calls, output)
	}
}

func TestCORE_GIT_011OfflineSkipsAllCredentialProviderAndGitAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := storedProvider("personal")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	store := &recordingStore{credentials: map[string]credential.Credential{"github.com/personal": {Secret: "offline-secret"}}}
	runner := &fakeGit{origin: "https://github.com/octocat/demo.git"}
	clients := 0
	output, err := execute(t, &App{ConfigPath: path, Credentials: store, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
		clients++
		return nil, nil
	}}, "auth", "status", "--offline")
	if err != nil || store.gets != 0 || clients != 0 || len(runner.calls) != 0 || strings.Count(output, "not checked") != 2 || strings.Contains(output, "offline-secret") {
		t.Fatalf("error=%v gets=%d clients=%d git=%v output=%q", err, store.gets, clients, runner.calls, output)
	}
}

func TestMatchingOriginAcceptsEitherAuthoritativeTransport(t *testing.T) {
	p := appProvider()
	p.Transport = "ssh"
	cfg := config.Config{Providers: map[string]config.Provider{"work": p}}
	for _, tc := range []struct {
		origin, transport string
	}{
		{"https://gitlab.com/team/demo.git", "https"},
		{"git@gitlab.com:team/demo.git", "ssh"},
	} {
		alias, transport, project := matchingOrigin(cfg, tc.origin)
		if alias != "work" || transport != tc.transport || project != "demo" {
			t.Fatalf("matchingOrigin(%q) = %q, %q, %q", tc.origin, alias, transport, project)
		}
	}
}
