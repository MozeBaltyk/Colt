package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type fakeHost struct {
	goos      string
	euid      int
	missing   map[string]bool
	existing  map[string]bool
	files     map[string]string
	modes     map[string]fs.FileMode
	owners    map[string][2]int
	commands  []string
	mutations []string
	outputs   map[string]string
	failures  map[string]error
	resources map[string]bool
	writeErrs map[string]error
	mkdirErr  error
	labels    map[string]string
	readyErr  error
	readyHook func(context.Context) error
	runHook   func(context.Context, string, []string) error
}

func newFakeHost() *fakeHost {
	return &fakeHost{goos: "linux", labels: map[string]string{}, files: map[string]string{}, modes: map[string]fs.FileMode{}, owners: map[string][2]int{}, missing: map[string]bool{}, existing: map[string]bool{}, outputs: map[string]string{}, failures: map[string]error{}, resources: map[string]bool{}, writeErrs: map[string]error{}}
}

type fakeExitError struct{ code int }

func (e fakeExitError) Error() string { return "exit status" }
func (e fakeExitError) ExitCode() int { return e.code }

func (f *fakeHost) OS() string { return f.goos }
func (f *fakeHost) EUID() int  { return f.euid }
func (f *fakeHost) LookPath(name string) (string, error) {
	if f.missing[name] {
		return "", errors.New("missing")
	}
	return "/usr/bin/" + name, nil
}
func (f *fakeHost) Exists(path string) (bool, error) { return f.existing[path], nil }
func (f *fakeHost) MkdirAll(path string, mode fs.FileMode) error {
	f.mutations = append(f.mutations, "mkdir-all:"+path)
	f.modes[path] = mode
	f.existing[path] = true
	return nil
}
func (f *fakeHost) Mkdir(path string, mode fs.FileMode) error {
	f.mutations = append(f.mutations, "mkdir:"+path)
	if f.mkdirErr != nil {
		f.existing[path] = true
		return f.mkdirErr
	}
	if f.existing[path] {
		return fs.ErrExist
	}
	f.modes[path] = mode
	f.existing[path] = true
	return nil
}
func (f *fakeHost) WriteFile(path string, data []byte, mode fs.FileMode) error {
	f.mutations = append(f.mutations, "write:"+path)
	if err := f.writeErrs[path]; err != nil {
		f.existing[path] = true
		return err
	}
	f.files[path], f.modes[path] = string(data), mode
	f.existing[path] = true
	return nil
}
func (f *fakeHost) Chown(path string, uid, gid int) error {
	f.mutations = append(f.mutations, "chown:"+path)
	f.owners[path] = [2]int{uid, gid}
	return nil
}
func (f *fakeHost) ReplaceFile(path string, data []byte, mode fs.FileMode) error {
	return f.WriteFile(path, data, mode)
}
func (f *fakeHost) WaitReady(ctx context.Context, _, _, _ string) error {
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("readiness must be bounded")
	}
	if f.readyHook != nil {
		return f.readyHook(ctx)
	}
	return f.readyErr
}
func (f *fakeHost) Run(ctx context.Context, name string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	call := strings.Join(append([]string{name}, args...), " ")
	f.mutations = append(f.mutations, "run:"+call)
	f.commands = append(f.commands, call)
	if f.runHook != nil {
		if err := f.runHook(ctx, name, args); err != nil {
			return err
		}
	}
	if len(args) == 5 && args[1] == "create" {
		if err := f.failures[name+" "+args[0]+" create "+args[4]]; err != nil {
			return err
		}
	}
	if err := f.failures[call]; err != nil {
		return err
	}
	if strings.HasSuffix(name, "/systemctl") && len(args) > 1 && (args[0] == "enable" || args[0] == "start") {
		unit := args[len(args)-1]
		if strings.HasPrefix(unit, "container-") {
			container := strings.TrimSuffix(strings.TrimPrefix(unit, "container-"), ".service")
			content := f.files[filepath.Join(unitDir, unit)]
			owner := strings.TrimPrefix(strings.SplitN(content, "\n", 2)[0], "# colt-owner=")
			if !f.resources["container:"+container] {
				f.resources["container:"+container] = true
				f.labels["container:"+container] = owner
			}
		}
	}
	if strings.HasSuffix(name, "/podman") && len(args) >= 3 {
		if args[0] == "rm" {
			f.resources["container:"+args[len(args)-1]] = false
			return nil
		}
		key := args[0] + ":" + args[len(args)-1]
		if args[1] == "create" {
			if f.resources[key] {
				return nil
			}
			f.resources[key] = true
			if len(args) == 5 {
				f.labels[key] = strings.TrimPrefix(args[3], ownershipLabel+"=")
			}
		} else if args[1] == "rm" {
			f.resources[key] = false
		}
	}
	return nil
}
func (f *fakeHost) RunOutput(ctx context.Context, name string, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	call := strings.Join(append([]string{name}, args...), " ")
	if value, ok := f.outputs[call]; ok {
		return value, f.failures[call]
	}
	if len(args) == 5 && args[1] == "inspect" && strings.Contains(args[3], ownershipLabel) {
		return f.labels[args[0]+":"+args[4]] + "|" + args[4], nil
	}
	if strings.Contains(call, "--property=SystemState") {
		if value, ok := f.outputs[call]; ok {
			return value, f.failures[call]
		}
		return "running", f.failures[call]
	}
	if strings.Contains(call, "podman info") {
		return "false", f.failures[call]
	}
	if strings.Contains(call, "systemctl show") {
		if value, ok := f.outputs[call]; ok {
			return value, f.failures[call]
		}
		return "active", nil
	}
	if strings.HasSuffix(name, "/podman") && len(args) == 3 && args[1] == "exists" {
		if f.resources[args[0]+":"+args[2]] {
			return "", nil
		}
		return "", fakeExitError{1}
	}
	return f.outputs[call], f.failures[call]
}
func (f *fakeHost) ReadFile(path string) ([]byte, error) { return []byte(f.files[path]), nil }
func (f *fakeHost) Remove(path string) error {
	f.mutations = append(f.mutations, "remove:"+path)
	if err := f.failures["remove:"+path]; err != nil {
		return err
	}
	delete(f.files, path)
	f.existing[path] = false
	return nil
}
func (f *fakeHost) RemoveAll(path string) error {
	f.mutations = append(f.mutations, "remove-all:"+path)
	for file := range f.files {
		if file == path || strings.HasPrefix(file, path+"/") {
			delete(f.files, file)
			f.existing[file] = false
		}
	}
	f.existing[path] = false
	return nil
}

func TestRUNDeployGitea(t *testing.T) {
	host := newFakeHost()
	a := &App{Host: host}
	out, err := execute(t, a, "run", "gitea", "personal", "--password", "S3cret!", "--image", "docker.io/gitea/gitea:1.21.4-rootless")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deployed gitea personal") {
		t.Fatalf("output = %q", out)
	}
	paths := deploymentPaths("personal")
	if host.modes[paths.configDir] != 0o700 || host.modes[paths.env] != 0o600 {
		t.Fatalf("secure modes = dir %o env %o", host.modes[paths.configDir], host.modes[paths.env])
	}
	for _, path := range []string{paths.networkUnit, paths.dbUnit, paths.appUnit} {
		if host.modes[path] != 0o644 || host.owners[path] != [2]int{0, 0} {
			t.Fatalf("unit metadata for %s = mode %o owner %v", path, host.modes[path], host.owners[path])
		}
		if strings.Contains(host.files[path], "S3cret!") {
			t.Fatalf("password leaked into %s", path)
		}
	}
	if host.owners[paths.env] != [2]int{0, 0} || !strings.Contains(host.files[paths.env], "GITEA__security__SECRET_KEY=S3cret!") || strings.Contains(host.files[paths.env], "GITEA_PASSWORD") {
		t.Fatalf("unexpected env file: %q owner=%v", host.files[paths.env], host.owners[paths.env])
	}
	if appUnit := host.files[paths.appUnit]; !strings.Contains(appUnit, "docker.io/gitea/gitea:1.21.4-rootless") || !strings.Contains(appUnit, "--publish 127.0.0.1:3000:3000") || !strings.Contains(appUnit, "container exists personal-db") || !strings.Contains(appUnit, "wait --condition=healthy personal-db") {
		t.Fatalf("unexpected app unit: %s", appUnit)
	}
	if !strings.Contains(host.files[paths.dbUnit], `--health-cmd "healthcheck.sh --connect --innodb_initialized"`) {
		t.Fatalf("database health check missing: %s", host.files[paths.dbUnit])
	}
	owner, _ := deploymentOwner(host, paths)
	label := " --label " + ownershipLabel + "=" + owner + " "
	wantCommands := []string{
		"/usr/bin/podman network create" + label + "personal-net",
		"/usr/bin/podman volume create" + label + "personal-data-" + owner,
		"/usr/bin/podman volume create" + label + "personal-config-" + owner,
		"/usr/bin/podman volume create" + label + "personal-db-" + owner,
		"/usr/bin/systemctl daemon-reload",
		"/usr/bin/systemctl enable --now podman-network-personal-net.service",
		"/usr/bin/systemctl enable --now container-personal-db.service",
		"/usr/bin/systemctl enable --now container-personal-app.service",
	}
	if !slices.Equal(host.commands, wantCommands) {
		t.Fatalf("commands = %#v", host.commands)
	}
}

func TestRUNPreconditionsDoNotMutateHost(t *testing.T) {
	tests := []struct {
		name string
		args []string
		edit func(*fakeHost, *App)
		want string
	}{
		{"unsupported type", []string{"run", "gitlab", "personal"}, nil, "unknown command"},
		{"invalid name", []string{"run", "gitea", "Bad_Name", "--password", "x"}, nil, "invalid deployment name"},
		{"invalid image", []string{"run", "gitea", "personal", "--password", "x", "--image", "bad image"}, nil, "invalid --image"},
		{"not linux", []string{"run", "gitea", "personal", "--password", "x"}, func(h *fakeHost, _ *App) { h.goos = "darwin" }, "requires Linux"},
		{"systemd missing", []string{"run", "gitea", "personal", "--password", "x"}, func(h *fakeHost, _ *App) { h.missing["systemctl"] = true }, "systemd is unavailable"},
		{"podman missing", []string{"run", "gitea", "personal", "--password", "x"}, func(h *fakeHost, _ *App) { h.missing["podman"] = true }, "podman is unavailable"},
		{"not root", []string{"run", "gitea", "personal", "--password", "x"}, func(h *fakeHost, _ *App) { h.euid = 1000 }, "sudo"},
		{"existing", []string{"run", "gitea", "personal", "--password", "x"}, func(h *fakeHost, _ *App) { h.existing[deploymentPaths("personal").appUnit] = true }, "already exists"},
		{"unsafe password", []string{"run", "gitea", "personal", "--password", "x\ny"}, nil, "password must"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host := newFakeHost()
			a := &App{Host: host}
			if tc.edit != nil {
				tc.edit(host, a)
			}
			_, err := execute(t, a, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
			if len(host.mutations) != 0 {
				t.Fatalf("host mutated before validation: %v", host.mutations)
			}
		})
	}
}

func TestRUNGeneratesKeyWithoutPrompt(t *testing.T) {
	host := newFakeHost()
	a := &App{Host: host, IsTerminal: func() bool { return true }, ReadPassword: func() (string, error) { return "prompt-secret", nil }}
	out, err := execute(t, a, "run", "gitea", "personal")
	if err != nil {
		t.Fatal(err)
	}
	key := envSetting(host.files[deploymentPaths("personal").env], "GITEA__security__SECRET_KEY")
	if strings.Contains(out, "App password") || !dbSecretRE.MatchString(key) || strings.Contains(out, key) {
		t.Fatal("key was not generated privately")
	}
	for path, content := range host.files {
		if path != deploymentPaths("personal").env && strings.Contains(content, "prompt-secret") {
			t.Fatal(fmt.Sprintf("password leaked into %s", path))
		}
	}
}

func TestEnvironmentValueQuotesSpecialCharacters(t *testing.T) {
	if got, want := environmentValue("a b\"$`\\c"), "\"a b\\\"$`\\\\c\""; got != want {
		t.Fatalf("environmentValue = %q, want %q", got, want)
	}
}

func deployFixture(t *testing.T, host *fakeHost) *App {
	t.Helper()
	a := &App{Host: host}
	if _, err := execute(t, a, "run", "gitea", "personal", "--password", "old-secret"); err != nil {
		t.Fatal(err)
	}
	host.commands, host.mutations = nil, nil
	return a
}

func deployForgejoFixture(t *testing.T, host *fakeHost) *App {
	t.Helper()
	a := &App{Host: host}
	if _, err := execute(t, a, "run", "forgejo", "personal", "--password", "old-secret"); err != nil {
		t.Fatal(err)
	}
	host.commands, host.mutations = nil, nil
	return a
}

func envSetting(env, key string) string {
	for _, line := range strings.Split(env, "\n") {
		if value, ok := strings.CutPrefix(line, key+"="); ok {
			return value
		}
	}
	return ""
}

func TestRUNReplaceValidatesBeforeRemovalAndRedeploys(t *testing.T) {
	host := newFakeHost()
	a := deployFixture(t, host)
	oldDBPassword := envSetting(host.files[deploymentPaths("personal").env], "DB_PASSWD")
	if _, err := execute(t, a, "run", "gitea", "personal", "--replace", "--password", "bad\nsecret"); err == nil {
		t.Fatal("unsafe replacement password succeeded")
	}
	if len(host.mutations) != 0 {
		t.Fatalf("replacement mutated before validation: %v", host.mutations)
	}

	if _, err := execute(t, a, "run", "gitea", "personal", "--replace"); err != nil {
		t.Fatal(err)
	}
	newEnv := host.files[deploymentPaths("personal").env]
	if !strings.Contains(newEnv, "GITEA__security__SECRET_KEY=old-secret") {
		t.Fatal("application key changed")
	}
	if newDBPassword := envSetting(newEnv, "DB_PASSWD"); oldDBPassword == "" || newDBPassword != oldDBPassword {
		t.Fatalf("database password changed across replacement")
	}
	firstWrite := slices.IndexFunc(host.mutations, func(v string) bool { return strings.HasPrefix(v, "write:") })
	removeUnit := slices.Index(host.mutations, "remove:"+deploymentPaths("personal").appUnit)
	if removeUnit < 0 || firstWrite <= removeUnit || slices.Contains(host.mutations, "remove-all:/etc/colt/run/personal") {
		t.Fatalf("replacement order = %v", host.mutations)
	}
}

func TestRUNReplaceRejectsUnrecoverableDBPasswordWithoutMutation(t *testing.T) {
	host := newFakeHost()
	a := deployFixture(t, host)
	host.files[deploymentPaths("personal").env] = "DB_PASSWD=invalid\n"
	_, err := execute(t, a, "run", "gitea", "personal", "--replace", "--password", "new-secret")
	if err == nil || !strings.Contains(err.Error(), "recover existing database password") {
		t.Fatalf("error = %v", err)
	}
	if len(host.mutations) != 0 {
		t.Fatalf("replacement mutated host: %v", host.mutations)
	}
}

func TestRUNStatusReportsStateImagePortsAndVolumes(t *testing.T) {
	host := newFakeHost()
	a := deployFixture(t, host)
	p := deploymentPaths("personal")
	host.outputs["/usr/bin/systemctl show --property=ActiveState --value "+filepath.Base(p.networkUnit)] = "active"
	host.outputs["/usr/bin/systemctl show --property=ActiveState --value "+filepath.Base(p.dbUnit)] = "inactive"
	host.outputs["/usr/bin/systemctl show --property=ActiveState --value "+filepath.Base(p.appUnit)] = "active"
	for _, volume := range []string{"personal-data", "personal-config", "personal-db"} {
		owner, _ := deploymentOwner(host, p)
		host.outputs["/usr/bin/podman volume inspect --format {{.Mountpoint}} "+volume+"-"+owner] = "/volumes/" + volume
	}
	out, err := execute(t, a, "run", "status", "personal")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"network unit: active", "database unit: inactive", "app unit: active", "type: gitea", "image: " + giteaImage, "browser URL: http://127.0.0.1:3000/", "ports: 3000, 2222", "/volumes/personal-data", "/volumes/personal-config", "/volumes/personal-db"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output %q missing %q", out, want)
		}
	}
	if len(host.mutations) != 0 {
		t.Fatalf("status mutated host: %v", host.mutations)
	}
}

func TestRUNStatusReadsImageFromOlderGeneratedUnit(t *testing.T) {
	host := newFakeHost()
	a := deployFixture(t, host)
	p := deploymentPaths("personal")
	delete(host.files, p.metadata)
	host.existing[p.metadata] = false
	host.files[p.appUnit] = strings.Replace(host.files[p.appUnit], giteaImage, "registry.example/gitea:old", 1)
	for _, unit := range []string{p.networkUnit, p.dbUnit, p.appUnit} {
		host.outputs["/usr/bin/systemctl show --property=ActiveState --value "+filepath.Base(unit)] = "active"
	}
	out, err := execute(t, a, "run", "status", "personal")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "type: gitea") || !strings.Contains(out, "image: registry.example/gitea:old") {
		t.Fatalf("status output = %q", out)
	}
}

func TestRUNDeployForgejo(t *testing.T) {
	host := newFakeHost()
	out, err := execute(t, &App{Host: host}, "run", "forgejo", "personal", "--password", "forgejo-secret")
	if err != nil {
		t.Fatal(err)
	}
	p := deploymentPaths("personal")
	for _, want := range []string{"DB_TYPE=postgres", "DB_HOST=personal-db:5432", "POSTGRES_DB=forgejo", "POSTGRES_USER=forgejo", "POSTGRES_PASSWORD=", "FORGEJO__database__DB_TYPE=postgres", "FORGEJO__database__HOST=personal-db:5432", "FORGEJO__security__SECRET_KEY=forgejo-secret"} {
		if !strings.Contains(host.files[p.env], want) {
			t.Fatalf("env missing %q: %s", want, host.files[p.env])
		}
	}
	if db := host.files[p.dbUnit]; !strings.Contains(db, postgresImage) || !strings.Contains(db, `--health-cmd "pg_isready -U forgejo -d forgejo"`) || strings.Contains(db, "forgejo-secret") {
		t.Fatalf("unexpected PostgreSQL unit: %s", db)
	}
	if app := host.files[p.appUnit]; !strings.Contains(app, forgejoImage) || !strings.Contains(app, "--env FORGEJO__database__DB_TYPE") || !strings.Contains(app, "container exists personal-db") || !strings.Contains(app, "wait --condition=healthy personal-db") || strings.Contains(app, "forgejo-secret") {
		t.Fatalf("unexpected Forgejo unit: %s", app)
	}
	if host.files[p.metadata] != "type=forgejo\nimage="+forgejoImage+"\nexternal_url=\n" || !strings.Contains(out, "deployed forgejo personal") {
		t.Fatalf("metadata=%q output=%q", host.files[p.metadata], out)
	}
}

func TestRUNForgejoStatusAndReplacement(t *testing.T) {
	host := newFakeHost()
	a := deployForgejoFixture(t, host)
	p := deploymentPaths("personal")
	oldDBPassword := envSetting(host.files[p.env], "DB_PASSWD")
	if _, err := execute(t, a, "run", "forgejo", "personal", "--replace"); err != nil {
		t.Fatal(err)
	}
	newEnv := host.files[p.env]
	if envSetting(newEnv, "DB_PASSWD") != oldDBPassword || !strings.Contains(newEnv, "FORGEJO__security__SECRET_KEY=old-secret") {
		t.Fatalf("replacement env is wrong")
	}
	host.commands, host.mutations = nil, nil
	for _, unit := range []string{p.networkUnit, p.dbUnit, p.appUnit} {
		host.outputs["/usr/bin/systemctl show --property=ActiveState --value "+filepath.Base(unit)] = "active"
	}
	out, err := execute(t, a, "run", "status", "personal")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "type: forgejo") || !strings.Contains(out, "image: "+forgejoImage) {
		t.Fatalf("status output = %q", out)
	}
}

func TestRUNCrossTypeReplacementFailsWithoutMutation(t *testing.T) {
	host := newFakeHost()
	a := deployFixture(t, host)
	_, err := execute(t, a, "run", "forgejo", "personal", "--replace", "--password", "new-secret")
	if err == nil || !strings.Contains(err.Error(), "is gitea") || !strings.Contains(err.Error(), "through forgejo") {
		t.Fatalf("error = %v", err)
	}
	if len(host.mutations) != 0 {
		t.Fatalf("cross-type replacement mutated host: %v", host.mutations)
	}
}

func TestRUNStatusReadsImageOnlyMetadata(t *testing.T) {
	host := newFakeHost()
	a := deployFixture(t, host)
	p := deploymentPaths("personal")
	host.files[p.metadata] = "image=registry.example/gitea:current\n"
	for _, unit := range []string{p.networkUnit, p.dbUnit, p.appUnit} {
		host.outputs["/usr/bin/systemctl show --property=ActiveState --value "+filepath.Base(unit)] = "active"
	}
	out, err := execute(t, a, "run", "status", "personal")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "type: gitea") || !strings.Contains(out, "image: registry.example/gitea:current") {
		t.Fatalf("status output = %q", out)
	}
}

func TestNormalizeExternalURL(t *testing.T) {
	for _, tc := range []struct {
		input, want string
	}{
		{"https://git.example.com", "https://git.example.com/"},
		{"https://git.example.com/", "https://git.example.com/"},
		{"https://git.example.com:8443", "https://git.example.com:8443/"},
	} {
		got, err := normalizeExternalURL(tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("normalizeExternalURL(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
		}
	}
	for _, input := range []string{"", "http://git.example.com", "https://", "https://user@git.example.com", "https://git.example.com/repo", "https://git.example.com//", "https://git.example.com?x=1", "https://git.example.com#x", " https://git.example.com"} {
		if _, err := normalizeExternalURL(input); err == nil {
			t.Fatalf("normalizeExternalURL(%q) succeeded", input)
		}
	}
}

func TestRUNExternalURLRenderingAndOnboarding(t *testing.T) {
	for _, deploymentType := range []string{"gitea", "forgejo"} {
		t.Run(deploymentType, func(t *testing.T) {
			host := newFakeHost()
			secret := deploymentType + "-secret"
			out, err := execute(t, &App{Host: host}, "run", deploymentType, "personal", "--password", secret, "--external-url", "https://git.example.com")
			if err != nil {
				t.Fatal(err)
			}
			p := deploymentPaths("personal")
			prefix := strings.ToUpper(deploymentType)
			for _, setting := range []string{
				prefix + "__server__ROOT_URL=https://git.example.com/",
				prefix + "__server__DOMAIN=git.example.com",
				prefix + "__server__SSH_DOMAIN=git.example.com",
				prefix + "__server__SSH_PORT=2222",
				prefix + "__server__SSH_LISTEN_PORT=2222",
				prefix + "__server__PROTOCOL=http",
			} {
				if !strings.Contains(host.files[p.env], setting) {
					t.Fatalf("env missing %q", setting)
				}
			}
			for _, key := range []string{"ROOT_URL", "DOMAIN", "SSH_DOMAIN", "SSH_PORT", "SSH_LISTEN_PORT", "PROTOCOL"} {
				if !strings.Contains(host.files[p.appUnit], "--env "+prefix+"__server__"+key) {
					t.Fatalf("app unit missing %s server env: %s", key, host.files[p.appUnit])
				}
			}
			if !strings.Contains(host.files[p.metadata], "external_url=https://git.example.com/\n") {
				t.Fatalf("metadata = %q", host.files[p.metadata])
			}
			for _, want := range []string{
				"browser URL: https://git.example.com/",
				"create an access token first",
				"colt auth login " + deploymentType + " personal",
				"--host 'git.example.com'",
				"--base-url 'https://git.example.com'",
				"--namespace 'YOUR_NAMESPACE'",
				"--git-name 'YOUR_GIT_NAME'",
			} {
				if !strings.Contains(out, want) {
					t.Fatalf("output %q missing %q", out, want)
				}
			}
			if strings.Contains(out, secret) || strings.Contains(out, "DB_PASSWD") {
				t.Fatalf("secret leaked in output: %q", out)
			}
		})
	}
}

func TestRUNExternalURLStatusAndReplacement(t *testing.T) {
	host := newFakeHost()
	a := &App{Host: host}
	if _, err := execute(t, a, "run", "gitea", "personal", "--password", "old-secret", "--external-url", "https://old.example.com"); err != nil {
		t.Fatal(err)
	}
	p := deploymentPaths("personal")
	oldDBPassword := envSetting(host.files[p.env], "DB_PASSWD")
	host.commands, host.mutations = nil, nil
	if _, err := execute(t, a, "run", "gitea", "personal", "--replace"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(host.files[p.metadata], "external_url=https://old.example.com/\n") || !strings.Contains(host.files[p.env], "GITEA__server__ROOT_URL=https://old.example.com/") {
		t.Fatal("replacement did not preserve external URL")
	}
	if envSetting(host.files[p.env], "DB_PASSWD") != oldDBPassword {
		t.Fatal("replacement changed database password")
	}
	if _, err := execute(t, a, "run", "gitea", "personal", "--replace", "--external-url", "https://new.example.com/"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(host.files[p.metadata], "external_url=https://new.example.com/\n") || !strings.Contains(host.files[p.env], "GITEA__server__ROOT_URL=https://new.example.com/") {
		t.Fatal("replacement did not change external URL")
	}
	host.commands, host.mutations = nil, nil
	for _, unit := range []string{p.networkUnit, p.dbUnit, p.appUnit} {
		host.outputs["/usr/bin/systemctl show --property=ActiveState --value "+filepath.Base(unit)] = "active"
	}
	out, err := execute(t, a, "run", "status", "personal")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "browser URL: https://new.example.com/") {
		t.Fatalf("status output = %q", out)
	}
}

func TestRUNInvalidExternalURLDoesNotMutate(t *testing.T) {
	for _, externalURL := range []string{"http://git.example.com", "https://git.example.com/path", "https://user@git.example.com", "https://git.example.com?query=1", "https://git.example.com#fragment"} {
		host := newFakeHost()
		_, err := execute(t, &App{Host: host}, "run", "gitea", "personal", "--password", "x", "--external-url", externalURL)
		if err == nil || !strings.Contains(err.Error(), "invalid --external-url") {
			t.Fatalf("URL %q error = %v", externalURL, err)
		}
		if len(host.mutations) != 0 {
			t.Fatalf("URL %q mutated host: %v", externalURL, host.mutations)
		}
	}
}

func TestRUNLifecyclePreflightDoesNotMutate(t *testing.T) {
	for _, args := range [][]string{{"run", "status", "Bad_Name"}, {"run", "start", "Bad_Name"}, {"run", "stop", "Bad_Name"}, {"run", "rm", "Bad_Name", "--volumes"}} {
		host := newFakeHost()
		_, err := execute(t, &App{Host: host}, args...)
		if err == nil || !strings.Contains(err.Error(), "invalid deployment name") {
			t.Fatalf("args %v: error = %v", args, err)
		}
		if len(host.mutations) != 0 {
			t.Fatalf("args %v mutated host: %v", args, host.mutations)
		}
	}
}

func TestRUNStatusRequiresRoot(t *testing.T) {
	host := newFakeHost()
	host.euid = 1000
	_, err := execute(t, &App{Host: host}, "run", "status", "personal")
	if err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("error = %v", err)
	}
	if len(host.mutations) != 0 {
		t.Fatalf("status mutated host: %v", host.mutations)
	}
}

func TestRUNStartStopOrdering(t *testing.T) {
	host := newFakeHost()
	a := deployFixture(t, host)
	p := deploymentPaths("personal")
	if _, err := execute(t, a, "run", "stop", "personal"); err != nil {
		t.Fatal(err)
	}
	wantStop := []string{
		"/usr/bin/systemctl stop " + filepath.Base(p.appUnit),
		"/usr/bin/systemctl stop " + filepath.Base(p.dbUnit),
		"/usr/bin/systemctl stop " + filepath.Base(p.networkUnit),
	}
	if !slices.Equal(host.commands, wantStop) {
		t.Fatalf("stop commands = %v", host.commands)
	}
	host.commands = nil
	if _, err := execute(t, a, "run", "start", "personal"); err != nil {
		t.Fatal(err)
	}
	wantStart := []string{
		"/usr/bin/systemctl start " + filepath.Base(p.networkUnit),
		"/usr/bin/systemctl start " + filepath.Base(p.dbUnit),
		"/usr/bin/systemctl start " + filepath.Base(p.appUnit),
	}
	if !slices.Equal(host.commands, wantStart) {
		t.Fatalf("start commands = %v", host.commands)
	}
}

func TestRUNRemovePreservesVolumesUnlessRequested(t *testing.T) {
	for _, tc := range []struct {
		name      string
		flag      []string
		wantVolRM bool
	}{{"preserve", nil, false}, {"remove", []string{"--volumes"}, true}} {
		t.Run(tc.name, func(t *testing.T) {
			host := newFakeHost()
			a := deployFixture(t, host)
			args := append([]string{"run", "rm", "personal"}, tc.flag...)
			if _, err := execute(t, a, args...); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(host.commands, "\n")
			if got := strings.Contains(joined, "volume rm"); got != tc.wantVolRM {
				t.Fatalf("volume removal = %v, commands = %v", got, host.commands)
			}
			if _, ok := host.files[deploymentPaths("personal").appUnit]; ok {
				t.Fatal("app unit remains")
			}
			if !strings.Contains(joined, "podman container rm personal-app") || !strings.Contains(joined, "podman network rm personal-net") || strings.Contains(joined, "--force") {
				t.Fatalf("resource removals missing: %v", host.commands)
			}
		})
	}
}

func TestRUNFailureNamesLayerAndStops(t *testing.T) {
	host := newFakeHost()
	host.failures["/usr/bin/podman network create personal-net"] = errors.New("boom")
	a := &App{Host: host}
	_, err := execute(t, a, "run", "gitea", "personal", "--password", "x")
	if err == nil || !strings.Contains(err.Error(), "podman layer") {
		t.Fatalf("error = %v", err)
	}
	if len(host.commands) != 1 {
		t.Fatalf("commands continued after failure: %v", host.commands)
	}
}

func TestRUNFreshDeployFailureRollsBackCreatedResources(t *testing.T) {
	host := newFakeHost()
	host.failures["/usr/bin/systemctl enable --now container-personal-db.service"] = errors.New("db start failed")
	_, err := execute(t, &App{Host: host}, "run", "gitea", "personal", "--password", "x")
	if err == nil || !strings.Contains(err.Error(), "systemd layer") {
		t.Fatalf("error = %v", err)
	}
	if slices.Contains(host.commands, "/usr/bin/systemctl enable --now container-personal-app.service") {
		t.Fatalf("startup continued after failure: %v", host.commands)
	}
	stop := slices.Index(host.commands, "/usr/bin/systemctl disable --now container-personal-db.service")
	firstResourceRM := slices.IndexFunc(host.commands, func(s string) bool { return strings.Contains(s, "volume rm") })
	if stop < 0 || firstResourceRM < stop {
		t.Fatal("resources removed before stopping units")
	}
	if len(host.files) != 0 {
		t.Fatalf("files remain after rollback: %v", host.files)
	}
	for resource, exists := range host.resources {
		if exists {
			t.Fatalf("resource remains after rollback: %s", resource)
		}
	}
}

func TestRUNWriteRaceDoesNotRemoveForeignUnit(t *testing.T) {
	host := newFakeHost()
	path := deploymentPaths("personal").networkUnit
	host.files[path] = "foreign"
	host.writeErrs[path] = fs.ErrExist
	_, err := execute(t, &App{Host: host}, "run", "gitea", "personal", "--password", "x")
	if err == nil || !errors.Is(err, fs.ErrExist) {
		t.Fatalf("error = %v", err)
	}
	if host.files[path] != "foreign" || !host.existing[path] {
		t.Fatalf("foreign unit was removed: files=%v existing=%v", host.files, host.existing[path])
	}
}

func TestRUNDirectoryRaceDoesNotRemoveForeignDeployment(t *testing.T) {
	host := newFakeHost()
	host.mkdirErr = fs.ErrExist
	foreign := deploymentPaths("personal").configDir + "/foreign"
	host.files[foreign] = "keep"
	_, err := execute(t, &App{Host: host}, "run", "gitea", "personal", "--password", "x")
	if err == nil || !errors.Is(err, fs.ErrExist) {
		t.Fatalf("error = %v", err)
	}
	if host.files[foreign] != "keep" || slices.Contains(host.mutations, "remove-all:"+deploymentPaths("personal").configDir) {
		t.Fatalf("foreign deployment was removed: mutations=%v files=%v", host.mutations, host.files)
	}
}

func TestNativeHostWriteFilePreservesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(path, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := (nativeHost{}).WriteFile(path, []byte("replacement"), 0o600)
	if err == nil || !errors.Is(err, fs.ErrExist) {
		t.Fatalf("error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "foreign" {
		t.Fatalf("existing file changed: data=%q error=%v", data, err)
	}
}

func TestRUNFreshDeployRefusesPreexistingPodmanResource(t *testing.T) {
	host := newFakeHost()
	host.resources["volume:personal-data"] = true
	_, err := execute(t, &App{Host: host}, "run", "gitea", "personal", "--password", "x")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v", err)
	}
	if len(host.mutations) != 0 || !host.resources["volume:personal-data"] {
		t.Fatalf("pre-existing resource changed: mutations=%v resources=%v", host.mutations, host.resources)
	}
}

func TestRUNFreshDeployReturnsOriginalAndRollbackFailure(t *testing.T) {
	host := newFakeHost()
	host.failures["/usr/bin/systemctl enable --now container-personal-db.service"] = errors.New("db start failed")
	host.failures["/usr/bin/podman network rm personal-net"] = errors.New("network cleanup failed")
	_, err := execute(t, &App{Host: host}, "run", "gitea", "personal", "--password", "x")
	if err == nil || !strings.Contains(err.Error(), "db start failed") || !strings.Contains(err.Error(), "rollback failed") || !strings.Contains(err.Error(), "network cleanup failed") {
		t.Fatalf("error = %v", err)
	}
}
