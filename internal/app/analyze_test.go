package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAnalyzeDetectsEcosystemsDeterministically(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "", 0o644)
	write("go.sum", "", 0o644)
	write("package.json", "", 0o644)
	write("Cargo.toml", "", 0o644)
	write("src/pyproject.toml", "", 0o644)
	write("node_modules/package.json", "", 0o644) // should be skipped
	write(".git/config", "", 0o644)

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if !stableRoot(root) {
		t.Fatal("root not stable")
	}

	first, err := detectEcosystems(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := detectEcosystems(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic detection: %#v vs %#v", first, second)
	}

	got := map[string][]string{}
	for _, eco := range first {
		got[eco.Name] = eco.Manifests
	}
	if want := []string{"Cargo.toml"}; !reflect.DeepEqual(got["rust"], want) {
		t.Fatalf("rust=%v", got["rust"])
	}
	if want := []string{"go.mod", "go.sum"}; !reflect.DeepEqual(got["go"], want) {
		t.Fatalf("go=%v", got["go"])
	}
	if want := []string{"package.json"}; !reflect.DeepEqual(got["node"], want) {
		t.Fatalf("node=%v", got["node"])
	}
	if want := []string{"src/pyproject.toml"}; !reflect.DeepEqual(got["python"], want) {
		t.Fatalf("python=%v", got["python"])
	}
	if len(got) != 4 {
		t.Fatalf("unexpected ecosystems: %v", got)
	}
}

func TestAnalyzeDoesNotExecuteRepositoryContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "EXECUTED")
	script := "#!/bin/sh\ntouch '" + marker + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "evil.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	a := &App{WorkDir: dir}
	if _, err := execute(t, a, "analyze", "--format", "yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("repository content was executed")
	}
}

func TestAnalyzeReportViewsAndCanonicalOutput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	sumOut, err := execute(t, &App{WorkDir: dir}, "analyze")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sumOut, "Repository:") || !strings.Contains(sumOut, "  node: package.json") {
		t.Fatalf("summary output = %q", sumOut)
	}

	yamlOut, err := execute(t, &App{WorkDir: dir}, "analyze", "--format", "yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yamlOut, "schema_version: 1") || !strings.Contains(yamlOut, "name: node") || !strings.Contains(yamlOut, "package.json") {
		t.Fatalf("yaml output = %q", yamlOut)
	}

	outFile := filepath.Join(dir, "inventory.yaml")
	if _, err := execute(t, &App{WorkDir: dir}, "analyze", "--format", "yaml", "--output", outFile); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != yamlOut {
		t.Fatalf("inventory file != yaml view:\n%s\n---\n%s", written, yamlOut)
	}
}

func TestAnalyzeSanitizesOriginCredentials(t *testing.T) {
	tests := map[string]string{
		"":                            "",
		"git@example.com:ns/demo.git": "git@example.com:ns/demo.git",
		"https://user:secret@example.com/ns/demo.git":      "https://example.com/ns/demo.git",
		"https://example.com/ns/demo.git?access_token=x#f": "https://example.com/ns/demo.git",
		"ssh://git@example.com/ns/demo.git":                "ssh://example.com/ns/demo.git",
	}
	for in, want := range tests {
		if got := sanitizeOrigin(in); got != want {
			t.Errorf("sanitizeOrigin(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnalyzeInvalidFormatRejected(t *testing.T) {
	_, err := execute(t, &App{WorkDir: t.TempDir()}, "analyze", "--format", "xml")
	if err == nil || !strings.Contains(err.Error(), "invalid --format") {
		t.Fatalf("error = %v", err)
	}
}

func TestAnalyzeSchemaRoundTrips(t *testing.T) {
	inv := analyzerInventory{
		SchemaVersion: 1,
		Repository:    analyzerRepository{Name: "demo", Path: "/tmp/demo", Origin: "https://example.com/ns/demo.git"},
		Ecosystems:    []analyzerEcosystem{{Name: "go", Manifests: []string{"go.mod"}}},
	}
	data, err := yaml.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	var back analyzerInventory
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inv, back) {
		t.Fatalf("round-trip mismatch: %#v vs %#v", inv, back)
	}
}
