package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealthPolicyStrictValidation(t *testing.T) {
	valid := "providers: {}\npolicy:\n  repository:\n    require: [README.md, docs/SECURITY.md]\n    default_branch: main\n    allowed_visibility: [private, internal]\n"
	tests := []struct{ name, replace string }{
		{"unknown", "    unknown: true\n"},
		{"absolute", strings.Replace(valid, "README.md", "/etc/passwd", 1)},
		{"backslash", strings.Replace(valid, "README.md", `docs\\README.md`, 1)},
		{"traversal", strings.Replace(valid, "README.md", "../README.md", 1)},
		{"current", strings.Replace(valid, "README.md", "docs/./README.md", 1)},
		{"git", strings.Replace(valid, "README.md", ".git/config", 1)},
		{"duplicate", strings.Replace(valid, "README.md, docs/SECURITY.md", "README.md, README.md", 1)},
		{"branch", strings.Replace(valid, "default_branch: main", "default_branch: bad..branch", 1)},
		{"visibility", strings.Replace(valid, "private, internal", "secret", 1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content := tc.replace
			if tc.name == "unknown" {
				content = valid + tc.replace
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("unsafe policy accepted")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := Load(path); err != nil || cfg.Policy == nil {
		t.Fatalf("valid policy: %#v, %v", cfg.Policy, err)
	}
}
