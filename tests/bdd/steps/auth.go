package steps

import (
	"bytes"
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
	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
	"github.com/cucumber/godog"
)

func RegisterAuthSteps(ctx *godog.ScenarioContext, w *fixture.World) {
	// --- fixtures ---
	ctx.Step(`^provider "([^"]*)" has auth source "env" with token_env "([^"]*)"$`, func(alias, tokenEnv string) error {
		pType := "gitlab"
		if tokenEnv == "GITHUB_TOKEN" {
			pType = "github"
		}
		p := fixture.StdProvider(pType, "example-ns")
		p.Auth.TokenEnv = tokenEnv
		w.Providers = map[string]config.Provider{alias: p}
		w.LoginAlias, w.LoginType = alias, pType
		return w.SaveConfig()
	})
	ctx.Step(`^provider "([^"]*)" has auth source "stored" with credential_id "([^"]*)"$`, func(alias, credentialID string) error {
		p := fixture.StdProvider("github", "example-user")
		p.Auth = config.Auth{Source: "stored", CredentialID: credentialID}
		w.Providers = map[string]config.Provider{alias: p}
		return w.SaveConfig()
	})
	ctx.Step(`^a plaintext file credential store is explicitly injected with "([^"]*)"$`, func(id string) error {
		secret := "plaintext-fake-secret-1"
		w.Secrets = append(w.Secrets, secret)
		store := credential.NewFileStore(credential.DefaultFilePath(w.ConfigPath))
		w.Credentials = store // explicit test-only injection; production enrollment remains disabled
		return store.Put(id, credential.Credential{Kind: "bearer_token", Secret: secret})
	})
	ctx.Step(`^an injected credential store holds "([^"]*)" with secret "([^"]*)"$`, func(id, secret string) error {
		w.Store = fixture.NewFakeCredentialStore()
		w.Store.Credentials[id] = credential.Credential{Kind: "bearer_token", Secret: secret}
		w.Credentials = w.Store
		w.Secrets = append(w.Secrets, secret)
		return nil
	})
	ctx.Step(`^an empty injected credential store is supplied$`, func() error {
		w.Store = fixture.NewFakeCredentialStore()
		w.Credentials = w.Store
		return nil
	})
	ctx.Step(`^the injected credential store also holds "([^"]*)" with secret "([^"]*)"$`, func(id, secret string) error {
		if w.Store == nil {
			return errors.New("injected credential store is not configured")
		}
		w.Store.Credentials[id] = credential.Credential{Kind: "bearer_token", Secret: secret}
		w.Secrets = append(w.Secrets, secret)
		return nil
	})
	ctx.Step(`^representative SSH configuration, key, and agent state exist$`, func() error {
		sshDir := filepath.Join(w.Dir, ".ssh")
		if err := os.Mkdir(sshDir, 0o700); err != nil {
			return err
		}
		w.SSHState = map[string][]byte{
			filepath.Join(sshDir, "config"):  []byte("Host github.com\n  IdentityFile ~/.ssh/id_test\n"),
			filepath.Join(sshDir, "id_test"): []byte("representative-private-key"),
		}
		for path, contents := range w.SSHState {
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				return err
			}
		}
		w.SSHAgent = filepath.Join(w.Dir, "agent.sock")
		return os.Setenv("SSH_AUTH_SOCK", w.SSHAgent)
	})
	ctx.Step(`^an injected credential store holds a credential containing a newline$`, func() error {
		w.Store = fixture.NewFakeCredentialStore()
		w.Store.Credentials["github.com/personal"] = credential.Credential{Kind: "bearer_token", Secret: "safe-prefix\nusername=attacker"}
		w.Credentials = w.Store
		return nil
	})
	ctx.Step(`^the injected credential store fails while reading$`, func() error {
		w.Store = fixture.NewFakeCredentialStore()
		w.Store.GetErr = errors.New("credential backend read failed")
		w.Credentials = w.Store
		return nil
	})
	ctx.Step(`^the plaintext fallback file declares "version: 1"$`, func() error {
		return os.WriteFile(credential.DefaultFilePath(w.ConfigPath), []byte("version: 1\ncredentials: {}\n"), 0o600)
	})
	ctx.Step(`^it holds credential_id "([^"]*)"$`, func(id string) error {
		secret := "plaintext-fake-secret-1"
		w.Secrets = append(w.Secrets, secret)
		return credential.NewFileStore(credential.DefaultFilePath(w.ConfigPath)).Put(id, credential.Credential{Kind: "bearer_token", Secret: secret})
	})
	ctx.Step(`^the plaintext credential file (is group-readable|is world-readable)$`, func(condition string) error {
		p := fixture.StdProvider("github", "example-ns")
		p.Auth = config.Auth{Source: "stored", CredentialID: "github.com/personal"}
		w.Providers = map[string]config.Provider{"personal": p}
		if err := w.SaveConfig(); err != nil {
			return err
		}
		os.Unsetenv("GITHUB_TOKEN")
		secret := "plaintext-fake-secret-1"
		w.Secrets = append(w.Secrets, secret)
		path := credential.DefaultFilePath(w.ConfigPath)
		store := credential.NewFileStore(path)
		w.Credentials = store // explicitly exercise the approved fallback
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
		raw := fixture.RawProviderYAML("work", "gitlab", "gitlab.com", "https://gitlab.com", "    auth:\n      source: env\n      token_env: "+fixture.TokenEnv+"\n") +
			"    " + field + ": " + value + "\n"
		return os.WriteFile(w.ConfigPath, []byte(raw), 0o600) // ponytail: raw write bypasses Save validation on purpose
	})
	ctx.Step(`^provider "work" has an existing valid configuration$`, func() error {
		w.Providers = map[string]config.Provider{"work": fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-ns"), fixture.TokenEnv)}
		w.LoginAlias, w.LoginType = "work", "gitlab"
		return w.SaveConfig()
	})
	ctx.Step(`^its referenced credential is invalid$`, func() error {
		w.SetSecret(fixture.TokenEnv, "bdd-invalid-secret")
		w.Client.AuthErr = fmt.Errorf("authentication failed: rejected credential")
		return nil
	})
	ctx.Step(`^provider "work" is already configured$`, func() error {
		w.Providers = map[string]config.Provider{"work": fixture.StdProvider("gitlab", "example-ns")}
		w.LoginAlias, w.LoginType = "work", "gitlab"
		return w.SaveConfig()
	})
	ctx.Step(`^a GitLab provider uses host "([^"]*)"$`, func(host string) error {
		w.LoginAlias, w.LoginType = "work", "gitlab"
		w.PendingHost, w.PendingBase = host, "https://"+host
		w.SetSecret("GITLAB_TOKEN", "bdd-fake-secret") // a request needs a credential
		return nil
	})
	ctx.Step(`^"([^"]*)" contains "([^"]*)"$`, func(name, value string) error {
		w.SetSecret(name, value)
		return nil
	})
	ctx.Step(`^"([^"]*)" contains a different temporary token "([^"]*)"$`, func(name, value string) error {
		w.SetSecret(name, value)
		return nil
	})
	ctx.Step(`^a (\w+) provider has no explicit token_env and no persisted credential$`, func(pType string) error {
		kind := strings.ToLower(pType)
		w.Providers = map[string]config.Provider{"work": fixture.StdProvider(kind, "example-ns")}
		w.LoginAlias, w.LoginType = "work", kind
		return w.SaveConfig()
	})
	ctx.Step(`^environment variable "([^"]*)" contains a valid token$`, func(name string) error {
		w.SetSecret(name, "bdd-valid-token")
		return nil
	})
	ctx.Step(`^provider "personal" has no persisted credential$`, func() error {
		w.Providers = map[string]config.Provider{"personal": fixture.StdProvider("github", "example-ns")}
		w.LoginAlias, w.LoginType = "personal", "github"
		return w.SaveConfig()
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
		w.Providers = map[string]config.Provider{"work": fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-ns"), fixture.TokenEnv)}
		w.LoginAlias, w.LoginType = "work", "gitlab"
		return w.SaveConfig()
	})
	ctx.Step(`^provider API authentication succeeds for provider "work"$`, func() error {
		w.Providers = map[string]config.Provider{"work": fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-ns"), fixture.TokenEnv)}
		return w.SaveConfig()
	})
	ctx.Step(`^provider "work" has no environment credential and no persisted credential$`, func() error {
		w.Providers = map[string]config.Provider{"work": fixture.StdProvider("gitlab", "example-ns")}
		if err := fixture.UnsetCredentialEnv(); err != nil {
			return err
		}
		return w.SaveConfig()
	})
	ctx.Step(`^a provider is configured with a host that redirects to a different host$`, func() error {
		trap := &fixture.RedirectTrap{}
		trap.B = httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			trap.SawAuth = trap.SawAuth || r.Header.Get("Authorization") != ""
		}))
		trap.A = httptest.NewTLSServer(http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
			http.Redirect(wr, r, trap.B.URL+"/api/v4/user", http.StatusFound)
		}))
		w.RedirectB = trap
		w.RedirectClose = func() { trap.A.Close(); trap.B.Close() }
		w.LoginAlias, w.LoginType = "work", "gitlab"
		w.PendingBase = trap.A.URL
		w.PendingHost = strings.TrimPrefix(trap.A.URL, "https://")
		trusting := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // ponytail: loopback test only
		w.NewClient = func(p config.Provider, token string) (provider.Client, error) {
			w.NewClientCalls = append(w.NewClientCalls, fixture.NewClientCall{Provider: p, Token: token})
			return provider.New(p, token, trusting)
		}
		return nil
	})
	ctx.Step(`^a valid credential is available$`, func() error {
		w.SetSecret("GITLAB_TOKEN", "bdd-fake-secret")
		return nil
	})
	ctx.Step(`^the provider rejects authentication$`, func() error {
		w.Client.AuthErr = errors.New("authentication failed: rejected credential")
		return nil
	})
	ctx.Step(`^the provider API is unreachable due to a network failure$`, func() error {
		w.Client.AuthErr = errors.New("network failure: provider API unreachable")
		return nil
	})
	ctx.Step(`^the provider API is unreachable$`, func() error {
		w.Client.AuthErr = errors.New("provider API unreachable")
		return nil
	})
	ctx.Step(`^no providers are configured$`, func() error {
		return config.Save(w.ConfigPath, config.Config{Providers: map[string]config.Provider{}})
	})
	ctx.Step(`^providers "work" and "personal" are configured out of alias order$`, func() error {
		personal := fixture.StdProvider("github", "example-ns")
		personal.Default = true
		w.Providers = map[string]config.Provider{
			"work":     fixture.StdProvider("gitlab", "example-ns"),
			"personal": personal,
		}
		return w.SaveConfig()
	})
	ctx.Step(`^their credential environment variables are unset$`, func() error {
		return fixture.UnsetCredentialEnv()
	})
	ctx.Step(`^a locally initialized repository using provider "([^"]*)"$`, func(alias string) error {
		w.Providers = map[string]config.Provider{alias: fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-ns"), fixture.TokenEnv)}
		if err := w.SaveConfig(); err != nil {
			return err
		}
		w.Run("colt init demo --local")
		return w.RunErr
	})

	// --- actions ---
	ctx.Step(`^I try to log in to another provider named "([^"]*)"$`, func(alias string) error {
		w.LoginAlias = alias
		w.RunArgs(w.LoginArgs())
		return nil
	})
	ctx.Step("^I run `colt auth login (\\w+) ([\\w-]+) --replace` with the new settings$", func(pType, alias string) error {
		w.LoginAlias, w.LoginType = alias, pType
		w.RunArgs(w.LoginArgs("--replace"))
		return nil
	})
	ctx.Step(`^Colt loads the configuration$`, func() error {
		w.Run("colt auth status --offline")
		return nil
	})
	ctx.Step(`^Colt authenticates the provider$`, func() error {
		// ponytail: --replace keeps the focus on authentication; alias
		// collision is covered by its own scenario.
		w.RunArgs(w.LoginArgs("--replace"))
		return nil
	})
	ctx.Step(`^Colt authenticates provider "([^"]*)"$`, func(alias string) error {
		w.LoginAlias = alias
		if p, ok := w.Providers[alias]; ok {
			w.LoginType = p.Type
			if p.Auth.Source == "stored" {
				w.Run("colt auth status")
				return nil
			}
		}
		w.RunArgs(w.LoginArgs("--replace"))
		return nil
	})
	ctx.Step(`^provider authentication fails$`, func() error {
		w.Client.AuthErr = fmt.Errorf("authentication failed: rejected credential")
		w.RunArgs(w.LoginArgs("--replace"))
		return nil
	})
	ctx.Step(`^authentication is attempted against the provider$`, func() error {
		w.RunArgs(w.LoginArgs())
		return nil
	})
	ctx.Step(`^Colt parses the credential file$`, func() error {
		store := credential.NewFileStore(credential.DefaultFilePath(w.ConfigPath))
		first, err := store.Get("github.com/personal")
		if err != nil {
			return err
		}
		second, err := store.Get("github.com/personal")
		w.RepeatedParsingSame = err == nil && first == second
		return err
	})
	ctx.Step(`^Colt resolves the persisted credential$`, func() error {
		w.Run("colt init demo")
		return nil
	})
	ctx.Step(`^Git requests "protocol=https host=github\.com path=example-user/example-project\.git"$`, func() error {
		w.RunHelper("get", "protocol=https\nhost=github.com\npath=example-user/example-project.git\n\n")
		return nil
	})
	ctx.Step(`^Git requests the provider credential$`, func() error {
		w.RunHelper("get", "protocol=https\nhost=github.com\npath=example-user/example-project.git\n\n")
		return nil
	})
	ctx.Step(`^Git requests credentials for "([^"]*)"$`, func(host string) error {
		w.RunHelper("get", "protocol=https\nhost="+host+"\npath=example-user/example-project.git\n\n")
		return nil
	})
	ctx.Step(`^Git invokes the helper with "store" for an unknown credential$`, func() error {
		w.HelperOperation = "store"
		w.HelperInput = "protocol=https\nhost=example.org\npath=other/project.git\npassword=unknown-secret\n\n"
		return nil
	})
	ctx.Step(`^Git invokes the helper with "erase" after a failed authentication$`, func() error {
		w.HelperOperation = "erase"
		w.HelperInput = "protocol=https\nhost=github.com\npath=example-user/example-project.git\n\n"
		return nil
	})
	ctx.Step(`^Git supplies a malformed credential request containing "([^"]*)"$`, func(secret string) error {
		w.Store = fixture.NewFakeCredentialStore()
		w.Credentials = w.Store
		w.HelperOperation = "get"
		w.HelperInput = "password=" + secret + "\nmalformed\n"
		w.Secrets = append(w.Secrets, secret)
		return nil
	})
	ctx.Step(`^the helper handles the invocation$`, func() error {
		w.RunHelper(w.HelperOperation, w.HelperInput)
		return nil
	})

	// --- assertions ---
	ctx.Step(`^config\.yaml contains "([^"]*)"$`, func(want string) error {
		data, err := os.ReadFile(w.ConfigPath)
		if err != nil || !strings.Contains(string(data), want) {
			return fmt.Errorf("config lacks %q: %v", want, err)
		}
		return nil
	})
	ctx.Step(`^config\.yaml contains no reusable credential value$`, func() error {
		data, err := os.ReadFile(w.ConfigPath)
		if err != nil {
			return err
		}
		if secret := w.Leaked(string(data)); secret != "" {
			return fmt.Errorf("config leaks a credential value")
		}
		return nil
	})
	ctx.Step(`^the load fails with a configuration error$`, func() error {
		if w.RunErr == nil || !strings.Contains(strings.ToLower(w.RunErr.Error()), "config") {
			return fmt.Errorf("expected configuration error, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^no authentication is attempted$`, func() error {
		if len(w.NewClientCalls) != 0 || len(w.Client.Calls) != 0 {
			return fmt.Errorf("authentication was attempted: %v", w.Client.Calls)
		}
		return nil
	})
	ctx.Step(`^the command fails with an authentication error$`, func() error {
		if w.RunErr == nil || !strings.Contains(strings.ToLower(w.RunErr.Error()), "authentication") {
			return fmt.Errorf("expected authentication error, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^the prior provider configuration is unchanged$`, func() error {
		data, err := os.ReadFile(w.ConfigPath)
		if err != nil || string(data) != string(w.ConfigBefore) {
			return fmt.Errorf("prior configuration changed")
		}
		return nil
	})
	ctx.Step(`^provider configuration is unchanged$`, func() error {
		data, err := os.ReadFile(w.ConfigPath)
		if err != nil || string(data) != string(w.ConfigBefore) {
			return fmt.Errorf("provider configuration changed")
		}
		return nil
	})
	ctx.Step(`^the provider receives the resolved credential without knowing its backend type$`, func() error {
		token, ok := w.LastToken()
		if !ok || token != "plaintext-fake-secret-1" {
			return fmt.Errorf("provider did not receive the fallback credential")
		}
		return nil
	})
	ctx.Step(`^exactly credential "([^"]*)" is deleted from the injected store$`, func(id string) error {
		if w.Store == nil || w.Store.Gets != 0 || w.Store.Puts != 0 || w.Store.Deletes != 1 || len(w.Store.DeletedIDs) != 1 || w.Store.DeletedIDs[0] != id {
			return fmt.Errorf("credential store calls = %#v", w.Store)
		}
		if _, exists := w.Store.Credentials[id]; exists {
			return fmt.Errorf("credential %q still exists", id)
		}
		return nil
	})
	ctx.Step(`^credential "([^"]*)" remains unchanged$`, func(id string) error {
		stored, exists := w.Store.Credentials[id]
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
		if len(w.SSHState) == 0 || os.Getenv("SSH_AUTH_SOCK") != w.SSHAgent {
			return errors.New("SSH agent state changed or was not observed")
		}
		for path, before := range w.SSHState {
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				return fmt.Errorf("SSH state changed at %s: %v", path, err)
			}
		}
		return nil
	})
	ctx.Step(`^output explains the environment credential still resolves without revealing it$`, func() error {
		if !strings.Contains(w.Out, "GITHUB_TOKEN may still provide credentials") || strings.Contains(w.Out, os.Getenv("GITHUB_TOKEN")) {
			return fmt.Errorf("unsafe or unclear logout output: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^logout warns that "([^"]*)" may still resolve without revealing its value$`, func(name string) error {
		if !strings.Contains(w.Out, name+" may still provide credentials") || strings.Contains(w.Out, os.Getenv(name)) {
			return fmt.Errorf("unsafe or missing environment warning: %q", w.Out)
		}
		return nil
	})
	ctx.Step("^`colt auth status` still authenticates via \"([^\"]*)\"$", func(name string) error {
		if len(w.NewClientCalls) != 0 || len(w.Client.Calls) != 0 || len(w.Git.Operations) != 0 {
			return fmt.Errorf("logout contacted provider or Git: clients=%d provider=%v git=%v", len(w.NewClientCalls), w.Client.Calls, w.Git.Operations)
		}
		w.Run("colt auth status")
		if w.RunErr != nil || len(w.NewClientCalls) != 1 || w.NewClientCalls[0].Token != os.Getenv(name) || !w.Client.Called("Authenticate") {
			return fmt.Errorf("status did not authenticate via %s: error=%v clients=%v provider=%v", name, w.RunErr, w.NewClientCalls, w.Client.Calls)
		}
		return nil
	})
	ctx.Step(`^the injected stored credential is unchanged$`, func() error {
		if w.Store == nil || len(w.Store.Credentials) != 1 || w.Store.Puts != 0 || w.Store.Deletes != 0 {
			return fmt.Errorf("credential store changed: %#v", w.Store)
		}
		return nil
	})
	ctx.Step(`^the injected stored credential is unchanged and was not read$`, func() error {
		if w.Store == nil || len(w.Store.Credentials) != 1 || w.Store.Gets != 0 || w.Store.Puts != 0 || w.Store.Deletes != 0 {
			return fmt.Errorf("credential store was accessed or changed: %#v", w.Store)
		}
		return nil
	})
	ctx.Step(`^repeated parsing returns the same credential$`, func() error {
		if !w.RepeatedParsingSame {
			return fmt.Errorf("repeated credential parsing differed")
		}
		return nil
	})
	ctx.Step(`^config\.yaml remains free of reusable credential values$`, func() error {
		data, _ := os.ReadFile(w.ConfigPath)
		if secret := w.Leaked(string(data)); secret != "" {
			return fmt.Errorf("config leaks a credential value")
		}
		return nil
	})
	ctx.Step(`^Colt fails safely without exposing the secret value$`, func() error {
		if w.RunErr == nil || w.Leaked(w.RunErr.Error()) != "" || len(w.NewClientCalls) != 0 {
			return fmt.Errorf("unsafe credential failure: %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^normal, verbose, and error output do not contain "([^"]*)"$`, func(secret string) error {
		if strings.Contains(w.Out, secret) || strings.Contains(fmt.Sprint(w.RunErr), secret) {
			return fmt.Errorf("credential output leaked")
		}
		return nil
	})
	ctx.Step(`^the command fails with a duplicate alias error$`, func() error {
		if w.RunErr == nil || !strings.Contains(w.RunErr.Error(), "already exists") {
			return fmt.Errorf("expected duplicate alias error, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^the existing provider configuration is unchanged$`, func() error {
		data, err := os.ReadFile(w.ConfigPath)
		if err != nil || string(data) != string(w.ConfigBefore) {
			return fmt.Errorf("existing configuration changed")
		}
		return nil
	})
	ctx.Step(`^the request uses the configured self-hosted GitLab base URL$`, func() error {
		if len(w.NewClientCalls) == 0 {
			return fmt.Errorf("no provider request was made")
		}
		if got := w.NewClientCalls[0].Provider.BaseURL; !strings.Contains(got, "gitlab.company.example") {
			return fmt.Errorf("base URL = %q", got)
		}
		return nil
	})
	ctx.Step(`^no request is sent to GitLab\.com$`, func() error {
		for _, c := range w.NewClientCalls {
			u, err := url.Parse(c.Provider.BaseURL)
			if err != nil || strings.EqualFold(u.Host, "gitlab.com") {
				return fmt.Errorf("request used %q", c.Provider.BaseURL)
			}
		}
		return nil
	})
	ctx.Step(`^authentication used the value from "([^"]*)"$`, func(name string) error {
		token, ok := w.LastToken()
		if !ok || token != os.Getenv(name) {
			return fmt.Errorf("client did not receive the value from %q", name)
		}
		return nil
	})
	ctx.Step(`^normal and error output do not contain "([^"]*)"$`, func(secret string) error {
		if strings.Contains(w.Out, secret) || strings.Contains(fmt.Sprint(w.RunErr), secret) {
			return fmt.Errorf("output leaks a credential value")
		}
		return nil
	})
	ctx.Step(`^normal Colt configuration does not contain "([^"]*)"$`, func(secret string) error {
		data, _ := os.ReadFile(w.ConfigPath) // ponytail: absent config contains nothing
		if strings.Contains(string(data), secret) {
			return fmt.Errorf("config leaks a credential value")
		}
		return nil
	})
	ctx.Step(`^authentication uses the value from "([^"]*)"$`, func(name string) error {
		tok, ok := w.LastToken()
		if !ok || tok != os.Getenv(name) {
			return fmt.Errorf("client did not receive the value from %q", name)
		}
		return nil
	})
	ctx.Step(`^authentication succeeds for this invocation$`, func() error {
		if w.RunErr != nil {
			return fmt.Errorf("expected success, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^no Colt credential file is written$`, func() error {
		matches, _ := filepath.Glob(filepath.Join(w.Dir, "credentials*"))
		if len(matches) != 0 {
			return fmt.Errorf("credential file written: %v", matches)
		}
		data, _ := os.ReadFile(w.ConfigPath)
		if secret := w.Leaked(string(data)); secret != "" {
			return fmt.Errorf("config leaks a credential value")
		}
		return nil
	})
	ctx.Step(`^the command fails before sending an authenticated request$`, func() error {
		if w.RunErr == nil {
			return fmt.Errorf("expected failure, got success")
		}
		if len(w.NewClientCalls) != 0 || len(w.Client.Calls) != 0 {
			return fmt.Errorf("authenticated request was attempted: %v", w.Client.Calls)
		}
		return nil
	})
	ctx.Step(`^no project or provider state is changed$`, func() error {
		data, _ := os.ReadFile(w.ConfigPath) // ponytail: absent config counts as unchanged
		if string(data) != string(w.ConfigBefore) {
			return fmt.Errorf("provider state changed")
		}
		if _, err := os.Lstat(filepath.Join(w.Dir, "demo")); !os.IsNotExist(err) {
			return fmt.Errorf("project state changed")
		}
		return nil
	})
	ctx.Step(`^the command succeeds$`, func() error {
		if w.RunErr != nil {
			return fmt.Errorf("expected success, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^status shows the authenticated account and "connected"$`, func() error {
		if !strings.Contains(w.Out, "example-user") || !strings.Contains(w.Out, "connected") {
			return fmt.Errorf("unexpected status output:\n%s", w.Out)
		}
		return nil
	})
	ctx.Step(`^"connected" means only the provider API check passed$`, func() error {
		if !strings.Contains(w.Out, "connected") || !w.Client.Called("Authenticate") || len(w.Git.Operations) != 0 {
			return fmt.Errorf("status was not API-only: provider=%v git=%v output=%q", w.Client.Calls, w.Git.Operations, w.Out)
		}
		return nil
	})
	ctx.Step(`^no Git clone or push access is claimed$`, func() error {
		if strings.Contains(w.Out, "clone") || strings.Contains(w.Out, "push") || strings.Contains(w.Out, "Git access") {
			return fmt.Errorf("Git access claimed in:\n%s", w.Out)
		}
		return nil
	})
	ctx.Step(`^status shows "credentials missing" for the provider$`, func() error {
		if !strings.Contains(w.Out, "credentials missing") {
			return fmt.Errorf("unexpected status output:\n%s", w.Out)
		}
		return nil
	})
	ctx.Step(`^status does not show "connected"$`, func() error {
		if strings.Contains(w.Out, "connected") {
			return fmt.Errorf("status incorrectly reports connected:\n%s", w.Out)
		}
		return nil
	})
	ctx.Step(`^status reports authentication failure without exposing "([^"]*)"$`, func(secret string) error {
		if !strings.Contains(w.Out, "authentication failed") || strings.Contains(w.Out, secret) {
			return fmt.Errorf("unsafe or unclear authentication status:\n%s", w.Out)
		}
		return nil
	})
	ctx.Step(`^the command reports a connection failure$`, func() error {
		if w.RunErr != nil || !strings.Contains(w.Out, "unreachable") {
			return fmt.Errorf("expected connection failure status, output=%q error=%v", w.Out, w.RunErr)
		}
		return nil
	})
	ctx.Step(`^status shows "credential storage failure" and not "credentials missing"$`, func() error {
		if !strings.Contains(w.Out, "credential storage failure") || strings.Contains(w.Out, "credentials missing") {
			return fmt.Errorf("storage failure was misclassified:\n%s", w.Out)
		}
		return nil
	})
	ctx.Step(`^helper stdout is exactly the GitHub username and password protocol fields for "([^"]*)"$`, func(secret string) error {
		want := "username=x-access-token\npassword=" + secret + "\n\n"
		if w.RunErr != nil || w.Out != want {
			return fmt.Errorf("helper error=%v stdout=%q, want %q", w.RunErr, w.Out, want)
		}
		return nil
	})
	ctx.Step(`^the helper uses the value from "([^"]*)"$`, func(name string) error {
		want := "username=x-access-token\npassword=" + os.Getenv(name) + "\n\n"
		if w.RunErr != nil || w.Out != want {
			return fmt.Errorf("helper error=%v stdout=%q, want %q", w.RunErr, w.Out, want)
		}
		return nil
	})
	ctx.Step(`^the helper returns no credential and performs no store lookup$`, func() error {
		if w.RunErr != nil || w.Out != "" || w.Store == nil || w.Store.Gets != 0 {
			return fmt.Errorf("helper error=%v stdout=%q store=%#v", w.RunErr, w.Out, w.Store)
		}
		return nil
	})
	ctx.Step(`^no unknown credential is written to the Colt credential store$`, func() error {
		if w.Store == nil || len(w.Store.Credentials) != 1 || w.Store.Puts != 0 || w.Store.Deletes != 0 {
			return fmt.Errorf("helper changed store: %#v", w.Store)
		}
		return nil
	})
	ctx.Step(`^the invocation follows safe Git credential-helper semantics$`, func() error {
		if w.RunErr != nil || w.Out != "" {
			return fmt.Errorf("no-op helper error=%v output=%q", w.RunErr, w.Out)
		}
		return nil
	})
	ctx.Step(`^the injected stored credential is unchanged and no delete was attempted$`, func() error {
		if w.Store == nil || len(w.Store.Credentials) != 1 || w.Store.Deletes != 0 || w.Store.Puts != 0 {
			return fmt.Errorf("erase changed store: %#v", w.Store)
		}
		return nil
	})
	ctx.Step(`^the helper fails without outputting "([^"]*)"$`, func(secret string) error {
		if w.RunErr == nil || strings.Contains(w.Out, secret) || strings.Contains(w.RunErr.Error(), secret) {
			return fmt.Errorf("malformed helper request was not safely rejected: error=%v output=%q", w.RunErr, w.Out)
		}
		return nil
	})
	ctx.Step(`^no credential store operation is attempted$`, func() error {
		if w.Store != nil && (w.Store.Gets != 0 || w.Store.Puts != 0 || w.Store.Deletes != 0) {
			return fmt.Errorf("credential store operations: %#v", w.Store)
		}
		return nil
	})
	ctx.Step(`^the helper succeeds with no credential output$`, func() error {
		if w.RunErr != nil || w.Out != "" || w.Store == nil || w.Store.Gets != 1 {
			return fmt.Errorf("unsafe helper response: error=%v output=%q store=%#v", w.RunErr, w.Out, w.Store)
		}
		return nil
	})
	ctx.Step(`^the redirect target never receives the credentials$`, func() error {
		if w.RedirectB == nil || w.RedirectB.SawAuth {
			return fmt.Errorf("credentials reached the redirect target")
		}
		return nil
	})
	ctx.Step(`^the command fails with a redirect refusal error$`, func() error {
		if w.RunErr == nil || !strings.Contains(strings.ToLower(w.RunErr.Error()), "redirect") {
			return fmt.Errorf("expected redirect refusal, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^the command succeeds with a clear no-providers result$`, func() error {
		if w.RunErr != nil || !strings.Contains(w.Out, "no providers configured") {
			return fmt.Errorf("unexpected output %q, err %v", w.Out, w.RunErr)
		}
		return nil
	})
	ctx.Step(`^status shows providers deterministically in alias order as "personal" then "work"$`, func() error {
		personal, work := strings.Index(w.Out, "personal (default)\n"), strings.Index(w.Out, "work\n")
		if personal < 0 || work <= personal {
			return fmt.Errorf("providers not in alias order:\n%s", w.Out)
		}
		return nil
	})
	ctx.Step(`^each provider shows its alias, type, host, namespace, and default status$`, func() error {
		for _, want := range []string{"personal (default)", "GitHub · github.com", "work\n", "GitLab · gitlab.com", "Namespace:   example-ns"} {
			if !strings.Contains(w.Out, want) {
				return fmt.Errorf("status lacks %q:\n%s", want, w.Out)
			}
		}
		return nil
	})
	ctx.Step(`^"Connection" is "not checked"$`, func() error {
		if strings.Count(w.Out, "Connection:  not checked") != 2 {
			return fmt.Errorf("offline connection state is incomplete:\n%s", w.Out)
		}
		return nil
	})
	ctx.Step(`^no provider or native Git operation is invoked$`, func() error {
		if len(w.NewClientCalls) != 0 || len(w.Client.Calls) != 0 || len(w.Git.Operations) != 0 {
			return fmt.Errorf("operations were invoked: provider=%v git=%v", w.Client.Calls, w.Git.Operations)
		}
		return nil
	})
	ctx.Step(`^no provider API or Git remote is contacted$`, func() error {
		if len(w.NewClientCalls) != 0 || len(w.Client.Calls) != 0 || len(w.Git.Operations) != 0 {
			return fmt.Errorf("remote was contacted: provider=%v git=%v", w.Client.Calls, w.Git.Operations)
		}
		return nil
	})
	ctx.Step(`^config\.yaml does not contain "([^"]*)"$`, func(secret string) error {
		data, _ := os.ReadFile(w.ConfigPath) // ponytail: absent config contains nothing
		if strings.Contains(string(data), secret) {
			return fmt.Errorf("config leaks a credential value")
		}
		return nil
	})
	ctx.Step(`^output does not contain any reusable credential value$`, func() error {
		return checkOfflineClean(w)
	})
	ctx.Step(`^output does not contain "([^"]*)"$`, func(secret string) error {
		if strings.Contains(w.Out, secret) {
			return fmt.Errorf("output leaks a credential value")
		}
		return nil
	})
}

func checkOfflineClean(w *fixture.World) error {
	if secret := w.Leaked(w.Out); secret != "" {
		return fmt.Errorf("output leaks a credential value")
	}
	return nil
}
