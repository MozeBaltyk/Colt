package steps

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
	"github.com/cucumber/godog"
)

func RegisterWorkspaceSteps(ctx *godog.ScenarioContext, w *fixture.World) {
	var statusPlan, outside, moved, sentinel, preservedOrigin string
	var target *fixture.FakeClient
	var mirrorCount int
	var mirrorFailure, targetExists bool

	repository := func(p config.Provider, name string) provider.Repository {
		return provider.Repository{Name: name, Namespace: p.Namespace, CloneURL: "https://" + p.Host + "/" + p.Namespace + "/" + name + ".git", SSHURL: "git@" + p.Host + ":" + p.Namespace + "/" + name + ".git"}
	}
	github := func(namespace string) config.Provider {
		return fixture.WithTokenEnv(fixture.StdProvider("github", namespace), fixture.TokenEnv)
	}
	gitlab := func(namespace string) config.Provider {
		return fixture.WithTokenEnv(fixture.StdProvider("gitlab", namespace), fixture.TokenEnv)
	}
	saveWorkspace := func(providers map[string]config.Provider, selections ...config.RepositorySelection) error {
		w.Providers = providers
		return config.Save(w.ConfigPath, config.Config{Providers: providers, Workspace: &config.Workspace{Repositories: selections}})
	}
	writeRepo := func(relative, origin string) error {
		dir := filepath.Join(w.Dir, relative, ".git")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "config"), []byte("[remote \"origin\"]\n\turl = "+origin+"\n"), 0o600)
	}
	configureReconciliation := func() error {
		p := github("team")
		include := []string{"existing", "good", "bad", "mismatch", "absent"}
		if err := saveWorkspace(map[string]config.Provider{"work": p}, config.RepositorySelection{Provider: "work", Namespace: "team", Include: &include}); err != nil {
			return err
		}
		w.Client.ListRepos = []provider.Repository{repository(p, "existing"), repository(p, "good"), repository(p, "bad"), repository(p, "mismatch")}
		w.Git.Real = false
		w.Git.CloneErrors = map[string]error{"bad": errors.New("injected clone failure")}
		if err := writeRepo("team/existing", repository(p, "existing").CloneURL); err != nil {
			return err
		}
		return os.MkdirAll(filepath.Join(w.Dir, "team", "mismatch"), 0o755)
	}
	configureMirror := func(sourceType, sourceAlias, targetAlias string) error {
		source := github("source")
		if sourceType == "gitlab" {
			source = gitlab("source")
		}
		local := config.Provider{Type: "gitea", Host: "code.example", BaseURL: "https://code.example", Namespace: "target", Visibility: "private", GitName: "Test", GitEmail: "test@example.invalid", Auth: config.Auth{Source: "env", TokenEnv: fixture.TokenEnv}}
		if err := saveWorkspace(map[string]config.Provider{sourceAlias: source, targetAlias: local}); err != nil {
			return err
		}
		w.Client.ListRepos = nil
		for i := 1; i <= mirrorCount; i++ {
			name := fmt.Sprintf("repo%d", i)
			w.Client.ListRepos = append(w.Client.ListRepos, repository(source, name))
		}
		target = &fixture.FakeClient{GetRepos: map[string]*provider.Repository{}, GetErrors: map[string]error{}}
		if targetExists {
			r := repository(local, "demo")
			target.GetRepos["demo"] = &r
			w.Client.ListRepos = []provider.Repository{repository(source, "demo")}
		} else {
			for _, r := range w.Client.ListRepos {
				target.GetErrors[r.Name] = provider.ErrNotFound
			}
		}
		target.CreateFunc = func(project string) (*provider.Repository, error) {
			r := repository(local, project)
			return &r, nil
		}
		w.NewClient = func(p config.Provider, _ string) (provider.Client, error) {
			if p.Type == "gitea" {
				return target, nil
			}
			return w.Client, nil
		}
		w.Git.Real = false
		w.Git.MirrorCloneErr = map[string]error{}
		if mirrorFailure {
			w.Git.MirrorCloneErr["repo1"] = errors.New("injected mirror failure")
		}
		return nil
	}

	ctx.Step(`^a strict data-only YAML document has a top-level workspace mapping with repository selections$`, func() error {
		work, personal := github("team"), gitlab("group")
		include := []string{"web", "api"}
		if err := saveWorkspace(map[string]config.Provider{"work": work, "personal": personal},
			config.RepositorySelection{Provider: "work", Namespace: "team", Include: &include},
			config.RepositorySelection{Provider: "personal", Namespace: "group"}); err != nil {
			return err
		}
		clients := map[string]*fixture.FakeClient{
			"github": {ListRepos: []provider.Repository{repository(work, "api"), repository(work, "web"), repository(work, "ignored")}},
			"gitlab": {ListRepos: []provider.Repository{repository(personal, "zeta"), repository(personal, "alpha")}},
		}
		w.NewClient = func(p config.Provider, _ string) (provider.Client, error) { return clients[p.Type], nil }
		return nil
	})
	ctx.Step(`^each repository selection names a provider alias and namespace$`, func() error {
		cfg, err := config.Load(w.ConfigPath)
		if err != nil || cfg.Workspace == nil || len(cfg.Workspace.Repositories) != 2 {
			return fmt.Errorf("workspace selections unavailable: %v", err)
		}
		return nil
	})
	ctx.Step(`^one selection includes named repositories while another omits include$`, func() error {
		cfg, err := config.Load(w.ConfigPath)
		if err != nil || cfg.Workspace.Repositories[0].Include == nil || cfg.Workspace.Repositories[1].Include != nil {
			return errors.New("include omission was not represented distinctly")
		}
		return nil
	})
	ctx.Step(`^Colt resolves the desired workspace$`, func() error { w.Run("colt status"); return nil })
	ctx.Step(`^the first selection contains only its include list$`, func() error {
		if strings.Contains(w.Out, "ignored") || !strings.Contains(w.Out, "team/api") || !strings.Contains(w.Out, "team/web") {
			return fmt.Errorf("included selection output: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^the second contains all visible repositories in its selection$`, func() error {
		if !strings.Contains(w.Out, "group/alpha") || !strings.Contains(w.Out, "group/zeta") {
			return fmt.Errorf("omitted include output: %q", w.Out)
		}
		return nil
	})
	ctx.Step(`^every desired repository identity and local path is unique and deterministic$`, func() error {
		lines := strings.Split(strings.TrimSpace(w.Out), "\n")
		if !slices.IsSorted(lines) {
			return fmt.Errorf("workspace output is not deterministic: %v", lines)
		}
		seen := map[string]bool{}
		for _, line := range lines {
			if seen[line] {
				return fmt.Errorf("duplicate workspace result %q", line)
			}
			seen[line] = true
		}
		return nil
	})

	ctx.Step(`^workspace YAML has a duplicate mapping key$`, func() error {
		raw := "providers: {}\nworkspace:\n  repositories:\n    - provider: missing\n      provider: duplicate\n      namespace: team\n"
		return os.WriteFile(w.ConfigPath, []byte(raw), 0o600)
	})
	ctx.Step(`^valid workspace YAML includes a traversal repository name$`, func() error {
		p := github("team")
		w.Providers = map[string]config.Provider{"work": p}
		if err := config.Save(w.ConfigPath, config.Config{Providers: w.Providers}); err != nil {
			return err
		}
		f, err := os.OpenFile(w.ConfigPath, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString("workspace:\n  repositories:\n    - provider: work\n      namespace: team\n      include: [../outside]\n")
		return errors.Join(writeErr, f.Close())
	})
	ctx.Step(`^Colt parses the manifest$`, func() error { w.Run("colt status"); return nil })
	ctx.Step(`^the manifest fails before provider resolution or local access$`, func() error {
		if w.RunErr == nil || len(w.NewClientCalls) != 0 || len(w.Client.Calls) != 0 || len(w.Git.Operations) != 0 {
			return fmt.Errorf("manifest did not fail before access: %v provider=%v git=%v", w.RunErr, w.Client.Calls, w.Git.Operations)
		}
		return nil
	})

	ctx.Step(`^desired repositories are present, missing locally, absent remotely, and mismatched$`, configureReconciliation)
	ctx.Step(`^deterministic results categorize them as present, to-clone, absent remotely, and remote/config mismatch$`, func() error {
		for _, want := range []string{"present team/existing", "to-clone team/good", "absent-remotely team/absent", "mismatch team/mismatch"} {
			if !strings.Contains(w.Out, want) {
				return fmt.Errorf("status %q lacks %q", w.Out, want)
			}
		}
		return nil
	})
	ctx.Step(`^no local, remote, or configuration state is changed$`, func() error {
		after, err := os.ReadFile(w.ConfigPath)
		if err != nil || string(after) != string(w.ConfigBefore) || slices.Contains(w.Git.Operations, "clone") {
			return fmt.Errorf("read-only command mutated state: %v git=%v", err, w.Git.Operations)
		}
		return nil
	})
	ctx.Step(`^desired repositories include an existing repository, two missing repositories, an inconsistent repository, and a remotely absent repository$`, configureReconciliation)
	ctx.Step(`^one missing repository cannot be cloned$`, func() error {
		if w.Git.CloneErrors["bad"] == nil {
			return errors.New("clone-failure fixture is missing")
		}
		return nil
	})
	ctx.Step(`^the other missing repository is cloned$`, func() error {
		if _, err := os.Stat(filepath.Join(w.Dir, "team", "good", ".git")); err != nil {
			return err
		}
		return nil
	})
	ctx.Step(`^existing, inconsistent, remotely absent, and failed repositories are summarized$`, func() error {
		for _, want := range []string{"present team/existing", "mismatch team/mismatch", "absent-remotely team/absent", "failed team/bad"} {
			if !strings.Contains(w.Out, want) {
				return fmt.Errorf("sync output %q lacks %q", w.Out, want)
			}
		}
		return nil
	})
	ctx.Step(`^completed clones are retained$`, func() error {
		_, err := os.Stat(filepath.Join(w.Dir, "team", "good"))
		return err
	})
	ctx.Step(`^reconciliation requires remote metadata reads and a missing repository clone$`, func() error {
		p := github("team")
		include := []string{"demo"}
		if err := saveWorkspace(map[string]config.Provider{"work": p}, config.RepositorySelection{Provider: "work", Namespace: "team", Include: &include}); err != nil {
			return err
		}
		w.Client.ListRepos = []provider.Repository{repository(p, "demo")}
		w.Run("colt status")
		statusPlan = w.Out
		w.Git.Operations = nil
		return nil
	})
	ctx.Step(`^Colt reports the same plan as `+"`"+`colt sync`+"`"+`$`, func() error {
		if w.Out != statusPlan {
			return fmt.Errorf("dry-run plan %q differs from status %q", w.Out, statusPlan)
		}
		return nil
	})
	ctx.Step(`^remote metadata may be read$`, func() error {
		if !w.Client.Called("List") {
			return errors.New("remote metadata was not read")
		}
		return nil
	})

	ctx.Step(`^a derived path initially validates beneath the workspace root$`, func() error {
		p := github("team")
		include := []string{"demo"}
		if err := saveWorkspace(map[string]config.Provider{"work": p}, config.RepositorySelection{Provider: "work", Namespace: "team", Include: &include}); err != nil {
			return err
		}
		w.Client.ListRepos = []provider.Repository{repository(p, "demo")}
		return nil
	})
	ctx.Step(`^the workspace root is swapped to an escaping symlink before root-relative access$`, func() error {
		var err error
		outside, err = os.MkdirTemp("", "colt-workspace-outside-")
		if err != nil {
			return err
		}
		sentinel = filepath.Join(outside, "sentinel")
		if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
			return err
		}
		moved = w.Dir + "-moved"
		w.Client.ListHook = func() error {
			if err := os.Rename(w.Dir, moved); err != nil {
				return err
			}
			return os.Symlink(outside, w.Dir)
		}
		return nil
	})
	ctx.Step(`^the workspace has an unrelated non-empty path and a dirty or diverged repository with a mismatched origin$`, func() error {
		if err := os.MkdirAll(filepath.Join(w.Dir, "unrelated"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(w.Dir, "unrelated", "keep"), []byte("keep"), 0o600); err != nil {
			return err
		}
		preservedOrigin = filepath.Join(w.Dir, "team", "dirty", ".git", "config")
		if err := os.MkdirAll(filepath.Dir(preservedOrigin), 0o755); err != nil {
			return err
		}
		return os.WriteFile(preservedOrigin, []byte("[remote \"origin\"]\n\turl = https://wrong.example/demo.git\n"), 0o600)
	})
	ctx.Step(`^root-relative no-follow access fails closed without outside-root access or mutation$`, func() error {
		data, err := os.ReadFile(sentinel)
		if w.RunErr == nil || err != nil || string(data) != "unchanged" {
			return fmt.Errorf("swap did not fail closed: %v sentinel=%q/%v", w.RunErr, data, err)
		}
		return nil
	})
	ctx.Step(`^Colt does not overwrite, prune, reset, or delete existing paths$`, func() error {
		if slices.Contains(w.Git.Operations, "clone") {
			return fmt.Errorf("unsafe Git operation: %v", w.Git.Operations)
		}
		return nil
	})
	ctx.Step(`^Colt does not rewrite the repository origin$`, func() error {
		data, err := os.ReadFile(strings.Replace(preservedOrigin, w.Dir, moved, 1))
		if err != nil || !strings.Contains(string(data), "wrong.example") {
			return fmt.Errorf("repository origin changed: %q, %v", data, err)
		}
		return nil
	})
	ctx.Step(`^each inconsistency is reported$`, func() error {
		defer os.RemoveAll(outside)
		if err := os.Remove(w.Dir); err == nil {
			_ = os.Rename(moved, w.Dir)
		}
		if w.RunErr == nil {
			return errors.New("unsafe workspace was not reported")
		}
		return nil
	})

	ctx.Step(`^a configured fake local Gitea or Forgejo target$`, func() error { mirrorCount = 2; return nil })
	ctx.Step(`^the source provider namespace has three repositories$`, func() error { mirrorCount = 3; return nil })
	ctx.Step(`^all three source repositories are listed$`, func() error {
		if !w.Client.Called("List") || len(w.Client.ListRepos) != 3 {
			return fmt.Errorf("source list calls=%v repositories=%d", w.Client.Calls, len(w.Client.ListRepos))
		}
		return nil
	})
	ctx.Step(`^each repository is cloned into a temporary directory$`, func() error {
		if len(w.Git.MirrorDirs) != mirrorCount {
			return fmt.Errorf("mirror clone directories=%v", w.Git.MirrorDirs)
		}
		return nil
	})
	ctx.Step(`^the repository is created on the target provider$`, func() error {
		if target == nil || !target.Called("Create") {
			return fmt.Errorf("target calls=%v", target.Calls)
		}
		return nil
	})
	ctx.Step(`^a mirror push of all branches and tags is requested for the target$`, func() error {
		if w.Git.Pushes != mirrorCount {
			return fmt.Errorf("mirror pushes=%d operations=%v", w.Git.Pushes, w.Git.Operations)
		}
		return nil
	})
	ctx.Step(`^the temporary directories are removed$`, func() error {
		for _, dir := range w.Git.MirrorDirs {
			if _, err := os.Stat(filepath.Dir(dir)); !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("temporary directory retained: %s", dir)
			}
		}
		return nil
	})
	ctx.Step(`^the source provider namespace has two repositories$`, func() error { mirrorCount = 2; return nil })
	ctx.Step(`^one repository cannot be cloned$`, func() error { mirrorFailure = true; return nil })
	ctx.Step(`^the other repository is mirrored successfully$`, func() error {
		if w.Git.Pushes != 1 {
			return fmt.Errorf("independent mirror did not complete: %v", w.Git.Operations)
		}
		return nil
	})
	ctx.Step(`^the failed repository is reported$`, func() error {
		if !strings.Contains(w.Out, "failed repo1") || w.RunErr == nil {
			return fmt.Errorf("mirror failure not summarized: %q %v", w.Out, w.RunErr)
		}
		return nil
	})
	ctx.Step(`^completed mirrors are retained$`, func() error {
		if w.Git.Pushes == 0 {
			return errors.New("no completed mirror")
		}
		return nil
	})
	ctx.Step(`^the target provider already has repository "demo"$`, func() error { targetExists, mirrorCount = true, 1; return nil })
	ctx.Step(`^the command fails without overwriting the existing target$`, func() error {
		if w.RunErr == nil || w.Git.Pushes != 0 {
			return fmt.Errorf("target conflict unsafe: %v pushes=%d", w.RunErr, w.Git.Pushes)
		}
		return nil
	})
	ctx.Step(`^the existing target repository is unchanged$`, func() error {
		if target == nil {
			return errors.New("target fixture was not configured")
		}
		if target.Called("Create") || w.Git.Pushes != 0 {
			return fmt.Errorf("existing target changed: calls=%v pushes=%d", target.Calls, w.Git.Pushes)
		}
		return nil
	})
	ctx.Step(`^the target repository is replaced$`, func() error {
		if w.RunErr != nil || len(w.Git.MirrorForce) != 1 || !w.Git.MirrorForce[0] {
			return fmt.Errorf("replace was not explicit: %v force=%v", w.RunErr, w.Git.MirrorForce)
		}
		return nil
	})
	ctx.Step(`^unrelated state is not silently overwritten$`, func() error {
		if target.Called("Create") {
			return errors.New("replace recreated provider repository")
		}
		return nil
	})
	ctx.Step(`^the source provider has repository "demo"$`, func() error { mirrorCount = 1; return nil })
	ctx.Step(`^the mirror completes successfully$`, func() error { return w.RunErr })
	ctx.Step(`^the command performs no background propagation after it returns$`, func() error {
		if w.Git.Pushes != 1 {
			return fmt.Errorf("unexpected continuous pushes: %d", w.Git.Pushes)
		}
		return nil
	})
	ctx.Step(`^another explicit `+"`"+`colt mirror`+"`"+` invocation is required$`, func() error {
		if w.Git.Pushes != 1 {
			return fmt.Errorf("mirror was not one-shot: pushes=%d", w.Git.Pushes)
		}
		return nil
	})
	ctx.Step(`^the source provider namespace has repository "demo"$`, func() error { mirrorCount = 1; return nil })
	ctx.Step(`^the target remote URL contains no credentials$`, func() error {
		if strings.Contains(w.Git.PushURL, "@code.example") || w.Leaked(w.Git.PushURL) != "" {
			return fmt.Errorf("credential-bearing target URL %q", w.Git.PushURL)
		}
		return nil
	})
	ctx.Step(`^the target provider URL is used$`, func() error {
		if !strings.HasPrefix(w.Git.PushURL, "https://code.example/target/") {
			return fmt.Errorf("unexpected target URL %q", w.Git.PushURL)
		}
		return nil
	})
	ctx.Step(`^the source provider URL is not retained as the target push remote$`, func() error {
		if strings.Contains(w.Git.PushURL, "github.com") || strings.Contains(w.Git.PushURL, "gitlab.com") {
			return fmt.Errorf("source URL retained: %q", w.Git.PushURL)
		}
		return nil
	})

	ctx.Step(`^I execute `+"`"+`(colt (?:status|sync|sync --dry-run))`+"`"+`$`, func(command string) error { w.Run(command); return nil })
	ctx.Step(`^I execute `+"`"+`colt mirror ([^ ]+) ([^ ]+) --namespace ([^ ]+)`+"`"+`$`, func(sourceAlias, targetAlias, namespace string) error {
		if err := configureMirror(sourceAlias, sourceAlias, targetAlias); err != nil {
			return err
		}
		w.Run("colt mirror " + sourceAlias + " " + targetAlias + " --namespace " + namespace)
		return nil
	})
	ctx.Step(`^I execute `+"`"+`colt mirror ([^ ]+) ([^ ]+)`+"`"+`$`, func(sourceAlias, targetAlias string) error {
		if err := configureMirror(sourceAlias, sourceAlias, targetAlias); err != nil {
			return err
		}
		w.Run("colt mirror " + sourceAlias + " " + targetAlias)
		return nil
	})
	ctx.Step(`^I execute `+"`"+`colt mirror ([^ ]+) ([^ ]+) --replace`+"`"+`$`, func(sourceAlias, targetAlias string) error {
		if err := configureMirror(sourceAlias, sourceAlias, targetAlias); err != nil {
			return err
		}
		w.Run("colt mirror " + sourceAlias + " " + targetAlias + " --replace")
		return nil
	})
}
