package fixture

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/credential"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
)

// FakeGit records calls; with Real=true it delegates to native git.
type FakeGit struct {
	Real         bool
	AvailableErr error
	PushErr      error
	Origins      []string
	Pushes       int
	PushURL      string
	Helpers      int
	Operations   []string
	OriginURL    string
	OriginErr    error
	LsRemoteErr  error
	ProbeBounded bool
	ProbeAlias   string
	ProbeProject string
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
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		return fmt.Errorf("fake git init: %s: %v", out, err)
	}
	f.Origins = append(f.Origins, url)
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
	safe := func(value string) bool {
		return value != "" && strings.IndexFunc(value, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-')
		}) < 0
	}
	if !safe(alias) || !safe(project) {
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

type FakeClient struct {
	Account    string
	AuthErr    error
	GetRepo    *provider.Repository
	GetErr     error
	CreateRepo *provider.Repository
	CreateErr  error
	Visibility string
	RevokeErr  error
	Calls      []string
	Events     *[]string
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
	if f.GetErr != nil {
		return nil, f.GetErr
	}
	return f.GetRepo, nil
}

func (f *FakeClient) Create(_ context.Context, project string, visibility ...string) (*provider.Repository, error) {
	f.Calls = append(f.Calls, "Create:"+project)
	if len(visibility) > 0 {
		f.Visibility = visibility[0]
	}
	if f.CreateErr != nil {
		return nil, f.CreateErr
	}
	return f.CreateRepo, nil
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
