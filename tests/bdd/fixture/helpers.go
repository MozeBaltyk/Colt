package fixture

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
)

func StdProvider(pType, namespace string) config.Provider {
	host, base := "github.com", "https://api.github.com"
	if pType == "gitlab" {
		host, base = "gitlab.com", "https://gitlab.com"
	}
	return config.Provider{
		Type: pType, Host: host, BaseURL: base, Namespace: namespace,
		Visibility: "private", GitName: "Example User", GitEmail: "user@example.invalid",
		Auth: config.Auth{Source: "env"},
	}
}

// WithTokenEnv attaches an explicit credential source. Providers without
// one resolve conventional variables (GITHUB_TOKEN / GITLAB_TOKEN).
func WithTokenEnv(p config.Provider, env string) config.Provider {
	p.Auth.TokenEnv = env
	return p
}

func WithDefault(p config.Provider, d bool) config.Provider {
	p.Default = d
	return p
}

func RawProviderYAML(alias, pType, host, base, extra string) string {
	return "providers:\n  " + alias + ":\n    type: " + pType + "\n    host: " + host +
		"\n    base_url: " + base + "\n    namespace: example-ns\n    visibility: private" +
		"\n    git_name: Example User\n    git_email: user@example.invalid\n" + extra
}

func GitGlobalSnapshot() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), CommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "config", "--global", "--list").CombinedOutput() // ponytail: read-only snapshot
	return string(out), err
}

func GitOut(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), CommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return strings.TrimSpace(buf.String()), err
}

// SplitCmd supports the small shell-like subset used by feature command steps.
func SplitCmd(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	var quote rune
	escaped, started := false, false
	flush := func() {
		if started {
			out = append(out, cur.String())
			cur.Reset()
			started = false
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
			started = true
		case r == '\\':
			escaped = true
			started = true
		case quote != 0 && r == quote:
			quote = 0
			started = true
		case quote == 0 && (r == '"' || r == '\''):
			quote = r
			started = true
		case quote == 0 && (r == ' ' || r == '\t'):
			flush()
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if escaped {
		return nil, errors.New("bdd: dangling command escape")
	}
	if quote != 0 {
		return nil, errors.New("bdd: unterminated command quote")
	}
	flush()
	return out, nil
}
