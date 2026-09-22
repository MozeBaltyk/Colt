package steps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
	"github.com/cucumber/godog"
)

// RegisterCoreGitSteps activates the CORE-GIT helper/SSH transport scenarios
// and the CORE-PROVIDER-005 status-source / CORE-CREDENTIAL-002 redaction
// scenarios. Real `git` and a freshly built `colt` binary are used where a
// credential helper must actually resolve; everything else stays in-process
// with the fake provider client and injected credential stores.
func RegisterCoreGitSteps(ctx *godog.ScenarioContext, w *fixture.World) {
	// managedDir tracks the repository created by the HTTPS-helper scenarios.
	var managedDir string
	var managedConfigBefore []byte
	var managedRemote string

	buildBinary := func() error {
		bin := filepath.Join(w.Dir, "colt")
		build := exec.Command("go", "build", "-o", bin, "../../cmd/colt")
		build.Env = fixture.BuildEnv()
		if out, err := build.CombinedOutput(); err != nil {
			return fmt.Errorf("build colt helper: %v: %s", err, out)
		}
		return nil
	}

	// configureStoredProvider writes the provider config and a plaintext-fallback
	// credential so both the in-process app and the real `colt git-credential`
	// helper resolve it deterministically from the sandbox config directory.
	configureStoredProvider := func(alias, secret string) error {
		p := fixture.StdProvider("github", "example-user")
		p.Auth = config.Auth{Source: "stored", CredentialID: "github.com/" + alias}
		w.Providers = map[string]config.Provider{alias: p}
		if err := w.SaveConfig(); err != nil {
			return err
		}
		store := credential.NewFileStore(credential.DefaultFilePath(w.ConfigPath))
		if err := store.Put("github.com/"+alias, credential.Credential{Kind: "bearer_token", Secret: secret}); err != nil {
			return err
		}
		w.Secrets = append(w.Secrets, secret)
		w.Credentials = credential.NewMemoryStore()
		w.FallbackCredentials = store
		return nil
	}

	initManagedRepo := func(dir, remote, alias, project string) error {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := (gitnative.Native{}).Init(context.Background(), dir); err != nil {
			return err
		}
		if err := (gitnative.Native{}).AddOrigin(context.Background(), dir, remote); err != nil {
			return err
		}
		return (gitnative.Native{}).ConfigureCredentialHelper(context.Background(), dir, remote, "", alias, project)
	}

	runFill := func(host, path string) {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.CommandTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", "credential", "fill")
		cmd.Dir = managedDir
		cmd.Env = append(os.Environ(),
			"COLT_CONFIG="+w.ConfigPath,
			"PATH="+w.Dir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"GIT_TERMINAL_PROMPT=0",
			"GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_CONFIG_NOSYSTEM=1",
		)
		cmd.Stdin = strings.NewReader("protocol=https\nhost=" + host + "\npath=" + path + "\n\n")
		output, err := cmd.CombinedOutput()
		w.Out, w.RunErr = string(output), err
	}

	sshProbeTarget := func() string {
		for _, op := range w.Git.Operations {
			if strings.HasPrefix(op, "ls-remote:") {
				return strings.TrimPrefix(op, "ls-remote:")
			}
		}
		return ""
	}

	// --- HTTPS managed-repository / credential-helper scenarios ---
	ctx.Step(`^a managed repository has remote "([^"]*)"$`, func(remote string) error {
		u, err := url.Parse(remote)
		if err != nil {
			return err
		}
		parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		if len(parts) != 2 || u.Host != "github.com" {
			return fmt.Errorf("unsupported managed repository remote %q", remote)
		}
		project := strings.TrimSuffix(parts[1], ".git")
		if err := configureStoredProvider("personal", "helper-persisted-secret"); err != nil {
			return err
		}
		if err := buildBinary(); err != nil {
			return err
		}
		managedDir = filepath.Join(w.Dir, "repo")
		managedRemote = remote
		return initManagedRepo(managedDir, remote, "personal", project)
	})

	ctx.Step(`^I run ordinary "git push" without invoking Colt$`, func() error {
		runFill("github.com", "example-user/example-project.git")
		return nil
	})

	ctx.Step(`^Git resolves credentials through the repository-local Colt credential helper$`, func() error {
		if w.RunErr != nil || !strings.Contains(w.Out, "username=x-access-token\npassword=helper-persisted-secret\n") {
			return fmt.Errorf("helper error=%v output=%q", w.RunErr, w.Out)
		}
		return nil
	})

	ctx.Step(`^no provider credential is re-entered$`, func() error {
		if strings.Contains(w.Out, "Username") || strings.Contains(w.Out, "password for") {
			return fmt.Errorf("credential was re-entered interactively: %q", w.Out)
		}
		return nil
	})

	ctx.Step(`^provider "personal" has a persisted credential$`, func() error {
		return configureStoredProvider("personal", "push-persisted-secret")
	})

	ctx.Step(`^the managed repository configures the Colt credential helper$`, func() error {
		if err := buildBinary(); err != nil {
			return err
		}
		managedDir = filepath.Join(w.Dir, "repo")
		return initManagedRepo(managedDir, "https://github.com/example-user/demo.git", "personal", "demo")
	})

	ctx.Step(`^I run ordinary "git push"$`, func() error {
		runFill("github.com", "example-user/demo.git")
		return nil
	})

	ctx.Step(`^the push authenticates without re-entering credentials$`, func() error {
		if w.RunErr != nil || !strings.Contains(w.Out, "username=x-access-token\npassword=push-persisted-secret\n") {
			return fmt.Errorf("helper error=%v output=%q", w.RunErr, w.Out)
		}
		return nil
	})

	ctx.Step(`^a managed HTTPS repository$`, func() error {
		if err := configureStoredProvider("personal", "https-remote-secret"); err != nil {
			return err
		}
		managedDir = filepath.Join(w.Dir, filepath.FromSlash("example-user"), "demo")
		return initManagedRepo(managedDir, "https://github.com/example-user/demo.git", "personal", "demo")
	})

	ctx.Step(`^a managed HTTPS repository configures the Colt credential helper$`, func() error {
		if err := configureStoredProvider("personal", "logout-helper-secret"); err != nil {
			return err
		}
		managedDir = filepath.Join(w.Dir, "repo")
		if err := initManagedRepo(managedDir, "https://github.com/example-user/demo.git", "personal", "demo"); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(managedDir, ".git", "config"))
		if err != nil {
			return err
		}
		managedConfigBefore = data
		managedRemote = "https://github.com/example-user/demo.git"
		return nil
	})

	ctx.Step(`^the "origin" URL contains no token, password, or userinfo$`, func() error {
		out, err := fixture.GitOut(managedDir, "remote", "get-url", "origin")
		if err != nil || out != "https://github.com/example-user/demo.git" || strings.Contains(out, "@") {
			return fmt.Errorf("origin = %q: %v", out, err)
		}
		return nil
	})

	ctx.Step(`^the repository remote and helper configuration are unchanged$`, func() error {
		data, err := os.ReadFile(filepath.Join(managedDir, ".git", "config"))
		if err != nil || !bytes.Equal(data, managedConfigBefore) {
			return fmt.Errorf(".git/config changed: %v", err)
		}
		out, err := fixture.GitOut(managedDir, "remote", "get-url", "origin")
		if err != nil || out != managedRemote {
			return fmt.Errorf("remote changed to %q: %v", out, err)
		}
		return nil
	})

	ctx.Step(`^a later "git push" falls through without a stored credential$`, func() error {
		if err := buildBinary(); err != nil {
			return err
		}
		runFill("github.com", "example-user/demo.git")
		// The stored credential was removed by logout: the helper returns no
		// password, so ordinary Git falls through to another helper/prompt and,
		// with prompts disabled, fails rather than re-entering a credential.
		if strings.Contains(w.Out, "password=") {
			return fmt.Errorf("stored credential still resolved: %q", w.Out)
		}
		return nil
	})

	// --- SSH transport scenarios ---
	ctx.Step(`^provider "personal" is configured for SSH transport with authoritative SSH URL "([^"]*)"$`, func(sshURL string) error {
		p := fixture.WithTokenEnv(fixture.StdProvider("github", "example-user"), fixture.TokenEnv)
		p.Transport = "ssh"
		w.Providers = map[string]config.Provider{"personal": p}
		w.Git.Real = false
		w.Store = fixture.NewFakeCredentialStore()
		w.Credentials = w.Store
		w.Client.GetRepo = &provider.Repository{
			CloneURL: "https://github.com/example-user/example-project.git",
			SSHURL:   sshURL,
		}
		return w.SaveConfig()
	})

	ctx.Step(`^the SSH transport validates the provider-authoritative SSH URL "([^"]*)"$`, func(want string) error {
		if w.RunErr != nil {
			return fmt.Errorf("status failed: %v", w.RunErr)
		}
		if got := sshProbeTarget(); got != want {
			return fmt.Errorf("SSH transport validated %q, want %q (operations=%v)", got, want, w.Git.Operations)
		}
		return nil
	})

	ctx.Step(`^the SSH remote uses "git" as the SSH user, not the provider account name$`, func() error {
		target := sshProbeTarget()
		if !strings.HasPrefix(target, "git@") {
			return fmt.Errorf("SSH remote does not use git as the user: %q", target)
		}
		if strings.HasPrefix(target, w.Client.Account+"@") {
			return fmt.Errorf("SSH remote uses the provider account name: %q", target)
		}
		return nil
	})

	ctx.Step(`^the repository owner "([^"]*)" remains in the repository path$`, func(owner string) error {
		target := sshProbeTarget()
		if !strings.Contains(target, ":"+owner+"/") {
			return fmt.Errorf("repository owner %q missing from SSH path %q", owner, target)
		}
		return nil
	})

	ctx.Step(`^the SSH transport probe succeeds without writing "~/.ssh/config"$`, func() error {
		if w.RunErr != nil {
			return fmt.Errorf("status failed: %v", w.RunErr)
		}
		target := sshProbeTarget()
		if target == "" || !strings.HasPrefix(target, "git@github.com:") {
			return fmt.Errorf("SSH transport probe did not run against the SSH origin: %v", w.Git.Operations)
		}
		// The user's pre-existing SSH environment must suffice: the probe must not
		// have created a ~/.ssh/config entry for authentication.
		configPath := filepath.Join(w.Dir, "home", ".ssh", "config")
		if before, set := w.SSHState[configPath]; set {
			after, err := os.ReadFile(configPath)
			if err != nil || !bytes.Equal(before, after) {
				return fmt.Errorf("~/.ssh/config was modified: %v", err)
			}
		} else if _, err := os.Stat(configPath); err == nil {
			return errors.New("~/.ssh/config was created by Colt")
		}
		return nil
	})

	ctx.Step(`^the Colt credential subsystem holds no SSH private-key material$`, func() error {
		reject := func(s string) error {
			if strings.Contains(s, "PRIVATE KEY") || strings.Contains(s, "ssh-rsa") || strings.Contains(s, "ssh-ed25519") || strings.Contains(s, "BEGIN OPENSSH") {
				return fmt.Errorf("credential subsystem holds SSH key material")
			}
			return nil
		}
		for _, secret := range w.Secrets {
			if err := reject(secret); err != nil {
				return err
			}
		}
		if w.Store != nil {
			for _, cred := range w.Store.Credentials {
				if err := reject(cred.Secret); err != nil {
					return err
				}
			}
		}
		return nil
	})

	ctx.Step(`^Colt never invokes key generation or agent management$`, func() error {
		for _, op := range w.Git.Operations {
			if strings.Contains(op, "keygen") || strings.Contains(op, "ssh-add") || strings.Contains(op, "ssh-agent") {
				return fmt.Errorf("colt invoked SSH key generation or agent management: %v", w.Git.Operations)
			}
		}
		return nil
	})

	ctx.Step(`^the configured Git transport is SSH$`, func() error {
		p := fixture.StdProvider("github", "example-user")
		p.Transport = "ssh"
		w.Providers = map[string]config.Provider{"work": p}
		w.Git.Real = false
		sshDir := filepath.Join(w.Dir, ".ssh")
		if err := os.MkdirAll(sshDir, 0o700); err != nil {
			return err
		}
		w.SSHState = map[string][]byte{
			filepath.Join(sshDir, "id_test"): []byte("representative-private-key"),
		}
		for path, contents := range w.SSHState {
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				return err
			}
		}
		w.SSHAgent = filepath.Join(w.Dir, "agent.sock")
		if err := os.Setenv("SSH_AUTH_SOCK", w.SSHAgent); err != nil {
			return err
		}
		return w.SaveConfig()
	})

	ctx.Step(`^native Git SSH access to a repository works$`, func() error {
		secret := "bdd-device-token"
		w.Secrets = append(w.Secrets, secret)
		w.AuthorizeGitHubDevice = func(_ context.Context, out io.Writer) (string, error) {
			w.DeviceFlowCalls++
			fmt.Fprint(out, "Open: https://github.com/login/device\nCode: BDD-CODE\n")
			return secret, nil
		}
		return nil
	})

	ctx.Step(`^the SSH key is not used for provider API authentication$`, func() error {
		token, ok := w.LastToken()
		if !ok || token == "" {
			return errors.New("provider API did not receive a token")
		}
		if token != "bdd-device-token" {
			return fmt.Errorf("provider API used a non-device-flow credential: %q", token)
		}
		return nil
	})

	ctx.Step(`^provider "personal" API authentication succeeds over SSH transport$`, func() error {
		p := fixture.WithTokenEnv(fixture.StdProvider("github", "example-user"), fixture.TokenEnv)
		p.Transport = "ssh"
		w.Providers = map[string]config.Provider{"personal": p}
		w.Git.Real = false
		w.Git.OriginURL = "git@github.com:example-user/demo.git"
		return w.SaveConfig()
	})

	ctx.Step(`^native Git SSH access fails because no registered public key matches$`, func() error {
		w.Git.LsRemoteErr = errors.New("public key authorization failed")
		return nil
	})

	ctx.Step(`^repository access is checked$`, func() error {
		w.Run("colt auth status")
		return nil
	})

	ctx.Step(`^the Git failure is reported separately from provider authentication$`, func() error {
		if !strings.Contains(w.Out, "Connection:  ✓ connected") || !strings.Contains(w.Out, "Transport:   SSH · origin unreachable") {
			return fmt.Errorf("transport failure not reported separately:\n%s", w.Out)
		}
		return nil
	})

	ctx.Step(`^provider "personal" has a persisted credential and user SSH keys$`, func() error {
		p := fixture.StdProvider("github", "example-user")
		p.Auth = config.Auth{Source: "stored", CredentialID: "github.com/personal"}
		w.Providers = map[string]config.Provider{"personal": p}
		if err := w.SaveConfig(); err != nil {
			return err
		}
		w.Store = fixture.NewFakeCredentialStore()
		w.Store.Credentials["github.com/personal"] = credential.Credential{Kind: "bearer_token", Secret: "ssh-logout-secret"}
		w.Secrets = append(w.Secrets, "ssh-logout-secret")
		w.Credentials = w.Store
		sshDir := filepath.Join(w.Dir, ".ssh")
		if err := os.MkdirAll(sshDir, 0o700); err != nil {
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

	ctx.Step(`^the current repository remote is the alias "([^"]*)"$`, func(aliasURL string) error {
		w.Git.OriginURL = aliasURL
		return nil
	})

	ctx.Step(`^the alias is treated as a local SSH name, not a provider authority$`, func() error {
		target := sshProbeTarget()
		if strings.Contains(target, "github-personal") {
			return fmt.Errorf("SSH alias was mistaken for a provider authority: %q", target)
		}
		return nil
	})

	ctx.Step(`^the transport is validated against the expected provider repository$`, func() error {
		if got := sshProbeTarget(); got != "git@github.com:example-user/example-project.git" {
			return fmt.Errorf("transport was validated against %q, want the authoritative SSH URL", got)
		}
		return nil
	})

	// --- CORE-PROVIDER-005 status-source reporting + redaction ---
	ctx.Step(`^providers are configured with environment, secure-store, and plaintext credential sources$`, func() error {
		envP := fixture.WithTokenEnv(fixture.StdProvider("github", "example-user"), "COLT_OFFLINE_ENV")
		secP := fixture.StdProvider("github", "example-user")
		secP.Auth = config.Auth{Source: "stored", CredentialID: "github.com/secp"}
		ptP := fixture.StdProvider("github", "example-user")
		ptP.Auth = config.Auth{Source: "stored", CredentialID: "github.com/ptp"}
		w.Providers = map[string]config.Provider{"envp": envP, "ptp": ptP, "secp": secP}
		if err := w.SaveConfig(); err != nil {
			return err
		}
		w.SetSecret("COLT_OFFLINE_ENV", "offline-env-read-secret")
		w.Store = fixture.NewFakeCredentialStore()
		w.Store.Credentials["github.com/secp"] = credential.Credential{Kind: "bearer_token", Secret: "offline-secure-read-secret"}
		w.Secrets = append(w.Secrets, "offline-secure-read-secret")
		w.Credentials = w.Store
		w.FallbackCredentials = credential.NewFileStore(credential.DefaultFilePath(w.ConfigPath))
		if err := w.FallbackCredentials.Put("github.com/ptp", credential.Credential{Kind: "bearer_token", Secret: "offline-plaintext-read-secret"}); err != nil {
			return err
		}
		w.Secrets = append(w.Secrets, "offline-plaintext-read-secret")
		return nil
	})

	ctx.Step(`^no environment credential value is read$`, func() error {
		if len(w.NewClientCalls) != 0 || strings.Contains(w.Out, os.Getenv("COLT_OFFLINE_ENV")) {
			return errors.New("environment credential was resolved or exposed")
		}
		return nil
	})

	ctx.Step(`^no secure credential value is read$`, func() error {
		if w.Store != nil && w.Store.Gets != 0 {
			return fmt.Errorf("secure credential store reads = %d", w.Store.Gets)
		}
		return nil
	})

	ctx.Step(`^no plaintext credential value is read$`, func() error {
		if w.FallbackCredentials == nil {
			return errors.New("no plaintext credential store configured")
		}
		if len(w.NewClientCalls) != 0 || strings.Contains(w.Out, "offline-plaintext-read-secret") {
			return errors.New("plaintext credential was resolved or exposed")
		}
		return nil
	})

	ctx.Step(`^provider "personal" has a persisted credential the provider rejects$`, func() error {
		p := fixture.StdProvider("github", "example-user")
		p.Auth = config.Auth{Source: "stored", CredentialID: "github.com/personal"}
		w.Providers = map[string]config.Provider{"personal": p}
		if err := w.SaveConfig(); err != nil {
			return err
		}
		secret := "rejected-persisted-secret"
		w.Store = fixture.NewFakeCredentialStore()
		w.Store.Credentials["github.com/personal"] = credential.Credential{Kind: "bearer_token", Secret: secret}
		w.Secrets = append(w.Secrets, secret)
		w.Credentials = w.Store
		w.Client.AuthErr = errors.New("authentication failed: rejected credential")
		return nil
	})

	ctx.Step(`^the command reports an authentication failure advising re-login$`, func() error {
		if w.RunErr != nil || !strings.Contains(w.Out, "✗ authentication failed") || !strings.Contains(w.Out, "Run: colt auth login github personal") {
			return fmt.Errorf("re-login advice missing from %q (err=%v)", w.Out, w.RunErr)
		}
		return nil
	})

	ctx.Step(`^output does not contain the rejected secret value$`, func() error {
		if secret := w.Leaked(w.Out); secret != "" {
			return fmt.Errorf("rejected credential leaked: %q", secret)
		}
		return nil
	})

	ctx.Step(`^its credential "([^"]*)" is valid$`, func(secret string) error {
		w.Store = fixture.NewFakeCredentialStore()
		w.Store.Credentials["github.com/personal"] = credential.Credential{Kind: "bearer_token", Secret: secret}
		w.Secrets = append(w.Secrets, secret)
		w.Credentials = w.Store
		return nil
	})

	ctx.Step(`^status shows "Credential: stored"$`, func() error {
		if !strings.Contains(w.Out, "Credential:  stored") {
			return fmt.Errorf("stored credential source not reported:\n%s", w.Out)
		}
		return nil
	})

	ctx.Step(`^status shows "Credential: environment"$`, func() error {
		if !strings.Contains(w.Out, "Credential:  environment") {
			return fmt.Errorf("environment credential source not reported:\n%s", w.Out)
		}
		return nil
	})

	ctx.Step(`^status shows "connected"$`, func() error {
		if !strings.Contains(w.Out, "✓ connected") {
			return fmt.Errorf("connected state not reported:\n%s", w.Out)
		}
		return nil
	})

	ctx.Step(`^status shows "Auth source: stored"$`, func() error {
		if !strings.Contains(w.Out, "Auth source: stored") {
			return fmt.Errorf("offline stored auth source not reported:\n%s", w.Out)
		}
		return nil
	})

	ctx.Step(`^status shows "Credential ID: ([^"]*)"$`, func(id string) error {
		if !strings.Contains(w.Out, "Credential ID: "+id) {
			return fmt.Errorf("offline credential id %q not reported:\n%s", id, w.Out)
		}
		return nil
	})

	ctx.Step(`^status shows "not checked"$`, func() error {
		if !strings.Contains(w.Out, "Connection:  not checked") {
			return fmt.Errorf("offline not-checked state not reported:\n%s", w.Out)
		}
		return nil
	})

	ctx.Step(`^no secret value is read or displayed$`, func() error {
		if secret := w.Leaked(w.Out); secret != "" {
			return fmt.Errorf("secret value displayed: %q", secret)
		}
		if whic := len(w.NewClientCalls) != 0 || len(w.Git.Operations) != 0 || (w.Store != nil && w.Store.Gets != 0); whic {
			return errors.New("secret value was resolved")
		}
		return nil
	})

	ctx.Step(`^the plaintext credential file contains "([^"]*)"$`, func(secret string) error {
		p := fixture.StdProvider("github", "example-user")
		p.Auth = config.Auth{Source: "stored", CredentialID: "github.com/personal"}
		w.Providers = map[string]config.Provider{"personal": p}
		if err := w.SaveConfig(); err != nil {
			return err
		}
		w.Credentials = credential.NewMemoryStore()
		w.FallbackCredentials = credential.NewFileStore(credential.DefaultFilePath(w.ConfigPath))
		if err := w.FallbackCredentials.Put("github.com/personal", credential.Credential{Kind: "bearer_token", Secret: secret}); err != nil {
			return err
		}
		w.Secrets = append(w.Secrets, secret)
		return nil
	})

	ctx.Step(`^any auth command runs with verbose output enabled$`, func() error {
		w.Run("colt auth status -v")
		return nil
	})

	ctx.Step(`^normal, verbose, error, and diagnostic output do not contain "([^"]*)"$`, func(secret string) error {
		all := w.Out + fmt.Sprint(w.RunErr)
		if strings.Contains(all, secret) {
			return fmt.Errorf("credential appeared in verbose/error/diagnostic output")
		}
		data, err := os.ReadFile(w.ConfigPath)
		if err == nil && strings.Contains(string(data), secret) {
			return fmt.Errorf("credential appeared in config.yaml")
		}
		return nil
	})
}
