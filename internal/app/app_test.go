package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

func executeInput(t *testing.T, a *App, input string, args ...string) (string, error) {
	t.Helper()
	cmd := a.Root()
	var output bytes.Buffer
	cmd.SetIn(strings.NewReader(input))
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return output.String(), err
}

type recordingStore struct {
	mu          sync.Mutex
	credentials map[string]credential.Credential
	gets        int
	puts        int
	deletes     []string
	deleteErr   error
	getErr      error
	putErr      error
}

func (s *recordingStore) Get(id string) (credential.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	if s.getErr != nil {
		return credential.Credential{}, s.getErr
	}
	value, ok := s.credentials[id]
	if !ok {
		return credential.Credential{}, credential.ErrNotFound
	}
	return value, nil
}
func (s *recordingStore) Put(id string, value credential.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts++
	if s.putErr != nil {
		return s.putErr
	}
	s.credentials[id] = value
	return nil
}
func (s *recordingStore) Create(id string, value credential.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts++
	if s.putErr != nil {
		return s.putErr
	}
	if _, exists := s.credentials[id]; exists {
		return credential.ErrAlreadyExists
	}
	s.credentials[id] = value
	return nil
}
func (s *recordingStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
func (s *recordingStore) DeleteIf(id string, expected credential.Credential) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes = append(s.deletes, id)
	if s.deleteErr != nil {
		return false, s.deleteErr
	}
	current, ok := s.credentials[id]
	if !ok || current.Kind != expected.Kind || current.Secret != expected.Secret {
		return false, nil
	}
	delete(s.credentials, id)
	return true, nil
}

func storedProvider(alias string) config.Provider {
	p := config.Provider{Type: "github", Host: "github.com", BaseURL: "https://api.github.com", Namespace: "octocat", Visibility: "private", GitName: "Test", GitEmail: "test@example.com"}
	p.Auth = config.Auth{Source: "stored", CredentialID: p.Host + "/" + alias}
	return p
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

func TestApplyCABundle(t *testing.T) {
	if err := applyCABundle(""); err != nil {
		t.Fatalf("empty CA bundle: %v", err)
	}
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, []byte("cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyCABundle(ca); err != nil {
		t.Fatalf("valid bundle: %v", err)
	}
	if got := os.Getenv("SSL_CERT_FILE"); got != ca {
		t.Fatalf("SSL_CERT_FILE = %q, want %q", got, ca)
	}
	if err := applyCABundle(filepath.Join(dir, "missing.pem")); err == nil {
		t.Fatal("missing bundle accepted")
	}
	if err := applyCABundle(dir); err == nil {
		t.Fatal("directory accepted as CA bundle")
	}
}

func TestRootVersionFlag(t *testing.T) {
	a := &App{ConfigPath: filepath.Join(t.TempDir(), "config.yaml")}
	var out bytes.Buffer
	cmd := a.Root()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out.String(), "version devel") {
		t.Fatalf("version output = %q", out.String())
	}
}

type countingStore struct{ gets int }

func (s *countingStore) Get(string) (credential.Credential, error) {
	s.gets++
	return credential.Credential{}, credential.ErrNotFound
}
func (*countingStore) Put(string, credential.Credential) error    { return nil }
func (*countingStore) Create(string, credential.Credential) error { return nil }
func (*countingStore) Delete(string) error                        { return nil }
func (*countingStore) DeleteIf(string, credential.Credential) (bool, error) {
	return false, nil
}

type fakeGit struct {
	calls                 []string
	availableErr          error
	cloneErr              error
	pushErr               error
	origin                string
	originErr             error
	lsRemoteErr           error
	lsRemoteTokenEnvNames []string
	cloneHook             func(string) error
	health                gitnative.HealthState
	healthErr             error
	healthFunc            func(string) (gitnative.HealthState, error)
}

func (g *fakeGit) Available() error { g.calls = append(g.calls, "available"); return g.availableErr }
func (g *fakeGit) Origin(context.Context, string) (string, error) {
	g.calls = append(g.calls, "origin")
	return g.origin, g.originErr
}
func (g *fakeGit) LsRemote(_ context.Context, _, origin, alias, project string, tokenEnvNames []string) error {
	g.calls = append(g.calls, "ls-remote:"+origin+":"+alias+":"+project)
	g.lsRemoteTokenEnvNames = append([]string(nil), tokenEnvNames...)
	return g.lsRemoteErr
}
func (g *fakeGit) Init(context.Context, string) error { g.calls = append(g.calls, "init"); return nil }
func (g *fakeGit) Clone(_ context.Context, url, destination, _, _, _ string) error {
	g.calls = append(g.calls, "clone:"+url+":"+destination)
	if g.cloneErr != nil {
		return g.cloneErr
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(destination, "README.md"), []byte("clone\n"), 0o644); err != nil {
		return err
	}
	if g.cloneHook != nil {
		return g.cloneHook(destination)
	}
	return nil
}
func (g *fakeGit) MirrorClone(ctx context.Context, url, destination, alias, project, _ string, _ []string) error {
	g.calls = append(g.calls, "mirror-clone:"+url)
	return g.Clone(ctx, url, destination, "", alias, project)
}
func (g *fakeGit) MirrorPush(_ context.Context, _, url, _, project string, force bool, _ string, _ []string) error {
	g.calls = append(g.calls, fmt.Sprintf("mirror-push:%s:%s:%t", url, project, force))
	return g.pushErr
}
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
func (g *fakeGit) ConfigureCredentialHelper(context.Context, string, string, string, string, string) error {
	g.calls = append(g.calls, "helper")
	return nil
}
func (g *fakeGit) Push(_ context.Context, _, url, _, _ string) error {
	g.calls = append(g.calls, "push:"+url)
	return g.pushErr
}
func (g *fakeGit) PushTag(_ context.Context, _, url, _, _, tag string, _ []string) error {
	g.calls = append(g.calls, "push-tag:"+url+":"+tag)
	return g.pushErr
}
func (g *fakeGit) CreateTag(_ context.Context, _ /* dir */, tag, _, _ string) error {
	g.calls = append(g.calls, "tag:"+tag)
	return nil
}
func (g *fakeGit) ValidateTag(_ context.Context, _ /* dir */, tag string) error {
	g.calls = append(g.calls, "validate-tag:"+tag)
	return nil
}
func (g *fakeGit) ValidateRepoConfig(context.Context, string) error {
	g.calls = append(g.calls, "validate-config")
	return nil
}
func (g *fakeGit) Health(_ context.Context, dir string) (gitnative.HealthState, error) {
	g.calls = append(g.calls, "health")
	if g.healthFunc != nil {
		return g.healthFunc(dir)
	}
	return g.health, g.healthErr
}

type fakeClient struct {
	account         string
	authErr, getErr error
	createErr       error
	listErr         error
	found, created  *provider.Repository
	listRepos       []provider.Repository
	gets, creates   int
	lists           int
	visibility      string
	revokeErr       error
	revokes         int
	getRepos        map[string]*provider.Repository
	getErrors       map[string]error
}

func (f *fakeClient) Authenticate(context.Context) (string, error) { return f.account, f.authErr }
func (f *fakeClient) Get(_ context.Context, project string) (*provider.Repository, error) {
	f.gets++
	if err := f.getErrors[project]; err != nil {
		return nil, err
	}
	if repository := f.getRepos[project]; repository != nil {
		return repository, nil
	}
	return f.found, f.getErr
}
func (f *fakeClient) List(context.Context) ([]provider.Repository, error) {
	f.lists++
	return f.listRepos, f.listErr
}
func (f *fakeClient) Create(_ context.Context, _ string, visibility ...string) (*provider.Repository, error) {
	f.creates++
	if len(visibility) > 0 {
		f.visibility = visibility[0]
	}
	return f.created, f.createErr
}
func (f *fakeClient) Release(context.Context, string, string) error { return nil }
func (f *fakeClient) Revoke(context.Context, provider.RevocationOptions) error {
	f.revokes++
	return f.revokeErr
}

func testApp(path, workDir string, runner *fakeGit, client *fakeClient) *App {
	return &App{ConfigPath: path, WorkDir: workDir, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) { return client, nil }}
}

func containsCall(calls []string, op string) bool {
	for _, c := range calls {
		if c == op || strings.HasPrefix(c, op+":") {
			return true
		}
	}
	return false
}
