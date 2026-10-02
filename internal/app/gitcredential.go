package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/spf13/cobra"
)

func (a *App) gitCredentialCommand() *cobra.Command {
	providerAlias := ""
	repository := ""
	namespace := ""
	cmd := &cobra.Command{
		Use:    "git-credential <get|store|erase>",
		Short:  "Serve credentials to Git",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "get" && args[0] != "store" && args[0] != "erase" {
				return errors.New("git-credential operation must be get, store, or erase")
			}
			if args[0] != "get" {
				_, err := io.Copy(io.Discard, io.LimitReader(cmd.InOrStdin(), 64<<10))
				return err
			}
			request, err := readGitCredential(cmd.InOrStdin())
			if err != nil {
				return err
			}
			cfg, err := config.Load(a.ConfigPath)
			if err != nil {
				return err
			}
			p, ok := credentialProvider(cfg, request, providerAlias, repository, namespace)
			if !ok {
				return nil
			}
			token, _, err := config.Token(p, a.credentialStore())
			if err != nil {
				return nil // Let Git continue to its next configured helper.
			}
			if strings.ContainsAny(token, "\r\n") {
				return nil
			}
			username := "x-access-token"
			if p.Type == "gitlab" {
				username = "oauth2"
			} else if p.Type == "gitea" || p.Type == "forgejo" {
				username = request["username"]
				if username == "" {
					username = p.Namespace
				}
				if strings.ContainsAny(username, "\r\n") {
					return nil
				}
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "username=%s\npassword=%s\n\n", username, token)
			return err
		},
	}
	cmd.Flags().StringVar(&providerAlias, "provider", "", "provider alias")
	cmd.Flags().StringVar(&repository, "repository", "", "repository name")
	cmd.Flags().StringVar(&namespace, "namespace", "", "repository namespace")
	_ = cmd.Flags().MarkHidden("provider")
	_ = cmd.Flags().MarkHidden("repository")
	_ = cmd.Flags().MarkHidden("namespace")
	return cmd
}

func readGitCredential(r io.Reader) (map[string]string, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, 64<<10))
	request := map[string]string{}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return nil, errors.New("invalid Git credential request")
		}
		// Git's own credential parser tolerates repeated fields (last value
		// wins); match it so real Git invocations that emit a duplicated field
		// are not rejected.
		request[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("invalid Git credential request: input too large")
	}
	return request, nil
}

func credentialProvider(cfg config.Config, request map[string]string, alias, repository, namespace string) (config.Provider, bool) {
	if request["protocol"] != "https" || request["host"] == "" || request["path"] == "" {
		return config.Provider{}, false
	}
	raw := "https://" + request["host"] + "/" + strings.TrimPrefix(request["path"], "/")
	if alias != "" {
		p, ok := cfg.Providers[alias]
		if !ok || repository == "" {
			return config.Provider{}, false
		}
		if namespace != "" {
			parts := strings.Split(namespace, "/")
			if p.Type != "gitlab" && len(parts) != 1 {
				return config.Provider{}, false
			}
			for _, part := range parts {
				if !gitnative.SafeIdentifier(part) || part == "." || part == ".." {
					return config.Provider{}, false
				}
			}
			p.Namespace = namespace
		}
		actual, ok := cleanHTTPSRepository(raw, p)
		return p, ok && actual == repository
	}
	var match config.Provider
	found := false
	for _, p := range cfg.Providers {
		if _, ok := cleanHTTPSRepository(raw, p); !ok {
			continue
		}
		if found {
			return config.Provider{}, false
		}
		match, found = p, true
	}
	return match, found
}
