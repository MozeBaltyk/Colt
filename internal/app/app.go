package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/MozeBaltyk/Colt/internal/update"
	"github.com/MozeBaltyk/Colt/internal/version"
	"github.com/spf13/cobra"
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
	Updater               *update.Client
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
		Use:           "colt",
		Short:         "Initialize provider-independent Git projects",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
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
	root.AddCommand(a.authCommand(), a.initCommand(), a.templateCommand(), a.listCommand(), a.cloneCommand(), a.releaseCommand(), a.workspaceStatusCommand(), a.syncCommand(), a.mirrorCommand(), a.checkCommand(), a.analyzeCommand(), a.updateCommand(), a.runCommand(), a.gitCredentialCommand())
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
