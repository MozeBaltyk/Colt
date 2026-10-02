package app

import (
	"errors"
	"net/url"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
)

func cleanHTTPSRepository(raw string, p config.Provider) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !strings.EqualFold(u.Host, p.Host) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	namespaceParts := strings.Split(p.Namespace, "/")
	if len(parts) != len(namespaceParts)+1 || !namespaceMatches(parts[:len(namespaceParts)], namespaceParts, p.Type) || !strings.HasSuffix(parts[len(parts)-1], ".git") {
		return "", false
	}
	project := strings.TrimSuffix(parts[len(parts)-1], ".git")
	return project, config.ValidProjectName(project)
}

func cleanSSHRepository(raw string, p config.Provider) (string, bool) {
	host, path := "", ""
	if u, err := url.Parse(raw); err == nil && u.Scheme == "ssh" {
		if u.User.Username() != "git" {
			return "", false
		}
		host = u.Hostname()
		path = strings.TrimPrefix(u.Path, "/")
	} else {
		if !strings.HasPrefix(raw, "git@") {
			return "", false
		}
		authority, rest, ok := strings.Cut(strings.TrimPrefix(raw, "git@"), ":")
		if !ok {
			return "", false
		}
		host, path = authority, rest
	}
	if !strings.EqualFold(host, p.Host) {
		return "", false
	}
	parts := strings.Split(path, "/")
	namespaceParts := strings.Split(p.Namespace, "/")
	if len(parts) != len(namespaceParts)+1 || !namespaceMatches(parts[:len(namespaceParts)], namespaceParts, p.Type) || !strings.HasSuffix(parts[len(parts)-1], ".git") {
		return "", false
	}
	project := strings.TrimSuffix(parts[len(parts)-1], ".git")
	return project, config.ValidProjectName(project)
}

func namespaceMatches(actual, configured []string, providerType string) bool {
	return len(actual) == len(configured) && config.NamespaceEqual(strings.Join(actual, "/"), strings.Join(configured, "/"), providerType)
}

func matchingOrigin(cfg config.Config, origin string) (alias, transport, project string) {
	alias, transport, project, _ = matchingOriginResult(cfg, origin)
	return
}

func matchingOriginResult(cfg config.Config, origin string) (alias, transport, project string, ambiguous bool) {
	type match struct{ alias, transport, project string }
	var matches []match
	for candidate, p := range cfg.Providers {
		actual, ok := cleanHTTPSRepository(origin, p)
		detected := "https"
		if !ok {
			actual, ok = cleanSSHRepository(origin, p)
			detected = "ssh"
		}
		if !ok {
			continue
		}
		matches = append(matches, match{candidate, detected, actual})
	}
	if len(matches) == 1 {
		m := matches[0]
		return m.alias, m.transport, m.project, false
	}
	if len(matches) > 1 {
		var selected *match
		for i := range matches {
			if cfg.Providers[matches[i].alias].Default {
				if selected != nil {
					return "", "", "", true
				}
				selected = &matches[i]
			}
		}
		if selected != nil {
			return selected.alias, selected.transport, selected.project, false
		}
		return "", "", "", true
	}
	return "", "", "", false
}

func providerTokenEnvNames(cfg config.Config) []string {
	names := make([]string, 0, len(cfg.Providers)*2)
	seen := map[string]bool{}
	for _, p := range cfg.Providers {
		for _, name := range []string{p.Auth.TokenEnv, credential.ConventionalVar(p.Type)} {
			if name != "" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names
}

func InitTransport(p config.Provider, explicitTransport string, configPath string) (string, error) {
	// Precedence: explicit command option > provider transport preference >
	// global transport preference > product default (HTTPS).
	if explicitTransport != "" {
		if explicitTransport != "https" && explicitTransport != "ssh" {
			return "", errors.New("unsupported transport; use https or ssh")
		}
		return explicitTransport, nil
	}
	if p.Transport != "" {
		if p.Transport != "https" && p.Transport != "ssh" {
			return "", errors.New("unsupported transport; use https or ssh")
		}
		return p.Transport, nil
	}
	cfg, err := config.Load(configPath)
	if err == nil && cfg.Transport != "" {
		if cfg.Transport != "https" && cfg.Transport != "ssh" {
			return "", errors.New("unsupported transport; use https or ssh")
		}
		return cfg.Transport, nil
	}
	return "https", nil
}
