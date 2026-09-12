package credential

import (
	"errors"
	"strings"
	"testing"
)

func envOf(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestResolveOrderExplicitConventionalStored(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "stored-secret"}); err != nil {
		t.Fatal(err)
	}
	r := Resolver{Store: store, LookupEnv: envOf(map[string]string{
		"WORK_TOKEN":   "explicit-secret",
		"GITHUB_TOKEN": "conventional-secret",
	})}
	got, err := r.Resolve("github", "WORK_TOKEN", "github.com/personal")
	if err != nil || got.Secret != "explicit-secret" || got.Source != SourceEnvironment {
		t.Fatalf("explicit = %+v, %v", got, err)
	}
	r.LookupEnv = envOf(map[string]string{"GITHUB_TOKEN": "conventional-secret"})
	got, err = r.Resolve("github", "", "github.com/personal")
	if err != nil || got.Secret != "conventional-secret" || got.Source != SourceEnvironment {
		t.Fatalf("conventional = %+v, %v", got, err)
	}
	if err := store.Delete("github.com/personal"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "stored-secret"}); err != nil {
		t.Fatal(err)
	}
	r.LookupEnv = envOf(nil)
	got, err = r.Resolve("github", "", "github.com/personal")
	if err != nil || got.Secret != "stored-secret" || got.Source != SourceStored {
		t.Fatalf("stored = %+v, %v", got, err)
	}
}

func TestResolveExplicitMissingFailsWithoutFallthrough(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "stored-secret"}); err != nil {
		t.Fatal(err)
	}
	r := Resolver{Store: store, LookupEnv: envOf(map[string]string{"GITHUB_TOKEN": "conventional-secret"})}
	for _, tokenEnv := range []string{"MISSING_TOKEN", "EMPTY_TOKEN"} {
		if _, err := r.Resolve("github", tokenEnv, "github.com/personal"); err == nil || !strings.Contains(err.Error(), tokenEnv) {
			t.Fatalf("tokenEnv %q error = %v", tokenEnv, err)
		}
		if _, err := store.Get("github.com/personal"); err != nil {
			t.Fatalf("failed resolution disturbed stored credential: %v", err)
		}
	}
}

func TestResolveConventionalEmptyFallsThroughToStored(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Put("gitlab.com/work", Credential{Kind: "bearer_token", Secret: "stored-secret"}); err != nil {
		t.Fatal(err)
	}
	r := Resolver{Store: store, LookupEnv: envOf(map[string]string{"GITLAB_TOKEN": ""})}
	got, err := r.Resolve("gitlab", "", "gitlab.com/work")
	if err != nil || got.Secret != "stored-secret" || got.Source != SourceStored {
		t.Fatalf("fallthrough = %+v, %v", got, err)
	}
}

func TestResolveMissingReportsIDWithoutSecrets(t *testing.T) {
	r := Resolver{Store: NewMemoryStore(), LookupEnv: envOf(nil)}
	_, err := r.Resolve("github", "", "github.com/personal")
	if err == nil || !strings.Contains(err.Error(), "github.com/personal") {
		t.Fatalf("error = %v", err)
	}
	_, err = r.Resolve("gitlab", "", "")
	if err == nil || !errors.Is(err, ErrMissing) && !strings.Contains(err.Error(), "credentials missing") {
		t.Fatalf("error = %v", err)
	}
}

func TestStoreDeleteTargetsExactID(t *testing.T) {
	store := NewMemoryStore()
	ids := []string{"github.com/personal", "github.com/work", "gitlab.com/personal"}
	for _, id := range ids {
		if err := store.Put(id, Credential{Kind: "bearer_token", Secret: "secret-for-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Delete("github.com/personal"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("github.com/personal"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted ID still resolves: %v", err)
	}
	for _, id := range ids[1:] {
		got, err := store.Get(id)
		if err != nil || got.Secret != "secret-for-"+id {
			t.Fatalf("neighbor %q = %+v, %v", id, got, err)
		}
	}
	if err := store.Delete("github.com/personal"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestStoreRefusesEmptySecretAndBadIDs(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Put("github.com/personal", Credential{Kind: "bearer_token"}); err == nil {
		t.Fatal("stored empty secret")
	}
	for _, id := range []string{"", "no-slash", "a/b/c", "/alias", "host/", "host/white space"} {
		if err := ValidateID(id); err == nil {
			t.Fatalf("accepted ID %q", id)
		}
		if _, err := store.Get(id); err == nil {
			t.Fatalf("store accepted ID %q", id)
		}
	}
	for _, id := range []string{"github.com/personal", "gitlab.com/work", "git.example.org/company"} {
		if err := ValidateID(id); err != nil {
			t.Fatalf("rejected ID %q: %v", id, err)
		}
	}
}

func TestErrorsNeverContainSecrets(t *testing.T) {
	secrets := []string{"explicit-secret-1", "conventional-secret-1", "stored-secret-1"}
	store := NewMemoryStore()
	if err := store.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: secrets[2]}); err != nil {
		t.Fatal(err)
	}
	r := Resolver{Store: store, LookupEnv: envOf(map[string]string{
		"EXPLICIT_TOKEN": secrets[0],
		"GITHUB_TOKEN":   secrets[1],
	})}
	_, errExplicit := r.Resolve("github", "MISSING_TOKEN", "github.com/personal")
	_, errMissing := r.Resolve("github", "", "github.com/absent")
	errDelete := store.Delete("github.com/absent")
	for _, err := range []error{errExplicit, errMissing, errDelete} {
		for _, s := range secrets {
			if err != nil && strings.Contains(err.Error(), s) {
				t.Fatalf("error %q contains secret", err)
			}
		}
	}
}
