package app

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
	templating "github.com/MozeBaltyk/Colt/internal/template"
	"github.com/spf13/cobra"
)

func (a *App) templateCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "template", Short: "Inspect configured templates"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List configured template versions",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if a.pathErr != nil {
					return a.pathErr
				}
				cfg, err := config.Load(a.ConfigPath)
				if err != nil {
					return err
				}
				var refs []string
				for name, template := range cfg.Templates {
					for version := range template.Versions {
						ref := name + "@" + version
						if version == template.Default {
							ref += " (default)"
						}
						refs = append(refs, ref)
					}
				}
				sort.Strings(refs)
				for _, ref := range refs {
					fmt.Fprintln(cmd.OutOrStdout(), ref)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "show <name>[@<version>]",
			Short: "Show configured template metadata",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if a.pathErr != nil {
					return a.pathErr
				}
				cfg, err := config.Load(a.ConfigPath)
				if err != nil {
					return err
				}
				name, version, configured, err := cfg.ResolveTemplate(args[0])
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "Template: %s\nResolved: %s@%s\nSource: %s\nDigest: %s\nParameters:\n", name, name, version, configured.Source, configured.Digest)
				parameters := make([]string, 0, len(configured.Parameters))
				for parameter := range configured.Parameters {
					parameters = append(parameters, parameter)
				}
				sort.Strings(parameters)
				for _, parameter := range parameters {
					declaration := configured.Parameters[parameter]
					detail := "optional (default empty)"
					if declaration.Required {
						detail = "required"
					} else if declaration.Default != nil {
						detail = "default " + strconv.Quote(*declaration.Default)
					}
					fmt.Fprintf(out, "  %s: %s\n", parameter, detail)
				}
				return nil
			},
		},
	)
	return cmd
}

func (a *App) prepareTemplate(cmd *cobra.Command, cfg config.Config, ref string, assignments []string) (templating.Plan, error) {
	_, _, configured, err := cfg.ResolveTemplate(ref)
	if err != nil {
		return templating.Plan{}, err
	}
	values := make(map[string]string, len(configured.Parameters))
	for _, assignment := range assignments {
		key, value, ok := strings.Cut(assignment, "=")
		if !ok || key == "" {
			return templating.Plan{}, fmt.Errorf("invalid --set %q; use key=value", assignment)
		}
		if _, ok := configured.Parameters[key]; !ok {
			return templating.Plan{}, fmt.Errorf("unknown template parameter %q", key)
		}
		if _, duplicate := values[key]; duplicate {
			return templating.Plan{}, fmt.Errorf("duplicate template parameter %q", key)
		}
		if err := validateTemplateParameterValue(key, value); err != nil {
			return templating.Plan{}, fmt.Errorf("template parameter %q must be one line and at most 65536 bytes", key)
		}
		values[key] = value
	}
	parameters := make([]string, 0, len(configured.Parameters))
	for parameter := range configured.Parameters {
		parameters = append(parameters, parameter)
	}
	sort.Strings(parameters)
	var reader *bufio.Reader
	for _, parameter := range parameters {
		declaration := configured.Parameters[parameter]
		if value, supplied := values[parameter]; supplied {
			if declaration.Required && value == "" {
				return templating.Plan{}, fmt.Errorf("required template parameter %q is empty", parameter)
			}
			continue
		}
		if declaration.Default != nil {
			values[parameter] = *declaration.Default
			continue
		}
		if !declaration.Required {
			values[parameter] = ""
			continue
		}
		value := ""
		if err := a.requireInputs(cmd, &reader, requiredInput{"template parameter " + parameter, "--set " + parameter + "=value", &value}); err != nil {
			return templating.Plan{}, err
		}
		if err := validateTemplateParameterValue(parameter, value); err != nil {
			return templating.Plan{}, err
		}
		values[parameter] = value
	}
	source := templating.SourcePath(a.ConfigPath, configured.Source)
	return templating.Prepare(source, configured.Digest, configured.Parameters, values)
}

func validateTemplateParameterValue(parameter, value string) error {
	if strings.ContainsAny(value, "\r\n") || len(value) > maxInteractiveInput {
		return fmt.Errorf("template parameter %q must be one line and at most 65536 bytes", parameter)
	}
	return nil
}

func materializeTemplate(destination string, plan *templating.Plan) error {
	root, err := templating.OpenDestination(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := validateEmptyTemplateWorktree(root); err != nil {
		return err
	}
	return plan.MaterializeRoot(root)
}

func validateEmptyTemplateWorktree(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("inspect template destination: %w", err)
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Errorf("inspect template destination: %w", errors.Join(readErr, closeErr))
	}
	for _, entry := range entries {
		if entry.Name() != ".git" {
			return errors.New("template destination contains content not validated from the template")
		}
		info, err := root.Lstat(entry.Name())
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("template destination Git metadata is not an ordinary directory")
		}
	}
	return nil
}
