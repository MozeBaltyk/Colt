package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MozeBaltyk/Colt/internal/config"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
	templating "github.com/MozeBaltyk/Colt/internal/template"
)

func TestTEMPLATE_SOURCE_001ListAndShowMetadataOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{Providers: map[string]config.Provider{}, Templates: map[string]config.Template{
		"zeta": {Default: "1", Versions: map[string]config.TemplateVersion{"1": {Source: "missing-zeta", Digest: "sha256:" + strings.Repeat("b", 64)}}},
		"service": {Default: "2", Versions: map[string]config.TemplateVersion{
			"2": {Source: "missing-service", Digest: "sha256:" + strings.Repeat("a", 64), Parameters: map[string]config.TemplateParameter{"owner": {Required: true}}},
			"1": {Source: "also-missing", Digest: "sha256:" + strings.Repeat("c", 64)},
		}},
	}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	a := &App{ConfigPath: path}
	out, err := execute(t, a, "template", "list")
	if err != nil || out != "service@1\nservice@2 (default)\nzeta@1 (default)\n" {
		t.Fatalf("list output=%q error=%v", out, err)
	}
	out, err = execute(t, a, "template", "show", "service")
	for _, want := range []string{"Resolved: service@2", "Source: missing-service", "Digest: sha256:", "owner: required"} {
		if err != nil || !strings.Contains(out, want) {
			t.Fatalf("show output=%q error=%v lacks %q", out, err, want)
		}
	}
}

func TestTEMPLATE_INIT_001LocalTemplateIsCommitted(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README-{{owner}}.md"), []byte("{{owner}} / {{license}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := templating.Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	license := "MIT"
	p := appProvider()
	p.Default = true
	path := filepath.Join(root, "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": p}, Templates: map[string]config.Template{
		"service": {Default: "2", Versions: map[string]config.TemplateVersion{"2": {
			Source: "source", Digest: digest,
			Parameters: map[string]config.TemplateParameter{"owner": {Required: true}, "license": {Default: &license}},
		}}},
	}}); err != nil {
		t.Fatal(err)
	}
	a := &App{ConfigPath: path, WorkDir: root, Git: gitnative.Native{}}
	out, err := execute(t, a, "--noninteractive", "init", "demo", "--local", "--template", "service", "--set", "owner=platform")
	if err != nil {
		t.Fatalf("init error=%v output=%q", err, out)
	}
	data, err := os.ReadFile(filepath.Join(root, "demo", "README-platform.md"))
	if err != nil || string(data) != "platform / MIT\n" {
		t.Fatalf("materialized data=%q error=%v", data, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "show", "HEAD:README-platform.md")
	cmd.Dir = filepath.Join(root, "demo")
	committed, err := cmd.Output()
	if err != nil || string(committed) != string(data) {
		t.Fatalf("committed data=%q error=%v", committed, err)
	}
	if _, err := os.Stat(filepath.Join(source, "README-{{owner}}.md")); err != nil {
		t.Fatalf("source changed: %v", err)
	}
}

func TestTEMPLATE_PARAM_001InvalidParametersFailBeforeMutation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("{{owner}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := templating.Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	p := appProvider()
	p.Default = true
	path := filepath.Join(root, "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": p}, Templates: map[string]config.Template{
		"service": {Default: "1", Versions: map[string]config.TemplateVersion{"1": {Source: "source", Digest: digest, Parameters: map[string]config.TemplateParameter{"owner": {Required: true}}}}},
	}}); err != nil {
		t.Fatal(err)
	}
	tests := [][]string{
		{"init", "demo", "--local", "--set", "owner=x"},
		{"--noninteractive", "init", "demo", "--local", "--template", "service"},
		{"init", "demo", "--local", "--template", "service", "--set", "other=x"},
		{"init", "demo", "--local", "--template", "service", "--set", "owner=x", "--set", "owner=y"},
		{"init", "demo", "--local", "--template", "service", "--set", "owner=x\ny"},
	}
	for _, args := range tests {
		if err := os.RemoveAll(filepath.Join(root, "demo")); err != nil {
			t.Fatal(err)
		}
		runner := &fakeGit{}
		_, err := execute(t, &App{ConfigPath: path, WorkDir: root, Git: runner}, args...)
		if err == nil {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
		if _, statErr := os.Lstat(filepath.Join(root, "demo")); !os.IsNotExist(statErr) || len(runner.calls) != 0 {
			t.Fatalf("%v mutated before rejection: stat=%v git=%v", args, statErr, runner.calls)
		}
	}
}

func TestTEMPLATE_PARAM_001InteractiveValuesAreBoundedAndSingleLine(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("{{owner}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := templating.Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	p := appProvider()
	p.Default = true
	path := filepath.Join(root, "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": p}, Templates: map[string]config.Template{
		"service": {Default: "1", Versions: map[string]config.TemplateVersion{"1": {Source: "source", Digest: digest, Parameters: map[string]config.TemplateParameter{"owner": {Required: true}}}}},
	}}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{strings.Repeat("x", maxInteractiveInput+1) + "\n", "x\ry\n"} {
		runner := &fakeGit{}
		_, err := executeInput(t, &App{ConfigPath: path, WorkDir: root, Git: runner, IsTerminal: func() bool { return true }}, input, "init", "demo", "--local", "--template", "service")
		if err == nil || len(runner.calls) != 0 {
			t.Fatalf("interactive input accepted or mutated: error=%v calls=%v", err, runner.calls)
		}
	}
}

type inheritedCloneGit struct{ fakeGit }

func (g *inheritedCloneGit) Clone(ctx context.Context, _, destination, _, _, _ string) error {
	g.calls = append(g.calls, "clone")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	native := gitnative.Native{}
	if err := native.Init(ctx, destination); err != nil {
		return err
	}
	if err := native.SetIdentity(ctx, destination, "Inherited", "inherited@example.com"); err != nil {
		return err
	}
	_, err := native.Commit(ctx, destination)
	return err
}

func TestTEMPLATE_INIT_001RemoteRejectsEmptyInheritedCommit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "template.txt"), []byte("validated"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := templating.Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	p := appProvider()
	p.Default = true
	path := filepath.Join(root, "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"work": p}, Templates: map[string]config.Template{
		"service": {Default: "1", Versions: map[string]config.TemplateVersion{"1": {Source: "source", Digest: digest}}},
	}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COLT_TEST_TOKEN", "test-token")
	client := &fakeClient{getErr: provider.ErrNotFound, created: &provider.Repository{CloneURL: "https://gitlab.com/team/demo.git"}}
	runner := &inheritedCloneGit{}
	a := &App{ConfigPath: path, WorkDir: root, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) { return client, nil }}
	_, err = execute(t, a, "init", "demo", "--template", "service")
	if err == nil || !strings.Contains(err.Error(), "history") {
		t.Fatalf("inherited empty commit accepted: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "team", "demo", "template.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("template materialized into inherited history: %v", statErr)
	}
}
