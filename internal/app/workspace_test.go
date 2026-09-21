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

func writeWorkspaceConfig(t *testing.T, path string, providers map[string]config.Provider, selections ...config.RepositorySelection) {
	t.Helper()
	if err := config.Save(path, config.Config{Providers: providers, Workspace: &config.Workspace{Repositories: selections}}); err != nil {
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
	writeWorkspaceConfig(t, path, map[string]config.Provider{"work": p}, config.RepositorySelection{Provider: "work", Namespace: "team", Include: &include})
	present := workspaceRepo("github.com", "team", "present")
	writeRepository(t, root, "team/present", present.CloneURL)
	if err := os.MkdirAll(filepath.Join(root, "team", "mismatch"), 0o755); err != nil {
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
	for _, want := range []string{"absent-remotely team/absent", "to-clone team/missing", "mismatch team/mismatch", "present team/present"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status %q lacks %q", status, want)
		}
	}
	if len(runner.calls) != 0 {
		t.Fatalf("dry-run mutated through Git: %v", runner.calls)
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

type selectiveGit struct {
	fakeGit
	fail string
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
	writeWorkspaceConfig(t, path, map[string]config.Provider{"work": p}, config.RepositorySelection{Provider: "work", Namespace: "team", Include: &include})
	client := &fakeClient{listRepos: []provider.Repository{workspaceRepo("github.com", "team", "bad"), workspaceRepo("github.com", "team", "good")}}
	runner := &selectiveGit{fail: "bad"}
	a := &App{ConfigPath: path, WorkDir: root, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) { return client, nil }}
	output, err := execute(t, a, "sync")
	if err == nil || !strings.Contains(output, "failed team/bad") || !strings.Contains(output, "cloned team/good") {
		t.Fatalf("sync=%q, %v", output, err)
	}
	if _, err := os.Stat(filepath.Join(root, "team", "good", ".git", "config")); err != nil {
		t.Fatalf("completed clone not retained: %v", err)
	}
	runner.fail = ""
	if _, err := execute(t, a, "sync"); err != nil {
		t.Fatal(err)
	}
	before := len(runner.calls)
	output, err = execute(t, a, "sync")
	if err != nil || !strings.Contains(output, "present team/bad") || !strings.Contains(output, "present team/good") || len(runner.calls) != before {
		t.Fatalf("idempotent sync=%q err=%v calls=%v", output, err, runner.calls)
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
	writeWorkspaceConfig(t, path, map[string]config.Provider{"source": source, "target": target})
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
