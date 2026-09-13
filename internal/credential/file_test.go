package credential

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func secureTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFileStoreWritesVersionedDeterministicRestrictedFile(t *testing.T) {
	path := filepath.Join(secureTempDir(t), "private", "credentials")
	store := NewFileStore(path)
	for id, secret := range map[string]string{
		"github.com/work":     "work-secret",
		"github.com/personal": "personal-secret",
	} {
		if err := store.Put(id, Credential{Kind: "bearer_token", Secret: secret}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "version: 1\ncredentials:\n  github.com/personal:\n    kind: bearer_token\n    secret: personal-secret\n  github.com/work:\n    kind: bearer_token\n    secret: work-secret\n"
	if string(data) != want {
		t.Fatalf("credential YAML = %q, want %q", data, want)
	}
	for target, wantMode := range map[string]os.FileMode{filepath.Dir(path): 0o700, path: 0o600} {
		info, err := os.Stat(target)
		if err != nil || info.Mode().Perm() != wantMode {
			t.Fatalf("mode for %s = %v, %v; want %v", target, info.Mode().Perm(), err, wantMode)
		}
	}
	if err := store.Put("github.com/work", Credential{Kind: "bearer_token", Secret: "work-secret"}); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != want {
		t.Fatal("repeated write was not deterministic")
	}
}

func TestFileStoreReadsDeletesExactIDAndMissingFile(t *testing.T) {
	path := filepath.Join(secureTempDir(t), "credentials")
	store := NewFileStore(path)
	if _, err := store.Get("github.com/personal"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file Get = %v", err)
	}
	for _, id := range []string{"github.com/personal", "github.com/work"} {
		if err := store.Put(id, Credential{Kind: "bearer_token", Secret: id + "-secret"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Delete("github.com/personal"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("github.com/personal"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted credential Get = %v", err)
	}
	if got, err := store.Get("github.com/work"); err != nil || got.Secret != "github.com/work-secret" {
		t.Fatalf("neighbor = %+v, %v", got, err)
	}
	if err := store.Delete("github.com/personal"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestFileStoreRejectsUnsafeFilesAndNeverLeaksContents(t *testing.T) {
	secret := "plaintext-fake-secret-1"
	valid := "version: 1\ncredentials:\n  github.com/personal:\n    kind: bearer_token\n    secret: " + secret + "\n"
	tests := []struct {
		name string
		data string
		mode os.FileMode
	}{
		{"group-readable", valid, 0o640},
		{"world-readable", valid, 0o604},
		{"unknown-field", valid + "unknown: " + secret + "\n", 0o600},
		{"multiple-documents", valid + "---\nsecret: " + secret + "\n", 0o600},
		{"wrong-version", strings.Replace(valid, "version: 1", "version: 2", 1), 0o600},
		{"unsafe-id", strings.Replace(valid, "github.com/personal", "../"+secret, 1), 0o600},
		{"empty-kind", strings.Replace(valid, "bearer_token", "''", 1), 0o600},
		{"empty-secret", strings.Replace(valid, secret, "''", 1), 0o600},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(secureTempDir(t), "credentials")
			if err := os.WriteFile(path, []byte(tc.data), tc.mode); err != nil {
				t.Fatal(err)
			}
			_, err := NewFileStore(path).Get("github.com/personal")
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("unsafe file error = %v", err)
			}
		})
	}

	dir := secureTempDir(t)
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "credentials")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(link).Get("github.com/personal"); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestFileStoreRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(secureTempDir(t), "credentials")
	if err := os.WriteFile(path, []byte("version: 1\n#"+strings.Repeat("x", maxFileSize)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(path).Get("github.com/personal"); err == nil || !strings.Contains(err.Error(), "maximum size") {
		t.Fatalf("oversized error = %v", err)
	}
	if err := NewFileStore(filepath.Join(secureTempDir(t), "credentials")).Put("github.com/personal", Credential{Kind: "bearer_token", Secret: strings.Repeat("x", maxFileSize)}); err == nil || !strings.Contains(err.Error(), "maximum size") {
		t.Fatalf("oversized Put error = %v", err)
	}
}

func TestFileStoreNeverUsesConfigYAML(t *testing.T) {
	store := NewFileStore(filepath.Join(secureTempDir(t), "config.yaml"))
	if err := store.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "secret"}); err == nil {
		t.Fatal("FileStore accepted config.yaml")
	}
}

func TestFileStoreValidatesImmediateDirectory(t *testing.T) {
	t.Run("normal 0755 directory", func(t *testing.T) {
		dir := secureTempDir(t)
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		store := NewFileStore(filepath.Join(dir, "credentials"))
		if err := store.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "secret"}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get("github.com/personal"); err != nil {
			t.Fatalf("0755 directory rejected: %v", err)
		}
	})

	t.Run("group-writable directory", func(t *testing.T) {
		dir := secureTempDir(t)
		store := NewFileStore(filepath.Join(dir, "credentials"))
		if err := store.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "secret"}); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o770); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get("github.com/personal"); err == nil || !strings.Contains(err.Error(), "directory permissions") {
			t.Fatalf("unsafe directory error = %v", err)
		}
	})

	t.Run("symlink directory", func(t *testing.T) {
		root := secureTempDir(t)
		realDir := filepath.Join(root, "real")
		if err := os.Mkdir(realDir, 0o700); err != nil {
			t.Fatal(err)
		}
		realStore := NewFileStore(filepath.Join(realDir, "credentials"))
		if err := realStore.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "secret"}); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "linked")
		if err := os.Symlink(realDir, link); err != nil {
			t.Fatal(err)
		}
		if _, err := NewFileStore(filepath.Join(link, "credentials")).Get("github.com/personal"); err == nil || !strings.Contains(err.Error(), "real directory") {
			t.Fatalf("symlink directory error = %v", err)
		}
	})
}

func TestFileStoreLockIsExclusiveBoundedAndNeverFollowsSymlink(t *testing.T) {
	dir := secureTempDir(t)
	path := filepath.Join(dir, "credentials")
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path+".lock"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err := NewFileStore(path).Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "secret"})
	if err == nil || time.Since(started) > 2*lockTimeout {
		t.Fatalf("lock error=%v duration=%v", err, time.Since(started))
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != "unchanged" {
		t.Fatalf("symlink target changed: %q, %v", data, readErr)
	}
}

func TestFileStoreRemovesMutationLockAndRejectsCRLF(t *testing.T) {
	path := filepath.Join(secureTempDir(t), "credentials")
	store := NewFileStore(path)
	if err := store.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "safe"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock was not cleaned up: %v", err)
	}
	if err := store.Put("github.com/work", Credential{Kind: "bearer_token", Secret: "bad\nsecret"}); err == nil {
		t.Fatal("FileStore accepted bearer token with CR/LF")
	}
}
