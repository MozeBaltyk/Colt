package credential

import (
	"errors"
	"os"
	"sync"

	keyring "github.com/99designs/keyring"
)

// SecureStore uses only native OS credential facilities. In particular, the
// dependency's command-backed and file backends are never eligible.
type SecureStore struct {
	ring    keyring.Keyring
	openErr error
	once    sync.Once
}

var secureMutationMu sync.Mutex

func NewSecureStore() *SecureStore { return &SecureStore{} }

func (s *SecureStore) open() error {
	s.once.Do(func() {
		if s.ring != nil || s.openErr != nil {
			return
		}
		s.ring, s.openErr = keyring.Open(keyring.Config{
			ServiceName: "colt",
			AllowedBackends: []keyring.BackendType{
				keyring.WinCredBackend,
				keyring.KeychainBackend,
				keyring.SecretServiceBackend,
			},
		})
	})
	return s.openErr
}

func (s *SecureStore) Get(id string) (Credential, error) {
	if err := ValidateID(id); err != nil {
		return Credential{}, err
	}
	if s.open() != nil {
		return Credential{}, ErrStoreUnavailable
	}
	item, err := s.ring.Get(id)
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, ErrStoreUnavailable
	}
	cred := Credential{Kind: "bearer_token", Secret: string(item.Data)}
	if validateCredential(cred) != nil {
		return Credential{}, errors.New("secure credential is invalid")
	}
	return cred, nil
}

func (s *SecureStore) Put(id string, cred Credential) error {
	return s.put(id, cred, false)
}

func (s *SecureStore) Create(id string, cred Credential) error {
	return s.put(id, cred, true)
}

func (s *SecureStore) put(id string, cred Credential, create bool) (retErr error) {
	if err := ValidateID(id); err != nil {
		return err
	}
	if validateCredential(cred) != nil {
		return errors.New("refusing to store an invalid credential")
	}
	if s.open() != nil {
		return ErrStoreUnavailable
	}
	secureMutationMu.Lock()
	defer secureMutationMu.Unlock()
	if create {
		if _, err := s.ring.Get(id); err == nil {
			return ErrAlreadyExists
		} else if !errors.Is(err, keyring.ErrKeyNotFound) {
			return ErrStoreUnavailable
		}
	}
	if err := s.ring.Set(keyring.Item{Key: id, Data: []byte(cred.Secret), Label: id, Description: "Colt provider credential"}); err != nil {
		return ErrPersistenceUncertain
	}
	return nil
}

func (s *SecureStore) Delete(id string) (retErr error) {
	if err := ValidateID(id); err != nil {
		return err
	}
	if s.open() != nil {
		return ErrStoreUnavailable
	}
	secureMutationMu.Lock()
	defer secureMutationMu.Unlock()
	return s.deleteLocked(id)
}

func (s *SecureStore) DeleteIf(id string, cred Credential) (deleted bool, retErr error) {
	if err := ValidateID(id); err != nil {
		return false, err
	}
	if err := validateCredential(cred); err != nil {
		return false, errors.New("invalid credential comparison")
	}
	if s.open() != nil {
		return false, ErrStoreUnavailable
	}
	secureMutationMu.Lock()
	defer secureMutationMu.Unlock()
	item, err := s.ring.Get(id)
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return false, nil
	}
	if err != nil {
		return false, ErrStoreUnavailable
	}
	if !sameCredential(Credential{Kind: "bearer_token", Secret: string(item.Data)}, cred) {
		return false, nil
	}
	if err := s.deleteLocked(id); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SecureStore) deleteLocked(id string) error {
	removeErr := s.ring.Remove(id)
	_, getErr := s.ring.Get(id)
	if getErr == nil {
		return ErrStoreUnavailable
	}
	if !errors.Is(getErr, keyring.ErrKeyNotFound) {
		return ErrStoreUnavailable
	}
	if errors.Is(removeErr, keyring.ErrKeyNotFound) {
		return ErrNotFound
	}
	return nil
}

var ErrPartialDelete = errors.New("credential removal partially failed")

// PersistentStore resolves secure-store-first and consults the plaintext
// fallback only when its consent-created file already exists.
type PersistentStore struct {
	Secure   Store
	Fallback *FileStore
}

func (s PersistentStore) Get(id string) (Credential, error) {
	cred, secureErr := s.Secure.Get(id)
	if secureErr == nil {
		return cred, nil
	}
	if !errors.Is(secureErr, ErrNotFound) && !errors.Is(secureErr, ErrStoreUnavailable) {
		return Credential{}, secureErr
	}
	present, err := s.Fallback.present()
	if err != nil {
		return Credential{}, err
	}
	if !present {
		return Credential{}, secureErr
	}
	return s.Fallback.Get(id)
}

func (s PersistentStore) Put(id string, cred Credential) error    { return s.Secure.Put(id, cred) }
func (s PersistentStore) Create(id string, cred Credential) error { return s.Secure.Create(id, cred) }

func (s PersistentStore) DeleteIf(id string, cred Credential) (bool, error) {
	deleted, secureErr := s.Secure.DeleteIf(id, cred)
	present, inspectErr := s.Fallback.present()
	if inspectErr != nil {
		return deleted, inspectErr
	}
	if !present {
		return deleted, secureErr
	}
	fallbackDeleted, fallbackErr := s.Fallback.DeleteIf(id, cred)
	if secureErr != nil || fallbackErr != nil {
		return deleted || fallbackDeleted, ErrPartialDelete
	}
	return deleted || fallbackDeleted, nil
}

func (s PersistentStore) Delete(id string) error {
	secureErr := s.Secure.Delete(id)
	present, inspectErr := s.Fallback.present()
	if inspectErr != nil {
		if secureErr == nil {
			return ErrPartialDelete
		}
		return inspectErr
	}
	var fallbackErr error = ErrNotFound
	if present {
		fallbackErr = s.Fallback.Delete(id)
	}
	secureRemoved, fallbackRemoved := secureErr == nil, fallbackErr == nil
	if secureRemoved || fallbackRemoved {
		if (secureErr != nil && !errors.Is(secureErr, ErrNotFound)) || (fallbackErr != nil && !errors.Is(fallbackErr, ErrNotFound)) {
			return ErrPartialDelete
		}
		return nil
	}
	if errors.Is(secureErr, ErrNotFound) && errors.Is(fallbackErr, ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(secureErr, ErrStoreUnavailable) {
		return ErrStoreUnavailable
	}
	return errors.New("credential removal failed")
}

func (s *FileStore) present() (bool, error) {
	_, err := os.Lstat(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fileError("inspect credential file", err)
	}
	return true, nil
}
