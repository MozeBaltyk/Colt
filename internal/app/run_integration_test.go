//go:build colt_integration

package app

import (
	"context"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// DESTRUCTIVE, opt-in disposable-VM smoke test. Never run on a shared host.
// Both gates and an empty deployment directory are mandatory. Default go test
// does not compile this file; compiling with the tag alone still skips it.
func TestRUNIsolatedVMIntegration(t *testing.T) {
	if os.Getenv("COLT_RUN_ISOLATED_VM") != "destroy-disposable-vm" {
		t.Skip("requires explicit isolated-VM opt-in")
	}
	marker, err := os.ReadFile("/etc/colt/disposable-integration-vm")
	if err != nil || string(marker) != "destroy-disposable-vm\n" {
		t.Fatal("missing disposable VM marker; refusing host changes")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("requires root inside a disposable Linux systemd VM")
	}
	entries, err := os.ReadDir(runDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("existing deployments/locks found; refusing integration test")
	}
	for _, path := range deploymentPaths("gateau").all() {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("gateau or an ambiguous path exists; refusing integration test")
		}
	}
	for _, port := range []string{":3000", ":2222"} {
		listener, err := net.Listen("tcp", port)
		if err != nil {
			t.Fatal("deployment ports unavailable")
		}
		listener.Close()
	}
	h := nativeHost{}
	podman, err := h.LookPath("podman")
	if err != nil {
		t.Fatal(err)
	}
	for _, product := range []string{"gitea", "forgejo"} {
		t.Run(product, func(t *testing.T) {
			secret, err := randomSecret()
			if err != nil {
				t.Fatal(err)
			}
			name := "colt-smoke-" + strings.ToLower(secret[:10])
			name = strings.ReplaceAll(name, "_", "-")
			run := func(args ...string) error {
				ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
				defer cancel()
				cmd := (&App{Host: h}).Root()
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				cmd.SetArgs(append([]string{"run"}, args...))
				return cmd.ExecuteContext(ctx)
			}
			t.Cleanup(func() {
				if err := run("rm", name, "--volumes"); err != nil {
					t.Errorf("cleanup failed; preserve VM for recovery: %v", err)
				}
			})
			if err := run(product, name); err != nil {
				t.Fatal(err)
			}
			env, err := h.ReadFile(deploymentPaths(name).env)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			dbSQL := func(ctx context.Context, sql string) (string, error) {
				if product == "forgejo" {
					return h.RunOutput(ctx, podman, "exec", name+"-db", "psql", "-At", "-v", "ON_ERROR_STOP=1", "-U", "forgejo", "-d", "forgejo", "-c", sql)
				}
				return h.RunOutput(ctx, podman, "exec", name+"-db", "sh", "-ec", `exec mariadb --batch --skip-column-names -ugitea --password="$MARIADB_PASSWORD" gitea -e "$1"`, "colt-smoke", sql)
			}
			if _, err := dbSQL(ctx, "CREATE TABLE colt_smoke (value INTEGER); INSERT INTO colt_smoke VALUES (42);"); err != nil {
				t.Fatal(err)
			}
			// Real Git commit and push, stored on the application's data volume.
			if err := h.Run(ctx, podman, "exec", name+"-app", "sh", "-ec", `git init --bare /var/lib/gitea/colt-smoke.git; git init /tmp/colt-smoke; cd /tmp/colt-smoke; git -c user.name=Colt -c user.email=colt@example.invalid commit --allow-empty -m smoke; git push /var/lib/gitea/colt-smoke.git HEAD:refs/heads/smoke; printf smoke > /etc/gitea/colt-smoke`); err != nil {
				t.Fatal(err)
			}
			before, err := h.RunOutput(ctx, podman, "exec", name+"-app", "git", "--git-dir=/var/lib/gitea/colt-smoke.git", "rev-parse", "refs/heads/smoke")
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"stop", name}, {"start", name}, {product, name, "--replace"}, {"rm", name}, {product, name, "--replace"}} {
				if err := run(args...); err != nil {
					t.Fatal(err)
				}
			}
			ctx2, cancel2 := context.WithTimeout(context.Background(), time.Minute)
			defer cancel2()
			value, err := dbSQL(ctx2, "SELECT value FROM colt_smoke;")
			if err != nil || value != "42" {
				t.Fatal("database row did not persist")
			}
			after, err := h.RunOutput(ctx2, podman, "exec", name+"-app", "git", "--git-dir=/var/lib/gitea/colt-smoke.git", "rev-parse", "refs/heads/smoke")
			if err != nil || before != after {
				t.Fatal("repository commit did not persist")
			}
			config, err := h.RunOutput(ctx2, podman, "exec", name+"-app", "cat", "/etc/gitea/colt-smoke")
			if err != nil || config != "smoke" {
				t.Fatal("configuration volume did not persist")
			}
			retained, err := h.ReadFile(deploymentPaths(name).env)
			if err != nil || string(env) != string(retained) {
				t.Fatal("credentials changed")
			}
		})
	}
}
