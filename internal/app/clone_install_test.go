package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	"github.com/MozeBaltyk/Colt/internal/provider"
)

func TestInstallClonePreservesFilesLinksAndExecutableMode(t *testing.T) {
	work := t.TempDir()
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "bin"), 0o751); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(source, "bin", "run")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o751); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("bin/run", filepath.Join(source, "run")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(work)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := installClone(root, source, "demo"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(work, "demo", "bin", "run")); err != nil || string(data) != "#!/bin/sh\n" {
		t.Fatalf("copied file = %q, %v", data, err)
	}
	if info, err := os.Stat(filepath.Join(work, "demo", "bin", "run")); err != nil || info.Mode().Perm() != 0o751 {
		t.Fatalf("copied mode = %v, %v", info, err)
	}
	if target, err := os.Readlink(filepath.Join(work, "demo", "run")); err != nil || target != "bin/run" {
		t.Fatalf("copied link = %q, %v", target, err)
	}
}

func TestCloneDestinationRejectsUnsafeEntries(t *testing.T) {
	work := t.TempDir()
	root, err := os.OpenRoot(work)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Mkdir(filepath.Join(work, "full"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "full", "data"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(work, "link")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../escape", filepath.Join(work, "absolute"), "full", "link"} {
		if _, _, err := validateCloneDestination(root, name); err == nil {
			t.Errorf("validateCloneDestination(%q) succeeded", name)
		}
	}
}

func TestCloneWorkRootRenameAndSymlinkSwapCannotRedirectWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("renaming an open directory is not supported by Windows")
	}
	parent := t.TempDir()
	work := filepath.Join(parent, "work")
	moved := filepath.Join(parent, "moved")
	outside := filepath.Join(parent, "outside")
	for _, dir := range []string{work, outside} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(parent, "config.yaml")
	p := storedProvider("personal")
	if err := config.Save(configPath, config.Config{Providers: map[string]config.Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeGit{cloneHook: func(string) error {
		if err := os.Rename(work, moved); err != nil {
			return err
		}
		return os.Symlink(outside, work)
	}}
	store := &recordingStore{credentials: map[string]credential.Credential{
		"github.com/personal": {Kind: "bearer_token", Secret: "secret"},
	}}
	client := &fakeClient{found: &provider.Repository{CloneURL: "https://github.com/octocat/demo.git"}}
	_, err := execute(t, &App{ConfigPath: configPath, WorkDir: work, Credentials: store, Git: runner, NewClient: func(config.Provider, string) (provider.Client, error) {
		return client, nil
	}}, "clone", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "unchanged" {
		t.Fatalf("outside sentinel = %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "demo")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("clone escaped opened root: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(moved, "demo", "README.md")); err != nil || !strings.Contains(string(data), "clone") {
		t.Fatalf("confined clone = %q, %v", data, err)
	}
}
