package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testAsset() string {
	name := "colt-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// newReleaseServer serves /releases/latest (redirect), checksums.txt, and the
// platform asset. When tamper is true the published checksum is wrong.
func newReleaseServer(t *testing.T, tag string, binary []byte, tamper bool) *httptest.Server {
	t.Helper()
	asset := testAsset()
	sum := sha256.Sum256(binary)
	if tamper {
		sum[0] ^= 0xff
	}
	checksums := hex.EncodeToString(sum[:]) + "  " + asset + "\n"
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/"+tag, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(checksums))
	})
	mux.HandleFunc("/releases/download/"+tag+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(binary)
	})
	return httptest.NewServer(mux)
}

func testClient(srv *httptest.Server, target, version string) *Client {
	return &Client{
		HTTP:        &http.Client{},
		BaseURL:     srv.URL,
		GOOS:        runtime.GOOS,
		GOARCH:      runtime.GOARCH,
		CurrentPath: target,
		Version:     version,
	}
}

func TestLatestResolvesTag(t *testing.T) {
	srv := newReleaseServer(t, "v0.5.0", []byte("x"), false)
	defer srv.Close()
	c := testClient(srv, filepath.Join(t.TempDir(), "colt"), "v0.4.0")
	tag, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.5.0" {
		t.Fatalf("tag = %q", tag)
	}
}

func TestInstallReplacesBinaryAtomically(t *testing.T) {
	binary := []byte("#!/bin/sh\necho v0.5.0\n")
	srv := newReleaseServer(t, "v0.5.0", binary, false)
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "colt")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := testClient(srv, target, "v0.4.0")
	tag, err := c.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.5.0" {
		t.Fatalf("tag = %q", tag)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(binary) {
		t.Fatalf("target not replaced: %q", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestInstallChecksumMismatchLeavesOriginalIntact(t *testing.T) {
	srv := newReleaseServer(t, "v0.5.0", []byte("tampered"), true)
	defer srv.Close()

	dir := t.TempDir()
	target := filepath.Join(dir, "colt")
	if err := os.WriteFile(target, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := testClient(srv, target, "v0.4.0")
	if _, err := c.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("error = %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("original modified: %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".colt-update-") {
			t.Fatalf("leftover temp file %q", e.Name())
		}
	}
}

func TestInstallAlreadyCurrent(t *testing.T) {
	srv := newReleaseServer(t, "v0.5.0", []byte("x"), false)
	defer srv.Close()
	c := testClient(srv, filepath.Join(t.TempDir(), "colt"), "v0.5.0")
	if _, err := c.Install(context.Background()); err == nil {
		t.Fatal("expected already-current error")
	} else if _, ok := AlreadyCurrent(err); !ok {
		t.Fatalf("not recognized as already current: %v", err)
	}
}

func TestMakeReleaseURLs(t *testing.T) {
	c := &Client{BaseURL: DefaultBaseURL, GOOS: "windows", GOARCH: "amd64"}
	if got, want := c.assetName(), "colt-windows-amd64.exe"; got != want {
		t.Fatalf("asset = %q, want %q", got, want)
	}
}
