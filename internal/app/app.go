package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/spf13/cobra"
)

type App struct {
	ConfigPath  string
	WorkDir     string
	Git         gitnative.Runner
	NewClient   func(config.Provider, string) (provider.Client, error)
	Credentials credential.Store
	pathErr     error
}

func New() *App {
	path, err := config.Path()
	httpClient := &http.Client{}
	return &App{
		ConfigPath:  path,
		Git:         gitnative.Native{},
		Credentials: credential.DisabledStore{},
		NewClient: func(p config.Provider, token string) (provider.Client, error) {
			return provider.New(p, token, httpClient)
		},
		pathErr: err,
	}
}

func (a *App) credentialStore() credential.Store {
	if a.Credentials != nil {
		return a.Credentials
	}
	return credential.DisabledStore{}
}

func (a *App) Root() *cobra.Command {
	root := &cobra.Command{
		Use:               "colt",
		Short:             "Initialize provider-independent Git projects",
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.AddCommand(a.authCommand(), a.initCommand(), a.gitCredentialCommand())
	return root
}

func (a *App) gitCredentialCommand() *cobra.Command {
	return &cobra.Command{
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
			p, ok := credentialProvider(cfg, request)
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
		if _, exists := request[key]; exists {
			return nil, errors.New("invalid Git credential request: duplicate field")
		}
		request[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("invalid Git credential request: input too large")
	}
	return request, nil
}

func credentialProvider(cfg config.Config, request map[string]string) (config.Provider, bool) {
	if request["protocol"] != "https" || request["host"] == "" || request["path"] == "" {
		return config.Provider{}, false
	}
	raw := "https://" + request["host"] + "/" + strings.TrimPrefix(request["path"], "/")
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

func cleanHTTPSRepository(raw string, p config.Provider) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !strings.EqualFold(u.Host, p.Host) {
		return "", false
	}
	prefix := "/" + p.Namespace + "/"
	if !strings.HasPrefix(u.Path, prefix) || !strings.HasSuffix(u.Path, ".git") {
		return "", false
	}
	project := strings.TrimSuffix(strings.TrimPrefix(u.Path, prefix), ".git")
	return project, config.ValidProjectName(project)
}

type authOptions struct {
	host, baseURL, namespace, visibility, gitName, gitEmail, tokenEnv, transport string
	replace, makeDefault                                                         bool
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
		Use:   "login <github|gitlab|gitea|forgejo> <alias>",
		Short: "Log in to a provider",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.loginProvider(cmd, args[0], args[1], opts)
		},
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show configured providers",
		Args:  cobra.NoArgs,
		RunE:  a.providerStatus,
	}
	status.Flags().Bool("offline", false, "inspect configuration only, do not call provider APIs")
	revoke := false
	logout := &cobra.Command{
		Use:   "logout <alias>",
		Short: "Remove a locally stored provider credential",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if revoke {
				return errors.New("--revoke is not supported; no local or remote credential was changed")
			}
			return a.logoutProvider(cmd, args[0])
		},
	}
	logout.Flags().BoolVar(&revoke, "revoke", false, "unsupported: provider-side revocation is not implemented")
	f := login.Flags()
	f.StringVar(&opts.host, "host", "", "provider host (required for Gitea/Forgejo; defaults for GitHub and GitLab)")
	f.StringVar(&opts.baseURL, "base-url", "", "HTTPS API base URL (defaults from provider host)")
	f.StringVar(&opts.namespace, "namespace", "", "repository owner: GitHub/Gitea/Forgejo user or organization; GitLab group or subgroup/full path (required)")
	f.StringVar(&opts.visibility, "visibility", "private", "default repository visibility")
	f.StringVar(&opts.gitName, "git-name", "", "repository-local Git author name (required)")
	f.StringVar(&opts.gitEmail, "git-email", "", "repository-local Git author email (required)")
	f.StringVar(&opts.tokenEnv, "token-env", "", "token environment variable (provider default when omitted)")
	f.StringVar(&opts.transport, "transport", "", "Git transport: https or ssh (default https)")
	f.BoolVar(&opts.makeDefault, "default", false, "select this provider by default")
	f.BoolVar(&opts.replace, "replace", false, "replace an existing alias after validation")
	auth.AddCommand(login, logout, status)
	return auth
}

func (a *App) logoutProvider(cmd *cobra.Command, alias string) error {
	if a.pathErr != nil {
		return a.pathErr
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		return err
	}
	p, ok := cfg.Providers[alias]
	if !ok {
		return fmt.Errorf("unknown provider alias %q; configure it or choose an existing alias", alias)
	}
	envName := p.Auth.TokenEnv
	if envName == "" {
		envName = credential.ConventionalVar(p.Type)
	}
	if p.Auth.Source == "env" {
		fmt.Fprintf(cmd.OutOrStdout(), "no stored credential removed for %s; ! %s may still provide credentials (environment unchanged)\n", alias, envName)
		return nil
	}

	err = a.credentialStore().Delete(p.Auth.CredentialID)
	switch {
	case errors.Is(err, credential.ErrNotFound):
		fmt.Fprintf(cmd.OutOrStdout(), "no stored credential found for %s; nothing changed\n", p.Auth.CredentialID)
	case errors.Is(err, credential.ErrStoreUnavailable):
		return errors.New("credential storage unavailable; local credential was not removed")
	case err != nil:
		return errors.New("credential storage failure; local credential was not removed safely")
	default:
		fmt.Fprintf(cmd.OutOrStdout(), "removed stored credential %s; provider configuration unchanged\n", p.Auth.CredentialID)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "! %s may still provide credentials (environment unchanged)\n", envName)
	return nil
}

func (a *App) providerStatus(cmd *cobra.Command, _ []string) error {
	if a.pathErr != nil {
		return a.pathErr
	}
	offline, _ := cmd.Flags().GetBool("offline")
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
		defaultMarker := ""
		if p.Default {
			defaultMarker = " (default)"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s%s\n", alias, defaultMarker)
		fmt.Fprintf(cmd.OutOrStdout(), "  %s · %s\n", providerTypeName(p.Type), p.Host)
		if offline {
			fmt.Fprintf(cmd.OutOrStdout(), "  Namespace:   %s\n", p.Namespace)
			if p.GitName == "" || p.GitEmail == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "  Git identity: not configured")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  Git name:    %s\n", p.GitName)
				fmt.Fprintf(cmd.OutOrStdout(), "  Git email:   %s\n", p.GitEmail)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "  Connection:  not checked")
		} else {
			token, _, tokenErr := config.Token(p, a.credentialStore())
			if tokenErr != nil {
				if errors.Is(tokenErr, credential.ErrMissing) {
					fmt.Fprintln(cmd.OutOrStdout(), "  Connection:  ✗ credentials missing")
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "  Connection:  ✗ credential storage failure")
				}
				continue
			}
			client, clientErr := a.NewClient(p, token)
			if clientErr != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "  Connection:  ✗ unreachable\n    %s\n", safeExplanation(clientErr.Error()))
				continue
			}
			account, authErr := client.Authenticate(cmd.Context())
			if authErr != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "  Connection:  ✗ %s\n", authState(authErr))
				if exp := safeExplanation(authErr.Error()); exp != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "    %s\n", exp)
				}
				continue
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  Account:     %s\n", account)
			fmt.Fprintf(cmd.OutOrStdout(), "  Namespace:   %s\n", p.Namespace)
			if p.GitName == "" || p.GitEmail == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "  Git identity: not configured")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  Git name:    %s\n", p.GitName)
				fmt.Fprintf(cmd.OutOrStdout(), "  Git email:   %s\n", p.GitEmail)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "  Connection:  ✓ connected")
		}
	}
	return nil
}

func providerTypeName(t string) string {
	switch t {
	case "github":
		return "GitHub"
	case "gitlab":
		return "GitLab"
	case "gitea":
		return "Gitea"
	case "forgejo":
		return "Forgejo"
	default:
		return t
	}
}

func authState(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "credential environment variable") || strings.Contains(msg, "missing or empty") {
		return "credentials missing"
	}
	if strings.Contains(msg, "authentication failed") {
		return "authentication failed"
	}
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded") {
		return "timeout"
	}
	return "unreachable"
}

func safeExplanation(msg string) string {
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
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
	} else if providerType == "gitea" && host != "" && baseURL == "" {
		baseURL = "https://" + host
	} else if providerType == "forgejo" && host != "" && baseURL == "" {
		baseURL = "https://" + host
	}
	p := config.Provider{
		Type: providerType, Host: host, BaseURL: strings.TrimRight(baseURL, "/"), Namespace: opts.namespace,
		Visibility: opts.visibility, GitName: opts.gitName, GitEmail: opts.gitEmail,
		Transport: opts.transport, Auth: config.Auth{Source: "env", TokenEnv: opts.tokenEnv}, Default: opts.makeDefault,
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
	token, _, err := config.Token(p, a.credentialStore())
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
	transport, err := initTransport(selected)
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
	if err := validateWorkDir(workDir); err != nil {
		return err
	}
	destination := filepath.Join(workDir, project)
	exists, err := validateDestination(destination)
	if err != nil {
		return err
	}
	token := ""
	if !opts.local {
		token, _, err = config.Token(selected, a.credentialStore())
		if err != nil {
			return err
		}
	}
	var client provider.Client
	gitUsername := ""
	if !opts.local {
		client, err = a.NewClient(selected, token)
		if err != nil {
			return err
		}
		if selected.Type == "gitea" || selected.Type == "forgejo" {
			gitUsername, err = client.Authenticate(cmd.Context())
			if err != nil {
				return err
			}
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
	if repo == nil {
		return partial("validate remote repository", destination, "creation reported success but omitted the repository target", "inspect the provider repository and configure origin manually only after verifying its authority", errors.New("provider omitted the created repository"))
	}
	cloneURL := repo.CloneURL
	valid := cleanHTTPSRepository
	if transport == "ssh" {
		cloneURL, valid = repo.SSHURL, cleanSSHRepository
	}
	if actual, ok := valid(cloneURL, selected); !ok || actual != project {
		return partial("validate remote repository", destination, "created but provider returned an unexpected clone target", "inspect the provider repository and configure origin manually only after verifying its authority", errors.New("provider returned a clone URL for a different authority or repository"))
	}
	if err := a.Git.AddOrigin(cmd.Context(), destination, cloneURL); err != nil {
		return partial("add origin", destination, "created at "+cloneURL, "add the validated clone URL as origin, then push HEAD", err)
	}
	pushToken := ""
	if transport == "https" {
		pushToken = token
		if err := a.Git.ConfigureCredentialHelper(cmd.Context(), destination, cloneURL, gitUsername); err != nil {
			return partial("configure credential helper", destination, "created at "+cloneURL, "configure the Colt helper locally, then push HEAD", err)
		}
	}
	if err := a.Git.Push(cmd.Context(), destination, cloneURL, selected.Type, gitUsername, pushToken); err != nil {
		return partial("push initial commit", destination, "created at "+cloneURL, "run 'git push --set-upstream origin HEAD' after fixing authentication or connectivity", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "initialized %s via %s in namespace %s at %s; initial commit %s; remote %s\n", project, alias, selected.Namespace, destination, commit, cloneURL)
	return nil
}

func initTransport(p config.Provider) (string, error) {
	if p.Transport == "" {
		return "https", nil
	}
	if p.Transport != "https" && p.Transport != "ssh" {
		return "", errors.New("unsupported transport; use https or ssh")
	}
	return p.Transport, nil
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

func cleanSSHRepository(raw string, p config.Provider) (string, bool) {
	prefix := "git@"
	if !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	authority, path, ok := strings.Cut(strings.TrimPrefix(raw, prefix), ":")
	if !ok || !strings.EqualFold(authority, p.Host) {
		return "", false
	}
	prefix = p.Namespace + "/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, ".git") {
		return "", false
	}
	project := strings.TrimSuffix(strings.TrimPrefix(path, prefix), ".git")
	return project, config.ValidProjectName(project)
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
