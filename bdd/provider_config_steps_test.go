package bdd

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/cucumber/godog"
)

// redirectTrap is the loopback pair for the redirect-refusal scenario:
// server A redirects to server B, which records whether credentials arrived.
type redirectTrap struct {
	a, b    *httptest.Server
	sawAuth bool
}

// loginArgs builds the full login invocation from scenario context. Login
// flags (namespace, identity) are required by the CLI, so feature steps
// that say `colt auth login <type> <alias>` map to this complete form.
func (w *world) loginArgs(extra ...string) []string {
	args := []string{"auth", "login", w.loginType, w.loginAlias,
		"--namespace", "example-ns",
		"--git-name", "Example User",
		"--git-email", "user@example.invalid"}
	if w.pendingHost != "" {
		args = append(args, "--host", w.pendingHost, "--base-url", w.pendingBase)
	}
	// ponytail: login builds its candidate from flags, not saved config,
	// so carry the configured token_env over explicitly.
	if p, ok := w.providers[w.loginAlias]; ok && p.Auth.TokenEnv != "" {
		args = append(args, "--token-env", p.Auth.TokenEnv)
	}
	return append(args, extra...)
}

func (w *world) lastToken() (string, bool) {
	if len(w.newClientCalls) == 0 {
		return "", false
	}
	return w.newClientCalls[len(w.newClientCalls)-1].token, true
}

func (w *world) runHelper(operation, input string) {
	w.configBefore, _ = os.ReadFile(w.configPath)
	w.buildApp()
	cmd := w.app.Root()
	var output strings.Builder
	cmd.SetIn(strings.NewReader(input))
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"git-credential", operation})
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	w.runErr = cmd.ExecuteContext(ctx)
	w.out = output.String()
}

func rawProviderYAML(alias, pType, host, base, extra string) string {
	return "providers:\n  " + alias + ":\n    type: " + pType + "\n    host: " + host +
		"\n    base_url: " + base + "\n    namespace: example-ns\n    visibility: private" +
		"\n    git_name: Example User\n    git_email: user@example.invalid\n" + extra
}

func registerProviderConfigSteps(ctx *godog.ScenarioContext, w *world) {
	// --- fixtures ---
	ctx.Step(`^provider "([^"]*)" has auth source "env" with token_env "([^"]*)"$`, func(alias, tokenEnv string) error {
		pType := "gitlab"
		if tokenEnv == "GITHUB_TOKEN" {
			pType = "github"
		}
		p := stdProvider(pType, "example-ns")
		p.Auth.TokenEnv = tokenEnv
		w.providers = map[string]config.Provider{alias: p}
		w.loginAlias, w.loginType = alias, pType
		return w.saveConfig()
	})
	ctx.Step(`^provider "([^"]*)" has auth source "stored" with credential_id "([^"]*)"$`, func(alias, credentialID string) error {
		p := stdProvider("github", "example-user")
		p.Auth = config.Auth{Source: "stored", CredentialID: credentialID}
		w.providers = map[string]config.Provider{alias: p}
		return w.saveConfig()
	})
	ctx.Step(`^a plaintext file credential store is explicitly injected with "([^"]*)"$`, func(id string) error {
		secret := "plaintext-fake-secret-1"
		w.secrets = append(w.secrets, secret)
		store := credential.NewFileStore(credential.DefaultFilePath(w.configPath))
		w.credentials = store // explicit test-only injection; production enrollment remains disabled
		return store.Put(id, credential.Credential{Kind: "bearer_token", Secret: secret})
	})
	ctx.Step(`^an injected credential store holds "([^"]*)" with secret "([^"]*)"$`, func(id, secret string) error {
		w.store = newFakeCredentialStore()
		w.store.credentials[id] = credential.Credential{Kind: "bearer_token", Secret: secret}
		w.credentials = w.store
		w.secrets = append(w.secrets, secret)
		return nil
	})
	ctx.Step(`^an empty injected credential store is supplied$`, func() error {
		w.store = newFakeCredentialStore()
		w.credentials = w.store
		return nil
	})
	ctx.Step(`^the injected credential store also holds "([^"]*)" with secret "([^"]*)"$`, func(id, secret string) error {
		if w.store == nil {
			return errors.New("injected credential store is not configured")
		}
		w.store.credentials[id] = credential.Credential{Kind: "bearer_token", Secret: secret}
		w.secrets = append(w.secrets, secret)
		return nil
	})
	ctx.Step(`^representative SSH configuration, key, and agent state exist$`, func() error {
		sshDir := filepath.Join(w.dir, ".ssh")
		if err := os.Mkdir(sshDir, 0o700); err != nil {
			return err
		}
		w.sshState = map[string][]byte{
			filepath.Join(sshDir, "config"):  []byte("Host github.com\n  IdentityFile ~/.ssh/id_test\n"),
			filepath.Join(sshDir, "id_test"): []byte("representative-private-key"),
		}
		for path, contents := range w.sshState {
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				return err
			}
		}
		w.sshAgent = filepath.Join(w.dir, "agent.sock")
		return os.Setenv("SSH_AUTH_SOCK", w.sshAgent)
	})
	ctx.Step(`^an injected credential store holds a credential containing a newline$`, func() error {
		w.store = newFakeCredentialStore()
		w.store.credentials["github.com/personal"] = credential.Credential{Kind: "bearer_token", Secret: "safe-prefix\nusername=attacker"}
		w.credentials = w.store
		return nil
	})
	ctx.Step(`^the injected credential store fails while reading$`, func() error {
		w.store = newFakeCredentialStore()
		w.store.getErr = errors.New("credential backend read failed")
		w.credentials = w.store
		return nil
	})
	ctx.Step(`^the plaintext fallback file declares "version: 1"$`, func() error {
		return os.WriteFile(credential.DefaultFilePath(w.configPath), []byte("version: 1\ncredentials: {}\n"), 0o600)
	})
	ctx.Step(`^it holds credential_id "([^"]*)"$`, func(id string) error {
		secret := "plaintext-fake-secret-1"
		w.secrets = append(w.secrets, secret)
		return credential.NewFileStore(credential.DefaultFilePath(w.configPath)).Put(id, credential.Credential{Kind: "bearer_token", Secret: secret})
	})
	ctx.Step(`^the plaintext credential file (is group-readable|is world-readable)$`, func(condition string) error {
		p := stdProvider("github", "example-ns")
		p.Auth = config.Auth{Source: "stored", CredentialID: "github.com/personal"}
		w.providers = map[string]config.Provider{"personal": p}
		if err := w.saveConfig(); err != nil {
			return err
		}
		os.Unsetenv("GITHUB_TOKEN")
		secret := "plaintext-fake-secret-1"
		w.secrets = append(w.secrets, secret)
		path := credential.DefaultFilePath(w.configPath)
		store := credential.NewFileStore(path)
		w.credentials = store // explicitly exercise the approved fallback
		if err := store.Put(p.Auth.CredentialID, credential.Credential{Kind: "bearer_token", Secret: secret}); err != nil {
			return err
		}
		mode := os.FileMode(0o640)
		if condition == "is world-readable" {
			mode = 0o604
		}
		return os.Chmod(path, mode)
	})
	ctx.Step(`^config\.yaml declares "([^"]*)" with value "([^"]*)"$`, func(field, value string) error {
		raw := rawProviderYAML("work", "gitlab", "gitlab.com", "https://gitlab.com", "    auth:\n      source: env\n      token_env: "+tokenEnv+"\n") +
			"    " + field + ": " + value + "\n"
		return os.WriteFile(w.configPath, []byte(raw), 0o600) // ponytail: raw write bypasses Save validation on purpose
	})
	ctx.Step(`^provider "work" has an existing valid configuration$`, func() error {
		w.providers = map[string]config.Provider{"work": withTokenEnv(stdProvider("gitlab", "example-ns"), tokenEnv)}
		w.loginAlias, w.loginType = "work", "gitlab"
		return w.saveConfig()
	})
	ctx.Step(`^its referenced credential is invalid$`, func() error {
		w.setSecret(tokenEnv, "bdd-invalid-secret")
		w.client.authErr = fmt.Errorf("authentication failed: rejected credential")
		return nil
	})
	ctx.Step(`^provider "work" is already configured$`, func() error {
		w.providers = map[string]config.Provider{"work": stdProvider("gitlab", "example-ns")}
		w.loginAlias, w.loginType = "work", "gitlab"
		return w.saveConfig()
	})
	ctx.Step(`^a GitLab provider uses host "([^"]*)"$`, func(host string) error {
		w.loginAlias, w.loginType = "work", "gitlab"
		w.pendingHost, w.pendingBase = host, "https://"+host
		w.setSecret("GITLAB_TOKEN", "bdd-fake-secret") // a request needs a credential
		return nil
	})
	ctx.Step(`^"([^"]*)" contains "([^"]*)"$`, func(name, value string) error {
		w.setSecret(name, value)
		return nil
	})
	ctx.Step(`^"([^"]*)" contains a different temporary token "([^"]*)"$`, func(name, value string) error {
		w.setSecret(name, value)
		return nil
	})
	ctx.Step(`^a (\w+) provider has no explicit token_env and no persisted credential$`, func(pType string) error {
		kind := strings.ToLower(pType)
		w.providers = map[string]config.Provider{"work": stdProvider(kind, "example-ns")}
		w.loginAlias, w.loginType = "work", kind
		return w.saveConfig()
	})
	ctx.Step(`^environment variable "([^"]*)" contains a valid token$`, func(name string) error {
		w.setSecret(name, "bdd-valid-token")
		return nil
	})
	ctx.Step(`^provider "personal" has no persisted credential$`, func() error {
		w.providers = map[string]config.Provider{"personal": stdProvider("github", "example-ns")}
		w.loginAlias, w.loginType = "personal", "github"
		return w.saveConfig()
	})
	ctx.Step(`^"([^"]*)" is (unset|an empty value)$`, func(name, state string) error {
		if state == "unset" {
			os.Unsetenv(name)
		} else {
			os.Setenv(name, "")
		}
		return nil
	})
	ctx.Step(`^provider "work" is configured with a valid credential$`, func() error {
		w.providers = map[string]config.Provider{"work": withTokenEnv(stdProvider("gitlab", "example-ns"), tokenEnv)}
		w.loginAlias, w.loginType = "work", "gitlab"
		return w.saveConfig()
	})
	ctx.Step(`^provider API authentication succeeds for provider "work"$`, func() error {
		w.providers = map[string]config.Provider{"work": withTokenEnv(stdProvider("gitlab", "example-ns"), tokenEnv)}
		return w.saveConfig()
	})
	ctx.Step(`^provider "work" has no environment credential and no persisted credential$`, func() error {
		w.providers = map[string]config.Provider{"work": stdProvider("gitlab", "example-ns")}
		if err := unsetCredentialEnv(); err != nil {
			return err
		}
		return w.saveConfig()
	})
	ctx.Step(`^a provider is configured with a host that redirects to a different host$`, func() error {
		trap := &redirectTrap{}
		trap.b = httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			trap.sawAuth = trap.sawAuth || r.Header.Get("Authorization") != ""
		}))
		trap.a = httptest.NewTLSServer(http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
			http.Redirect(wr, r, trap.b.URL+"/api/v4/user", http.StatusFound)
		}))
		w.redirectB = trap
		w.redirectClose = func() { trap.a.Close(); trap.b.Close() }
		w.loginAlias, w.loginType = "work", "gitlab"
		w.pendingBase = trap.a.URL
		w.pendingHost = strings.TrimPrefix(trap.a.URL, "https://")
		trusting := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // ponytail: loopback test only
		w.newClient = func(p config.Provider, token string) (provider.Client, error) {
			w.newClientCalls = append(w.newClientCalls, newClientCall{provider: p, token: token})
			return provider.New(p, token, trusting)
		}
		return nil
	})
	ctx.Step(`^a valid credential is available$`, func() error {
		w.setSecret("GITLAB_TOKEN", "bdd-fake-secret")
		return nil
	})
	ctx.Step(`^the provider rejects authentication$`, func() error {
		w.client.authErr = errors.New("authentication failed: rejected credential")
		return nil
	})
	ctx.Step(`^the provider API is unreachable due to a network failure$`, func() error {
		w.client.authErr = errors.New("network failure: provider API unreachable")
		return nil
	})
	ctx.Step(`^the provider API is unreachable$`, func() error {
		w.client.authErr = errors.New("provider API unreachable")
		return nil
	})
	ctx.Step(`^no providers are configured$`, func() error {
		return config.Save(w.configPath, config.Config{Providers: map[string]config.Provider{}})
	})
	ctx.Step(`^providers "work" and "personal" are configured out of alias order$`, func() error {
		personal := stdProvider("github", "example-ns")
		personal.Default = true
		w.providers = map[string]config.Provider{
			"work":     stdProvider("gitlab", "example-ns"),
			"personal": personal,
		}
		return w.saveConfig()
	})
	ctx.Step(`^their credential environment variables are unset$`, func() error {
		return unsetCredentialEnv()
	})
	ctx.Step(`^a locally initialized repository using provider "([^"]*)"$`, func(alias string) error {
		w.providers = map[string]config.Provider{alias: withTokenEnv(stdProvider("gitlab", "example-ns"), tokenEnv)}
		if err := w.saveConfig(); err != nil {
			return err
		}
		w.run("colt init demo --local")
		return w.runErr
	})

	// --- actions ---
	ctx.Step(`^I try to log in to another provider named "([^"]*)"$`, func(alias string) error {
		w.loginAlias = alias
		w.runArgs(w.loginArgs())
		return nil
	})
	ctx.Step("^I run `colt auth login (\\w+) ([\\w-]+) --replace` with the new settings$", func(pType, alias string) error {
		w.loginAlias, w.loginType = alias, pType
		w.runArgs(w.loginArgs("--replace"))
		return nil
	})
	ctx.Step(`^Colt loads the configuration$`, func() error {
		w.run("colt auth status --offline")
		return nil
	})
	ctx.Step(`^Colt authenticates the provider$`, func() error {
		// ponytail: --replace keeps the focus on authentication; alias
		// collision is covered by its own scenario.
		w.runArgs(w.loginArgs("--replace"))
		return nil
	})
	ctx.Step(`^Colt authenticates provider "([^"]*)"$`, func(alias string) error {
		w.loginAlias = alias
		if p, ok := w.providers[alias]; ok {
			w.loginType = p.Type
			if p.Auth.Source == "stored" {
				w.run("colt auth status")
				return nil
			}
		}
		w.runArgs(w.loginArgs("--replace"))
		return nil
	})
	ctx.Step(`^provider authentication fails$`, func() error {
		w.client.authErr = fmt.Errorf("authentication failed: rejected credential")
		w.runArgs(w.loginArgs("--replace"))
		return nil
	})
	ctx.Step(`^authentication is attempted against the provider$`, func() error {
		w.runArgs(w.loginArgs())
		return nil
	})
	ctx.Step(`^Colt parses the credential file$`, func() error {
		store := credential.NewFileStore(credential.DefaultFilePath(w.configPath))
		first, err := store.Get("github.com/personal")
		if err != nil {
			return err
		}
		second, err := store.Get("github.com/personal")
		w.repeatedParsingSame = err == nil && first == second
		return err
	})
	ctx.Step(`^Colt resolves the persisted credential$`, func() error {
		w.run("colt init demo")
		return nil
	})
	ctx.Step(`^Git requests "protocol=https host=github\.com path=example-user/example-project\.git"$`, func() error {
		w.runHelper("get", "protocol=https\nhost=github.com\npath=example-user/example-project.git\n\n")
		return nil
	})
	ctx.Step(`^Git requests the provider credential$`, func() error {
		w.runHelper("get", "protocol=https\nhost=github.com\npath=example-user/example-project.git\n\n")
		return nil
	})
	ctx.Step(`^Git requests credentials for "([^"]*)"$`, func(host string) error {
		w.runHelper("get", "protocol=https\nhost="+host+"\npath=example-user/example-project.git\n\n")
		return nil
	})
	ctx.Step(`^Git invokes the helper with "store" for an unknown credential$`, func() error {
		w.helperOperation = "store"
		w.helperInput = "protocol=https\nhost=example.org\npath=other/project.git\npassword=unknown-secret\n\n"
		return nil
	})
	ctx.Step(`^Git invokes the helper with "erase" after a failed authentication$`, func() error {
		w.helperOperation = "erase"
		w.helperInput = "protocol=https\nhost=github.com\npath=example-user/example-project.git\n\n"
		return nil
	})
	ctx.Step(`^Git supplies a malformed credential request containing "([^"]*)"$`, func(secret string) error {
		w.store = newFakeCredentialStore()
		w.credentials = w.store
		w.helperOperation = "get"
		w.helperInput = "password=" + secret + "\nmalformed\n"
		w.secrets = append(w.secrets, secret)
		return nil
	})
	ctx.Step(`^the helper handles the invocation$`, func() error {
		w.runHelper(w.helperOperation, w.helperInput)
		return nil
	})

	// --- assertions ---
	ctx.Step(`^config\.yaml contains "([^"]*)"$`, func(want string) error {
		data, err := os.ReadFile(w.configPath)
		if err != nil || !strings.Contains(string(data), want) {
			return fmt.Errorf("config lacks %q: %v", want, err)
		}
		return nil
	})
	ctx.Step(`^config\.yaml contains no reusable credential value$`, func() error {
		data, err := os.ReadFile(w.configPath)
		if err != nil {
			return err
		}
		if secret := w.leaked(string(data)); secret != "" {
			return fmt.Errorf("config leaks a credential value")
		}
		return nil
	})
	ctx.Step(`^the load fails with a configuration error$`, func() error {
		if w.runErr == nil || !strings.Contains(strings.ToLower(w.runErr.Error()), "config") {
			return fmt.Errorf("expected configuration error, got %v", w.runErr)
		}
		return nil
	})
	ctx.Step(`^no authentication is attempted$`, func() error {
		if len(w.newClientCalls) != 0 || len(w.client.calls) != 0 {
			return fmt.Errorf("authentication was attempted: %v", w.client.calls)
		}
		return nil
	})
	ctx.Step(`^the command fails with an authentication error$`, func() error {
		if w.runErr == nil || !strings.Contains(strings.ToLower(w.runErr.Error()), "authentication") {
			return fmt.Errorf("expected authentication error, got %v", w.runErr)
		}
		return nil
	})
	ctx.Step(`^the prior provider configuration is unchanged$`, func() error {
		data, err := os.ReadFile(w.configPath)
		if err != nil || string(data) != string(w.configBefore) {
			return fmt.Errorf("prior configuration changed")
		}
		return nil
	})
	ctx.Step(`^provider configuration is unchanged$`, func() error {
		data, err := os.ReadFile(w.configPath)
		if err != nil || string(data) != string(w.configBefore) {
			return fmt.Errorf("provider configuration changed")
		}
		return nil
	})
	ctx.Step(`^the provider receives the resolved credential without knowing its backend type$`, func() error {
		token, ok := w.lastToken()
		if !ok || token != "plaintext-fake-secret-1" {
			return fmt.Errorf("provider did not receive the fallback credential")
		}
		return nil
	})
	ctx.Step(`^exactly credential "([^"]*)" is deleted from the injected store$`, func(id string) error {
		if w.store == nil || w.store.gets != 0 || w.store.puts != 0 || w.store.deletes != 1 || len(w.store.deletedIDs) != 1 || w.store.deletedIDs[0] != id {
			return fmt.Errorf("credential store calls = %#v", w.store)
		}
		if _, exists := w.store.credentials[id]; exists {
			return fmt.Errorf("credential %q still exists", id)
		}
		return nil
	})
	ctx.Step(`^credential "([^"]*)" remains unchanged$`, func(id string) error {
		stored, exists := w.store.credentials[id]
		if !exists || stored.Secret != "neighbor-logout-secret" {
			return fmt.Errorf("neighbor credential %q changed", id)
		}
		return nil
	})
	ctx.Step(`^the environment variable is unchanged$`, func() error {
		if os.Getenv("GITHUB_TOKEN") != "logout-env-secret" {
			return errors.New("environment credential changed")
		}
		return nil
	})
	ctx.Step(`^no SSH key, SSH configuration, or ssh-agent state is modified$`, func() error {
		if len(w.sshState) == 0 || os.Getenv("SSH_AUTH_SOCK") != w.sshAgent {
			return errors.New("SSH agent state changed or was not observed")
		}
		for path, before := range w.sshState {
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				return fmt.Errorf("SSH state changed at %s: %v", path, err)
			}
		}
		return nil
	})
	ctx.Step(`^output explains the environment credential still resolves without revealing it$`, func() error {
		if !strings.Contains(w.out, "GITHUB_TOKEN may still provide credentials") || strings.Contains(w.out, os.Getenv("GITHUB_TOKEN")) {
			return fmt.Errorf("unsafe or unclear logout output: %q", w.out)
		}
		return nil
	})
	ctx.Step(`^logout warns that "([^"]*)" may still resolve without revealing its value$`, func(name string) error {
		if !strings.Contains(w.out, name+" may still provide credentials") || strings.Contains(w.out, os.Getenv(name)) {
			return fmt.Errorf("unsafe or missing environment warning: %q", w.out)
		}
		return nil
	})
	ctx.Step("^`colt auth status` still authenticates via \"([^\"]*)\"$", func(name string) error {
		if len(w.newClientCalls) != 0 || len(w.client.calls) != 0 || len(w.git.operations) != 0 {
			return fmt.Errorf("logout contacted provider or Git: clients=%d provider=%v git=%v", len(w.newClientCalls), w.client.calls, w.git.operations)
		}
		w.run("colt auth status")
		if w.runErr != nil || len(w.newClientCalls) != 1 || w.newClientCalls[0].token != os.Getenv(name) || !w.client.called("Authenticate") {
			return fmt.Errorf("status did not authenticate via %s: error=%v clients=%v provider=%v", name, w.runErr, w.newClientCalls, w.client.calls)
		}
		return nil
	})
	ctx.Step(`^the injected stored credential is unchanged$`, func() error {
		if w.store == nil || len(w.store.credentials) != 1 || w.store.puts != 0 || w.store.deletes != 0 {
			return fmt.Errorf("credential store changed: %#v", w.store)
		}
		return nil
	})
	ctx.Step(`^the injected stored credential is unchanged and was not read$`, func() error {
		if w.store == nil || len(w.store.credentials) != 1 || w.store.gets != 0 || w.store.puts != 0 || w.store.deletes != 0 {
			return fmt.Errorf("credential store was accessed or changed: %#v", w.store)
		}
		return nil
	})
	ctx.Step(`^repeated parsing returns the same credential$`, func() error {
		if !w.repeatedParsingSame {
			return fmt.Errorf("repeated credential parsing differed")
		}
		return nil
	})
	ctx.Step(`^config\.yaml remains free of reusable credential values$`, func() error {
		data, _ := os.ReadFile(w.configPath)
		if secret := w.leaked(string(data)); secret != "" {
			return fmt.Errorf("config leaks a credential value")
		}
		return nil
	})
	ctx.Step(`^Colt fails safely without exposing the secret value$`, func() error {
		if w.runErr == nil || w.leaked(w.runErr.Error()) != "" || len(w.newClientCalls) != 0 {
			return fmt.Errorf("unsafe credential failure: %v", w.runErr)
		}
		return nil
	})
	ctx.Step(`^normal, verbose, and error output do not contain "([^"]*)"$`, func(secret string) error {
		if strings.Contains(w.out, secret) || strings.Contains(fmt.Sprint(w.runErr), secret) {
			return fmt.Errorf("credential output leaked")
		}
		return nil
	})
	ctx.Step(`^the command fails with a duplicate alias error$`, func() error {
		if w.runErr == nil || !strings.Contains(w.runErr.Error(), "already exists") {
			return fmt.Errorf("expected duplicate alias error, got %v", w.runErr)
		}
		return nil
	})
	ctx.Step(`^the existing provider configuration is unchanged$`, func() error {
		data, err := os.ReadFile(w.configPath)
		if err != nil || string(data) != string(w.configBefore) {
			return fmt.Errorf("existing configuration changed")
		}
		return nil
	})
	ctx.Step(`^the request uses the configured self-hosted GitLab base URL$`, func() error {
		if len(w.newClientCalls) == 0 {
			return fmt.Errorf("no provider request was made")
		}
		if got := w.newClientCalls[0].provider.BaseURL; !strings.Contains(got, "gitlab.company.example") {
			return fmt.Errorf("base URL = %q", got)
		}
		return nil
	})
	ctx.Step(`^no request is sent to GitLab\.com$`, func() error {
		for _, c := range w.newClientCalls {
			u, err := url.Parse(c.provider.BaseURL)
			if err != nil || strings.EqualFold(u.Host, "gitlab.com") {
				return fmt.Errorf("request used %q", c.provider.BaseURL)
			}
		}
		return nil
	})
	ctx.Step(`^authentication used the value from "([^"]*)"$`, func(name string) error {
		token, ok := w.lastToken()
		if !ok || token != os.Getenv(name) {
			return fmt.Errorf("client did not receive the value from %q", name)
		}
		return nil
	})
	ctx.Step(`^normal and error output do not contain "([^"]*)"$`, func(secret string) error {
		if strings.Contains(w.out, secret) || strings.Contains(fmt.Sprint(w.runErr), secret) {
			return fmt.Errorf("output leaks a credential value")
		}
		return nil
	})
	ctx.Step(`^normal Colt configuration does not contain "([^"]*)"$`, func(secret string) error {
		data, _ := os.ReadFile(w.configPath) // ponytail: absent config contains nothing
		if strings.Contains(string(data), secret) {
			return fmt.Errorf("config leaks a credential value")
		}
		return nil
	})
	ctx.Step(`^authentication uses the value from "([^"]*)"$`, func(name string) error {
		tok, ok := w.lastToken()
		if !ok || tok != os.Getenv(name) {
			return fmt.Errorf("client did not receive the value from %q", name)
		}
		return nil
	})
	ctx.Step(`^authentication succeeds for this invocation$`, func() error {
		if w.runErr != nil {
			return fmt.Errorf("expected success, got %v", w.runErr)
		}
		return nil
	})
	ctx.Step(`^no Colt credential file is written$`, func() error {
		matches, _ := filepath.Glob(filepath.Join(w.dir, "credentials*"))
		if len(matches) != 0 {
			return fmt.Errorf("credential file written: %v", matches)
		}
		data, _ := os.ReadFile(w.configPath)
		if secret := w.leaked(string(data)); secret != "" {
			return fmt.Errorf("config leaks a credential value")
		}
		return nil
	})
	ctx.Step(`^the command fails before sending an authenticated request$`, func() error {
		if w.runErr == nil {
			return fmt.Errorf("expected failure, got success")
		}
		if len(w.newClientCalls) != 0 || len(w.client.calls) != 0 {
			return fmt.Errorf("authenticated request was attempted: %v", w.client.calls)
		}
		return nil
	})
	ctx.Step(`^no project or provider state is changed$`, func() error {
		data, _ := os.ReadFile(w.configPath) // ponytail: absent config counts as unchanged
		if string(data) != string(w.configBefore) {
			return fmt.Errorf("provider state changed")
		}
		if _, err := os.Lstat(filepath.Join(w.dir, "demo")); !os.IsNotExist(err) {
			return fmt.Errorf("project state changed")
		}
		return nil
	})
	ctx.Step(`^the command succeeds$`, func() error {
		if w.runErr != nil {
			return fmt.Errorf("expected success, got %v", w.runErr)
		}
		return nil
	})
	ctx.Step(`^status shows the authenticated account and "connected"$`, func() error {
		if !strings.Contains(w.out, "example-user") || !strings.Contains(w.out, "connected") {
			return fmt.Errorf("unexpected status output:\n%s", w.out)
		}
		return nil
	})
	ctx.Step(`^"connected" means only the provider API check passed$`, func() error {
		if !strings.Contains(w.out, "connected") || !w.client.called("Authenticate") || len(w.git.operations) != 0 {
			return fmt.Errorf("status was not API-only: provider=%v git=%v output=%q", w.client.calls, w.git.operations, w.out)
		}
		return nil
	})
	ctx.Step(`^no Git clone or push access is claimed$`, func() error {
		if strings.Contains(w.out, "clone") || strings.Contains(w.out, "push") || strings.Contains(w.out, "Git access") {
			return fmt.Errorf("Git access claimed in:\n%s", w.out)
		}
		return nil
	})
	ctx.Step(`^status shows "credentials missing" for the provider$`, func() error {
		if !strings.Contains(w.out, "credentials missing") {
			return fmt.Errorf("unexpected status output:\n%s", w.out)
		}
		return nil
	})
	ctx.Step(`^status does not show "connected"$`, func() error {
		if strings.Contains(w.out, "connected") {
			return fmt.Errorf("status incorrectly reports connected:\n%s", w.out)
		}
		return nil
	})
	ctx.Step(`^status reports authentication failure without exposing "([^"]*)"$`, func(secret string) error {
		if !strings.Contains(w.out, "authentication failed") || strings.Contains(w.out, secret) {
			return fmt.Errorf("unsafe or unclear authentication status:\n%s", w.out)
		}
		return nil
	})
	ctx.Step(`^the command reports a connection failure$`, func() error {
		if w.runErr != nil || !strings.Contains(w.out, "unreachable") {
			return fmt.Errorf("expected connection failure status, output=%q error=%v", w.out, w.runErr)
		}
		return nil
	})
	ctx.Step(`^status shows "credential storage failure" and not "credentials missing"$`, func() error {
		if !strings.Contains(w.out, "credential storage failure") || strings.Contains(w.out, "credentials missing") {
			return fmt.Errorf("storage failure was misclassified:\n%s", w.out)
		}
		return nil
	})
	ctx.Step(`^helper stdout is exactly the GitHub username and password protocol fields for "([^"]*)"$`, func(secret string) error {
		want := "username=x-access-token\npassword=" + secret + "\n\n"
		if w.runErr != nil || w.out != want {
			return fmt.Errorf("helper error=%v stdout=%q, want %q", w.runErr, w.out, want)
		}
		return nil
	})
	ctx.Step(`^the helper uses the value from "([^"]*)"$`, func(name string) error {
		want := "username=x-access-token\npassword=" + os.Getenv(name) + "\n\n"
		if w.runErr != nil || w.out != want {
			return fmt.Errorf("helper error=%v stdout=%q, want %q", w.runErr, w.out, want)
		}
		return nil
	})
	ctx.Step(`^the helper returns no credential and performs no store lookup$`, func() error {
		if w.runErr != nil || w.out != "" || w.store == nil || w.store.gets != 0 {
			return fmt.Errorf("helper error=%v stdout=%q store=%#v", w.runErr, w.out, w.store)
		}
		return nil
	})
	ctx.Step(`^no unknown credential is written to the Colt credential store$`, func() error {
		if w.store == nil || len(w.store.credentials) != 1 || w.store.puts != 0 || w.store.deletes != 0 {
			return fmt.Errorf("helper changed store: %#v", w.store)
		}
		return nil
	})
	ctx.Step(`^the invocation follows safe Git credential-helper semantics$`, func() error {
		if w.runErr != nil || w.out != "" {
			return fmt.Errorf("no-op helper error=%v output=%q", w.runErr, w.out)
		}
		return nil
	})
	ctx.Step(`^the injected stored credential is unchanged and no delete was attempted$`, func() error {
		if w.store == nil || len(w.store.credentials) != 1 || w.store.deletes != 0 || w.store.puts != 0 {
			return fmt.Errorf("erase changed store: %#v", w.store)
		}
		return nil
	})
	ctx.Step(`^the helper fails without outputting "([^"]*)"$`, func(secret string) error {
		if w.runErr == nil || strings.Contains(w.out, secret) || strings.Contains(w.runErr.Error(), secret) {
			return fmt.Errorf("malformed helper request was not safely rejected: error=%v output=%q", w.runErr, w.out)
		}
		return nil
	})
	ctx.Step(`^no credential store operation is attempted$`, func() error {
		if w.store != nil && (w.store.gets != 0 || w.store.puts != 0 || w.store.deletes != 0) {
			return fmt.Errorf("credential store operations: %#v", w.store)
		}
		return nil
	})
	ctx.Step(`^the helper succeeds with no credential output$`, func() error {
		if w.runErr != nil || w.out != "" || w.store == nil || w.store.gets != 1 {
			return fmt.Errorf("unsafe helper response: error=%v output=%q store=%#v", w.runErr, w.out, w.store)
		}
		return nil
	})
	ctx.Step(`^the redirect target never receives the credentials$`, func() error {
		if w.redirectB == nil || w.redirectB.sawAuth {
			return fmt.Errorf("credentials reached the redirect target")
		}
		return nil
	})
	ctx.Step(`^the command fails with a redirect refusal error$`, func() error {
		if w.runErr == nil || !strings.Contains(strings.ToLower(w.runErr.Error()), "redirect") {
			return fmt.Errorf("expected redirect refusal, got %v", w.runErr)
		}
		return nil
	})
	ctx.Step(`^the command succeeds with a clear no-providers result$`, func() error {
		if w.runErr != nil || !strings.Contains(w.out, "no providers configured") {
			return fmt.Errorf("unexpected output %q, err %v", w.out, w.runErr)
		}
		return nil
	})
	ctx.Step(`^status shows providers deterministically in alias order as "personal" then "work"$`, func() error {
		personal, work := strings.Index(w.out, "personal (default)\n"), strings.Index(w.out, "work\n")
		if personal < 0 || work <= personal {
			return fmt.Errorf("providers not in alias order:\n%s", w.out)
		}
		return nil
	})
	ctx.Step(`^each provider shows its alias, type, host, namespace, and default status$`, func() error {
		for _, want := range []string{"personal (default)", "GitHub · github.com", "work\n", "GitLab · gitlab.com", "Namespace:   example-ns"} {
			if !strings.Contains(w.out, want) {
				return fmt.Errorf("status lacks %q:\n%s", want, w.out)
			}
		}
		return nil
	})
	ctx.Step(`^"Connection" is "not checked"$`, func() error {
		if strings.Count(w.out, "Connection:  not checked") != 2 {
			return fmt.Errorf("offline connection state is incomplete:\n%s", w.out)
		}
		return nil
	})
	ctx.Step(`^no provider or native Git operation is invoked$`, func() error {
		if len(w.newClientCalls) != 0 || len(w.client.calls) != 0 || len(w.git.operations) != 0 {
			return fmt.Errorf("operations were invoked: provider=%v git=%v", w.client.calls, w.git.operations)
		}
		return nil
	})
	ctx.Step(`^no provider API or Git remote is contacted$`, func() error {
		if len(w.newClientCalls) != 0 || len(w.client.calls) != 0 || len(w.git.operations) != 0 {
			return fmt.Errorf("remote was contacted: provider=%v git=%v", w.client.calls, w.git.operations)
		}
		return nil
	})
	ctx.Step(`^config\.yaml does not contain "([^"]*)"$`, func(secret string) error {
		data, _ := os.ReadFile(w.configPath) // ponytail: absent config contains nothing
		if strings.Contains(string(data), secret) {
			return fmt.Errorf("config leaks a credential value")
		}
		return nil
	})
	ctx.Step(`^output does not contain any reusable credential value$`, func() error {
		return checkOfflineClean(w)
	})
	ctx.Step(`^output does not contain "([^"]*)"$`, func(secret string) error {
		if strings.Contains(w.out, secret) {
			return fmt.Errorf("output leaks a credential value")
		}
		return nil
	})
}

func checkOfflineClean(w *world) error {
	if secret := w.leaked(w.out); secret != "" {
		return fmt.Errorf("output leaks a credential value")
	}
	return nil
}
