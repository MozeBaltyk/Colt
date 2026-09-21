package fixture

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/credential"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
)

// FakeHost records all host mutations in memory. It never invokes systemctl,
// podman, sudo, or writes outside the test process.
type FakeHost struct {
	GOOS       string
	UID        int
	Missing    map[string]bool
	Existing   map[string]bool
	Files      map[string]string
	Modes      map[string]fs.FileMode
	Commands   []string
	Mutations  []string
	Resources  map[string]bool
	Labels     map[string]string
	UnitStates map[string]string
}

func NewFakeHost() *FakeHost {
	return &FakeHost{GOOS: "linux", Labels: map[string]string{}, Missing: map[string]bool{}, Existing: map[string]bool{}, Files: map[string]string{}, Modes: map[string]fs.FileMode{}, Resources: map[string]bool{}, UnitStates: map[string]string{}}
}

type fakeExitError struct{ code int }

func (e fakeExitError) Error() string { return "exit status" }
func (e fakeExitError) ExitCode() int { return e.code }

func (f *FakeHost) OS() string { return f.GOOS }
func (f *FakeHost) EUID() int  { return f.UID }
func (f *FakeHost) LookPath(name string) (string, error) {
	if f.Missing[name] {
		return "", fmt.Errorf("missing %s", name)
	}
	return "/usr/bin/" + name, nil
}
func (f *FakeHost) Exists(path string) (bool, error) { return f.Existing[path], nil }
func (f *FakeHost) ReadDir(dir string) ([]fs.DirEntry, error) {
	entries := map[string]bool{}
	for path, exists := range f.Existing {
		if exists && filepath.Dir(path) == dir {
			_, file := f.Files[path]
			entries[filepath.Base(path)] = dir == "/etc/colt/run" && !file
		}
	}
	result := make([]fs.DirEntry, 0, len(entries))
	for name, isDir := range entries {
		result = append(result, fakeDirEntry{name, isDir})
	}
	return result, nil
}

type fakeDirEntry struct {
	name string
	dir  bool
}

func (e fakeDirEntry) Name() string { return e.name }
func (e fakeDirEntry) IsDir() bool  { return e.dir }
func (e fakeDirEntry) Type() fs.FileMode {
	if e.dir {
		return fs.ModeDir
	}
	return 0
}
func (e fakeDirEntry) Info() (fs.FileInfo, error) { return nil, nil }
func (f *FakeHost) MkdirAll(path string, mode fs.FileMode) error {
	f.Mutations = append(f.Mutations, "mkdir-all:"+path)
	f.Modes[path] = mode
	f.Existing[path] = true
	return nil
}
func (f *FakeHost) Mkdir(path string, mode fs.FileMode) error {
	f.Mutations = append(f.Mutations, "mkdir:"+path)
	if f.Existing[path] {
		return fs.ErrExist
	}
	f.Modes[path] = mode
	f.Existing[path] = true
	return nil
}
func (f *FakeHost) WriteFile(path string, data []byte, mode fs.FileMode) error {
	f.Mutations = append(f.Mutations, "write:"+path)
	f.Files[path], f.Modes[path] = string(data), mode
	f.Existing[path] = true
	return nil
}
func (f *FakeHost) Chown(path string, _, _ int) error {
	f.Mutations = append(f.Mutations, "chown:"+path)
	return nil
}
func (f *FakeHost) ReplaceFile(path string, data []byte, mode fs.FileMode) error {
	return f.WriteFile(path, data, mode)
}
func (f *FakeHost) WaitReady(ctx context.Context, _, _, _ string) error {
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("readiness must be bounded")
	}
	return ctx.Err()
}
func (f *FakeHost) Run(_ context.Context, name string, args ...string) error {
	call := strings.Join(append([]string{name}, args...), " ")
	f.Mutations = append(f.Mutations, "run:"+call)
	f.Commands = append(f.Commands, call)
	if strings.HasSuffix(name, "/systemctl") && len(args) > 1 && (args[0] == "enable" || args[0] == "start") {
		unit := args[len(args)-1]
		f.UnitStates[unit] = "active"
		if strings.HasPrefix(unit, "container-") {
			container := strings.TrimSuffix(strings.TrimPrefix(unit, "container-"), ".service")
			content := f.Files[filepath.Join("/etc/systemd/system", unit)]
			owner := strings.TrimPrefix(strings.SplitN(content, "\n", 2)[0], "# colt-owner=")
			if !f.Resources["container:"+container] {
				f.Resources["container:"+container] = true
				f.Labels["container:"+container] = owner
			}
		}
	}
	if strings.HasSuffix(name, "/systemctl") && len(args) == 2 && args[0] == "stop" {
		unit := args[1]
		f.UnitStates[unit] = "inactive"
		if strings.HasPrefix(unit, "container-") {
			container := strings.TrimSuffix(strings.TrimPrefix(unit, "container-"), ".service")
			f.Resources["container:"+container] = false
		}
	}
	if strings.HasSuffix(name, "/podman") && len(args) >= 3 {
		if args[0] == "rm" {
			f.Resources["container:"+args[len(args)-1]] = false
			return nil
		}
		key := args[0] + ":" + args[len(args)-1]
		if args[1] == "create" {
			if f.Resources[key] {
				return nil
			}
			f.Resources[key] = true
			if len(args) == 5 {
				f.Labels[key] = strings.TrimPrefix(args[3], "org.colt.deployment=")
			}
		} else if args[1] == "rm" {
			f.Resources[key] = false
		}
	}
	return nil
}
func (f *FakeHost) RunOutput(_ context.Context, name string, args ...string) (string, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	if len(args) == 5 && args[1] == "inspect" && strings.Contains(args[3], "org.colt.deployment") {
		return f.Labels[args[0]+":"+args[4]] + "|" + args[4], nil
	}
	if strings.Contains(call, "--property=SystemState") {
		return "running", nil
	}
	if strings.Contains(call, "podman info") {
		return "false", nil
	}
	if strings.HasSuffix(name, "/podman") && len(args) == 3 && args[1] == "exists" {
		if f.Resources[args[0]+":"+args[2]] {
			return "", nil
		}
		return "", fakeExitError{1}
	}
	if strings.Contains(call, "systemctl show") {
		if len(args) != 0 && f.UnitStates[args[len(args)-1]] != "" {
			return f.UnitStates[args[len(args)-1]], nil
		}
		return "active", nil
	}
	if strings.Contains(call, "podman volume inspect") {
		fields := strings.Fields(call)
		return filepath.Join("/var/lib/containers/storage/volumes", fields[len(fields)-1], "_data"), nil
	}
	return "", nil
}
func (f *FakeHost) ReadFile(path string) ([]byte, error) {
	data, ok := f.Files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(data), nil
}
func (f *FakeHost) Remove(path string) error {
	f.Mutations = append(f.Mutations, "remove:"+path)
	delete(f.Files, path)
	f.Existing[path] = false
	return nil
}
func (f *FakeHost) RemoveAll(path string) error {
	f.Mutations = append(f.Mutations, "remove-all:"+path)
	for file := range f.Files {
		if file == path || strings.HasPrefix(file, path+"/") {
			delete(f.Files, file)
			f.Existing[file] = false
		}
	}
	f.Existing[path] = false
	return nil
}

// FakeGit records calls; with Real=true it delegates to native git.
type FakeGit struct {
	Real           bool
	AvailableErr   error
	PushErr        error
	Origins        []string
	Pushes         int
	PushURL        string
	Helpers        int
	Operations     []string
	OriginURL      string
	OriginErr      error
	LsRemoteErr    error
	ProbeBounded   bool
	ProbeAlias     string
	ProbeProject   string
	CloneHook      func(string) error
	CloneErrors    map[string]error
	MirrorCloneErr map[string]error
	MirrorDirs     []string
	MirrorForce    []bool
}

func (f *FakeGit) Available() error {
	f.Operations = append(f.Operations, "available")
	if f.AvailableErr != nil {
		return f.AvailableErr
	}
	if f.Real {
		return gitnative.Native{}.Available()
	}
	return nil
}

func (f *FakeGit) Origin(ctx context.Context, dir string) (string, error) {
	f.Operations = append(f.Operations, "origin")
	if f.OriginErr != nil || f.OriginURL != "" || !f.Real {
		return f.OriginURL, f.OriginErr
	}
	return gitnative.Native{}.Origin(ctx, dir)
}

func (f *FakeGit) LsRemote(ctx context.Context, dir, origin, alias, project string, tokenEnvNames []string) error {
	f.Operations = append(f.Operations, "ls-remote:"+origin)
	_, f.ProbeBounded = ctx.Deadline()
	f.ProbeAlias, f.ProbeProject = alias, project
	if f.LsRemoteErr != nil || !f.Real {
		return f.LsRemoteErr
	}
	return gitnative.Native{}.LsRemote(ctx, dir, origin, alias, project, tokenEnvNames)
}

func (f *FakeGit) Init(ctx context.Context, dir string) error {
	f.Operations = append(f.Operations, "init")
	if f.Real {
		return gitnative.Native{}.Init(ctx, dir)
	}
	return os.MkdirAll(filepath.Join(dir, ".git"), 0o755) // ponytail: marker only, no history
}

func (f *FakeGit) Clone(ctx context.Context, url, dir, username, alias, project string) error {
	f.Operations = append(f.Operations, "clone")
	if err := f.CloneErrors[project]; err != nil {
		return err
	}
	if f.Real {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		native := gitnative.Native{}
		if err := native.Init(ctx, dir); err != nil {
			return err
		}
		if err := native.AddOrigin(ctx, dir, url); err != nil {
			return err
		}
		f.Origins = append(f.Origins, url)
		if f.CloneHook != nil {
			return f.CloneHook(dir)
		}
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		return fmt.Errorf("fake git init: %s: %v", out, err)
	}
	f.Origins = append(f.Origins, url)
	if f.CloneHook != nil {
		return f.CloneHook(dir)
	}
	return nil
}

func (f *FakeGit) MirrorClone(ctx context.Context, url, dir, alias, project, _ string, _ []string) error {
	f.Operations = append(f.Operations, "mirror-clone:"+project)
	f.MirrorDirs = append(f.MirrorDirs, dir)
	if err := f.MirrorCloneErr[project]; err != nil {
		return err
	}
	return f.Clone(ctx, url, dir, "", alias, project)
}

func (f *FakeGit) MirrorPush(_ context.Context, _ /* dir */, url, _, project string, force bool, _ string, _ []string) error {
	f.Operations = append(f.Operations, fmt.Sprintf("mirror-push:%s:%t", project, force))
	f.MirrorForce = append(f.MirrorForce, force)
	if f.PushErr != nil {
		return f.PushErr
	}
	f.Pushes++
	f.PushURL = url
	return nil
}

func (f *FakeGit) SetIdentity(ctx context.Context, dir, name, email string) error {
	f.Operations = append(f.Operations, "identity")
	if f.Real {
		return gitnative.Native{}.SetIdentity(ctx, dir, name, email)
	}
	return nil
}

func (f *FakeGit) Commit(ctx context.Context, dir string) (string, error) {
	f.Operations = append(f.Operations, "commit")
	if f.Real {
		return gitnative.Native{}.Commit(ctx, dir)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "COMMIT"), []byte("fake"), 0o600); err != nil {
		return "", err
	}
	return "fake-commit", nil
}

func (f *FakeGit) AddOrigin(ctx context.Context, dir, url string) error {
	f.Operations = append(f.Operations, "origin")
	if f.Real {
		if err := (gitnative.Native{}).AddOrigin(ctx, dir, url); err != nil {
			return err
		}
	}
	f.Origins = append(f.Origins, url)
	return nil
}

func (f *FakeGit) ConfigureCredentialHelper(ctx context.Context, dir, cloneURL, username, alias, project string) error {
	f.Operations = append(f.Operations, "helper")
	if !gitnative.SafeIdentifier(alias) || !gitnative.SafeIdentifier(project) {
		return fmt.Errorf("refusing unsafe provider or repository scope for Git credential helper")
	}
	helper := "!colt git-credential --provider " + alias + " --repository " + project
	if f.Real {
		if err := (gitnative.Native{}).ConfigureCredentialHelper(ctx, dir, cloneURL, username, alias, project); err != nil {
			return err
		}
	} else {
		cmd := exec.Command("git", "config", "--local", "--add", "credential.helper", helper)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("fake git config credential helper: %s: %v", out, err)
		}
	}
	f.Helpers++
	return nil
}

func (f *FakeGit) Push(_ context.Context, _ /* dir */, url, _, _ string) error {
	f.Operations = append(f.Operations, "push")
	if f.PushErr != nil {
		return f.PushErr
	}
	f.Pushes++
	f.PushURL = url
	return nil
}

func (f *FakeGit) PushTag(_ context.Context, _ /* dir */, url, _, _, tag string, _ []string) error {
	f.Operations = append(f.Operations, "push-tag:"+tag)
	if f.PushErr != nil {
		return f.PushErr
	}
	f.Pushes++
	f.PushURL = url
	return nil
}

func (f *FakeGit) CreateTag(_ context.Context, _ /* dir */, tag, _, _ string) error {
	f.Operations = append(f.Operations, "tag:"+tag)
	return nil
}

func (f *FakeGit) ValidateTag(_ context.Context, _ /* dir */, tag string) error {
	f.Operations = append(f.Operations, "validate-tag:"+tag)
	return nil
}

func (f *FakeGit) ValidateRepoConfig(ctx context.Context, dir string) error {
	f.Operations = append(f.Operations, "validate-config")
	// Always enforce against real repository-local config: a fake worktree has no
	// repository, so Native returns nil there and only rejects a real hostile repo.
	return gitnative.Native{}.ValidateRepoConfig(ctx, dir)
}

type FakeClient struct {
	Account    string
	AuthErr    error
	GetRepo    *provider.Repository
	GetErr     error
	ListRepos  []provider.Repository
	ListErr    error
	CreateRepo *provider.Repository
	CreateErr  error
	ReleaseErr error
	Visibility string
	RevokeErr  error
	Calls      []string
	Events     *[]string
	GetRepos   map[string]*provider.Repository
	GetErrors  map[string]error
	CreateFunc func(string) (*provider.Repository, error)
	ListHook   func() error
}

func (f *FakeClient) Authenticate(context.Context) (string, error) {
	f.Calls = append(f.Calls, "Authenticate")
	if f.Events != nil {
		*f.Events = append(*f.Events, "authenticate")
	}
	if f.AuthErr != nil {
		return "", f.AuthErr
	}
	return f.Account, nil
}

func (f *FakeClient) Get(_ context.Context, project string) (*provider.Repository, error) {
	f.Calls = append(f.Calls, "Get:"+project)
	if err := f.GetErrors[project]; err != nil {
		return nil, err
	}
	if repository, ok := f.GetRepos[project]; ok {
		return repository, nil
	}
	if f.GetErr != nil {
		return nil, f.GetErr
	}
	return f.GetRepo, nil
}

func (f *FakeClient) List(_ context.Context) ([]provider.Repository, error) {
	f.Calls = append(f.Calls, "List")
	if f.ListHook != nil {
		if err := f.ListHook(); err != nil {
			return nil, err
		}
	}
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	return f.ListRepos, nil
}

func (f *FakeClient) Create(_ context.Context, project string, visibility ...string) (*provider.Repository, error) {
	f.Calls = append(f.Calls, "Create:"+project)
	if len(visibility) > 0 {
		f.Visibility = visibility[0]
	}
	if f.CreateErr != nil {
		return nil, f.CreateErr
	}
	if f.CreateFunc != nil {
		return f.CreateFunc(project)
	}
	return f.CreateRepo, nil
}

func (f *FakeClient) Release(_ context.Context, version, tag string) error {
	f.Calls = append(f.Calls, "Release:"+version+":"+tag)
	return f.ReleaseErr
}

func (f *FakeClient) Revoke(context.Context, provider.RevocationOptions) error {
	f.Calls = append(f.Calls, "Revoke")
	return f.RevokeErr
}

func (f *FakeClient) Called(op string) bool {
	for _, c := range f.Calls {
		if c == op || strings.HasPrefix(c, op+":") {
			return true
		}
	}
	return false
}

type FakeCredentialStore struct {
	Credentials         map[string]credential.Credential
	GetErr              error
	DeleteErr           error
	Gets, Puts, Deletes int
	DeletedIDs          []string
	Events              *[]string
}

func NewFakeCredentialStore() *FakeCredentialStore {
	return &FakeCredentialStore{Credentials: map[string]credential.Credential{}}
}

func (s *FakeCredentialStore) Get(id string) (credential.Credential, error) {
	s.Gets++
	if s.GetErr != nil {
		return credential.Credential{}, s.GetErr
	}
	value, ok := s.Credentials[id]
	if !ok {
		return credential.Credential{}, credential.ErrNotFound
	}
	return value, nil
}

func (s *FakeCredentialStore) Put(id string, value credential.Credential) error {
	s.Puts++
	if s.Events != nil {
		*s.Events = append(*s.Events, "persist")
	}
	s.Credentials[id] = value
	return nil
}

func (s *FakeCredentialStore) Create(id string, value credential.Credential) error {
	if _, exists := s.Credentials[id]; exists {
		return credential.ErrAlreadyExists
	}
	return s.Put(id, value)
}

func (s *FakeCredentialStore) Delete(id string) error {
	s.Deletes++
	s.DeletedIDs = append(s.DeletedIDs, id)
	if s.DeleteErr != nil {
		return s.DeleteErr
	}
	if _, ok := s.Credentials[id]; !ok {
		return credential.ErrNotFound
	}
	delete(s.Credentials, id)
	return nil
}

func (s *FakeCredentialStore) DeleteIf(id string, expected credential.Credential) (bool, error) {
	current, ok := s.Credentials[id]
	if !ok || current.Kind != expected.Kind || current.Secret != expected.Secret {
		return false, nil
	}
	return true, s.Delete(id)
}
