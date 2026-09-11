package app

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/spf13/cobra"
)

type App struct {
	ConfigPath string
	WorkDir    string
	Git        gitnative.Runner
	NewClient  func(config.Provider, string) (provider.Client, error)
	pathErr    error
}

func New() *App {
	path, err := config.Path()
	httpClient := &http.Client{}
	return &App{
		ConfigPath: path,
		Git:        gitnative.Native{},
		NewClient: func(p config.Provider, token string) (provider.Client, error) {
			return provider.New(p, token, httpClient)
		},
		pathErr: err,
	}
}

func (a *App) Root() *cobra.Command {
	root := &cobra.Command{
		Use:               "colt",
		Short:             "Initialize provider-independent Git projects",
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.AddCommand(a.authCommand(), a.initCommand())
	return root
}

type authOptions struct {
	host, baseURL, namespace, visibility, gitName, gitEmail, tokenEnv string
	replace, makeDefault                                              bool
}

func (a *App) authCommand() *cobra.Command {
	auth := &cobra.Command{
		Use:   "auth",
		Short: "Configure provider authentication",
		Long: "Configure provider authentication.\n\n" +
			"Configuration is stored in the OS user configuration directory. On Linux, the path is\n" +
			"$XDG_CONFIG_HOME/colt/config.yaml when XDG_CONFIG_HOME is set, otherwise\n" +
			"~/.config/colt/config.yaml. COLT_CONFIG overrides the complete path.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	opts := authOptions{}
	login := &cobra.Command{
		Use:   "login <github|gitlab> <alias>",
		Short: "Log in to a provider",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.loginProvider(cmd, args[0], args[1], opts)
		},
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show configured providers (offline)",
		Args:  cobra.NoArgs,
		RunE:  a.providerStatus,
	}
	f := login.Flags()
	f.StringVar(&opts.host, "host", "", "provider host (defaults to github.com or gitlab.com)")
	f.StringVar(&opts.baseURL, "base-url", "", "HTTPS API base URL (defaults from provider host)")
	f.StringVar(&opts.namespace, "namespace", "", "repository owner: GitHub username or organization; GitLab group or subgroup/full path (required)")
	f.StringVar(&opts.visibility, "visibility", "private", "default repository visibility")
	f.StringVar(&opts.gitName, "git-name", "", "repository-local Git author name (required)")
	f.StringVar(&opts.gitEmail, "git-email", "", "repository-local Git author email (required)")
	f.StringVar(&opts.tokenEnv, "token-env", "", "token environment variable (provider default when omitted)")
	f.BoolVar(&opts.makeDefault, "default", false, "select this provider by default")
	f.BoolVar(&opts.replace, "replace", false, "replace an existing alias after validation")
	auth.AddCommand(login, status)
	return auth
}

func (a *App) providerStatus(cmd *cobra.Command, _ []string) error {
	if a.pathErr != nil {
		return a.pathErr
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		return err
	}
	aliases := make([]string, 0, len(cfg.Providers))
	for alias := range cfg.Providers {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	if len(aliases) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no providers configured")
		return nil
	}
	for _, alias := range aliases {
		p := cfg.Providers[alias]
		fmt.Fprintf(cmd.OutOrStdout(), "alias=%s type=%s host=%s namespace=%s default=%t\n", alias, p.Type, p.Host, p.Namespace, p.Default)
	}
	return nil
}

func (a *App) loginProvider(cmd *cobra.Command, providerType, alias string, opts authOptions) error {
	if a.pathErr != nil {
		return a.pathErr
	}
	host := opts.host
	baseURL := opts.baseURL
	if providerType == "github" {
		if host == "" {
			host = "github.com"
		}
		if baseURL == "" {
			baseURL = "https://api.github.com"
		}
	} else if providerType == "gitlab" {
		if host == "" {
			host = "gitlab.com"
		}
		if baseURL == "" {
			baseURL = "https://" + host
		}
	}
	p := config.Provider{
		Type: providerType, Host: host, BaseURL: strings.TrimRight(baseURL, "/"), Namespace: opts.namespace,
		Visibility: opts.visibility, GitName: opts.gitName, GitEmail: opts.gitEmail, TokenEnv: opts.tokenEnv, Default: opts.makeDefault,
	}
	if err := config.ValidateProvider(alias, p); err != nil {
		return fmt.Errorf("invalid provider %q: %w", alias, err)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		return err
	}
	if _, exists := cfg.Providers[alias]; exists && !opts.replace {
		return fmt.Errorf("provider alias %q already exists; use --replace to replace it", alias)
	}
	candidate := config.Config{Providers: make(map[string]config.Provider, len(cfg.Providers)+1)}
	for key, value := range cfg.Providers {
		if opts.makeDefault {
			value.Default = false
		}
		candidate.Providers[key] = value
	}
	candidate.Providers[alias] = p
	if err := candidate.Validate(); err != nil {
		return err
	}
	token, _, err := config.Token(p)
	if err != nil {
		return err
	}
	client, err := a.NewClient(p, token)
	if err != nil {
		return err
	}
	account, err := client.Authenticate(cmd.Context())
	if err != nil {
		return err
	}
	if err := config.Save(a.ConfigPath, candidate); err != nil {
		return fmt.Errorf("authentication succeeded but config was not changed: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "configured provider %s (authenticated as %s)\n", alias, account)
	return nil
}

type initOptions struct {
	local    bool
	provider string
}

func (a *App) initCommand() *cobra.Command {
	opts := initOptions{}
	cmd := &cobra.Command{
		Use:   "init <project>",
		Short: "Initialize a blank project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.initialize(cmd, args[0], opts)
		},
	}
	cmd.Flags().BoolVar(&opts.local, "local", false, "create only the local repository")
	cmd.Flags().StringVar(&opts.provider, "provider", "", "provider alias")
	return cmd
}

func (a *App) initialize(cmd *cobra.Command, project string, opts initOptions) error {
	if a.pathErr != nil {
		return a.pathErr
	}
	if !config.ValidProjectName(project) {
		return errors.New("invalid project name; use 1-100 letters, digits, '.', '_' or '-' and no path separators")
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		return err
	}
	alias, selected, err := cfg.Resolve(opts.provider)
	if err != nil {
		return err
	}
	token, _, err := config.Token(selected)
	if err != nil {
		return err
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
	exists, err := validateDestination(destination)
	if err != nil {
		return err
	}
	var client provider.Client
	if !opts.local {
		client, err = a.NewClient(selected, token)
		if err != nil {
			return err
		}
		repo, getErr := client.Get(cmd.Context(), project)
		if getErr == nil && repo != nil {
			return fmt.Errorf("remote repository %s/%s already exists; choose another project name", selected.Namespace, project)
		}
		if getErr != nil && !errors.Is(getErr, provider.ErrNotFound) {
			return getErr
		}
	}
	if !exists {
		if err := os.Mkdir(destination, 0o755); err != nil {
			return fmt.Errorf("create destination: %w", err)
		}
	}
	if err := a.Git.Init(cmd.Context(), destination); err != nil {
		return partial("initialize git", destination, "not created", "inspect the destination and retry after correcting git", err)
	}
	if err := a.Git.SetIdentity(cmd.Context(), destination, selected.GitName, selected.GitEmail); err != nil {
		return partial("set repository-local identity", destination, "not created", "set local user.name and user.email, then create the initial commit", err)
	}
	commit, err := a.Git.Commit(cmd.Context(), destination)
	if err != nil {
		return partial("create initial commit", destination, "not created", "fix the reported git error and create the initial commit", err)
	}
	if opts.local {
		fmt.Fprintf(cmd.OutOrStdout(), "initialized %s with provider %s identity at %s; initial commit %s (local only)\n", project, alias, destination, commit)
		return nil
	}
	repo, err := client.Create(cmd.Context(), project)
	if err != nil {
		remoteState := "creation outcome unknown; no rollback attempted"
		recovery := "check the configured namespace, then create or attach the remote manually as appropriate"
		if errors.Is(err, provider.ErrConflict) {
			remoteState = "provider conflict; not adopted, replaced, or deleted by Colt"
			recovery = "the provider conflict is authoritative; choose another name and do not adopt or delete the existing remote"
		}
		return partial("create remote repository", destination, remoteState, recovery, err)
	}
	if err := a.Git.AddOrigin(cmd.Context(), destination, repo.CloneURL); err != nil {
		return partial("add origin", destination, "created at "+repo.CloneURL, "add the clean HTTPS URL as origin, then push HEAD", err)
	}
	if err := a.Git.Push(cmd.Context(), destination, repo.CloneURL, selected.Type, token); err != nil {
		return partial("push initial commit", destination, "created at "+repo.CloneURL, "run 'git push --set-upstream origin HEAD' after fixing authentication or connectivity", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "initialized %s via %s in namespace %s at %s; initial commit %s; remote %s\n", project, alias, selected.Namespace, destination, commit, repo.CloneURL)
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

func partial(step, local, remote, recovery string, cause error) error {
	return fmt.Errorf("partial failure at %s: local state preserved at %s; remote state: %s; recovery: %s: %w", step, local, remote, recovery, cause)
}
