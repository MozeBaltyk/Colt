package fixture

import (
	"context"
	"os"
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
	PushToken    string
	Helpers      int
	Operations   []string
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

func (f *FakeGit) Init(ctx context.Context, dir string) error {
	f.Operations = append(f.Operations, "init")
	if f.Real {
		return gitnative.Native{}.Init(ctx, dir)
	}
	return os.MkdirAll(filepath.Join(dir, ".git"), 0o755) // ponytail: marker only, no history
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

func (f *FakeGit) ConfigureCredentialHelper(ctx context.Context, dir, cloneURL, username string) error {
	f.Operations = append(f.Operations, "helper")
	if f.Real {
		if err := (gitnative.Native{}).ConfigureCredentialHelper(ctx, dir, cloneURL, username); err != nil {
			return err
		}
	}
	f.Helpers++
	return nil
}

func (f *FakeGit) Push(_ context.Context, _ /* dir */, url, _, _, token string) error {
	f.Operations = append(f.Operations, "push")
	if f.PushErr != nil {
		return f.PushErr
	}
	f.Pushes++
	f.PushURL, f.PushToken = url, token
	return nil
}

type FakeClient struct {
	Account    string
	AuthErr    error
	GetRepo    *provider.Repository
	GetErr     error
	CreateRepo *provider.Repository
	CreateErr  error
	Calls      []string
}

func (f *FakeClient) Authenticate(context.Context) (string, error) {
	f.Calls = append(f.Calls, "Authenticate")
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

func (f *FakeClient) Create(_ context.Context, project string) (*provider.Repository, error) {
	f.Calls = append(f.Calls, "Create:"+project)
	if f.CreateErr != nil {
		return nil, f.CreateErr
	}
	return f.CreateRepo, nil
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
	s.Credentials[id] = value
	return nil
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
