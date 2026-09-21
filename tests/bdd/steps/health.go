package steps

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/app"
	"github.com/MozeBaltyk/Colt/internal/config"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
	"github.com/cucumber/godog"
)

func RegisterHealthSteps(ctx *godog.ScenarioContext, w *fixture.World) {
	var expectedCode int
	var before []byte
	var beforeGitConfig, beforeGitIndex, beforeGitHead, beforeReadme []byte
	var beforeProvider string
	var sentinel string

	setup := func() error {
		p := fixture.WithTokenEnv(fixture.StdProvider("github", "team"), fixture.TokenEnv)
		policy := &config.Policy{Repository: config.RepositoryPolicy{Require: []string{"LICENSE", "README.md"}, DefaultBranch: "main", AllowedVisibility: []string{"private"}}}
		include := []string{"demo"}
		cfg := config.Config{Providers: map[string]config.Provider{"work": p}, Policy: policy, Workspace: &config.Workspace{Repositories: []config.RepositorySelection{{Provider: "work", Namespace: "team", Include: &include}}}}
		if err := config.Save(w.ConfigPath, cfg); err != nil {
			return err
		}
		remote := provider.Repository{Name: "demo", Namespace: "team", CloneURL: "https://github.com/team/demo.git", SSHURL: "git@github.com:team/demo.git", DefaultBranch: "main", Visibility: "private"}
		w.Client.GetRepo = &remote
		w.Client.ListRepos = []provider.Repository{remote}
		w.Git.Real = false
		w.Git.HealthState = gitnative.HealthState{Name: p.GitName, Email: p.GitEmail, Origin: remote.CloneURL, Clean: true}
		for _, dir := range []string{w.Dir, filepath.Join(w.Dir, "team", "demo")} {
			if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[remote \"origin\"]\nurl = "+remote.CloneURL+"\n"), 0o600); err != nil {
				return err
			}
			for _, name := range policy.Repository.Require {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0o600); err != nil {
					return err
				}
			}
		}
		w.BuildApp()
		return nil
	}

	ctx.Step(`^a healthy repository and managed workspace with project health policy$`, setup)
	ctx.Step(`^I run the health command `+"`"+`(colt check(?: --all)?)`+"`"+`$`, func(command string) error { w.Run(command); return nil })
	ctx.Step(`^every selected repository is checked through Git and the provider API$`, func() error {
		if !slices.Contains(w.Git.Operations, "health") || !w.Client.Called("Get") {
			return fmt.Errorf("operations=%v provider=%v", w.Git.Operations, w.Client.Calls)
		}
		return nil
	})
	ctx.Step(`^the health check has no findings and exit code 0$`, func() error {
		if w.Out != "" || app.ExitCode(w.RunErr) != 0 {
			return fmt.Errorf("output=%q error=%v code=%d", w.Out, w.RunErr, app.ExitCode(w.RunErr))
		}
		return nil
	})
	ctx.Step(`^health policy contains an unknown field$`, func() error {
		if err := os.WriteFile(w.ConfigPath, []byte("providers: {}\npolicy:\n  repository:\n    require: []\n    default_branch: main\n    allowed_visibility: [private]\n    execute: no\n"), 0o600); err != nil {
			return err
		}
		w.Git.Real = false
		return nil
	})
	ctx.Step(`^health configuration fails with exit code 2 before Git or provider access$`, func() error {
		if app.ExitCode(w.RunErr) != 2 || len(w.Git.Operations) != 0 || len(w.Client.Calls) != 0 {
			return fmt.Errorf("error=%v git=%v provider=%v", w.RunErr, w.Git.Operations, w.Client.Calls)
		}
		return nil
	})
	ctx.Step(`^a health check produces (no findings|drift findings|operational error)$`, func(result string) error {
		if err := setup(); err != nil {
			return err
		}
		switch result {
		case "no findings":
			expectedCode = 0
		case "drift findings":
			expectedCode = 1
			w.Git.HealthState.Name, w.Git.HealthState.Clean = "wrong", false
			if err := os.Remove(filepath.Join(w.Dir, "README.md")); err != nil {
				return err
			}
		case "operational error":
			expectedCode = 2
			w.Git.HealthErr = errors.New("raw-secret-child-output")
		}
		return nil
	})
	ctx.Step(`^health findings are sorted and the exit code is (\d+)$`, func(raw string) error {
		want, _ := strconv.Atoi(raw)
		lines := strings.Split(strings.TrimSpace(w.Out), "\n")
		if w.Out == "" {
			lines = nil
		}
		if want != expectedCode || app.ExitCode(w.RunErr) != want || !slices.IsSorted(lines) {
			return fmt.Errorf("output=%q error=%v code=%d want=%d", w.Out, w.RunErr, app.ExitCode(w.RunErr), want)
		}
		return nil
	})
	ctx.Step(`^a health repository has executable Git configuration and secret-bearing inputs$`, func() error {
		if err := setup(); err != nil {
			return err
		}
		w.Git.Real = true
		sentinel = filepath.Join(w.Dir, "executed")
		script := filepath.Join(w.Dir, "malicious")
		if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch \""+sentinel+"\"\n"), 0o700); err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(w.Dir, ".git")); err != nil {
			return err
		}
		cmd := exec.Command("git", "init", "--initial-branch=main", "--template=")
		cmd.Dir = w.Dir
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("initialize hostile repository: %v: %s", err, output)
		}
		for _, args := range [][]string{
			{"config", "user.name", "Health Test"},
			{"config", "user.email", "health@example.invalid"},
			{"add", "README.md", "LICENSE"},
			{"commit", "-m", "health fixture"},
		} {
			cmd = exec.Command("git", args...)
			cmd.Dir = w.Dir
			if output, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("prepare hostile repository: %v: %s", err, output)
			}
		}
		cmd = exec.Command("git", "config", "--local", "core.fsmonitor", script)
		cmd.Dir = w.Dir
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("configure hostile repository: %v: %s", err, output)
		}
		if err := os.Setenv(fixture.TokenEnv, "bdd-health-secret"); err != nil {
			return err
		}
		var err error
		before, err = os.ReadFile(w.ConfigPath)
		if err != nil {
			return err
		}
		beforeGitConfig, err = os.ReadFile(filepath.Join(w.Dir, ".git", "config"))
		if err != nil {
			return err
		}
		beforeGitIndex, err = os.ReadFile(filepath.Join(w.Dir, ".git", "index"))
		if err != nil {
			return err
		}
		beforeGitHead, err = os.ReadFile(filepath.Join(w.Dir, ".git", "HEAD"))
		if err != nil {
			return err
		}
		beforeReadme, err = os.ReadFile(filepath.Join(w.Dir, "README.md"))
		beforeProvider = fmt.Sprintf("%#v", w.Client.GetRepo)
		return err
	})
	ctx.Step(`^health inspection executes nothing, preserves repository, provider, and configuration state, and emits only bounded redacted findings$`, func() error {
		after, err := os.ReadFile(w.ConfigPath)
		afterGitConfig, gitErr := os.ReadFile(filepath.Join(w.Dir, ".git", "config"))
		afterGitIndex, indexErr := os.ReadFile(filepath.Join(w.Dir, ".git", "index"))
		afterGitHead, headErr := os.ReadFile(filepath.Join(w.Dir, ".git", "HEAD"))
		afterReadme, readmeErr := os.ReadFile(filepath.Join(w.Dir, "README.md"))
		_, executed := os.Stat(sentinel)
		if err != nil || gitErr != nil || indexErr != nil || headErr != nil || readmeErr != nil ||
			string(after) != string(before) || string(afterGitConfig) != string(beforeGitConfig) || string(afterGitIndex) != string(beforeGitIndex) || string(afterGitHead) != string(beforeGitHead) || string(afterReadme) != string(beforeReadme) || fmt.Sprintf("%#v", w.Client.GetRepo) != beforeProvider ||
			executed == nil || app.ExitCode(w.RunErr) != 2 || len(w.Out) > 1024 || strings.Contains(w.Out+w.RunErr.Error(), "bdd-health-secret") {
			return fmt.Errorf("unsafe result output=%q error=%v executed=%v", w.Out, w.RunErr, executed)
		}
		return nil
	})
}
