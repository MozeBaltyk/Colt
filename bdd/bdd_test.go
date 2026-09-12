// Package bdd runs the active deterministic Gherkin scenarios with local
// fixtures only: temp config, temp workdir, stub provider client (or
// loopback TLS servers), and real or stub native git. No real providers,
// no global Git writes.
//
// Scope: every feature is loaded. Scenarios specifying
// behavior the binary does not implement yet (device flow, production
// credential enrollment, backend-specific logout, and revoke) carry
// @unimplemented and are excluded by tag until the behavior lands.
package bdd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MozeBaltyk/Colt/internal/app"
	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/cucumber/godog"
)

const (
	tokenEnv       = "COLT_BDD_TOKEN"
	commandTimeout = 30 * time.Second
)

// world is reset for every scenario by the Before hook.
type world struct {
	dir         string
	configPath  string
	app         *app.App
	git         *fakeGit
	client      *fakeClient
	credentials credential.Store
	newClient   func(config.Provider, string) (provider.Client, error)
	out         string
	runErr      error
	globalGit   string
	providers   map[string]config.Provider
	// login context for `colt auth login` steps
	loginAlias  string
	loginType   string
	pendingHost string
	pendingBase string
	// recorded construction calls: proves which credential reached the client
	newClientCalls      []newClientCall
	configBefore        []byte
	secrets             []string
	savedEnv            map[string]*string
	redirectB           *redirectTrap
	redirectClose       func()
	repeatedParsingSame bool
	store               *fakeCredentialStore
	helperOperation     string
	helperInput         string
	sshState            map[string][]byte
	sshAgent            string
}

type newClientCall struct {
	provider config.Provider
	token    string
}

type fakeCredentialStore struct {
	credentials         map[string]credential.Credential
	getErr              error
	deleteErr           error
	gets, puts, deletes int
	deletedIDs          []string
}

func newFakeCredentialStore() *fakeCredentialStore {
	return &fakeCredentialStore{credentials: map[string]credential.Credential{}}
}

func (s *fakeCredentialStore) Get(id string) (credential.Credential, error) {
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

func (s *fakeCredentialStore) Put(id string, value credential.Credential) error {
	s.puts++
	s.credentials[id] = value
	return nil
}

func (s *fakeCredentialStore) Delete(id string) error {
	s.deletes++
	s.deletedIDs = append(s.deletedIDs, id)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if _, ok := s.credentials[id]; !ok {
		return credential.ErrNotFound
	}
	delete(s.credentials, id)
	return nil
}

var credentialEnvVars = []string{tokenEnv, "GITHUB_TOKEN", "GITLAB_TOKEN", "COMPANY_GL_TOKEN"}

var sandboxEnvVars = []string{
	"SSH_AUTH_SOCK",
	"HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "GIT_TERMINAL_PROMPT",
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "all_proxy", "no_proxy",
}

// managedEnvVars are restored by the After hook.
var managedEnvVars = append(append([]string(nil), credentialEnvVars...), sandboxEnvVars...)

func unsetCredentialEnv() error {
	var unsetErr error
	for _, name := range credentialEnvVars {
		if err := os.Unsetenv(name); err != nil {
			unsetErr = errors.Join(unsetErr, fmt.Errorf("unset %s: %w", name, err))
		}
	}
	return unsetErr
}

func TestCredentialEnvBoundary(t *testing.T) {
	for _, name := range managedEnvVars {
		t.Setenv(name, "preserved")
	}
	if err := unsetCredentialEnv(); err != nil {
		t.Fatal(err)
	}
	for _, name := range credentialEnvVars {
		if _, ok := os.LookupEnv(name); ok {
			t.Errorf("credential environment %s was not unset", name)
		}
	}
	for _, name := range sandboxEnvVars {
		if got := os.Getenv(name); got != "preserved" {
			t.Errorf("sandbox environment %s = %q, want preserved", name, got)
		}
	}
}

func (w *world) reset(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}

	// NOTE: add new world fields here so scenario state cannot leak.
	w.dir = dir
	w.configPath = filepath.Join(dir, "config.yaml")
	w.app = nil
	w.git = &fakeGit{real: true}
	w.client = &fakeClient{account: "example-user"}
	w.credentials = nil
	w.out = ""
	w.runErr = nil
	w.globalGit = ""
	w.providers = map[string]config.Provider{}
	w.loginAlias, w.loginType = "work", "gitlab"
	w.pendingHost, w.pendingBase = "", ""
	w.newClientCalls = nil
	w.configBefore = nil
	w.secrets = nil
	w.savedEnv = saveEnv()
	w.redirectB = nil
	w.redirectClose = nil
	w.repeatedParsingSame = false
	w.store = nil
	w.helperOperation, w.helperInput = "", ""
	w.sshState = nil
	w.sshAgent = ""
	w.newClient = func(p config.Provider, token string) (provider.Client, error) {
		w.newClientCalls = append(w.newClientCalls, newClientCall{provider: p, token: token})
		return w.client, nil
	}

	setEnv(t, "HOME", home)
	setEnv(t, "GIT_CONFIG_GLOBAL", os.DevNull)
	setEnv(t, "GIT_CONFIG_NOSYSTEM", "1")
	setEnv(t, "GIT_TERMINAL_PROMPT", "0")
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		setEnv(t, name, "http://127.0.0.1:1")
	}
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		setEnv(t, name, "localhost,127.0.0.1")
	}
	setEnv(t, tokenEnv, "bdd-fake-secret")
	w.secrets = []string{"bdd-fake-secret"}
}

func setEnv(t *testing.T, name, value string) {
	t.Helper()
	if err := os.Setenv(name, value); err != nil {
		t.Fatalf("set %s: %v", name, err)
	}
}

func (w *world) buildApp() {
	w.app = &app.App{
		ConfigPath:  w.configPath,
		WorkDir:     w.dir,
		Git:         w.git,
		NewClient:   w.newClient,
		Credentials: w.credentials,
	}
}

func (w *world) saveConfig() error {
	return config.Save(w.configPath, config.Config{Providers: w.providers})
}

func stdProvider(pType, namespace string) config.Provider {
	host, base := "github.com", "https://api.github.com"
	if pType == "gitlab" {
		host, base = "gitlab.com", "https://gitlab.com"
	}
	return config.Provider{
		Type: pType, Host: host, BaseURL: base, Namespace: namespace,
		Visibility: "private", GitName: "Example User", GitEmail: "user@example.invalid",
		Auth: config.Auth{Source: "env"},
	}
}

// withTokenEnv attaches an explicit credential source. Providers without
// one resolve conventional variables (GITHUB_TOKEN / GITLAB_TOKEN).
func withTokenEnv(p config.Provider, env string) config.Provider {
	p.Auth.TokenEnv = env
	return p
}

// --- fakes ---

// fakeGit records calls; with real=true it delegates to native git.
type fakeGit struct {
	real         bool
	availableErr error
	pushErr      error
	origins      []string
	pushes       int
	pushURL      string
	pushToken    string
	helpers      int
	operations   []string
}

func (f *fakeGit) Available() error {
	f.operations = append(f.operations, "available")
	if f.availableErr != nil {
		return f.availableErr
	}
	if f.real {
		return gitnative.Native{}.Available()
	}
	return nil
}

func (f *fakeGit) Init(ctx context.Context, dir string) error {
	f.operations = append(f.operations, "init")
	if f.real {
		return gitnative.Native{}.Init(ctx, dir)
	}
	return os.MkdirAll(filepath.Join(dir, ".git"), 0o755) // ponytail: marker only, no history
}

func (f *fakeGit) SetIdentity(ctx context.Context, dir, name, email string) error {
	f.operations = append(f.operations, "identity")
	if f.real {
		return gitnative.Native{}.SetIdentity(ctx, dir, name, email)
	}
	return nil
}

func (f *fakeGit) Commit(ctx context.Context, dir string) (string, error) {
	f.operations = append(f.operations, "commit")
	if f.real {
		return gitnative.Native{}.Commit(ctx, dir)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "COMMIT"), []byte("fake"), 0o600); err != nil {
		return "", err
	}
	return "fake-commit", nil
}

func (f *fakeGit) AddOrigin(ctx context.Context, dir, url string) error {
	f.operations = append(f.operations, "origin")
	if f.real {
		if err := (gitnative.Native{}).AddOrigin(ctx, dir, url); err != nil {
			return err
		}
	}
	f.origins = append(f.origins, url)
	return nil
}

func (f *fakeGit) ConfigureCredentialHelper(ctx context.Context, dir string) error {
	f.operations = append(f.operations, "helper")
	if f.real {
		if err := (gitnative.Native{}).ConfigureCredentialHelper(ctx, dir); err != nil {
			return err
		}
	}
	f.helpers++
	return nil
}

func (f *fakeGit) Push(_ context.Context, _ /* dir */, url, _, token string) error {
	f.operations = append(f.operations, "push")
	if f.pushErr != nil {
		return f.pushErr
	}
	f.pushes++
	f.pushURL, f.pushToken = url, token
	return nil
}

type fakeClient struct {
	account    string
	authErr    error
	getRepo    *provider.Repository
	getErr     error
	createRepo *provider.Repository
	createErr  error
	calls      []string
}

func (f *fakeClient) Authenticate(context.Context) (string, error) {
	f.calls = append(f.calls, "Authenticate")
	if f.authErr != nil {
		return "", f.authErr
	}
	return f.account, nil
}

func (f *fakeClient) Get(_ context.Context, project string) (*provider.Repository, error) {
	f.calls = append(f.calls, "Get:"+project)
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getRepo, nil
}

func (f *fakeClient) Create(_ context.Context, project string) (*provider.Repository, error) {
	f.calls = append(f.calls, "Create:"+project)
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createRepo, nil
}

func (f *fakeClient) called(op string) bool {
	for _, c := range f.calls {
		if c == op || strings.HasPrefix(c, op+":") {
			return true
		}
	}
	return false
}

// --- suite ---

func TestBDD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("native git not on PATH")
	}
	suite := godog.TestSuite{
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			w := &world{}
			ctx.Before(func(scenarioCtx context.Context, _ *godog.Scenario) (context.Context, error) {
				w.reset(t)
				var err error
				w.globalGit, err = gitGlobalSnapshot()
				w.buildApp()
				return scenarioCtx, err
			})
			ctx.After(func(scenarioCtx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				if w.redirectClose != nil {
					w.redirectClose()
				}
				return scenarioCtx, restoreEnv(w.savedEnv)
			})
			registerSteps(ctx, w)
			registerProviderConfigSteps(ctx, w)
		},
		Options: &godog.Options{
			Format:      "pretty",
			Paths:       []string{"../features"},
			Strict:      true,
			Concurrency: 1,
			// Status tags are the only exclusions: every other scenario executes.
			Tags:     "~@planned&&~@unimplemented",
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("bdd suite failed")
	}
}

func gitGlobalSnapshot() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "config", "--global", "--list").CombinedOutput() // ponytail: read-only snapshot
	return string(out), err
}

func (w *world) run(cmdLine string) {
	fields, err := splitCmd(cmdLine)
	if err != nil {
		w.runErr = err
		return
	}
	if len(fields) < 2 || fields[0] != "colt" {
		w.runErr = fmt.Errorf("bdd: cannot run %q", cmdLine)
		return
	}
	w.runArgs(fields[1:])
}

func (w *world) runArgs(args []string) {
	w.configBefore, _ = os.ReadFile(w.configPath)
	w.buildApp()
	cmd := w.app.Root()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	w.runErr = cmd.ExecuteContext(ctx)
	w.out = buf.String()
}

// splitCmd supports the small shell-like subset used by feature command steps.
func splitCmd(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	var quote rune
	escaped, started := false, false
	flush := func() {
		if started {
			out = append(out, cur.String())
			cur.Reset()
			started = false
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
			started = true
		case r == '\\':
			escaped = true
			started = true
		case quote != 0 && r == quote:
			quote = 0
			started = true
		case quote == 0 && (r == '"' || r == '\''):
			quote = r
			started = true
		case quote == 0 && (r == ' ' || r == '\t'):
			flush()
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if escaped {
		return nil, errors.New("bdd: dangling command escape")
	}
	if quote != 0 {
		return nil, errors.New("bdd: unterminated command quote")
	}
	flush()
	return out, nil
}

func TestSplitCmd(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
		err  bool
	}{
		{"double quoted spaces", `colt auth --git-name "Example User"`, []string{"colt", "auth", "--git-name", "Example User"}, false},
		{"single quoted spaces", `colt auth --git-name 'Example User'`, []string{"colt", "auth", "--git-name", "Example User"}, false},
		{"empty quotes", `colt init "" ''`, []string{"colt", "init", "", ""}, false},
		{"tabs", "colt\tinit\tdemo", []string{"colt", "init", "demo"}, false},
		{"escaped space", `colt init Example\ User`, []string{"colt", "init", "Example User"}, false},
		{"escaped quote", `colt init "Example \"User\""`, []string{"colt", "init", `Example "User"`}, false},
		{"unterminated quote", `colt init "demo`, nil, true},
		{"dangling escape", `colt init demo\`, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := splitCmd(tc.in)
			if (err != nil) != tc.err || !slices.Equal(got, tc.want) {
				t.Fatalf("splitCmd(%q) = %#v, %v; want %#v, error=%v", tc.in, got, err, tc.want, tc.err)
			}
		})
	}
}

func saveEnv() map[string]*string {
	saved := map[string]*string{}
	for _, name := range managedEnvVars {
		if v, ok := os.LookupEnv(name); ok {
			v := v
			saved[name] = &v
		} else {
			saved[name] = nil
		}
	}
	return saved
}

func restoreEnv(saved map[string]*string) error {
	var restoreErr error
	for _, name := range managedEnvVars {
		if v, ok := saved[name]; ok && v != nil {
			if err := os.Setenv(name, *v); err != nil {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("restore %s: %w", name, err))
			}
		} else {
			if err := os.Unsetenv(name); err != nil {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("restore %s: %w", name, err))
			}
		}
	}
	return restoreErr
}

// setSecret exports a credential value for one step and tracks it so
// redaction assertions can verify it never leaks into output or files.
func (w *world) setSecret(name, value string) {
	os.Setenv(name, value)
	w.secrets = append(w.secrets, value)
}

func (w *world) leaked(s string) string {
	for _, secret := range w.secrets {
		if secret != "" && strings.Contains(s, secret) {
			return secret
		}
	}
	return ""
}

func gitOut(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return strings.TrimSpace(buf.String()), err
}

func registerSteps(ctx *godog.ScenarioContext, w *world) {
	// fixtures
	ctx.Step(`^a valid provider with namespace, defaults, and Git identity is selected$`, func() error {
		w.providers = map[string]config.Provider{"work": withTokenEnv(stdProvider("gitlab", "example-namespace"), tokenEnv)}
		w.providers["work"] = withDefault(w.providers["work"], true)
		return w.saveConfig()
	})
	ctx.Step(`^native git is available$`, func() error {
		w.git.real = true
		return gitnative.Native{}.Available()
	})
	ctx.Step(`^native git is unavailable$`, func() error {
		w.git.real = false
		w.git.availableErr = errors.New("native git is unavailable; install git and ensure it is on PATH")
		return nil
	})
	ctx.Step(`^destination "([^"]*)" exists and contains user data$`, func(name string) error {
		dst := filepath.Join(w.dir, name)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, "notes.txt"), []byte("user data"), 0o600)
	})
	ctx.Step(`^destination "([^"]*)" exists and is empty$`, func(name string) error {
		return os.Mkdir(filepath.Join(w.dir, name), 0o755)
	})
	ctx.Step(`^destination "([^"]*)" is a (regular file|symlink)$`, func(name, kind string) error {
		dst := filepath.Join(w.dir, name)
		if kind == "regular file" {
			return os.WriteFile(dst, []byte("user data"), 0o600)
		}
		target := filepath.Join(w.dir, "symlink-target")
		if err := os.Mkdir(target, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(target, "notes.txt"), []byte("user data"), 0o600); err != nil {
			return err
		}
		return os.Symlink(target, dst)
	})
	ctx.Step(`^the project name is invalid for the selected provider$`, func() error {
		w.providers = map[string]config.Provider{"work": withTokenEnv(stdProvider("gitlab", "example-namespace"), tokenEnv)}
		return w.saveConfig()
	})
	ctx.Step(`^the selected provider type is (GitHub|GitLab)$`, func(pType string) error {
		w.git.real = false
		kind := strings.ToLower(pType)
		p := withTokenEnv(stdProvider(kind, "example-namespace"), tokenEnv)
		p.Default = true
		w.providers = map[string]config.Provider{"personal": p}
		host := "github.com"
		if kind == "gitlab" {
			host = "gitlab.com"
		}
		w.client.createRepo = &provider.Repository{CloneURL: "https://" + host + "/example-namespace/demo.git"}
		w.client.getErr = provider.ErrNotFound
		return w.saveConfig()
	})
	ctx.Step(`^the transport preference resolves to HTTPS$`, func() error {
		w.git.real = true
		p := w.providers["personal"]
		p.Namespace = "example-user"
		w.providers["personal"] = p
		w.client.createRepo = &provider.Repository{CloneURL: "https://github.com/example-user/demo.git"}
		return w.saveConfig()
	})
	ctx.Step(`^the transport preference resolves to SSH$`, func() error {
		w.git.real = true
		p := w.providers["personal"]
		p.Namespace, p.Transport = "example-user", "ssh"
		w.providers["personal"] = p
		w.client.createRepo = &provider.Repository{
			CloneURL: "https://github.com/example-user/demo.git",
			SSHURL:   "git@github.com:example-user/demo.git",
		}
		return w.saveConfig()
	})
	ctx.Step(`^provider API authentication fails for the selected provider$`, func() error {
		w.git.real = false
		w.providers = map[string]config.Provider{"work": withTokenEnv(stdProvider("gitlab", "example-namespace"), tokenEnv)}
		w.client.getErr = errors.New("authentication failed: rejected credential")
		return w.saveConfig()
	})
	ctx.Step(`^the selected provider credential is missing$`, func() error {
		p := withTokenEnv(stdProvider("gitlab", "example-namespace"), "COMPANY_GL_TOKEN")
		p.Default = true
		w.providers = map[string]config.Provider{"work": p}
		os.Unsetenv("COMPANY_GL_TOKEN")
		return w.saveConfig()
	})
	ctx.Step(`^the selected provider uses an unavailable stored API credential$`, func() error {
		p := stdProvider("gitlab", "example-namespace")
		p.Default = true
		p.Auth = config.Auth{Source: "stored", CredentialID: "gitlab.com/work"}
		w.providers = map[string]config.Provider{"work": p}
		w.store = newFakeCredentialStore()
		w.store.getErr = credential.ErrStoreUnavailable
		w.credentials = w.store
		return w.saveConfig()
	})
	ctx.Step(`^the selected provider has no Git identity$`, func() error {
		raw := "providers:\n  work:\n    type: gitlab\n    host: gitlab.com\n    base_url: https://gitlab.com\n    namespace: example-namespace\n    visibility: private\n    git_email: user@example.invalid\n    auth:\n      source: env\n      token_env: " + tokenEnv + "\n    default: true\n"
		return os.WriteFile(w.configPath, []byte(raw), 0o600)
	})
	ctx.Step(`^the selected provider transport is unsupported$`, func() error {
		raw := "providers:\n  work:\n    type: gitlab\n    host: gitlab.com\n    base_url: https://gitlab.com\n    namespace: example-namespace\n    visibility: private\n    git_name: Example User\n    git_email: user@example.invalid\n    transport: ftp\n    auth:\n      source: env\n      token_env: " + tokenEnv + "\n    default: true\n"
		return os.WriteFile(w.configPath, []byte(raw), 0o600)
	})
	ctx.Step(`^the selected transport is (HTTPS|SSH)$`, func(transport string) error {
		w.git.real = false
		p := withTokenEnv(stdProvider("github", "example-namespace"), tokenEnv)
		p.Default = true
		if transport == "SSH" {
			p.Transport = "ssh"
		}
		w.providers = map[string]config.Provider{"personal": p}
		w.client.getErr = provider.ErrNotFound
		return w.saveConfig()
	})
	ctx.Step(`^provider creation succeeds with a (missing|wrong repository) selected clone target$`, func(target string) error {
		p := w.providers["personal"]
		repo := &provider.Repository{}
		if target == "wrong repository" {
			if p.Transport == "ssh" {
				repo.SSHURL = "git@github.com:example-namespace/other.git"
			} else {
				repo.CloneURL = "https://github.com/example-namespace/other.git"
			}
		}
		w.client.createRepo = repo
		return nil
	})
	ctx.Step(`^a native repository has an existing local credential helper$`, func() error {
		dst := filepath.Join(w.dir, "demo")
		if err := os.Mkdir(dst, 0o755); err != nil {
			return err
		}
		commandCtx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		if err := (gitnative.Native{}).Init(commandCtx, dst); err != nil {
			return err
		}
		_, err := gitOut(dst, "config", "--local", "--add", "credential.helper", "existing-helper")
		return err
	})
	ctx.Step(`^the remote repository did not exist during preflight$`, func() error {
		w.git.real = false
		w.providers = map[string]config.Provider{"work": withTokenEnv(stdProvider("gitlab", "example-namespace"), tokenEnv)}
		w.client.getErr = provider.ErrNotFound
		return w.saveConfig()
	})
	ctx.Step(`^another actor creates it before Colt's create request completes$`, func() error {
		w.client.createErr = provider.ErrConflict
		return nil
	})
	ctx.Step(`^repository "([^"]*)" already exists in the selected namespace$`, func() error {
		w.git.real = false
		w.providers = map[string]config.Provider{"work": withTokenEnv(stdProvider("gitlab", "example-namespace"), tokenEnv)}
		w.client.getRepo = &provider.Repository{CloneURL: "https://gitlab.com/example-namespace/demo.git"}
		return w.saveConfig()
	})
	ctx.Step(`^Colt created the local repository, initial commit, remote repository, and "origin"$`, func() error {
		w.git.real = false
		w.git.pushErr = errors.New("native git failed: remote hung up")
		w.providers = map[string]config.Provider{"work": withTokenEnv(stdProvider("gitlab", "example-namespace"), tokenEnv)}
		w.client.getErr = provider.ErrNotFound
		w.client.createRepo = &provider.Repository{CloneURL: "https://gitlab.com/example-namespace/demo.git"}
		return w.saveConfig()
	})
	ctx.Step(`^providers "([^"]*)" and "([^"]*)" are configured$`, func(a, b string) error {
		w.providers = map[string]config.Provider{a: withTokenEnv(stdProvider("github", "example-ns"), tokenEnv), b: withTokenEnv(stdProvider("gitlab", "example-ns"), tokenEnv)}
		return w.saveConfig()
	})
	ctx.Step(`^"([^"]*)" is the configured default$`, func(alias string) error {
		for k, p := range w.providers {
			p.Default = k == alias
			w.providers[k] = p
		}
		return w.saveConfig()
	})
	ctx.Step(`^only one (\w+) provider named "([^"]*)" is configured$`, func(pType, alias string) error {
		w.providers = map[string]config.Provider{alias: withTokenEnv(stdProvider(strings.ToLower(pType), "example-ns"), tokenEnv)}
		return w.saveConfig()
	})
	ctx.Step(`^no default provider is configured$`, func() error {
		for k, p := range w.providers {
			p.Default = false
			w.providers[k] = p
		}
		return w.saveConfig()
	})
	ctx.Step(`^"([^"]*)" is otherwise selectable$`, func(alias string) error {
		p := w.providers[alias]
		p.Default = true
		w.providers[alias] = p
		return w.saveConfig()
	})
	ctx.Step(`^provider selection encounters "([^"]*)"$`, func(condition string) error {
		switch condition {
		case "an unknown explicit alias":
			w.run("colt init demo --local --provider ghost")
		case "multiple configured defaults":
			raw := "providers:\n  personal:\n    type: github\n    host: github.com\n    base_url: https://api.github.com\n    namespace: example-ns\n    visibility: private\n    git_name: Example User\n    git_email: user@example.invalid\n    auth:\n      source: env\n      token_env: " + tokenEnv + "\n    default: true\n" +
				"  work:\n    type: gitlab\n    host: gitlab.com\n    base_url: https://gitlab.com\n    namespace: example-ns\n    visibility: private\n    git_name: Example User\n    git_email: user@example.invalid\n    auth:\n      source: env\n      token_env: " + tokenEnv + "\n    default: true\n"
			if err := os.WriteFile(w.configPath, []byte(raw), 0o600); err != nil {
				return err
			}
			w.run("colt init demo --local")
		case "an invalid selected provider":
			raw := "providers:\n  personal:\n    type: gitea\n    host: gitea.example.invalid\n    base_url: https://gitea.example.invalid\n    namespace: example-ns\n    visibility: private\n    git_name: Example User\n    git_email: user@example.invalid\n    auth:\n      source: env\n      token_env: " + tokenEnv + "\n    default: true\n"
			if err := os.WriteFile(w.configPath, []byte(raw), 0o600); err != nil {
				return err
			}
			w.run("colt init demo --local")
		default:
			return fmt.Errorf("bdd: unknown condition %q", condition)
		}
		return nil
	})

	// actions
	runCmd := func(cmdLine string) error { w.run(cmdLine); return nil }
	ctx.Step(`^I run "([^"]+)"$`, runCmd)
	ctx.Step("^I run `([^`]+)`$", runCmd)
	ctx.Step(`^pushing the initial branch fails$`, func() error { w.run("colt init demo"); return nil })
	ctx.Step(`^the Colt credential helper is configured twice$`, func() error {
		dst := filepath.Join(w.dir, "demo")
		commandCtx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		if err := (gitnative.Native{}).ConfigureCredentialHelper(commandCtx, dst); err != nil {
			return err
		}
		return (gitnative.Native{}).ConfigureCredentialHelper(commandCtx, dst)
	})
	ctx.Step(`^I run a command with provider "([^"]*)"$`, func(alias string) error {
		w.run("colt init demo --local --provider " + alias)
		return nil
	})
	ctx.Step(`^I run a command without a provider option$`, func() error {
		w.run("colt init demo --local")
		return nil
	})

	// assertions: local git state
	ctx.Step(`^"([^"]*)" is a new Git repository with no remote$`, func(name string) error {
		dst := filepath.Join(w.dir, name)
		if _, err := gitOut(dst, "rev-parse", "--git-dir"); err != nil {
			return fmt.Errorf("not a git repository: %v", err)
		}
		remotes, err := gitOut(dst, "remote")
		if err != nil || remotes != "" {
			return fmt.Errorf("expected no remotes, got %q, %v", remotes, err)
		}
		return nil
	})
	ctx.Step(`^it has exactly one initial commit$`, func() error {
		n, err := gitOut(filepath.Join(w.dir, "demo"), "rev-list", "--count", "HEAD")
		if err != nil || n != "1" {
			return fmt.Errorf("expected one commit, got %q, %v", n, err)
		}
		return nil
	})
	ctx.Step(`^local Git user\.name and user\.email match the selected identity$`, func() error {
		dst := filepath.Join(w.dir, "demo")
		name, _ := gitOut(dst, "config", "--local", "user.name")
		email, _ := gitOut(dst, "config", "--local", "user.email")
		if name != "Example User" || email != "user@example.invalid" {
			return fmt.Errorf("identity mismatch: %q %q", name, email)
		}
		return nil
	})
	globalGitUnchanged := func() error {
		after, err := gitGlobalSnapshot()
		if err != nil {
			return fmt.Errorf("read global git configuration: %w", err)
		}
		if after != w.globalGit {
			return errors.New("global git configuration changed")
		}
		return nil
	}
	ctx.Step(`^global Git identity is unchanged$`, globalGitUnchanged)
	ctx.Step(`^global Git configuration is unchanged$`, globalGitUnchanged)
	ctx.Step(`^no provider mutation was requested$`, func() error {
		if len(w.newClientCalls) != 0 || len(w.client.calls) != 0 {
			return fmt.Errorf("provider was constructed or contacted: constructions=%d calls=%v", len(w.newClientCalls), w.client.calls)
		}
		return nil
	})
	ctx.Step(`^no provider client is constructed$`, func() error {
		if len(w.newClientCalls) != 0 {
			return fmt.Errorf("provider client constructions = %d", len(w.newClientCalls))
		}
		return nil
	})
	ctx.Step(`^no provider API credential is read$`, func() error {
		if w.store == nil {
			return errors.New("credential store fixture is not configured")
		}
		if w.store.gets != 0 {
			return fmt.Errorf("credential store reads = %d", w.store.gets)
		}
		return nil
	})
	ctx.Step(`^no provider client, credential helper, or push is invoked$`, func() error {
		if len(w.newClientCalls) != 0 || len(w.client.calls) != 0 || w.git.helpers != 0 || w.git.pushes != 0 {
			return fmt.Errorf("clients=%d provider=%v helpers=%d pushes=%d", len(w.newClientCalls), w.client.calls, w.git.helpers, w.git.pushes)
		}
		return nil
	})
	ctx.Step(`^local Git user\.name is "([^"]*)"$`, func(want string) error {
		got, _ := gitOut(filepath.Join(w.dir, "demo"), "config", "--local", "user.name")
		if got != want {
			return fmt.Errorf("user.name = %q, want %q", got, want)
		}
		return nil
	})
	ctx.Step(`^local Git user\.email is "([^"]*)"$`, func(want string) error {
		got, _ := gitOut(filepath.Join(w.dir, "demo"), "config", "--local", "user.email")
		if got != want {
			return fmt.Errorf("user.email = %q, want %q", got, want)
		}
		return nil
	})

	// assertions: failures
	ctx.Step(`^the command fails before mutation$`, func() error {
		if w.runErr == nil {
			return errors.New("expected failure, got success")
		}
		return nil
	})
	ctx.Step(`^the command fails before creating a destination or remote repository$`, func() error {
		if w.runErr == nil {
			return errors.New("expected failure, got success")
		}
		if _, err := os.Lstat(filepath.Join(w.dir, "invalid")); !os.IsNotExist(err) {
			return fmt.Errorf("destination was created: %v", err)
		}
		if w.client.called("Create") {
			return errors.New("remote creation was attempted")
		}
		return nil
	})
	ctx.Step(`^the existing destination is unchanged$`, func() error {
		data, err := os.ReadFile(filepath.Join(w.dir, "demo", "notes.txt"))
		if err != nil || string(data) != "user data" {
			return fmt.Errorf("user data changed: %q, %v", data, err)
		}
		if _, err := os.Lstat(filepath.Join(w.dir, "demo", ".git")); !os.IsNotExist(err) {
			return errors.New("destination was turned into a repository")
		}
		return nil
	})
	ctx.Step(`^the command fails before mutation with an actionable native git error$`, func() error {
		if w.runErr == nil || !strings.Contains(strings.ToLower(w.runErr.Error()), "git") {
			return fmt.Errorf("expected actionable git error, got %v", w.runErr)
		}
		if strings.Join(w.git.operations, ",") != "available" {
			return fmt.Errorf("Git operations after availability failure: %v", w.git.operations)
		}
		if _, err := os.Lstat(filepath.Join(w.dir, "demo")); !os.IsNotExist(err) {
			return fmt.Errorf("destination was mutated: %v", err)
		}
		return nil
	})
	ctx.Step(`^the command fails before changing the (regular file|symlink)$`, func(kind string) error {
		if w.runErr == nil {
			return errors.New("expected destination failure")
		}
		dst := filepath.Join(w.dir, "demo")
		info, err := os.Lstat(dst)
		if err != nil {
			return fmt.Errorf("destination disappeared: %v", err)
		}
		if kind == "regular file" {
			data, readErr := os.ReadFile(dst)
			if readErr != nil || !info.Mode().IsRegular() || string(data) != "user data" {
				return fmt.Errorf("regular file changed: mode=%v data=%q err=%v", info.Mode(), data, readErr)
			}
		} else {
			data, readErr := os.ReadFile(filepath.Join(w.dir, "symlink-target", "notes.txt"))
			if readErr != nil || info.Mode()&os.ModeSymlink == 0 || string(data) != "user data" {
				return fmt.Errorf("symlink or target changed: mode=%v data=%q err=%v", info.Mode(), data, readErr)
			}
		}
		if strings.Join(w.git.operations, ",") != "available" {
			return fmt.Errorf("unexpected Git mutation: %v", w.git.operations)
		}
		return nil
	})
	ctx.Step(`^no mutating Git operation or provider client construction occurs$`, func() error {
		if strings.Join(w.git.operations, ",") != "available" || len(w.newClientCalls) != 0 {
			return fmt.Errorf("operations occurred: git=%v clients=%d", w.git.operations, len(w.newClientCalls))
		}
		if _, err := os.Lstat(filepath.Join(w.dir, "demo")); !os.IsNotExist(err) {
			return fmt.Errorf("destination was created: %v", err)
		}
		return nil
	})
	ctx.Step(`^the command fails before creating a remote repository$`, func() error {
		if w.runErr == nil {
			return errors.New("expected failure, got success")
		}
		if w.client.called("Create") {
			return errors.New("remote creation was attempted")
		}
		return nil
	})
	ctx.Step(`^SSH Git access alone does not satisfy the requirement$`, func() error {
		if w.runErr == nil {
			return errors.New("expected failure, got success")
		}
		return nil
	})
	ctx.Step(`^no local repository, origin, helper, or push is attempted$`, func() error {
		if strings.Join(w.git.operations, ",") != "available" {
			return fmt.Errorf("unexpected Git operations: %v", w.git.operations)
		}
		if _, err := os.Lstat(filepath.Join(w.dir, "demo")); !os.IsNotExist(err) {
			return fmt.Errorf("local repository was created: %v", err)
		}
		return nil
	})
	ctx.Step(`^preflight state is unchanged with no credential, provider, or Git operation$`, func() error {
		if len(w.git.operations) != 0 || len(w.newClientCalls) != 0 || len(w.client.calls) != 0 {
			return fmt.Errorf("operations occurred: git=%v clients=%d provider=%v", w.git.operations, len(w.newClientCalls), w.client.calls)
		}
		if w.store != nil && (w.store.gets != 0 || w.store.puts != 0 || w.store.deletes != 0) {
			return fmt.Errorf("credential store changed or was read: gets=%d puts=%d deletes=%d", w.store.gets, w.store.puts, w.store.deletes)
		}
		if after, _ := os.ReadFile(w.configPath); !bytes.Equal(w.configBefore, after) {
			return errors.New("provider configuration changed")
		}
		if _, err := os.Lstat(filepath.Join(w.dir, "demo")); !os.IsNotExist(err) {
			return fmt.Errorf("destination was mutated: %v", err)
		}
		return globalGitUnchanged()
	})

	// assertions: remote flows
	ctx.Step(`^Colt creates repository "([^"]*)" in the selected namespace through the (\w+) HTTP API$`, func(project, pType string) error {
		if w.runErr != nil {
			return fmt.Errorf("init failed: %v", w.runErr)
		}
		if !w.client.called("Create") {
			return fmt.Errorf("no Create call: %v", w.client.calls)
		}
		want := "https://" + map[string]string{"GitHub": "github.com", "GitLab": "gitlab.com"}[pType] + "/example-namespace/demo.git"
		if len(w.git.origins) != 1 || w.git.origins[0] != want {
			return fmt.Errorf("origin = %v, want [%s]", w.git.origins, want)
		}
		return nil
	})
	ctx.Step(`^the created repository URL is the only "origin"$`, func() error {
		if len(w.git.origins) != 1 {
			return fmt.Errorf("origins = %v", w.git.origins)
		}
		return nil
	})
	ctx.Step(`^the initial commit is submitted once through the configured Git runner$`, func() error {
		if w.git.pushes != 1 {
			return fmt.Errorf("pushes = %d", w.git.pushes)
		}
		if !strings.HasSuffix(w.git.pushURL, "/demo.git") {
			return fmt.Errorf("push URL = %q", w.git.pushURL)
		}
		return nil // Runner.Push owns native --set-upstream arguments; its unit test covers those exact process arguments.
	})
	ctx.Step(`^the "origin" URL is "([^"]*)" with no credential in the URL$`, func(want string) error {
		got, err := gitOut(filepath.Join(w.dir, "demo"), "remote", "get-url", "origin")
		if err != nil || got != want || strings.Contains(got, "@") {
			return fmt.Errorf("origin = %q, want %q: %v", got, want, err)
		}
		return nil
	})
	ctx.Step(`^the "origin" URL is "([^"]*)"$`, func(want string) error {
		got, err := gitOut(filepath.Join(w.dir, "demo"), "remote", "get-url", "origin")
		if err != nil || got != want {
			return fmt.Errorf("origin = %q, want %q: %v", got, want, err)
		}
		return nil
	})
	ctx.Step(`^the SSH user is "git", not the provider account name$`, func() error {
		if !strings.HasPrefix(w.git.pushURL, "git@") || strings.HasPrefix(w.git.pushURL, w.client.account+"@") {
			return fmt.Errorf("SSH target uses the wrong user: %q", w.git.pushURL)
		}
		return nil
	})
	ctx.Step(`^native Git handles SSH authentication with no provider token or Colt credential helper injection$`, func() error {
		if w.git.pushes != 1 || w.git.pushToken != "" || w.git.helpers != 0 {
			return fmt.Errorf("SSH push used Colt HTTP authentication: pushes=%d token=%q helpers=%d", w.git.pushes, w.git.pushToken, w.git.helpers)
		}
		if helper, err := gitOut(filepath.Join(w.dir, "demo"), "config", "--local", "--get-all", "credential.helper"); err == nil || helper != "" {
			return fmt.Errorf("SSH repository configured a credential helper: %q", helper)
		}
		return nil
	})
	ctx.Step(`^local Git configuration contains "credential\.helper = colt"$`, func() error {
		got, err := gitOut(filepath.Join(w.dir, "demo"), "config", "--local", "--get-all", "credential.helper")
		if err != nil || !strings.Contains(got, "!colt git-credential") || w.git.helpers != 1 {
			return fmt.Errorf("local credential helpers = %q: %v", got, err)
		}
		return nil
	})
	ctx.Step(`^"\.git/config" contains no reusable credential value$`, func() error {
		data, err := os.ReadFile(filepath.Join(w.dir, "demo", ".git", "config"))
		if err != nil {
			return err
		}
		if secret := w.leaked(string(data)); secret != "" {
			return errors.New(".git/config contains a reusable credential value")
		}
		return nil
	})
	ctx.Step(`^ordinary Git invokes the Colt credential helper to resolve credentials for a later push$`, func() error {
		binary := filepath.Join(w.dir, "colt")
		buildCtx, cancelBuild := context.WithTimeout(context.Background(), commandTimeout)
		defer cancelBuild()
		build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "../cmd/colt")
		if output, err := build.CombinedOutput(); err != nil {
			return fmt.Errorf("build Colt helper: %v: %s", err, output)
		}
		gitCtx, cancelGit := context.WithTimeout(context.Background(), commandTimeout)
		defer cancelGit()
		cmd := exec.CommandContext(gitCtx, "git", "credential", "fill")
		cmd.Dir = filepath.Join(w.dir, "demo")
		cmd.Env = append(os.Environ(), "COLT_CONFIG="+w.configPath, "PATH="+w.dir+string(os.PathListSeparator)+os.Getenv("PATH"), "GIT_TERMINAL_PROMPT=0")
		cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\npath=example-user/demo.git\n\n")
		output, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "username=x-access-token\n") || !strings.Contains(string(output), "password=bdd-fake-secret\n") {
			return fmt.Errorf("ordinary Git did not resolve through Colt: %v", err)
		}
		return nil
	})
	ctx.Step(`^the provider conflict is authoritative$`, func() error {
		if w.runErr == nil || !strings.Contains(strings.ToLower(w.runErr.Error()), "conflict") {
			return fmt.Errorf("expected authoritative conflict, got %v", w.runErr)
		}
		return nil
	})
	ctx.Step(`^Colt does not adopt, replace, or delete the remote repository$`, func() error {
		if strings.Join(w.client.calls, ",") != "Get:demo,Create:demo" || len(w.git.origins) != 0 || w.git.pushes != 0 {
			return fmt.Errorf("unexpected conflict handling: provider=%v origins=%v pushes=%d", w.client.calls, w.git.origins, w.git.pushes)
		}
		return nil
	})
	ctx.Step(`^Colt preserves completed local work and reports the conflict$`, func() error {
		data, err := os.ReadFile(filepath.Join(w.dir, "demo", ".git", "COMMIT"))
		if err != nil || string(data) != "fake" || strings.Join(w.git.operations, ",") != "available,init,identity,commit" {
			return fmt.Errorf("completed local work missing: data=%q err=%v operations=%v", data, err, w.git.operations)
		}
		if w.runErr == nil || !strings.Contains(w.runErr.Error(), "conflict") {
			return fmt.Errorf("conflict not reported: %v", w.runErr)
		}
		return nil
	})
	ctx.Step(`^the command fails without changing that remote repository$`, func() error {
		if w.runErr == nil {
			return errors.New("expected failure, got success")
		}
		if w.client.called("Create") {
			return errors.New("remote was touched")
		}
		return nil
	})
	ctx.Step(`^Colt does not adopt the existing remote$`, func() error {
		if len(w.git.origins) != 0 {
			return fmt.Errorf("adopted remote: %v", w.git.origins)
		}
		return nil
	})
	ctx.Step(`^the command returns failure and identifies the failed push$`, func() error {
		if w.runErr == nil || !strings.Contains(w.runErr.Error(), "push") {
			return fmt.Errorf("expected push failure, got %v", w.runErr)
		}
		return nil
	})
	ctx.Step(`^the local commit, "origin", and remote repository remain intact$`, func() error {
		if strings.Join(w.client.calls, ",") != "Get:demo,Create:demo" || len(w.git.origins) != 1 || w.git.pushes != 0 {
			return fmt.Errorf("partial state lost: calls=%v origins=%v", w.client.calls, w.git.origins)
		}
		if data, err := os.ReadFile(filepath.Join(w.dir, "demo", ".git", "COMMIT")); err != nil || string(data) != "fake" {
			return fmt.Errorf("local commit missing: %q, %v", data, err)
		}
		return nil
	})
	ctx.Step(`^clone target validation fails with the initial commit preserved$`, func() error {
		if w.runErr == nil || !strings.Contains(w.runErr.Error(), "validate remote repository") {
			return fmt.Errorf("expected clone target validation failure, got %v", w.runErr)
		}
		if strings.Join(w.client.calls, ",") != "Get:demo,Create:demo" {
			return fmt.Errorf("provider calls = %v", w.client.calls)
		}
		data, err := os.ReadFile(filepath.Join(w.dir, "demo", ".git", "COMMIT"))
		if err != nil || string(data) != "fake" {
			return fmt.Errorf("initial commit not preserved: %q, %v", data, err)
		}
		return nil
	})
	ctx.Step(`^no origin, credential helper, or push is attempted$`, func() error {
		if len(w.git.origins) != 0 || w.git.helpers != 0 || w.git.pushes != 0 || strings.Join(w.git.operations, ",") != "available,init,identity,commit" {
			return fmt.Errorf("unsafe Git operations: %v origins=%v helpers=%d pushes=%d", w.git.operations, w.git.origins, w.git.helpers, w.git.pushes)
		}
		return nil
	})
	ctx.Step(`^the existing helper remains and the Colt helper appears exactly once locally$`, func() error {
		got, err := gitOut(filepath.Join(w.dir, "demo"), "config", "--local", "--get-all", "credential.helper")
		if err != nil || got != "existing-helper\n!colt git-credential" {
			return fmt.Errorf("credential helpers = %q: %v", got, err)
		}
		return nil
	})
	ctx.Step(`^credential\.useHttpPath is true in repository-local configuration$`, func() error {
		dst := filepath.Join(w.dir, "demo")
		local, err := gitOut(dst, "config", "--local", "--get", "credential.useHttpPath")
		if err != nil || local != "true" {
			return fmt.Errorf("local credential.useHttpPath = %q: %v", local, err)
		}
		return nil
	})
	ctx.Step(`^the result reports local and remote state and a safe recovery action$`, func() error {
		msg := fmt.Sprint(w.runErr)
		if !strings.Contains(msg, "local state") || !strings.Contains(msg, "recovery") {
			return fmt.Errorf("failure does not report state/recovery: %v", w.runErr)
		}
		return nil
	})

	// assertions: provider resolution
	ctx.Step(`^Colt selects provider "([^"]*)"$`, func(alias string) error {
		if w.runErr != nil {
			return fmt.Errorf("command failed: %v", w.runErr)
		}
		if !strings.Contains(w.out, "with provider "+alias) {
			return fmt.Errorf("provider %q not selected in %q", alias, w.out)
		}
		return nil
	})
	ctx.Step(`^output identifies the authenticated account as "([^"]*)"$`, func(account string) error {
		if !strings.Contains(w.out, "authenticated as "+account) {
			return fmt.Errorf("account %q not identified in %q", account, w.out)
		}
		return nil
	})
	ctx.Step(`^the command fails without selecting a provider$`, func() error {
		if w.runErr == nil {
			return fmt.Errorf("expected failure, got %q", w.out)
		}
		return nil
	})
	ctx.Step(`^the error requires an explicit provider or one configured default$`, func() error {
		if w.runErr == nil || !strings.Contains(strings.ToLower(w.runErr.Error()), "provider") {
			return fmt.Errorf("expected provider-selection error, got %v", w.runErr)
		}
		return nil
	})
	ctx.Step(`^the command fails without selecting "([^"]*)"$`, func(alias string) error {
		if w.runErr == nil {
			return errors.New("expected failure, got success")
		}
		if strings.Contains(w.out, "with provider "+alias) {
			return fmt.Errorf("incorrectly selected %q", alias)
		}
		return nil
	})
}

func withDefault(p config.Provider, d bool) config.Provider {
	p.Default = d
	return p
}
