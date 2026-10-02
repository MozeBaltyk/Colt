package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	"github.com/MozeBaltyk/Colt/internal/provider"
)

func TestCLONE_001ClonesRepository(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := storedProvider("personal")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	store := &recordingStore{credentials: map[string]credential.Credential{
		"github.com/personal": {Kind: "bearer_token", Secret: "secret"},
	}}
	runner := &fakeGit{}
	client := &fakeClient{
		found: &provider.Repository{CloneURL: "https://github.com/octocat/demo.git"},
	}
	_, err := execute(t, &App{ConfigPath: path, WorkDir: workDir, Credentials: store, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
		return client, nil
	}}, "clone", "demo")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !containsCall(runner.calls, "clone") {
		t.Fatalf("expected clone call, got %v", runner.calls)
	}
}

func TestCLONE_002UnknownProviderAliasFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{}}); err != nil {
		t.Fatal(err)
	}
	_, err := execute(t, testApp(path, "", &fakeGit{}, &fakeClient{}), "clone", "demo", "--provider", "missing")
	if err == nil || !strings.Contains(err.Error(), "unknown provider alias") {
		t.Fatalf("expected unknown alias error, got %v", err)
	}
}

func TestCLONE_003InvalidProjectNameFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := storedProvider("personal")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	_, err := execute(t, testApp(path, "", &fakeGit{}, &fakeClient{}), "clone", "bad/name")
	if err == nil || !strings.Contains(err.Error(), "invalid project name") {
		t.Fatalf("expected invalid name error, got %v", err)
	}
}
