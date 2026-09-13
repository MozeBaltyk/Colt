package credential

import (
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(name string) string { return m[name] }
}

func TestBearerTokensRejectCRLFAtResolutionAndStorageBoundaries(t *testing.T) {
	for _, secret := range []string{"token\nnext", "token\rnext"} {
		t.Run(strings.ReplaceAll(secret, "token", ""), func(t *testing.T) {
			if _, err := (Resolver{LookupEnv: envOf(map[string]string{"GITHUB_TOKEN": secret})}).Resolve("github", "", ""); err == nil {
				t.Fatal("environment bearer token with CR/LF resolved")
			}
			store := NewMemoryStore()
			if err := store.Put("github.com/personal", Credential{Kind: "bearer_token", Secret: secret}); err == nil {
				t.Fatal("bearer token with CR/LF was stored")
			}
			store.creds["github.com/personal"] = Credential{Kind: "bearer_token", Secret: secret}
			if _, err := (Resolver{Store: store, LookupEnv: envOf(nil)}).Resolve("github", "", "github.com/personal"); err == nil {
				t.Fatal("stored bearer token with CR/LF resolved")
			}
		})
	}
}

func TestForgejoUsesConventionalEnvironmentToken(t *testing.T) {
	r := Resolver{LookupEnv: envOf(map[string]string{"FORGEJO_TOKEN": "forgejo-secret"})}
	got, err := r.Resolve("forgejo", "", "")
	if err != nil || got.Secret != "forgejo-secret" || got.Source != SourceEnvironment {
		t.Fatalf("Resolve() = %+v, %v", got, err)
	}
}
