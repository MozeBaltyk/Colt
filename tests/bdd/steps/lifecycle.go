package steps

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
	"github.com/cucumber/godog"
)

func RegisterLifecycleSteps(ctx *godog.ScenarioContext, w *fixture.World) {
	var outside, moved, sentinel string
	var hostileMarker string
	configure := func(transport string) error {
		p := fixture.WithDefault(fixture.WithTokenEnv(fixture.StdProvider("github", "example-user"), fixture.TokenEnv), true)
		p.Transport = transport
		w.Providers = map[string]config.Provider{"personal": p}
		w.Git.Real = false
		return w.SaveConfig()
	}
	prepareRelease := func(transport string) error {
		if err := configure(transport); err != nil {
			return err
		}
		w.Git.OriginURL = "https://github.com/example-user/demo.git"
		if transport == "ssh" {
			w.Git.OriginURL = "git@github.com:example-user/demo.git"
		}
		w.Client.GetRepo = &provider.Repository{CloneURL: "https://github.com/example-user/demo.git", SSHURL: "git@github.com:example-user/demo.git"}
		return nil
	}

	ctx.Step(`^selected-provider metadata resolves "([^"]*)" to a clean authoritative URL on its configured authority without userinfo, query, or fragment$`, func(project string) error {
		if err := configure("https"); err != nil {
			return err
		}
		w.Client.GetRepo = &provider.Repository{CloneURL: "https://github.com/example-user/" + project + ".git", SSHURL: "git@github.com:example-user/" + project + ".git"}
		return nil
	})
	ctx.Step(`^HTTPS credentials resolve through the repository-local Colt credential helper without URL persistence$`, func() error {
		if w.Git.Helpers != 1 || strings.Contains(w.Git.Origins[0], "bdd-fake-secret") {
			return fmt.Errorf("unsafe HTTPS clone state: helpers=%d origins=%v", w.Git.Helpers, w.Git.Origins)
		}
		return nil
	})
	ctx.Step(`^the selected identity is applied only to the local repository$`, func() error {
		if !slices.Contains(w.Git.Operations, "identity") {
			return fmt.Errorf("local identity was not applied: %v", w.Git.Operations)
		}
		after, err := fixture.GitGlobalSnapshot()
		if err != nil || after != w.GlobalGit {
			return errors.New("global Git configuration changed")
		}
		return nil
	})

	ctx.Step(`^repository resolution is ambiguous or its origin is not an authoritative HTTPS or SSH URL on the selected configured authority$`, func() error {
		if err := configure("https"); err != nil {
			return err
		}
		w.Client.GetRepo = &provider.Repository{CloneURL: "https://user@evil.example/api.git?token=secret#fragment"}
		return nil
	})
	ctx.Step(`^the origin has userinfo, query, fragment, or an authority-changing redirect$`, func() error {
		u, err := url.Parse(w.Client.GetRepo.CloneURL)
		if err != nil || u.User == nil || u.RawQuery == "" || u.Fragment == "" || !strings.EqualFold(u.Hostname(), "evil.example") {
			return fmt.Errorf("unsafe clone URL fixture is incomplete: %q", w.Client.GetRepo.CloneURL)
		}
		return nil
	})
	ctx.Step(`^the command fails closed before Git invocation, credential exposure, or outside-root access or mutation$`, func() error {
		if w.RunErr == nil || slices.Contains(w.Git.Operations, "clone") || w.Leaked(w.Out+fmt.Sprint(w.RunErr)) != "" {
			return fmt.Errorf("unsafe clone result: error=%v operations=%v output=%q", w.RunErr, w.Git.Operations, w.Out)
		}
		return nil
	})
	ctx.Step(`^the clone destination is a symlink to an outside sentinel$`, func() error {
		moved = ""
		if err := configure("https"); err != nil {
			return err
		}
		w.Client.GetRepo = &provider.Repository{CloneURL: "https://github.com/example-user/api.git"}
		var err error
		outside, err = os.MkdirTemp("", "colt-bdd-outside-")
		if err != nil {
			return err
		}
		sentinel = filepath.Join(outside, "sentinel")
		if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
			return err
		}
		return os.Symlink(outside, filepath.Join(w.Dir, "api"))
	})
	ctx.Step(`^the work root is renamed and replaced by an escaping symlink while Git clones to private staging$`, func() error {
		if err := configure("https"); err != nil {
			return err
		}
		w.Client.GetRepo = &provider.Repository{CloneURL: "https://github.com/example-user/api.git"}
		var err error
		outside, err = os.MkdirTemp("", "colt-bdd-outside-")
		if err != nil {
			return err
		}
		sentinel = filepath.Join(outside, "sentinel")
		if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
			return err
		}
		moved = w.Dir + "-moved"
		w.Git.CloneHook = func(string) error {
			if err := os.Rename(w.Dir, moved); err != nil {
				return err
			}
			return os.Symlink(outside, w.Dir)
		}
		return nil
	})
	ctx.Step(`^the completed clone is installed beneath the opened work root$`, func() error {
		defer func() {
			_ = os.Remove(w.Dir)
			_ = os.Rename(moved, w.Dir)
		}()
		if w.RunErr != nil {
			return w.RunErr
		}
		if _, err := os.Stat(filepath.Join(moved, "api", ".git")); err != nil {
			return fmt.Errorf("confined clone missing: %w", err)
		}
		if _, err := os.Lstat(filepath.Join(outside, "api")); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clone escaped opened root: %v", err)
		}
		return nil
	})
	ctx.Step(`^the outside sentinel is unchanged$`, func() error {
		defer os.RemoveAll(outside)
		data, err := os.ReadFile(sentinel)
		if err != nil || string(data) != "unchanged" {
			return fmt.Errorf("outside sentinel changed: %q, %v", data, err)
		}
		return nil
	})
	ctx.Step(`^the repository, version, and tag pass preflight$`, func() error { return prepareRelease("https") })
	ctx.Step(`^provider metadata supplies a clean authoritative push URL matching the current repository identity, origin, provider, repository, and authority after URL rewriting$`, func() error {
		p := w.Providers["personal"]
		if p.Host != "github.com" || p.Namespace != "example-user" || w.Git.OriginURL != w.Client.GetRepo.CloneURL {
			return fmt.Errorf("release metadata mismatch: provider=%+v origin=%q repo=%+v", p, w.Git.OriginURL, w.Client.GetRepo)
		}
		return nil
	})
	ctx.Step(`^Colt creates the local tag, pushes it over the configured transport to the validated explicit URL, and creates the provider release using provider API authentication in order$`, func() error {
		want := []string{"validate-tag:1.2.3", "tag:1.2.3", "push-tag:1.2.3"}
		position := 0
		for _, op := range w.Git.Operations {
			if position < len(want) && op == want[position] {
				position++
			}
		}
		if w.RunErr != nil || position != len(want) || !w.Client.Called("Release:1.2.3:1.2.3") {
			return fmt.Errorf("release order invalid: error=%v git=%v provider=%v", w.RunErr, w.Git.Operations, w.Client.Calls)
		}
		return nil
	})
	ctx.Step(`^the persistent remote URL stays credential-free$`, func() error {
		if strings.Contains(w.Git.OriginURL, "@github.com") || w.Leaked(w.Git.OriginURL+w.Git.PushURL) != "" {
			return fmt.Errorf("credential persisted in transport URL")
		}
		return nil
	})
	ctx.Step(`^the configured Git transport is SSH or HTTPS$`, func() error { return prepareRelease("ssh") })
	ctx.Step(`^a remote name or nominal fetch URL appears to match the selected repository$`, func() error { return prepareRelease("https") })
	ctx.Step(`^pushurl or URL rewriting selects a different authority or repository$`, func() error {
		w.Client.GetRepo = &provider.Repository{CloneURL: "https://evil.example/other/demo.git"}
		return nil
	})
	ctx.Step(`^the command fails before creating or changing a tag, executing local controls, or exposing credentials$`, func() error {
		if w.RunErr == nil || slices.ContainsFunc(w.Git.Operations, func(op string) bool { return strings.HasPrefix(op, "tag:") || strings.HasPrefix(op, "push-tag:") }) {
			return fmt.Errorf("unsafe release result: error=%v operations=%v", w.RunErr, w.Git.Operations)
		}
		return nil
	})

	ctx.Step(`^an explicit transport preference selects SSH$`, func() error {
		w.ExplicitTransport = "ssh"
		w.Git.Real = false
		w.Client.GetErr = provider.ErrNotFound
		w.Client.CreateRepo = &provider.Repository{CloneURL: "https://github.com/example-user/demo.git", SSHURL: "git@github.com:example-user/demo.git"}
		return nil
	})
	ctx.Step(`^the target is the authoritative HTTPS URL for the selected provider repository$`, func() error {
		if w.SelectedTransport != "https" || w.Client.CreateRepo == nil || w.Client.CreateRepo.CloneURL != "https://github.com/example-user/demo.git" {
			return fmt.Errorf("selected transport = %q", w.SelectedTransport)
		}
		return nil
	})
	ctx.Step(`^the target is the authoritative SSH URL for the selected provider repository$`, func() error {
		if w.SelectedTransport != "ssh" || w.Client.CreateRepo == nil || w.Client.CreateRepo.SSHURL != "git@github.com:example-user/demo.git" {
			return fmt.Errorf("selected transport = %q", w.SelectedTransport)
		}
		return nil
	})
	ctx.Step(`^the requested transport is unsupported for the selected provider$`, func() error {
		if err := configure("https"); err != nil {
			return err
		}
		w.ExplicitTransport = "ftp"
		return nil
	})
	ctx.Step(`^the command fails before repository mutation$`, func() error {
		if w.RunErr == nil || len(w.Git.Operations) != 0 {
			return fmt.Errorf("error=%v operations=%v", w.RunErr, w.Git.Operations)
		}
		return nil
	})

	ctx.Step(`^a repository was cloned with HTTPS transport and the Colt credential helper$`, func() error {
		if err := configure("https"); err != nil {
			return err
		}
		dir := filepath.Join(w.Dir, "demo")
		if err := os.Mkdir(dir, 0o700); err != nil {
			return err
		}
		if err := (gitnative.Native{}).Init(context.Background(), dir); err != nil {
			return err
		}
		return (gitnative.Native{}).ConfigureCredentialHelper(context.Background(), dir, "https://github.com/example-user/demo.git", "", "personal", "demo")
	})
	ctx.Step(`^I run ordinary "git fetch" without invoking Colt$`, func() error {
		binary := filepath.Join(w.Dir, "colt")
		build := exec.Command("go", "build", "-o", binary, "../../cmd/colt")
		if output, err := build.CombinedOutput(); err != nil {
			return fmt.Errorf("build helper: %v: %s", err, output)
		}
		cmd := exec.Command("git", "credential", "fill")
		cmd.Dir = filepath.Join(w.Dir, "demo")
		cmd.Env = append(os.Environ(), "COLT_CONFIG="+w.ConfigPath, "PATH="+w.Dir+string(os.PathListSeparator)+os.Getenv("PATH"), "GIT_TERMINAL_PROMPT=0")
		cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\npath=example-user/demo.git\n\n")
		output, err := cmd.CombinedOutput()
		w.Out, w.RunErr = string(output), err
		return nil
	})
	ctx.Step(`^authentication resolves through the Colt credential helper without re-entering credentials$`, func() error {
		if w.RunErr != nil || !strings.Contains(w.Out, "password=bdd-fake-secret") {
			return fmt.Errorf("credential helper failed: %v: %q", w.RunErr, w.Out)
		}
		return nil
	})

	ctx.Step(`^the local release tag was created and pushed over the configured Git transport$`, func() error { return prepareRelease("https") })
	ctx.Step(`^provider release creation fails$`, func() error {
		w.Client.ReleaseErr = errors.New("provider release failed")
		w.Run("colt release 1.2.3")
		return nil
	})
	ctx.Step(`^Colt does not force, overwrite, or roll back the tag or release$`, func() error {
		if !slices.Contains(w.Git.Operations, "push-tag:1.2.3") || w.RunErr == nil {
			return fmt.Errorf("partial release state missing: %v %v", w.RunErr, w.Git.Operations)
		}
		return nil
	})

	// LIFECYCLE-SECURITY-001: hostile inherited/global/system Git controls must
	// have no effect on clone. The exact argv/env isolation is proven in the
	// internal/git shell-fake tests; here the command must still complete against
	// a hostile environment and produce the authoritative, credential-free clone
	// with exactly the Colt helper configured.
	ctx.Step(`^inherited, global, and system Git controls are hostile$`, func() error {
		if err := configure("https"); err != nil {
			return err
		}
		w.Client.GetRepo = &provider.Repository{CloneURL: "https://github.com/example-user/api.git", SSHURL: "git@github.com:example-user/api.git"}
		for name, value := range map[string]string{
			"GIT_CONFIG_COUNT":   "1",
			"GIT_CONFIG_KEY_0":   "core.sshCommand",
			"GIT_CONFIG_VALUE_0": "/bin/evil-ssh",
			"GIT_SSH_COMMAND":    "/bin/evil-ssh",
			"GIT_ASKPASS":        "/bin/evil-askpass",
			"GIT_CONFIG_GLOBAL":  filepath.Join(w.Dir, "hostile-global.gitconfig"),
			"GIT_CONFIG_SYSTEM":  filepath.Join(w.Dir, "hostile-system.gitconfig"),
		} {
			if err := os.Setenv(name, value); err != nil {
				return err
			}
		}
		global := "[core]\n\tsshCommand = !/bin/evil-ssh\n[credential]\n\thelper = !/bin/evil-helper\n[user]\n\tname = Hostile\n\temail = hostile@example.invalid\n"
		if err := os.WriteFile(os.Getenv("GIT_CONFIG_GLOBAL"), []byte(global), 0o600); err != nil {
			return err
		}
		system := "[core]\n\tsshCommand = !/bin/evil-ssh\n"
		return os.WriteFile(os.Getenv("GIT_CONFIG_SYSTEM"), []byte(system), 0o600)
	})
	ctx.Step(`^native Git ignores inherited GIT controls and global and system configuration$`, func() error {
		if w.RunErr != nil {
			return fmt.Errorf("clone failed under hostile environment: %v", w.RunErr)
		}
		if !slices.Contains(w.Git.Operations, "clone") {
			return fmt.Errorf("clone not recorded: %v", w.Git.Operations)
		}
		if w.Git.Helpers != 1 {
			return fmt.Errorf("expected exactly the Colt credential helper, got %d", w.Git.Helpers)
		}
		if len(w.Git.Origins) == 0 || !strings.HasPrefix(w.Git.Origins[len(w.Git.Origins)-1], "https://github.com/") {
			return fmt.Errorf("clone origin not authoritative and credential-free: %v", w.Git.Origins)
		}
		return nil
	})

	// LIFECYCLE-CLONE-004: SSH clone delegates to the user's SSH environment and
	// must not configure a credential helper.
	ctx.Step(`^selected-provider metadata resolves "([^"]*)" to a clean authoritative SSH URL$`, func(project string) error {
		if err := configure("ssh"); err != nil {
			return err
		}
		w.Client.GetRepo = &provider.Repository{CloneURL: "https://github.com/example-user/" + project + ".git", SSHURL: "git@github.com:example-user/" + project + ".git"}
		return nil
	})
	ctx.Step(`^SSH clone uses the existing user SSH environment without Colt reading private keys$`, func() error {
		if w.RunErr != nil {
			return fmt.Errorf("ssh clone failed: %v", w.RunErr)
		}
		if w.Git.Helpers != 0 {
			return fmt.Errorf("SSH clone configured a credential helper: %d", w.Git.Helpers)
		}
		if len(w.Git.Origins) == 0 || !strings.HasPrefix(w.Git.Origins[len(w.Git.Origins)-1], "git@") {
			return fmt.Errorf("SSH clone origin not recorded as SSH: %v", w.Git.Origins)
		}
		return nil
	})

	// LIFECYCLE-SECURITY-001/002/003: hostile repository-local configuration plus
	// a pre-push hook must make `colt release` fail closed before any mutation.
	ctx.Step(`^repository-local configuration requests malicious execution, transport, proxy, credential-helper, or pre-push hook behavior$`, func() error {
		if err := prepareRelease("https"); err != nil {
			return err
		}
		native := gitnative.Native{}
		if err := native.Init(context.Background(), w.Dir); err != nil {
			return err
		}
		if err := native.SetIdentity(context.Background(), w.Dir, "Test", "test@example.invalid"); err != nil {
			return err
		}
		if _, err := native.Commit(context.Background(), w.Dir); err != nil {
			return err
		}
		hostileMarker = filepath.Join(w.Dir, "hostile-executed")
		for _, kv := range [][2]string{
			{"core.sshCommand", "touch " + hostileMarker},
			{"core.hooksPath", filepath.Join(w.Dir, ".git", "hooks")},
			{"url.evil.example.insteadOf", "https://github.com/"},
			{"http.proxy", "http://127.0.0.1:1"},
			{"gpg.program", "touch " + hostileMarker},
			{"credential.helper", "!touch " + hostileMarker},
			{"protocol.ext.allow", "always"},
		} {
			cmd := exec.Command("git", "config", "--local", kv[0], kv[1])
			cmd.Dir = w.Dir
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("git config --local %s: %v: %s", kv[0], err, out)
			}
		}
		hook := filepath.Join(w.Dir, ".git", "hooks", "pre-push")
		if err := os.MkdirAll(filepath.Dir(hook), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch '"+hostileMarker+"'\nexit 1\n"), 0o700); err != nil {
			return err
		}
		return nil
	})
	ctx.Step(`^release Git ignores inherited, global, system, and unsafe repository-local controls and disables hooks including pre-push$`, func() error {
		if w.RunErr == nil {
			return errors.New("hostile release unexpectedly succeeded")
		}
		if !strings.Contains(w.RunErr.Error(), "unsafe") {
			return fmt.Errorf("release rejection did not name unsafe config: %v", w.RunErr)
		}
		if _, err := os.Stat(hostileMarker); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("hostile repository-local control or pre-push hook executed: %v", err)
		}
		return nil
	})
}
