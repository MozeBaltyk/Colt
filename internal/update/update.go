package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultBaseURL is the release channel Colt updates from. It mirrors the
// asset naming used by install.sh and the release workflow.
const DefaultBaseURL = "https://github.com/MozeBaltyk/Colt"

// maxDownloadBytes bounds a single asset download so a compromised or
// misbehaving release channel cannot exhaust memory.
const maxDownloadBytes = 100 << 20

// Client fetches and installs Colt releases. Every field is overridable so
// tests can point it at a local release server and a throwaway target path.
type Client struct {
	HTTP        *http.Client
	BaseURL     string
	GOOS        string
	GOARCH      string
	CurrentPath string
	Version     string
}

// New builds a Client that updates the running binary from the default channel.
func New(currentVersion string) (*Client, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &Client{
		HTTP:        &http.Client{},
		BaseURL:     DefaultBaseURL,
		GOOS:        runtime.GOOS,
		GOARCH:      runtime.GOARCH,
		CurrentPath: exe,
		Version:     currentVersion,
	}, nil
}

func (c *Client) assetName() string {
	name := "colt-" + c.GOOS + "-" + c.GOARCH
	if c.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{}
}

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return c.BaseURL
}

// Latest resolves the newest release tag by following the /releases/latest
// redirect to /releases/tag/<tag>; the last path segment is the tag.
func (c *Client) Latest(ctx context.Context) (string, error) {
	resp, err := c.http().Get(c.baseURL() + "/releases/latest")
	if err != nil {
		return "", fmt.Errorf("resolve latest release: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.Request == nil || resp.Request.URL == nil {
		return "", errors.New("resolve latest release: missing redirect target")
	}
	tag := filepath.Base(resp.Request.URL.Path)
	if tag == "" || tag == "." || tag == "/" {
		return "", errors.New("resolve latest release: empty tag")
	}
	return tag, nil
}

// checksums fetches and parses checksums.txt (one "hex  filename" per line).
func (c *Client) checksums(ctx context.Context, tag string) (map[string]string, error) {
	resp, err := c.http().Get(c.baseURL() + "/releases/download/" + tag + "/checksums.txt")
	if err != nil {
		return nil, fmt.Errorf("fetch checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch checksums: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes))
	if err != nil {
		return nil, fmt.Errorf("fetch checksums: %w", err)
	}
	checksums := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		checksums[fields[1]] = strings.ToLower(fields[0])
	}
	if len(checksums) == 0 {
		return nil, errors.New("fetch checksums: empty checksums file")
	}
	return checksums, nil
}

// download fetches the named asset and bounds its size.
func (c *Client) download(ctx context.Context, tag, asset string) ([]byte, error) {
	resp, err := c.http().Get(c.baseURL() + "/releases/download/" + tag + "/" + asset)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", asset, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes))
}

// Install resolves the latest release, verifies its artifact against the
// published SHA-256, and atomically replaces the current binary. It returns the
// installed tag. On any failure the original binary is left intact.
func (c *Client) Install(ctx context.Context) (string, error) {
	if c.CurrentPath == "" {
		return "", errors.New("current binary path is unknown")
	}
	tag, err := c.Latest(ctx)
	if err != nil {
		return "", err
	}
	if tag == c.Version {
		return "", errAlreadyCurrent{tag}
	}
	asset := c.assetName()
	checksums, err := c.checksums(ctx, tag)
	if err != nil {
		return "", err
	}
	want, ok := checksums[asset]
	if !ok {
		return "", fmt.Errorf("release %s has no checksum for %s", tag, asset)
	}
	data, err := c.download(ctx, tag, asset)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return "", errors.New("downloaded artifact failed checksum verification")
	}
	if err := replaceFile(c.CurrentPath, data); err != nil {
		return "", err
	}
	return tag, nil
}

// errAlreadyCurrent marks the no-op upgrade path for callers that want to
// distinguish it from a real failure.
type errAlreadyCurrent struct{ tag string }

func (e errAlreadyCurrent) Error() string { return "already running the latest release " + e.tag }

// AlreadyCurrent reports whether err is the "already up to date" outcome and
// returns the version that is already installed.
func AlreadyCurrent(err error) (string, bool) {
	var e errAlreadyCurrent
	if errors.As(err, &e) {
		return e.tag, true
	}
	return "", false
}

// replaceFile writes data to a temporary file beside path and renames it into
// place so the replacement is atomic on the same filesystem. If the rename
// fails (for example a running executable on Windows) the original is untouched
// and the temporary file removed.
func replaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	mode := fs.FileMode(0o755)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".colt-update-*")
	if err != nil {
		return fmt.Errorf("prepare update: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(cause error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return cause
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(fmt.Errorf("write update: %w", err))
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(fmt.Errorf("set update mode: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		return fail(fmt.Errorf("sync update: %w", err))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close update: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace binary: %w", err)
	}
	return nil
}
