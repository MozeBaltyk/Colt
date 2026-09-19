package steps

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
	"github.com/cucumber/godog"
)

func RegisterAuthSteps(ctx *godog.ScenarioContext, w *fixture.World) {
	configureStatusProvider := func(transport string, stored bool) error {
		p := fixture.StdProvider("github", "example-user")
		p.Default, p.Transport = true, transport
		if stored {
			p.Auth = config.Auth{Source: "stored", CredentialID: "github.com/personal"}
			w.Store = fixture.NewFakeCredentialStore()
			w.Credentials = w.Store
		} else {
			p = fixture.WithTokenEnv(p, fixture.TokenEnv)
		}
		w.Providers = map[string]config.Provider{"personal": p}
		w.Git.Real = false
		return w.SaveConfig()
	}
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
	ctx.Step(`^an injected secure-store double holds "([^"]*)"$`, func(id string) error {
		secret := "secure-fake-secret-1"
		w.Secrets = append(w.Secrets, secret)
		w.Store = fixture.NewFakeCredentialStore()
		w.Store.Credentials[id] = credential.Credential{Kind: "bearer_token", Secret: secret}
		w.Credentials = w.Store
		return nil
	})
	ctx.Step(`^provider "personal" has a credential in an injected secure-store double$`, func() error {
		p := fixture.StdProvider("github", "example-user")
		p.Auth = config.Auth{Source: "stored", CredentialID: "github.com/personal"}
		w.Providers = map[string]config.Provider{"personal": p}
		w.Store = fixture.NewFakeCredentialStore()
		w.Store.Credentials[p.Auth.CredentialID] = credential.Credential{Kind: "bearer_token", Secret: "selected-secret"}
		w.Store.Credentials["github.com/work"] = credential.Credential{Kind: "bearer_token", Secret: "neighbor-secret"}
		w.Credentials = w.Store
		return w.SaveConfig()
	})
	ctx.Step(`^provider "personal" has a persisted plaintext credential$`, func() error {
		p := fixture.StdProvider("github", "example-user")
		p.Auth = config.Auth{Source: "stored", CredentialID: "github.com/personal"}
		w.Providers = map[string]config.Provider{"personal": p}
		w.Credentials = credential.NewMemoryStore()
		w.FallbackCredentials = credential.NewFileStore(credential.DefaultFilePath(w.ConfigPath))
		if err := w.SaveConfig(); err != nil {
			return err
		}
		if err := w.FallbackCredentials.Put(p.Auth.CredentialID, credential.Credential{Kind: "bearer_token", Secret: "selected-secret"}); err != nil {
			return err
		}
		return w.FallbackCredentials.Put("github.com/work", credential.Credential{Kind: "bearer_token", Secret: "neighbor-secret"})
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
		w.Store.Events = &w.Events
		w.Credentials = w.Store
		return nil
	})
	ctx.Step(`^an injected secure-store double is available$`, func() error {
		w.Store = fixture.NewFakeCredentialStore()
		w.Credentials = w.Store
		return nil
	})
	ctx.Step(`^the secure credential backend is unavailable$`, func() error {
		if err := fixture.UnsetCredentialEnv(); err != nil {
			return err
		}
		w.Credentials = credential.DisabledStore{}
		w.FallbackCredentials = credential.NewFileStore(credential.DefaultFilePath(w.ConfigPath))
		return nil
	})
	ctx.Step(`^the user accepts plaintext credential persistence$`, func() error {
		w.Interactive = true
		w.ConfirmPlaintext = func() (bool, error) { return true, nil }
		return nil
	})
	ctx.Step(`^the user has not consented to plaintext storage$`, func() error { return nil })
	ctx.Step(`^the user rejects plaintext credential persistence$`, func() error {
		w.Interactive = true
		w.ConfirmPlaintext = func() (bool, error) { return false, nil }
		return nil
	})
	ctx.Step(`^no provider token environment variable resolves$`, func() error {
		return fixture.UnsetCredentialEnv()
	})
	ctx.Step(`^no GitHub token environment variable resolves$`, func() error {
		return os.Unsetenv("GITHUB_TOKEN")
	})
	ctx.Step(`^no persisted credential exists for provider "([^"]*)"$`, func(alias string) error {
		w.LoginAlias, w.LoginType = alias, "github"
		w.Store = fixture.NewFakeCredentialStore()
		w.Credentials = w.Store
		return nil
	})
	ctx.Step(`^no persisted credential or GitHub token environment variable resolves$`, func() error {
		w.Store = fixture.NewFakeCredentialStore()
		w.Credentials = w.Store
		return fixture.UnsetCredentialEnv()
	})
	ctx.Step(`^standard input is an interactive terminal$`, func() error {
		w.Interactive = true
		w.Input = "example-ns\nExample User\nuser@example.invalid\n"
		return nil
	})
	ctx.Step(`^no legacy GitHub client ID environment variable is configured$`, func() error {
		return os.Unsetenv("COLT_GITHUB_CLIENT_ID")
	})
	ctx.Step(`^the provider authorization flow reports approval for account "([^"]*)"$`, func(account string) error {
		secret := "bdd-device-secret"
		w.Secrets = append(w.Secrets, secret)
		w.Client.Account = account
		w.Store = fixture.NewFakeCredentialStore()
		w.Store.Events = &w.Events
		w.Credentials = w.Store
		w.AuthorizeGitHubDevice = func(_ context.Context, out io.Writer) (string, error) {
			w.DeviceFlowCalls++
			fmt.Fprint(out, "Open: https://github.com/login/device\nCode: BDD-CODE\n")
			return secret, nil
		}
		return nil
	})
	ctx.Step(`^the provider authorization flow reports rejection by the user$`, func() error {
		w.AuthorizeGitHubDevice = func(context.Context, io.Writer) (string, error) {
			w.DeviceFlowCalls++
			return "", errors.New("GitHub device authorization was rejected")
		}
		return nil
	})
	ctx.Step(`^the provider authorization flow reports expiry before approval$`, func() error {
		w.AuthorizeGitHubDevice = func(context.Context, io.Writer) (string, error) {
			w.DeviceFlowCalls++
			return "", errors.New("GitHub device authorization expired")
		}
		return nil
	})
	ctx.Step("^I pass the root flag `--noninteractive`$", func() error {
		w.Noninteractive = true
		if w.Store == nil {
			w.Store = fixture.NewFakeCredentialStore()
			w.Credentials = w.Store
		}
		return nil
	})
	ctx.Step(`^standard input is not a terminal$`, func() error {
		w.Interactive = false
		if w.Store == nil {
			w.Store = fixture.NewFakeCredentialStore()
			w.Credentials = w.Store
		}
		return nil
	})
	ctx.Step(`^interactive terminal input supplies "([^"]*)"$`, func(input string) error {
		w.Interactive = true
		w.Input = input + "\n"
		return nil
	})
	ctx.Step(`^manual token entry supplies "([^"]*)"$`, func(secret string) error {
		w.Interactive = true
		w.Secrets = append(w.Secrets, secret)
		w.ReadToken = func() (string, error) { return secret, nil }
		return nil
	})
	ctx.Step(`^the command fails with an error mentioning "([^"]*)"$`, func(want string) error {
		if w.RunErr == nil || !strings.Contains(w.RunErr.Error(), want) {
			return fmt.Errorf("expected error containing %q, got %v", want, w.RunErr)
		}
		return nil
	})
	ctx.Step(`^the login command does not carry the provider token environment variable$`, func() error {
		w.LoginNoTokenEnv = true
		return nil
	})
	ctx.Step(`^the command fails with a token validation error$`, func() error {
		if w.RunErr == nil || !strings.Contains(strings.ToLower(w.RunErr.Error()), "invalid") {
			return fmt.Errorf("expected token validation error, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^Colt automatically starts GitHub OAuth Device Flow$`, func() error {
		if w.DeviceFlowCalls != 1 {
			return fmt.Errorf("device flow calls = %d", w.DeviceFlowCalls)
		}
		return nil
	})
	ctx.Step(`^Colt does not start GitHub OAuth Device Flow$`, func() error {
		if w.DeviceFlowCalls != 0 {
			return fmt.Errorf("device flow calls = %d", w.DeviceFlowCalls)
		}
		return nil
	})
	ctx.Step(`^Colt displays the authorization URL and user code$`, func() error {
		if !strings.Contains(w.Out, "https://github.com/login/device") || !strings.Contains(w.Out, "BDD-CODE") {
			return fmt.Errorf("device instructions missing from %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^Colt identifies the authenticated account as "([^"]*)"$`, func(account string) error {
		if !strings.Contains(w.Out, "authenticated as "+account) {
			return fmt.Errorf("account %q missing from %q", account, w.Out)
		}
		return nil
	})
	ctx.Step(`^the resulting API token is authenticated before persistence$`, func() error {
		if len(w.Events) < 2 || w.Events[0] != "authenticate" || w.Events[1] != "persist" {
			return fmt.Errorf("authentication/persistence order = %v", w.Events)
		}
		return nil
	})
	ctx.Step(`^no credential is persisted$`, func() error {
		if w.FallbackCredentials != nil {
			if _, getErr := w.FallbackCredentials.Get("github.com/personal"); getErr == nil {
				return fmt.Errorf("credential was persisted in fallback file")
			}
		}
		if w.Store != nil {
			if _, getErr := w.Store.Get("github.com/personal"); getErr == nil {
				return fmt.Errorf("credential was persisted in store")
			}
		}
		return nil
	})
	ctx.Step(`^the token is persisted in the available secure-store double$`, func() error {
		if w.Store == nil || w.Store.Puts != 1 || w.FallbackCredentials != nil {
			return fmt.Errorf("unexpected persistence: store=%#v fallback=%v", w.Store, w.FallbackCredentials != nil)
		}
		got, ok := w.Store.Credentials["github.com/personal"]
		token, called := w.LastToken()
		if !ok || !called || got.Secret != token {
			return fmt.Errorf("persisted credential does not match the authenticated token")
		}
		return nil
	})
	ctx.Step(`^the configured Git transport remains "(ssh|https)"$`, func(transport string) error {
		cfg, err := config.Load(w.ConfigPath)
		if err != nil || cfg.Providers["personal"].Transport != transport {
			return fmt.Errorf("transport=%q: %v", cfg.Providers["personal"].Transport, err)
		}
		return nil
	})
	ctx.Step(`^the command fails with an actionable missing-credential error$`, func() error {
		if w.RunErr == nil || !strings.Contains(w.RunErr.Error(), "no stored credential") || !strings.Contains(w.RunErr.Error(), "--token-env") {
			return fmt.Errorf("expected actionable missing-credential error, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^no reusable credential value appears in output$`, func() error {
		if secret := w.Leaked(w.Out); secret != "" {
			return fmt.Errorf("output contains registered credential %q", secret)
		}
		return nil
	})
	ctx.Step(`^login prompts are exactly "([^"]*)" in that order$`, func(prompts string) error {
		if !strings.HasPrefix(w.Out, prompts) {
			return fmt.Errorf("output %q does not start with prompts %q", w.Out, prompts)
		}
		return nil
	})
	ctx.Step(`^the supplied login values are saved$`, func() error {
		cfg, err := config.Load(w.ConfigPath)
		p := cfg.Providers["personal"]
		if err != nil || p.Namespace != "example-ns" || p.GitName != "Example User" || p.GitEmail != "user@example.invalid" {
			return fmt.Errorf("provider=%+v: %v", p, err)
		}
		return nil
	})
	ctx.Step(`^optional visibility and transport defaults are unchanged$`, func() error {
		cfg, err := config.Load(w.ConfigPath)
		p := cfg.Providers["personal"]
		if err != nil || p.Visibility != "private" || p.Transport != "" {
			return fmt.Errorf("provider defaults=%+v: %v", p, err)
		}
		return nil
	})
	ctx.Step(`^the prompt is exactly "([^"]*)"$`, func(prompt string) error {
		if !strings.HasPrefix(w.Out, prompt) {
			return fmt.Errorf("output %q does not start with prompt %q", w.Out, prompt)
		}
		return nil
	})
	ctx.Step(`^the output starts with the exact prompt "([^"]*)"$`, func(prompt string) error {
		if !strings.HasPrefix(w.Out, prompt) {
			return fmt.Errorf("output %q does not start with prompt %q", w.Out, prompt)
		}
		return nil
	})
	ctx.Step(`^the missing-input error lists "([^"]*)" in that order$`, func(list string) error {
		if w.RunErr == nil {
			return errors.New("expected missing-input error")
		}
		position := 0
		for _, name := range strings.Split(list, ", ") {
			next := strings.Index(w.RunErr.Error()[position:], name)
			if next < 0 {
				return fmt.Errorf("%q missing or out of order in %q", name, w.RunErr)
			}
			position += next + len(name)
		}
		return nil
	})
	ctx.Step(`^no prompt or interactive authorization flow starts$`, func() error {
		if w.Out != "" || w.DeviceFlowCalls != 0 {
			return fmt.Errorf("output=%q device flow calls=%d", w.Out, w.DeviceFlowCalls)
		}
		return nil
	})
	ctx.Step(`^no config, credential, provider, or Git mutation occurs$`, func() error {
		data, err := os.ReadFile(w.ConfigPath)
		if errors.Is(err, os.ErrNotExist) {
			data, err = nil, nil
		}
		if err != nil || string(data) != string(w.ConfigBefore) || len(w.NewClientCalls) != 0 || len(w.Git.Operations) != 0 || (w.Store != nil && (w.Store.Puts != 0 || w.Store.Deletes != 0)) {
			return fmt.Errorf("config error=%v clients=%d git=%v store=%#v", err, len(w.NewClientCalls), w.Git.Operations, w.Store)
		}
		return nil
	})
	ctx.Step(`^the command fails with an invalid-input error, not a missing-input error$`, func() error {
		if w.RunErr == nil || !strings.Contains(strings.ToLower(w.RunErr.Error()), "invalid") || strings.Contains(w.RunErr.Error(), "missing required input") {
			return fmt.Errorf("expected invalid-input error, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^the injected stored credential for "([^"]*)" still has secret "([^"]*)"$`, func(id, secret string) error {
		if w.Store == nil {
			return fmt.Errorf("no injected store configured")
		}
		got, err := w.Store.Get(id)
		if err != nil {
			return fmt.Errorf("credential missing: %v", err)
		}
		if got.Secret != secret {
			return fmt.Errorf("credential secret changed to %q", got.Secret)
		}
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
		// Interpret common escape sequences so feature files can express
		// values that contain newlines, tabs, or literal backslashes.
		value = strings.ReplaceAll(value, `\n`, "\n")
		value = strings.ReplaceAll(value, `\t`, "\t")
		value = strings.ReplaceAll(value, `\\`, "\\")
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
	ctx.Step(`^the command fails with an authorization error$`, func() error {
		if w.RunErr == nil || !strings.Contains(strings.ToLower(w.RunErr.Error()), "authorization") {
			return fmt.Errorf("expected authorization error, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^provider "work" is configured with a valid credential$`, func() error {
		w.Providers = map[string]config.Provider{"work": fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-ns"), fixture.TokenEnv)}
		w.LoginAlias, w.LoginType = "work", "gitlab"
		return w.SaveConfig()
	})
	ctx.Step(`^provider API authentication succeeds for provider "work"$`, func() error {
		if _, ok := w.Providers["work"]; !ok {
			w.Providers = map[string]config.Provider{"work": fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-ns"), fixture.TokenEnv)}
		}
		return w.SaveConfig()
	})
	ctx.Step(`^provider "work" is a configured (github|gitlab|gitea|forgejo) provider at "([^"]*)" for namespace "([^"]*)" using "(https|ssh)"$`, func(providerType, host, namespace, transport string) error {
		p := fixture.WithTokenEnv(fixture.StdProvider(providerType, namespace), fixture.TokenEnv)
		p.Host, p.BaseURL, p.Transport = host, "https://"+host, transport
		if providerType == "github" {
			p.BaseURL = "https://api.github.com"
		}
		w.Providers = map[string]config.Provider{"work": p}
		w.Git.Real = false
		return w.SaveConfig()
	})
	ctx.Step(`^repository "([^"]*)" metadata supplies HTTPS URL "([^"]*)" and SSH URL "([^"]*)"$`, func(_ /* project */, httpsURL, sshURL string) error {
		w.Client.GetRepo = &provider.Repository{CloneURL: httpsURL, SSHURL: sshURL}
		return nil
	})
	ctx.Step(`^repository "([^"]*)" metadata cannot be obtained$`, func(_ string) error {
		w.Client.GetErr = errors.New("provider metadata unavailable")
		return nil
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
	// provisionStoredLogout configures provider "personal" with a stored
	// credential so logout scenarios (plain and --revoke) have a local
	// credential to remove.
	provisionStoredLogout := func(secret string) error {
		p := fixture.StdProvider("github", "example-user")
		p.Auth = config.Auth{Source: "stored", CredentialID: "github.com/personal"}
		w.Providers = map[string]config.Provider{"personal": p}
		w.Store = fixture.NewFakeCredentialStore()
		w.Store.Credentials[p.Auth.CredentialID] = credential.Credential{Kind: "bearer_token", Secret: secret}
		w.Credentials = w.Store
		w.Secrets = append(w.Secrets, secret)
		return w.SaveConfig()
	}
	ctx.Step(`^the provider API is unreachable$`, func() error {
		w.Client.AuthErr = errors.New("provider API unreachable")
		return nil
	})
	ctx.Step(`^provider "personal" supports provider-side revocation$`, func() error {
		w.Client.RevokeErr = nil
		return provisionStoredLogout("revoke-secret")
	})
	ctx.Step(`^provider-side revocation fails for provider "personal"$`, func() error {
		w.Client.RevokeErr = errors.New("remote revocation failed")
		return provisionStoredLogout("revoke-secret")
	})
	ctx.Step(`^provider-side revocation succeeds for provider "personal"$`, func() error {
		w.Client.RevokeErr = nil
		return provisionStoredLogout("revoke-secret")
	})
	ctx.Step(`^local credential deletion fails$`, func() error {
		if w.Store == nil {
			return errors.New("no injected credential store configured")
		}
		w.Store.DeleteErr = errors.New("credential deletion failed")
		return nil
	})
	ctx.Step(`^the provider reports revocation as unsupported$`, func() error {
		w.Client.RevokeErr = provider.ErrRevocationUnsupported
		return provisionStoredLogout("revoke-secret")
	})
	ctx.Step(`^the remote credential is revoked$`, func() error {
		if !w.Client.Called("Revoke") {
			return fmt.Errorf("revocation was not attempted: %v", w.Client.Calls)
		}
		return nil
	})
	ctx.Step(`^output reports "! remote revocation failed"$`, func() error {
		if !strings.Contains(w.Out, "! remote revocation failed") {
			return fmt.Errorf("remote revocation failure missing from %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^output reports local credential removal$`, func() error {
		if !strings.Contains(w.Out, "removed stored credential") {
			return fmt.Errorf("local credential removal missing from %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^output reports the remote revocation$`, func() error {
		if !strings.Contains(w.Out, "✓ remote credential revoked") {
			return fmt.Errorf("remote revocation report missing from %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^output reports "local credential removal failed"$`, func() error {
		if !strings.Contains(w.Out, "! local credential removal failed") {
			return fmt.Errorf("local credential removal failure missing from %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^output reports revocation is unsupported without exposing secrets$`, func() error {
		if !strings.Contains(w.Out, "provider-side revocation unsupported") || w.Leaked(w.Out) != "" {
			return fmt.Errorf("unsafe or missing unsupported-revocation report: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^the local persisted credential is removed$`, func() error {
		return assertStoredCredentialRemoved(w)
	})
	ctx.Step(`^the local persisted credential is still removed$`, func() error {
		return assertStoredCredentialRemoved(w)
	})
	ctx.Step(`^no revocation API call is attempted$`, func() error {
		if w.Client.Called("Revoke") {
			return fmt.Errorf("unexpected revocation attempt: %v", w.Client.Calls)
		}
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
	ctx.Step(`^I manually log in to (\w+) provider "([^"]*)"$`, func(pType, alias string) error {
		w.LoginAlias, w.LoginType = alias, pType
		w.RunArgs(w.LoginArgs())
		return nil
	})
	ctx.Step(`^interactive authentication succeeds$`, func() error {
		secret := "plaintext-consented-secret"
		w.Secrets = append(w.Secrets, secret)
		w.Interactive = true
		w.ReadToken = func() (string, error) { return secret, nil }
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
	ctx.Step(`^no reusable credential value appears in output or config$`, func() error {
		if err := checkOfflineClean(w); err != nil {
			return err
		}
		data, err := os.ReadFile(w.ConfigPath)
		if err != nil {
			return err
		}
		if secret := w.Leaked(string(data)); secret != "" {
			return fmt.Errorf("config leaks a credential value")
		}
		return nil
	})
	ctx.Step(`^provider "personal" has a valid API credential$`, func() error {
		return configureStatusProvider("https", false)
	})
	ctx.Step(`^provider API authentication succeeds for provider "personal"$`, func() error {
		return configureStatusProvider("https", false)
	})
	ctx.Step(`^provider "personal" is configured for (HTTPS|SSH) transport$`, func(transport string) error {
		p := w.Providers["personal"]
		if p.Type == "" {
			p = fixture.WithTokenEnv(fixture.StdProvider("github", "example-user"), fixture.TokenEnv)
			p.Default = true
		}
		p.Transport = strings.ToLower(transport)
		w.Providers["personal"] = p
		if transport == "SSH" {
			sshDir := filepath.Join(os.Getenv("HOME"), ".ssh")
			if err := os.MkdirAll(sshDir, 0o700); err != nil {
				return err
			}
			w.SSHState = map[string][]byte{filepath.Join(sshDir, "config"): []byte("Host github.com\n"), filepath.Join(sshDir, "id_test"): []byte("test-key")}
			for path, data := range w.SSHState {
				if err := os.WriteFile(path, data, 0o600); err != nil {
					return err
				}
			}
			w.SSHAgent = filepath.Join(w.Dir, "agent.sock")
			if err := os.Setenv("SSH_AUTH_SOCK", w.SSHAgent); err != nil {
				return err
			}
		}
		return w.SaveConfig()
	})
	ctx.Step(`^the current repository origin is "([^"]*)"$`, func(origin string) error {
		w.Git.Real, w.Git.OriginURL = false, origin
		return nil
	})
	ctx.Step(`^the current repository has a matching origin that rejects the configured transport$`, func() error {
		w.Git.Real = false
		w.Git.OriginURL = "https://github.com/example-user/demo.git"
		w.Git.LsRemoteErr = errors.New("access denied")
		return nil
	})
	ctx.Step(`^the current repository has a matching origin reachable through the configured transport$`, func() error {
		w.Git.Real = false
		w.Git.OriginURL = "git@github.com:example-user/demo.git"
		return nil
	})
	ctx.Step(`^the current directory has no origin matching provider "personal"$`, func() error {
		w.Git.Real = false
		w.Git.OriginURL = "https://example.org/example-user/demo.git"
		return nil
	})
	ctx.Step(`^the current repository has a matching origin$`, func() error {
		p := w.Providers["personal"]
		if p.Transport == "ssh" {
			w.Git.OriginURL = "git@github.com:example-user/demo.git"
		} else {
			w.Git.OriginURL = "https://github.com/example-user/demo.git"
		}
		w.Git.Real = false
		return nil
	})
	ctx.Step(`^the provider API credential is missing$`, func() error {
		return configureStatusProvider("ssh", true)
	})
	ctx.Step(`^status reports the provider API connection as "([^"]*)"$`, func(state string) error {
		want := state
		if state == "connected" {
			want = "✓ connected"
		} else if state != "not checked" {
			want = "✗ " + state
		}
		if !strings.Contains(w.Out, "Connection:  "+want) {
			return fmt.Errorf("connection state %q missing from %q", state, w.Out)
		}
		return nil
	})
	ctx.Step("^Colt runs bounded noninteractive `git ls-remote` against the current origin$", func() error {
		if !w.Git.ProbeBounded || len(w.Git.Operations) == 0 || !strings.HasPrefix(w.Git.Operations[len(w.Git.Operations)-1], "ls-remote:"+w.Git.OriginURL) {
			return fmt.Errorf("transport probe was not bounded or exact: operations=%v bounded=%v", w.Git.Operations, w.Git.ProbeBounded)
		}
		return nil
	})
	ctx.Step(`^Colt runs bounded noninteractive `+"`git ls-remote`"+` against "([^"]*)"$`, func(target string) error {
		if !w.Git.ProbeBounded || !slices.Contains(w.Git.Operations, "ls-remote:"+target) {
			return fmt.Errorf("transport probe was not bounded or exact: operations=%v bounded=%v", w.Git.Operations, w.Git.ProbeBounded)
		}
		return nil
	})
	ctx.Step(`^the provider client looks up repository "([^"]*)"$`, func(project string) error {
		if !w.Client.Called("Get:" + project) {
			return fmt.Errorf("repository lookup missing: %v", w.Client.Calls)
		}
		return nil
	})
	ctx.Step(`^repository metadata lookup is not attempted$`, func() error {
		if w.Client.Called("Get") {
			return fmt.Errorf("unexpected repository lookup: %v", w.Client.Calls)
		}
		return nil
	})
	ctx.Step(`^status reports Git authentication/connectivity and read access for clone, fetch, and pull$`, func() error {
		if !strings.Contains(w.Out, "Git authentication/connectivity and read access confirmed (clone/fetch/pull)") {
			return fmt.Errorf("Git read-access result missing from %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^status does not claim push or write permission was validated$`, func() error {
		if !strings.Contains(w.Out, "push/write not checked") || strings.Contains(w.Out, "push/write confirmed") || strings.Contains(w.Out, "write access confirmed") {
			return fmt.Errorf("unsafe write-access claim in %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^the HTTPS transport probe uses the transient Colt credential helper$`, func() error {
		if w.Git.ProbeAlias != "personal" || w.Git.ProbeProject != "demo" {
			return fmt.Errorf("probe scope=%s/%s", w.Git.ProbeAlias, w.Git.ProbeProject)
		}
		return nil
	})
	ctx.Step(`^native Git uses the user's SSH configuration and keys without a provider token$`, func() error {
		if w.Git.ProbeAlias != "personal" || w.Git.ProbeProject != "demo" {
			return fmt.Errorf("SSH probe scope=%s/%s", w.Git.ProbeAlias, w.Git.ProbeProject)
		}
		return nil
	})
	ctx.Step(`^status reports the transport as "([^"]*)"$`, func(state string) error {
		if !strings.Contains(w.Out, "Transport:   "+state) {
			return fmt.Errorf("transport state %q missing from %q", state, w.Out)
		}
		return nil
	})
	ctx.Step(`^status reports the transport as "not checked" with a safe metadata reason$`, func() error {
		if !strings.Contains(w.Out, "Transport:   not checked") || (!strings.Contains(w.Out, "metadata unavailable") && !strings.Contains(w.Out, "unexpected clone target")) {
			return fmt.Errorf("safe metadata result missing from %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^status reports the Git transport failure separately with a safe actionable reason$`, func() error {
		if !strings.Contains(w.Out, "Transport:   HTTPS · origin unreachable; check repository access and connectivity") {
			return fmt.Errorf("safe transport failure missing from %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^status does not report the transport as reachable$`, func() error {
		if strings.Contains(w.Out, "read access confirmed") {
			return fmt.Errorf("transport incorrectly reachable: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^status reports "credentials missing" for the provider API connection$`, func() error {
		if !strings.Contains(w.Out, "Connection:  ✗ credentials missing") {
			return fmt.Errorf("missing credential state absent: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^status reports the Git transport as reachable separately$`, func() error {
		if !strings.Contains(w.Out, "Transport:   SSH · ✓ Git authentication/connectivity and read access confirmed") {
			return fmt.Errorf("independent transport result absent: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^no Git or SSH transport command is invoked$`, func() error {
		for _, operation := range w.Git.Operations {
			if strings.HasPrefix(operation, "ls-remote:") {
				return fmt.Errorf("transport command invoked: %v", w.Git.Operations)
			}
		}
		return nil
	})
	ctx.Step(`^no provider, Git, or SSH command is invoked$`, func() error {
		if len(w.NewClientCalls) != 0 || len(w.Git.Operations) != 0 {
			return fmt.Errorf("offline access occurred: clients=%d git=%v", len(w.NewClientCalls), w.Git.Operations)
		}
		return nil
	})
	ctx.Step(`^the command fails with an actionable error mentioning "([^"]*)"$`, func(want string) error {
		if w.RunErr == nil || !strings.Contains(w.RunErr.Error(), want) {
			return fmt.Errorf("expected error containing %q, got %v", want, w.RunErr)
		}
		return nil
	})
	ctx.Step(`^no credential, provider, Git, or SSH read is attempted$`, func() error {
		gets := 0
		if w.Store != nil {
			gets = w.Store.Gets
		}
		if gets != 0 || len(w.NewClientCalls) != 0 || len(w.Client.Calls) != 0 || len(w.Git.Operations) != 0 {
			return fmt.Errorf("unexpected read: credentials=%d clients=%d provider=%v git=%v", gets, len(w.NewClientCalls), w.Client.Calls, w.Git.Operations)
		}
		return nil
	})
	ctx.Step(`^no reusable credential value appears in output, process arguments, the origin, or Git configuration$`, func() error {
		if secret := w.Leaked(w.Out + w.Git.OriginURL + strings.Join(w.Git.Operations, " ")); secret != "" {
			return fmt.Errorf("transport probe leaked a credential")
		}
		return nil
	})
	ctx.Step(`^Colt does not modify SSH configuration, keys, agents, or host verification$`, func() error {
		if len(w.SSHState) == 0 || os.Getenv("SSH_AUTH_SOCK") != w.SSHAgent {
			return errors.New("SSH state was not preserved")
		}
		for path, before := range w.SSHState {
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				return fmt.Errorf("SSH file changed: %s", path)
			}
		}
		return nil
	})
	ctx.Step(`^no provider configuration is changed$`, func() error {
		data, err := os.ReadFile(w.ConfigPath)
		if errors.Is(err, os.ErrNotExist) && len(w.ConfigBefore) == 0 {
			return nil
		}
		if err != nil {
			return err
		}
		if string(data) != string(w.ConfigBefore) {
			return fmt.Errorf("provider configuration was changed")
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
	ctx.Step(`^provider "([^"]*)" configuration is unchanged$`, func(_ string) error {
		data, err := os.ReadFile(w.ConfigPath)
		if err != nil || !bytes.Equal(data, w.ConfigBefore) {
			return fmt.Errorf("provider configuration changed")
		}
		return nil
	})
	ctx.Step(`^the provider receives the resolved credential without knowing its backend type$`, func() error {
		token, ok := w.LastToken()
		if !ok || token == "" || w.Leaked(token) == "" {
			return fmt.Errorf("provider did not receive the fallback credential")
		}
		return nil
	})
	ctx.Step(`^the credential is stored as "([^"]*)" without its value in config$`, func(id string) error {
		if w.Store == nil || w.Store.Puts != 1 || w.Store.Credentials[id].Secret == "" {
			return fmt.Errorf("credential was not stored under %q: %#v", id, w.Store)
		}
		data, err := os.ReadFile(w.ConfigPath)
		if err != nil || !strings.Contains(string(data), "credential_id: "+id) || w.Leaked(string(data)) != "" {
			return fmt.Errorf("unsafe stored credential config: %v", err)
		}
		return nil
	})
	ctx.Step(`^the credential is stored in the separate credential file, not in config\.yaml$`, func() error {
		if w.FallbackCredentials == nil {
			return errors.New("fallback store is not configured")
		}
		cred, err := w.FallbackCredentials.Get("gitlab.com/work")
		data, configErr := os.ReadFile(w.ConfigPath)
		if err != nil || configErr != nil || cred.Secret == "" || w.Leaked(string(data)) != "" {
			return fmt.Errorf("fallback credential/config state is invalid: credential=%v config=%v", err, configErr)
		}
		return nil
	})
	ctx.Step(`^the credential file has user-readable-only permissions$`, func() error {
		info, err := os.Stat(w.FallbackCredentials.Path)
		if err != nil || info.Mode().Perm() != 0o600 {
			return fmt.Errorf("credential file mode = %v: %v", info, err)
		}
		return nil
	})
	ctx.Step(`^output warns the file is plaintext protected only by filesystem permissions$`, func() error {
		if !strings.Contains(w.Out, "plaintext protected only by filesystem permissions") || w.Leaked(w.Out) != "" {
			return fmt.Errorf("unsafe or missing plaintext warning: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^no reusable credential is written to any file$`, func() error {
		for _, path := range []string{w.ConfigPath, credential.DefaultFilePath(w.ConfigPath)} {
			data, err := os.ReadFile(path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if w.Leaked(string(data)) != "" {
				return fmt.Errorf("credential leaked to %s", path)
			}
		}
		return nil
	})
	ctx.Step(`^output warns that plaintext storage requires explicit consent$`, func() error {
		if w.RunErr == nil || !strings.Contains(w.RunErr.Error(), "requires explicit consent") {
			return fmt.Errorf("missing consent warning: %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^the command reports that authentication was not persisted$`, func() error {
		if w.RunErr == nil || !strings.Contains(w.RunErr.Error(), "not persisted") {
			return fmt.Errorf("missing persistence failure: %v", w.RunErr)
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
	ctx.Step(`^the injected secure-store credential for "personal" is removed$`, func() error {
		if w.Store == nil || w.Store.Deletes != 1 {
			return fmt.Errorf("secure credential was not deleted: %#v", w.Store)
		}
		if _, ok := w.Store.Credentials["github.com/personal"]; ok {
			return errors.New("secure credential remains")
		}
		return nil
	})
	ctx.Step(`^the stored secret for "personal" is removed$`, func() error {
		if _, err := w.FallbackCredentials.Get("github.com/personal"); !errors.Is(err, credential.ErrNotFound) {
			return fmt.Errorf("plaintext credential remains: %v", err)
		}
		return nil
	})
	ctx.Step(`^unrelated credentials are unchanged$`, func() error {
		if w.Store != nil {
			if got := w.Store.Credentials["github.com/work"].Secret; got != "neighbor-secret" {
				return errors.New("unrelated secure credential changed")
			}
			return nil
		}
		got, err := w.FallbackCredentials.Get("github.com/work")
		if err != nil || got.Secret != "neighbor-secret" {
			return fmt.Errorf("unrelated plaintext credential changed: %v", err)
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
		if !strings.Contains(w.Out, "connected") || !w.Client.Called("Authenticate") || slices.ContainsFunc(w.Git.Operations, func(op string) bool { return strings.HasPrefix(op, "ls-remote:") }) {
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

func assertStoredCredentialRemoved(w *fixture.World) error {
	if w.Store == nil {
		return errors.New("no injected credential store configured")
	}
	if _, ok := w.Store.Credentials["github.com/personal"]; ok {
		return errors.New("local credential was not removed")
	}
	return nil
}
