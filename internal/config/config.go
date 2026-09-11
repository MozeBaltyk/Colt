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

	"gopkg.in/yaml.v3"
)

type Config struct {
	Providers map[string]Provider `yaml:"providers"`
}

type Provider struct {
	Type       string `yaml:"type"`
	Host       string `yaml:"host"`
	BaseURL    string `yaml:"base_url"`
	Namespace  string `yaml:"namespace"`
	Visibility string `yaml:"visibility"`
	GitName    string `yaml:"git_name"`
	GitEmail   string `yaml:"git_email"`
	TokenEnv   string `yaml:"token_env,omitempty"`
	Default    bool   `yaml:"default,omitempty"`
}

const maxConfigSize = 1 << 20

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
var envRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var namespacePartRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

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
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
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
	return nil
}

func ValidateProvider(alias string, p Provider) error {
	if !nameRE.MatchString(alias) {
		return errors.New("alias must start with a letter or digit and contain only letters, digits, '.', '_' or '-'")
	}
	if p.Type != "github" && p.Type != "gitlab" {
		return errors.New("type must be github or gitlab")
	}
	if strings.TrimSpace(p.Host) == "" || strings.ContainsAny(p.Host, "/@?#") {
		return errors.New("host must be a hostname without a scheme or path")
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("base_url must be a clean HTTPS URL")
	}
	if p.Type == "github" && (p.Host != "github.com" || strings.TrimRight(p.BaseURL, "/") != "https://api.github.com") {
		return errors.New("the MVP supports GitHub.com with https://api.github.com only")
	}
	if p.Type == "gitlab" && !strings.EqualFold(u.Host, p.Host) {
		return errors.New("GitLab host and base_url host must match")
	}
	parts := strings.Split(p.Namespace, "/")
	if strings.TrimSpace(p.Namespace) == "" || p.Type == "github" && len(parts) != 1 {
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
	if p.TokenEnv != "" && !envRE.MatchString(p.TokenEnv) {
		return errors.New("token_env is not a valid environment variable name")
	}
	return nil
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

func Token(p Provider) (string, string, error) {
	name := p.TokenEnv
	if name == "" {
		if p.Type == "github" {
			name = "GITHUB_TOKEN"
		} else {
			name = "GITLAB_TOKEN"
		}
	}
	token := os.Getenv(name)
	if token == "" {
		return "", name, fmt.Errorf("credential environment variable %s is missing or empty", name)
	}
	return token, name, nil
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
