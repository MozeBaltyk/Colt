// Package credential implements Colt's provider-independent credential
// subsystem: deterministic resolution, secret isolation, backend
// independence, and safe deletion.
//
// Provider implementations consume an already-resolved Credential and
// never touch the environment, the OS secure store, or the fallback
// file directly. All errors in this package carry only non-secret
// names and identifiers, never secret values.
package credential

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
)

// Source identifies where a resolved credential came from. The provider
// layer sees only environment vs stored; which persistent backend owns
// a stored credential is a Colt-internal detail.
type Source string

const (
	SourceEnvironment Source = "environment"
	SourceStored      Source = "stored"
)

// Credential is the common result every credential source resolves to.
type Credential struct {
	Kind   string // e.g. "bearer_token"
	Secret string
	Source Source
}

// ErrNotFound reports that no persisted credential exists for an ID.
// ErrMissing reports that no credential source resolved for a provider.
var (
	ErrNotFound             = errors.New("credential not found")
	ErrMissing              = errors.New("credentials missing")
	ErrStoreUnavailable     = errors.New("credential store unavailable")
	ErrPersistenceUncertain = errors.New("credential persistence outcome is uncertain")
	ErrAlreadyExists        = errors.New("credential already exists")
	idPartRE                = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)
)

func validateCredential(cred Credential) error {
	if strings.TrimSpace(cred.Kind) == "" || cred.Secret == "" {
		return errors.New("credential kind and secret must not be empty")
	}
	if cred.Kind == "bearer_token" && strings.ContainsAny(cred.Secret, "\r\n") {
		return errors.New("bearer token must not contain CR or LF")
	}
	return nil
}

func ValidateBearerToken(secret string) error {
	return validateCredential(Credential{Kind: "bearer_token", Secret: secret})
}

// Store persists Colt-owned credentials by stable non-secret ID.
// Implementations: OS secure store, plaintext fallback file, in-memory
// fake for tests.
type Store interface {
	Get(id string) (Credential, error)
	Put(id string, cred Credential) error
	// Create stores a credential only when id is absent and returns
	// ErrAlreadyExists otherwise. The check and write are one atomic operation.
	Create(id string, cred Credential) error
	Delete(id string) error
	// DeleteIf removes id only when its current value matches cred.
	DeleteIf(id string, cred Credential) (bool, error)
}

// DisabledStore keeps stored credentials explicitly disabled until a caller
// supplies an approved persistent backend.
type DisabledStore struct{}

func (DisabledStore) Get(string) (Credential, error)  { return Credential{}, ErrStoreUnavailable }
func (DisabledStore) Put(string, Credential) error    { return ErrStoreUnavailable }
func (DisabledStore) Create(string, Credential) error { return ErrStoreUnavailable }
func (DisabledStore) Delete(string) error             { return ErrStoreUnavailable }
func (DisabledStore) DeleteIf(string, Credential) (bool, error) {
	return false, ErrStoreUnavailable
}

// ValidateID checks the deterministic `<provider-host>/<provider-alias>`
// form. It validates shape only; the ID must never contain secret
// material, which shape alone cannot prove.
func ValidateID(id string) error {
	host, alias, ok := strings.Cut(id, "/")
	if !ok || strings.Contains(alias, "/") {
		return errors.New("credential ID must have the form <provider-host>/<provider-alias>")
	}
	if !idPartRE.MatchString(host) || !idPartRE.MatchString(alias) || host == "." || host == ".." || alias == "." || alias == ".." {
		return errors.New("credential ID contains an unsafe host or alias")
	}
	return nil
}

// Resolver resolves a provider credential in the normative order:
//
//  1. explicitly configured token_env (missing/empty fails, no fallthrough)
//  2. conventional provider variable (for example GITHUB_TOKEN, empty falls through)
//  3. persisted Colt credential for credentialID (secure store, then fallback)
//  4. otherwise credentials missing
//
// An environment credential selected for one resolution is never written
// to persistent storage by this type.
type Resolver struct {
	Store Store
	// LookupEnv overrides os.Getenv; nil means os.Getenv. Tests use a map.
	LookupEnv func(string) string
}

func (r Resolver) getenv(name string) string {
	if r.LookupEnv != nil {
		return r.LookupEnv(name)
	}
	return os.Getenv(name)
}

// ConventionalVar returns the provider-default environment variable.
func ConventionalVar(providerType string) string {
	switch providerType {
	case "github":
		return "GITHUB_TOKEN"
	case "gitlab":
		return "GITLAB_TOKEN"
	case "gitea":
		return "GITEA_TOKEN"
	case "forgejo":
		return "FORGEJO_TOKEN"
	default:
		return ""
	}
}

// Resolve returns the credential for one provider. tokenEnv is the
// explicitly configured variable (empty when unconfigured);
// credentialID is the stable stored-credential identifier (empty when
// the provider uses environment authentication only).
func (r Resolver) Resolve(providerType, tokenEnv, credentialID string) (Credential, error) {
	if tokenEnv != "" {
		secret := r.getenv(tokenEnv)
		if secret == "" {
			return Credential{}, fmt.Errorf("%w: credential environment variable %s is missing or empty", ErrMissing, tokenEnv)
		}
		cred := Credential{Kind: "bearer_token", Secret: secret, Source: SourceEnvironment}
		if err := validateCredential(cred); err != nil {
			return Credential{}, fmt.Errorf("invalid bearer token in credential environment variable %s", tokenEnv)
		}
		return cred, nil
	}
	if name := ConventionalVar(providerType); name != "" {
		if secret := r.getenv(name); secret != "" {
			cred := Credential{Kind: "bearer_token", Secret: secret, Source: SourceEnvironment}
			if err := validateCredential(cred); err != nil {
				return Credential{}, fmt.Errorf("invalid bearer token in credential environment variable %s", name)
			}
			return cred, nil
		}
	}
	if credentialID != "" {
		if err := ValidateID(credentialID); err != nil {
			return Credential{}, err
		}
		if r.Store == nil {
			return Credential{}, fmt.Errorf("%w for %s", ErrStoreUnavailable, credentialID)
		}
		cred, err := r.Store.Get(credentialID)
		if errors.Is(err, ErrNotFound) {
			return Credential{}, fmt.Errorf("%w: no stored credential for %s", ErrMissing, credentialID)
		}
		if err != nil {
			return Credential{}, err
		}
		if validateCredential(cred) != nil {
			return Credential{}, errors.New("stored credential is invalid for " + credentialID)
		}
		cred.Source = SourceStored
		return cred, nil
	}
	return Credential{}, fmt.Errorf("%w: no environment credential or stored credential configured", ErrMissing)
}

// MemoryStore is a mutex-guarded in-memory Store. Production code uses
// it as the test double for the secure store and fallback backends.
type MemoryStore struct {
	mu    sync.Mutex
	creds map[string]Credential
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{creds: map[string]Credential{}}
}

func (m *MemoryStore) Get(id string) (Credential, error) {
	if err := ValidateID(id); err != nil {
		return Credential{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cred, ok := m.creds[id]
	if !ok {
		return Credential{}, ErrNotFound
	}
	return cred, nil
}

func (m *MemoryStore) Put(id string, cred Credential) error {
	return m.put(id, cred, false)
}

func (m *MemoryStore) Create(id string, cred Credential) error {
	return m.put(id, cred, true)
}

func (m *MemoryStore) put(id string, cred Credential, create bool) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	if validateCredential(cred) != nil {
		return errors.New("refusing to store an invalid credential for " + id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.creds[id]; create && exists {
		return ErrAlreadyExists
	}
	m.creds[id] = cred
	return nil
}

// Delete removes exactly one credential ID. A missing ID reports
// ErrNotFound so callers can distinguish "nothing to remove" from
// successful removal; it never touches any other ID.
func (m *MemoryStore) Delete(id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.creds[id]; !ok {
		return ErrNotFound
	}
	delete(m.creds, id)
	return nil
}

func (m *MemoryStore) DeleteIf(id string, cred Credential) (bool, error) {
	if err := ValidateID(id); err != nil {
		return false, err
	}
	if err := validateCredential(cred); err != nil {
		return false, errors.New("invalid credential comparison")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.creds[id]
	if !ok || !sameCredential(current, cred) {
		return false, nil
	}
	delete(m.creds, id)
	return true, nil
}

func sameCredential(a, b Credential) bool {
	return a.Kind == b.Kind && subtle.ConstantTimeCompare([]byte(a.Secret), []byte(b.Secret)) == 1
}
