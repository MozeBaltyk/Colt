package steps

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
	"github.com/cucumber/godog"
)

func RegisterRunSteps(ctx *godog.ScenarioContext, w *fixture.World) {
	// Orchestration only: host availability and readiness are explicitly simulated.
	ctx.Step(`^podman and systemd are available on the host$`, func() error {
		w.Host.GOOS, w.Host.UID = "linux", 0
		delete(w.Host.Missing, "podman")
		delete(w.Host.Missing, "systemctl")
		return nil
	})
	ctx.Step(`^a private application key is generated$`, func() error {
		env := w.Host.Files["/etc/colt/run/personal/env"]
		for _, line := range strings.Split(env, "\n") {
			if key, ok := strings.CutPrefix(line, "GITEA__security__SECRET_KEY="); ok && len(key) == 43 && !strings.Contains(w.Out, key) && w.RunErr == nil {
				return nil
			}
		}
		return fmt.Errorf("private application key was not generated")
	})
	ctx.Step(`^no existing deployment named "([^"]*)"$`, func(name string) error {
		w.Host.Existing[filepath.Join("/etc/colt/run", name)] = false
		return nil
	})
	ctx.Step(`^a deployment named "([^"]*)" is active$`, func(name string) error {
		w.Secrets = append(w.Secrets, "old-secret")
		w.Run("colt run gitea " + name + " --password old-secret")
		if w.RunErr != nil {
			return w.RunErr
		}
		w.Host.Commands, w.Host.Mutations = nil, nil
		return nil
	})
	ctx.Step(`^a legacy partial deployment named "([^"]*)" has no network unit$`, func(name string) error {
		dir := filepath.Join("/etc/colt/run", name)
		app := filepath.Join("/etc/systemd/system", "container-"+name+"-app.service")
		db := filepath.Join("/etc/systemd/system", "container-"+name+"-db.service")
		w.Host.Existing[dir], w.Host.Existing[app], w.Host.Existing[db] = true, true, true
		w.Host.Files[app] = "ExecStart=/usr/bin/podman run docker.io/gitea/gitea:1-rootless\n"
		w.Host.Files[db] = "[Unit]\n"
		return nil
	})
	ctx.Step(`^stdin is a terminal$`, func() error {
		w.Interactive = true
		return nil
	})
	ctx.Step(`^no unit files are created$`, func() error {
		for path := range w.Host.Files {
			if strings.HasPrefix(path, "/etc/systemd/system/") {
				return fmt.Errorf("unit created: %s", path)
			}
		}
		return nil
	})
	ctx.Step(`^no units are created$`, func() error {
		for path := range w.Host.Files {
			if strings.HasPrefix(path, "/etc/systemd/system/") {
				return fmt.Errorf("unit created: %s", path)
			}
		}
		return nil
	})
	ctx.Step(`^the three units are created under /etc/systemd/system/$`, func(table *godog.Table) error {
		for _, row := range table.Rows[1:] {
			path := filepath.Join("/etc/systemd/system", row.Cells[0].Value)
			if _, ok := w.Host.Files[path]; !ok {
				return fmt.Errorf("unit not created: %s", path)
			}
		}
		return nil
	})
	ctx.Step(`^the podman network ([a-z0-9-]+) exists$`, func(name string) error {
		if !w.Host.Resources["network:"+name] {
			return fmt.Errorf("network was not created: %v", w.Host.Commands)
		}
		return nil
	})
	ctx.Step(`^volumes ([a-z0-9-]+), ([a-z0-9-]+), ([a-z0-9-]+) exist$`, func(first, second, third string) error {
		for _, name := range []string{first, second, third} {
			owner := strings.TrimSpace(w.Host.Files["/etc/colt/run/personal/owner"])
			if !w.Host.Resources["volume:"+name+"-"+owner] {
				return fmt.Errorf("volume %s was not created: %v", name, w.Host.Commands)
			}
		}
		return nil
	})
	ctx.Step(`^all three units are enabled and active$`, func() error {
		for _, unit := range []string{"podman-network-personal-net.service", "container-personal-db.service", "container-personal-app.service"} {
			if !slices.Contains(w.Host.Commands, "/usr/bin/systemctl enable --now "+unit) {
				return fmt.Errorf("unit was not enabled and started: %s", unit)
			}
		}
		return nil
	})
	ctx.Step(`^the env file /etc/colt/run/personal/env has mode 0600$`, func() error {
		if mode := w.Host.Modes["/etc/colt/run/personal/env"]; mode != 0o600 {
			return fmt.Errorf("env mode = %o", mode)
		}
		return nil
	})
	ctx.Step(`^no password appears in any unit file as a literal$`, func() error {
		env := w.Host.Files["/etc/colt/run/personal/env"]
		marker := "GITEA__security__SECRET_KEY="
		index := strings.LastIndex(env, marker)
		if index < 0 {
			return fmt.Errorf("application secret missing from env")
		}
		secret := strings.TrimSpace(env[index+len(marker):])
		for path, content := range w.Host.Files {
			if strings.HasPrefix(path, "/etc/systemd/system/") && strings.Contains(content, secret) {
				return fmt.Errorf("secret leaked into %s", path)
			}
		}
		return nil
	})
	ctx.Step(`^container-personal-app.service uses image (.+)$`, func(image string) error {
		unit := w.Host.Files["/etc/systemd/system/container-personal-app.service"]
		if w.RunErr != nil || !strings.Contains(unit, image) {
			return fmt.Errorf("deployment error=%v app unit=%q", w.RunErr, unit)
		}
		return nil
	})
	ctx.Step(`^the database unit declares an engine-appropriate health command$`, func() error {
		unit := w.Host.Files["/etc/systemd/system/container-personal-db.service"]
		if !strings.Contains(unit, `--health-cmd "healthcheck.sh --connect --innodb_initialized"`) && !strings.Contains(unit, `--health-cmd "pg_isready -U forgejo -d forgejo"`) {
			return fmt.Errorf("database health command missing: %q", unit)
		}
		return nil
	})
	ctx.Step(`^the app unit waits for the database to become healthy$`, func() error {
		unit := w.Host.Files["/etc/systemd/system/container-personal-app.service"]
		if !strings.Contains(unit, "container exists personal-db") || !strings.Contains(unit, "wait --condition=healthy personal-db") {
			return fmt.Errorf("database health gate missing: %q", unit)
		}
		return nil
	})
	ctx.Step(`^container-personal-db.service uses the postgres image$`, func() error {
		unit := w.Host.Files["/etc/systemd/system/container-personal-db.service"]
		if !strings.Contains(unit, "docker.io/library/postgres:16") {
			return fmt.Errorf("PostgreSQL image missing: %q", unit)
		}
		return nil
	})
	ctx.Step(`^the env file sets DB_TYPE=postgres$`, func() error {
		if !strings.Contains(w.Host.Files["/etc/colt/run/personal/env"], "DB_TYPE=postgres\n") {
			return fmt.Errorf("PostgreSQL DB_TYPE missing")
		}
		return nil
	})
	ctx.Step(`^the (gitea|forgejo) deployment advertises https://git\.example\.com/ while listening over container HTTP$`, func(deploymentType string) error {
		prefix := strings.ToUpper(deploymentType)
		env := w.Host.Files["/etc/colt/run/personal/env"]
		unit := w.Host.Files["/etc/systemd/system/container-personal-app.service"]
		for _, want := range []string{
			prefix + "__server__ROOT_URL=https://git.example.com/",
			prefix + "__server__DOMAIN=git.example.com",
			prefix + "__server__SSH_DOMAIN=git.example.com",
			prefix + "__server__SSH_PORT=2222",
			prefix + "__server__SSH_LISTEN_PORT=2222",
			prefix + "__server__PROTOCOL=http",
		} {
			if !strings.Contains(env, want) {
				return fmt.Errorf("environment missing %q", want)
			}
		}
		if !strings.Contains(unit, "--env "+prefix+"__server__ROOT_URL") || !strings.Contains(unit, "--publish 127.0.0.1:3000:3000") {
			return fmt.Errorf("external settings are not passed to the app unit")
		}
		return nil
	})
	ctx.Step(`^output contains the browser URL and a shell-safe (gitea|forgejo) onboarding template$`, func(deploymentType string) error {
		for _, want := range []string{
			"browser URL: https://git.example.com/",
			"create an access token first",
			"colt auth login " + deploymentType + " personal",
			"--host 'git.example.com'",
			"--base-url 'https://git.example.com'",
			"--namespace 'YOUR_NAMESPACE'",
			"--git-name 'YOUR_GIT_NAME'",
			"--git-email 'you@example.com'",
		} {
			if !strings.Contains(w.Out, want) {
				return fmt.Errorf("output missing %q: %q", want, w.Out)
			}
		}
		return nil
	})
	ctx.Step(`^no token or administrator password is printed$`, func() error {
		if strings.Contains(w.Out, "--token ") || strings.Contains(strings.ToLower(w.Out), "admin password") {
			return fmt.Errorf("output printed a token or administrator password")
		}
		return nil
	})
	ctx.Step(`^the command fails with an invalid-external-URL error$`, func() error {
		if w.RunErr == nil || !strings.Contains(w.RunErr.Error(), "invalid --external-url") {
			return fmt.Errorf("expected invalid external URL error, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^the current user is not root$`, func() error {
		w.Host.UID = 1000
		return nil
	})
	ctx.Step(`^the command fails with an actionable error suggesting sudo$`, func() error {
		if w.RunErr == nil || !strings.Contains(w.RunErr.Error(), "sudo") {
			return fmt.Errorf("expected sudo error, got %v", w.RunErr)
		}
		return nil
	})
	ctx.Step(`^no units, networks, or volumes are created$`, func() error {
		if len(w.Host.Mutations) != 0 {
			return fmt.Errorf("host mutations: %v", w.Host.Mutations)
		}
		return nil
	})
	ctx.Step(`^the old app container is stopped and removed$`, func() error {
		commands := strings.Join(w.Host.Commands, "\n")
		if !strings.Contains(commands, "systemctl disable --now container-personal-app.service") || !strings.Contains(commands, "podman container rm personal-app") {
			return fmt.Errorf("old app was not stopped and removed: %v", w.Host.Commands)
		}
		return nil
	})
	ctx.Step(`^replacement preserves the application key$`, func() error {
		if !strings.Contains(w.Host.Files["/etc/colt/run/personal/env"], "GITEA__security__SECRET_KEY=old-secret") {
			return fmt.Errorf("replacement env was not written")
		}
		return nil
	})
	ctx.Step(`^the deployment is active$`, func() error {
		if !slices.Contains(w.Host.Commands, "/usr/bin/systemctl enable --now container-personal-app.service") {
			return fmt.Errorf("app was not activated: %v", w.Host.Commands)
		}
		return nil
	})
	ctx.Step(`^the command fails$`, func() error {
		if w.RunErr == nil {
			return fmt.Errorf("expected command failure")
		}
		return nil
	})
	ctx.Step(`^the prior deployment remains active unchanged$`, func() error {
		if len(w.Host.Mutations) != 0 {
			return fmt.Errorf("prior deployment changed: %v", w.Host.Mutations)
		}
		return nil
	})
	ctx.Step(`^output contains "active", the image ref, ports 3000 and 2222, and volume paths$`, func() error {
		for _, want := range []string{"type: gitea", "active", "docker.io/gitea/gitea:1-rootless", "3000", "2222", "/var/lib/containers/storage/volumes/personal-data-"} {
			if !strings.Contains(w.Out, want) {
				return fmt.Errorf("status output missing %q: %q", want, w.Out)
			}
		}
		return nil
	})
	ctx.Step(`^status lists "gateau" before "personal" with gateau legacy read-only and its network absent$`, func() error {
		gateau, personal := strings.Index(w.Out, "gateau  legacy (read-only)"), strings.Index(w.Out, "personal  managed")
		if w.RunErr != nil || gateau < 0 || personal < gateau || !strings.Contains(w.Out[gateau:personal], "network=absent") {
			return fmt.Errorf("unexpected status error=%v output=%q", w.RunErr, w.Out)
		}
		return nil
	})
	ctx.Step(`^container-personal-app.service is inactive$`, func() error {
		if !slices.Contains(w.Host.Commands, "/usr/bin/systemctl stop container-personal-app.service") {
			return fmt.Errorf("app was not stopped: %v", w.Host.Commands)
		}
		return nil
	})
	ctx.Step(`^the app unit is active again$`, func() error {
		if !slices.Contains(w.Host.Commands, "/usr/bin/systemctl start container-personal-app.service") {
			return fmt.Errorf("app was not started: %v", w.Host.Commands)
		}
		return nil
	})
	ctx.Step(`^named volumes are intact$`, func() error {
		if strings.Contains(strings.Join(w.Host.Commands, "\n"), "volume rm") {
			return fmt.Errorf("named volumes were removed: %v", w.Host.Commands)
		}
		return nil
	})
	ctx.Step(`^units are disabled and removed$`, func() error {
		for _, unit := range []string{"podman-network-personal-net.service", "container-personal-db.service", "container-personal-app.service"} {
			if w.Host.Existing[filepath.Join("/etc/systemd/system", unit)] {
				return fmt.Errorf("unit remains: %s", unit)
			}
		}
		return nil
	})
	ctx.Step(`^containers are removed$`, func() error {
		commands := strings.Join(w.Host.Commands, "\n")
		if !strings.Contains(commands, "podman container rm personal-app") || !strings.Contains(commands, "podman container rm personal-db") {
			return fmt.Errorf("containers were not removed: %v", w.Host.Commands)
		}
		return nil
	})
	ctx.Step(`^volumes personal-data, personal-config, personal-db still exist$`, func() error {
		if strings.Contains(strings.Join(w.Host.Commands, "\n"), "volume rm") {
			return fmt.Errorf("volumes were removed: %v", w.Host.Commands)
		}
		return nil
	})
	ctx.Step(`^volumes personal-data, personal-config, personal-db are removed$`, func() error {
		commands := strings.Join(w.Host.Commands, "\n")
		for _, volume := range []string{"personal-data", "personal-config", "personal-db"} {
			if !strings.Contains(commands, "podman volume rm "+volume+"-") {
				return fmt.Errorf("volume was not removed: %s", volume)
			}
		}
		return nil
	})
	ctx.Step(`^podman is not on PATH$`, func() error {
		w.Host.Missing["podman"] = true
		return nil
	})
	ctx.Step(`^the error names podman as the missing runtime$`, func() error {
		if w.RunErr == nil || !strings.Contains(w.RunErr.Error(), "podman") {
			return fmt.Errorf("expected podman error, got %v", w.RunErr)
		}
		return nil
	})
}
