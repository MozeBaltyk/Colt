package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
	templating "github.com/MozeBaltyk/Colt/internal/template"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type App struct {
	ConfigPath            string
	WorkDir               string
	Git                   gitnative.Runner
	NewClient             func(config.Provider, string) (provider.Client, error)
	Credentials           credential.Store
	FallbackCredentials   *credential.FileStore
	ReadToken             func() (string, error)
	ConfirmPlaintext      func() (bool, error)
	ReadPassword          func() (string, error)
	IsTerminal            func() bool
	IsOutputTerminal      func(io.Writer) bool
	AuthorizeGitHubDevice func(context.Context, io.Writer) (string, error)
	SaveConfig            func(string, config.Config) error
	Host                  HostOperations
	pathErr               error
}

func New() *App {
	path, err := config.Path()
	httpClient := &http.Client{}
	return &App{
		ConfigPath:          path,
		Git:                 gitnative.Native{},
		Credentials:         credential.NewSecureStore(),
		FallbackCredentials: credential.NewFileStore(credential.DefaultFilePath(path)),
		NewClient: func(p config.Provider, token string) (provider.Client, error) {
			return provider.New(p, token, httpClient)
		},
		AuthorizeGitHubDevice: func(ctx context.Context, out io.Writer) (string, error) {
			return provider.AuthorizeGitHubDevice(ctx, nil, out)
		},
		Host:    nativeHost{},
		pathErr: err,
	}
}

func (a *App) credentialStore() credential.Store {
	if a.Credentials != nil {
		if a.FallbackCredentials != nil {
			return credential.PersistentStore{Secure: a.Credentials, Fallback: a.FallbackCredentials}
		}
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
	root.PersistentFlags().Bool("noninteractive", false, "disable interactive prompts and authorization flows")
	root.PersistentFlags().BoolP("verbose", "v", false, "print additional non-secret diagnostics")
	root.PersistentFlags().String("ca-cert", "", "trust this CA bundle for HTTPS (overrides SSL_CERT_FILE)")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		ca, err := cmd.Flags().GetString("ca-cert")
		if err != nil {
			return err
		}
		if err := applyCABundle(ca); err != nil {
			return fmt.Errorf("invalid --ca-cert: %w", err)
		}
		return nil
	}
	root.AddCommand(a.authCommand(), a.initCommand(), a.templateCommand(), a.listCommand(), a.cloneCommand(), a.releaseCommand(), a.workspaceStatusCommand(), a.syncCommand(), a.mirrorCommand(), a.checkCommand(), a.runCommand(), a.gitCredentialCommand())
	return root
}

// applyCABundle validates a --ca-cert path and stores it in SSL_CERT_FILE so
// both the provider API client (Go's crypto/x509 honors SSL_CERT_FILE on Unix)
// and the native Git layer (which forwards it as http.sslCAInfo) trust the CA.
// An empty path leaves any existing SSL_CERT_FILE untouched.
func applyCABundle(path string) error {
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("CA bundle must be a regular file")
	}
	if strings.ContainsAny(abs, "\r\n") {
		return errors.New("CA bundle path must not contain line breaks")
	}
	return os.Setenv("SSL_CERT_FILE", abs)
}

func verbose(cmd *cobra.Command) bool {
	v, _ := cmd.Root().PersistentFlags().GetBool("verbose")
	return v
}

type requiredInput struct {
	name, supply string
	value        *string
}

const maxInteractiveInput = 64 << 10

func (a *App) interactive(cmd *cobra.Command) bool {
	disabled, _ := cmd.Root().PersistentFlags().GetBool("noninteractive")
	if disabled {
		return false
	}
	if a.IsTerminal != nil {
		return a.IsTerminal()
	}
	f, ok := cmd.InOrStdin().(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func (a *App) requireInputs(cmd *cobra.Command, reader **bufio.Reader, inputs ...requiredInput) error {
	missing := make([]requiredInput, 0, len(inputs))
	for _, input := range inputs {
		if *input.value == "" {
			missing = append(missing, input)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if !a.interactive(cmd) {
		parts := make([]string, len(missing))
		for i, input := range missing {
			parts[i] = input.name + " (supply " + input.supply + ")"
		}
		return errors.New("missing required input: " + strings.Join(parts, ", ") + "; use an interactive terminal without --noninteractive to be prompted")
	}
	if *reader == nil {
		*reader = bufio.NewReader(cmd.InOrStdin())
	}
	for _, input := range missing {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: ", input.name)
		value, err := readInteractiveLine(*reader)
		if err != nil {
			return fmt.Errorf("read %s: %w", input.name, err)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("%s is required; supply %s", input.name, input.supply)
		}
		*input.value = value
	}
	return nil
}

func readInteractiveLine(reader *bufio.Reader) (string, error) {
	line := make([]byte, 0, 256)
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > maxInteractiveInput+2 {
			return "", fmt.Errorf("input exceeds maximum size of %d bytes", maxInteractiveInput)
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			line = line[:len(line)-1]
			if len(line) != 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			return string(line), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return string(line), nil
		default:
			return "", err
		}
	}
}

func (a *App) gitCredentialCommand() *cobra.Command {
	providerAlias := ""
	repository := ""
	namespace := ""
	cmd := &cobra.Command{
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
			p, ok := credentialProvider(cfg, request, providerAlias, repository, namespace)
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
	cmd.Flags().StringVar(&providerAlias, "provider", "", "provider alias")
	cmd.Flags().StringVar(&repository, "repository", "", "repository name")
	cmd.Flags().StringVar(&namespace, "namespace", "", "repository namespace")
	_ = cmd.Flags().MarkHidden("provider")
	_ = cmd.Flags().MarkHidden("repository")
	_ = cmd.Flags().MarkHidden("namespace")
	return cmd
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

func credentialProvider(cfg config.Config, request map[string]string, alias, repository, namespace string) (config.Provider, bool) {
	if request["protocol"] != "https" || request["host"] == "" || request["path"] == "" {
		return config.Provider{}, false
	}
	raw := "https://" + request["host"] + "/" + strings.TrimPrefix(request["path"], "/")
	if alias != "" {
		p, ok := cfg.Providers[alias]
		if !ok || repository == "" {
			return config.Provider{}, false
		}
		if namespace != "" {
			parts := strings.Split(namespace, "/")
			if p.Type != "gitlab" && len(parts) != 1 {
				return config.Provider{}, false
			}
			for _, part := range parts {
				if !gitnative.SafeIdentifier(part) || part == "." || part == ".." {
					return config.Provider{}, false
				}
			}
			p.Namespace = namespace
		}
		actual, ok := cleanHTTPSRepository(raw, p)
		return p, ok && actual == repository
	}
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
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	namespaceParts := strings.Split(p.Namespace, "/")
	if len(parts) != len(namespaceParts)+1 || !namespaceMatches(parts[:len(namespaceParts)], namespaceParts, p.Type) || !strings.HasSuffix(parts[len(parts)-1], ".git") {
		return "", false
	}
	project := strings.TrimSuffix(parts[len(parts)-1], ".git")
	return project, config.ValidProjectName(project)
}

func namespaceMatches(actual, configured []string, providerType string) bool {
	return len(actual) == len(configured) && config.NamespaceEqual(strings.Join(actual, "/"), strings.Join(configured, "/"), providerType)
}

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

func providerTokenEnvNames(cfg config.Config) []string {
	names := make([]string, 0, len(cfg.Providers)*2)
	seen := map[string]bool{}
	for _, p := range cfg.Providers {
		for _, name := range []string{p.Auth.TokenEnv, credential.ConventionalVar(p.Type)} {
			if name != "" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names
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

func matchingOrigin(cfg config.Config, origin string) (alias, transport, project string) {
	alias, transport, project, _ = matchingOriginResult(cfg, origin)
	return
}

func matchingOriginResult(cfg config.Config, origin string) (alias, transport, project string, ambiguous bool) {
	type match struct{ alias, transport, project string }
	var matches []match
	for candidate, p := range cfg.Providers {
		actual, ok := cleanHTTPSRepository(origin, p)
		detected := "https"
		if !ok {
			actual, ok = cleanSSHRepository(origin, p)
			detected = "ssh"
		}
		if !ok {
			continue
		}
		matches = append(matches, match{candidate, detected, actual})
	}
	if len(matches) == 1 {
		m := matches[0]
		return m.alias, m.transport, m.project, false
	}
	if len(matches) > 1 {
		var selected *match
		for i := range matches {
			if cfg.Providers[matches[i].alias].Default {
				if selected != nil {
					return "", "", "", true
				}
				selected = &matches[i]
			}
		}
		if selected != nil {
			return selected.alias, selected.transport, selected.project, false
		}
		return "", "", "", true
	}
	return "", "", "", false
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

func (a *App) listCommand() *cobra.Command {
	all := false
	providerAlias := ""
	namespace := ""
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List repositories from a provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.pathErr != nil {
				return a.pathErr
			}
			if namespace != "" && all {
				return errors.New("--namespace cannot be combined with --all")
			}
			cfg, err := config.Load(a.ConfigPath)
			if err != nil {
				return err
			}
			aliases := make([]string, 0, len(cfg.Providers))
			if providerAlias != "" {
				if _, ok := cfg.Providers[providerAlias]; !ok {
					return fmt.Errorf("unknown provider alias %q; configure it or choose an existing alias", providerAlias)
				}
				aliases = append(aliases, providerAlias)
			} else if all {
				for alias := range cfg.Providers {
					aliases = append(aliases, alias)
				}
			} else {
				alias, _, err := cfg.Resolve("")
				if err != nil {
					return err
				}
				aliases = append(aliases, alias)
			}
			sort.Strings(aliases)
			out := cmd.OutOrStdout()
			anyOK := false
			for _, alias := range aliases {
				p := cfg.Providers[alias]
				if namespace != "" {
					p.Namespace = namespace
					if err := config.ValidateProvider(alias, p); err != nil {
						return fmt.Errorf("invalid --namespace: %w", err)
					}
				}
				token, _, err := config.Token(p, a.credentialStore())
				if err != nil {
					if all {
						fmt.Fprintf(out, "%s: credential error: %v\n", alias, err)
						continue
					}
					return err
				}
				client, err := a.NewClient(p, token)
				if err != nil {
					if all {
						fmt.Fprintf(out, "%s: client error: %v\n", alias, err)
						continue
					}
					return err
				}
				repos, err := client.List(cmd.Context())
				if err != nil {
					if all {
						fmt.Fprintf(out, "%s: %v\n", alias, err)
						continue
					}
					return err
				}
				if namespace != "" {
					filtered := repos[:0]
					for _, repo := range repos {
						if namespaceMatches(strings.Split(repo.Namespace, "/"), strings.Split(namespace, "/"), p.Type) {
							filtered = append(filtered, repo)
						}
					}
					repos = filtered
				}
				sort.Slice(repos, func(i, j int) bool {
					return repos[i].CloneURL < repos[j].CloneURL
				})
				for _, repo := range repos {
					fmt.Fprintln(out, repo.CloneURL)
				}
				anyOK = true
			}
			if !anyOK && all {
				return errors.New("no providers returned results")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "list repositories from every configured provider")
	cmd.Flags().StringVar(&providerAlias, "provider", "", "provider alias")
	cmd.Flags().StringVar(&namespace, "namespace", "", "namespace to list (defaults to the provider's configured namespace)")
	return cmd
}

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

func InitTransport(p config.Provider, explicitTransport string, configPath string) (string, error) {
	// Precedence: explicit command option > provider transport preference >
	// global transport preference > product default (HTTPS).
	if explicitTransport != "" {
		if explicitTransport != "https" && explicitTransport != "ssh" {
			return "", errors.New("unsupported transport; use https or ssh")
		}
		return explicitTransport, nil
	}
	if p.Transport != "" {
		if p.Transport != "https" && p.Transport != "ssh" {
			return "", errors.New("unsupported transport; use https or ssh")
		}
		return p.Transport, nil
	}
	cfg, err := config.Load(configPath)
	if err == nil && cfg.Transport != "" {
		if cfg.Transport != "https" && cfg.Transport != "ssh" {
			return "", errors.New("unsupported transport; use https or ssh")
		}
		return cfg.Transport, nil
	}
	return "https", nil
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
	host, path := "", ""
	if u, err := url.Parse(raw); err == nil && u.Scheme == "ssh" {
		if u.User.Username() != "git" {
			return "", false
		}
		host = u.Hostname()
		path = strings.TrimPrefix(u.Path, "/")
	} else {
		if !strings.HasPrefix(raw, "git@") {
			return "", false
		}
		authority, rest, ok := strings.Cut(strings.TrimPrefix(raw, "git@"), ":")
		if !ok {
			return "", false
		}
		host, path = authority, rest
	}
	if !strings.EqualFold(host, p.Host) {
		return "", false
	}
	parts := strings.Split(path, "/")
	namespaceParts := strings.Split(p.Namespace, "/")
	if len(parts) != len(namespaceParts)+1 || !namespaceMatches(parts[:len(namespaceParts)], namespaceParts, p.Type) || !strings.HasSuffix(parts[len(parts)-1], ".git") {
		return "", false
	}
	project := strings.TrimSuffix(parts[len(parts)-1], ".git")
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
	return &partialError{step: step, local: local, remote: remote, recovery: recovery, cause: cause}
}
