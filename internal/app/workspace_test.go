package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/provider"
)

func workspaceProvider(kind, namespace string) config.Provider {
	p := config.Provider{Type: kind, Namespace: namespace, Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "env", TokenEnv: "WORKSPACE_TOKEN"}}
	if kind == "github" {
		p.Host, p.BaseURL = "github.com", "https://api.github.com"
	} else {
		p.Host, p.BaseURL = "gitlab.com", "https://gitlab.com"
	}
	return p
}

func workspaceRepo(host, namespace, name string) provider.Repository {
	return provider.Repository{Name: name, Namespace: namespace, CloneURL: "https://" + host + "/" + namespace + "/" + name + ".git", SSHURL: "git@" + host + ":" + namespace + "/" + name + ".git"}
}

func writeWorkspaceConfig(t *testing.T, path, root string, providers map[string]config.Provider, selections ...config.RepositorySelection) {
	t.Helper()
	cfg := config.Config{Providers: providers}
	if len(selections) != 0 {
		cfg.Workspaces = map[string]config.Workspace{"managed": {Path: root, Repositories: selections}}
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_TOKEN", "secret")
}

func writeRepository(t *testing.T, root, relative, origin string) {
	t.Helper()
	dir := filepath.Join(root, relative, ".git")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := "[remote \"origin\"]\n\turl = " + origin + "\n"
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWORKSPACE_STATUS_001AndDryRunUseSameReadOnlyPlan(t *testing.T) {
	root := t.TempDir()
	p := workspaceProvider("github", "team")
	include := []string{"present", "missing", "mismatch", "absent"}
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeWorkspaceConfig(t, path, root, map[string]config.Provider{"work": p}, config.RepositorySelection{Provider: "work", Namespace: "team", Include: &include})
	present := workspaceRepo("github.com", "team", "present")
	writeRepository(t, root, "present", present.CloneURL)
	if err := os.MkdirAll(filepath.Join(root, "mismatch"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{listRepos: []provider.Repository{present, workspaceRepo("github.com", "team", "missing"), workspaceRepo("github.com", "team", "mismatch")}}
	runner := &fakeGit{}
	a := testApp(path, root, runner, client)
	status, statusErr := execute(t, a, "status")
	dry, dryErr := execute(t, a, "sync", "--dry-run")
	if statusErr == nil || dryErr == nil || status != dry {
		t.Fatalf("status=%q/%v dry=%q/%v", status, statusErr, dry, dryErr)
	}
	for _, want := range []string{"managed: absent-remotely absent", "managed: to-clone missing", "managed: mismatch mismatch", "managed: present present"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status %q lacks %q", status, want)
		}
	}
	if len(runner.calls) != 0 {
		t.Fatalf("dry-run mutated through Git: %v", runner.calls)
	}
}

func TestWORKSPACE_SELECTION_001AllAndSelectedAreDeterministicAndQualified(t *testing.T) {
	alphaRoot, factoryRoot := t.TempDir(), t.TempDir()
	alpha, factory := workspaceProvider("github", "alpha"), workspaceProvider("github", "factory")
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{
		Providers: map[string]config.Provider{"alpha": alpha, "factory": factory},
		Workspaces: map[string]config.Workspace{
			"factory": {Path: factoryRoot, Repositories: []config.RepositorySelection{{Provider: "factory", Namespace: "factory"}}},
			"alpha":   {Path: alphaRoot, Repositories: []config.RepositorySelection{{Provider: "alpha", Namespace: "alpha"}}},
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_TOKEN", "secret")
	clients := map[string]*fakeClient{
		"alpha":   {listRepos: []provider.Repository{workspaceRepo("github.com", "alpha", "one")}},
		"factory": {listRepos: []provider.Repository{workspaceRepo("github.com", "factory", "two")}},
	}
	a := &App{ConfigPath: path, Git: &fakeGit{}, NewClient: func(p config.Provider, _ string) (provider.Client, error) { return clients[p.Namespace], nil }}

	all, err := execute(t, a, "status")
	if err != nil || all != "alpha: to-clone one\nfactory: to-clone two\n" {
		t.Fatalf("all status=%q, %v", all, err)
	}
	selected, err := execute(t, a, "status", "--workspace", "factory")
	if err != nil || selected != "factory: to-clone two\n" || clients["alpha"].lists != 1 || clients["factory"].lists != 2 {
		t.Fatalf("selected status=%q, %v lists=%d/%d", selected, err, clients["alpha"].lists, clients["factory"].lists)
	}
	for _, command := range []string{"status", "sync"} {
		if _, err := execute(t, a, command, "--workspace", "missing"); err == nil || !strings.Contains(err.Error(), "unknown workspace") {
			t.Fatalf("%s unknown workspace error = %v", command, err)
		}
	}
}

func TestWORKSPACE_SELECTION_001FailureInOneWorkspaceDoesNotStopAnother(t *testing.T) {
	badRoot, goodRoot := t.TempDir(), t.TempDir()
	bad, good := workspaceProvider("github", "bad"), workspaceProvider("github", "good")
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{
		Providers: map[string]config.Provider{"bad": bad, "good": good},
		Workspaces: map[string]config.Workspace{
			"bad":  {Path: badRoot, Repositories: []config.RepositorySelection{{Provider: "bad", Namespace: "bad"}}},
			"good": {Path: goodRoot, Repositories: []config.RepositorySelection{{Provider: "good", Namespace: "good"}}},
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_TOKEN", "secret")
	badClient := &fakeClient{listErr: errors.New("injected list failure")}
	goodClient := &fakeClient{listRepos: []provider.Repository{workspaceRepo("github.com", "good", "demo")}}
	a := &App{ConfigPath: path, Git: &selectiveGit{}, NewClient: func(p config.Provider, _ string) (provider.Client, error) {
		if p.Namespace == "bad" {
			return badClient, nil
		}
		return goodClient, nil
	}}
	output, err := execute(t, a, "sync")
	if err == nil || !strings.Contains(err.Error(), "bad") || !strings.Contains(output, "bad: failed") || !strings.Contains(output, "good: cloned demo") || goodClient.lists != 1 {
		t.Fatalf("sync=%q, %v good lists=%d", output, err, goodClient.lists)
	}
}

func TestWorkspaceNamespaceMatchingIsExactAndProviderAware(t *testing.T) {
	if namespaceMatches([]string{"team", "sub"}, []string{"team"}, "gitlab") || namespaceMatches([]string{"team"}, []string{"team", "sub"}, "gitlab") {
		t.Fatal("GitLab namespace prefix matched as an exact namespace")
	}
	if !namespaceMatches([]string{"Team"}, []string{"team"}, "gitea") || namespaceMatches([]string{"Team", "sub"}, []string{"team"}, "gitea") {
		t.Fatal("Gitea namespace matching is inconsistent with config validation")
	}
}

func TestMIRROR_001CredentialScopeAcceptsValidatedNamespaceOverride(t *testing.T) {
	p := workspaceProvider("gitlab", "configured")
	cfg := config.Config{Providers: map[string]config.Provider{"source": p}}
	request := map[string]string{"protocol": "https", "host": p.Host, "path": "other/group/demo.git"}
	got, ok := credentialProvider(cfg, request, "source", "demo", "other/group")
	if !ok || got.Namespace != "other/group" {
		t.Fatalf("credential namespace override = %#v, %v", got, ok)
	}
	if _, ok := credentialProvider(cfg, request, "source", "demo", "../outside"); ok {
		t.Fatal("unsafe credential namespace override accepted")
	}
}

func TestWORKSPACE_SAFETY_001SyncNeverReplacesAppearedDestination(t *testing.T) {
	workspace := t.TempDir()
	root, err := openWorkRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	staging := t.TempDir()
	if err := os.WriteFile(filepath.Join(staging, "clone-data"), []byte("clone"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(workspace, "team", "demo")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := installCloneAbsent(root, staging, "team/demo"); err == nil || !strings.Contains(err.Error(), "appeared") {
		t.Fatalf("appeared destination error=%v", err)
	}
	after, err := os.Stat(destination)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("appeared destination was replaced: before=%v after=%v err=%v", before, after, err)
	}
}

func TestWorkspaceRootResolvesLinkedSystemPaths(t *testing.T) {
	parent, target := t.TempDir(), t.TempDir()
	workspace := filepath.Join(target, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	path := filepath.Join(link, "workspace")
	root, err := openWorkspaceRoot(path)
	if err != nil {
		t.Fatalf("canonical workspace root rejected: %v", err)
	}
	defer root.Close()
	canonical, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if root.Name() != canonical {
		t.Fatalf("workspace root = %q, want canonical %q", root.Name(), canonical)
	}
}

func TestWorkspaceStatusRejectsAmbiguousOrigin(t *testing.T) {
	root := t.TempDir()
	repository := workspaceRepo("github.com", "team", "demo")
	writeRepository(t, root, "demo", repository.CloneURL)
	configPath := filepath.Join(root, "demo", ".git", "config")
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\turl = https://github.com/team/other.git\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	workspaceRoot, err := openWorkspaceRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer workspaceRoot.Close()
	if category, _ := classifyWorkspacePath(workspaceRoot, "demo", repository); category != "mismatch" {
		t.Fatalf("ambiguous origin category = %q, want mismatch", category)
	}
}

func TestWorkspaceRootRejectsSwappedRootSymlink(t *testing.T) {
	parent, outside := t.TempDir(), t.TempDir()
	workspace := filepath.Join(parent, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(workspace, workspace+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, workspace); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if root, err := openWorkspaceRoot(workspace); err == nil {
		root.Close()
		t.Fatal("workspace root accepted a swapped escaping symlink")
	}
}

func TestSyncCreatesMissingNamedWorkspaceRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing", "workspace")
	p := workspaceProvider("github", "team")
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeWorkspaceConfig(t, path, root, map[string]config.Provider{"work": p}, config.RepositorySelection{Provider: "work", Namespace: "team"})
	repository := workspaceRepo("github.com", "team", "demo")
	runner := &selectiveGit{}
	client := &fakeClient{listRepos: []provider.Repository{repository}}
	a := &App{ConfigPath: path, WorkDir: t.TempDir(), Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) { return client, nil }}
	if output, err := execute(t, a, "sync"); err != nil || !strings.Contains(output, "managed: cloned demo") {
		t.Fatalf("sync=%q, %v", output, err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("created workspace root = %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(root, "demo", ".git", "config")); err != nil {
		t.Fatalf("clone missing from created workspace: %v", err)
	}
}

func TestStatusDoesNotCreateMissingNamedWorkspaceRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing", "workspace")
	p := workspaceProvider("github", "team")
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeWorkspaceConfig(t, path, root, map[string]config.Provider{"work": p}, config.RepositorySelection{Provider: "work", Namespace: "team"})
	a := &App{ConfigPath: path, WorkDir: t.TempDir(), Git: &fakeGit{}, NewClient: func(config.Provider, string) (provider.Client, error) {
		return &fakeClient{listRepos: []provider.Repository{workspaceRepo("github.com", "team", "demo")}}, nil
	}}
	if _, err := execute(t, a, "status"); err == nil {
		t.Fatal("status unexpectedly succeeded for a missing workspace root")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status mutated the missing workspace root: %v", err)
	}
}

func TestWorkspaceRefreshRejectsReplacedRepositoryPath(t *testing.T) {
	workspace := t.TempDir()
	path := "demo"
	if err := os.Mkdir(filepath.Join(workspace, path), 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := openWorkspaceRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	repositoryRoot, err := root.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repositoryRoot.Close()
	if err := os.Rename(filepath.Join(workspace, path), filepath.Join(workspace, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(workspace, "moved"), filepath.Join(workspace, path)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if stableWorkspaceRepository(root, repositoryRoot, path) {
		t.Fatal("replaced repository path remained refreshable")
	}
}

type selectiveGit struct {
	fakeGit
	fail string
}

type selectiveRefreshGit struct{ fakeGit }

func (g *selectiveRefreshGit) Fetch(_ context.Context, dir string, _ []string, _, _, _ string) error {
	name := filepath.Base(dir)
	g.calls = append(g.calls, "fetch:"+name)
	if name == "bad" {
		return errors.New("injected fetch failure")
	}
	return nil
}

func (g *selectiveGit) Clone(ctx context.Context, url, destination, username, alias, project string) error {
	if project == g.fail {
		g.calls = append(g.calls, "clone-failed:"+project)
		return errors.New("injected clone failure")
	}
	g.cloneHook = func(destination string) error {
		write := "[remote \"origin\"]\n\turl = " + url + "\n"
		if err := os.MkdirAll(filepath.Join(destination, ".git"), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, ".git", "config"), []byte(write), 0o600)
	}
	return g.fakeGit.Clone(ctx, url, destination, username, alias, project)
}

func TestWORKSPACE_SYNC_001ContinuesAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	p := workspaceProvider("github", "team")
	include := []string{"bad", "good"}
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeWorkspaceConfig(t, path, root, map[string]config.Provider{"work": p}, config.RepositorySelection{Provider: "work", Namespace: "team", Include: &include})
	client := &fakeClient{listRepos: []provider.Repository{workspaceRepo("github.com", "team", "bad"), workspaceRepo("github.com", "team", "good")}}
	runner := &selectiveGit{fail: "bad"}
	a := &App{ConfigPath: path, WorkDir: root, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) { return client, nil }}
	output, err := execute(t, a, "sync")
	if err == nil || !strings.Contains(output, "managed: failed bad") || !strings.Contains(output, "managed: cloned good") {
		t.Fatalf("sync=%q, %v", output, err)
	}
	if _, err := os.Stat(filepath.Join(root, "good", ".git", "config")); err != nil {
		t.Fatalf("completed clone not retained: %v", err)
	}
	runner.fail = ""
	if _, err := execute(t, a, "sync"); err != nil {
		t.Fatal(err)
	}
	before := len(runner.calls)
	output, err = execute(t, a, "sync")
	if err != nil || !strings.Contains(output, "managed: present bad") || !strings.Contains(output, "managed: present good") || len(runner.calls) != before {
		t.Fatalf("idempotent sync=%q err=%v calls=%v", output, err, runner.calls)
	}
}

func TestWorkspaceRefreshFlagsAndIndependentFetch(t *testing.T) {
	root := t.TempDir()
	p := workspaceProvider("github", "team")
	include := []string{"bad", "good", "missing"}
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeWorkspaceConfig(t, path, root, map[string]config.Provider{"work": p}, config.RepositorySelection{Provider: "work", Namespace: "team", Include: &include})
	repositories := []provider.Repository{
		workspaceRepo("github.com", "team", "bad"), workspaceRepo("github.com", "team", "good"), workspaceRepo("github.com", "team", "missing"),
	}
	writeRepository(t, root, "bad", repositories[0].CloneURL)
	writeRepository(t, root, "good", repositories[1].CloneURL)
	runner := &selectiveRefreshGit{}
	runner.cloneHook = func(destination string) error {
		return os.MkdirAll(filepath.Join(destination, ".git"), 0o755)
	}
	client := &fakeClient{listRepos: repositories}
	a := &App{ConfigPath: path, WorkDir: root, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) { return client, nil }}
	output, err := execute(t, a, "sync", "--fetch")
	if err == nil || !strings.Contains(output, "managed: failed bad") || !strings.Contains(output, "managed: fetched good") || !strings.Contains(output, "managed: cloned missing") {
		t.Fatalf("fetch sync=%q, %v", output, err)
	}
	if strings.Contains(strings.Join(runner.calls, "\n"), "fetch:missing") {
		t.Fatalf("new clone was redundantly fetched: %v", runner.calls)
	}
	if _, err := execute(t, a, "sync", "--fetch", "--pull"); err == nil {
		t.Fatal("--fetch and --pull were accepted together")
	}
}

func TestWorkspaceRefreshDryRunReportsWithoutGitMutation(t *testing.T) {
	root := t.TempDir()
	p := workspaceProvider("github", "team")
	include := []string{"present"}
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeWorkspaceConfig(t, path, root, map[string]config.Provider{"work": p}, config.RepositorySelection{Provider: "work", Namespace: "team", Include: &include})
	repository := workspaceRepo("github.com", "team", "present")
	writeRepository(t, root, "present", repository.CloneURL)
	for _, mode := range []string{"--fetch", "--pull"} {
		runner := &fakeGit{}
		output, err := execute(t, testApp(path, root, runner, &fakeClient{listRepos: []provider.Repository{repository}}), "sync", mode, "--dry-run")
		if err != nil || !strings.Contains(output, "managed: would-"+strings.TrimPrefix(mode, "--")+" present") || len(runner.calls) != 0 {
			t.Fatalf("%s dry-run=%q/%v calls=%v", mode, output, err, runner.calls)
		}
	}
}

func TestSyncNamespaceUsesFlatPathOverrideAndCanSave(t *testing.T) {
	root := t.TempDir()
	configured := workspaceProvider("github", "configured")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": configured}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_TOKEN", "secret")
	client := &fakeClient{listRepos: []provider.Repository{
		workspaceRepo("github.com", "other", "demo"),
		workspaceRepo("github.com", "configured", "ignored"),
	}}
	runner := &selectiveGit{}
	a := &App{ConfigPath: path, WorkDir: t.TempDir(), Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) { return client, nil }}
	output, err := execute(t, a, "sync", "other", "--provider", "work", "--path", root, "--workspace", "factory", "--save")
	if err != nil || !strings.Contains(output, "factory: to-clone demo") || strings.Contains(output, "ignored") {
		t.Fatalf("sync=%q err=%v", output, err)
	}
	if _, err := os.Stat(filepath.Join(root, "demo", ".git", "config")); err != nil {
		t.Fatalf("flat clone missing: %v", err)
	}
	if len(runner.helperNamespaces) != 1 || runner.helperNamespaces[0] != "other" {
		t.Fatalf("credential helper namespaces = %v", runner.helperNamespaces)
	}
	cfg, err := config.Load(path)
	saved := cfg.Workspaces["factory"]
	if err != nil || saved.Path != root || len(saved.Repositories) != 1 || saved.Repositories[0].Namespace != "other" {
		t.Fatalf("saved workspace = %#v, %v", saved, err)
	}
	if _, err := execute(t, a, "sync", "other", "--provider", "work", "--path", root, "--workspace", "factory", "--save"); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(path)
	saved = cfg.Workspaces["factory"]
	if err != nil || len(saved.Repositories) != 1 {
		t.Fatalf("save duplicated selection: %#v, %v", saved, err)
	}
	a.WorkDir = t.TempDir()
	status, err := execute(t, a, "status")
	if err != nil || !strings.Contains(status, "factory: present demo") {
		t.Fatalf("saved status=%q err=%v", status, err)
	}
	if err := os.RemoveAll(filepath.Join(root, "demo")); err != nil {
		t.Fatal(err)
	}
	if output, err := execute(t, a, "sync"); err != nil || !strings.Contains(output, "factory: cloned demo") {
		t.Fatalf("saved sync=%q err=%v", output, err)
	}
}

func TestSyncSaveRejectsFlatDestinationCollision(t *testing.T) {
	root := t.TempDir()
	p := workspaceProvider("github", "configured")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{Providers: map[string]config.Provider{"work": p}, Workspaces: map[string]config.Workspace{"factory": {
		Path: root, Repositories: []config.RepositorySelection{{Provider: "work", Namespace: "one"}},
	}}}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_TOKEN", "secret")
	client := &fakeClient{listRepos: []provider.Repository{
		workspaceRepo("github.com", "one", "demo"),
		workspaceRepo("github.com", "two", "demo"),
	}}
	a := testApp(configPath, t.TempDir(), &fakeGit{}, client)
	if _, err := execute(t, a, "sync", "two", "--path", root, "--workspace", "factory", "--save"); err == nil || !strings.Contains(err.Error(), "duplicate repository identity or path") {
		t.Fatalf("flat collision error = %v", err)
	}
	stored, err := config.Load(configPath)
	if err != nil || len(stored.Workspaces["factory"].Repositories) != 1 {
		t.Fatalf("conflicting workspace was saved: %#v, %v", stored.Workspaces["factory"], err)
	}
}

func TestSyncSaveRequiresWorkspaceAndRejectsInvalidCombinations(t *testing.T) {
	p := workspaceProvider("github", "team")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(configPath, config.Config{Providers: map[string]config.Provider{"work": p}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_TOKEN", "secret")
	a := testApp(configPath, t.TempDir(), &fakeGit{}, &fakeClient{})
	if _, err := execute(t, a, "sync", "team", "--path", t.TempDir(), "--save"); err == nil || !strings.Contains(err.Error(), "--workspace") {
		t.Fatalf("missing workspace error = %v", err)
	}
	if _, err := execute(t, a, "sync", "team", "--path", filepath.Join(t.TempDir(), "missing"), "--workspace", "factory", "--save"); err == nil {
		t.Fatal("missing explicit path was accepted")
	}
	if _, err := execute(t, a, "sync", "team", "--path", t.TempDir(), "--workspace", "factory", "--save", "--dry-run"); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("save dry-run error = %v", err)
	}
	if _, err := execute(t, a, "sync", "team", "--path", t.TempDir()); err != nil {
		t.Fatalf("ad-hoc sync without save required a workspace name: %v", err)
	}
}

type mirrorTarget struct {
	fakeClient
	provider   config.Provider
	exists     bool
	operations []string
}

func (c *mirrorTarget) Get(_ context.Context, project string) (*provider.Repository, error) {
	c.operations = append(c.operations, "get:"+project)
	if !c.exists {
		return nil, provider.ErrNotFound
	}
	r := workspaceRepo(c.provider.Host, c.provider.Namespace, project)
	return &r, nil
}

func (c *mirrorTarget) Create(_ context.Context, project string, _ ...string) (*provider.Repository, error) {
	c.operations = append(c.operations, "create:"+project)
	c.exists = true
	r := workspaceRepo(c.provider.Host, c.provider.Namespace, project)
	return &r, nil
}

func TestMIRROR_001_005ConflictAndReplace(t *testing.T) {
	root := t.TempDir()
	source, target := workspaceProvider("github", "source"), workspaceProvider("gitlab", "target")
	path := filepath.Join(root, "config.yaml")
	writeWorkspaceConfig(t, path, root, map[string]config.Provider{"source": source, "target": target})
	sourceClient := &fakeClient{listRepos: []provider.Repository{workspaceRepo(source.Host, source.Namespace, "demo")}}
	targetClient := &mirrorTarget{provider: target, exists: true}
	runner := &fakeGit{}
	a := testApp(path, root, runner, sourceClient)
	a.NewClient = func(p config.Provider, _ string) (provider.Client, error) {
		if p.Type == target.Type {
			return targetClient, nil
		}
		return sourceClient, nil
	}
	if _, err := execute(t, a, "mirror", "source", "target"); err == nil || strings.Contains(strings.Join(runner.calls, ","), "mirror-push") {
		t.Fatalf("conflict overwrote target: err=%v calls=%v", err, runner.calls)
	}
	runner.calls = nil
	if output, err := execute(t, a, "mirror", "source", "target", "--replace"); err != nil || !strings.Contains(output, "mirrored demo") || !strings.Contains(strings.Join(runner.calls, ","), "mirror-push:https://gitlab.com/target/demo.git:demo:true") {
		t.Fatalf("replace output=%q err=%v calls=%v", output, err, runner.calls)
	}
	if strings.Contains(strings.Join(runner.calls, ","), "secret") {
		t.Fatal("credential leaked into Git arguments")
	}
}

func TestMIRROR_002TargetNamespaceAndRepositoryFilter(t *testing.T) {
	root := t.TempDir()
	source, target := workspaceProvider("github", "source"), workspaceProvider("gitlab", "target")
	path := filepath.Join(root, "config.yaml")
	writeWorkspaceConfig(t, path, root, map[string]config.Provider{"source": source, "target": target})
	sourceClient := &fakeClient{listRepos: []provider.Repository{
		workspaceRepo(source.Host, source.Namespace, "keep"),
		workspaceRepo(source.Host, source.Namespace, "skip"),
	}}
	targetClient := &mirrorTarget{provider: target}
	runner := &fakeGit{}
	a := testApp(path, root, runner, sourceClient)
	var resolvedTarget config.Provider
	a.NewClient = func(p config.Provider, _ string) (provider.Client, error) {
		if p.Type == target.Type {
			resolvedTarget = p
			targetClient.provider = p
			return targetClient, nil
		}
		return sourceClient, nil
	}
	output, err := execute(t, a, "mirror", "source", "target", "--repository", "keep", "--target-namespace", "other/group")
	if err != nil || !strings.Contains(output, "mirrored keep") || strings.Contains(output, "mirrored skip") {
		t.Fatalf("mirror=%q err=%v", output, err)
	}
	if resolvedTarget.Namespace != "other/group" {
		t.Fatalf("target namespace = %q", resolvedTarget.Namespace)
	}
	if _, err := execute(t, a, "mirror", "source", "target", "--repository", "missing"); err == nil {
		t.Fatalf("missing repository filter should error")
	}
}

type caseFoldTarget struct {
	mirrorTarget
}

func (c *caseFoldTarget) Create(_ context.Context, project string, _ ...string) (*provider.Repository, error) {
	r := workspaceRepo(c.provider.Host, c.provider.Namespace, strings.ToLower(project))
	return &r, nil
}

func TestMIRROR_003TargetNameMatchingIsCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	source, target := workspaceProvider("github", "source"), workspaceProvider("gitlab", "target")
	path := filepath.Join(root, "config.yaml")
	writeWorkspaceConfig(t, path, root, map[string]config.Provider{"source": source, "target": target})
	sourceClient := &fakeClient{listRepos: []provider.Repository{workspaceRepo(source.Host, source.Namespace, "Deployment")}}
	targetClient := &caseFoldTarget{mirrorTarget{provider: target}}
	runner := &fakeGit{}
	a := testApp(path, root, runner, sourceClient)
	a.NewClient = func(p config.Provider, _ string) (provider.Client, error) {
		if p.Type == target.Type {
			return targetClient, nil
		}
		return sourceClient, nil
	}
	output, err := execute(t, a, "mirror", "source", "target")
	if err != nil || !strings.Contains(output, "mirrored Deployment") {
		t.Fatalf("mirror=%q err=%v", output, err)
	}
}
