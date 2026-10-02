package app

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
	templating "github.com/MozeBaltyk/Colt/internal/template"
	"github.com/spf13/cobra"
)

type initOptions struct {
	local       bool
	provider    string
	destination string
	visibility  string
	transport   string
	template    string
	sets        []string
}

func (a *App) initCommand() *cobra.Command {
	opts := initOptions{}
	cmd := &cobra.Command{
		Use:   "init <project>",
		Short: "Initialize a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("accepts at most 1 arg(s), received %d", len(args))
			}
			project := ""
			if len(args) == 1 {
				project = args[0]
			} else {
				var reader *bufio.Reader
				if err := a.requireInputs(cmd, &reader, requiredInput{"project", "the positional argument", &project}); err != nil {
					return err
				}
			}
			return a.initialize(cmd, project, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.local, "local", false, "create only the local repository")
	cmd.Flags().StringVarP(&opts.provider, "provider", "p", "", "provider alias")
	cmd.Flags().StringVarP(&opts.destination, "destination", "d", "", "remote clone destination (relative to the current directory or absolute)")
	cmd.Flags().StringVar(&opts.visibility, "visibility", "", "repository visibility (private or public)")
	cmd.Flags().StringVar(&opts.transport, "transport", "", "Git transport: https or ssh (default from config or https)")
	cmd.Flags().StringVar(&opts.template, "template", "", "configured template name or name@version")
	cmd.Flags().StringArrayVar(&opts.sets, "set", nil, "template parameter key=value (repeatable)")
	return cmd
}

func (a *App) initialize(cmd *cobra.Command, project string, opts initOptions) (retErr error) {
	report := newInitReport(project, opts.local)
	report.begin("Preflight")
	out := cmd.OutOrStdout()
	_, noColor := os.LookupEnv("NO_COLOR")
	report.color = !noColor && ((a.IsOutputTerminal != nil && a.IsOutputTerminal(out)) || (a.IsOutputTerminal == nil && outputIsTerminal(out)))
	defer func() {
		if retErr != nil {
			retErr = report.fail(retErr)
		}
		report.write(out)
	}()
	if a.pathErr != nil {
		return a.pathErr
	}
	if !config.ValidProjectName(project) {
		return errors.New("invalid project name; use 1-255 letters, digits, '.', '_' or '-' and no path separators")
	}
	if opts.local && opts.destination != "" {
		return errors.New("--destination applies only to remote initialization; omit it with --local")
	}
	if opts.visibility != "" && opts.visibility != "private" && opts.visibility != "public" {
		return errors.New("invalid --visibility; use private or public")
	}
	if opts.local && opts.visibility != "" {
		return errors.New("--visibility applies only to remote initialization; omit it with --local")
	}
	if opts.template == "" && len(opts.sets) != 0 {
		return errors.New("--set requires --template")
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		return err
	}
	alias, selected, err := cfg.Resolve(opts.provider)
	if err != nil {
		return err
	}
	report.provider = alias + " · namespace " + selected.Namespace
	var templatePlan *templating.Plan
	if opts.template != "" {
		plan, err := a.prepareTemplate(cmd, cfg, opts.template, opts.sets)
		if err != nil {
			return err
		}
		templatePlan = &plan
	}
	transport, err := InitTransport(selected, opts.transport, a.ConfigPath)
	if err != nil {
		return err
	}
	report.transport = transport
	if !opts.local {
		report.remoteSteps(transport == "https", selected.Type == "gitea" || selected.Type == "forgejo")
	}
	if templatePlan != nil {
		report.addTemplateStep()
	}
	if err := a.Git.Available(); err != nil {
		return err
	}
	workDir, destination, customDestination, err := a.initDestination(project, selected.Namespace, opts)
	if err != nil {
		return err
	}
	exists, err := validateDestination(destination)
	if err != nil {
		return err
	}
	if exists {
		report.local = "destination preserved at " + destination
	} else {
		report.local = "not created"
	}
	report.succeeded()
	token := ""
	if !opts.local {
		report.begin("Resolve provider API credential")
		token, _, err = config.Token(selected, a.credentialStore())
		if err != nil {
			return err
		}
		report.succeeded()
	}
	var client provider.Client
	gitUsername := ""
	if !opts.local {
		client, err = a.NewClient(selected, token)
		if err != nil {
			return err
		}
		if selected.Type == "gitea" || selected.Type == "forgejo" {
			report.begin("Authenticate provider API")
			gitUsername, err = client.Authenticate(cmd.Context())
			if err != nil {
				return err
			}
			report.succeeded()
		}
		report.begin("Look up remote repository")
		repo, getErr := client.Get(cmd.Context(), project)
		if getErr == nil && repo != nil {
			return fmt.Errorf("remote repository %s/%s already exists; choose another project name", selected.Namespace, project)
		}
		if getErr != nil && !errors.Is(getErr, provider.ErrNotFound) {
			return getErr
		}
		report.succeeded()
	}
	if opts.local {
		report.begin("Initialize local repository")
		if !exists {
			if err := os.Mkdir(destination, 0o755); err != nil {
				return fmt.Errorf("create destination: %w", err)
			}
			report.local = "destination preserved at " + destination
		}
		if err := a.Git.Init(cmd.Context(), destination); err != nil {
			return partial("initialize git", destination, "not created", "inspect the destination and retry after correcting git", err)
		}
		report.succeeded()
		if templatePlan != nil {
			report.begin("Materialize template")
			if err := materializeTemplate(destination, templatePlan); err != nil {
				return partial("materialize template", destination, "not created", "inspect the preserved destination and retry with a clean destination", err)
			}
			report.succeeded()
		}
		report.begin("Set repository-local identity")
		if err := a.Git.SetIdentity(cmd.Context(), destination, selected.GitName, selected.GitEmail); err != nil {
			return partial("set repository-local identity", destination, "not created", "set local user.name and user.email, then create the initial commit", err)
		}
		report.succeeded()
		report.begin("Create initial commit")
		commit, err := a.Git.Commit(cmd.Context(), destination)
		if err != nil {
			return partial("create initial commit", destination, "not created", "fix the reported git error and create the initial commit", err)
		}
		report.succeeded()
		report.local = "initial commit " + commit + " preserved at " + destination
		return nil
	}
	visibility := selected.Visibility
	if opts.visibility != "" {
		visibility = opts.visibility
	}
	report.begin("Create remote repository")
	repo, err := client.Create(cmd.Context(), project, visibility)
	if err != nil {
		remoteState := "creation outcome unknown; no rollback attempted"
		recovery := "check the configured namespace, then create or attach the remote manually as appropriate"
		if errors.Is(err, provider.ErrConflict) {
			remoteState = "provider conflict; not adopted, replaced, or deleted by Colt"
			recovery = "the provider conflict is authoritative; choose another name and do not adopt or delete the existing remote"
		}
		return partial("create remote repository", destination, remoteState, recovery, err)
	}
	if repo == nil {
		return partial("validate remote repository", destination, "creation reported success but omitted the repository target", "inspect the provider repository and configure origin manually only after verifying its authority", errors.New("provider omitted the created repository"))
	}
	report.succeeded()
	report.begin("Clone remote repository")
	cloneURL := repo.CloneURL
	valid := cleanHTTPSRepository
	if transport == "ssh" {
		cloneURL, valid = repo.SSHURL, cleanSSHRepository
	}
	if actual, ok := valid(cloneURL, selected); !ok || actual != project {
		return partial("validate remote repository", destination, "created but provider returned an unexpected clone target", "inspect the provider repository and configure origin manually only after verifying its authority", errors.New("provider returned a clone URL for a different authority or repository"))
	}
	report.remote = "created at " + cloneURL
	if !customDestination {
		err = createNamespaceDir(workDir, selected.Namespace)
	}
	if err != nil {
		return partial("create namespace directory", destination, "created at "+cloneURL, "correct the local namespace path and clone the remote", err)
	}
	if exists {
		if err := os.Remove(destination); err != nil {
			return partial("prepare clone destination", destination, "created at "+cloneURL, "remove the empty destination and clone the remote", err)
		}
	}
	if err := a.Git.Clone(cmd.Context(), cloneURL, destination, gitUsername, alias, project); err != nil {
		return partial("clone remote repository", destination, "created at "+cloneURL, "inspect the destination and retry the clone after fixing authentication or connectivity", err)
	}
	report.succeeded()
	report.local = "clone preserved at " + destination
	if templatePlan != nil {
		report.begin("Materialize template")
		if err := gitnative.RequireUnbornHEAD(cmd.Context(), destination); err != nil {
			return partial("validate cloned repository history", destination, "created at "+cloneURL, "inspect the created remote; template initialization requires an unborn HEAD", err)
		}
		if err := materializeTemplate(destination, templatePlan); err != nil {
			return partial("materialize template", destination, "created at "+cloneURL, "inspect the preserved clone and retry after correcting local filesystem state", err)
		}
		report.succeeded()
	}
	report.begin("Set repository-local identity")
	if err := a.Git.SetIdentity(cmd.Context(), destination, selected.GitName, selected.GitEmail); err != nil {
		return partial("set repository-local identity", destination, "created at "+cloneURL, "set local user.name and user.email, then create the initial commit", err)
	}
	report.succeeded()
	report.begin("Create initial commit")
	commit, err := a.Git.Commit(cmd.Context(), destination)
	if err != nil {
		return partial("create initial commit", destination, "created at "+cloneURL, "fix the reported git error and create the initial commit", err)
	}
	report.succeeded()
	report.local = "initial commit " + commit + " preserved at " + destination
	if transport == "https" {
		report.begin("Configure HTTPS credential helper")
		if err := a.Git.ConfigureCredentialHelper(cmd.Context(), destination, cloneURL, gitUsername, alias, project); err != nil {
			return partial("configure credential helper", destination, "created at "+cloneURL, "configure the Colt helper locally, then push HEAD", err)
		}
		report.succeeded()
	}
	report.begin("Push initial commit")
	if err := a.Git.Push(cmd.Context(), destination, cloneURL, alias, project); err != nil {
		return partial("push initial commit", destination, "created at "+cloneURL, "from the preserved local repository run: git push --set-upstream origin HEAD", err)
	}
	report.succeeded()
	return nil
}

func (a *App) initDestination(project, namespace string, opts initOptions) (root, destination string, custom bool, err error) {
	root = a.WorkDir
	if opts.local {
		if root == "" {
			root, err = os.Getwd()
		}
		if err == nil {
			err = validateWorkDir(root)
		}
		return root, filepath.Join(root, project), false, err
	}
	if opts.destination != "" {
		destination = opts.destination
		if !filepath.IsAbs(destination) {
			if root == "" {
				root, err = os.Getwd()
			}
			if err != nil {
				return "", "", true, fmt.Errorf("determine working directory: %w", err)
			}
			destination = filepath.Join(root, destination)
		}
		root = filepath.Dir(destination)
		info, statErr := os.Lstat(root)
		if statErr != nil {
			return root, destination, true, fmt.Errorf("inspect destination parent: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return root, destination, true, errors.New("destination parent is not an ordinary directory")
		}
		return root, destination, true, nil
	}
	if root == "" {
		root, err = os.UserHomeDir()
		if err != nil {
			return "", "", false, fmt.Errorf("determine user home directory: %w", err)
		}
	}
	if err = validateWorkDir(root); err == nil {
		err = validateNamespacePath(root, namespace)
	}
	return root, filepath.Join(root, filepath.FromSlash(namespace), project), false, err
}

func createNamespaceDir(root, namespace string) error {
	if err := validateNamespacePath(root, namespace); err != nil {
		return err
	}
	current := root
	for _, part := range strings.Split(namespace, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return fmt.Errorf("create namespace directory: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect namespace directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("namespace path contains a non-directory or symbolic link")
		}
	}
	return nil
}

func validateNamespacePath(root, namespace string) error {
	current := root
	for _, part := range strings.Split(namespace, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect namespace directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("namespace path contains a non-directory or symbolic link")
		}
	}
	return nil
}

func validateWorkDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect working directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("working directory is not a directory")
	}
	return nil
}

func validateDestination(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, errors.New("destination exists and is not an empty ordinary directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("inspect destination: %w", err)
	}
	if len(entries) != 0 {
		return false, errors.New("destination exists and is non-empty; refusing to modify it")
	}
	return true, nil
}
