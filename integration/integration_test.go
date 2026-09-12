//go:build integration

// Package integration exercises real Gitea and Forgejo backends and runs the
// tagged Gitea feature through the real Colt binary.
//
// Run: just test-integration (or go test -tags integration ./integration/).
// Requires a container tool (podman by default) and registry access to
// pull the backend images on first use. Ephemeral containers only; no
// volumes, no SSH, fixed loopback ports (overridable, see env below).
package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"
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
	containerSeq   atomic.Uint64
	credentialURL  = regexp.MustCompile(`(?i)(https?://[^:/[:space:]]+:)[^@[:space:]]+@`)
	secretHeader   = regexp.MustCompile(`(?i)((?:authorization|private-token):[[:space:]]*(?:basic|bearer|token)?[[:space:]]*)[^[:space:]]+`)
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// backend is one live forge instance under test.
type backend struct {
	name      string // gitea | forgejo
	image     string
	port      string
	cli       string // admin CLI binary inside the image
	baseURL   string
	token     string
	http      *http.Client
	container string
}

func TestMain(m *testing.M) {
	if _, err := exec.LookPath(containerTool); err != nil {
		fmt.Fprintf(os.Stderr, "integration: required container tool %q not on PATH\n", containerTool)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func startBackend(t *testing.T, name, image, port, cli string) *backend {
	return startBackendAt(t, name, image, port, cli, "http://localhost:"+port, http.DefaultClient)
}

func startBackendAt(t *testing.T, name, image, port, cli, baseURL string, hc *http.Client, extra ...string) *backend {
	t.Helper()
	container := fmt.Sprintf("colt-it-%s-%d-%d", name, time.Now().UnixNano(), containerSeq.Add(1))
	b := &backend{name: name, image: image, port: port, cli: cli,
		baseURL: baseURL, http: hc, container: container}
	run(t, "pull", "pull", image)
	args := []string{"run", "-d", "--name", container,
		"-p", "127.0.0.1:" + port + ":3000",
		"-e", "GITEA__database__DB_TYPE=sqlite3",
		"-e", "GITEA__database__PATH=/data/gitea/gitea.db",
		"-e", "GITEA__security__INSTALL_LOCK=true",
		"-e", "GITEA__server__DOMAIN=localhost",
		"-e", "GITEA__server__HTTP_PORT=3000",
		"-e", "GITEA__server__ROOT_URL=" + baseURL + "/",
		"-e", "GITEA__server__DISABLE_SSH=true",
		"-e", "GITEA__service__DISABLE_REGISTRATION=true",
	}
	args = append(args, extra...)
	args = append(args, image)
	run(t, "run", args...)
	t.Cleanup(func() {
		if keepContainers {
			t.Logf("retained owned container %q", b.container)
			return
		}
		output, err := exec.Command(containerTool, "rm", "-f", b.container).CombinedOutput()
		if err != nil {
			t.Errorf("cleanup owned container %q: %v\n%s", b.container, err, redact(string(output), b.token))
		}
	})
	deadline := time.Now().Add(90 * time.Second)
	for {
		if _, err := doWithClient(t, hc, http.MethodGet, b.baseURL+"/api/v1/version", "", nil, nil); err == nil {
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
	runWithEnv(t, "admin", []string{"COLT_IT_PASSWORD=" + adminPass},
		"exec", "--user", "git", "--env", "COLT_IT_PASSWORD", b.container,
		"sh", "-c", `exec "$1" admin user create --admin --username "$2" --password "$COLT_IT_PASSWORD" --email "$3" --must-change-password=false`,
		"colt-admin", b.cli, adminUser, adminUser+"@example.invalid")
	var out struct {
		SHA1 string `json:"sha1"`
	}
	postWithClient(t, b.http, b.baseURL+"/api/v1/users/"+adminUser+"/tokens", adminUser+":"+adminPass, map[string]any{
		"name":   "colt-it",
		"scopes": []string{"read:user", "write:user", "read:repository", "write:repository"},
	}, &out)
	if out.SHA1 == "" {
		t.Fatalf("%s: token mint returned no secret", b.name)
	}
	b.token = out.SHA1
}

func run(t *testing.T, what string, args ...string) string {
	return runWithEnv(t, what, nil, args...)
}

func runWithEnv(t *testing.T, what string, extraEnv []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, containerTool, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %s failed: %v\n%s", what, containerTool, err, redact(buf.String()))
	}
	return buf.String()
}

func redact(text string, secrets ...string) string {
	secrets = append(secrets, adminPass)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		text = strings.ReplaceAll(text, secret, "[REDACTED]")
		text = strings.ReplaceAll(text, base64.StdEncoding.EncodeToString([]byte(secret)), "[REDACTED]")
		if _, password, ok := strings.Cut(secret, ":"); ok && password != "" {
			text = strings.ReplaceAll(text, password, "[REDACTED]")
		}
	}
	text = credentialURL.ReplaceAllString(text, `${1}[REDACTED]@`)
	return secretHeader.ReplaceAllString(text, `${1}[REDACTED]`)
}

func TestFailureRedaction(t *testing.T) {
	const token = "integration-api-token"
	got := redact("password="+adminPass+" https://user:"+token+"@example.invalid/repo Authorization: Bearer unknown-token", token)
	for _, secret := range []string{adminPass, token, "unknown-token"} {
		if strings.Contains(got, secret) {
			t.Fatal("redaction retained a protected value")
		}
	}
}

func do(t *testing.T, method, url, auth string, body any, result any) (int, error) {
	t.Helper()
	return requestWithClient(t, http.DefaultClient, method, url, auth, body, result)
}

func requestWithClient(t *testing.T, hc *http.Client, method, url, auth string, body any, result any) (int, error) {
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
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(redact(string(data), auth)))
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			t.Fatalf("decode %s %s: %v\n%s", method, url, err, redact(string(data), auth))
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
	postWithClient(t, http.DefaultClient, url, auth, body, result)
}

func postWithClient(t *testing.T, hc *http.Client, url, auth string, body any, result any) {
	t.Helper()
	if _, err := doWithClient(t, hc, http.MethodPost, url, auth, body, result); err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
}

func doWithClient(t *testing.T, hc *http.Client, method, url, auth string, body any, result any) (int, error) {
	t.Helper()
	return requestWithClient(t, hc, method, url, auth, body, result)
}

func git(t *testing.T, dir string, args ...string) string {
	return gitWithEnv(t, dir, nil, nil, args...)
}

func gitWithEnv(t *testing.T, dir string, env, secrets []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("git failed: %v\n%s", err, redact(buf.String(), secrets...))
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
			askpass := filepath.Join(workdir, "askpass.sh")
			if err := os.WriteFile(askpass, []byte("#!/bin/sh\ncase \"$1\" in *Username*) printf '%s\\n' \"$COLT_IT_GIT_USER\";; *) printf '%s\\n' \"$COLT_IT_GIT_PASSWORD\";; esac\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			gitEnv := []string{"GIT_ASKPASS=" + askpass, "GIT_TERMINAL_PROMPT=0", "COLT_IT_GIT_USER=" + adminUser, "COLT_IT_GIT_PASSWORD=" + b.token}
			cloneURL := fmt.Sprintf("http://localhost:%s/%s/%s.git", b.port, adminUser, repo)
			gitWithEnv(t, "", gitEnv, []string{b.token}, "clone", cloneURL, workdir+"/repo")
			dir := workdir + "/repo"
			gitWithEnv(t, dir, gitEnv, []string{b.token}, "-c", "user.name=colt-it", "-c", "user.email=colt-it@example.invalid",
				"commit", "--allow-empty", "-m", "integration probe")
			gitWithEnv(t, dir, gitEnv, []string{b.token}, "push", "origin", "HEAD:"+created.DefaultBranch)
			// ponytail: verify over the Git transport itself. The
			// /repos/{owner}/{repo}/branches list endpoint returns null
			// on these backends even with the branch present (single-ref
			// /git/refs/heads/<branch> works), so ls-remote is the honest
			// proof and the one future adapters should prefer.
			head := git(t, dir, "rev-parse", "HEAD")
			remote := gitWithEnv(t, dir, gitEnv, []string{b.token}, "ls-remote", "origin", "refs/heads/"+created.DefaultBranch)
			if !strings.HasPrefix(remote, head) {
				t.Fatalf("ls-remote = %q, want prefix %q", remote, head)
			}
		})
	}
}

type giteaWorld struct {
	t       *testing.T
	backend *backend
	binary  string
	dir     string
	config  string
	ca      string
	output  string
}

func TestGiteaVerticalSlice(t *testing.T) {
	w := &giteaWorld{t: t}
	suite := godog.TestSuite{
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			ctx.Step(`^an ephemeral Gitea provider serving trusted HTTPS$`, w.start)
			ctx.Step(`^the real Colt binary initializes project "([^"]*)" through that provider$`, w.initialize)
			ctx.Step(`^Gitea contains the initial commit on branch "([^"]*)"$`, w.remoteHasCommit)
			ctx.Step(`^the local origin and Gitea credential helper are configured without storing the token$`, w.safeLocalConfig)
		},
		Options: &godog.Options{
			Format: "pretty", Paths: []string{"../features/provider_expansion.feature"},
			Tags: "@gitea", Strict: true, Concurrency: 1, TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("Gitea integration feature failed")
	}
}

func (w *giteaWorld) start() error {
	w.dir = w.t.TempDir()
	certDir := filepath.Join(w.dir, "certs")
	// The image drops to its git user before opening the bind-mounted certs.
	// The parent TempDir remains 0700 on the host; this mount is test-only.
	if err := os.Mkdir(certDir, 0o755); err != nil {
		return err
	}
	ca, err := writeLocalCertificates(certDir)
	if err != nil {
		return err
	}
	w.ca = ca
	pool := x509.NewCertPool()
	pemBytes, err := os.ReadFile(ca)
	if err != nil || !pool.AppendCertsFromPEM(pemBytes) {
		return fmt.Errorf("load integration CA: %w", err)
	}
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}, Timeout: 30 * time.Second}
	b := startBackendAt(w.t, "gitea", giteaImage, giteaPort, "gitea", "https://localhost:"+giteaPort, hc,
		"-v", certDir+":/certs:ro,Z",
		"-e", "GITEA__server__PROTOCOL=https",
		"-e", "GITEA__server__CERT_FILE=/certs/server.crt",
		"-e", "GITEA__server__KEY_FILE=/certs/server.key")
	b.bootstrap(w.t)
	w.backend = b
	w.config = filepath.Join(w.dir, "config.yaml")
	w.binary = filepath.Join(w.dir, "bin", "colt")
	if err := os.Mkdir(filepath.Dir(w.binary), 0o700); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-o", w.binary, "./cmd/colt")
	cmd.Dir = filepath.Clean("..")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build Colt: %v: %s", err, output)
	}
	config := fmt.Sprintf("providers:\n  live:\n    type: gitea\n    host: localhost:%s\n    base_url: %s\n    namespace: %s\n    visibility: private\n    git_name: Colt Integration\n    git_email: colt@example.invalid\n    auth:\n      source: env\n      token_env: GITEA_TOKEN\n    default: true\n", b.port, b.baseURL, adminUser)
	if err := os.WriteFile(w.config, []byte(config), 0o600); err != nil {
		return err
	}
	w.t.Setenv("GITEA_TOKEN", b.token)
	w.t.Setenv("COLT_CONFIG", w.config)
	w.t.Setenv("SSL_CERT_FILE", w.ca)
	w.t.Setenv("CURL_CA_BUNDLE", w.ca)
	w.t.Setenv("HOME", filepath.Join(w.dir, "home"))
	w.t.Setenv("PATH", filepath.Dir(w.binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	w.t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	w.t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	w.t.Setenv("GIT_TERMINAL_PROMPT", "0")
	w.t.Setenv("NO_PROXY", "localhost,127.0.0.1")
	w.t.Setenv("no_proxy", "localhost,127.0.0.1")
	return os.Mkdir(os.Getenv("HOME"), 0o700)
}

func (w *giteaWorld) initialize(project string) error {
	cmd := exec.Command(w.binary, "init", project)
	cmd.Dir = w.dir
	output, err := cmd.CombinedOutput()
	w.output = string(output)
	if err != nil {
		return fmt.Errorf("colt init failed: %v: %s", err, redact(string(output), w.backend.token))
	}
	if strings.Contains(w.output, w.backend.token) {
		return errors.New("Colt output exposed the Gitea token")
	}
	return nil
}

func (w *giteaWorld) remoteHasCommit(branch string) error {
	repo := filepath.Join(w.dir, "demo")
	head := git(w.t, repo, "rev-parse", "HEAD")
	remote := git(w.t, repo, "-c", "http.sslCAInfo="+w.ca, "ls-remote", "origin", "refs/heads/"+branch)
	if !strings.HasPrefix(remote, head+"\t") {
		return fmt.Errorf("remote branch = %q, want commit %s", remote, head)
	}
	return nil
}

func (w *giteaWorld) safeLocalConfig() error {
	repo := filepath.Join(w.dir, "demo")
	origin := git(w.t, repo, "remote", "get-url", "origin")
	helpers := git(w.t, repo, "config", "--local", "--get-all", "credential.helper")
	username := git(w.t, repo, "config", "--local", "--get", "credential."+origin+".username")
	data, err := os.ReadFile(filepath.Join(repo, ".git", "config"))
	if err != nil {
		return err
	}
	if origin != w.backend.baseURL+"/"+adminUser+"/demo.git" || strings.Contains(origin, "@") {
		return fmt.Errorf("unsafe or unexpected origin %q", redact(origin, w.backend.token))
	}
	if !strings.Contains(helpers, "!colt git-credential") || username != adminUser {
		return fmt.Errorf("helper=%q username=%q", helpers, username)
	}
	if bytes.Contains(data, []byte(w.backend.token)) || strings.Contains(w.output, w.backend.token) {
		return errors.New("Gitea token was stored or printed")
	}
	return nil
}

type forgejoWorld struct {
	t       *testing.T
	backend *backend
	binary  string
	dir     string
	config  string
	ca      string
	output  string
}

func TestForgejoVerticalSlice(t *testing.T) {
	w := &forgejoWorld{t: t}
	suite := godog.TestSuite{
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			ctx.Step(`^an ephemeral Forgejo provider serving trusted HTTPS$`, w.start)
			ctx.Step(`^the real Colt binary initializes project "([^"]*)" through that provider$`, w.initialize)
			ctx.Step(`^Forgejo contains the initial commit on branch "([^"]*)"$`, w.remoteHasCommit)
			ctx.Step(`^the local origin and Forgejo credential helper are configured without storing the token$`, w.safeLocalConfig)
		},
		Options: &godog.Options{
			Format: "pretty", Paths: []string{"../features/provider_expansion.feature"},
			Tags: "@forgejo", Strict: true, Concurrency: 1, TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("Forgejo integration feature failed")
	}
}

func (w *forgejoWorld) start() error {
	w.dir = w.t.TempDir()
	certDir := filepath.Join(w.dir, "certs")
	if err := os.Mkdir(certDir, 0o755); err != nil {
		return err
	}
	ca, err := writeLocalCertificates(certDir)
	if err != nil {
		return err
	}
	w.ca = ca
	pool := x509.NewCertPool()
	pemBytes, err := os.ReadFile(ca)
	if err != nil || !pool.AppendCertsFromPEM(pemBytes) {
		return fmt.Errorf("load integration CA: %w", err)
	}
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}, Timeout: 30 * time.Second}
	b := startBackendAt(w.t, "forgejo", forgejoImage, forgejoPort, "forgejo", "https://localhost:"+forgejoPort, hc,
		"-v", certDir+":/certs:ro,Z",
		"-e", "FORGEJO__server__PROTOCOL=https",
		"-e", "FORGEJO__server__CERT_FILE=/certs/server.crt",
		"-e", "FORGEJO__server__KEY_FILE=/certs/server.key")
	b.bootstrap(w.t)
	w.backend = b
	w.config = filepath.Join(w.dir, "config.yaml")
	w.binary = filepath.Join(w.dir, "bin", "colt")
	if err := os.Mkdir(filepath.Dir(w.binary), 0o700); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-o", w.binary, "./cmd/colt")
	cmd.Dir = filepath.Clean("..")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build Colt: %v: %s", err, output)
	}
	config := fmt.Sprintf("providers:\n  live:\n    type: forgejo\n    host: localhost:%s\n    base_url: %s\n    namespace: %s\n    visibility: private\n    git_name: Colt Integration\n    git_email: colt@example.invalid\n    auth:\n      source: env\n      token_env: FORGEJO_TOKEN\n    default: true\n", b.port, b.baseURL, adminUser)
	if err := os.WriteFile(w.config, []byte(config), 0o600); err != nil {
		return err
	}
	w.t.Setenv("FORGEJO_TOKEN", b.token)
	w.t.Setenv("COLT_CONFIG", w.config)
	w.t.Setenv("SSL_CERT_FILE", w.ca)
	w.t.Setenv("CURL_CA_BUNDLE", w.ca)
	w.t.Setenv("HOME", filepath.Join(w.dir, "home"))
	w.t.Setenv("PATH", filepath.Dir(w.binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	w.t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	w.t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	w.t.Setenv("GIT_TERMINAL_PROMPT", "0")
	w.t.Setenv("NO_PROXY", "localhost,127.0.0.1")
	w.t.Setenv("no_proxy", "localhost,127.0.0.1")
	return os.Mkdir(os.Getenv("HOME"), 0o700)
}

func (w *forgejoWorld) initialize(project string) error {
	cmd := exec.Command(w.binary, "init", project)
	cmd.Dir = w.dir
	output, err := cmd.CombinedOutput()
	w.output = string(output)
	if err != nil {
		return fmt.Errorf("colt init failed: %v: %s", err, redact(string(output), w.backend.token))
	}
	if strings.Contains(w.output, w.backend.token) {
		return errors.New("Colt output exposed the Forgejo token")
	}
	return nil
}

func (w *forgejoWorld) remoteHasCommit(branch string) error {
	repo := filepath.Join(w.dir, "demo")
	head := git(w.t, repo, "rev-parse", "HEAD")
	remote := git(w.t, repo, "-c", "http.sslCAInfo="+w.ca, "ls-remote", "origin", "refs/heads/"+branch)
	if !strings.HasPrefix(remote, head+"\t") {
		return fmt.Errorf("remote branch = %q, want commit %s", remote, head)
	}
	return nil
}

func (w *forgejoWorld) safeLocalConfig() error {
	repo := filepath.Join(w.dir, "demo")
	origin := git(w.t, repo, "remote", "get-url", "origin")
	helpers := git(w.t, repo, "config", "--local", "--get-all", "credential.helper")
	username := git(w.t, repo, "config", "--local", "--get", "credential."+origin+".username")
	data, err := os.ReadFile(filepath.Join(repo, ".git", "config"))
	if err != nil {
		return err
	}
	if origin != w.backend.baseURL+"/"+adminUser+"/demo.git" || strings.Contains(origin, "@") {
		return fmt.Errorf("unsafe or unexpected origin %q", redact(origin, w.backend.token))
	}
	if !strings.Contains(helpers, "!colt git-credential") || username != adminUser {
		return fmt.Errorf("helper=%q username=%q", helpers, username)
	}
	if bytes.Contains(data, []byte(w.backend.token)) || strings.Contains(w.output, w.backend.token) {
		return errors.New("Forgejo token was stored or printed")
	}
	return nil
}

func writeLocalCertificates(dir string) (string, error) {
	now := time.Now()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Colt integration CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return "", err
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	serverTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		return "", err
	}
	caPath := filepath.Join(dir, "ca.crt")
	if err := writePEM(caPath, "CERTIFICATE", caDER, 0o644); err != nil {
		return "", err
	}
	if err := writePEM(filepath.Join(dir, "server.crt"), "CERTIFICATE", serverDER, 0o644); err != nil {
		return "", err
	}
	if err := writePEM(filepath.Join(dir, "server.key"), "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey), 0o644); err != nil {
		return "", err
	}
	return caPath, nil
}

func writePEM(path, kind string, data []byte, mode os.FileMode) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data}), mode)
}
