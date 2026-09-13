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
	ctx.Step(`^the transport preference resolves to HTTPS$`, func() error {
		w.Git.Real = true
		p := w.Providers["personal"]
		p.Namespace = "example-user"
		w.Providers["personal"] = p
		w.Client.CreateRepo = &provider.Repository{CloneURL: "https://github.com/example-user/demo.git"}
		return w.SaveConfig()
	})
	ctx.Step(`^the transport preference resolves to SSH$`, func() error {
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
		got, err := fixture.GitOut(remoteProjectDir(w), "config", "--local", "--get-all", "credential.helper")
		if err != nil || !strings.Contains(got, "!colt git-credential --provider personal --repository demo") || w.Git.Helpers != 1 {
			return fmt.Errorf("local credential helpers = %q: %v", got, err)
		}
		return nil
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
		msg := fmt.Sprint(w.RunErr)
		if !strings.Contains(msg, "local state") || !strings.Contains(msg, "recovery") {
			return fmt.Errorf("failure does not report state/recovery: %v", w.RunErr)
		}
		return nil
	})

	// assertions: provider resolution
	ctx.Step(`^Colt selects provider "([^"]*)"$`, func(alias string) error {
		if w.RunErr != nil {
			return fmt.Errorf("command failed: %v", w.RunErr)
		}
		if !strings.Contains(w.Out, "with provider "+alias) {
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
}
