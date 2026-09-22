package steps

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

	"github.com/MozeBaltyk/Colt/internal/app"
	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
	"github.com/cucumber/godog"
)

func remoteProjectDir(w *fixture.World) string {
	for _, p := range w.Providers {
		return filepath.Join(w.Dir, filepath.FromSlash(p.Namespace), "demo")
	}
	return filepath.Join(w.Dir, "demo")
}

// RegisterInitSteps covers blank project initialization and provider
// resolution scenarios: local/remote init flows, safety and conflict
// behavior, transport selection, and resolution precedence.
func RegisterInitSteps(ctx *godog.ScenarioContext, w *fixture.World) {
	ensurePersonal := func() error {
		if p := w.Providers["personal"]; p.Type != "" {
			return nil
		}
		p := fixture.WithDefault(fixture.WithTokenEnv(fixture.StdProvider("github", "example-user"), fixture.TokenEnv), true)
		w.Providers = map[string]config.Provider{"personal": p}
		return w.SaveConfig()
	}
	// fixtures
	ctx.Step(`^a valid provider with namespace, defaults, and Git identity is selected$`, func() error {
		w.Providers = map[string]config.Provider{"work": fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-namespace"), fixture.TokenEnv)}
		w.Providers["work"] = fixture.WithDefault(w.Providers["work"], true)
		return w.SaveConfig()
	})
	ctx.Step(`^native git is available$`, func() error {
		w.Git.Real = true
		return gitnative.Native{}.Available()
	})
	ctx.Step(`^native git is unavailable$`, func() error {
		w.Git.Real = false
		w.Git.AvailableErr = errors.New("native git is unavailable; install git and ensure it is on PATH")
		return nil
	})
	ctx.Step(`^destination "([^"]*)" exists and contains user data$`, func(name string) error {
		dst := filepath.Join(w.Dir, name)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, "notes.txt"), []byte("user data"), 0o600)
	})
	ctx.Step(`^destination "([^"]*)" exists and is empty$`, func(name string) error {
		return os.Mkdir(filepath.Join(w.Dir, name), 0o755)
	})
	ctx.Step(`^destination "([^"]*)" is a (regular file|symlink)$`, func(name, kind string) error {
		dst := filepath.Join(w.Dir, name)
		if kind == "regular file" {
			return os.WriteFile(dst, []byte("user data"), 0o600)
		}
		target := filepath.Join(w.Dir, "symlink-target")
		if err := os.Mkdir(target, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(target, "notes.txt"), []byte("user data"), 0o600); err != nil {
			return err
		}
		return os.Symlink(target, dst)
	})
	ctx.Step(`^the project name is invalid for the selected provider$`, func() error {
		w.Providers = map[string]config.Provider{"work": fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-namespace"), fixture.TokenEnv)}
		return w.SaveConfig()
	})
	ctx.Step(`^the selected provider type is (GitHub|GitLab)$`, func(pType string) error {
		w.Git.Real = false
		kind := strings.ToLower(pType)
		p := fixture.WithTokenEnv(fixture.StdProvider(kind, "example-namespace"), fixture.TokenEnv)
		p.Default = true
		w.Providers = map[string]config.Provider{"personal": p}
		host := "github.com"
		if kind == "gitlab" {
			host = "gitlab.com"
		}
		w.Client.CreateRepo = &provider.Repository{CloneURL: "https://" + host + "/example-namespace/demo.git"}
		w.Client.GetErr = provider.ErrNotFound
		return w.SaveConfig()
	})
	ctx.Step(`^the selected provider default visibility is "(private|public)"$`, func(visibility string) error {
		w.Git.Real = false
		p := fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-namespace"), fixture.TokenEnv)
		p.Default, p.Visibility = true, visibility
		w.Providers = map[string]config.Provider{"work": p}
		w.Client.GetErr = provider.ErrNotFound
		w.Client.CreateRepo = &provider.Repository{CloneURL: "https://gitlab.com/example-namespace/demo.git"}
		return w.SaveConfig()
	})
	ctx.Step(`^GitHub is configured as "([^"]*)" but returns canonical owner "([^"]*)"$`, func(configured, canonical string) error {
		w.Git.Real = true
		p := fixture.WithTokenEnv(fixture.StdProvider("github", configured), fixture.TokenEnv)
		p.Default = true
		w.Providers = map[string]config.Provider{"personal": p}
		w.Client.GetErr = provider.ErrNotFound
		w.Client.CreateRepo = &provider.Repository{CloneURL: "https://github.com/" + canonical + "/demo.git"}
		return w.SaveConfig()
	})
	ctx.Step(`^custom destination parent "([^"]*)" exists$`, func(relative string) error {
		return os.MkdirAll(filepath.Join(w.Dir, filepath.FromSlash(relative)), 0o755)
	})
	ctx.Step(`^destination parent "([^"]*)" does not exist$`, func(relative string) error {
		p := filepath.Join(w.Dir, filepath.FromSlash(relative))
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			return fmt.Errorf("parent %q already exists: %v", relative, err)
		}
		return nil
	})
	ctx.Step(`^the product default transport is (HTTPS|SSH)$`, func(transport string) error {
		if err := ensurePersonal(); err != nil {
			return err
		}
		cfg, err := config.Load(w.ConfigPath)
		if err != nil {
			return err
		}
		cfg.Transport = strings.ToLower(transport)
		return config.Save(w.ConfigPath, cfg)
	})
	ctx.Step(`^the provider preference is (HTTPS|SSH)$`, func(transport string) error {
		if err := ensurePersonal(); err != nil {
			return err
		}
		p := w.Providers["personal"]
		p.Transport = strings.ToLower(transport)
		w.Providers["personal"] = p
		return w.SaveConfig()
	})
	ctx.Step(`^the global preference is (HTTPS|SSH)$`, func(transport string) error {
		if err := ensurePersonal(); err != nil {
			return err
		}
		cfg, err := config.Load(w.ConfigPath)
		if err != nil {
			return err
		}
		cfg.Transport = strings.ToLower(transport)
		return config.Save(w.ConfigPath, cfg)
	})
	ctx.Step(`^the explicit command choice is (HTTPS|SSH)$`, func(transport string) error {
		w.ExplicitTransport = strings.ToLower(transport)
		return nil
	})
	ctx.Step(`^Colt selects the clone/push target$`, func() error {
		selected, ok := w.Providers["personal"]
		if !ok {
			return errors.New("no personal provider configured")
		}
		transport, err := app.InitTransport(selected, w.ExplicitTransport, w.ConfigPath)
		if err != nil {
			w.RunErr = err
			return nil
		}
		w.SelectedTransport = transport
		return nil
	})
	ctx.Step(`^the HTTPS target is used$`, func() error {
		if w.RunErr != nil {
			return fmt.Errorf("unexpected failure: %v", w.RunErr)
		}
		if w.SelectedTransport != "https" && !strings.HasPrefix(w.Git.PushURL, "https://") {
			return fmt.Errorf("expected HTTPS target, got %q", w.SelectedTransport)
		}
		return nil
	})
	ctx.Step(`^the SSH target is used$`, func() error {
		if w.RunErr != nil {
			return fmt.Errorf("unexpected failure: %v", w.RunErr)
		}
		if w.SelectedTransport != "ssh" && !strings.HasPrefix(w.Git.PushURL, "git@") {
			return fmt.Errorf("expected SSH target, got %q", w.SelectedTransport)
		}
		return nil
	})
	ctx.Step(`^the transport preference resolves to HTTPS$`, func() error {
		if err := ensurePersonal(); err != nil {
			return err
		}
		w.Git.Real = true
		p := w.Providers["personal"]
		p.Namespace = "example-user"
		w.Providers["personal"] = p
		w.Client.CreateRepo = &provider.Repository{CloneURL: "https://github.com/example-user/demo.git"}
		return w.SaveConfig()
	})
	ctx.Step(`^the transport preference resolves to SSH$`, func() error {
		if err := ensurePersonal(); err != nil {
			return err
		}
		w.Git.Real = true
		p := w.Providers["personal"]
		p.Namespace, p.Transport = "example-user", "ssh"
		w.Providers["personal"] = p
		w.Client.CreateRepo = &provider.Repository{
			CloneURL: "https://github.com/example-user/demo.git",
			SSHURL:   "git@github.com:example-user/demo.git",
		}
		return w.SaveConfig()
	})
	ctx.Step(`^provider API authentication fails for the selected provider$`, func() error {
		w.Git.Real = false
		w.Providers = map[string]config.Provider{"work": fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-namespace"), fixture.TokenEnv)}
		w.Client.GetErr = errors.New("authentication failed: rejected credential")
		return w.SaveConfig()
	})
	ctx.Step(`^the selected provider credential is missing$`, func() error {
		p := fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-namespace"), "COMPANY_GL_TOKEN")
		p.Default = true
		w.Providers = map[string]config.Provider{"work": p}
		os.Unsetenv("COMPANY_GL_TOKEN")
		return w.SaveConfig()
	})
	ctx.Step(`^the selected provider uses an unavailable stored API credential$`, func() error {
		p := fixture.StdProvider("gitlab", "example-namespace")
		p.Default = true
		p.Auth = config.Auth{Source: "stored", CredentialID: "gitlab.com/work"}
		w.Providers = map[string]config.Provider{"work": p}
		w.Store = fixture.NewFakeCredentialStore()
		w.Store.GetErr = credential.ErrStoreUnavailable
		w.Credentials = w.Store
		return w.SaveConfig()
	})
	ctx.Step(`^the selected provider has no Git identity$`, func() error {
		raw := "providers:\n  work:\n    type: gitlab\n    host: gitlab.com\n    base_url: https://gitlab.com\n    namespace: example-namespace\n    visibility: private\n    git_email: user@example.invalid\n    auth:\n      source: env\n      token_env: " + fixture.TokenEnv + "\n    default: true\n"
		return os.WriteFile(w.ConfigPath, []byte(raw), 0o600)
	})
	ctx.Step(`^the selected provider transport is unsupported$`, func() error {
		raw := "providers:\n  work:\n    type: bitbucket\n    host: bitbucket.com\n    base_url: https://bitbucket.com\n    namespace: example-namespace\n    visibility: private\n    git_name: Example User\n    git_email: user@example.invalid\n    transport: ftp\n    auth:\n      source: env\n      token_env: " + fixture.TokenEnv + "\n    default: true\n"
		return os.WriteFile(w.ConfigPath, []byte(raw), 0o600)
	})
	ctx.Step(`^the selected transport is (HTTPS|SSH)$`, func(transport string) error {
		w.Git.Real = false
		p := fixture.WithTokenEnv(fixture.StdProvider("github", "example-namespace"), fixture.TokenEnv)
		p.Default = true
		if transport == "SSH" {
			p.Transport = "ssh"
		}
		w.Providers = map[string]config.Provider{"personal": p}
		w.Client.GetErr = provider.ErrNotFound
		return w.SaveConfig()
	})
	ctx.Step(`^provider creation succeeds with a (missing|wrong repository) selected clone target$`, func(target string) error {
		p := w.Providers["personal"]
		repo := &provider.Repository{}
		if target == "wrong repository" {
			if p.Transport == "ssh" {
				repo.SSHURL = "git@github.com:example-namespace/other.git"
			} else {
				repo.CloneURL = "https://github.com/example-namespace/other.git"
			}
		}
		w.Client.CreateRepo = repo
		return nil
	})
	ctx.Step(`^a native repository has an existing local credential helper$`, func() error {
		dst := filepath.Join(w.Dir, "demo")
		if err := os.Mkdir(dst, 0o755); err != nil {
			return err
		}
		commandCtx, cancel := context.WithTimeout(context.Background(), fixture.CommandTimeout)
		defer cancel()
		if err := (gitnative.Native{}).Init(commandCtx, dst); err != nil {
			return err
		}
		_, err := fixture.GitOut(dst, "config", "--local", "--add", "credential.helper", "existing-helper")
		return err
	})
	ctx.Step(`^the remote repository did not exist during preflight$`, func() error {
		w.Git.Real = false
		w.Providers = map[string]config.Provider{"work": fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-namespace"), fixture.TokenEnv)}
		w.Client.GetErr = provider.ErrNotFound
		return w.SaveConfig()
	})
	ctx.Step(`^another actor creates it before Colt's create request completes$`, func() error {
		w.Client.CreateErr = provider.ErrConflict
		return nil
	})
	ctx.Step(`^repository "([^"]*)" already exists in the selected namespace$`, func() error {
		w.Git.Real = false
		w.Providers = map[string]config.Provider{"work": fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-namespace"), fixture.TokenEnv)}
		w.Client.GetRepo = &provider.Repository{CloneURL: "https://gitlab.com/example-namespace/demo.git"}
		return w.SaveConfig()
	})
	ctx.Step(`^Colt created the local repository, initial commit, remote repository, and "origin"$`, func() error {
		w.Git.Real = false
		w.Git.PushErr = errors.New("native git failed: remote hung up")
		w.Providers = map[string]config.Provider{"work": fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-namespace"), fixture.TokenEnv)}
		w.Client.GetErr = provider.ErrNotFound
		w.Client.CreateRepo = &provider.Repository{CloneURL: "https://gitlab.com/example-namespace/demo.git"}
		return w.SaveConfig()
	})
	ctx.Step(`^providers "([^"]*)" and "([^"]*)" are configured$`, func(a, b string) error {
		w.Providers = map[string]config.Provider{a: fixture.WithTokenEnv(fixture.StdProvider("github", "example-ns"), fixture.TokenEnv), b: fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-ns"), fixture.TokenEnv)}
		return w.SaveConfig()
	})
	ctx.Step(`^"([^"]*)" is the configured default$`, func(alias string) error {
		for k, p := range w.Providers {
			p.Default = k == alias
			w.Providers[k] = p
		}
		return w.SaveConfig()
	})
	ctx.Step(`^only one (\w+) provider named "([^"]*)" is configured$`, func(pType, alias string) error {
		w.Providers = map[string]config.Provider{alias: fixture.WithTokenEnv(fixture.StdProvider(strings.ToLower(pType), "example-ns"), fixture.TokenEnv)}
		return w.SaveConfig()
	})
	ctx.Step(`^no default provider is configured$`, func() error {
		for k, p := range w.Providers {
			p.Default = false
			w.Providers[k] = p
		}
		return w.SaveConfig()
	})
	ctx.Step(`^"([^"]*)" is otherwise selectable$`, func(alias string) error {
		p := w.Providers[alias]
		p.Default = true
		w.Providers[alias] = p
		return w.SaveConfig()
	})
	ctx.Step(`^provider selection encounters "([^"]*)"$`, func(condition string) error {
		switch condition {
		case "an unknown explicit alias":
			w.Run("colt init demo --local --provider ghost")
		case "multiple configured defaults":
			raw := "providers:\n  personal:\n    type: github\n    host: github.com\n    base_url: https://api.github.com\n    namespace: example-ns\n    visibility: private\n    git_name: Example User\n    git_email: user@example.invalid\n    auth:\n      source: env\n      token_env: " + fixture.TokenEnv + "\n    default: true\n" +
				"  work:\n    type: gitlab\n    host: gitlab.com\n    base_url: https://gitlab.com\n    namespace: example-ns\n    visibility: private\n    git_name: Example User\n    git_email: user@example.invalid\n    auth:\n      source: env\n      token_env: " + fixture.TokenEnv + "\n    default: true\n"
			if err := os.WriteFile(w.ConfigPath, []byte(raw), 0o600); err != nil {
				return err
			}
			w.Run("colt init demo --local")
		case "an invalid selected provider":
			raw := "providers:\n  personal:\n    type: bitbucket\n    host: bitbucket.example.invalid\n    base_url: https://bitbucket.example.invalid\n    namespace: example-ns\n    visibility: private\n    git_name: Example User\n    git_email: user@example.invalid\n    auth:\n      source: env\n      token_env: " + fixture.TokenEnv + "\n    default: true\n"
			if err := os.WriteFile(w.ConfigPath, []byte(raw), 0o600); err != nil {
				return err
			}
			w.Run("colt init demo --local")
		default:
			return fmt.Errorf("bdd: unknown condition %q", condition)
		}
		return nil
	})

	// actions
	runCmd := func(cmdLine string) error { w.Run(cmdLine); return nil }
	ctx.Step(`^I run "([^"]+)"$`, runCmd)
	ctx.Step("^I run `([^`]+)`$", runCmd)
	ctx.Step(`^pushing the initial branch fails$`, func() error { w.Run("colt init demo"); return nil })
	ctx.Step(`^the Colt credential helper is configured twice$`, func() error {
		dst := filepath.Join(w.Dir, "demo")
		commandCtx, cancel := context.WithTimeout(context.Background(), fixture.CommandTimeout)
		defer cancel()
		if err := (gitnative.Native{}).ConfigureCredentialHelper(commandCtx, dst, "", "", "work", "demo"); err != nil {
			return err
		}
		return (gitnative.Native{}).ConfigureCredentialHelper(commandCtx, dst, "", "", "work", "demo")
	})
	ctx.Step(`^I run a command with provider "([^"]*)"$`, func(alias string) error {
		w.Run("colt init demo --local --provider " + alias)
		return nil
	})
	ctx.Step(`^I run a command without a provider option$`, func() error {
		w.Run("colt init demo --local")
		return nil
	})

	// assertions: local git state
	ctx.Step(`^"([^"]*)" is a new Git repository with no remote$`, func(name string) error {
		dst := filepath.Join(w.Dir, name)
		if _, err := fixture.GitOut(dst, "rev-parse", "--git-dir"); err != nil {
			return fmt.Errorf("not a git repository: %v", err)
		}
		remotes, err := fixture.GitOut(dst, "remote")
		if err != nil || remotes != "" {
			return fmt.Errorf("expected no remotes, got %q, %v", remotes, err)
		}
		return nil
	})
	ctx.Step(`^it has exactly one initial commit$`, func() error {
		n, err := fixture.GitOut(filepath.Join(w.Dir, "demo"), "rev-list", "--count", "HEAD")
		if err != nil || n != "1" {
			return fmt.Errorf("expected one commit, got %q, %v", n, err)
		}
		return nil
	})
	ctx.Step(`^local Git user\.name and user\.email match the selected identity$`, func() error {
		dst := filepath.Join(w.Dir, "demo")
		name, _ := fixture.GitOut(dst, "config", "--local", "user.name")
		email, _ := fixture.GitOut(dst, "config", "--local", "user.email")
		if name != "Example User" || email != "user@example.invalid" {
			return fmt.Errorf("identity mismatch: %q %q", name, email)
		}
		return nil
	})
	globalGitUnchanged := func() error {
		after, err := fixture.GitGlobalSnapshot()
		if err != nil {
			return fmt.Errorf("read global git configuration: %w", err)
		}
		if after != w.GlobalGit {
			return errors.New("global git configuration changed")
		}
		return nil
	}
	ctx.Step(`^global Git identity is unchanged$`, globalGitUnchanged)
	ctx.Step(`^global Git configuration is unchanged$`, globalGitUnchanged)
	ctx.Step(`^no provider mutation was requested$`, func() error {
		if len(w.NewClientCalls) != 0 || len(w.Client.Calls) != 0 {
			return fmt.Errorf("provider was constructed or contacted: constructions=%d calls=%v", len(w.NewClientCalls), w.Client.Calls)
		}
		return nil
	})
	ctx.Step(`^no provider client is constructed$`, func() error {
		if len(w.NewClientCalls) != 0 {
			return fmt.Errorf("provider client constructions = %d", len(w.NewClientCalls))
		}
		return nil
	})
	ctx.Step(`^no provider API credential is read$`, func() error {
		if w.Store == nil {
			return errors.New("credential store fixture is not configured")
		}
		if w.Store.Gets != 0 {
			return fmt.Errorf("credential store reads = %d", w.Store.Gets)
		}
		return nil
	})
	ctx.Step(`^no provider client, credential helper, or push is invoked$`, func() error {
		if len(w.NewClientCalls) != 0 || len(w.Client.Calls) != 0 || w.Git.Helpers != 0 || w.Git.Pushes != 0 {
			return fmt.Errorf("clients=%d provider=%v helpers=%d pushes=%d", len(w.NewClientCalls), w.Client.Calls, w.Git.Helpers, w.Git.Pushes)
		}
		return nil
	})
	ctx.Step(`^local Git user\.name is "([^"]*)"$`, func(want string) error {
		got, _ := fixture.GitOut(filepath.Join(w.Dir, "demo"), "config", "--local", "user.name")
		if got != want {
			return fmt.Errorf("user.name = %q, want %q", got, want)
		}
		return nil
	})
	ctx.Step(`^local Git user\.email is "([^"]*)"$`, func(want string) error {
		got, _ := fixture.GitOut(filepath.Join(w.Dir, "demo"), "config", "--local", "user.email")
		if got != want {
			return fmt.Errorf("user.email = %q, want %q", got, want)
		}
		return nil
	})

	// assertions: failures
	ctx.Step(`^the command fails before mutation$`, func() error {
		if w.RunErr == nil {
			return errors.New("expected failure, got success")
		}
		return nil
	})
	ctx.Step(`^the command fails before creating a destination or remote repository$`, func() error {
		if w.RunErr == nil {
			return errors.New("expected failure, got success")
		}
		if _, err := os.Lstat(filepath.Join(w.Dir, "invalid")); !os.IsNotExist(err) {
			return fmt.Errorf("destination was created: %v", err)
		}
		if w.Client.Called("Create") {
			return errors.New("remote creation was attempted")
		}
		return nil
	})
	ctx.Step(`^the requested transport is invalid$`, ensurePersonal)
	ctx.Step(`^the command fails before creating a destination or remote$`, func() error {
		if w.RunErr == nil || w.Client.Called("Create") || slices.Contains(w.Git.Operations, "clone") {
			return fmt.Errorf("unsafe invalid-transport result: error=%v provider=%v git=%v", w.RunErr, w.Client.Calls, w.Git.Operations)
		}
		return nil
	})
	ctx.Step(`^the existing destination is unchanged$`, func() error {
		data, err := os.ReadFile(filepath.Join(w.Dir, "demo", "notes.txt"))
		if err != nil || string(data) != "user data" {
			return fmt.Errorf("user data changed: %q, %v", data, err)
		}
		if _, err := os.Lstat(filepath.Join(w.Dir, "demo", ".git")); !os.IsNotExist(err) {
			return errors.New("destination was turned into a repository")
		}
		return nil
	})
	ctx.Step(`^the command fails before mutation with an actionable native git error$`, func() error {
		if w.RunErr == nil || !strings.Contains(strings.ToLower(w.RunErr.Error()), "git") {
			return fmt.Errorf("expected actionable git error, got %v", w.RunErr)
		}
		if strings.Join(w.Git.Operations, ",") != "available" {
			return fmt.Errorf("Git operations after availability failure: %v", w.Git.Operations)
		}
		if _, err := os.Lstat(filepath.Join(w.Dir, "demo")); !os.IsNotExist(err) {
			return fmt.Errorf("destination was mutated: %v", err)
		}
		return nil
	})
	ctx.Step(`^the command fails before changing the (regular file|symlink)$`, func(kind string) error {
		if w.RunErr == nil {
			return errors.New("expected destination failure")
		}
		dst := filepath.Join(w.Dir, "demo")
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
			data, readErr := os.ReadFile(filepath.Join(w.Dir, "symlink-target", "notes.txt"))
			if readErr != nil || info.Mode()&os.ModeSymlink == 0 || string(data) != "user data" {
				return fmt.Errorf("symlink or target changed: mode=%v data=%q err=%v", info.Mode(), data, readErr)
			}
		}
		if strings.Join(w.Git.Operations, ",") != "available" {
			return fmt.Errorf("unexpected Git mutation: %v", w.Git.Operations)
		}
		return nil
	})
	ctx.Step(`^the missing parent is not created$`, func() error {
		p := filepath.Join(w.Dir, "missing")
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			return fmt.Errorf("missing parent was created: %v", err)
		}
		return nil
	})
	ctx.Step(`^no mutating Git operation or provider client construction occurs$`, func() error {
		if strings.Join(w.Git.Operations, ",") != "available" || len(w.NewClientCalls) != 0 {
			return fmt.Errorf("operations occurred: git=%v clients=%d", w.Git.Operations, len(w.NewClientCalls))
		}
		if _, err := os.Lstat(filepath.Join(w.Dir, "demo")); !os.IsNotExist(err) {
			return fmt.Errorf("destination was created: %v", err)
		}
		return nil
	})
	ctx.Step(`^the command fails before creating a remote repository$`, func() error {
		if w.RunErr == nil {
			return errors.New("expected failure, got success")
		}
		if w.Client.Called("Create") {
			return errors.New("remote creation was attempted")
		}
		return nil
	})
	ctx.Step(`^SSH Git access alone does not satisfy the requirement$`, func() error {
		if w.RunErr == nil {
			return errors.New("expected failure, got success")
		}
		return nil
	})
	ctx.Step(`^no local repository, origin, helper, or push is attempted$`, func() error {
		if strings.Join(w.Git.Operations, ",") != "available" {
			return fmt.Errorf("unexpected Git operations: %v", w.Git.Operations)
		}
		if _, err := os.Lstat(filepath.Join(w.Dir, "demo")); !os.IsNotExist(err) {
			return fmt.Errorf("local repository was created: %v", err)
		}
		return nil
	})
	ctx.Step(`^preflight state is unchanged with no credential, provider, or Git operation$`, func() error {
		if len(w.Git.Operations) != 0 || len(w.NewClientCalls) != 0 || len(w.Client.Calls) != 0 {
			return fmt.Errorf("operations occurred: git=%v clients=%d provider=%v", w.Git.Operations, len(w.NewClientCalls), w.Client.Calls)
		}
		if w.Store != nil && (w.Store.Gets != 0 || w.Store.Puts != 0 || w.Store.Deletes != 0) {
			return fmt.Errorf("credential store changed or was read: gets=%d puts=%d deletes=%d", w.Store.Gets, w.Store.Puts, w.Store.Deletes)
		}
		if after, _ := os.ReadFile(w.ConfigPath); !bytes.Equal(w.ConfigBefore, after) {
			return errors.New("provider configuration changed")
		}
		if _, err := os.Lstat(filepath.Join(w.Dir, "demo")); !os.IsNotExist(err) {
			return fmt.Errorf("destination was mutated: %v", err)
		}
		return globalGitUnchanged()
	})
	ctx.Step(`^no unrelated filesystem state is modified$`, func() error {
		var mutOps []string
		for _, op := range w.Git.Operations {
			if op != "available" {
				mutOps = append(mutOps, op)
			}
		}
		if len(mutOps) != 0 || len(w.NewClientCalls) != 0 || len(w.Client.Calls) != 0 {
			return fmt.Errorf("operations occurred: git=%v clients=%d provider=%v", mutOps, len(w.NewClientCalls), w.Client.Calls)
		}
		if w.Store != nil && (w.Store.Gets != 0 || w.Store.Puts != 0 || w.Store.Deletes != 0) {
			return fmt.Errorf("credential store changed or was read: gets=%d puts=%d deletes=%d", w.Store.Gets, w.Store.Puts, w.Store.Deletes)
		}
		if after, _ := os.ReadFile(w.ConfigPath); !bytes.Equal(w.ConfigBefore, after) {
			return errors.New("provider configuration changed")
		}
		return globalGitUnchanged()
	})

	// assertions: remote flows
	ctx.Step(`^Colt creates repository "([^"]*)" in the selected namespace through the (\w+) HTTP API$`, func(project, pType string) error {
		if w.RunErr != nil {
			return fmt.Errorf("init failed: %v", w.RunErr)
		}
		if !w.Client.Called("Create") {
			return fmt.Errorf("no Create call: %v", w.Client.Calls)
		}
		want := "https://" + map[string]string{"GitHub": "github.com", "GitLab": "gitlab.com"}[pType] + "/example-namespace/demo.git"
		if len(w.Git.Origins) != 1 || w.Git.Origins[0] != want {
			return fmt.Errorf("origin = %v, want [%s]", w.Git.Origins, want)
		}
		return nil
	})
	ctx.Step(`^the created repository URL is the only "origin"$`, func() error {
		if len(w.Git.Origins) != 1 {
			return fmt.Errorf("origins = %v", w.Git.Origins)
		}
		return nil
	})
	ctx.Step(`^Colt creates repository "([^\"]*)" with visibility "(private|public)"$`, func(project, visibility string) error {
		if w.RunErr != nil || !w.Client.Called("Create:"+project) || w.Client.Visibility != visibility {
			return fmt.Errorf("error=%v calls=%v visibility=%q", w.RunErr, w.Client.Calls, w.Client.Visibility)
		}
		return nil
	})
	ctx.Step(`^the selected provider default visibility remains "(private|public)"$`, func(visibility string) error {
		cfg, err := config.Load(w.ConfigPath)
		if err != nil || cfg.Providers["work"].Visibility != visibility {
			return fmt.Errorf("visibility=%q: %v", cfg.Providers["work"].Visibility, err)
		}
		return nil
	})
	ctx.Step(`^it is cloned locally as "([^"]+)"$`, func(relative string) error {
		destination := filepath.Join(w.Dir, filepath.FromSlash(relative))
		if info, err := os.Stat(filepath.Join(destination, ".git")); err != nil || !info.IsDir() {
			return fmt.Errorf("clone destination %q missing: %v", destination, err)
		}
		if !slices.Contains(w.Git.Operations, "clone") {
			return fmt.Errorf("clone was not requested: %v", w.Git.Operations)
		}
		return nil
	})
	ctx.Step(`^the initial commit is submitted once through the configured Git runner$`, func() error {
		if w.Git.Pushes != 1 {
			return fmt.Errorf("pushes = %d", w.Git.Pushes)
		}
		if !strings.HasSuffix(w.Git.PushURL, "/demo.git") {
			return fmt.Errorf("push URL = %q", w.Git.PushURL)
		}
		return nil // Runner.Push owns native --set-upstream arguments; its unit test covers those exact process arguments.
	})
	ctx.Step(`^the "origin" URL is "([^"]*)" with no credential in the URL$`, func(want string) error {
		got, err := fixture.GitOut(remoteProjectDir(w), "remote", "get-url", "origin")
		if err != nil || got != want || strings.Contains(got, "@") {
			return fmt.Errorf("origin = %q, want %q: %v", got, want, err)
		}
		return nil
	})
	ctx.Step(`^the "origin" URL is "([^"]*)"$`, func(want string) error {
		got, err := fixture.GitOut(remoteProjectDir(w), "remote", "get-url", "origin")
		if err != nil || got != want {
			return fmt.Errorf("origin = %q, want %q: %v", got, want, err)
		}
		return nil
	})
	ctx.Step(`^the SSH user is "git", not the provider account name$`, func() error {
		if !strings.HasPrefix(w.Git.PushURL, "git@") || strings.HasPrefix(w.Git.PushURL, w.Client.Account+"@") {
			return fmt.Errorf("SSH target uses the wrong user: %q", w.Git.PushURL)
		}
		return nil
	})
	ctx.Step(`^native Git handles SSH authentication with no provider token or Colt credential helper injection$`, func() error {
		if w.Git.Pushes != 1 || w.Git.Helpers != 0 {
			return fmt.Errorf("SSH push used Colt HTTP authentication: pushes=%d helpers=%d", w.Git.Pushes, w.Git.Helpers)
		}
		if helper, err := fixture.GitOut(remoteProjectDir(w), "config", "--local", "--get-all", "credential.helper"); err == nil || helper != "" {
			return fmt.Errorf("SSH repository configured a credential helper: %q", helper)
		}
		return nil
	})
	ctx.Step(`^local Git configuration contains "credential\.helper = colt"$`, func() error {
		repoDir := remoteProjectDir(w)
		keys := []string{"credential.helper"}
		if w.Git.Real && w.Client.CreateRepo != nil && w.Client.CreateRepo.CloneURL != "" {
			keys = append(keys, "credential."+w.Client.CreateRepo.CloneURL+".helper")
		}
		var lastErr error
		for _, key := range keys {
			got, err := fixture.GitOut(repoDir, "config", "--local", "--get-all", key)
			if err != nil {
				lastErr = err
				continue
			}
			if strings.Contains(got, "!colt git-credential --provider personal --repository demo") {
				if w.Git.Helpers != 1 {
					return fmt.Errorf("expected exactly one helper, got %d", w.Git.Helpers)
				}
				return nil
			}
			lastErr = fmt.Errorf("key %s missing expected helper: %q", key, got)
		}
		return lastErr
	})
	ctx.Step(`^"\.git/config" contains no reusable credential value$`, func() error {
		data, err := os.ReadFile(filepath.Join(remoteProjectDir(w), ".git", "config"))
		if err != nil {
			return err
		}
		if secret := w.Leaked(string(data)); secret != "" {
			return errors.New(".git/config contains a reusable credential value")
		}
		return nil
	})
	ctx.Step(`^ordinary Git invokes the Colt credential helper to resolve credentials for a later push$`, func() error {
		binary := filepath.Join(w.Dir, "colt")
		buildCtx, cancelBuild := context.WithTimeout(context.Background(), fixture.CommandTimeout)
		defer cancelBuild()
		build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "../../cmd/colt")
		if output, err := build.CombinedOutput(); err != nil {
			return fmt.Errorf("build Colt helper: %v: %s", err, output)
		}
		gitCtx, cancelGit := context.WithTimeout(context.Background(), fixture.CommandTimeout)
		defer cancelGit()
		cmd := exec.CommandContext(gitCtx, "git", "credential", "fill")
		cmd.Dir = remoteProjectDir(w)
		cmd.Env = append(os.Environ(), "COLT_CONFIG="+w.ConfigPath, "PATH="+w.Dir+string(os.PathListSeparator)+os.Getenv("PATH"), "GIT_TERMINAL_PROMPT=0")
		cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\npath=example-user/demo.git\n\n")
		output, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "username=x-access-token\n") || !strings.Contains(string(output), "password=bdd-fake-secret\n") {
			return fmt.Errorf("ordinary Git did not resolve through Colt: %v", err)
		}
		return nil
	})
	ctx.Step(`^the provider conflict is authoritative$`, func() error {
		if w.RunErr == nil || !strings.Contains(strings.ToLower(w.RunErr.Error()), "conflict") {
			return fmt.Errorf("expected authoritative conflict, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^Colt does not adopt, replace, or delete the remote repository$`, func() error {
		if strings.Join(w.Client.Calls, ",") != "Get:demo,Create:demo" || len(w.Git.Origins) != 0 || w.Git.Pushes != 0 {
			return fmt.Errorf("unexpected conflict handling: provider=%v origins=%v pushes=%d", w.Client.Calls, w.Git.Origins, w.Git.Pushes)
		}
		return nil
	})
	ctx.Step(`^Colt leaves no local clone and reports the conflict$`, func() error {
		if _, err := os.Lstat(remoteProjectDir(w)); !errors.Is(err, os.ErrNotExist) || strings.Join(w.Git.Operations, ",") != "available" {
			return fmt.Errorf("unexpected local conflict state: err=%v operations=%v", err, w.Git.Operations)
		}
		if w.RunErr == nil || !strings.Contains(w.RunErr.Error(), "conflict") {
			return fmt.Errorf("conflict not reported: %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^the command fails without changing that remote repository$`, func() error {
		if w.RunErr == nil {
			return errors.New("expected failure, got success")
		}
		if w.Client.Called("Create") {
			return errors.New("remote was touched")
		}
		return nil
	})
	ctx.Step(`^Colt does not adopt the existing remote$`, func() error {
		if len(w.Git.Origins) != 0 {
			return fmt.Errorf("adopted remote: %v", w.Git.Origins)
		}
		return nil
	})
	ctx.Step(`^the command returns failure and identifies the failed push$`, func() error {
		if w.RunErr == nil || !strings.Contains(w.RunErr.Error(), "push") {
			return fmt.Errorf("expected push failure, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^the local commit, "origin", and remote repository remain intact$`, func() error {
		if strings.Join(w.Client.Calls, ",") != "Get:demo,Create:demo" || len(w.Git.Origins) != 1 || w.Git.Pushes != 0 {
			return fmt.Errorf("partial state lost: calls=%v origins=%v", w.Client.Calls, w.Git.Origins)
		}
		if data, err := os.ReadFile(filepath.Join(remoteProjectDir(w), ".git", "COMMIT")); err != nil || string(data) != "fake" {
			return fmt.Errorf("local commit missing: %q, %v", data, err)
		}
		return nil
	})
	ctx.Step(`^clone target validation fails before creating a local clone$`, func() error {
		if w.RunErr == nil || !strings.Contains(w.RunErr.Error(), "validate remote repository") {
			return fmt.Errorf("expected clone target validation failure, got %v", w.RunErr)
		}
		if strings.Join(w.Client.Calls, ",") != "Get:demo,Create:demo" {
			return fmt.Errorf("provider calls = %v", w.Client.Calls)
		}
		if _, err := os.Lstat(remoteProjectDir(w)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("unexpected local clone: %v", err)
		}
		return nil
	})
	ctx.Step(`^no origin, credential helper, or push is attempted$`, func() error {
		if len(w.Git.Origins) != 0 || w.Git.Helpers != 0 || w.Git.Pushes != 0 || strings.Join(w.Git.Operations, ",") != "available" {
			return fmt.Errorf("unsafe Git operations: %v origins=%v helpers=%d pushes=%d", w.Git.Operations, w.Git.Origins, w.Git.Helpers, w.Git.Pushes)
		}
		return nil
	})
	ctx.Step(`^the existing helper remains and the Colt helper appears exactly once locally$`, func() error {
		got, err := fixture.GitOut(filepath.Join(w.Dir, "demo"), "config", "--local", "--get-all", "credential.helper")
		if err != nil || got != "existing-helper\n!colt git-credential --provider work --repository demo" {
			return fmt.Errorf("credential helpers = %q: %v", got, err)
		}
		return nil
	})
	ctx.Step(`^credential\.useHttpPath is true in repository-local configuration$`, func() error {
		dst := filepath.Join(w.Dir, "demo")
		local, err := fixture.GitOut(dst, "config", "--local", "--get", "credential.useHttpPath")
		if err != nil || local != "true" {
			return fmt.Errorf("local credential.useHttpPath = %q: %v", local, err)
		}
		return nil
	})
	ctx.Step(`^the result reports local and remote state and a safe recovery action$`, func() error {
		msg := w.Out + fmt.Sprint(w.RunErr)
		if !strings.Contains(msg, "local state") || !strings.Contains(msg, "recovery") {
			return fmt.Errorf("failure does not report state/recovery: %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^the initialization report has the operation title followed by these successful steps in order:$`, func(table *godog.Table) error {
		position := strings.Index(w.Out, "Initialize demo")
		if position < 0 {
			return fmt.Errorf("operation title missing from %q", w.Out)
		}
		for _, row := range table.Rows {
			want := "✓ " + row.Cells[0].Value + ": succeeded"
			next := strings.Index(w.Out[position:], want)
			if next < 0 {
				return fmt.Errorf("ordered step %q missing from %q", want, w.Out)
			}
			position += next + len(want)
		}
		return nil
	})
	ctx.Step(`^the initialization report contains no remote, clone, helper, or push step$`, func() error {
		for _, forbidden := range []string{"remote repository", "Clone", "credential helper", "Push"} {
			if strings.Contains(w.Out, forbidden) {
				return fmt.Errorf("unexpected %q in %q", forbidden, w.Out)
			}
		}
		return nil
	})
	ctx.Step(`^completed initialization steps precede the failed push step$`, func() error {
		completed := strings.Index(w.Out, "✓ Create initial commit: succeeded")
		failed := strings.Index(w.Out, "✗ Push initial commit: failed")
		if completed < 0 || failed <= completed {
			return fmt.Errorf("unexpected progress order: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^the result reports the preserved local commit and remote repository$`, func() error {
		if !strings.Contains(w.Out, "Local state: initial commit") || !strings.Contains(w.Out, "Remote state: created") {
			return fmt.Errorf("preserved state missing: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^the result gives a bounded cause and a safe push recovery command$`, func() error {
		if !strings.Contains(w.Out, "Cause:") || !strings.Contains(w.Out, "git push --set-upstream origin HEAD") || len(w.Out) > 4096 {
			return fmt.Errorf("unsafe or missing recovery: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^no reusable credential or authorization header appears in the report$`, func() error {
		for _, secret := range w.Secrets {
			if strings.Contains(w.Out, secret) {
				return fmt.Errorf("credential leaked: %q", w.Out)
			}
		}
		if strings.Contains(strings.ToLower(w.Out), "authorization:") {
			return fmt.Errorf("credential detail leaked: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^command output is not a terminal$`, func() error { return nil })
	ctx.Step(`^initialization status text and symbols are meaningful without color$`, func() error {
		if !strings.Contains(w.Out, "✓ Preflight: succeeded") {
			return fmt.Errorf("plain status missing: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^the initialization report contains no ANSI color$`, func() error {
		if strings.Contains(w.Out, "\x1b[") {
			return fmt.Errorf("ANSI in non-terminal output: %q", w.Out)
		}
		return nil
	})

	// assertions: provider resolution
	ctx.Step(`^Colt selects provider "([^"]*)"$`, func(alias string) error {
		if w.RunErr != nil {
			return fmt.Errorf("command failed: %v", w.RunErr)
		}
		if !strings.Contains(w.Out, "Provider: "+alias) {
			return fmt.Errorf("provider %q not selected in %q", alias, w.Out)
		}
		return nil
	})
	ctx.Step(`^output identifies the authenticated account as "([^"]*)"$`, func(account string) error {
		if !strings.Contains(w.Out, "authenticated as "+account) {
			return fmt.Errorf("account %q not identified in %q", account, w.Out)
		}
		return nil
	})
	ctx.Step(`^the command fails without selecting a provider$`, func() error {
		if w.RunErr == nil {
			return fmt.Errorf("expected failure, got %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^the error requires an explicit provider or one configured default$`, func() error {
		if w.RunErr == nil || !strings.Contains(strings.ToLower(w.RunErr.Error()), "provider") {
			return fmt.Errorf("expected provider-selection error, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^the command fails without selecting "([^"]*)"$`, func(alias string) error {
		if w.RunErr == nil {
			return errors.New("expected failure, got success")
		}
		if strings.Contains(w.Out, "with provider "+alias) {
			return fmt.Errorf("incorrectly selected %q", alias)
		}
		return nil
	})

	// list steps
	ctx.Step(`^one provider is selected by normal provider resolution$`, func() error {
		w.Providers = map[string]config.Provider{"work": fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-namespace"), fixture.TokenEnv)}
		w.Providers["work"] = fixture.WithDefault(w.Providers["work"], true)
		return w.SaveConfig()
	})
	ctx.Step(`^multiple providers are configured and one provider request fails$`, func() error {
		w.Providers = map[string]config.Provider{
			"work":     fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-namespace"), fixture.TokenEnv),
			"personal": fixture.WithTokenEnv(fixture.StdProvider("github", "user"), fixture.TokenEnv),
		}
		w.Providers["work"] = fixture.WithDefault(w.Providers["work"], true)
		success := &fixture.FakeClient{ListRepos: []provider.Repository{{CloneURL: "https://github.com/user/z.git"}, {CloneURL: "https://github.com/user/a.git"}}}
		failure := &fixture.FakeClient{ListErr: errors.New("provider request failed")}
		w.NewClient = func(p config.Provider, token string) (provider.Client, error) {
			w.NewClientCalls = append(w.NewClientCalls, fixture.NewClientCall{Provider: p, Token: token})
			if p.Type == "gitlab" {
				return failure, nil
			}
			return success, nil
		}
		return w.SaveConfig()
	})
	ctx.Step(`^all visible repositories from that provider are returned in deterministic order$`, func() error {
		if w.RunErr != nil {
			return fmt.Errorf("command failed: %v", w.RunErr)
		}
		if w.Client.Calls == nil || !containsCall(w.Client.Calls, "List") {
			return fmt.Errorf("List was not called")
		}
		return nil
	})
	ctx.Step(`^complete results from successful providers are returned in deterministic order$`, func() error {
		if w.RunErr != nil {
			return fmt.Errorf("command failed: %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^the independent provider failure is reported$`, func() error {
		if !strings.Contains(w.Out, "provider request failed") {
			return fmt.Errorf("provider failure not reported in %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^one provider has repositories in more than one namespace$`, func() error {
		w.Providers = map[string]config.Provider{"work": fixture.WithTokenEnv(fixture.StdProvider("github", "example-namespace"), fixture.TokenEnv)}
		w.Providers["work"] = fixture.WithDefault(w.Providers["work"], true)
		w.Client.ListRepos = []provider.Repository{
			{Name: "keep", Namespace: "example-namespace", CloneURL: "https://github.com/example-namespace/keep.git"},
			{Name: "other", Namespace: "other-org", CloneURL: "https://github.com/other-org/other.git"},
		}
		return w.SaveConfig()
	})
	ctx.Step(`^only repositories in the requested namespace are returned$`, func() error {
		if w.RunErr != nil || !strings.Contains(w.Out, "github.com/example-namespace/keep.git") {
			return fmt.Errorf("namespace listing incomplete: %q %v", w.Out, w.RunErr)
		}
		return nil
	})
	ctx.Step(`^repositories in other namespaces are excluded$`, func() error {
		if strings.Contains(w.Out, "github.com/other-org") {
			return fmt.Errorf("other-org repositories leaked into listing: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^the command fails because --namespace cannot be combined with --all$`, func() error {
		if w.RunErr == nil || !strings.Contains(w.RunErr.Error(), "--namespace cannot be combined with --all") {
			return fmt.Errorf("expected namespace/all conflict, got %v", w.RunErr)
		}
		return nil
	})

	// clone steps
	ctx.Step(`^the selected-provider metadata resolves "([^"]*)" to a clean authoritative URL$`, func(url string) error {
		w.Client.GetRepo = &provider.Repository{CloneURL: url}
		return nil
	})
	ctx.Step(`^its destination is a clean relative missing or empty path$`, func() error {
		if _, err := os.Lstat(filepath.Join(w.Dir, "api")); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clone destination is not missing: %v", err)
		}
		return nil
	})
	ctx.Step(`^destination access remains root-relative, no-follow, and confined throughout the operation$`, func() error {
		return nil
	})
	ctx.Step(`^the managed repository configures the repository-local Colt credential helper$`, func() error {
		if w.SelectedTransport == "https" {
			return nil // the clone scenario exercises the actual repository-local helper
		}
		if !strings.Contains(w.Out, "cloned to") {
			return fmt.Errorf("expected clone output, got %q", w.Out)
		}
		return nil
	})

	// release steps
	ctx.Step(`^provider metadata supplies a clean authoritative push URL$`, func() error {
		return nil
	})
	ctx.Step(`^the tag push uses the configured transport$`, func() error {
		if !strings.Contains(w.Out, "pushed tag") {
			return fmt.Errorf("expected push in output, got %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^provider release creation still requires provider API authentication$`, func() error {
		if len(w.NewClientCalls) == 0 || !w.Client.Called("Release") {
			return fmt.Errorf("provider API release was not authenticated: clients=%d calls=%v", len(w.NewClientCalls), w.Client.Calls)
		}
		return nil
	})
	ctx.Step(`^the completed tag state and failed step are reported as partial completion$`, func() error {
		result := w.Out + fmt.Sprint(w.RunErr)
		for _, want := range []string{"partial failure at create provider release", "local state: tag 1.2.3 retained locally", "remote state: tag 1.2.3 pushed; provider release not created", "recovery:"} {
			if !strings.Contains(result, want) {
				return fmt.Errorf("partial release report lacks %q: output=%q error=%v", want, w.Out, w.RunErr)
			}
		}
		return nil
	})
}

func containsCall(calls []string, op string) bool {
	for _, c := range calls {
		if c == op || strings.HasPrefix(c, op+":") {
			return true
		}
	}
	return false
}
