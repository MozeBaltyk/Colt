package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validProvider(kind string) Provider {
	p := Provider{Type: kind, Namespace: "team", Visibility: "private", GitName: "Colt User", GitEmail: "colt@example.com"}
	if kind == "github" {
		p.Host, p.BaseURL = "github.com", "https://api.github.com"
	} else {
		p.Host, p.BaseURL = "gitlab.com", "https://gitlab.com"
	}
	return p
}

func TestCORE_PROVIDER_001ConfigPathOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom.yaml")
	t.Setenv("COLT_CONFIG", want)
	got, err := Path()
	if err != nil || got != want {
		t.Fatalf("Path() = %q, %v; want %q", got, err, want)
	}
}

func TestCORE_PROVIDER_001DefaultConfigPath(t *testing.T) {
	t.Setenv("COLT_CONFIG", "")
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Path()
	want := filepath.Join(base, "colt", "config.yaml")
	if err != nil || got != want {
		t.Fatalf("Path() = %q, %v; want %q", got, err, want)
	}
}

func TestCORE_RESOLVE_001_002ProviderNeutralPrecedence(t *testing.T) {
	github, gitlab := validProvider("github"), validProvider("gitlab")
	defaultGitHub := github
	defaultGitHub.Default = true
	invalid := gitlab
	invalid.GitEmail = ""
	tests := []struct {
		name, explicit, want string
		cfg                  Config
		wantErr              string
	}{
		{"explicit wins", "work", "work", Config{Providers: map[string]Provider{"personal": defaultGitHub, "work": gitlab}}, ""},
		{"default", "", "personal", Config{Providers: map[string]Provider{"personal": defaultGitHub, "work": gitlab}}, ""},
		{"sole github", "", "one", Config{Providers: map[string]Provider{"one": github}}, ""},
		{"sole gitlab", "", "one", Config{Providers: map[string]Provider{"one": gitlab}}, ""},
		{"none", "", "", Config{Providers: map[string]Provider{}}, "run 'colt auth login' first"},
		{"ambiguous", "", "", Config{Providers: map[string]Provider{"one": github, "two": gitlab}}, "ambiguous"},
		{"unknown explicit", "missing", "", Config{Providers: map[string]Provider{"one": github}}, "unknown provider"},
		{"multiple defaults", "", "", Config{Providers: map[string]Provider{"one": defaultGitHub, "two": func() Provider { p := gitlab; p.Default = true; return p }()}}, "multiple"},
		{"invalid selected does not fall through", "bad", "", Config{Providers: map[string]Provider{"bad": invalid, "one": github}}, "invalid provider"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alias, _, err := tt.cfg.Resolve(tt.explicit)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || alias != tt.want {
				t.Fatalf("Resolve() = %q, %v; want %q", alias, err, tt.want)
			}
		})
	}
}

func TestCORE_CREDENTIAL_001_002_003EnvironmentOnly(t *testing.T) {
	tests := []struct {
		name, kind, configured, envName, value string
		wantErr                                bool
	}{
		{"configured", "gitlab", "COMPANY_GL_TOKEN", "COMPANY_GL_TOKEN", "secret", false},
		{"github default", "github", "", "GITHUB_TOKEN", "secret", false},
		{"gitlab default", "gitlab", "", "GITLAB_TOKEN", "secret", false},
		{"missing", "gitlab", "MISSING_COLT_TEST_TOKEN", "MISSING_COLT_TEST_TOKEN", "", true},
		{"empty", "github", "EMPTY_COLT_TEST_TOKEN", "EMPTY_COLT_TEST_TOKEN", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.envName, tt.value)
			p := validProvider(tt.kind)
			p.TokenEnv = tt.configured
			token, name, err := Token(p)
			if tt.wantErr {
				if err == nil || strings.Contains(err.Error(), tt.value+"secret") {
					t.Fatalf("expected safe credential error, got %v", err)
				}
				return
			}
			if err != nil || token != tt.value || name != tt.envName {
				t.Fatalf("Token() = %q, %q, %v", token, name, err)
			}
		})
	}
}

func TestCORE_PROVIDER_001_002AtomicRestrictiveConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	cfg := Config{Providers: map[string]Provider{"work": validProvider("gitlab")}}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]os.FileMode{filepath.Dir(path): 0o700, path: 0o600} {
		info, err := os.Stat(target)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("mode for %s = %v, %v; want %v", target, info.Mode().Perm(), err, want)
		}
	}
	before, _ := os.ReadFile(path)
	broken := cfg
	p := broken.Providers["work"]
	p.GitEmail = ""
	broken.Providers["work"] = p
	if err := Save(path, broken); err == nil {
		t.Fatal("invalid save succeeded")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("failed save changed prior configuration")
	}
	if strings.Contains(string(after), "secret-token-value") {
		t.Fatal("token value persisted")
	}
	loaded, err := Load(path)
	if err != nil || loaded.Providers["work"].Type != "gitlab" {
		t.Fatalf("Load() = %#v, %v", loaded, err)
	}
}

func TestCORE_PROVIDER_001ConfigOverridePreservesExistingParentMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COLT_CONFIG", filepath.Join(dir, "config.yaml"))
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Config{Providers: map[string]Provider{"work": validProvider("gitlab")}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("existing parent mode = %v; want 0755", info.Mode().Perm())
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v; want 0600", info.Mode().Perm())
	}
}

func TestCORE_PROVIDER_001OversizedConfigIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("providers: {}\n" + strings.Repeat(" ", maxConfigSize))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "maximum size") {
		t.Fatalf("Load() error = %v", err)
	}
}
