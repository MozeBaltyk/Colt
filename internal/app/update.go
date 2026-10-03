package app

import (
	"fmt"

	"github.com/MozeBaltyk/Colt/internal/update"
	"github.com/MozeBaltyk/Colt/internal/version"
	"github.com/spf13/cobra"
)

func (a *App) updateClient() (*update.Client, error) {
	if a.Updater != nil {
		return a.Updater, nil
	}
	return update.New(version.Version)
}

func (a *App) updateCommand() *cobra.Command {
	check := false
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update Colt to the latest release",
		Long:  "Download the latest verified release and replace this binary. Use --check to report without changing anything.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.updateClient()
			if err != nil {
				return err
			}
			if check {
				return a.updateCheck(cmd, c)
			}
			return a.updateApply(cmd, c)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "report available updates without changing anything")
	return cmd
}

func (a *App) updateCheck(cmd *cobra.Command, c *update.Client) error {
	tag, err := c.Latest(cmd.Context())
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if c.Version == "" || c.Version == "devel" {
		fmt.Fprintf(out, "current: development build\nlatest: %s\nupdate available\n", tag)
		return nil
	}
	if tag == c.Version {
		fmt.Fprintf(out, "current: %s\nlatest: %s\nup to date\n", c.Version, tag)
		return nil
	}
	fmt.Fprintf(out, "current: %s\nlatest: %s\nupdate available\n", c.Version, tag)
	return nil
}

func (a *App) updateApply(cmd *cobra.Command, c *update.Client) error {
	tag, err := c.Install(cmd.Context())
	if already, ok := update.AlreadyCurrent(err); ok {
		fmt.Fprintf(cmd.OutOrStdout(), "already up to date (%s)\n", already)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "updated to %s\n", tag)
	return nil
}
