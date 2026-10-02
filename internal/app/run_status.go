package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func (a *App) runStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status [name]",
		Short: "Show deployment status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if err := validateDeploymentName(args[0]); err != nil {
					return err
				}
			}
			host, systemctl, podman, err := a.runPreflight(true)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				names, err := deploymentNames(host)
				if err != nil {
					return err
				}
				if len(names) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "no deployments found")
					return nil
				}
				for _, name := range names {
					p := deploymentPaths(name)
					states := deploymentUnitStates(cmd.Context(), host, systemctl, p)
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  app=%s db=%s network=%s\n", name, deploymentLifecycle(host, p), states["app"], states["database"], states["network"])
				}
				return nil
			}
			name := args[0]
			p := deploymentPaths(name)
			fmt.Fprintf(cmd.OutOrStdout(), "ownership/lifecycle: %s\n", deploymentLifecycle(host, p))
			states := deploymentUnitStates(cmd.Context(), host, systemctl, p)
			for _, label := range []string{"network", "database", "app"} {
				fmt.Fprintf(cmd.OutOrStdout(), "%s unit: %s\n", label, states[label])
			}
			info := statusDeploymentInfo(host, p)
			browserURL := info.externalURL
			if browserURL == "" {
				browserURL = "http://127.0.0.1:3000/"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "type: %s\nimage: %s\nbrowser URL: %s\nports: 3000, 2222\n", info.deploymentType, info.image, browserURL)
			owner, ownerErr := deploymentOwner(host, p)
			for _, volume := range []string{name + "-data", name + "-config", name + "-db"} {
				if ownerErr == nil {
					volume += "-" + owner
				}
				path, err := host.RunOutput(cmd.Context(), podman, "volume", "inspect", "--format", "{{.Mountpoint}}", volume)
				if err != nil || path == "" {
					fmt.Fprintf(cmd.OutOrStdout(), "volume %s: unavailable\n", volume)
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "volume %s: %s\n", volume, path)
			}
			return nil
		},
	}
}

func deploymentNames(host HostOperations) ([]string, error) {
	names := map[string]bool{}
	entries, err := host.ReadDir(runDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("filesystem layer: list deployments: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() && validateDeploymentName(entry.Name()) == nil {
			names[entry.Name()] = true
		}
	}
	entries, err = host.ReadDir(unitDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("filesystem layer: list deployment units: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			if name := deploymentNameFromUnit(entry.Name()); name != "" {
				names[name] = true
			}
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func deploymentNameFromUnit(unit string) string {
	var name string
	if strings.HasPrefix(unit, "podman-network-") && strings.HasSuffix(unit, "-net.service") {
		name = strings.TrimSuffix(strings.TrimPrefix(unit, "podman-network-"), "-net.service")
	} else if strings.HasPrefix(unit, "container-") {
		name = strings.TrimPrefix(unit, "container-")
		if strings.HasSuffix(name, "-app.service") {
			name = strings.TrimSuffix(name, "-app.service")
		} else if strings.HasSuffix(name, "-db.service") {
			name = strings.TrimSuffix(name, "-db.service")
		} else {
			return ""
		}
	}
	if validateDeploymentName(name) != nil {
		return ""
	}
	return name
}

func deploymentUnitStates(ctx context.Context, host HostOperations, systemctl string, p runPaths) map[string]string {
	states := map[string]string{}
	for _, unit := range []struct{ label, path string }{{"network", p.networkUnit}, {"database", p.dbUnit}, {"app", p.appUnit}} {
		exists, err := host.Exists(unit.path)
		if err != nil {
			states[unit.label] = "unavailable"
		} else if !exists {
			states[unit.label] = "absent"
		} else if state, err := host.RunOutput(ctx, systemctl, "show", "--property=ActiveState", "--value", filepath.Base(unit.path)); err != nil || state == "" {
			states[unit.label] = "unavailable"
		} else {
			states[unit.label] = state
		}
	}
	return states
}

func deploymentLifecycle(host HostOperations, p runPaths) string {
	if _, err := deploymentOwner(host, p); err != nil {
		return "legacy (read-only)"
	}
	for _, path := range []string{p.networkUnit, p.dbUnit, p.appUnit} {
		if exists, err := host.Exists(path); err != nil || !exists {
			return "retained/partial"
		}
	}
	return "managed"
}

func statusDeploymentInfo(host HostOperations, p runPaths) deploymentMetadata {
	info := deploymentMetadata{"unknown", "unknown", ""}
	metadataImage := false
	if data, err := host.ReadFile(p.metadata); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch key {
			case "type":
				if value == "gitea" || value == "forgejo" {
					info.deploymentType = value
				}
			case "image":
				if imageRefRE.MatchString(value) && !strings.Contains(value, "..") {
					info.image = value
					metadataImage = true
				}
			case "external_url":
				if normalized, err := normalizeExternalURL(value); value != "" && err == nil && normalized == value {
					info.externalURL = value
				}
			}
		}
	}
	data, err := host.ReadFile(p.appUnit)
	if err == nil && info.image == "unknown" {
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "ExecStart=") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 0 && imageRefRE.MatchString(fields[len(fields)-1]) && !strings.Contains(fields[len(fields)-1], "..") {
				info.image = fields[len(fields)-1]
				break
			}
		}
	}
	if info.deploymentType == "unknown" && info.image != "unknown" {
		if metadataImage {
			info.deploymentType = "gitea" // image-only metadata predates Forgejo support
		} else if strings.Contains(strings.ToLower(info.image), "forgejo") {
			info.deploymentType = "forgejo"
		} else if strings.Contains(strings.ToLower(info.image), "gitea") {
			info.deploymentType = "gitea"
		}
	}
	return info
}

type deploymentMetadata struct{ deploymentType, image, externalURL string }

func deploymentInfo(host HostOperations, p runPaths) (deploymentMetadata, error) {
	if exists, err := host.Exists(p.metadata); err != nil {
		return deploymentMetadata{}, fmt.Errorf("filesystem layer: inspect deployment metadata: %w", err)
	} else if exists {
		data, err := host.ReadFile(p.metadata)
		if err != nil {
			return deploymentMetadata{}, fmt.Errorf("filesystem layer: read deployment metadata: %w", err)
		}
		values := map[string]string{}
		seen := map[string]bool{}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if !ok || (key != "type" && key != "image" && key != "external_url") || seen[key] {
				return deploymentMetadata{}, errors.New("filesystem layer: invalid deployment metadata")
			}
			seen[key] = true
			values[key] = value
		}
		deploymentType := values["type"]
		if deploymentType == "" {
			deploymentType = "gitea"
		}
		image := values["image"]
		if (deploymentType != "gitea" && deploymentType != "forgejo") || !imageRefRE.MatchString(image) || strings.Contains(image, "..") {
			return deploymentMetadata{}, errors.New("filesystem layer: invalid deployment metadata")
		}
		externalURL := values["external_url"]
		if externalURL != "" {
			normalized, err := normalizeExternalURL(externalURL)
			if err != nil || normalized != externalURL {
				return deploymentMetadata{}, errors.New("filesystem layer: invalid deployment metadata")
			}
		}
		return deploymentMetadata{deploymentType, image, externalURL}, nil
	}
	data, err := host.ReadFile(p.appUnit)
	if err != nil {
		return deploymentMetadata{}, fmt.Errorf("filesystem layer: read app unit: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 0 {
			image := fields[len(fields)-1]
			if imageRefRE.MatchString(image) && !strings.Contains(image, "..") {
				return deploymentMetadata{"gitea", image, ""}, nil
			}
		}
	}
	return deploymentMetadata{"gitea", giteaImage, ""}, nil
}
