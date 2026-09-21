package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MozeBaltyk/Colt/internal/config"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
)

func healthFixture(t *testing.T) (*App, *fakeGit, *fakeClient) {
	t.Helper()
	t.Setenv("COLT_TEST_TOKEN", "test-secret-token")
	dir := t.TempDir()
	p := appProvider()
	p.Namespace = "team"
	p.Host = "gitlab.com"
	p.BaseURL = "https://gitlab.com"
	policy := &config.Policy{Repository: config.RepositoryPolicy{Require: []string{"LICENSE", "README.md"}, DefaultBranch: "main", AllowedVisibility: []string{"private"}}}
	path := filepath.Join(dir, "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": p}, Policy: policy}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range policy.Repository.Require {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("not inspected"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	remote := &provider.Repository{Name: "demo", Namespace: "team", CloneURL: "https://gitlab.com/team/demo.git", SSHURL: "git@gitlab.com:team/demo.git", DefaultBranch: "main", Visibility: "private"}
	g := &fakeGit{health: gitnative.HealthState{Name: p.GitName, Email: p.GitEmail, Origin: remote.CloneURL, Clean: true}}
	c := &fakeClient{found: remote}
	return testApp(path, dir, g, c), g, c
}

func TestCheckHealthyAndFindingsAreCodedAndSorted(t *testing.T) {
	a, g, _ := healthFixture(t)
	out, err := execute(t, a, "check")
	if err != nil || out != "" || ExitCode(err) != 0 {
		t.Fatalf("healthy check output=%q error=%v code=%d", out, err, ExitCode(err))
	}
	g.health.Name, g.health.Email, g.health.Clean = "wrong", "wrong", false
	if err := os.Remove(filepath.Join(a.WorkDir, "README.md")); err != nil {
		t.Fatal(err)
	}
	out, err = execute(t, a, "check")
	want := ".: identity-email\n.: identity-name\n.: required-file-missing README.md\n.: worktree-dirty\n"
	if out != want || ExitCode(err) != 1 || !ErrorReported(err) {
		t.Fatalf("finding output=%q want=%q error=%v code=%d", out, want, err, ExitCode(err))
	}
}

func TestCheckConfigurationAndOperationalErrorsUseCodeTwo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("providers: {}\npolicy:\n  repository:\n    require: []\n    default_branch: main\n    allowed_visibility: [private]\n    extra: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{ConfigPath: path, WorkDir: dir, Git: &fakeGit{}, NewClient: func(config.Provider, string) (provider.Client, error) { return nil, errors.New("must not run") }}
	_, err := execute(t, a, "check")
	if ExitCode(err) != 2 {
		t.Fatalf("configuration error=%v code=%d", err, ExitCode(err))
	}
	a, g, _ := healthFixture(t)
	g.healthErr = errors.New("secret child output")
	out, err := execute(t, a, "check")
	if ExitCode(err) != 2 || out != ".: operational-error\n" || strings.Contains(out+err.Error(), "secret") {
		t.Fatalf("operational output=%q error=%v code=%d", out, err, ExitCode(err))
	}
}

func TestRequiredFileRootAndAncestorSwapsStayConfined(t *testing.T) {
	parent := t.TempDir()
	repository := filepath.Join(parent, "repository")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(filepath.Join(repository, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "docs", "README.md"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("inside-root"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "README.md"), []byte("outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(filepath.Join(repository, "docs"), filepath.Join(repository, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repository, "docs")); err != nil {
		t.Fatal(err)
	}
	if regular, err := regularFile(root, "docs/README.md"); regular || err != nil {
		t.Fatal("escaping ancestor symlink was accepted")
	}
	if err := os.Rename(repository, repository+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, repository); err != nil {
		t.Fatal(err)
	}
	if regular, err := regularFile(root, "README.md"); regular || err == nil {
		t.Fatal("outside file was accepted after root swap")
	}
}

func TestCheckAllContinuesAfterIndependentRepositoryFailure(t *testing.T) {
	t.Setenv("COLT_TEST_TOKEN", "test-secret-token")
	dir := t.TempDir()
	p := appProvider()
	p.Namespace, p.Host, p.BaseURL = "team", "gitlab.com", "https://gitlab.com"
	include := []string{"bad", "good"}
	policy := &config.Policy{Repository: config.RepositoryPolicy{DefaultBranch: "main", AllowedVisibility: []string{"private"}}}
	path := filepath.Join(dir, "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": p}, Workspace: &config.Workspace{Repositories: []config.RepositorySelection{{Provider: "work", Namespace: "team", Include: &include}}}, Policy: policy}); err != nil {
		t.Fatal(err)
	}
	repos := map[string]*provider.Repository{}
	listed := []provider.Repository{}
	for _, name := range include {
		r := provider.Repository{Name: name, Namespace: "team", CloneURL: "https://gitlab.com/team/" + name + ".git", SSHURL: "git@gitlab.com:team/" + name + ".git", DefaultBranch: "main", Visibility: "private"}
		repos[name] = &r
		listed = append(listed, r)
		repoDir := filepath.Join(dir, "team", name)
		if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repoDir, ".git", "config"), []byte("[remote \"origin\"]\nurl = "+r.CloneURL+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	g := &fakeGit{healthFunc: func(dir string) (gitnative.HealthState, error) {
		name := filepath.Base(dir)
		if name == "bad" {
			return gitnative.HealthState{}, errors.New("failure")
		}
		return gitnative.HealthState{Name: p.GitName, Email: p.GitEmail, Origin: repos[name].CloneURL, Clean: true}, nil
	}}
	c := &fakeClient{listRepos: listed, getRepos: repos}
	out, err := execute(t, testApp(path, dir, g, c), "check", "--all")
	if ExitCode(err) != 2 || c.gets != 2 || !strings.Contains(out, "team/bad: operational-error") {
		t.Fatalf("output=%q error=%v gets=%d", out, err, c.gets)
	}
}

func TestCheckAllContinuesAfterSelectionCredentialFailure(t *testing.T) {
	dir := t.TempDir()
	bad, good := appProvider(), appProvider()
	bad.Namespace, bad.Auth.TokenEnv = "bad-team", "MISSING_HEALTH_TOKEN"
	good.Namespace, good.Auth.TokenEnv = "good-team", "GOOD_HEALTH_TOKEN"
	t.Setenv("GOOD_HEALTH_TOKEN", "good-secret-token")
	badInclude, goodInclude := []string{"bad"}, []string{"good"}
	policy := &config.Policy{Repository: config.RepositoryPolicy{DefaultBranch: "main", AllowedVisibility: []string{"private"}}}
	path := filepath.Join(dir, "config.yaml")
	if err := config.Save(path, config.Config{
		Providers: map[string]config.Provider{"bad": bad, "good": good},
		Workspace: &config.Workspace{Repositories: []config.RepositorySelection{
			{Provider: "bad", Namespace: bad.Namespace, Include: &badInclude},
			{Provider: "good", Namespace: good.Namespace, Include: &goodInclude},
		}},
		Policy: policy,
	}); err != nil {
		t.Fatal(err)
	}
	remote := &provider.Repository{Name: "good", Namespace: good.Namespace, CloneURL: "https://gitlab.com/good-team/good.git", SSHURL: "git@gitlab.com:good-team/good.git", DefaultBranch: "main", Visibility: "private"}
	repositoryDir := filepath.Join(dir, "good-team", "good")
	if err := os.MkdirAll(filepath.Join(repositoryDir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{listRepos: []provider.Repository{*remote}, found: remote}
	runner := &fakeGit{health: gitnative.HealthState{Name: good.GitName, Email: good.GitEmail, Origin: remote.CloneURL, Clean: true}}
	a := &App{ConfigPath: path, WorkDir: dir, Git: runner, NewClient: func(p config.Provider, _ string) (provider.Client, error) { return client, nil }}
	out, err := execute(t, a, "check", "--all")
	if ExitCode(err) != 2 || !strings.Contains(out, "bad-team/bad: operational-error") || strings.Contains(out, "good-team/good:") || client.gets != 1 {
		t.Fatalf("output=%q error=%v gets=%d", out, err, client.gets)
	}
}

func TestMatchingOriginUsesConfiguredDefaultAndReportsAmbiguity(t *testing.T) {
	p := appProvider()
	cfg := config.Config{Providers: map[string]config.Provider{"one": p, "two": p}}
	origin := "https://gitlab.com/team/demo.git"
	if _, _, _, ambiguous := matchingOriginResult(cfg, origin); !ambiguous {
		t.Fatal("duplicate origin match was not ambiguous")
	}
	p.Default = true
	cfg.Providers["two"] = p
	alias, _, project, ambiguous := matchingOriginResult(cfg, origin)
	if ambiguous || alias != "two" || project != "demo" {
		t.Fatalf("default origin match=%q %q ambiguous=%t", alias, project, ambiguous)
	}
}

type foreignExitError struct{}

func (foreignExitError) Error() string { return "foreign" }
func (foreignExitError) ExitCode() int { return 2 }

func TestExitCodeIgnoresForeignTypedErrors(t *testing.T) {
	if got := ExitCode(fmt.Errorf("wrapped: %w", foreignExitError{})); got != 1 {
		t.Fatalf("ExitCode(foreign) = %d, want 1", got)
	}
}

func TestCheckRejectsInjectedUnsafeProviderMetadata(t *testing.T) {
	for _, mutate := range []func(*provider.Repository){
		func(r *provider.Repository) { r.DefaultBranch = "bad..branch" },
		func(r *provider.Repository) { r.Visibility = "secret" },
	} {
		a, _, client := healthFixture(t)
		mutate(client.found)
		out, err := execute(t, a, "check")
		if ExitCode(err) != 2 || out != ".: operational-error\n" {
			t.Fatalf("output=%q error=%v code=%d", out, err, ExitCode(err))
		}
	}
}

func TestHealthRepositoryMetadataUsesProviderNameCaseSemantics(t *testing.T) {
	github := config.Provider{Type: "github", Host: "github.com", Namespace: "Team"}
	repository := provider.Repository{Name: "demo", Namespace: "Team", CloneURL: "https://github.com/Team/demo.git", DefaultBranch: "main", Visibility: "private"}
	if err := healthRepositoryMetadata(repository, github, "Demo"); err != nil {
		t.Fatalf("GitHub case-insensitive name rejected: %v", err)
	}
	gitlab := github
	gitlab.Type, gitlab.Host = "gitlab", "gitlab.com"
	repository.CloneURL = "https://gitlab.com/Team/demo.git"
	if err := healthRepositoryMetadata(repository, gitlab, "Demo"); err == nil {
		t.Fatal("GitLab case-sensitive name mismatch accepted")
	}
}

func TestRequiredFileErrorsAreOperationalButInvalidPathsAreFindings(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("missing", filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	regular, err := regularFile(root, "README.md")
	if regular || err != nil {
		t.Fatalf("symlink regular=%t error=%v", regular, err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := regularFile(root, "README.md"); err == nil {
		t.Fatal("closed root was not operational")
	}
}
