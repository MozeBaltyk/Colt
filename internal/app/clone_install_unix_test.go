//go:build unix

package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestInstallCloneRejectsSpecialFileAndCleansPartialDestination(t *testing.T) {
	work := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "ordinary"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(source, "special"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(work)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := installClone(root, source, "demo"); err == nil || !strings.Contains(err.Error(), "special file") {
		t.Fatalf("installClone error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(work, "demo")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("partial destination remains: %v", err)
	}
	entries, err := os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("confined staging remains: %v", entries)
	}
}
