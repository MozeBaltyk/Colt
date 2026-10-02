package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type authOptions struct {
	host, baseURL, namespace, visibility, gitName, gitEmail, tokenEnv, transport, credential string
	replace, makeDefault                                                                     bool
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
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerType, alias := "", ""
			if len(args) > 0 {
				providerType = args[0]
			}
			if len(args) > 1 {
				alias = args[1]
			}
			if len(args) > 0 && providerType == "" {
				return errors.New("invalid provider type: value must not be empty")
			}
			if len(args) > 1 && alias == "" {
				return errors.New("invalid provider alias: value must not be empty")
			}
			positional := make([]requiredInput, 0, 2)
			if len(args) == 0 {
				positional = append(positional, requiredInput{"provider", "the first positional argument", &providerType})
			}
			if len(args) < 2 {
				positional = append(positional, requiredInput{"alias", "the second positional argument", &alias})
			}
			if err := validateExplicitLogin(cmd, providerType, alias, opts); err != nil {
				return err
			}
			var reader *bufio.Reader
			if !a.interactive(cmd) {
				if err := a.requireInputs(cmd, &reader, append(positional, loginRequiredInputs(cmd, providerType, &opts)...)...); err != nil {
					return err
				}
			} else {
				if err := a.requireInputs(cmd, &reader, positional...); err != nil {
					return err
				}
				if err := validateExplicitLogin(cmd, providerType, alias, opts); err != nil {
					return err
				}
				if err := a.requireInputs(cmd, &reader, loginRequiredInputs(cmd, providerType, &opts)...); err != nil {
					return err
				}
			}
			return a.loginProvider(cmd, providerType, alias, opts)
		},
	}
	repository := ""
	status := &cobra.Command{
		Use:   "status [alias]",
		Short: "Show configured providers",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if repository != "" && len(args) == 0 {
				return errors.New("--repository requires a provider alias; use: colt auth status <alias> --repository <project>")
			}
			if repository != "" {
				offline, _ := cmd.Flags().GetBool("offline")
				if offline {
					return errors.New("--offline and --repository cannot be used together; omit one")
				}
				if !config.ValidProjectName(repository) {
					return errors.New("invalid --repository; use 1-255 letters, digits, '.', '_' or '-' and no path separators")
				}
			}
			return a.providerStatus(cmd, args, repository)
		},
	}
	status.Flags().Bool("offline", false, "inspect configuration only, do not call provider APIs")
	status.Flags().StringVar(&repository, "repository", "", "repository to validate using the selected provider's configured Git transport")
	revoke := false
	logout := &cobra.Command{
		Use:   "logout <alias>",
		Short: "Remove a locally stored provider credential",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := ""
			if len(args) == 1 {
				alias = args[0]
			} else {
				var reader *bufio.Reader
				if err := a.requireInputs(cmd, &reader, requiredInput{"alias", "the positional argument", &alias}); err != nil {
					return err
				}
			}
			return a.logoutProvider(cmd, alias, revoke)
		},
	}
	logout.Flags().BoolVar(&revoke, "revoke", false, "also attempt provider-side revocation of the credential")
	f := login.Flags()
	f.StringVar(&opts.host, "host", "", "provider host (required for Gitea/Forgejo; defaults for GitHub and GitLab)")
	f.StringVar(&opts.baseURL, "base-url", "", "HTTPS API base URL (defaults from provider host)")
	f.StringVar(&opts.namespace, "namespace", "", "repository owner: GitHub/Gitea/Forgejo user or organization; GitLab group or subgroup/full path (required)")
	f.StringVar(&opts.visibility, "visibility", "private", "default repository visibility")
	f.StringVar(&opts.gitName, "git-name", "", "repository-local Git author name (required)")
	f.StringVar(&opts.gitEmail, "git-email", "", "repository-local Git author email (required)")
	f.StringVar(&opts.tokenEnv, "token-env", "", "token environment variable (provider default when omitted)")
	f.StringVar(&opts.transport, "transport", "", "Git transport: https or ssh (default https)")
	f.StringVar(&opts.credential, "credential", "", "credential source: env or stored")
	f.BoolVar(&opts.makeDefault, "default", false, "select this provider by default")
	f.BoolVar(&opts.replace, "replace", false, "replace an existing alias after validation")
	auth.AddCommand(login, logout, status)
	return auth
}

func validateExplicitLogin(cmd *cobra.Command, providerType, alias string, opts authOptions) error {
	if providerType != "" && providerType != "github" && providerType != "gitlab" && providerType != "gitea" && providerType != "forgejo" {
		return fmt.Errorf("invalid provider type %q; must be github, gitlab, gitea or forgejo", providerType)
	}
	for _, input := range []struct{ flag, value string }{
		{"host", opts.host}, {"base-url", opts.baseURL}, {"namespace", opts.namespace},
		{"visibility", opts.visibility}, {"git-name", opts.gitName}, {"git-email", opts.gitEmail},
		{"token-env", opts.tokenEnv}, {"transport", opts.transport}, {"credential", opts.credential},
	} {
		if cmd.Flags().Changed(input.flag) && input.value == "" {
			return fmt.Errorf("invalid --%s: value must not be empty", input.flag)
		}
	}
	if opts.credential != "" && opts.credential != "env" && opts.credential != "stored" {
		return fmt.Errorf("invalid --credential %q; must be env or stored", opts.credential)
	}
	if opts.transport != "" && opts.transport != "https" && opts.transport != "ssh" {
		return fmt.Errorf("invalid --transport %q; must be https or ssh", opts.transport)
	}
	if opts.visibility != "private" && opts.visibility != "public" && opts.visibility != "internal" {
		return fmt.Errorf("invalid --visibility %q; must be private, public, or internal for GitLab", opts.visibility)
	}
	if providerType == "" {
		return nil
	}
	validated := opts
	if validated.namespace == "" {
		validated.namespace = "placeholder"
	}
	if validated.gitName == "" {
		validated.gitName = "Placeholder"
	}
	if validated.gitEmail == "" {
		validated.gitEmail = "placeholder@example.com"
	}
	if (providerType == "gitea" || providerType == "forgejo") && validated.host == "" {
		validated.host = "placeholder.example.com"
	}
	if alias == "" {
		alias = "placeholder"
	}
	if err := config.ValidateProvider(alias, providerForLogin(providerType, validated)); err != nil {
		return fmt.Errorf("invalid login input: %w", err)
	}
	return nil
}

func loginRequiredInputs(cmd *cobra.Command, providerType string, opts *authOptions) []requiredInput {
	required := make([]requiredInput, 0, 4)
	if (providerType == "gitea" || providerType == "forgejo") && !cmd.Flags().Changed("host") {
		required = append(required, requiredInput{"host", "--host", &opts.host})
	}
	if !cmd.Flags().Changed("namespace") {
		required = append(required, requiredInput{"namespace", "--namespace", &opts.namespace})
	}
	if !cmd.Flags().Changed("git-name") {
		required = append(required, requiredInput{"git-name", "--git-name", &opts.gitName})
	}
	if !cmd.Flags().Changed("git-email") {
		required = append(required, requiredInput{"git-email", "--git-email", &opts.gitEmail})
	}
	return required
}

func (a *App) logoutProvider(cmd *cobra.Command, alias string, revoke bool) error {
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

	var remoteErr error
	if revoke {
		remoteErr = a.revokeProviderCredential(cmd, p)
	}

	if p.Auth.Source == "env" {
		fmt.Fprintf(cmd.OutOrStdout(), "no stored credential removed for %s; ! %s may still provide credentials (environment unchanged)\n", alias, envName)
		return remoteErr
	}

	err = a.credentialStore().Delete(p.Auth.CredentialID)
	var removeErr error
	switch {
	case errors.Is(err, credential.ErrNotFound):
		fmt.Fprintf(cmd.OutOrStdout(), "no stored credential found for %s; nothing changed\n", p.Auth.CredentialID)
	case errors.Is(err, credential.ErrStoreUnavailable):
		removeErr = errors.New("credential storage unavailable; local credential was not removed")
	case errors.Is(err, credential.ErrPartialDelete):
		removeErr = errors.New("credential removal partially failed; the credential may remain in one local store; provider configuration is unchanged")
	case err != nil:
		removeErr = errors.New("credential storage failure; local credential was not removed safely")
	default:
		fmt.Fprintf(cmd.OutOrStdout(), "removed stored credential %s; provider configuration unchanged\n", p.Auth.CredentialID)
	}
	if removeErr != nil {
		fmt.Fprintln(cmd.OutOrStdout(), "! local credential removal failed")
		return errors.Join(remoteErr, removeErr)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "! %s may still provide credentials (environment unchanged)\n", envName)
	return remoteErr
}

var errRemoteRevocationFailed = errors.New("remote credential revocation failed")

func (a *App) revokeProviderCredential(cmd *cobra.Command, p config.Provider) error {
	token, _, err := config.Token(p, a.credentialStore())
	if err == nil {
		var client provider.Client
		client, err = a.NewClient(p, token)
		if err == nil {
			err = client.Revoke(cmd.Context(), provider.RevocationOptions{})
		}
	}
	switch {
	case err == nil:
		fmt.Fprintln(cmd.OutOrStdout(), "✓ remote credential revoked")
		return nil
	case errors.Is(err, provider.ErrRevocationUnsupported):
		fmt.Fprintln(cmd.OutOrStdout(), "provider-side revocation unsupported")
		return nil
	default:
		fmt.Fprintln(cmd.OutOrStdout(), "! remote revocation failed")
		return errRemoteRevocationFailed
	}
}

func (a *App) providerStatus(cmd *cobra.Command, args []string, repository string) error {
	if a.pathErr != nil {
		return a.pathErr
	}
	offline, _ := cmd.Flags().GetBool("offline")
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		return err
	}
	aliases := make([]string, 0, len(cfg.Providers))
	if len(args) == 1 {
		if _, ok := cfg.Providers[args[0]]; !ok {
			return fmt.Errorf("unknown provider alias %q; configure it or choose an existing alias", args[0])
		}
		aliases = append(aliases, args[0])
	} else {
		for alias := range cfg.Providers {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	if len(aliases) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no providers configured")
		return nil
	}
	transportAlias, transportName, transportState := "", "", "not checked"
	if !offline && repository == "" && a.Git != nil {
		probeConfig := cfg
		if len(args) == 1 {
			probeConfig.Providers = map[string]config.Provider{args[0]: cfg.Providers[args[0]]}
		}
		dir := a.WorkDir
		if dir == "" {
			dir, _ = os.Getwd()
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
		origin, originErr := a.Git.Origin(ctx, dir)
		cancel()
		if originErr == nil && origin != "" {
			var project string
			transportAlias, transportName, project = matchingOrigin(probeConfig, origin)
			if transportAlias != "" {
				ctx, cancel = context.WithTimeout(cmd.Context(), 15*time.Second)
				err := a.Git.LsRemote(ctx, dir, origin, transportAlias, project, providerTokenEnvNames(cfg))
				cancel()
				if err == nil {
					transportState = "✓ Git authentication/connectivity and read access confirmed (clone/fetch/pull); push/write not checked"
				} else {
					transportState = "origin unreachable; check repository access and connectivity"
				}
			}
		}
	}
	for _, alias := range aliases {
		p := cfg.Providers[alias]
		defaultMarker := ""
		if p.Default {
			defaultMarker = " (default)"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s%s\n", alias, defaultMarker)
		fmt.Fprintf(cmd.OutOrStdout(), "  %s · %s\n", config.ProviderTypeName(p.Type), p.Host)
		if offline {
			fmt.Fprintf(cmd.OutOrStdout(), "  Namespace:   %s\n", p.Namespace)
			fmt.Fprintf(cmd.OutOrStdout(), "  Auth source: %s\n", p.Auth.Source)
			if p.Auth.CredentialID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Credential ID: %s\n", p.Auth.CredentialID)
			}
			if p.GitName == "" || p.GitEmail == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "  Git identity: not configured")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  Git name:    %s\n", p.GitName)
				fmt.Fprintf(cmd.OutOrStdout(), "  Git email:   %s\n", p.GitEmail)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "  Connection:  not checked")
			fmt.Fprintln(cmd.OutOrStdout(), "  Transport:   not checked")
		} else {
			explicitTransport, explicitState := "", ""
			token, _, tokenErr := config.Token(p, a.credentialStore())
			if tokenErr != nil {
				if errors.Is(tokenErr, credential.ErrMissing) {
					fmt.Fprintln(cmd.OutOrStdout(), "  Connection:  ✗ credentials missing")
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "  Connection:  ✗ credential storage failure")
				}
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  Credential:  %s\n", resolvedCredentialSource(p))
				if verbose(cmd) && p.Auth.CredentialID != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  Credential ID: %s\n", p.Auth.CredentialID)
				}
				client, clientErr := a.NewClient(p, token)
				if clientErr != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "  Connection:  ✗ unreachable\n    %s\n", safeExplanation(clientErr.Error()))
				} else {
					account, authErr := client.Authenticate(cmd.Context())
					if authErr != nil {
						state := authState(authErr)
						fmt.Fprintf(cmd.OutOrStdout(), "  Connection:  ✗ %s\n", state)
						if state == "authentication failed" && resolvedCredentialSource(p) == "stored" {
							fmt.Fprintf(cmd.OutOrStdout(), "    Run: colt auth login %s %s\n", p.Type, alias)
						} else if exp := safeExplanation(authErr.Error()); exp != "" {
							fmt.Fprintf(cmd.OutOrStdout(), "    %s\n", exp)
						}
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "  Account:     %s\n", account)
						fmt.Fprintf(cmd.OutOrStdout(), "  Namespace:   %s\n", p.Namespace)
						if p.GitName == "" || p.GitEmail == "" {
							fmt.Fprintln(cmd.OutOrStdout(), "  Git identity: not configured")
						} else {
							fmt.Fprintf(cmd.OutOrStdout(), "  Git name:    %s\n", p.GitName)
							fmt.Fprintf(cmd.OutOrStdout(), "  Git email:   %s\n", p.GitEmail)
						}
						fmt.Fprintln(cmd.OutOrStdout(), "  Connection:  ✓ connected")
						if repository != "" {
							explicitTransport, explicitState = a.repositoryTransportStatus(cmd, client, p, alias, repository, providerTokenEnvNames(cfg))
						}
					}
				}
			}
			if explicitState != "" {
				if explicitTransport == "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  Transport:   not checked · %s\n", explicitState)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "  Transport:   %s · %s\n", strings.ToUpper(explicitTransport), explicitState)
				}
			} else if alias == transportAlias {
				fmt.Fprintf(cmd.OutOrStdout(), "  Transport:   %s · %s\n", strings.ToUpper(transportName), transportState)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "  Transport:   not checked")
			}
		}
	}
	return nil
}

func (a *App) repositoryTransportStatus(cmd *cobra.Command, client provider.Client, p config.Provider, alias, project string, tokenEnvNames []string) (string, string) {
	transport, err := InitTransport(p, "", "")
	if err != nil {
		return "", "configured transport is invalid"
	}
	repo, err := client.Get(cmd.Context(), project)
	if err != nil || repo == nil {
		return "", "repository metadata unavailable; no clone URL was guessed"
	}
	target := repo.CloneURL
	valid := cleanHTTPSRepository
	if transport == "ssh" {
		target, valid = repo.SSHURL, cleanSSHRepository
	}
	if actual, ok := valid(target, p); !ok || actual != project {
		return "", "provider returned an unexpected clone target"
	}
	if a.Git == nil {
		return "", "native Git is unavailable"
	}
	dir := a.WorkDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	err = a.Git.LsRemote(ctx, dir, target, alias, project, tokenEnvNames)
	cancel()
	if err != nil {
		return transport, "origin unreachable; check repository access and connectivity"
	}
	return transport, "✓ Git authentication/connectivity and read access confirmed (clone/fetch/pull); push/write not checked"
}

func resolvedCredentialSource(p config.Provider) string {
	name := p.Auth.TokenEnv
	if name == "" {
		name = credential.ConventionalVar(p.Type)
	}
	if name != "" && os.Getenv(name) != "" {
		return "environment"
	}
	return "stored"
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

func providerForLogin(providerType string, opts authOptions) config.Provider {
	host, baseURL := opts.host, opts.baseURL
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
	} else if (providerType == "gitea" || providerType == "forgejo") && host != "" && baseURL == "" {
		baseURL = "https://" + host
	}
	return config.Provider{
		Type: providerType, Host: host, BaseURL: strings.TrimRight(baseURL, "/"), Namespace: opts.namespace,
		Visibility: opts.visibility, GitName: opts.gitName, GitEmail: opts.gitEmail,
		Transport: opts.transport, Auth: config.Auth{Source: "env", TokenEnv: opts.tokenEnv}, Default: opts.makeDefault,
	}
}

func (a *App) loginProvider(cmd *cobra.Command, providerType, alias string, opts authOptions) error {
	if a.pathErr != nil {
		return a.pathErr
	}
	p := providerForLogin(providerType, opts)
	host := p.Host
	if opts.credential != "" && opts.credential != "env" && opts.credential != "stored" {
		return fmt.Errorf("invalid --credential %q; must be env or stored", opts.credential)
	}
	if err := config.ValidateProvider(alias, p); err != nil {
		return fmt.Errorf("invalid provider %q: %w", alias, err)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		return err
	}
	existing, exists := cfg.Providers[alias]
	if exists && !opts.replace {
		return fmt.Errorf("provider alias %q already exists; use --replace to replace it", alias)
	}
	if exists && opts.replace && opts.credential != "env" && existing.Auth.Source == "stored" {
		if opts.tokenEnv != "" || existing.Host != host {
			return fmt.Errorf("provider alias %q has stored credential %s; replacement would orphan it", alias, existing.Auth.CredentialID)
		}
		p.Auth = existing.Auth
	}
	if opts.credential == "stored" {
		p.Auth = config.Auth{Source: "stored", CredentialID: host + "/" + alias}
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
	var token string
	manual := false
	if p.Auth.Source == "stored" {
		credentialID := p.Auth.CredentialID
		if cred, getErr := a.credentialStore().Get(credentialID); getErr == nil {
			if credential.ValidateBearerToken(cred.Secret) != nil {
				return errors.New("stored credential is invalid; provider configuration was not changed")
			}
			token = cred.Secret
		} else if !errors.Is(getErr, credential.ErrNotFound) && !errors.Is(getErr, credential.ErrStoreUnavailable) {
			return errors.New("credential storage failure; provider configuration was not changed")
		} else {
			tokenEnv := opts.tokenEnv
			if tokenEnv == "" {
				tokenEnv = credential.ConventionalVar(providerType)
			}
			if tokenEnv != "" {
				token = os.Getenv(tokenEnv)
			}
			if token != "" && credential.ValidateBearerToken(token) != nil {
				return fmt.Errorf("token from %s is invalid", tokenEnv)
			} else if token == "" && providerType == "github" {
				if !a.interactive(cmd) {
					return errors.New("no stored credential or GitHub environment token resolved; use an interactive terminal without --noninteractive for GitHub Device Flow, or set --token-env")
				}
				authorize := a.AuthorizeGitHubDevice
				if authorize == nil {
					authorize = func(ctx context.Context, out io.Writer) (string, error) {
						return provider.AuthorizeGitHubDevice(ctx, nil, out)
					}
				}
				manual = true
				var tokenErr error
				token, tokenErr = authorize(cmd.Context(), cmd.ErrOrStderr())
				if tokenErr != nil {
					return tokenErr
				}
			} else if token == "" && opts.tokenEnv != "" {
				return fmt.Errorf("token environment variable %q is not set", opts.tokenEnv)
			} else if token == "" {
				manual = true
				var tokenErr error
				token, tokenErr = a.readToken(cmd)
				if tokenErr != nil {
					return tokenErr
				}
			} else {
				manual = true
			}
		}
		p.Auth = config.Auth{Source: "stored", CredentialID: credentialID}
		candidate.Providers[alias] = p
		if err := candidate.Validate(); err != nil {
			return err
		}
	} else {
		token, _, err = config.Token(p, a.credentialStore())
		if errors.Is(err, credential.ErrMissing) {
			if opts.credential == "env" {
				return err
			}
			if opts.tokenEnv != "" {
				return err
			}
			if exists && opts.replace {
				return fmt.Errorf("manual stored credential enrollment is not allowed with --replace for provider alias %q", alias)
			}
			manual = true
			token, err = a.readToken(cmd)
			if err != nil {
				return err
			}
			p.Auth = config.Auth{Source: "stored", CredentialID: host + "/" + alias}
			candidate.Providers[alias] = p
			if err := candidate.Validate(); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	client, err := a.NewClient(p, token)
	if err != nil {
		return err
	}
	account, err := client.Authenticate(cmd.Context())
	if err != nil {
		return err
	}
	var rollback func() error
	plaintext := false
	if manual {
		if _, targetErr := a.credentialStore().Get(p.Auth.CredentialID); targetErr == nil {
			return fmt.Errorf("credential %s already exists; refusing to overwrite it", p.Auth.CredentialID)
		} else if !errors.Is(targetErr, credential.ErrNotFound) && !errors.Is(targetErr, credential.ErrStoreUnavailable) {
			return errors.New("credential storage failure; provider configuration was not changed")
		}
		created := credential.Credential{Kind: "bearer_token", Secret: token}
		rollback = func() error {
			_, rErr := a.Credentials.DeleteIf(p.Auth.CredentialID, created)
			return rErr
		}
		err = a.Credentials.Create(p.Auth.CredentialID, created)
		if errors.Is(err, credential.ErrStoreUnavailable) {
			accepted, confirmErr := a.confirmPlaintext(cmd)
			if confirmErr != nil {
				return confirmErr
			}
			if !accepted {
				return errors.New("authentication succeeded but the credential was not persisted; set the provider token environment variable or retry and consent to plaintext storage")
			}
			if a.FallbackCredentials == nil {
				return errors.New("plaintext credential storage is unavailable")
			}
			created := credential.Credential{Kind: "bearer_token", Secret: token}
			rollback = func() error {
				_, rErr := a.FallbackCredentials.DeleteIf(p.Auth.CredentialID, created)
				return rErr
			}
			err = a.FallbackCredentials.Create(p.Auth.CredentialID, created)
			plaintext = err == nil
		}
		if err != nil {
			if errors.Is(err, credential.ErrAlreadyExists) {
				return fmt.Errorf("credential %s already exists; refusing to overwrite it", p.Auth.CredentialID)
			}
			if errors.Is(err, credential.ErrPersistenceUncertain) {
				return fmt.Errorf("credential persistence outcome is uncertain for %s; provider configuration was not changed", p.Auth.CredentialID)
			}
			return errors.New("credential storage failure; provider configuration was not changed")
		}
	}
	save := a.SaveConfig
	if save == nil {
		save = config.Save
	}
	if err := save(a.ConfigPath, candidate); err != nil {
		if rollback != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				return errors.New("authentication succeeded but config was not changed and credential rollback failed; credential " + p.Auth.CredentialID + " may require manual removal")
			}
		}
		return fmt.Errorf("authentication succeeded but config was not changed: %w", err)
	}
	if plaintext {
		fmt.Fprintln(cmd.ErrOrStderr(), "! credential stored as plaintext protected only by filesystem permissions")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "configured provider %s (authenticated as %s)\n", alias, account)
	return nil
}

func (a *App) readToken(cmd *cobra.Command) (string, error) {
	if !a.interactive(cmd) {
		return "", errors.New("no environment token resolved; manual token entry requires an interactive terminal without --noninteractive")
	}
	if a.ReadToken != nil {
		value, err := a.ReadToken()
		if err != nil {
			return "", err
		}
		if credential.ValidateBearerToken(value) != nil {
			return "", errors.New("token must be non-empty and contain no CR or LF")
		}
		return value, nil
	}
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return "", errors.New("no environment token resolved; manual token entry requires an interactive terminal")
	}
	fmt.Fprint(cmd.ErrOrStderr(), "Token: ")
	value, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", errors.New("read token from terminal")
	}
	if credential.ValidateBearerToken(string(value)) != nil {
		return "", errors.New("token must be non-empty and contain no CR or LF")
	}
	return string(value), nil
}

func (a *App) confirmPlaintext(cmd *cobra.Command) (bool, error) {
	if !a.interactive(cmd) {
		return false, errors.New("secure credential storage is unavailable; plaintext fallback requires explicit consent in an interactive terminal without --noninteractive")
	}
	if a.ConfirmPlaintext != nil {
		return a.ConfirmPlaintext()
	}
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return false, errors.New("secure credential storage is unavailable; plaintext fallback requires explicit consent in an interactive terminal")
	}
	fmt.Fprint(cmd.ErrOrStderr(), "Secure credential storage is unavailable. Store as plaintext protected only by filesystem permissions? [y/N] ")
	answer, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, errors.New("read plaintext storage choice")
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}
