// Package fixture holds the shared BDD world: scenario state, fake Git and
// provider doubles, and test helpers. Step definitions in ../steps drive
// these; the suite in .. wires them to godog.
package fixture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MozeBaltyk/Colt/internal/app"
	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	"github.com/MozeBaltyk/Colt/internal/provider"
)

const (
	TokenEnv       = "COLT_BDD_TOKEN"
	CommandTimeout = 30 * time.Second
)

// World is reset for every scenario by the Before hook.
type World struct {
	Dir         string
	ConfigPath  string
	App         *app.App
	Git         *FakeGit
	Client      *FakeClient
	Credentials credential.Store
	NewClient   func(config.Provider, string) (provider.Client, error)
	Out         string
	RunErr      error
	GlobalGit   string
	Providers   map[string]config.Provider
	// Login context for `colt auth login` steps.
	LoginAlias  string
	LoginType   string
	PendingHost string
	PendingBase string
	// Recorded construction calls: proves which credential reached the client.
	NewClientCalls      []NewClientCall
	ConfigBefore        []byte
	Secrets             []string
	SavedEnv            map[string]*string
	RedirectB           *RedirectTrap
	RedirectClose       func()
	RepeatedParsingSame bool
	Store               *FakeCredentialStore
	HelperOperation     string
	HelperInput         string
	SSHState            map[string][]byte
	SSHAgent            string
}

type NewClientCall struct {
	Provider config.Provider
	Token    string
}

// RedirectTrap is the loopback pair for the redirect-refusal scenario:
// server A redirects to server B, which records whether credentials arrived.
type RedirectTrap struct {
	A, B    *httptest.Server
	SawAuth bool
}

var CredentialEnvVars = []string{TokenEnv, "GITHUB_TOKEN", "GITLAB_TOKEN", "COMPANY_GL_TOKEN"}

var SandboxEnvVars = []string{
	"SSH_AUTH_SOCK",
	"HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "GIT_TERMINAL_PROMPT",
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "all_proxy", "no_proxy",
}

// ManagedEnvVars are restored by the After hook.
var ManagedEnvVars = append(append([]string(nil), CredentialEnvVars...), SandboxEnvVars...)

func UnsetCredentialEnv() error {
	var unsetErr error
	for _, name := range CredentialEnvVars {
		if err := os.Unsetenv(name); err != nil {
			unsetErr = errors.Join(unsetErr, fmt.Errorf("unset %s: %w", name, err))
		}
	}
	return unsetErr
}

func (w *World) Reset(t *testing.T) {
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
	w.Dir = dir
	w.ConfigPath = filepath.Join(dir, "config.yaml")
	w.App = nil
	w.Git = &FakeGit{Real: true}
	w.Client = &FakeClient{Account: "example-user"}
	w.Credentials = nil
	w.Out = ""
	w.RunErr = nil
	w.GlobalGit = ""
	w.Providers = map[string]config.Provider{}
	w.LoginAlias, w.LoginType = "work", "gitlab"
	w.PendingHost, w.PendingBase = "", ""
	w.NewClientCalls = nil
	w.ConfigBefore = nil
	w.Secrets = nil
	w.SavedEnv = SaveEnv()
	w.RedirectB = nil
	w.RedirectClose = nil
	w.RepeatedParsingSame = false
	w.Store = nil
	w.HelperOperation, w.HelperInput = "", ""
	w.SSHState = nil
	w.SSHAgent = ""
	w.NewClient = func(p config.Provider, token string) (provider.Client, error) {
		w.NewClientCalls = append(w.NewClientCalls, NewClientCall{Provider: p, Token: token})
		return w.Client, nil
	}

	SetEnv(t, "HOME", home)
	SetEnv(t, "GIT_CONFIG_GLOBAL", os.DevNull)
	SetEnv(t, "GIT_CONFIG_NOSYSTEM", "1")
	SetEnv(t, "GIT_TERMINAL_PROMPT", "0")
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		SetEnv(t, name, "http://127.0.0.1:1")
	}
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		SetEnv(t, name, "localhost,127.0.0.1")
	}
	SetEnv(t, TokenEnv, "bdd-fake-secret")
	w.Secrets = []string{"bdd-fake-secret"}
}

func SetEnv(t *testing.T, name, value string) {
	t.Helper()
	if err := os.Setenv(name, value); err != nil {
		t.Fatalf("set %s: %v", name, err)
	}
}

func (w *World) BuildApp() {
	w.App = &app.App{
		ConfigPath:  w.ConfigPath,
		WorkDir:     w.Dir,
		Git:         w.Git,
		NewClient:   w.NewClient,
		Credentials: w.Credentials,
	}
}

func (w *World) SaveConfig() error {
	return config.Save(w.ConfigPath, config.Config{Providers: w.Providers})
}

func (w *World) Run(cmdLine string) {
	fields, err := SplitCmd(cmdLine)
	if err != nil {
		w.RunErr = err
		return
	}
	if len(fields) < 2 || fields[0] != "colt" {
		w.RunErr = fmt.Errorf("bdd: cannot run %q", cmdLine)
		return
	}
	w.RunArgs(fields[1:])
}

func (w *World) RunArgs(args []string) {
	w.ConfigBefore, _ = os.ReadFile(w.ConfigPath)
	w.BuildApp()
	cmd := w.App.Root()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	ctx, cancel := context.WithTimeout(context.Background(), CommandTimeout)
	defer cancel()
	w.RunErr = cmd.ExecuteContext(ctx)
	w.Out = buf.String()
}

// LoginArgs builds the full login invocation from scenario context. Login
// flags (namespace, identity) are required by the CLI, so feature steps
// that say `colt auth login <type> <alias>` map to this complete form.
func (w *World) LoginArgs(extra ...string) []string {
	args := []string{"auth", "login", w.LoginType, w.LoginAlias,
		"--namespace", "example-ns",
		"--git-name", "Example User",
		"--git-email", "user@example.invalid"}
	if w.PendingHost != "" {
		args = append(args, "--host", w.PendingHost, "--base-url", w.PendingBase)
	}
	// ponytail: login builds its candidate from flags, not saved config,
	// so carry the configured token_env over explicitly.
	if p, ok := w.Providers[w.LoginAlias]; ok && p.Auth.TokenEnv != "" {
		args = append(args, "--token-env", p.Auth.TokenEnv)
	}
	return append(args, extra...)
}

func (w *World) LastToken() (string, bool) {
	if len(w.NewClientCalls) == 0 {
		return "", false
	}
	return w.NewClientCalls[len(w.NewClientCalls)-1].Token, true
}

func (w *World) RunHelper(operation, input string) {
	w.ConfigBefore, _ = os.ReadFile(w.ConfigPath)
	w.BuildApp()
	cmd := w.App.Root()
	var output strings.Builder
	cmd.SetIn(strings.NewReader(input))
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"git-credential", operation})
	ctx, cancel := context.WithTimeout(context.Background(), CommandTimeout)
	defer cancel()
	w.RunErr = cmd.ExecuteContext(ctx)
	w.Out = output.String()
}

// SetSecret exports a credential value for one step and tracks it so
// redaction assertions can verify it never leaks into output or files.
func (w *World) SetSecret(name, value string) {
	os.Setenv(name, value)
	w.Secrets = append(w.Secrets, value)
}

func (w *World) Leaked(s string) string {
	for _, secret := range w.Secrets {
		if secret != "" && strings.Contains(s, secret) {
			return secret
		}
	}
	return ""
}

func SaveEnv() map[string]*string {
	saved := map[string]*string{}
	for _, name := range ManagedEnvVars {
		if v, ok := os.LookupEnv(name); ok {
			v := v
			saved[name] = &v
		} else {
			saved[name] = nil
		}
	}
	return saved
}

func RestoreEnv(saved map[string]*string) error {
	var restoreErr error
	for _, name := range ManagedEnvVars {
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
