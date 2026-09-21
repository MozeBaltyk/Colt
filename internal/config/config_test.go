package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MozeBaltyk/Colt/internal/credential"
)

func validProvider(kind string) Provider {
	p := Provider{Type: kind, Namespace: "team", Visibility: "private", GitName: "Colt User", GitEmail: "colt@example.com", Auth: Auth{Source: "env"}}
	if kind == "github" {
		p.Host, p.BaseURL = "github.com", "https://api.github.com"
	} else if kind == "gitea" {
		p.Host, p.BaseURL = "code.example.com", "https://code.example.com"
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

func TestWORKSPACE_MANIFEST_001StrictAndBounded(t *testing.T) {
	base := "providers:\n  work:\n    type: gitlab\n    host: gitlab.com\n    base_url: https://gitlab.com\n    namespace: team/tools\n    visibility: private\n    git_name: Colt User\n    git_email: colt@example.com\nworkspace:\n  repositories:\n    - provider: work\n      namespace: team/tools\n"
	for name, suffix := range map[string]string{
		"unknown field":  "      surprise: true\n",
		"duplicate key":  "      provider: work\n",
		"anchor":         "      include: &names [api]\n",
		"alias":          "      include: &names [api]\n    - provider: work\n      namespace: team/tools\n      include: *names\n",
		"merge":          "      <<: {include: [api]}\n",
		"custom tag":     "      include: !command [api]\n",
		"multiple docs":  "---\nproviders: {}\n",
		"traversal name": "      include: [../outside]\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(base+suffix), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatalf("unsafe YAML accepted: %s", suffix)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(base+"      include: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil || cfg.Workspace == nil || cfg.Workspace.Repositories[0].Include == nil || len(*cfg.Workspace.Repositories[0].Include) != 0 {
		t.Fatalf("explicit empty include was not preserved: %#v, %v", cfg.Workspace, err)
	}
}

func TestWORKSPACE_MANIFEST_001RejectsUnknownProviderAndDuplicatePath(t *testing.T) {
	p := validProvider("gitlab")
	p.Namespace = "team"
	includes := []string{"api"}
	cfg := Config{Providers: map[string]Provider{"one": p, "two": p}, Workspace: &Workspace{Repositories: []RepositorySelection{
		{Provider: "one", Namespace: "team", Include: &includes},
		{Provider: "two", Namespace: "team", Include: &includes},
	}}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate derived path") {
		t.Fatalf("duplicate path error = %v", err)
	}
	cfg.Workspace.Repositories = []RepositorySelection{{Provider: "missing", Namespace: "team"}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("unknown provider error = %v", err)
	}
}

func TestWORKSPACE_MANIFEST_001YAMLResourceLimits(t *testing.T) {
	deep := strings.Repeat("- ", maxYAMLDepth+1) + "value\n"
	if err := validateDataYAML([]byte(deep)); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("deep YAML error = %v", err)
	}
	large := "items:\n" + strings.Repeat("  - value\n", maxYAMLCollectionEntries+1)
	if err := validateDataYAML([]byte(large)); err == nil || !strings.Contains(err.Error(), "collection") {
		t.Fatalf("large collection error = %v", err)
	}
	second := "providers: {}\n---\n" + strings.Repeat("- ", maxYAMLDepth+1) + "value\n"
	if err := validateDataYAML([]byte(second)); err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("second document error = %v", err)
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
		{"sole gitea", "", "one", Config{Providers: map[string]Provider{"one": validProvider("gitea")}}, ""},
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

func TestINIT_007TransportDefaultsToHTTPSAndValidatesSSH(t *testing.T) {
	p := validProvider("github")
	if err := ValidateProvider("personal", p); err != nil {
		t.Fatalf("omitted transport rejected: %v", err)
	}
	p.Transport = "ssh"
	if err := ValidateProvider("personal", p); err != nil {
		t.Fatalf("SSH transport rejected: %v", err)
	}
	p.Transport = "file"
	if err := ValidateProvider("personal", p); err == nil || !strings.Contains(err.Error(), "https or ssh") {
		t.Fatalf("unsupported transport error = %v", err)
	}
}

func TestGiteaProviderValidation(t *testing.T) {
	p := validProvider("gitea")
	if err := ValidateProvider("work", p); err != nil {
		t.Fatalf("valid Gitea provider rejected: %v", err)
	}
	for _, tc := range []struct {
		name, want string
		change     func(*Provider)
	}{
		{"HTTP base URL", "clean HTTPS", func(p *Provider) { p.BaseURL = "http://code.example.com" }},
		{"different base host", "Gitea host and base_url host must match", func(p *Provider) { p.BaseURL = "https://other.example.com" }},
		{"nested owner", "nested namespaces", func(p *Provider) { p.Namespace = "team/sub" }},
		{"internal visibility", "visibility", func(p *Provider) { p.Visibility = "internal" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := p
			tc.change(&candidate)
			if err := ValidateProvider("work", candidate); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateProvider() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateProviderRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name, want string
		change     func(*Provider)
	}{
		{"legacy token environment", "top-level token_env", func(p *Provider) { p.LegacyTokenEnv = "TOKEN" }},
		{"unsafe alias", "alias must start", func(*Provider) {}},
		{"unknown type", "type must be", func(p *Provider) { p.Type = "bitbucket" }},
		{"host with scheme", "host must be", func(p *Provider) { p.Host = "https://gitlab.com" }},
		{"base URL with credentials", "clean HTTPS URL", func(p *Provider) { p.BaseURL = "https://user@gitlab.com" }},
		{"GitLab host mismatch", "host and base_url host must match", func(p *Provider) { p.Host = "git.example.com" }},
		{"Gitea host mismatch", "Gitea host and base_url host must match", func(p *Provider) {
			*p = validProvider("gitea")
			p.Host = "other.example.com"
		}},
		{"nested GitHub namespace", "nested namespaces", func(p *Provider) {
			p.Type, p.Host, p.BaseURL, p.Namespace = "github", "github.com", "https://api.github.com", "team/subgroup"
		}},
		{"unsafe namespace segment", "safe non-empty path segments", func(p *Provider) { p.Namespace = "team/../subgroup" }},
		{"GitHub internal visibility", "visibility must be", func(p *Provider) {
			p.Type, p.Host, p.BaseURL, p.Visibility = "github", "github.com", "https://api.github.com", "internal"
		}},
		{"multiline Git name", "git_name", func(p *Provider) { p.GitName = "Colt\nUser" }},
		{"invalid Git email", "git_email", func(p *Provider) { p.GitEmail = "not-an-email" }},
		{"invalid token environment", "environment variable name", func(p *Provider) { p.Auth.TokenEnv = "BAD-NAME" }},
		{"env source with credential ID", "only valid for a stored source", func(p *Provider) { p.Auth.CredentialID = "gitlab.com/personal" }},
		{"stored source with token environment", "only valid for an env source", func(p *Provider) {
			p.Auth = Auth{Source: "stored", CredentialID: "gitlab.com/personal", TokenEnv: "TOKEN"}
		}},
		{"unknown auth source", "auth.source must be", func(p *Provider) { p.Auth.Source = "file" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := validProvider("gitlab")
			tc.change(&p)
			alias := "personal"
			if tc.name == "unsafe alias" {
				alias = "-personal"
			}
			if err := ValidateProvider(alias, p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateProvider() error = %v, want containing %q", err, tc.want)
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
		{"gitea default", "gitea", "", "GITEA_TOKEN", "secret", false},
		{"forgejo default", "forgejo", "", "FORGEJO_TOKEN", "secret", false},
		{"missing", "gitlab", "MISSING_COLT_TEST_TOKEN", "MISSING_COLT_TEST_TOKEN", "", true},
		{"empty", "github", "EMPTY_COLT_TEST_TOKEN", "EMPTY_COLT_TEST_TOKEN", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.envName, tt.value)
			p := validProvider(tt.kind)
			p.Auth.TokenEnv = tt.configured
			token, name, err := Token(p, credential.NewMemoryStore())
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

func TestCORE_CREDENTIAL_004AuthSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := validProvider("github")
	p.Auth.TokenEnv = "GITHUB_TOKEN"
	if err := Save(path, Config{Providers: map[string]Provider{"personal": p}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"auth:\n", "source: env", "token_env: GITHUB_TOKEN"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("config %q lacks %q", data, want)
		}
	}
	stored := validProvider("github")
	stored.Auth = Auth{Source: "stored", CredentialID: "github.com/personal"}
	if err := (Config{Providers: map[string]Provider{"personal": stored}}).Validate(); err != nil {
		t.Fatalf("stored auth reference rejected: %v", err)
	}
	stored.Auth.CredentialID = ""
	if err := (Config{Providers: map[string]Provider{"personal": stored}}).Validate(); err == nil {
		t.Fatal("stored auth without credential_id accepted")
	}
	for _, id := range []string{"gitlab.com/personal", "github.com/work"} {
		stored.Auth.CredentialID = id
		if err := (Config{Providers: map[string]Provider{"personal": stored}}).Validate(); err == nil {
			t.Fatalf("mismatched credential_id %q accepted", id)
		}
	}
}

func TestLoadMigratesLegacyTokenEnvAndSaveUsesCanonicalAuth(t *testing.T) {
	for _, tokenEnv := range []string{"", "LEGACY_TOKEN"} {
		t.Run(map[string]string{"": "conventional", "LEGACY_TOKEN": "explicit"}[tokenEnv], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			raw := "providers:\n  work:\n    type: gitlab\n    host: gitlab.com\n    base_url: https://gitlab.com\n    namespace: team\n    visibility: private\n    git_name: Colt User\n    git_email: colt@example.com\n"
			if tokenEnv != "" {
				raw += "    token_env: " + tokenEnv + "\n"
			}
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil || cfg.Providers["work"].Auth != (Auth{Source: "env", TokenEnv: tokenEnv}) {
				t.Fatalf("Load() = %#v, %v", cfg, err)
			}
			if err := Save(path, cfg); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(data), "auth:\n") || strings.Contains(string(data), "\n    token_env:") {
				t.Fatalf("canonical config = %q, %v", data, err)
			}
		})
	}
}

func TestStoredAuthUsesConventionalEnvironmentBeforeUnsupportedStore(t *testing.T) {
	p := validProvider("github")
	p.Auth = Auth{Source: "stored", CredentialID: "github.com/personal"}
	t.Setenv("GITHUB_TOKEN", "environment-secret")
	token, name, err := Token(p, credential.NewMemoryStore())
	if err != nil || token != "environment-secret" || name != "GITHUB_TOKEN" {
		t.Fatalf("Token() = %q, %q, %v", token, name, err)
	}
	t.Setenv("GITHUB_TOKEN", "")
	if _, _, err := Token(p, credential.NewMemoryStore()); err == nil || !strings.Contains(err.Error(), "no stored credential") {
		t.Fatalf("missing store error = %v", err)
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

func TestTEMPLATE_SOURCE_001ConfigAndResolution(t *testing.T) {
	defaultValue := "MIT"
	version := TemplateVersion{
		Source: "./templates/service-2",
		Digest: "sha256:" + strings.Repeat("a", 64),
		Parameters: map[string]TemplateParameter{
			"owner":   {Required: true},
			"license": {Default: &defaultValue},
		},
	}
	cfg := Config{Providers: map[string]Provider{}, Templates: map[string]Template{
		"service": {Default: "2", Versions: map[string]TemplateVersion{"2": version}},
	}}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"service", "service@2"} {
		name, pin, got, err := loaded.ResolveTemplate(ref)
		if err != nil || name != "service" || pin != "2" || got.Digest != version.Digest {
			t.Fatalf("ResolveTemplate(%q)=%q %q %#v %v", ref, name, pin, got, err)
		}
	}
}

func TestTEMPLATE_SOURCE_001RejectsMalformedConfig(t *testing.T) {
	valid := func() Config {
		return Config{Providers: map[string]Provider{}, Templates: map[string]Template{
			"service": {Default: "2", Versions: map[string]TemplateVersion{"2": {Source: "./source", Digest: "sha256:" + strings.Repeat("a", 64)}}},
		}}
	}
	tests := []struct {
		name, want string
		change     func(*Config)
	}{
		{"name", "invalid template", func(c *Config) { c.Templates["bad@name"] = c.Templates["service"] }},
		{"default", "not configured", func(c *Config) { t := c.Templates["service"]; t.Default = "3"; c.Templates["service"] = t }},
		{"version", "version", func(c *Config) { t := c.Templates["service"]; t.Versions["bad@version"] = t.Versions["2"] }},
		{"source", "local directory path", func(c *Config) {
			t := c.Templates["service"]
			v := t.Versions["2"]
			v.Source = "https://example.invalid/x"
			t.Versions["2"] = v
		}},
		{"digest", "digest", func(c *Config) {
			t := c.Templates["service"]
			v := t.Versions["2"]
			v.Digest = "sha256:no"
			t.Versions["2"] = v
		}},
		{"parameter", "parameter", func(c *Config) {
			t := c.Templates["service"]
			v := t.Versions["2"]
			v.Parameters = map[string]TemplateParameter{"bad.name": {Required: true}}
			t.Versions["2"] = v
		}},
		{"required default", "mutually exclusive", func(c *Config) {
			value := "x"
			t := c.Templates["service"]
			v := t.Versions["2"]
			v.Parameters = map[string]TemplateParameter{"owner": {Required: true, Default: &value}}
			t.Versions["2"] = v
		}},
		{"multiline default", "one line", func(c *Config) {
			value := "x\ny"
			t := c.Templates["service"]
			v := t.Versions["2"]
			v.Parameters = map[string]TemplateParameter{"owner": {Default: &value}}
			t.Versions["2"] = v
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid()
			tc.change(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestTEMPLATE_SOURCE_001RejectsNonStringYAMLValues(t *testing.T) {
	base := "providers: {}\ntemplates:\n  service:\n    default: \"2\"\n    versions:\n      \"2\":\n        source: ./source\n        digest: sha256:" + strings.Repeat("a", 64) + "\n"
	for _, replacement := range []string{
		strings.Replace(base, `default: "2"`, "default: 2", 1),
		strings.Replace(base, "source: ./source", "source: 123", 1),
		base + "        parameters:\n          owner:\n            default: 123\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(replacement), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "must be a string") {
			t.Fatalf("Load error=%v", err)
		}
	}
}
