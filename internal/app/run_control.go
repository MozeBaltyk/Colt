package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

func (a *App) runControlCommand(action string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " <name>",
		Short: action + " a deployment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			cmd.SetContext(ctx)
			name := args[0]
			if err := validateDeploymentName(name); err != nil {
				return err
			}
			host, systemctl, podman, err := a.runPreflight(true)
			if err != nil {
				return err
			}
			if err := requireDeploymentUnits(host, name); err != nil {
				return err
			}
			p := deploymentPaths(name)
			owner, err := deploymentOwner(host, p)
			if err != nil {
				return err
			}
			if err := verifyDeployment(cmd.Context(), host, podman, name, owner); err != nil {
				return err
			}
			unlock, err := lockDeployment(host, name)
			if err != nil {
				return err
			}
			defer unlock()
			if err := verifyDeployment(cmd.Context(), host, podman, name, owner); err != nil {
				return err
			}
			units := []string{p.networkUnit, p.dbUnit, p.appUnit}
			if action == "stop" {
				units = []string{p.appUnit, p.dbUnit, p.networkUnit}
				for _, path := range units {
					unit := filepath.Base(path)
					if err := host.Run(cmd.Context(), systemctl, "stop", unit); err != nil {
						return fmt.Errorf("systemd layer: stop %s: %w", unit, err)
					}
					state, err := unitActiveState(cmd.Context(), host, systemctl, unit)
					if err != nil {
						return err
					}
					if state == "failed" {
						if err := host.Run(cmd.Context(), systemctl, "reset-failed", unit); err != nil {
							return fmt.Errorf("systemd layer: reset failed state for %s after stop: %w", unit, err)
						}
						state, err = unitActiveState(cmd.Context(), host, systemctl, unit)
						if err != nil {
							return err
						}
					}
					if state != "inactive" {
						return fmt.Errorf("systemd layer: stop %s completed but ActiveState is %q, want inactive", unit, state)
					}
					role := ""
					switch path {
					case p.appUnit:
						role = "app"
					case p.dbUnit:
						role = "db"
					}
					if role != "" {
						container := name + "-" + role
						id, err := ownedResource(cmd.Context(), host, podman, podmanResource{"container", container}, owner)
						if err != nil {
							return fmt.Errorf("podman layer: verify %s absent after stopping %s: %w", container, unit, err)
						}
						if id != "" {
							return fmt.Errorf("podman layer: %s ExecStopPost left owned container %s present; refusing to remove it from stop", unit, container)
						}
					}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "stopped deployment %s\n", name)
				return nil
			}
			for _, path := range units {
				unit := filepath.Base(path)
				if err := host.Run(cmd.Context(), systemctl, "reset-failed", unit); err != nil {
					return fmt.Errorf("systemd layer: reset failed state for %s before start: %w", unit, err)
				}
			}
			for _, path := range units {
				unit := filepath.Base(path)
				if err := host.Run(cmd.Context(), systemctl, "start", unit); err != nil {
					return fmt.Errorf("systemd layer: start %s: %w", unit, err)
				}
			}
			if action == "start" {
				if err := waitDeployment(cmd.Context(), host, podman, name, owner); err != nil {
					return err
				}
			}
			for _, path := range units {
				unit := filepath.Base(path)
				state, err := unitActiveState(cmd.Context(), host, systemctl, unit)
				if err != nil {
					return err
				}
				if state != "active" {
					return fmt.Errorf("systemd layer: start readiness passed but %s ActiveState is %q, want active", unit, state)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "started deployment %s\n", name)
			return nil
		},
	}
}

func unitActiveState(ctx context.Context, host HostOperations, systemctl, unit string) (string, error) {
	state, err := host.RunOutput(ctx, systemctl, "show", "--property=ActiveState", "--value", unit)
	if err != nil {
		return "", fmt.Errorf("systemd layer: inspect ActiveState for %s: %w", unit, err)
	}
	if state == "" {
		return "", fmt.Errorf("systemd layer: inspect ActiveState for %s: empty result", unit)
	}
	return state, nil
}

func (a *App) runRemoveCommand() *cobra.Command {
	var volumes bool
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a deployment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := validateDeploymentName(name); err != nil {
				return err
			}
			host, systemctl, podman, err := a.runPreflight(true)
			if err != nil {
				return err
			}
			owner, err := deploymentOwner(host, deploymentPaths(name))
			if err != nil {
				return err
			}
			if err := verifyDeployment(cmd.Context(), host, podman, name, owner); err != nil {
				return err
			}
			unlock, err := lockDeployment(host, name)
			if err != nil {
				return err
			}
			defer unlock()
			if err := verifyDeployment(cmd.Context(), host, podman, name, owner); err != nil {
				return err
			}
			if err := removeDeployment(cmd.Context(), host, systemctl, podman, name, volumes); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed deployment %s\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&volumes, "volumes", false, "remove named volumes")
	cmd.Flags().BoolVar(&volumes, "volume", false, "remove named volumes (deprecated alias for --volumes)")
	_ = cmd.Flags().MarkDeprecated("volume", "use --volumes instead")
	return cmd
}

func removeDeployment(ctx context.Context, host HostOperations, systemctl, podman, name string, volumes bool) error {
	p := deploymentPaths(name)
	owner, err := deploymentOwner(host, p)
	if err != nil {
		return err
	}
	if err := verifyDeployment(ctx, host, podman, name, owner); err != nil {
		return err
	}
	for _, path := range []string{p.appUnit, p.dbUnit, p.networkUnit} {
		exists, err := host.Exists(path)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := host.Run(ctx, systemctl, "disable", "--now", filepath.Base(path)); err != nil {
			return fmt.Errorf("systemd layer: disable %s: %w", filepath.Base(path), err)
		}
	}
	for _, container := range []string{name + "-app", name + "-db"} {
		if err := removeOwnedResource(ctx, host, podman, podmanResource{"container", container}, owner); err != nil {
			return fmt.Errorf("podman layer: remove container %s: %w", container, err)
		}
	}
	for _, resource := range ownedDeploymentResources(name, owner) {
		if resource.kind == "volume" && !volumes {
			continue
		}
		if err := removeOwnedResource(ctx, host, podman, resource, owner); err != nil {
			return err
		}
	}
	for _, path := range []string{p.appUnit, p.dbUnit, p.networkUnit} {
		if exists, err := host.Exists(path); err != nil {
			return err
		} else if !exists {
			continue
		}
		if err := host.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filesystem layer: remove %s: %w", path, err)
		}
	}
	if err := host.Run(ctx, systemctl, "daemon-reload"); err != nil {
		return fmt.Errorf("systemd layer: daemon-reload: %w", err)
	}
	if volumes {
		if err := host.RemoveAll(p.configDir); err != nil {
			return fmt.Errorf("filesystem layer: remove %s: %w", p.configDir, err)
		}
	}
	return nil
}

func podmanResourceExists(ctx context.Context, host HostOperations, podman, kind, name string) (bool, error) {
	_, err := host.RunOutput(ctx, podman, kind, "exists", name)
	if err == nil {
		return true, nil
	}
	var exitError interface{ ExitCode() int }
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}
