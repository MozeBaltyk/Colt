package app

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	"github.com/MozeBaltyk/Colt/internal/provider"
)

func TestLIST_001DefaultProviderListsRepositories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := storedProvider("personal")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	store := &recordingStore{credentials: map[string]credential.Credential{
		"github.com/personal": {Kind: "bearer_token", Secret: "secret"},
	}}
	client := &fakeClient{
		listRepos: []provider.Repository{
			{CloneURL: "https://github.com/user/demo.git"},
			{CloneURL: "https://github.com/user/other.git"},
		},
	}
	output, err := execute(t, &App{ConfigPath: path, Credentials: store, Git: &fakeGit{}, NewClient: func(config.Provider, string) (provider.Client, error) { return client, nil }}, "list")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(output, "https://github.com/user/demo.git") {
		t.Fatalf("expected repo URL in output, got %q", output)
	}
}

func TestLIST_002AllProvidersReportsFailuresIndependently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{
		"work":     storedProvider("work"),
		"personal": storedProvider("personal"),
	}}); err != nil {
		t.Fatal(err)
	}
	store := &recordingStore{credentials: map[string]credential.Credential{
		"github.com/work":     {Kind: "bearer_token", Secret: "secret"},
		"github.com/personal": {Kind: "bearer_token", Secret: "secret"},
	}}
	failingClient := &fakeClient{listErr: errors.New("provider request failed")}
	succeedingClient := &fakeClient{
		listRepos: []provider.Repository{{CloneURL: "https://github.com/user/demo.git"}},
	}
	clientIdx := 0
	output, err := execute(t, &App{ConfigPath: path, Credentials: store, Git: &fakeGit{}, NewClient: func(config.Provider, string) (provider.Client, error) {
		clientIdx++
		if clientIdx == 1 {
			return failingClient, nil
		}
		return succeedingClient, nil
	}}, "list", "--all")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(output, "provider request failed") {
		t.Fatalf("expected failure report in output, got %q", output)
	}
	if !strings.Contains(output, "https://github.com/user/demo.git") {
		t.Fatalf("expected successful provider result in output, got %q", output)
	}
}

func TestLIST_003UnknownProviderAliasFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{}}); err != nil {
		t.Fatal(err)
	}
	_, err := execute(t, testApp(path, "", &fakeGit{}, &fakeClient{}), "list", "--provider", "missing")
	if err == nil || !strings.Contains(err.Error(), "unknown provider alias") {
		t.Fatalf("expected unknown alias error, got %v", err)
	}
}

func TestLIST_004NoProvidersConfiguredFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{}}); err != nil {
		t.Fatal(err)
	}
	_, err := execute(t, testApp(path, "", &fakeGit{}, &fakeClient{}), "list")
	if err == nil {
		t.Fatal("expected error for no providers")
	}
}

func TestLIST_005NamespaceOverridesProviderNamespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := storedProvider("personal")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	store := &recordingStore{credentials: map[string]credential.Credential{
		"github.com/personal": {Kind: "bearer_token", Secret: "secret"},
	}}
	client := &fakeClient{listRepos: []provider.Repository{{CloneURL: "https://github.com/user/demo.git"}}}
	var got config.Provider
	_, err := execute(t, &App{ConfigPath: path, Credentials: store, Git: &fakeGit{}, NewClient: func(p config.Provider, _ string) (provider.Client, error) {
		got = p
		return client, nil
	}}, "list", "--namespace", "other")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if got.Namespace != "other" {
		t.Fatalf("namespace = %q", got.Namespace)
	}
}

func TestLIST_006NamespaceRejectsAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": storedProvider("personal")}}); err != nil {
		t.Fatal(err)
	}
	store := &recordingStore{credentials: map[string]credential.Credential{
		"github.com/personal": {Kind: "bearer_token", Secret: "secret"},
	}}
	_, err := execute(t, &App{ConfigPath: path, Credentials: store, Git: &fakeGit{}, NewClient: func(config.Provider, string) (provider.Client, error) { return &fakeClient{}, nil }}, "list", "--all", "--namespace", "other")
	if err == nil || !strings.Contains(err.Error(), "--namespace cannot be combined with --all") {
		t.Fatalf("expected namespace/all conflict, got %v", err)
	}
}

func TestLIST_007NamespaceFiltersListing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := storedProvider("personal")
	if err := config.Save(path, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	store := &recordingStore{credentials: map[string]credential.Credential{
		"github.com/personal": {Kind: "bearer_token", Secret: "secret"},
	}}
	client := &fakeClient{listRepos: []provider.Repository{
		{Name: "keep", Namespace: "keep", CloneURL: "https://github.com/keep/keep.git"},
		{Name: "skip", Namespace: "skip", CloneURL: "https://github.com/skip/skip.git"},
	}}
	output, err := execute(t, &App{ConfigPath: path, Credentials: store, Git: &fakeGit{}, NewClient: func(config.Provider, string) (provider.Client, error) { return client, nil }}, "list", "--namespace", "keep")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(output, "github.com/keep/keep.git") || strings.Contains(output, "github.com/skip/skip.git") {
		t.Fatalf("filtered output = %q", output)
	}
}
