package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/spf13/cobra"
)

func (a *App) cloneCommand() *cobra.Command {
	providerAlias := ""
	transport := ""
	cmd := &cobra.Command{
		Use:   "clone <repository>",
		Short: "Clone a provider repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.pathErr != nil {
				return a.pathErr
			}
			project := args[0]
			if !config.ValidProjectName(project) {
				return errors.New("invalid project name; use 1-255 letters, digits, '.', '_' or '-' and no path separators")
			}
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
			if err := a.Git.Available(); err != nil {
				return err
			}
			workDir := a.WorkDir
			if workDir == "" {
				workDir, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("determine working directory: %w", err)
				}
			}
			destination := filepath.Join(workDir, project)
			workRoot, err := openWorkRoot(workDir)
			if err != nil {
				return err
			}
			defer workRoot.Close()
			if _, _, err := validateCloneDestination(workRoot, project); err != nil {
				return err
			}
			token, _, err := config.Token(selected, a.credentialStore())
			if err != nil {
				return err
			}
			client, err := a.NewClient(selected, token)
			if err != nil {
				return err
			}
			repo, err := client.Get(cmd.Context(), project)
			if err != nil {
				return fmt.Errorf("resolve repository: %w", err)
			}
			if repo == nil {
				return errors.New("provider returned no repository")
			}
			cloneURL := repo.CloneURL
			valid := cleanHTTPSRepository
			if transport == "ssh" {
				cloneURL, valid = repo.SSHURL, cleanSSHRepository
			}
			if actual, ok := valid(cloneURL, selected); !ok || actual != project {
				return errors.New("provider returned an unexpected clone target")
			}
			stagingParent, err := os.MkdirTemp("", "colt-clone-")
			if err != nil {
				return fmt.Errorf("create private clone staging directory: %w", err)
			}
			defer os.RemoveAll(stagingParent)
			if err := os.Chmod(stagingParent, 0o700); err != nil {
				return fmt.Errorf("secure clone staging directory: %w", err)
			}
			staging := filepath.Join(stagingParent, "repository")
			if err := a.Git.Clone(cmd.Context(), cloneURL, staging, "", alias, project); err != nil {
				return fmt.Errorf("clone failed: %w", err)
			}
			if err := a.Git.SetIdentity(cmd.Context(), staging, selected.GitName, selected.GitEmail); err != nil {
				return fmt.Errorf("set repository identity: %w", err)
			}
			if transport == "https" {
				if err := a.Git.ConfigureCredentialHelper(cmd.Context(), staging, cloneURL, "", alias, project); err != nil {
					return fmt.Errorf("configure credential helper: %w", err)
				}
			}
			if err := installClone(workRoot, staging, project); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "cloned to "+destination)
			return nil
		},
	}
	cmd.Flags().StringVar(&providerAlias, "provider", "", "provider alias")
	cmd.Flags().StringVar(&transport, "transport", "", "Git transport: https or ssh (default from config or https)")
	return cmd
}
