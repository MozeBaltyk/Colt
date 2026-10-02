package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// analyzerInventory is the canonical analysis result. Reports are always
// derived from this struct; they are never a second source of truth.
type analyzerInventory struct {
	SchemaVersion int                 `yaml:"schema_version" json:"schema_version"`
	Repository    analyzerRepository  `yaml:"repository" json:"repository"`
	Ecosystems    []analyzerEcosystem `yaml:"ecosystems" json:"ecosystems"`
}

type analyzerRepository struct {
	Name   string `yaml:"name" json:"name"`
	Path   string `yaml:"path" json:"path"`
	Origin string `yaml:"origin,omitempty" json:"origin,omitempty"`
}

type analyzerEcosystem struct {
	Name      string   `yaml:"name" json:"name"`
	Manifests []string `yaml:"manifests" json:"manifests"`
}

// analyzerManifestNames is the deterministic allowlist of manifest basenames
// mapped to the ecosystem they declare. Detection only reads directory entry
// names; it never opens or executes repository content.
var analyzerManifestNames = map[string]string{
	"go.mod":           "go",
	"go.sum":           "go",
	"package.json":     "node",
	"requirements.txt": "python",
	"pyproject.toml":   "python",
	"setup.py":         "python",
	"setup.cfg":        "python",
	"Cargo.toml":       "rust",
	"pom.xml":          "java",
	"build.gradle":     "java",
	"build.gradle.kts": "java",
	"Gemfile":          "ruby",
	"composer.json":    "php",
	"Dockerfile":       "docker",
	"Containerfile":    "docker",
}

// maxAnalyzerEntries and maxAnalyzerDepth bound the read-only walk so a huge
// tree fails deterministically instead of being truncated secretly.
const (
	maxAnalyzerEntries = 20000
	maxAnalyzerDepth   = 12
)

func (a *App) analyzeCommand() *cobra.Command {
	var format, output string
	cmd := &cobra.Command{
		Use:   "analyze [path]",
		Short: "Inventory a repository and render derived reports",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := a.WorkDir
			if len(args) == 1 {
				dir = args[0]
			}
			if dir == "" {
				var err error
				dir, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("determine working directory: %w", err)
				}
			}
			return a.analyze(cmd, dir, format, output)
		},
	}
	cmd.Flags().StringVar(&format, "format", "summary", "report view: summary, json, or yaml")
	cmd.Flags().StringVar(&output, "output", "", "write the canonical inventory.yaml to this file")
	return cmd
}

func (a *App) analyze(cmd *cobra.Command, dir, format, output string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve repository path: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}
	defer root.Close()
	if !stableRoot(root) {
		return errors.New("repository root cannot be opened safely")
	}

	inv := analyzerInventory{
		SchemaVersion: 1,
		Repository:    analyzerRepository{Name: filepath.Base(abs), Path: abs},
	}
	if a.Git != nil {
		if origin, err := a.Git.Origin(cmd.Context(), abs); err == nil {
			inv.Repository.Origin = sanitizeOrigin(origin)
		}
	}
	inv.Ecosystems, err = detectEcosystems(root)
	if err != nil {
		return err
	}

	if output != "" {
		outPath := output
		if !filepath.IsAbs(outPath) {
			outPath = filepath.Join(abs, outPath)
		}
		data, err := yaml.Marshal(inv)
		if err != nil {
			return err
		}
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			return fmt.Errorf("write inventory: %w", err)
		}
	}

	w := cmd.OutOrStdout()
	switch format {
	case "summary":
		renderAnalyzerSummary(w, inv)
	case "json":
		data, err := json.MarshalIndent(inv, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(data))
	case "yaml":
		data, err := yaml.Marshal(inv)
		if err != nil {
			return err
		}
		fmt.Fprint(w, string(data))
	default:
		return fmt.Errorf("invalid --format %q; use summary, json, or yaml", format)
	}
	return nil
}

// detectEcosystems walks the repository read-only, bounded by entry count and
// depth, and reports the allowlisted manifest files it finds. It never opens a
// file and never follows symlinks.
func detectEcosystems(root *os.Root) ([]analyzerEcosystem, error) {
	found := map[string]map[string]struct{}{}
	visited := 0
	var walk func(rel string, depth int) error
	walk = func(rel string, depth int) error {
		if depth > maxAnalyzerDepth {
			return nil
		}
		d, err := root.Open(rel)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		entries, readErr := d.ReadDir(-1)
		d.Close()
		if readErr != nil {
			return readErr
		}
		for _, entry := range entries {
			visited++
			if visited > maxAnalyzerEntries {
				return errors.New("analysis exceeded repository entry limit")
			}
			name := entry.Name()
			child := name
			if rel != "." {
				child = filepath.Join(rel, name)
			}
			if entry.IsDir() {
				if skipAnalyzerDir(name) || entry.Type()&fs.ModeSymlink != 0 {
					continue
				}
				if err := walk(child, depth+1); err != nil {
					return err
				}
				continue
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				continue
			}
			if eco, ok := analyzerManifestNames[name]; ok {
				if found[eco] == nil {
					found[eco] = map[string]struct{}{}
				}
				found[eco][filepath.ToSlash(child)] = struct{}{}
			}
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return nil, err
	}

	ecos := make([]analyzerEcosystem, 0, len(found))
	for name, manifests := range found {
		ms := make([]string, 0, len(manifests))
		for m := range manifests {
			ms = append(ms, m)
		}
		sort.Strings(ms)
		ecos = append(ecos, analyzerEcosystem{Name: name, Manifests: ms})
	}
	sort.Slice(ecos, func(i, j int) bool { return ecos[i].Name < ecos[j].Name })
	return ecos, nil
}

func skipAnalyzerDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "vendor", "target", ".venv", "venv", "__pycache__":
		return true
	}
	return false
}

// sanitizeOrigin strips credentials, queries, and fragments from a remote URL
// before it is recorded. SCP-style remotes without an embedded secret are kept.
func sanitizeOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return raw
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func renderAnalyzerSummary(w io.Writer, inv analyzerInventory) {
	fmt.Fprintf(w, "Repository: %s\n", inv.Repository.Name)
	fmt.Fprintf(w, "Path:       %s\n", inv.Repository.Path)
	remote := inv.Repository.Origin
	if remote == "" {
		remote = "(none)"
	}
	fmt.Fprintf(w, "Remote:     %s\n", remote)
	if len(inv.Ecosystems) == 0 {
		fmt.Fprintln(w, "Ecosystems: none detected")
		return
	}
	fmt.Fprintln(w, "Ecosystems:")
	for _, eco := range inv.Ecosystems {
		fmt.Fprintf(w, "  %s: %s\n", eco.Name, strings.Join(eco.Manifests, ", "))
	}
}
