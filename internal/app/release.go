package app

import (
	"errors"
	"fmt"
	"os"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/spf13/cobra"
)

func (a *App) releaseCommand() *cobra.Command {
	providerAlias := ""
	transport := ""
	cmd := &cobra.Command{
		Use:   "release <version>",
		Short: "Create a provider release",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.pathErr != nil {
				return a.pathErr
			}
			version := args[0]
			cfg, err := config.Load(a.ConfigPath)
			if err != nil {
				return err
			}
			alias, selected, err := cfg.Resolve(providerAlias)
			if err != nil {
				return err
			}
			transport, err = InitTransport(selected, transport, a.ConfigPath)
			if err != nil {
				return err
			}
			if a.Git == nil {
				return errors.New("native Git is unavailable")
			}
			token, _, err := config.Token(selected, a.credentialStore())
			if err != nil {
				return err
			}
			client, err := a.NewClient(selected, token)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			// Derive project name from the current repository origin
			workDir := a.WorkDir
			if workDir == "" {
				workDir, _ = os.Getwd()
			}
			origin, originErr := a.Git.Origin(cmd.Context(), workDir)
			if originErr != nil || origin == "" {
				return errors.New("cannot determine repository from current origin; run this inside a Git repository with a matching remote")
			}
			originAlias, _, project := matchingOrigin(cfg, origin)
			if project == "" || originAlias != alias {
				return errors.New("current origin does not match the selected provider repository")
			}
			// Reject hostile repository-local configuration before any mutation.
			if err := a.Git.ValidateRepoConfig(cmd.Context(), workDir); err != nil {
				return err
			}
			// Validate repository exists
			repo, err := client.Get(cmd.Context(), project)
			if err != nil {
				return fmt.Errorf("resolve repository: %w", err)
			}
			if repo == nil {
				return errors.New("provider returned no repository")
			}
			// Validate tag exists locally
			tag := version
			if err := a.Git.ValidateTag(cmd.Context(), workDir, tag); err != nil {
				return fmt.Errorf("validate tag: %w", err)
			}
			// Validate push target belongs to selected provider/repository
			pushURL := repo.CloneURL
			if transport == "ssh" {
				pushURL = repo.SSHURL
			}
			if err := validatePushTarget(pushURL, selected, transport, project); err != nil {
				return err
			}
			// Create local tag
			if err := a.Git.CreateTag(cmd.Context(), workDir, tag, selected.GitName, selected.GitEmail); err != nil {
				return fmt.Errorf("create tag: %w", err)
			}
			fmt.Fprintln(out, "created tag "+tag)
			// Push tag
			if err := a.Git.PushTag(cmd.Context(), workDir, pushURL, alias, project, tag, providerTokenEnvNames(cfg)); err != nil {
				return fmt.Errorf("push tag: %w", err)
			}
			fmt.Fprintln(out, "pushed tag "+tag)
			// Create provider release
			if err := client.Release(cmd.Context(), version, tag); err != nil {
				return partial(
					"create provider release",
					"tag "+tag+" retained locally",
					"tag "+tag+" pushed; provider release not created",
					"retry the provider release after resolving the provider API failure; do not recreate or force the tag",
					err,
				)
			}
			fmt.Fprintln(out, "released "+version)
			return nil
		},
	}
	cmd.Flags().StringVar(&providerAlias, "provider", "", "provider alias")
	cmd.Flags().StringVar(&transport, "transport", "", "Git transport: https or ssh (default from config or https)")
	return cmd
}

func validatePushTarget(pushURL string, selected config.Provider, transport, project string) error {
	if transport == "ssh" {
		if actual, ok := cleanSSHRepository(pushURL, selected); !ok || actual != project {
			return errors.New("push target does not match selected provider repository")
		}
	} else {
		if actual, ok := cleanHTTPSRepository(pushURL, selected); !ok || actual != project {
			return errors.New("push target does not match selected provider repository")
		}
	}
	return nil
}
