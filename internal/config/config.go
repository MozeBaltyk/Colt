package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/credential"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Transport string              `yaml:"transport,omitempty"`
	Providers map[string]Provider `yaml:"providers"`
	Templates map[string]Template `yaml:"templates,omitempty"`
	Workspace *Workspace          `yaml:"workspace,omitempty"`
}

type Workspace struct {
	Repositories []RepositorySelection `yaml:"repositories"`
}

type RepositorySelection struct {
	Provider  string    `yaml:"provider"`
	Namespace string    `yaml:"namespace"`
	Include   *[]string `yaml:"include,omitempty"`
}

type Template struct {
	Default  string                     `yaml:"default"`
	Versions map[string]TemplateVersion `yaml:"versions"`
}

type TemplateVersion struct {
	Source     string                       `yaml:"source"`
	Digest     string                       `yaml:"digest"`
	Parameters map[string]TemplateParameter `yaml:"parameters,omitempty"`
}

type TemplateParameter struct {
	Required bool    `yaml:"required,omitempty"`
	Default  *string `yaml:"default,omitempty"`
}

type Provider struct {
	Type       string `yaml:"type"`
	Host       string `yaml:"host"`
	BaseURL    string `yaml:"base_url"`
	Namespace  string `yaml:"namespace"`
	Visibility string `yaml:"visibility"`
	GitName    string `yaml:"git_name"`
	GitEmail   string `yaml:"git_email"`
	Transport  string `yaml:"transport,omitempty"`
	Auth       Auth   `yaml:"auth"`
	// LegacyTokenEnv accepts pre-auth-schema config on load. Load migrates it
	// in memory and Save writes only Auth.
	LegacyTokenEnv string `yaml:"token_env,omitempty"`
	Default        bool   `yaml:"default,omitempty"`
}

type Auth struct {
	Source       string `yaml:"source"`
	TokenEnv     string `yaml:"token_env,omitempty"`
	CredentialID string `yaml:"credential_id,omitempty"`
}

const (
	maxConfigSize            = 1 << 20
	maxYAMLDepth             = 32
	maxYAMLCollectionEntries = 10000
	MaxWorkspaceSelections   = 128
	MaxWorkspaceIncludes     = 4096
	MaxProviderRepositories  = 10000
)

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
var envRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var namespacePartRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var parameterRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func Path() (string, error) {
	if path := os.Getenv("COLT_CONFIG"); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config directory: %w", err)
	}
	return filepath.Join(dir, "colt", "config.yaml"), nil
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{Providers: map[string]Provider{}}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigSize+1))
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if len(data) > maxConfigSize {
		return Config{}, fmt.Errorf("config exceeds maximum size of %d bytes", maxConfigSize)
	}
	document, err := parseDataYAML(data)
	if err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := validateTemplateYAML(document); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := validateWorkspaceYAML(document); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); errors.Is(err, io.EOF) {
		return Config{Providers: map[string]Provider{}}, nil
	} else if err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("parse config: multiple YAML documents are not allowed")
		}
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]Provider{}
	}
	for alias, p := range cfg.Providers {
		if p.Auth == (Auth{}) {
			p.Auth = Auth{Source: "env", TokenEnv: p.LegacyTokenEnv}
			p.LegacyTokenEnv = ""
			cfg.Providers[alias] = p
			continue
		}
		if p.LegacyTokenEnv != "" {
			return Config{}, fmt.Errorf("invalid provider %q: token_env cannot be combined with auth", alias)
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validateDataYAML(data []byte) error {
	_, err := parseDataYAML(data)
	return err
}

func parseDataYAML(data []byte) (*yaml.Node, error) {
	var document yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&document); errors.Is(err, io.EOF) {
		return &document, nil
	} else if err != nil {
		return nil, err
	}
	entries := 0
	var walk func(*yaml.Node, int) error
	walk = func(node *yaml.Node, depth int) error {
		if depth > maxYAMLDepth {
			return fmt.Errorf("YAML exceeds maximum depth of %d", maxYAMLDepth)
		}
		if node.Alias != nil || node.Kind == yaml.AliasNode || node.Anchor != "" {
			return errors.New("YAML aliases and anchors are not allowed")
		}
		allowedTag := node.Tag == "" || node.Tag == "!!map" || node.Tag == "!!seq" || node.Tag == "!!str" || node.Tag == "!!bool" || node.Tag == "!!null" || node.Tag == "!!int" || node.Tag == "!!float" || node.Tag == "!!timestamp"
		if !allowedTag {
			return fmt.Errorf("custom YAML tag %q is not allowed", node.Tag)
		}
		if node.Kind == yaml.MappingNode {
			if len(node.Content)%2 != 0 {
				return errors.New("malformed YAML mapping")
			}
			entries += len(node.Content) / 2
			seen := make(map[string]bool, len(node.Content)/2)
			for i := 0; i < len(node.Content); i += 2 {
				key := node.Content[i]
				if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
					return errors.New("YAML mapping keys must be strings")
				}
				if key.Value == "<<" {
					return errors.New("YAML merge keys are not allowed")
				}
				if seen[key.Value] {
					return fmt.Errorf("duplicate YAML key %q", key.Value)
				}
				seen[key.Value] = true
			}
		} else if node.Kind == yaml.SequenceNode {
			entries += len(node.Content)
		}
		if entries > maxYAMLCollectionEntries {
			return fmt.Errorf("YAML exceeds maximum collection size of %d", maxYAMLCollectionEntries)
		}
		for _, child := range node.Content {
			if err := walk(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(&document, 0); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("multiple YAML documents are not allowed")
	} else if !errors.Is(err, io.EOF) {
		return nil, err
	}
	return &document, nil
}

func validateTemplateYAML(document *yaml.Node) error {
	if document == nil || len(document.Content) == 0 {
		return nil
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	templates := mappingValue(root, "templates")
	if templates == nil {
		return nil
	}
	if templates.Kind != yaml.MappingNode {
		return errors.New("templates must be a mapping")
	}
	for i := 0; i < len(templates.Content); i += 2 {
		name, template := templates.Content[i], templates.Content[i+1]
		if name.Tag != "!!str" || template.Kind != yaml.MappingNode {
			return errors.New("template names must be strings and definitions must be mappings")
		}
		if value := mappingValue(template, "default"); value != nil && value.Tag != "!!str" {
			return fmt.Errorf("template %q default must be a string", name.Value)
		}
		versions := mappingValue(template, "versions")
		if versions == nil || versions.Kind != yaml.MappingNode {
			return fmt.Errorf("template %q versions must be a mapping", name.Value)
		}
		for j := 0; j < len(versions.Content); j += 2 {
			version, configured := versions.Content[j], versions.Content[j+1]
			if version.Tag != "!!str" || configured.Kind != yaml.MappingNode {
				return fmt.Errorf("template %q version names must be strings and definitions must be mappings", name.Value)
			}
			for _, field := range []string{"source", "digest"} {
				if value := mappingValue(configured, field); value != nil && value.Tag != "!!str" {
					return fmt.Errorf("template %q version %q %s must be a string", name.Value, version.Value, field)
				}
			}
			parameters := mappingValue(configured, "parameters")
			if parameters == nil {
				continue
			}
			if parameters.Kind != yaml.MappingNode {
				return fmt.Errorf("template %q version %q parameters must be a mapping", name.Value, version.Value)
			}
			for k := 0; k < len(parameters.Content); k += 2 {
				parameter, declaration := parameters.Content[k], parameters.Content[k+1]
				if parameter.Tag != "!!str" || declaration.Kind != yaml.MappingNode {
					return fmt.Errorf("template %q version %q parameter names must be strings and declarations must be mappings", name.Value, version.Value)
				}
				if value := mappingValue(declaration, "required"); value != nil && value.Tag != "!!bool" {
					return fmt.Errorf("template parameter %q required must be a boolean", parameter.Value)
				}
				if value := mappingValue(declaration, "default"); value != nil && value.Tag != "!!str" {
					return fmt.Errorf("template parameter %q default must be a string", parameter.Value)
				}
			}
		}
	}
	return nil
}

func validateWorkspaceYAML(document *yaml.Node) error {
	if document == nil || len(document.Content) == 0 {
		return nil
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	workspace := mappingValue(root, "workspace")
	if workspace == nil {
		return nil
	}
	if workspace.Kind != yaml.MappingNode {
		return errors.New("workspace must be a mapping")
	}
	repositories := mappingValue(workspace, "repositories")
	if repositories == nil || repositories.Kind != yaml.SequenceNode {
		return errors.New("workspace repositories must be a sequence")
	}
	for i, selection := range repositories.Content {
		if selection.Kind != yaml.MappingNode {
			return fmt.Errorf("workspace repository %d must be a mapping", i+1)
		}
		for _, field := range []string{"provider", "namespace"} {
			value := mappingValue(selection, field)
			if value == nil || value.Tag != "!!str" {
				return fmt.Errorf("workspace repository %d %s must be a string", i+1, field)
			}
		}
		include := mappingValue(selection, "include")
		if include == nil {
			continue
		}
		if include.Kind != yaml.SequenceNode {
			return fmt.Errorf("workspace repository %d include must be a sequence", i+1)
		}
		for _, project := range include.Content {
			if project.Tag != "!!str" {
				return fmt.Errorf("workspace repository %d include values must be strings", i+1)
			}
		}
	}
	return nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func (c Config) Validate() error {
	defaults := 0
	for alias, p := range c.Providers {
		if err := ValidateProvider(alias, p); err != nil {
			return fmt.Errorf("invalid provider %q: %w", alias, err)
		}
		if p.Default {
			defaults++
		}
	}
	if defaults > 1 {
		return errors.New("invalid config: multiple providers are marked default")
	}
	if err := c.validateWorkspace(); err != nil {
		return err
	}
	for name, template := range c.Templates {
		if !nameRE.MatchString(name) || name == "." || name == ".." || strings.Contains(name, "@") {
			return fmt.Errorf("invalid template %q: name must use 1-100 letters, digits, '.', '_' or '-'", name)
		}
		if len(template.Versions) == 0 {
			return fmt.Errorf("invalid template %q: at least one version is required", name)
		}
		if !nameRE.MatchString(template.Default) {
			return fmt.Errorf("invalid template %q: default must name a configured version", name)
		}
		if _, ok := template.Versions[template.Default]; !ok {
			return fmt.Errorf("invalid template %q: default version %q is not configured", name, template.Default)
		}
		for version, configured := range template.Versions {
			if !nameRE.MatchString(version) || version == "." || version == ".." || strings.Contains(version, "@") {
				return fmt.Errorf("invalid template %q version %q: version must use 1-100 letters, digits, '.', '_' or '-'", name, version)
			}
			if configured.Source == "" || len(configured.Source) > 4096 || strings.TrimSpace(configured.Source) != configured.Source || strings.ContainsAny(configured.Source, "\x00\r\n") || strings.Contains(configured.Source, "://") {
				return fmt.Errorf("invalid template %q version %q: source must be a non-empty local directory path", name, version)
			}
			if !digestRE.MatchString(configured.Digest) {
				return fmt.Errorf("invalid template %q version %q: digest must be sha256 followed by 64 lowercase hexadecimal digits", name, version)
			}
			for parameter, declaration := range configured.Parameters {
				if !parameterRE.MatchString(parameter) {
					return fmt.Errorf("invalid template %q version %q parameter %q: use a letter followed by letters, digits, '_' or '-'", name, version, parameter)
				}
				if declaration.Required && declaration.Default != nil {
					return fmt.Errorf("invalid template %q version %q parameter %q: required and default are mutually exclusive", name, version, parameter)
				}
				if declaration.Default != nil && strings.ContainsAny(*declaration.Default, "\r\n") {
					return fmt.Errorf("invalid template %q version %q parameter %q: default must be one line", name, version, parameter)
				}
			}
		}
	}
	return nil
}

func (c Config) validateWorkspace() error {
	if c.Workspace == nil {
		return nil
	}
	if len(c.Workspace.Repositories) > MaxWorkspaceSelections {
		return fmt.Errorf("invalid workspace: at most %d repository selections are allowed", MaxWorkspaceSelections)
	}
	totalIncludes := 0
	paths := map[string]bool{}
	for i, selection := range c.Workspace.Repositories {
		p, ok := c.Providers[selection.Provider]
		if !ok {
			return fmt.Errorf("invalid workspace repository %d: unknown provider alias %q", i+1, selection.Provider)
		}
		parts := strings.Split(selection.Namespace, "/")
		if selection.Namespace == "" || p.Type != "gitlab" && len(parts) != 1 {
			return fmt.Errorf("invalid workspace repository %d: namespace is required and nested namespaces are supported only by GitLab", i+1)
		}
		for _, part := range parts {
			if !namespacePartRE.MatchString(part) || part == "." || part == ".." {
				return fmt.Errorf("invalid workspace repository %d: namespace must contain safe non-empty path segments", i+1)
			}
		}
		if !NamespaceEqual(selection.Namespace, p.Namespace, p.Type) {
			return fmt.Errorf("invalid workspace repository %d: namespace %q is incompatible with provider %q namespace %q", i+1, selection.Namespace, selection.Provider, p.Namespace)
		}
		if selection.Include == nil {
			continue
		}
		totalIncludes += len(*selection.Include)
		if totalIncludes > MaxWorkspaceIncludes {
			return fmt.Errorf("invalid workspace: at most %d included repositories are allowed", MaxWorkspaceIncludes)
		}
		included := map[string]bool{}
		for _, project := range *selection.Include {
			if !ValidProjectName(project) {
				return fmt.Errorf("invalid workspace repository %d include %q: invalid repository name", i+1, project)
			}
			canonical := project
			if p.Type != "gitlab" {
				canonical = strings.ToLower(project)
			}
			if included[canonical] {
				return fmt.Errorf("invalid workspace repository %d: duplicate include %q", i+1, project)
			}
			included[canonical] = true
			path := filepath.Join(filepath.FromSlash(selection.Namespace), project)
			if path == "." || filepath.IsAbs(path) || !filepath.IsLocal(path) || filepath.Clean(path) != path {
				return fmt.Errorf("invalid workspace repository %d: derived path %q escapes the workspace root", i+1, path)
			}
			path = strings.ToLower(filepath.ToSlash(path))
			if paths[path] {
				return fmt.Errorf("invalid workspace repository %d: duplicate derived path %q", i+1, path)
			}
			paths[path] = true
		}
	}
	return nil
}

func NamespaceEqual(a, b, providerType string) bool {
	if providerType == "gitlab" {
		return a == b
	}
	return strings.EqualFold(a, b)
}

func (c Config) ResolveTemplate(ref string) (string, string, TemplateVersion, error) {
	name, version, pinned := strings.Cut(ref, "@")
	if name == "" || strings.Contains(version, "@") || pinned && version == "" {
		return "", "", TemplateVersion{}, errors.New("template must be name or name@version")
	}
	template, ok := c.Templates[name]
	if !ok {
		return "", "", TemplateVersion{}, fmt.Errorf("unknown template %q", name)
	}
	if !pinned {
		version = template.Default
	}
	configured, ok := template.Versions[version]
	if !ok {
		return "", "", TemplateVersion{}, fmt.Errorf("unknown template version %q", name+"@"+version)
	}
	return name, version, configured, nil
}

func ValidateProvider(alias string, p Provider) error {
	if p.LegacyTokenEnv != "" {
		return errors.New("top-level token_env is legacy input; use auth.token_env")
	}
	if !nameRE.MatchString(alias) {
		return errors.New("alias must start with a letter or digit and contain only letters, digits, '.', '_' or '-'")
	}
	if p.Type != "github" && p.Type != "gitlab" && p.Type != "gitea" && p.Type != "forgejo" {
		return errors.New("type must be github, gitlab, gitea or forgejo")
	}
	if strings.TrimSpace(p.Host) == "" || strings.ContainsAny(p.Host, "/@?#") {
		return errors.New("host must be a hostname without a scheme or path")
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("base_url must be a clean HTTPS URL")
	}
	if p.Type == "github" && (p.Host != "github.com" || strings.TrimRight(p.BaseURL, "/") != "https://api.github.com") {
		return errors.New("the MVP supports GitHub.com with https://api.github.com only")
	}
	if p.Type != "github" && !strings.EqualFold(u.Host, p.Host) {
		return fmt.Errorf("%s host and base_url host must match", ProviderTypeName(p.Type))
	}
	parts := strings.Split(p.Namespace, "/")
	if strings.TrimSpace(p.Namespace) == "" || p.Type != "gitlab" && len(parts) != 1 {
		return errors.New("namespace is required (nested namespaces are supported only by GitLab)")
	}
	for _, part := range parts {
		if !namespacePartRE.MatchString(part) || part == "." || part == ".." {
			return errors.New("namespace must contain safe non-empty path segments")
		}
	}
	if p.Visibility != "private" && p.Visibility != "public" && !(p.Type == "gitlab" && p.Visibility == "internal") {
		return errors.New("visibility must be private or public (or internal for GitLab)")
	}
	if strings.TrimSpace(p.GitName) == "" || strings.HasPrefix(p.GitName, "-") || strings.ContainsAny(p.GitName, "\r\n") {
		return errors.New("git_name is required and must be one line")
	}
	if strings.TrimSpace(p.GitEmail) == "" || strings.HasPrefix(p.GitEmail, "-") || strings.ContainsAny(p.GitEmail, "\r\n") || !strings.Contains(p.GitEmail, "@") {
		return errors.New("git_email must be a one-line email address")
	}
	if p.Transport != "" && p.Transport != "https" && p.Transport != "ssh" {
		return errors.New("transport must be https or ssh")
	}
	switch p.Auth.Source {
	case "env":
		if p.Auth.CredentialID != "" {
			return errors.New("auth.credential_id is only valid for a stored source")
		}
		if p.Auth.TokenEnv != "" && !envRE.MatchString(p.Auth.TokenEnv) {
			return errors.New("auth.token_env is not a valid environment variable name")
		}
	case "stored":
		if p.Auth.CredentialID == "" {
			return errors.New("auth.credential_id is required for a stored source")
		}
		if err := credential.ValidateID(p.Auth.CredentialID); err != nil {
			return fmt.Errorf("invalid auth.credential_id: %w", err)
		}
		if want := p.Host + "/" + alias; p.Auth.CredentialID != want {
			return fmt.Errorf("auth.credential_id must be %q", want)
		}
		if p.Auth.TokenEnv != "" {
			return errors.New("auth.token_env is only valid for an env source")
		}
	default:
		return errors.New("auth.source must be env or stored")
	}
	return nil
}

func ProviderTypeName(providerType string) string {
	switch providerType {
	case "github":
		return "GitHub"
	case "gitlab":
		return "GitLab"
	case "gitea":
		return "Gitea"
	case "forgejo":
		return "Forgejo"
	default:
		return providerType
	}
}

func (c Config) Resolve(explicit string) (string, Provider, error) {
	if err := c.Validate(); err != nil {
		return "", Provider{}, err
	}
	if explicit != "" {
		p, ok := c.Providers[explicit]
		if !ok {
			return "", Provider{}, fmt.Errorf("unknown provider alias %q; configure it or choose an existing alias", explicit)
		}
		return explicit, p, nil
	}
	for alias, p := range c.Providers {
		if p.Default {
			return alias, p, nil
		}
	}
	if len(c.Providers) == 1 {
		for alias, p := range c.Providers {
			return alias, p, nil
		}
	}
	if len(c.Providers) == 0 {
		return "", Provider{}, errors.New("no providers configured; run 'colt auth login' first")
	}
	return "", Provider{}, errors.New("provider selection is ambiguous; use --provider or configure exactly one default")
}

func Token(p Provider, store credential.Store) (string, string, error) {
	name := credential.ConventionalVar(p.Type)
	if p.Auth.TokenEnv != "" {
		name = p.Auth.TokenEnv
	}
	cred, err := (credential.Resolver{Store: store}).Resolve(p.Type, p.Auth.TokenEnv, p.Auth.CredentialID)
	return cred.Secret, name, err
}

func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	f, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("secure temporary config: %w", err)
	}
	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		f.Close()
		return fmt.Errorf("encode config: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync config: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace config atomically: %w", err)
	}
	return nil
}

func ValidProjectName(name string) bool {
	return nameRE.MatchString(name) && name != "." && name != ".." && name != ".git"
}
