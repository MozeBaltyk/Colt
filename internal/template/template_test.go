package template

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/MozeBaltyk/Colt/internal/config"
)

func TestPrepareAndMaterializeDataOnlyTemplate(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "{{owner}}"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "{{owner}}", "README.md"), []byte("owner={{owner}} license={{license}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".git", "config"), []byte("remote-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	declarations := map[string]config.TemplateParameter{"owner": {Required: true}, "license": {}}
	plan, err := Prepare(source, digest, declarations, map[string]string{"owner": "platform", "license": "MIT"})
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	root, err := OpenDestination(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := plan.MaterializeRoot(root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "platform", "README.md"))
	if err != nil || string(data) != "owner=platform license=MIT\n" {
		t.Fatalf("materialized data=%q error=%v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(destination, ".git")); !os.IsNotExist(err) {
		t.Fatalf("source Git metadata imported: %v", err)
	}
}

func TestMaterializeRootStaysOnOpenedDirectoryAfterPathSwap(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "destination")
	outside := filepath.Join(parent, "outside")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := OpenDestination(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	pinned := destination + ".pinned"
	if err := os.Rename(destination, pinned); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, destination); err != nil {
		t.Fatal(err)
	}
	plan := Plan{Entries: []Entry{{Path: "confined.txt", Mode: 0o644, Data: []byte("safe")}}}
	if err := plan.MaterializeRoot(root); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(pinned, "confined.txt")); err != nil || string(data) != "safe" {
		t.Fatalf("opened destination was not used: data=%q error=%v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "confined.txt")); !os.IsNotExist(err) {
		t.Fatalf("write escaped through swapped path: %v", err)
	}
}

func TestSafeModesArePinnedDespiteUmask(t *testing.T) {
	source := t.TempDir()
	dir := filepath.Join(source, "public")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "data")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Prepare(source, digest, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	root, err := OpenDestination(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	if err := plan.MaterializeRoot(root); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{filepath.Join(destination, "public"): 0o755, filepath.Join(destination, "public", "data"): 0o644} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("mode %s=%v, want %04o", path, info.Mode().Perm(), want)
		}
	}
}

func TestDigestRejectsUnsafeModesAndBounds(t *testing.T) {
	for _, mode := range []os.FileMode{0o755, 0o664} {
		t.Run(mode.String(), func(t *testing.T) {
			source := t.TempDir()
			path := filepath.Join(source, "file")
			if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			if _, err := Digest(source); err == nil || !strings.Contains(err.Error(), "mode") {
				t.Fatalf("unsafe mode %04o error=%v", mode, err)
			}
		})
	}
	t.Run("file size", func(t *testing.T) {
		source := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, "large"), make([]byte, MaxSourceFile+1), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Digest(source); err == nil || !strings.Contains(err.Error(), "size") {
			t.Fatalf("oversized source error=%v", err)
		}
	})
	t.Run("entry count", func(t *testing.T) {
		source := t.TempDir()
		for i := 0; i <= MaxEntries; i++ {
			name := filepath.Join(source, fmt.Sprintf("%04d", i))
			if err := os.WriteFile(name, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := Digest(source); err == nil || !strings.Contains(err.Error(), "entry count") {
			t.Fatalf("entry bound error=%v", err)
		}
	})
}

func TestPrepareRejectsUnsafeSourcesAndInterpolation(t *testing.T) {
	stringPtr := func(value string) *string { return &value }
	tests := []struct {
		name, file, content, value, want string
		link                             bool
	}{
		{name: "digest mismatch", file: "file", content: "ok", want: "digest mismatch"},
		{name: "unknown expression", file: "file", content: "{{unknown}}", value: "x", want: "unknown or arbitrary"},
		{name: "malformed expression", file: "file", content: "{{owner", value: "x", want: "malformed"},
		{name: "unsafe interpolated path", file: "{{owner}}", content: "ok", value: "../escape", want: "unsafe"},
		{name: "symlink", file: "link", content: "", link: true, want: "symlink or special"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := t.TempDir()
			path := filepath.Join(source, tc.file)
			if tc.link {
				if err := os.Symlink("outside", path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			digest, digestErr := Digest(source)
			if tc.link {
				if digestErr == nil || !strings.Contains(digestErr.Error(), tc.want) {
					t.Fatalf("Digest error=%v", digestErr)
				}
				return
			}
			if digestErr != nil {
				t.Fatal(digestErr)
			}
			if tc.name == "digest mismatch" {
				digest = "sha256:" + strings.Repeat("0", 64)
			}
			_, err := Prepare(source, digest, map[string]config.TemplateParameter{"owner": {Default: stringPtr("x")}}, map[string]string{"owner": tc.value})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Prepare error=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestPrepareRejectsInterpolatedPathCollision(t *testing.T) {
	source := t.TempDir()
	for _, name := range []string{"{{a}}", "{{b}}"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	declarations := map[string]config.TemplateParameter{"a": {}, "b": {}}
	if _, err := Prepare(source, digest, declarations, map[string]string{"a": "same", "b": "same"}); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("collision error=%v", err)
	}
}
