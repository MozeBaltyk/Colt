package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const ownershipLabel = "org.colt.deployment"

func checkRunRuntime(ctx context.Context, host HostOperations, systemctl, podman string) error {
	state, err := host.RunOutput(ctx, systemctl, "show", "--property=SystemState", "--value")
	if err != nil || (state != "running" && state != "degraded") {
		return errors.New("systemd manager is not running; inspect systemctl is-system-running before deployment")
	}
	rootless, err := host.RunOutput(ctx, podman, "info", "--format", "{{.Host.Security.Rootless}}")
	if err != nil || rootless != "false" {
		return errors.New("rootful Podman storage/runtime is unavailable; inspect podman info as the deployment user")
	}
	return nil
}

func lockDeployment(host HostOperations, name string) (func(), error) {
	if err := host.MkdirAll(runDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(runDir, name+".lock")
	if err := host.Mkdir(path, 0o700); err != nil {
		return nil, fmt.Errorf("deployment locked at %s; if interrupted, confirm no Colt operation is running before removing this empty lock directory: %w", path, err)
	}
	return func() { _ = host.Remove(path) }, nil
}

func (nativeHost) ReplaceFile(path string, data []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".colt-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func parseAppSecret(data []byte, product string) (string, error) {
	prefix := strings.ToUpper(product) + "__security__SECRET_KEY="
	value := ""
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if raw, ok := strings.CutPrefix(line, prefix); ok {
			if found {
				return "", errors.New("duplicate application SECRET_KEY; restore protected env backup")
			}
			found = true
			value = raw
			if strings.HasPrefix(raw, `"`) {
				var err error
				value, err = strconv.Unquote(raw)
				if err != nil {
					return "", errors.New("invalid application SECRET_KEY encoding")
				}
			}
		}
	}
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("cannot recover application SECRET_KEY; restore protected env backup")
	}
	return value, nil
}

func deploymentOwner(host HostOperations, p runPaths) (string, error) {
	data, err := host.ReadFile(p.configDir + "/owner")
	owner := strings.TrimSpace(string(data))
	if err != nil || !dbSecretRE.MatchString(owner) {
		return "", fmt.Errorf("ownership cannot be established at %s; legacy deployments are read-only: back up database, repositories and configuration, then migrate manually to a new Colt name; do not relabel or delete existing resources", p.configDir)
	}
	return owner, nil
}

func ownedDeploymentFiles(files []renderedFile, owner, name, podman string) []renderedFile {
	for i := range files {
		f := &files[i]
		if !strings.HasSuffix(f.path, ".service") {
			continue
		}
		for _, role := range []string{"data", "config", "db"} {
			f.content = strings.ReplaceAll(f.content, "--volume "+name+"-"+role+":", "--volume "+name+"-"+role+"-"+owner+":")
		}
		f.content = "# colt-owner=" + owner + "\n" + f.content
		for _, role := range []string{"app", "db"} {
			container := name + "-" + role
			cid := "/run/colt-" + owner + "-" + role + ".cid"
			// Reboot can leave an exited container after /run (and its CID) is lost.
			// Podman selects by both exact name and ownership label, never name alone.
			start := "ExecStart=" + podman + " run --name " + container
			cleanup := "ExecStartPre=/bin/sh -c '" + podman + " container rm --ignore --filter label=" + ownershipLabel + "=" + owner + " --filter name=^" + container + "$ && /usr/bin/rm -f " + cid + "'\n"
			f.content = strings.ReplaceAll(f.content, start, cleanup+start)
			f.content = strings.ReplaceAll(f.content, "run --name "+container, "run --cidfile "+cid+" --label "+ownershipLabel+"="+owner+" --name "+container)
			stop := podman + " stop --ignore --time 10 --cidfile " + cid
			f.content = strings.ReplaceAll(f.content, "ExecStop="+podman+" stop --time 10 "+container, "ExecStop="+stop+"\nExecStopPost=/bin/sh -c '"+stop+" && "+podman+" rm --ignore --cidfile "+cid+" && /usr/bin/rm -f "+cid+"'")
		}
	}
	// Write the ownership record first, so even interrupted setup has a recovery anchor.
	return append([]renderedFile{{deploymentPaths(name).configDir + "/owner", owner + "\n", 0o600}}, files...)
}

// Inspect labels before mutation. Containers/networks are removed by immutable ID,
// never by a name that a competing client can reassign between inspect and remove.
func ownedResource(ctx context.Context, host HostOperations, podman string, r podmanResource, owner string) (string, error) {
	exists, err := podmanResourceExists(ctx, host, podman, r.kind, r.name)
	if err != nil || !exists {
		return "", err
	}
	format := `{{index .Labels "org.colt.deployment"}}|{{.ID}}`
	if r.kind == "container" {
		format = `{{index .Config.Labels "org.colt.deployment"}}|{{.Id}}`
	}
	if r.kind == "volume" {
		format = `{{index .Labels "org.colt.deployment"}}|{{.Name}}`
	}
	identity, err := host.RunOutput(ctx, podman, r.kind, "inspect", "--format", format, r.name)
	if err != nil {
		return "", err
	}
	label, id, ok := strings.Cut(identity, "|")
	if label != owner {
		return "", fmt.Errorf("refusing foreign %s %s: ownership label mismatch", r.kind, r.name)
	}
	if !ok || id == "" {
		return "", fmt.Errorf("cannot obtain resource identity for %s %s", r.kind, r.name)
	}
	if r.kind == "volume" && (id != r.name || !strings.HasSuffix(id, "-"+owner)) {
		return "", errors.New("refusing volume without unique deployment identity")
	}
	return id, nil
}

func ownedDeploymentResources(name, owner string) []podmanResource {
	resources := deploymentResources(name)
	for i := range resources {
		if resources[i].kind == "volume" {
			resources[i].name += "-" + owner
		}
	}
	return resources
}

func verifyDeployment(ctx context.Context, host HostOperations, podman, name, owner string) error {
	p := deploymentPaths(name)
	currentOwner, err := deploymentOwner(host, p)
	if err != nil {
		return err
	}
	if currentOwner != owner {
		return errors.New("deployment ownership changed during preflight; refusing mutation, retry after the other operation completes")
	}
	for _, path := range []string{p.networkUnit, p.dbUnit, p.appUnit} {
		exists, err := host.Exists(path)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		data, err := host.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(data), "# colt-owner="+owner+"\n") {
			return fmt.Errorf("refusing unfamiliar unit %s; restore the owned unit from backup before retrying", path)
		}
	}
	resources := append(ownedDeploymentResources(name, owner), podmanResource{"container", name + "-app"}, podmanResource{"container", name + "-db"})
	for _, r := range resources {
		if _, err := ownedResource(ctx, host, podman, r, owner); err != nil {
			return err
		}
	}
	return nil
}

func removeOwnedResource(ctx context.Context, host HostOperations, podman string, r podmanResource, owner string) error {
	id, err := ownedResource(ctx, host, podman, r, owner)
	if err != nil || id == "" {
		return err
	}
	if r.kind == "container" {
		// A partial removal may have lost its unit but left an owned container running.
		if err := host.Run(ctx, podman, "container", "stop", "--time", "10", id); err != nil {
			return fmt.Errorf("podman layer: stop owned container %s; recovery state retained: %w", r.name, err)
		}
	}
	if err := host.Run(ctx, podman, r.kind, "rm", id); err != nil {
		return fmt.Errorf("podman layer: remove %s %s failed (possibly in use); detach consumers explicitly, then retry rm; recovery credentials retained: %w", r.kind, r.name, err)
	}
	return nil
}

func waitDeployment(ctx context.Context, host HostOperations, podman, name, owner string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := host.WaitReady(ctx, podman, name, owner); err != nil {
		return fmt.Errorf("readiness failed for %s: %w; inspect systemctl status container-%s-app.service container-%s-db.service and journalctl -u container-%s-app.service (logs may contain secrets; redact before sharing)", name, err, name, name, name)
	}
	return nil
}

func (h nativeHost) WaitReady(ctx context.Context, podman, name, owner string) error {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	return pollDeploymentReady(ctx, h, podman, name, owner, client, "http://127.0.0.1:3000/api/healthz")
}

func pollDeploymentReady(ctx context.Context, h HostOperations, podman, name, owner string, client *http.Client, endpoint string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		ready := true
		for _, role := range []string{"db", "app"} {
			id, err := ownedResource(ctx, h, podman, podmanResource{"container", name + "-" + role}, owner)
			if err != nil || id == "" {
				ready = false
				break
			}
			format, want := "{{.State.Running}}", "true"
			if role == "db" {
				format, want = "{{.State.Health.Status}}", "healthy"
			}
			state, err := h.RunOutput(ctx, podman, "container", "inspect", "--format", format, id)
			if err != nil || state != want {
				ready = false
				break
			}
		}
		if ready {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func validateRunAuthority(u *url.URL) error {
	invalid := errors.New("invalid --external-url hostname or port; use DNS labels/IP and a port from 1 to 65535")
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return invalid
		}
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return nil
	}
	if strings.HasPrefix(u.Host, "[") {
		return invalid
	}
	if len(host) > 253 {
		return invalid
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return invalid
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return invalid
			}
		}
	}
	return nil
}
