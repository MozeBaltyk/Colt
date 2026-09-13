package credential

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	keyring "github.com/99designs/keyring"
)

func TestStoresCreateIsAtomic(t *testing.T) {
	backend := &fakeKeyring{items: map[string]keyring.Item{}}
	memory := NewMemoryStore()
	filePath := filepath.Join(secureTempDir(t), "credentials")
	lockDir := t.TempDir()
	stores := map[string][2]Store{
		"memory": {memory, memory},
		"file":   {NewFileStore(filePath), NewFileStore(filePath)},
		"secure": {&SecureStore{ring: backend, lockDir: lockDir}, &SecureStore{ring: backend, lockDir: lockDir}},
	}
	for name, pair := range stores {
		t.Run(name, func(t *testing.T) {
			start := make(chan struct{})
			errs := make(chan error, 2)
			var wg sync.WaitGroup
			for i, secret := range []string{"first", "second"} {
				wg.Add(1)
				go func(store Store, secret string) {
					defer wg.Done()
					<-start
					errs <- store.Create("github.com/personal", Credential{Kind: "bearer_token", Secret: secret})
				}(pair[i], secret)
			}
			close(start)
			wg.Wait()
			close(errs)
			succeeded, existed := 0, 0
			for err := range errs {
				switch {
				case err == nil:
					succeeded++
				case errors.Is(err, ErrAlreadyExists):
					existed++
				default:
					t.Fatalf("Create() = %v", err)
				}
			}
			if succeeded != 1 || existed != 1 {
				t.Fatalf("successes=%d already-exists=%d", succeeded, existed)
			}
			original, err := pair[0].Get("github.com/personal")
			if err != nil {
				t.Fatal(err)
			}
			replacement := Credential{Kind: "bearer_token", Secret: "replacement"}
			start = make(chan struct{})
			errs = make(chan error, 2)
			go func() {
				<-start
				errs <- pair[0].Put("github.com/personal", replacement)
			}()
			go func() {
				<-start
				_, err := pair[1].DeleteIf("github.com/personal", original)
				errs <- err
			}()
			close(start)
			for range 2 {
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
			}
			if got, err := pair[0].Get("github.com/personal"); err != nil || !sameCredential(got, replacement) {
				t.Fatalf("replacement lost: credential=%+v error=%v", got, err)
			}
		})
	}
}

type fakeKeyring struct {
	items           map[string]keyring.Item
	getErr          error
	setErr          error
	removeErr       error
	keepAfterRemove bool
}

func (f *fakeKeyring) Get(id string) (keyring.Item, error) {
	if f.getErr != nil {
		return keyring.Item{}, f.getErr
	}
	item, ok := f.items[id]
	if !ok {
		return keyring.Item{}, keyring.ErrKeyNotFound
	}
	return item, nil
}
func (*fakeKeyring) GetMetadata(string) (keyring.Metadata, error) { return keyring.Metadata{}, nil }
func (f *fakeKeyring) Set(item keyring.Item) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.items[item.Key] = item
	return nil
}
func (f *fakeKeyring) Remove(id string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	if _, ok := f.items[id]; !ok {
		return keyring.ErrKeyNotFound
	}
	if !f.keepAfterRemove {
		delete(f.items, id)
	}
	return nil
}

func TestSecureStoreDeleteVerifiesAbsence(t *testing.T) {
	backend := &fakeKeyring{items: map[string]keyring.Item{"github.com/personal": {Data: []byte("secret")}}, keepAfterRemove: true}
	if err := (&SecureStore{ring: backend, lockDir: t.TempDir()}).Delete("github.com/personal"); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("Delete() = %v", err)
	}
}

func TestSecureStoreRejectsCRLFAndTreatsSetFailureAsUncertain(t *testing.T) {
	backend := &fakeKeyring{items: map[string]keyring.Item{}, setErr: errors.New("backend failed")}
	store := &SecureStore{ring: backend, lockDir: t.TempDir()}
	if err := store.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "bad\nsecret"}); err == nil {
		t.Fatal("SecureStore accepted bearer token with CR/LF")
	}
	if err := store.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "safe"}); !errors.Is(err, ErrPersistenceUncertain) {
		t.Fatalf("Set failure = %v", err)
	}
}
func (f *fakeKeyring) Keys() ([]string, error) { return nil, nil }

func TestSecureStoreMapsBackendErrorsWithoutLeakingThem(t *testing.T) {
	secret := "secure-test-secret"
	backend := &fakeKeyring{items: map[string]keyring.Item{}}
	store := &SecureStore{ring: backend, lockDir: t.TempDir()}
	if err := store.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: secret}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("github.com/personal")
	if err != nil || got.Secret != secret {
		t.Fatalf("Get() = %+v, %v", got, err)
	}
	backend.getErr = errors.New("backend detail " + secret)
	if _, err := store.Get("github.com/personal"); !errors.Is(err, ErrStoreUnavailable) || err.Error() != ErrStoreUnavailable.Error() {
		t.Fatalf("unsafe mapped error = %v", err)
	}
}

func TestPersistentStoreSecureFirstFallbackAndExactDelete(t *testing.T) {
	dir := secureTempDir(t)
	fallback := NewFileStore(filepath.Join(dir, "credentials"))
	secure := NewMemoryStore()
	if err := secure.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "secure"}); err != nil {
		t.Fatal(err)
	}
	if err := fallback.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "fallback"}); err != nil {
		t.Fatal(err)
	}
	if err := fallback.Put("github.com/work", Credential{Kind: "bearer_token", Secret: "neighbor"}); err != nil {
		t.Fatal(err)
	}
	store := PersistentStore{Secure: secure, Fallback: fallback}
	if got, err := store.Get("github.com/personal"); err != nil || got.Secret != "secure" {
		t.Fatalf("secure-first Get() = %+v, %v", got, err)
	}
	if err := store.Delete("github.com/personal"); err != nil {
		t.Fatal(err)
	}
	if _, err := secure.Get("github.com/personal"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("secure credential remains: %v", err)
	}
	if _, err := fallback.Get("github.com/personal"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fallback credential remains: %v", err)
	}
	if got, err := fallback.Get("github.com/work"); err != nil || got.Secret != "neighbor" {
		t.Fatalf("neighbor changed: %+v, %v", got, err)
	}
}

func TestPersistentStoreReportsPartialDeleteSafely(t *testing.T) {
	fallback := NewFileStore(filepath.Join(secureTempDir(t), "credentials"))
	if err := fallback.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "secret"}); err != nil {
		t.Fatal(err)
	}
	err := (PersistentStore{Secure: DisabledStore{}, Fallback: fallback}).Delete("github.com/personal")
	if !errors.Is(err, ErrPartialDelete) || err.Error() != ErrPartialDelete.Error() {
		t.Fatalf("partial delete error = %v", err)
	}
}

type deleteStore struct{ err error }

func (s deleteStore) Get(string) (Credential, error)  { return Credential{}, ErrNotFound }
func (s deleteStore) Put(string, Credential) error    { return nil }
func (s deleteStore) Create(string, Credential) error { return nil }
func (s deleteStore) Delete(string) error             { return s.err }
func (s deleteStore) DeleteIf(string, Credential) (bool, error) {
	return false, s.err
}

func TestPersistentStoreDeletionMatrix(t *testing.T) {
	tests := []struct {
		name           string
		secureErr      error
		fallback       bool
		unsafeFallback bool
		want           error
		wantText       string
	}{
		{"absent everywhere", ErrNotFound, false, false, ErrNotFound, ""},
		{"secure only", nil, false, false, nil, ""},
		{"fallback only", ErrNotFound, true, false, nil, ""},
		{"secure unavailable after fallback deletion", ErrStoreUnavailable, true, false, ErrPartialDelete, ""},
		{"secure deleted but fallback unsafe", nil, true, true, ErrPartialDelete, ""},
		{"secure unavailable and no fallback", ErrStoreUnavailable, false, false, ErrStoreUnavailable, ""},
		{"secure failure and no fallback", errors.New("delete failed"), false, false, nil, "credential removal failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fallback := NewFileStore(filepath.Join(secureTempDir(t), "credentials"))
			if tc.fallback {
				if err := fallback.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "secret"}); err != nil {
					t.Fatal(err)
				}
				if tc.unsafeFallback {
					if err := os.Chmod(fallback.Path, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := (PersistentStore{Secure: deleteStore{err: tc.secureErr}, Fallback: fallback}).Delete("github.com/personal")
			wrong := tc.wantText != "" && (err == nil || err.Error() != tc.wantText)
			wrong = wrong || tc.wantText == "" && tc.want == nil && err != nil
			wrong = wrong || tc.want != nil && !errors.Is(err, tc.want)
			if wrong {
				t.Fatalf("Delete() = %v, want %v", err, tc.want)
			}
		})
	}
}
