//go:build integration

// Package integration exercises real Gitea and Forgejo backends in
// containers: boot, admin bootstrap, token auth, repository API, and a
// git clone/push roundtrip over HTTPS. It characterizes backend behavior
// for the future provider adapters; it does not test Colt product code,
// which has no Gitea/Forgejo adapter yet.
//
// Run: just test-integration (or go test -tags integration ./integration/).
// Requires a container tool (podman by default) and registry access to
// pull the backend images on first use. Ephemeral containers only; no
// volumes, no SSH, fixed loopback ports (overridable, see env below).
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

var (
	containerTool  = envOr("CONTAINER_TOOL", "podman")
	giteaImage     = envOr("GITEA_IMAGE", "docker.io/gitea/gitea:1.24")
	forgejoImage   = envOr("FORGEJO_IMAGE", "codeberg.org/forgejo/forgejo:11")
	giteaPort      = envOr("GITEA_PORT", "13000")
	forgejoPort    = envOr("FORGEJO_PORT", "13001")
	adminUser      = envOr("COLT_IT_USER", "colt")
	adminPass      = envOr("COLT_IT_PASSWORD", "ColtIt2026pass")
	keepContainers = os.Getenv("COLT_IT_KEEP") == "1"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// backend is one live forge instance under test.
type backend struct {
	name    string // gitea | forgejo
	image   string
	port    string
	cli     string // admin CLI binary inside the image
	baseURL string
	token   string
}

func TestMain(m *testing.M) {
	if _, err := exec.LookPath(containerTool); err != nil {
		fmt.Fprintf(os.Stderr, "integration: container tool %q not on PATH, skipping\n", containerTool)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func startBackend(t *testing.T, name, image, port, cli string) *backend {
	t.Helper()
	b := &backend{name: name, image: image, port: port, cli: cli,
		baseURL: "http://localhost:" + port}
	run(t, "pull", "pull", image)
	run(t, "rm", "rm", "-f", "colt-it-"+name)
	args := []string{"run", "-d", "--name", "colt-it-" + name,
		"-p", port + ":3000",
		"-e", "GITEA__database__DB_TYPE=sqlite3",
		"-e", "GITEA__database__PATH=/data/gitea/gitea.db",
		"-e", "GITEA__security__INSTALL_LOCK=true",
		"-e", "GITEA__server__DOMAIN=localhost",
		"-e", "GITEA__server__HTTP_PORT=3000",
		"-e", "GITEA__server__ROOT_URL=http://localhost:" + port + "/",
		"-e", "GITEA__server__DISABLE_SSH=true",
		"-e", "GITEA__service__DISABLE_REGISTRATION=true",
		image,
	}
	run(t, "run", args...)
	if !keepContainers {
		t.Cleanup(func() { exec.Command(containerTool, "rm", "-f", "colt-it-"+name).Run() })
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		if get(t, b.baseURL+"/api/v1/version", "", nil) == nil {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not ready within 90s", name)
		}
		time.Sleep(2 * time.Second)
	}
}

func (b *backend) bootstrap(t *testing.T) {
	t.Helper()
	// ponytail: exec the admin CLI as the image's git user; as root the
	// gitea/forgejo binary refuses to run under rootless podman.
	run(t, "admin", "exec", "--user", "git", "colt-it-"+b.name,
		b.cli, "admin", "user", "create",
		"--admin", "--username", adminUser, "--password", adminPass,
		"--email", adminUser+"@example.invalid", "--must-change-password=false")
	var out struct {
		SHA1 string `json:"sha1"`
	}
	post(t, b.baseURL+"/api/v1/users/"+adminUser+"/tokens", adminUser+":"+adminPass, map[string]any{
		"name":   "colt-it",
		"scopes": []string{"read:user", "write:user", "read:repository", "write:repository"},
	}, &out)
	if out.SHA1 == "" {
		t.Fatalf("%s: token mint returned no secret", b.name)
	}
	b.token = out.SHA1
}

func run(t *testing.T, what string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, containerTool, args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %s %v: %v\n%s", what, containerTool, args, err, buf.String())
	}
	return buf.String()
}

func do(t *testing.T, method, url, auth string, body any, result any) (int, error) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.Contains(auth, ":") {
		user, pass, _ := strings.Cut(auth, ":")
		req.SetBasicAuth(user, pass)
	} else if auth != "" {
		req.Header.Set("Authorization", "token "+auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			t.Fatalf("decode %s %s: %v\n%s", method, url, err, data)
		}
	}
	return resp.StatusCode, nil
}

func get(t *testing.T, url, auth string, result any) error {
	t.Helper()
	_, err := do(t, http.MethodGet, url, auth, nil, result)
	return err
}

func post(t *testing.T, url, auth string, body any, result any) {
	t.Helper()
	if _, err := do(t, http.MethodPost, url, auth, body, result); err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, buf.String())
	}
	return strings.TrimSpace(buf.String())
}

func backends(t *testing.T) []*backend {
	t.Helper()
	bs := []*backend{
		startBackend(t, "gitea", giteaImage, giteaPort, "gitea"),
		startBackend(t, "forgejo", forgejoImage, forgejoPort, "forgejo"),
	}
	for _, b := range bs {
		b.bootstrap(t)
	}
	return bs
}

func TestBackendVersion(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			var v struct {
				Version string `json:"version"`
			}
			if err := get(t, b.baseURL+"/api/v1/version", "", &v); err != nil {
				t.Fatal(err)
			}
			if v.Version == "" {
				t.Fatal("empty version")
			}
			t.Logf("%s version %s", b.name, v.Version)
		})
	}
}

func TestBackendTokenAuth(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			var me struct {
				Login string `json:"login"`
			}
			if err := get(t, b.baseURL+"/api/v1/user", b.token, &me); err != nil {
				t.Fatal(err)
			}
			if me.Login != adminUser {
				t.Fatalf("login = %q, want %q", me.Login, adminUser)
			}
		})
	}
}

func TestBackendRepoRoundTrip(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			repo := fmt.Sprintf("probe-%d", time.Now().UnixNano())
			var created struct {
				FullName      string `json:"full_name"`
				DefaultBranch string `json:"default_branch"`
			}
			post(t, b.baseURL+"/api/v1/user/repos", b.token, map[string]any{
				"name": repo, "private": true, "auto_init": false,
			}, &created)
			if created.FullName != adminUser+"/"+repo {
				t.Fatalf("full_name = %q", created.FullName)
			}
			workdir := t.TempDir()
			cloneURL := fmt.Sprintf("http://%s:%s@localhost:%s/%s/%s.git",
				adminUser, b.token, b.port, adminUser, repo)
			git(t, "", "clone", cloneURL, workdir+"/repo")
			dir := workdir + "/repo"
			git(t, dir, "-c", "user.name=colt-it", "-c", "user.email=colt-it@example.invalid",
				"commit", "--allow-empty", "-m", "integration probe")
			git(t, dir, "push", "origin", "HEAD:"+created.DefaultBranch)
			// ponytail: verify over the Git transport itself. The
			// /repos/{owner}/{repo}/branches list endpoint returns null
			// on these backends even with the branch present (single-ref
			// /git/refs/heads/<branch> works), so ls-remote is the honest
			// proof and the one future adapters should prefer.
			head := git(t, dir, "rev-parse", "HEAD")
			remote := git(t, dir, "ls-remote", "origin", "refs/heads/"+created.DefaultBranch)
			if !strings.HasPrefix(remote, head) {
				t.Fatalf("ls-remote = %q, want prefix %q", remote, head)
			}
		})
	}
}
