package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These are deterministic orchestration checks, not container acceptance tests.
func TestRUNRetainedLifecycle(t *testing.T) {
	for _, product := range []string{"gitea", "forgejo"} {
		t.Run(product, func(t *testing.T) {
			h := newFakeHost()
			a := &App{Host: h}
			p := deploymentPaths("personal")
			image := "registry.example/" + product + ":custom-rootless"
			key := "key with spaces \" and \\"
			if _, err := execute(t, a, "run", product, "personal", "--image", image, "--password", key); err != nil {
				t.Fatal(err)
			}
			env, owner := h.files[p.env], h.files[p.configDir+"/owner"]
			if _, err := execute(t, a, "run", "rm", "personal"); err != nil {
				t.Fatal(err)
			}
			if h.files[p.env] != env || h.files[p.configDir+"/owner"] != owner || h.modes[p.env] != 0o600 {
				t.Fatal("lost protected recovery state")
			}
			if _, err := execute(t, a, "run", product, "personal", "--replace"); err != nil {
				t.Fatal(err)
			}
			if h.files[p.env] != env || !strings.Contains(h.files[p.appUnit], image) {
				t.Fatal("replacement changed credentials or image")
			}
			if _, err := execute(t, a, "run", "rm", "personal"); err != nil {
				t.Fatal(err)
			}
			if _, err := execute(t, a, "run", "rm", "personal", "--volumes"); err != nil {
				t.Fatal(err)
			}
			if h.existing[p.configDir] {
				t.Fatal("purge retained credentials")
			}
			for name, exists := range h.resources {
				if exists {
					t.Fatalf("purge retained %s", name)
				}
			}
		})
	}
}

func TestRUNRejectRotationAndLegacyMutation(t *testing.T) {
	h := newFakeHost()
	a := deployFixture(t, h)
	if _, err := execute(t, a, "run", "gitea", "personal", "--replace", "--password", "different"); err == nil || !strings.Contains(err.Error(), "rotation") {
		t.Fatalf("rotation error: %v", err)
	}
	if len(h.mutations) != 0 {
		t.Fatal("rotation mutated host")
	}
	p := deploymentPaths("personal")
	delete(h.files, p.configDir+"/owner")
	for _, args := range [][]string{{"run", "gitea", "personal", "--replace"}, {"run", "rm", "personal", "--volumes"}, {"run", "start", "personal"}, {"run", "stop", "personal"}} {
		if _, err := execute(t, a, args...); err == nil || !strings.Contains(err.Error(), "legacy deployments are read-only") {
			t.Fatalf("legacy error: %v", err)
		}
	}
	if len(h.mutations) != 0 {
		t.Fatal("legacy deployment mutated")
	}
	if _, err := execute(t, a, "run", "status", "personal"); err != nil {
		t.Fatal(err)
	}
}

func TestRUNForeignResourceAndCreateRace(t *testing.T) {
	for _, kind := range []string{"network", "volume", "container"} {
		t.Run(kind, func(t *testing.T) {
			h := newFakeHost()
			a := deployFixture(t, h)
			owner, _ := deploymentOwner(h, deploymentPaths("personal"))
			r := podmanResource{kind, "personal-app"}
			if kind == "network" {
				r.name = "personal-net"
			}
			if kind == "volume" {
				r.name = "personal-db-" + owner
			}
			h.resources[kind+":"+r.name] = true
			h.labels[kind+":"+r.name] = "foreign"
			if _, err := execute(t, a, "run", "rm", "personal", "--volumes"); err == nil {
				t.Fatal("foreign resource adopted")
			}
			if len(h.mutations) != 0 || !h.resources[kind+":"+r.name] {
				t.Fatal("foreign resource mutated")
			}
		})
	}
	for _, createFails := range []bool{false, true} {
		h := newFakeHost()
		h.runHook = func(_ context.Context, _ string, args []string) error {
			if len(args) == 5 && args[1] == "create" {
				key := args[0] + ":" + args[4]
				h.resources[key] = true
				h.labels[key] = "foreign"
				if createFails {
					return errors.New("name won by another client")
				}
			}
			return nil
		}
		if _, err := execute(t, &App{Host: h}, "run", "forgejo", "personal"); err == nil {
			t.Fatal("race accepted")
		}
		if !h.resources["network:personal-net"] {
			t.Fatal("race deleted foreign network")
		}
		for _, cmd := range h.commands {
			if strings.Contains(cmd, "network rm") {
				t.Fatal("race attempted foreign removal")
			}
		}
	}
}

func TestRUNPartialRemovalAndInUseRecovery(t *testing.T) {
	h := newFakeHost()
	a := deployFixture(t, h)
	p := deploymentPaths("personal")
	h.failures["/usr/bin/podman network rm personal-net"] = errors.New("in use by unrelated container")
	h.resources["container:unrelated"] = true
	if _, err := execute(t, a, "run", "rm", "personal", "--volumes"); err == nil {
		t.Fatal("in-use removal succeeded")
	}
	if h.files[p.env] == "" || !h.resources["container:unrelated"] {
		t.Fatal("lost credentials or foreign consumer")
	}
	delete(h.failures, "/usr/bin/podman network rm personal-net")
	h.failures["remove:"+p.dbUnit] = errors.New("filesystem failure")
	if _, err := execute(t, a, "run", "rm", "personal"); err == nil {
		t.Fatal("partial remove succeeded")
	}
	if h.existing[p.appUnit] || h.files[p.env] == "" {
		t.Fatal("partial removal state incorrect")
	}
	delete(h.failures, "remove:"+p.dbUnit)
	if _, err := execute(t, a, "run", "rm", "personal", "--volumes"); err != nil {
		t.Fatal(err)
	}
	if !h.resources["container:unrelated"] {
		t.Fatal("removed unrelated container")
	}
}

func TestRUNVolumeReuseRaceAndImmutableRemoval(t *testing.T) {
	h := newFakeHost()
	foreignVolume := ""
	h.runHook = func(_ context.Context, _ string, args []string) error {
		if len(args) == 5 && args[0] == "volume" && args[1] == "create" {
			foreignVolume = args[4]
			h.resources["volume:"+foreignVolume] = true
			h.labels["volume:"+foreignVolume] = "foreign"
		}
		return nil
	}
	if _, err := execute(t, &App{Host: h}, "run", "gitea", "personal"); err == nil {
		t.Fatal("reused volume adopted")
	}
	if foreignVolume == "" || !h.resources["volume:"+foreignVolume] {
		t.Fatal("foreign volume removed")
	}
	for _, cmd := range h.commands {
		if strings.Contains(cmd, "volume rm") {
			t.Fatal("foreign volume deletion attempted")
		}
	}

	h = newFakeHost()
	owner := strings.Repeat("a", 43)
	h.resources["network:personal-net"] = true
	h.outputs[`/podman network inspect --format {{index .Labels "org.colt.deployment"}}|{{.ID}} personal-net`] = owner + "|original-id"
	h.runHook = func(_ context.Context, _ string, args []string) error {
		// Name now refers to a different object, after inspection but before removal.
		h.labels["network:personal-net"] = "foreign"
		if len(args) != 3 || args[2] != "original-id" {
			t.Fatal("removal used a mutable name")
		}
		return nil
	}
	if err := removeOwnedResource(context.Background(), h, "/podman", podmanResource{"network", "personal-net"}, owner); err != nil {
		t.Fatal(err)
	}
	if !h.resources["network:personal-net"] {
		t.Fatal("foreign replacement was deleted")
	}
}

func TestRUNRemovalWithMissingUnitsStopsOnlyOwnedContainers(t *testing.T) {
	h := newFakeHost()
	a := deployFixture(t, h)
	p := deploymentPaths("personal")
	for _, path := range []string{p.appUnit, p.dbUnit, p.networkUnit} {
		delete(h.files, path)
		h.existing[path] = false
	}
	if _, err := execute(t, a, "run", "rm", "personal", "--volumes"); err != nil {
		t.Fatal(err)
	}
	commands := strings.Join(h.commands, "\n")
	if !strings.Contains(commands, "container stop --time 10 personal-app") || !strings.Contains(commands, "container stop --time 10 personal-db") {
		t.Fatal("owned running remnants were not stopped")
	}
}

func TestRUNFailedReplacementWriteKeepsCredentials(t *testing.T) {
	h := newFakeHost()
	a := deployFixture(t, h)
	p := deploymentPaths("personal")
	env := h.files[p.env]
	h.writeErrs[p.env] = errors.New("disk full")
	if _, err := execute(t, a, "run", "gitea", "personal", "--replace"); err == nil {
		t.Fatal("failed write accepted")
	}
	if h.files[p.env] != env || h.files[p.configDir+"/owner"] == "" {
		t.Fatal("lost protected credentials")
	}
	delete(h.writeErrs, p.env)
	if _, err := execute(t, a, "run", "gitea", "personal", "--replace"); err != nil {
		t.Fatal(err)
	}
}

func TestRUNStatusKeepsActualFailureState(t *testing.T) {
	h := newFakeHost()
	a := deployFixture(t, h)
	h.outputs["/usr/bin/systemctl show --property=ActiveState --value container-personal-app.service"] = "failed"
	out, err := execute(t, a, "run", "status", "personal")
	if err != nil || !strings.Contains(out, "app unit: failed") {
		t.Fatalf("status: %q %v", out, err)
	}
}

func TestRUNCancellationUsesIndependentCleanupAndRetainsOnFailure(t *testing.T) {
	for _, stopFails := range []bool{false, true} {
		h := newFakeHost()
		ctx, cancel := context.WithCancel(context.Background())
		h.readyHook = func(context.Context) error { cancel(); return context.Canceled }
		cleanupLive := false
		h.runHook = func(ctx context.Context, _ string, args []string) error {
			if len(args) > 0 && args[0] == "disable" {
				_, bounded := ctx.Deadline()
				cleanupLive = bounded && ctx.Err() == nil
				if stopFails {
					return errors.New("stop failed")
				}
			}
			return nil
		}
		cmd := (&App{Host: h}).Root()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs([]string{"run", "gitea", "personal"})
		err := cmd.ExecuteContext(ctx)
		cancel()
		if !errors.Is(err, context.Canceled) || !cleanupLive {
			t.Fatalf("cleanup context/error: %v live=%v", err, cleanupLive)
		}
		p := deploymentPaths("personal")
		if stopFails && (h.files[p.env] == "" || h.files[p.configDir+"/owner"] == "" || !h.resources["container:personal-app"]) {
			t.Fatal("failed teardown discarded recovery state")
		}
		if !stopFails && h.existing[p.configDir] {
			t.Fatal("successful cleanup retained state")
		}
	}
}

func TestRUNReadinessFailureNeverReportsSuccess(t *testing.T) {
	h := newFakeHost()
	h.readyErr = errors.New("application not responding")
	out, err := execute(t, &App{Host: h}, "run", "gitea", "personal")
	if err == nil || strings.Contains(out, "deployed gitea") || !strings.Contains(err.Error(), "journalctl") {
		t.Fatalf("readiness result: %q %v", out, err)
	}
}

func TestRUNReadinessChecksOwnedDBAndHTTP(t *testing.T) {
	h := newFakeHost()
	owner := strings.Repeat("a", 43)
	for _, role := range []string{"app", "db"} {
		h.resources["container:personal-"+role] = true
		h.labels["container:personal-"+role] = owner
	}
	h.outputs["/podman container inspect --format {{.State.Running}} personal-app"] = "true"
	h.outputs["/podman container inspect --format {{.State.Health.Status}} personal-db"] = "healthy"
	for _, tc := range []struct {
		status  int
		healthy bool
		want    bool
	}{{200, true, true}, {503, true, false}, {200, false, false}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/healthz" {
				t.Error("wrong readiness path")
			}
			w.WriteHeader(tc.status)
		}))
		if !tc.healthy {
			h.outputs["/podman container inspect --format {{.State.Health.Status}} personal-db"] = "unhealthy"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		err := pollDeploymentReady(ctx, h, "/podman", "personal", owner, server.Client(), server.URL+"/api/healthz")
		cancel()
		server.Close()
		if (err == nil) != tc.want {
			t.Fatalf("status %d healthy %v: %v", tc.status, tc.healthy, err)
		}
	}
}

func TestRUNAuthorityAndRestartContract(t *testing.T) {
	for _, raw := range []string{"https://bad_host", "https://-bad.example", "https://bad..example", "https://host:0", "https://host:65536", "https://[not-ip]", "https://host:abc"} {
		if _, err := normalizeExternalURL(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"https://[::1]:8443", "https://127.0.0.1", "https://git.example:65535"} {
		if _, err := normalizeExternalURL(raw); err != nil {
			t.Errorf("rejected %s: %v", raw, err)
		}
	}
	h := newFakeHost()
	deployForgejoFixture(t, h)
	unit := h.files[deploymentPaths("personal").appUnit]
	for _, want := range []string{"codeberg.org/forgejo/forgejo:16.0.5-rootless", "GITEA_APP_INI=/etc/gitea/app.ini", "--publish 127.0.0.1:3000:3000", "ExecStopPost=", "--cidfile /run/colt-", "container rm --ignore --filter label=org.colt.deployment=", "--filter name=^personal-app$"} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %s", want)
		}
	}
	if strings.Contains(unit, "stop --time 10 personal-app") || strings.Contains(unit, "run --rm") {
		t.Fatal("name-based cleanup or asynchronous auto-remove")
	}
}
