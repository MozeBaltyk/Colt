package app

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/spf13/cobra"
)

func (a *App) listCommand() *cobra.Command {
	all := false
	providerAlias := ""
	namespace := ""
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List repositories from a provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.pathErr != nil {
				return a.pathErr
			}
			if namespace != "" && all {
				return errors.New("--namespace cannot be combined with --all")
			}
			cfg, err := config.Load(a.ConfigPath)
			if err != nil {
				return err
			}
			aliases := make([]string, 0, len(cfg.Providers))
			if providerAlias != "" {
				if _, ok := cfg.Providers[providerAlias]; !ok {
					return fmt.Errorf("unknown provider alias %q; configure it or choose an existing alias", providerAlias)
				}
				aliases = append(aliases, providerAlias)
			} else if all {
				for alias := range cfg.Providers {
					aliases = append(aliases, alias)
				}
			} else {
				alias, _, err := cfg.Resolve("")
				if err != nil {
					return err
				}
				aliases = append(aliases, alias)
			}
			sort.Strings(aliases)
			out := cmd.OutOrStdout()
			anyOK := false
			for _, alias := range aliases {
				p := cfg.Providers[alias]
				if namespace != "" {
					p.Namespace = namespace
					if err := config.ValidateProvider(alias, p); err != nil {
						return fmt.Errorf("invalid --namespace: %w", err)
					}
				}
				token, _, err := config.Token(p, a.credentialStore())
				if err != nil {
					if all {
						fmt.Fprintf(out, "%s: credential error: %v\n", alias, err)
						continue
					}
					return err
				}
				client, err := a.NewClient(p, token)
				if err != nil {
					if all {
						fmt.Fprintf(out, "%s: client error: %v\n", alias, err)
						continue
					}
					return err
				}
				repos, err := client.List(cmd.Context())
				if err != nil {
					if all {
						fmt.Fprintf(out, "%s: %v\n", alias, err)
						continue
					}
					return err
				}
				if namespace != "" {
					filtered := repos[:0]
					for _, repo := range repos {
						if namespaceMatches(strings.Split(repo.Namespace, "/"), strings.Split(namespace, "/"), p.Type) {
							filtered = append(filtered, repo)
						}
					}
					repos = filtered
				}
				sort.Slice(repos, func(i, j int) bool {
					return repos[i].CloneURL < repos[j].CloneURL
				})
				for _, repo := range repos {
					fmt.Fprintln(out, repo.CloneURL)
				}
				anyOK = true
			}
			if !anyOK && all {
				return errors.New("no providers returned results")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "list repositories from every configured provider")
	cmd.Flags().StringVar(&providerAlias, "provider", "", "provider alias")
	cmd.Flags().StringVar(&namespace, "namespace", "", "namespace to list (defaults to the provider's configured namespace)")
	return cmd
}
