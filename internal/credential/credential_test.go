package credential

import (
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(name string) string { return m[name] }
}

func TestForgejoUsesConventionalEnvironmentToken(t *testing.T) {
	r := Resolver{LookupEnv: envOf(map[string]string{"FORGEJO_TOKEN": "forgejo-secret"})}
	got, err := r.Resolve("forgejo", "", "")
	if err != nil || got.Secret != "forgejo-secret" || got.Source != SourceEnvironment {
		t.Fatalf("Resolve() = %+v, %v", got, err)
	}
}
