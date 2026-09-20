package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	runDir        = "/etc/colt/run"
	unitDir       = "/etc/systemd/system"
	giteaImage    = "docker.io/gitea/gitea:1-rootless"
	mariaImage    = "docker.io/library/mariadb:11"
	forgejoImage  = "codeberg.org/forgejo/forgejo:16.0.5-rootless"
	postgresImage = "docker.io/library/postgres:16"
)

var (
	deploymentNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	imageRefRE       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]*$`)
	plainEnvValueRE  = regexp.MustCompile(`^[A-Za-z0-9_!@%+=,.:/-]+$`)
	dbSecretRE       = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

// HostOperations is the narrow boundary around host inspection and mutation.
// Tests replace it so they never touch the machine running the test suite.
type HostOperations interface {
	OS() string
	EUID() int
	LookPath(string) (string, error)
	Exists(string) (bool, error)
	ReadDir(string) ([]fs.DirEntry, error)
	MkdirAll(string, fs.FileMode) error
	Mkdir(string, fs.FileMode) error
	WriteFile(string, []byte, fs.FileMode) error
	ReplaceFile(string, []byte, fs.FileMode) error
	Chown(string, int, int) error
	Run(context.Context, string, ...string) error
	RunOutput(context.Context, string, ...string) (string, error)
	ReadFile(string) ([]byte, error)
	Remove(string) error
	RemoveAll(string) error
	WaitReady(context.Context, string, string, string) error
}

type nativeHost struct{}

type limitedOutput struct{ bytes.Buffer }

func (w *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 4096 - w.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = w.Buffer.Write(p)
	}
	return n, nil
}

func (nativeHost) OS() string                           { return runtime.GOOS }
func (nativeHost) EUID() int                            { return os.Geteuid() }
func (nativeHost) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (nativeHost) MkdirAll(path string, mode fs.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
func (nativeHost) Mkdir(path string, mode fs.FileMode) error {
	if err := os.Mkdir(path, mode); err != nil {
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		return errors.Join(err, os.Remove(path))
	}
	return nil
}
func (nativeHost) Chown(path string, uid, gid int) error { return os.Chown(path, uid, gid) }
func (nativeHost) Exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}
func (nativeHost) ReadDir(path string) ([]fs.DirEntry, error) { return os.ReadDir(path) }
func (nativeHost) WriteFile(path string, data []byte, mode fs.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if err = f.Chmod(mode); err != nil {
		err = errors.Join(err, f.Close())
	} else {
		if _, err = f.Write(data); err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
	}
	if err != nil {
		err = errors.Join(err, os.Remove(path))
	}
	return err
}
func (nativeHost) Run(ctx context.Context, name string, args ...string) error {
	_, err := (nativeHost{}).RunOutput(ctx, name, args...)
	return err
}

func (nativeHost) RunOutput(ctx context.Context, name string, args ...string) (string, error) {
	var output limitedOutput
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("%s failed: %w; stderr suppressed to protect secrets; inspect podman info for runtime/storage or systemctl status and journalctl -u for the named service (redact logs before sharing)", filepath.Base(name), err)
	}
	return strings.TrimSpace(output.String()), nil
}
func (nativeHost) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (nativeHost) Remove(path string) error             { return os.Remove(path) }
func (nativeHost) RemoveAll(path string) error          { return os.RemoveAll(path) }

type runOptions struct {
	image, password, externalURL      string
	replace, externalURLSet, imageSet bool
}

func (a *App) runCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Manage a systemd-managed Gitea or Forgejo server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("unknown command %q for colt run", args[0])
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(a.runDeployCommand("gitea", giteaImage), a.runDeployCommand("forgejo", forgejoImage), a.runStatusCommand(), a.runControlCommand("stop"), a.runControlCommand("start"), a.runRemoveCommand())
	return cmd
}

func (a *App) runDeployCommand(deploymentType, defaultImage string) *cobra.Command {
	opts := runOptions{}
	cmd := &cobra.Command{
		Use:   deploymentType + " <name>",
		Short: "Deploy a " + deploymentType + " server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.externalURLSet = cmd.Flags().Changed("external-url")
			opts.imageSet = cmd.Flags().Changed("image")
			return a.deployServer(cmd, deploymentType, args[0], opts)
		},
	}
	cmd.Flags().StringVar(&opts.image, "image", defaultImage, deploymentType+" container image")
	cmd.Flags().StringVar(&opts.password, "password", "", "legacy application SECRET_KEY override (not an admin password); generated if omitted; rotation forbidden")
	cmd.Flags().StringVar(&opts.externalURL, "external-url", "", "external HTTPS browser URL")
	cmd.Flags().BoolVar(&opts.replace, "replace", false, "replace an existing deployment")
	return cmd
}

func (a *App) deployServer(cmd *cobra.Command, deploymentType, name string, opts runOptions) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()
	cmd.SetContext(ctx)
	if !deploymentNameRE.MatchString(name) {
		return errors.New("invalid deployment name; use 1-63 lowercase letters, digits, or '-' and start with a letter or digit")
	}
	if !imageRefRE.MatchString(opts.image) || strings.Contains(opts.image, "..") {
		return errors.New("invalid --image reference")
	}
	externalURL := ""
	if opts.externalURLSet {
		var err error
		externalURL, err = normalizeExternalURL(opts.externalURL)
		if err != nil {
			return err
		}
	}
	host, systemctl, podman, err := a.runPreflight(true)
	if err != nil {
		return err
	}
	paths := deploymentPaths(name)
	var priorOwner string
	var priorEnv []byte
	var priorInfo deploymentMetadata
	existing := false
	for _, path := range paths.all() {
		exists, err := host.Exists(path)
		if err != nil {
			return fmt.Errorf("filesystem layer: inspect existing deployment %s: %w", name, err)
		}
		if exists {
			existing = true
		}
	}
	if existing && !opts.replace {
		return fmt.Errorf("deployment %q already exists; use --replace to recreate it", name)
	}
	if existing {
		priorOwner, err = deploymentOwner(host, paths)
		if err != nil {
			return err
		}
		info, err := deploymentInfo(host, paths)
		if err != nil {
			return err
		}
		if info.deploymentType != deploymentType {
			return fmt.Errorf("deployment %q is %s; refusing replacement through %s", name, info.deploymentType, deploymentType)
		}
		priorInfo = info
		if !opts.externalURLSet {
			externalURL = info.externalURL
		}
		if !opts.imageSet {
			opts.image = info.image
		}
	}
	if strings.ContainsAny(opts.password, "\r\n\x00") {
		return errors.New("password must be non-empty and contain no CR, LF, or NUL")
	}
	var dbPassword string
	if existing {
		data, err := host.ReadFile(paths.env)
		if err != nil {
			return fmt.Errorf("filesystem layer: recover existing database password: %w", err)
		}
		priorEnv = data
		dbPassword, err = parseDBPassword(data, deploymentType)
		if err != nil {
			return fmt.Errorf("filesystem layer: recover existing database password: %w", err)
		}
		key, err := parseAppSecret(data, deploymentType)
		if err != nil {
			return err
		}
		if opts.password != "" && opts.password != key {
			return errors.New("application SECRET_KEY rotation is unsupported; omit --password on replacement")
		}
		opts.password = key
	} else {
		dbPassword, err = randomSecret()
		if err != nil {
			return fmt.Errorf("generate database password: %w", err)
		}
		for _, resource := range deploymentResources(name) {
			exists, err := podmanResourceExists(cmd.Context(), host, podman, resource.kind, resource.name)
			if err != nil {
				return fmt.Errorf("podman layer: inspect %s %s: %w", resource.kind, resource.name, err)
			}
			if exists {
				return fmt.Errorf("podman layer: %s %s already exists; refusing to overwrite a resource not owned by this deployment", resource.kind, resource.name)
			}
		}
		for _, container := range deploymentContainers(name) {
			exists, err := podmanResourceExists(cmd.Context(), host, podman, "container", container)
			if err != nil {
				return fmt.Errorf("podman layer: inspect container %s: %w", container, err)
			}
			if exists {
				return fmt.Errorf("podman layer: container %s already exists; refusing to overwrite a resource not owned by this deployment", container)
			}
		}
	}
	if opts.password == "" {
		opts.password, err = randomSecret()
		if err != nil {
			return err
		}
	}
	owner, err := randomSecret()
	if err != nil {
		return err
	}
	if existing {
		owner = priorOwner
		if err = verifyDeployment(cmd.Context(), host, podman, name, owner); err != nil {
			return err
		}
		for _, r := range ownedDeploymentResources(name, owner) {
			if r.kind != "volume" {
				continue
			}
			id, err := ownedResource(cmd.Context(), host, podman, r, owner)
			if err != nil {
				return err
			}
			if id == "" {
				return errors.New("retained volume is missing; restore the complete backup before --replace, or finish rm --volumes; refusing a mixed old/new database")
			}
		}
	}
	unlock, err := lockDeployment(host, name)
	if err != nil {
		return err
	}
	defer unlock()
	if existing {
		if err := verifyDeployment(cmd.Context(), host, podman, name, owner); err != nil {
			return err
		}
		currentEnv, err := host.ReadFile(paths.env)
		if err != nil {
			return err
		}
		currentInfo, err := deploymentInfo(host, paths)
		if err != nil {
			return err
		}
		if !bytes.Equal(currentEnv, priorEnv) || currentInfo != priorInfo {
			return errors.New("deployment changed during preflight; retry after the other operation completes")
		}
	}
	if err := checkRunRuntime(cmd.Context(), host, systemctl, podman); err != nil {
		return err
	}
	files := renderDeploymentFiles(deploymentType, name, podman, opts.image, opts.password, dbPassword, externalURL)
	files = ownedDeploymentFiles(files, owner, name, podman)
	if existing {
		if err := removeDeployment(cmd.Context(), host, systemctl, podman, name, false); err != nil {
			return fmt.Errorf("replace %q: %w", name, err)
		}
	}
	var attemptedFiles []string
	var attemptedResources []podmanResource
	var attemptedUnits []string
	daemonReloadAttempted := false
	fail := func(original error) error {
		if existing {
			return fmt.Errorf("%w; recovery state retained at %s; retry --replace or rm", original, paths.configDir)
		}
		rollbackErr := rollbackFreshDeployment(cmd.Context(), host, systemctl, podman, name, paths, attemptedFiles, attemptedResources, attemptedUnits, daemonReloadAttempted)
		if rollbackErr != nil {
			return errors.Join(original, fmt.Errorf("rollback failed: %w", rollbackErr))
		}
		return original
	}
	if err := host.MkdirAll(runDir, 0o700); err != nil {
		return fmt.Errorf("filesystem layer: create deployment parent directory: %w", err)
	}
	if !existing {
		if err := host.Mkdir(paths.configDir, 0o700); err != nil {
			return fmt.Errorf("filesystem layer: create deployment directory: %w", err)
		}
	}
	if err := host.Chown(paths.configDir, 0, 0); err != nil {
		return fail(fmt.Errorf("filesystem layer: secure deployment directory: %w", err))
	}
	for _, file := range files {
		write := host.WriteFile
		if existing && (file.path == paths.env || file.path == paths.metadata) {
			write = host.ReplaceFile
		}
		if existing && file.path == paths.configDir+"/owner" {
			continue
		}
		if err := write(file.path, []byte(file.content), file.mode); err != nil {
			return fail(fmt.Errorf("filesystem layer: write %s: %w", file.path, err))
		}
		attemptedFiles = append(attemptedFiles, file.path)
		if err := host.Chown(file.path, 0, 0); err != nil {
			return fail(fmt.Errorf("filesystem layer: set root ownership on %s: %w", file.path, err))
		}
	}
	for _, resource := range ownedDeploymentResources(name, owner) {
		exists, err := podmanResourceExists(cmd.Context(), host, podman, resource.kind, resource.name)
		if err != nil {
			return fail(err)
		}
		if exists && existing {
			if _, err := ownedResource(cmd.Context(), host, podman, resource, owner); err != nil {
				return fail(err)
			}
			continue
		}
		if existing && resource.kind == "volume" {
			return fail(errors.New("retained volume disappeared; restore backup before retrying; refusing to initialize an empty volume"))
		}
		if err := host.Run(cmd.Context(), podman, resource.kind, "create", "--label", ownershipLabel+"="+owner, resource.name); err != nil {
			inspectCtx, cancel := context.WithTimeout(context.WithoutCancel(cmd.Context()), 10*time.Second)
			id, inspectErr := ownedResource(inspectCtx, host, podman, resource, owner)
			cancel()
			if inspectErr != nil {
				return fmt.Errorf("podman layer: create %s failed: %w; ownership could not be verified; recovery state retained at %s", resource.name, err, paths.configDir)
			}
			if id != "" {
				attemptedResources = append(attemptedResources, resource)
			}
			return fail(fmt.Errorf("podman layer: create %s %s: %w", resource.kind, resource.name, err))
		}
		inspectCtx, inspectCancel := context.WithTimeout(context.WithoutCancel(cmd.Context()), 10*time.Second)
		id, inspectErr := ownedResource(inspectCtx, host, podman, resource, owner)
		inspectCancel()
		if inspectErr != nil {
			return fmt.Errorf("%w; recovery state retained at %s; no unverified resource was removed", inspectErr, paths.configDir)
		} else if id == "" {
			return fail(errors.New("created resource disappeared before ownership verification"))
		}
		attemptedResources = append(attemptedResources, resource)
	}
	daemonReloadAttempted = true
	if err := host.Run(cmd.Context(), systemctl, "daemon-reload"); err != nil {
		return fail(fmt.Errorf("systemd layer: daemon-reload failed: %w", err))
	}
	for _, unit := range []string{paths.networkUnit, paths.dbUnit, paths.appUnit} {
		unit = filepath.Base(unit)
		attemptedUnits = append(attemptedUnits, unit)
		if err := host.Run(cmd.Context(), systemctl, "enable", "--now", unit); err != nil {
			return fail(fmt.Errorf("systemd layer: enable %s failed: %w", unit, err))
		}
	}
	if err := waitDeployment(cmd.Context(), host, podman, name, owner); err != nil {
		return fail(err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "deployed %s %s on ports 3000 and 2222\n", deploymentType, name)
	printDeploymentURLs(cmd, deploymentType, name, externalURL)
	return nil
}

func normalizeExternalURL(raw string) (string, error) {
	if raw == "" || raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "\r\n\x00") {
		return "", errors.New("invalid --external-url; use an absolute HTTPS URL with a hostname and root path")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || strings.HasSuffix(u.Host, ":") {
		return "", errors.New("invalid --external-url; use an absolute HTTPS URL with a hostname and root path")
	}
	u.Path = "/"
	if err := validateRunAuthority(u); err != nil {
		return "", err
	}
	normalized := u.String()
	if raw != normalized && raw != strings.TrimSuffix(normalized, "/") {
		return "", errors.New("invalid --external-url; URL must be clean and may have at most one trailing slash")
	}
	return normalized, nil
}

func printDeploymentURLs(cmd *cobra.Command, deploymentType, name, externalURL string) {
	browserURL := externalURL
	if browserURL == "" {
		browserURL = "http://127.0.0.1:3000/"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "browser URL: %s\n", browserURL)
	fmt.Fprintln(cmd.OutOrStdout(), "HTTP backend: 127.0.0.1:3000 only; use a same-host HTTPS proxy. SSH on port 2222 is for direct Git clients; Colt SSH transport does not support this port. One active deployment per host (fixed ports).")
	if externalURL == "" {
		return
	}
	u, _ := url.Parse(externalURL)
	fmt.Fprintln(cmd.OutOrStdout(), "Finish web setup and create an access token first; then run this onboarding template:")
	fmt.Fprintf(cmd.OutOrStdout(), "colt auth login %s %s --host %s --base-url %s --namespace %s --git-name %s --git-email %s --credential stored\n",
		deploymentType, name, shellQuote(u.Host), shellQuote(strings.TrimSuffix(externalURL, "/")), shellQuote("YOUR_NAMESPACE"), shellQuote("YOUR_GIT_NAME"), shellQuote("you@example.com"))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

type podmanResource struct{ kind, name string }

func deploymentResources(name string) []podmanResource {
	return []podmanResource{
		{"network", name + "-net"},
		{"volume", name + "-data"},
		{"volume", name + "-config"},
		{"volume", name + "-db"},
	}
}

func deploymentContainers(name string) []string {
	return []string{name + "-app", name + "-db"}
}

func rollbackFreshDeployment(ctx context.Context, host HostOperations, systemctl, podman, name string, paths runPaths, files []string, resources []podmanResource, units []string, reload bool) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	owner, err := deploymentOwner(host, paths)
	if err != nil {
		return fmt.Errorf("recovery state retained at %s: %w", paths.configDir, err)
	}
	var rollbackErrors []error
	for i := len(units) - 1; i >= 0; i-- {
		if err := host.Run(ctx, systemctl, "disable", "--now", units[i]); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("systemd layer: disable %s: %w", units[i], err))
		}
	}
	if len(rollbackErrors) != 0 {
		return fmt.Errorf("recovery state retained at %s: %w", paths.configDir, errors.Join(rollbackErrors...))
	}
	if len(units) != 0 {
		for _, container := range deploymentContainers(name) {
			if err := removeOwnedResource(ctx, host, podman, podmanResource{"container", container}, owner); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("podman layer: remove container %s: %w", container, err))
			}
		}
	}
	for i := len(resources) - 1; i >= 0; i-- {
		resource := resources[i]
		if err := removeOwnedResource(ctx, host, podman, resource, owner); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	if len(rollbackErrors) != 0 {
		return fmt.Errorf("recovery state retained at %s: %w", paths.configDir, errors.Join(rollbackErrors...))
	}
	for i := len(files) - 1; i >= 0; i-- {
		if !strings.HasSuffix(files[i], ".service") {
			continue
		}
		exists, err := host.Exists(files[i])
		if err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("filesystem layer: inspect %s: %w", files[i], err))
		} else if exists {
			if err := host.Remove(files[i]); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("filesystem layer: remove %s: %w", files[i], err))
			}
		}
	}
	if len(rollbackErrors) != 0 {
		return fmt.Errorf("recovery state retained at %s: %w", paths.configDir, errors.Join(rollbackErrors...))
	}
	if reload {
		if err := host.Run(ctx, systemctl, "daemon-reload"); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("systemd layer: daemon-reload: %w", err))
		}
	}
	if len(rollbackErrors) == 0 {
		if err := host.RemoveAll(paths.configDir); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("filesystem layer: remove %s: %w", paths.configDir, err))
		}
	}
	return errors.Join(rollbackErrors...)
}

func (a *App) runPreflight(requireRoot bool) (HostOperations, string, string, error) {
	host := a.Host
	if host == nil {
		host = nativeHost{}
	}
	if host.OS() != "linux" {
		return nil, "", "", errors.New("colt run requires Linux with systemd and podman")
	}
	systemctl, err := host.LookPath("systemctl")
	if err != nil {
		return nil, "", "", errors.New("systemd is unavailable: install systemd and ensure systemctl is on PATH")
	}
	podman, err := host.LookPath("podman")
	if err != nil {
		return nil, "", "", errors.New("podman is unavailable: install podman and ensure it is on PATH")
	}
	if requireRoot && host.EUID() != 0 {
		return nil, "", "", errors.New("colt run requires root; rerun with sudo")
	}
	return host, systemctl, podman, nil
}

func validateDeploymentName(name string) error {
	if !deploymentNameRE.MatchString(name) {
		return errors.New("invalid deployment name; use 1-63 lowercase letters, digits, or '-' and start with a letter or digit")
	}
	return nil
}

func requireDeploymentUnits(host HostOperations, name string) error {
	p := deploymentPaths(name)
	for _, path := range []string{p.networkUnit, p.dbUnit, p.appUnit} {
		exists, err := host.Exists(path)
		if err != nil {
			return fmt.Errorf("filesystem layer: inspect %s: %w", path, err)
		}
		if !exists {
			return fmt.Errorf("deployment %q is incomplete: required unit %s is missing", name, filepath.Base(path))
		}
	}
	return nil
}

func (a *App) runStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status [name]",
		Short: "Show deployment status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if err := validateDeploymentName(args[0]); err != nil {
					return err
				}
			}
			host, systemctl, podman, err := a.runPreflight(true)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				names, err := deploymentNames(host)
				if err != nil {
					return err
				}
				if len(names) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "no deployments found")
					return nil
				}
				for _, name := range names {
					p := deploymentPaths(name)
					states := deploymentUnitStates(cmd.Context(), host, systemctl, p)
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  app=%s db=%s network=%s\n", name, deploymentLifecycle(host, p), states["app"], states["database"], states["network"])
				}
				return nil
			}
			name := args[0]
			p := deploymentPaths(name)
			fmt.Fprintf(cmd.OutOrStdout(), "ownership/lifecycle: %s\n", deploymentLifecycle(host, p))
			states := deploymentUnitStates(cmd.Context(), host, systemctl, p)
			for _, label := range []string{"network", "database", "app"} {
				fmt.Fprintf(cmd.OutOrStdout(), "%s unit: %s\n", label, states[label])
			}
			info := statusDeploymentInfo(host, p)
			browserURL := info.externalURL
			if browserURL == "" {
				browserURL = "http://127.0.0.1:3000/"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "type: %s\nimage: %s\nbrowser URL: %s\nports: 3000, 2222\n", info.deploymentType, info.image, browserURL)
			owner, ownerErr := deploymentOwner(host, p)
			for _, volume := range []string{name + "-data", name + "-config", name + "-db"} {
				if ownerErr == nil {
					volume += "-" + owner
				}
				path, err := host.RunOutput(cmd.Context(), podman, "volume", "inspect", "--format", "{{.Mountpoint}}", volume)
				if err != nil || path == "" {
					fmt.Fprintf(cmd.OutOrStdout(), "volume %s: unavailable\n", volume)
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "volume %s: %s\n", volume, path)
			}
			return nil
		},
	}
}

func deploymentNames(host HostOperations) ([]string, error) {
	names := map[string]bool{}
	entries, err := host.ReadDir(runDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("filesystem layer: list deployments: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() && validateDeploymentName(entry.Name()) == nil {
			names[entry.Name()] = true
		}
	}
	entries, err = host.ReadDir(unitDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("filesystem layer: list deployment units: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			if name := deploymentNameFromUnit(entry.Name()); name != "" {
				names[name] = true
			}
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func deploymentNameFromUnit(unit string) string {
	var name string
	if strings.HasPrefix(unit, "podman-network-") && strings.HasSuffix(unit, "-net.service") {
		name = strings.TrimSuffix(strings.TrimPrefix(unit, "podman-network-"), "-net.service")
	} else if strings.HasPrefix(unit, "container-") {
		name = strings.TrimPrefix(unit, "container-")
		if strings.HasSuffix(name, "-app.service") {
			name = strings.TrimSuffix(name, "-app.service")
		} else if strings.HasSuffix(name, "-db.service") {
			name = strings.TrimSuffix(name, "-db.service")
		} else {
			return ""
		}
	}
	if validateDeploymentName(name) != nil {
		return ""
	}
	return name
}

func deploymentUnitStates(ctx context.Context, host HostOperations, systemctl string, p runPaths) map[string]string {
	states := map[string]string{}
	for _, unit := range []struct{ label, path string }{{"network", p.networkUnit}, {"database", p.dbUnit}, {"app", p.appUnit}} {
		exists, err := host.Exists(unit.path)
		if err != nil {
			states[unit.label] = "unavailable"
		} else if !exists {
			states[unit.label] = "absent"
		} else if state, err := host.RunOutput(ctx, systemctl, "show", "--property=ActiveState", "--value", filepath.Base(unit.path)); err != nil || state == "" {
			states[unit.label] = "unavailable"
		} else {
			states[unit.label] = state
		}
	}
	return states
}

func deploymentLifecycle(host HostOperations, p runPaths) string {
	if _, err := deploymentOwner(host, p); err != nil {
		return "legacy (read-only)"
	}
	for _, path := range []string{p.networkUnit, p.dbUnit, p.appUnit} {
		if exists, err := host.Exists(path); err != nil || !exists {
			return "retained/partial"
		}
	}
	return "managed"
}

func statusDeploymentInfo(host HostOperations, p runPaths) deploymentMetadata {
	info := deploymentMetadata{"unknown", "unknown", ""}
	metadataImage := false
	if data, err := host.ReadFile(p.metadata); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch key {
			case "type":
				if value == "gitea" || value == "forgejo" {
					info.deploymentType = value
				}
			case "image":
				if imageRefRE.MatchString(value) && !strings.Contains(value, "..") {
					info.image = value
					metadataImage = true
				}
			case "external_url":
				if normalized, err := normalizeExternalURL(value); value != "" && err == nil && normalized == value {
					info.externalURL = value
				}
			}
		}
	}
	data, err := host.ReadFile(p.appUnit)
	if err == nil && info.image == "unknown" {
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "ExecStart=") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 0 && imageRefRE.MatchString(fields[len(fields)-1]) && !strings.Contains(fields[len(fields)-1], "..") {
				info.image = fields[len(fields)-1]
				break
			}
		}
	}
	if info.deploymentType == "unknown" && info.image != "unknown" {
		if metadataImage {
			info.deploymentType = "gitea" // image-only metadata predates Forgejo support
		} else if strings.Contains(strings.ToLower(info.image), "forgejo") {
			info.deploymentType = "forgejo"
		} else if strings.Contains(strings.ToLower(info.image), "gitea") {
			info.deploymentType = "gitea"
		}
	}
	return info
}

type deploymentMetadata struct{ deploymentType, image, externalURL string }

func deploymentInfo(host HostOperations, p runPaths) (deploymentMetadata, error) {
	if exists, err := host.Exists(p.metadata); err != nil {
		return deploymentMetadata{}, fmt.Errorf("filesystem layer: inspect deployment metadata: %w", err)
	} else if exists {
		data, err := host.ReadFile(p.metadata)
		if err != nil {
			return deploymentMetadata{}, fmt.Errorf("filesystem layer: read deployment metadata: %w", err)
		}
		values := map[string]string{}
		seen := map[string]bool{}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if !ok || (key != "type" && key != "image" && key != "external_url") || seen[key] {
				return deploymentMetadata{}, errors.New("filesystem layer: invalid deployment metadata")
			}
			seen[key] = true
			values[key] = value
		}
		deploymentType := values["type"]
		if deploymentType == "" {
			deploymentType = "gitea"
		}
		image := values["image"]
		if (deploymentType != "gitea" && deploymentType != "forgejo") || !imageRefRE.MatchString(image) || strings.Contains(image, "..") {
			return deploymentMetadata{}, errors.New("filesystem layer: invalid deployment metadata")
		}
		externalURL := values["external_url"]
		if externalURL != "" {
			normalized, err := normalizeExternalURL(externalURL)
			if err != nil || normalized != externalURL {
				return deploymentMetadata{}, errors.New("filesystem layer: invalid deployment metadata")
			}
		}
		return deploymentMetadata{deploymentType, image, externalURL}, nil
	}
	data, err := host.ReadFile(p.appUnit)
	if err != nil {
		return deploymentMetadata{}, fmt.Errorf("filesystem layer: read app unit: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 0 {
			image := fields[len(fields)-1]
			if imageRefRE.MatchString(image) && !strings.Contains(image, "..") {
				return deploymentMetadata{"gitea", image, ""}, nil
			}
		}
	}
	return deploymentMetadata{"gitea", giteaImage, ""}, nil
}

func (a *App) runControlCommand(action string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " <name>",
		Short: action + " a deployment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			cmd.SetContext(ctx)
			name := args[0]
			if err := validateDeploymentName(name); err != nil {
				return err
			}
			host, systemctl, podman, err := a.runPreflight(true)
			if err != nil {
				return err
			}
			if err := requireDeploymentUnits(host, name); err != nil {
				return err
			}
			p := deploymentPaths(name)
			owner, err := deploymentOwner(host, p)
			if err != nil {
				return err
			}
			if err := verifyDeployment(cmd.Context(), host, podman, name, owner); err != nil {
				return err
			}
			unlock, err := lockDeployment(host, name)
			if err != nil {
				return err
			}
			defer unlock()
			if err := verifyDeployment(cmd.Context(), host, podman, name, owner); err != nil {
				return err
			}
			units := []string{p.networkUnit, p.dbUnit, p.appUnit}
			if action == "stop" {
				units = []string{p.appUnit, p.dbUnit, p.networkUnit}
				for _, path := range units {
					unit := filepath.Base(path)
					if err := host.Run(cmd.Context(), systemctl, "stop", unit); err != nil {
						return fmt.Errorf("systemd layer: stop %s: %w", unit, err)
					}
					state, err := unitActiveState(cmd.Context(), host, systemctl, unit)
					if err != nil {
						return err
					}
					if state == "failed" {
						if err := host.Run(cmd.Context(), systemctl, "reset-failed", unit); err != nil {
							return fmt.Errorf("systemd layer: reset failed state for %s after stop: %w", unit, err)
						}
						state, err = unitActiveState(cmd.Context(), host, systemctl, unit)
						if err != nil {
							return err
						}
					}
					if state != "inactive" {
						return fmt.Errorf("systemd layer: stop %s completed but ActiveState is %q, want inactive", unit, state)
					}
					role := ""
					switch path {
					case p.appUnit:
						role = "app"
					case p.dbUnit:
						role = "db"
					}
					if role != "" {
						container := name + "-" + role
						id, err := ownedResource(cmd.Context(), host, podman, podmanResource{"container", container}, owner)
						if err != nil {
							return fmt.Errorf("podman layer: verify %s absent after stopping %s: %w", container, unit, err)
						}
						if id != "" {
							return fmt.Errorf("podman layer: %s ExecStopPost left owned container %s present; refusing to remove it from stop", unit, container)
						}
					}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "stopped deployment %s\n", name)
				return nil
			}
			for _, path := range units {
				unit := filepath.Base(path)
				if err := host.Run(cmd.Context(), systemctl, "reset-failed", unit); err != nil {
					return fmt.Errorf("systemd layer: reset failed state for %s before start: %w", unit, err)
				}
			}
			for _, path := range units {
				unit := filepath.Base(path)
				if err := host.Run(cmd.Context(), systemctl, "start", unit); err != nil {
					return fmt.Errorf("systemd layer: start %s: %w", unit, err)
				}
			}
			if action == "start" {
				if err := waitDeployment(cmd.Context(), host, podman, name, owner); err != nil {
					return err
				}
			}
			for _, path := range units {
				unit := filepath.Base(path)
				state, err := unitActiveState(cmd.Context(), host, systemctl, unit)
				if err != nil {
					return err
				}
				if state != "active" {
					return fmt.Errorf("systemd layer: start readiness passed but %s ActiveState is %q, want active", unit, state)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "started deployment %s\n", name)
			return nil
		},
	}
}

func unitActiveState(ctx context.Context, host HostOperations, systemctl, unit string) (string, error) {
	state, err := host.RunOutput(ctx, systemctl, "show", "--property=ActiveState", "--value", unit)
	if err != nil {
		return "", fmt.Errorf("systemd layer: inspect ActiveState for %s: %w", unit, err)
	}
	if state == "" {
		return "", fmt.Errorf("systemd layer: inspect ActiveState for %s: empty result", unit)
	}
	return state, nil
}

func (a *App) runRemoveCommand() *cobra.Command {
	var volumes bool
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a deployment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := validateDeploymentName(name); err != nil {
				return err
			}
			host, systemctl, podman, err := a.runPreflight(true)
			if err != nil {
				return err
			}
			owner, err := deploymentOwner(host, deploymentPaths(name))
			if err != nil {
				return err
			}
			if err := verifyDeployment(cmd.Context(), host, podman, name, owner); err != nil {
				return err
			}
			unlock, err := lockDeployment(host, name)
			if err != nil {
				return err
			}
			defer unlock()
			if err := verifyDeployment(cmd.Context(), host, podman, name, owner); err != nil {
				return err
			}
			if err := removeDeployment(cmd.Context(), host, systemctl, podman, name, volumes); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed deployment %s\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&volumes, "volumes", false, "remove named volumes")
	cmd.Flags().BoolVar(&volumes, "volume", false, "remove named volumes (deprecated alias for --volumes)")
	_ = cmd.Flags().MarkDeprecated("volume", "use --volumes instead")
	return cmd
}

func removeDeployment(ctx context.Context, host HostOperations, systemctl, podman, name string, volumes bool) error {
	p := deploymentPaths(name)
	owner, err := deploymentOwner(host, p)
	if err != nil {
		return err
	}
	if err := verifyDeployment(ctx, host, podman, name, owner); err != nil {
		return err
	}
	for _, path := range []string{p.appUnit, p.dbUnit, p.networkUnit} {
		exists, err := host.Exists(path)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := host.Run(ctx, systemctl, "disable", "--now", filepath.Base(path)); err != nil {
			return fmt.Errorf("systemd layer: disable %s: %w", filepath.Base(path), err)
		}
	}
	for _, container := range []string{name + "-app", name + "-db"} {
		if err := removeOwnedResource(ctx, host, podman, podmanResource{"container", container}, owner); err != nil {
			return fmt.Errorf("podman layer: remove container %s: %w", container, err)
		}
	}
	for _, resource := range ownedDeploymentResources(name, owner) {
		if resource.kind == "volume" && !volumes {
			continue
		}
		if err := removeOwnedResource(ctx, host, podman, resource, owner); err != nil {
			return err
		}
	}
	for _, path := range []string{p.appUnit, p.dbUnit, p.networkUnit} {
		if exists, err := host.Exists(path); err != nil {
			return err
		} else if !exists {
			continue
		}
		if err := host.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem layer: remove %s: %w", path, err)
		}
	}
	if err := host.Run(ctx, systemctl, "daemon-reload"); err != nil {
		return fmt.Errorf("systemd layer: daemon-reload: %w", err)
	}
	if volumes {
		if err := host.RemoveAll(p.configDir); err != nil {
			return fmt.Errorf("filesystem layer: remove %s: %w", p.configDir, err)
		}
	}
	return nil
}

func podmanResourceExists(ctx context.Context, host HostOperations, podman, kind, name string) (bool, error) {
	_, err := host.RunOutput(ctx, podman, kind, "exists", name)
	if err == nil {
		return true, nil
	}
	var exitError interface{ ExitCode() int }
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func parseDBPassword(data []byte, deploymentType string) (string, error) {
	if len(data) > 64<<10 {
		return "", errors.New("existing env file is too large")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		wanted := key == "DB_PASSWD"
		if deploymentType == "gitea" {
			wanted = wanted || key == "MARIADB_PASSWORD" || key == "MARIADB_ROOT_PASSWORD" || key == "GITEA__database__PASSWD"
		} else {
			wanted = wanted || key == "POSTGRES_PASSWORD" || key == "FORGEJO__database__PASSWD"
		}
		if wanted {
			if _, duplicate := values[key]; duplicate {
				return "", errors.New("existing env file has duplicate database password settings")
			}
			values[key] = value
		}
	}
	password := values["DB_PASSWD"]
	if !dbSecretRE.MatchString(password) {
		return "", errors.New("existing env file has an invalid database password")
	}
	keys := []string{"MARIADB_PASSWORD", "MARIADB_ROOT_PASSWORD", "GITEA__database__PASSWD"}
	if deploymentType == "forgejo" {
		keys = []string{"POSTGRES_PASSWORD", "FORGEJO__database__PASSWD"}
	}
	for _, key := range keys {
		if values[key] != password {
			return "", errors.New("existing env file has inconsistent database password settings")
		}
	}
	return password, nil
}

type runPaths struct {
	configDir, env, metadata, networkUnit, dbUnit, appUnit string
}

func deploymentPaths(name string) runPaths {
	return runPaths{
		configDir:   filepath.Join(runDir, name),
		env:         filepath.Join(runDir, name, "env"),
		metadata:    filepath.Join(runDir, name, "metadata"),
		networkUnit: filepath.Join(unitDir, "podman-network-"+name+"-net.service"),
		dbUnit:      filepath.Join(unitDir, "container-"+name+"-db.service"),
		appUnit:     filepath.Join(unitDir, "container-"+name+"-app.service"),
	}
}

func (p runPaths) all() []string {
	return []string{p.configDir, p.env, p.networkUnit, p.dbUnit, p.appUnit}
}

type renderedFile struct {
	path, content string
	mode          fs.FileMode
}

func renderDeploymentFiles(deploymentType, name, podman, image, appSecret, dbPassword, externalURL string) []renderedFile {
	p := deploymentPaths(name)
	appName := "Gitea"
	env := fmt.Sprintf("DB_TYPE=mysql\nDB_HOST=%s-db:3306\nDB_NAME=gitea\nDB_USER=gitea\nDB_PASSWD=%s\nMARIADB_DATABASE=gitea\nMARIADB_USER=gitea\nMARIADB_PASSWORD=%s\nMARIADB_ROOT_PASSWORD=%s\nGITEA__database__DB_TYPE=mysql\nGITEA__database__HOST=%s-db:3306\nGITEA__database__NAME=gitea\nGITEA__database__USER=gitea\nGITEA__database__PASSWD=%s\nGITEA__security__SECRET_KEY=%s\n", name, dbPassword, dbPassword, dbPassword, name, dbPassword, environmentValue(appSecret))
	dbDescription := "MariaDB"
	dbCommand := fmt.Sprintf("--env MARIADB_DATABASE --env MARIADB_USER --env MARIADB_PASSWORD --env MARIADB_ROOT_PASSWORD --volume %s-db:/var/lib/mysql --health-cmd \"healthcheck.sh --connect --innodb_initialized\" --health-interval 5s --health-retries 30 %s", name, mariaImage)
	appEnv := "--env GITEA__database__DB_TYPE --env GITEA__database__HOST --env GITEA__database__NAME --env GITEA__database__USER --env GITEA__database__PASSWD --env GITEA__security__SECRET_KEY"
	if deploymentType == "forgejo" {
		appName = "Forgejo"
		env = fmt.Sprintf("DB_TYPE=postgres\nDB_HOST=%s-db:5432\nDB_NAME=forgejo\nDB_USER=forgejo\nDB_PASSWD=%s\nPOSTGRES_DB=forgejo\nPOSTGRES_USER=forgejo\nPOSTGRES_PASSWORD=%s\nFORGEJO__database__DB_TYPE=postgres\nFORGEJO__database__HOST=%s-db:5432\nFORGEJO__database__NAME=forgejo\nFORGEJO__database__USER=forgejo\nFORGEJO__database__PASSWD=%s\nFORGEJO__security__SECRET_KEY=%s\nFORGEJO__server__SSH_PORT=2222\nFORGEJO__server__SSH_LISTEN_PORT=2222\n", name, dbPassword, dbPassword, name, dbPassword, environmentValue(appSecret))
		dbDescription = "PostgreSQL"
		dbCommand = fmt.Sprintf("--env POSTGRES_DB --env POSTGRES_USER --env POSTGRES_PASSWORD --volume %s-db:/var/lib/postgresql/data --health-cmd \"pg_isready -U forgejo -d forgejo\" --health-interval 5s --health-retries 30 %s", name, postgresImage)
		appEnv = "--env FORGEJO__database__DB_TYPE --env FORGEJO__database__HOST --env FORGEJO__database__NAME --env FORGEJO__database__USER --env FORGEJO__database__PASSWD --env FORGEJO__security__SECRET_KEY --env FORGEJO__server__SSH_PORT --env FORGEJO__server__SSH_LISTEN_PORT"
		// Official v16 rootless supports GITEA_APP_INI; persist config in its own volume.
		appEnv += " --env GITEA_APP_INI=/etc/gitea/app.ini"
	}
	if externalURL != "" {
		u, _ := url.Parse(externalURL)
		serverEnv := ""
		if deploymentType == "forgejo" {
			serverEnv = "--env FORGEJO__server__ROOT_URL --env FORGEJO__server__DOMAIN --env FORGEJO__server__SSH_DOMAIN --env FORGEJO__server__PROTOCOL"
			env += fmt.Sprintf("FORGEJO__server__ROOT_URL=%s\nFORGEJO__server__DOMAIN=%s\nFORGEJO__server__SSH_DOMAIN=%s\nFORGEJO__server__PROTOCOL=http\n",
				environmentValue(externalURL), environmentValue(u.Hostname()), environmentValue(u.Hostname()))
		} else {
			serverEnv = "--env GITEA__server__ROOT_URL --env GITEA__server__DOMAIN --env GITEA__server__SSH_DOMAIN --env GITEA__server__SSH_PORT --env GITEA__server__SSH_LISTEN_PORT --env GITEA__server__PROTOCOL"
			env += fmt.Sprintf("GITEA__server__ROOT_URL=%s\nGITEA__server__DOMAIN=%s\nGITEA__server__SSH_DOMAIN=%s\nGITEA__server__SSH_PORT=2222\nGITEA__server__SSH_LISTEN_PORT=2222\nGITEA__server__PROTOCOL=http\n",
				environmentValue(externalURL), environmentValue(u.Hostname()), environmentValue(u.Hostname()))
		}
		appEnv += " " + serverEnv
	}
	network := fmt.Sprintf(`[Unit]
Description=Podman network for Colt deployment %s

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=%s network exists %s-net

[Install]
WantedBy=multi-user.target
`, name, podman, name)
	db := fmt.Sprintf(`[Unit]
Description=%s for Colt deployment %s
Requires=podman-network-%s-net.service
After=podman-network-%s-net.service

[Service]
Restart=always
EnvironmentFile=%s
ExecStart=%s run --name %s-db --network %s-net %s
ExecStop=%s stop --time 10 %s-db

[Install]
WantedBy=multi-user.target
`, dbDescription, name, name, name, p.env, podman, name, name, dbCommand, podman, name)
	app := fmt.Sprintf(`[Unit]
Description=%s for Colt deployment %s
Requires=podman-network-%s-net.service container-%s-db.service
After=podman-network-%s-net.service container-%s-db.service

[Service]
Restart=always
TimeoutStartSec=300
EnvironmentFile=%s
ExecStartPre=/bin/sh -c 'until %s container exists %s-db; do sleep 1; done'
ExecStartPre=%s wait --condition=healthy %s-db
ExecStart=%s run --name %s-app --network %s-net %s --publish 127.0.0.1:3000:3000 --publish 2222:2222 --volume %s-data:/var/lib/gitea:U --volume %s-config:/etc/gitea:U %s
ExecStop=%s stop --time 10 %s-app

[Install]
WantedBy=multi-user.target
`, appName, name, name, name, name, name, p.env, podman, name, podman, name, podman, name, name, appEnv, name, name, image, podman, name)
	metadata := fmt.Sprintf("type=%s\nimage=%s\nexternal_url=%s\n", deploymentType, image, externalURL)
	return []renderedFile{{p.env, env, 0o600}, {p.metadata, metadata, 0o600}, {p.networkUnit, network, 0o644}, {p.dbUnit, db, 0o644}, {p.appUnit, app, 0o644}}
}

func environmentValue(value string) string {
	if plainEnvValueRE.MatchString(value) {
		return value
	}
	value = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	return `"` + value + `"`
}
