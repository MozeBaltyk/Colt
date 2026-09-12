package fixture

import (
	"os"
	"slices"
	"testing"
)

func TestSplitCmd(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
		err  bool
	}{
		{"double quoted spaces", `colt auth --git-name "Example User"`, []string{"colt", "auth", "--git-name", "Example User"}, false},
		{"single quoted spaces", `colt auth --git-name 'Example User'`, []string{"colt", "auth", "--git-name", "Example User"}, false},
		{"empty quotes", `colt init "" ''`, []string{"colt", "init", "", ""}, false},
		{"tabs", "colt\tinit\tdemo", []string{"colt", "init", "demo"}, false},
		{"escaped space", `colt init Example\ User`, []string{"colt", "init", "Example User"}, false},
		{"escaped quote", `colt init "Example \"User\""`, []string{"colt", "init", `Example "User"`}, false},
		{"unterminated quote", `colt init "demo`, nil, true},
		{"dangling escape", `colt init demo\`, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SplitCmd(tc.in)
			if (err != nil) != tc.err || !slices.Equal(got, tc.want) {
				t.Fatalf("splitCmd(%q) = %#v, %v; want %#v, error=%v", tc.in, got, err, tc.want, tc.err)
			}
		})
	}
}

func TestCredentialEnvBoundary(t *testing.T) {
	for _, name := range ManagedEnvVars {
		t.Setenv(name, "preserved")
	}
	if err := UnsetCredentialEnv(); err != nil {
		t.Fatal(err)
	}
	for _, name := range CredentialEnvVars {
		if _, ok := os.LookupEnv(name); ok {
			t.Errorf("credential environment %s was not unset", name)
		}
	}
	for _, name := range SandboxEnvVars {
		if got := os.Getenv(name); got != "preserved" {
			t.Errorf("sandbox environment %s = %q, want preserved", name, got)
		}
	}
}
