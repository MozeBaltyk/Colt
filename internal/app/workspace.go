package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/credential"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/spf13/cobra"
)

type workspaceItem struct {
	alias, namespace, project, path string
	provider                        config.Provider
	repository                      provider.Repository
	category, detail                string
}

func (a *App) workspaceStatusCommand() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Report workspace reconciliation status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		items, err := a.workspacePlan(cmd.Context())
		if err != nil {
			return err
		}
		return writeWorkspacePlan(cmd.OutOrStdout(), items)
	}}
}

func (a *App) syncCommand() *cobra.Command {
	dryRun := false
	cmd := &cobra.Command{Use: "sync", Short: "Clone missing repositories declared by the workspace", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		items, err := a.workspacePlan(cmd.Context())
		if err != nil {
			return err
		}
		planErr := writeWorkspacePlan(cmd.OutOrStdout(), items)
		if dryRun {
			return planErr
		}
		needsClone := false
		for _, item := range items {
			needsClone = needsClone || item.category == "to-clone"
		}
		if !needsClone {
			return planErr
		}
		if a.Git == nil {
			return errors.New("native Git is unavailable")
		}
		if err := a.Git.Available(); err != nil {
			return err
		}
		rootPath, err := a.workDir()
		if err != nil {
			return err
		}
		root, err := openWorkRoot(rootPath)
		if err != nil {
			return err
		}
		defer root.Close()
		var failures []string
		for i := range items {
			item := &items[i]
			if item.category != "to-clone" {
				continue
			}
			if err := a.syncClone(cmd.Context(), root, item); err != nil {
				failures = append(failures, item.path+": "+err.Error())
				fmt.Fprintf(cmd.OutOrStdout(), "failed %s: %v\n", item.path, err)
				continue
			}
			fmt.Fprintf(cmd.OutOrStdout(), "cloned %s\n", item.path)
		}
		if len(failures) != 0 {
			if planErr != nil {
				return fmt.Errorf("workspace sync incomplete: %d clone failures; %w", len(failures), planErr)
			}
			return fmt.Errorf("workspace sync incomplete: %d clone failures", len(failures))
		}
		if planErr != nil {
			return planErr
		}
		return nil
	}}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report the reconciliation plan without mutation")
	return cmd
}

func (a *App) workDir() (string, error) {
	if a.WorkDir != "" {
		return a.WorkDir, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine working directory: %w", err)
	}
	return dir, nil
}

func (a *App) workspacePlan(ctx context.Context) ([]workspaceItem, error) {
	if a.pathErr != nil {
		return nil, a.pathErr
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		return nil, err
	}
	if cfg.Workspace == nil {
		return nil, errors.New("workspace is not configured")
	}
	var items []workspaceItem
	identities, paths := map[string]bool{}, map[string]bool{}
	for _, selection := range cfg.Workspace.Repositories {
		if selection.Include != nil && len(*selection.Include) == 0 {
			continue
		}
		p := cfg.Providers[selection.Provider]
		token, _, err := config.Token(p, a.credentialStore())
		if err != nil {
			return nil, fmt.Errorf("resolve provider %q credential: %w", selection.Provider, err)
		}
		client, err := a.NewClient(p, token)
		if err != nil {
			return nil, fmt.Errorf("configure provider %q: %w", selection.Provider, err)
		}
		repositories, err := client.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("list provider %q repositories: %w", selection.Provider, err)
		}
		if len(repositories) > config.MaxProviderRepositories {
			return nil, fmt.Errorf("provider %q returned more than %d repositories", selection.Provider, config.MaxProviderRepositories)
		}
		remote := map[string]provider.Repository{}
		for _, repository := range repositories {
			name, namespace, err := validatedRepository(repository, p)
			if err != nil {
				return nil, fmt.Errorf("provider %q returned unsafe repository metadata: %w", selection.Provider, err)
			}
			if !namespaceMatches(strings.Split(namespace, "/"), strings.Split(selection.Namespace, "/"), p.Type) {
				continue
			}
			key := repositoryKey(name, p.Type)
			if _, duplicate := remote[key]; duplicate {
				return nil, fmt.Errorf("provider %q returned duplicate repository %q", selection.Provider, name)
			}
			repository.Name, repository.Namespace = name, namespace
			remote[key] = repository
		}
		var names []string
		if selection.Include == nil {
			for _, repository := range remote {
				names = append(names, repository.Name)
			}
		} else {
			names = append(names, (*selection.Include)...)
		}
		sort.Strings(names)
		for _, name := range names {
			path, err := workspacePath(selection.Namespace, name)
			if err != nil {
				return nil, err
			}
			identity := selection.Provider + "\x00" + selection.Namespace + "\x00" + repositoryKey(name, p.Type)
			pathKey := strings.ToLower(filepath.ToSlash(path))
			if identities[identity] || paths[pathKey] {
				return nil, fmt.Errorf("workspace derives duplicate repository identity or path %q", path)
			}
			identities[identity], paths[pathKey] = true, true
			item := workspaceItem{alias: selection.Provider, namespace: selection.Namespace, project: name, path: path, provider: p, category: "absent-remotely"}
			if repository, ok := remote[repositoryKey(name, p.Type)]; ok {
				item.repository = repository
				item.category = "remote"
			}
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].path < items[j].path })
	rootPath, err := a.workDir()
	if err != nil {
		return nil, err
	}
	root, err := openWorkRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for i := range items {
		if items[i].category == "remote" {
			items[i].category, items[i].detail = classifyWorkspacePath(root, items[i].path, items[i].repository)
		}
	}
	return items, nil
}

func repositoryKey(name, providerType string) string {
	if providerType == "gitlab" {
		return name
	}
	return strings.ToLower(name)
}

func workspacePath(namespace, project string) (string, error) {
	path := filepath.Join(filepath.FromSlash(namespace), project)
	if path == "." || !filepath.IsLocal(path) || filepath.Clean(path) != path || filepath.IsAbs(path) {
		return "", fmt.Errorf("workspace path %q is not a clean confined relative path", path)
	}
	return path, nil
}

func validatedRepository(repository provider.Repository, p config.Provider) (string, string, error) {
	name, namespace, err := repositoryIdentity(repository, p)
	if err != nil {
		return "", "", err
	}
	if !namespaceMatches(strings.Split(namespace, "/"), strings.Split(p.Namespace, "/"), p.Type) {
		return "", "", errors.New("repository namespace does not match configured namespace")
	}
	return name, namespace, nil
}

func repositoryIdentity(repository provider.Repository, p config.Provider) (string, string, error) {
	u, err := url.Parse(repository.CloneURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || !strings.EqualFold(u.Host, p.Host) {
		return "", "", errors.New("unexpected HTTPS clone target")
	}
	path := strings.TrimPrefix(u.Path, "/")
	if !strings.HasSuffix(path, ".git") {
		return "", "", errors.New("repository URL must end in .git")
	}
	parts := strings.Split(strings.TrimSuffix(path, ".git"), "/")
	if len(parts) < 2 || p.Type != "gitlab" && len(parts) != 2 {
		return "", "", errors.New("repository URL has an incompatible namespace")
	}
	name, namespace := parts[len(parts)-1], strings.Join(parts[:len(parts)-1], "/")
	if !config.ValidProjectName(name) {
		return "", "", errors.New("repository URL has an invalid name")
	}
	for _, part := range parts[:len(parts)-1] {
		if !config.ValidProjectName(part) {
			return "", "", errors.New("repository URL has an invalid namespace")
		}
	}
	if repository.Name != "" && repository.Name != name {
		return "", "", errors.New("repository name does not match clone URL")
	}
	if repository.Namespace != "" && !namespaceMatches(strings.Split(repository.Namespace, "/"), strings.Split(namespace, "/"), p.Type) {
		return "", "", errors.New("repository namespace does not match clone URL")
	}
	if repository.SSHURL != "" {
		urlProvider := p
		urlProvider.Namespace = namespace
		if sshName, valid := cleanSSHRepository(repository.SSHURL, urlProvider); !valid || sshName != name {
			return "", "", fmt.Errorf("unexpected SSH clone target %q", repository.SSHURL)
		}
	}
	return name, namespace, nil
}

func classifyWorkspacePath(root *os.Root, path string, repository provider.Repository) (string, string) {
	info, err := root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "to-clone", ""
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "mismatch", "destination is not an ordinary repository directory"
	}
	repositoryRoot, err := root.OpenRoot(path)
	if err != nil {
		return "mismatch", "destination cannot be opened without following links"
	}
	defer repositoryRoot.Close()
	gitInfo, err := repositoryRoot.Lstat(".git")
	if err != nil || gitInfo.Mode()&os.ModeSymlink != 0 || !gitInfo.IsDir() {
		return "mismatch", "Git metadata is missing or not an ordinary directory"
	}
	origin, err := readOrigin(repositoryRoot)
	if err != nil || origin == "" {
		return "mismatch", "origin is missing or unreadable"
	}
	valid := origin == repository.CloneURL
	if repository.SSHURL != "" {
		valid = valid || origin == repository.SSHURL
	}
	if !valid {
		return "mismatch", "origin does not match provider metadata"
	}
	return "present", ""
}

func readOrigin(root *os.Root) (string, error) {
	expected, err := root.Lstat(".git/config")
	if err != nil || expected.Mode()&os.ModeSymlink != 0 || !expected.Mode().IsRegular() {
		return "", errors.New("Git config is not an ordinary file")
	}
	f, err := root.Open(".git/config")
	if err != nil {
		return "", err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(expected, actual) {
		return "", errors.New("Git config changed while opening")
	}
	scanner := bufio.NewScanner(io.LimitReader(f, 1<<20))
	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		if section != `remote "origin"` {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "url") {
			return strings.TrimSpace(value), nil
		}
	}
	return "", scanner.Err()
}

func writeWorkspacePlan(out io.Writer, items []workspaceItem) error {
	problems := 0
	for _, item := range items {
		fmt.Fprintf(out, "%s %s", item.category, filepath.ToSlash(item.path))
		if item.detail != "" {
			fmt.Fprintf(out, ": %s", item.detail)
		}
		fmt.Fprintln(out)
		if item.category == "absent-remotely" || item.category == "mismatch" {
			problems++
		}
	}
	if problems != 0 {
		return fmt.Errorf("workspace has %d absent or mismatched repositories", problems)
	}
	return nil
}

func (a *App) syncClone(ctx context.Context, root *os.Root, item *workspaceItem) (retErr error) {
	transport := item.provider.Transport
	if transport == "" {
		transport = "https"
	}
	cloneURL := item.repository.CloneURL
	if transport == "ssh" {
		cloneURL = item.repository.SSHURL
	}
	if cloneURL == "" {
		return fmt.Errorf("provider supplied no %s clone URL", transport)
	}
	stagingParent, err := os.MkdirTemp("", "colt-sync-")
	if err != nil {
		return fmt.Errorf("create private clone staging directory: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, os.RemoveAll(stagingParent)) }()
	if err := os.Chmod(stagingParent, 0o700); err != nil {
		return fmt.Errorf("secure clone staging directory: %w", err)
	}
	staging := filepath.Join(stagingParent, "repository")
	operationCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := a.Git.Clone(operationCtx, cloneURL, staging, "", item.alias, item.project); err != nil {
		return err
	}
	if err := a.Git.SetIdentity(operationCtx, staging, item.provider.GitName, item.provider.GitEmail); err != nil {
		return err
	}
	if transport == "https" {
		if err := a.Git.ConfigureCredentialHelper(operationCtx, staging, cloneURL, "", item.alias, item.project); err != nil {
			return err
		}
	}
	if err := ensureWorkspaceParents(root, filepath.Dir(item.path)); err != nil {
		return err
	}
	return installCloneAbsent(root, staging, item.path)
}

func ensureWorkspaceParents(root *os.Root, parent string) error {
	if parent == "." {
		return nil
	}
	current := ""
	for _, part := range strings.Split(filepath.ToSlash(parent), "/") {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			if err := root.Mkdir(current, 0o755); err != nil {
				return fmt.Errorf("create workspace namespace: %w", err)
			}
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("workspace namespace is not an ordinary directory")
		}
	}
	return nil
}

func (a *App) mirrorCommand() *cobra.Command {
	namespace := ""
	targetNamespace := ""
	repoName := ""
	replace := false
	cmd := &cobra.Command{Use: "mirror <source-provider> <target-provider>", Short: "Mirror a provider namespace once", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		return a.mirror(cmd, args[0], args[1], namespace, targetNamespace, repoName, replace)
	}}
	cmd.Flags().StringVar(&namespace, "namespace", "", "source namespace (defaults to the source provider namespace)")
	cmd.Flags().StringVar(&targetNamespace, "target-namespace", "", "target namespace (defaults to the target provider namespace)")
	cmd.Flags().StringVar(&repoName, "repository", "", "mirror only this repository name")
	cmd.Flags().BoolVar(&replace, "replace", false, "force source refs onto existing target repositories")
	return cmd
}

func (a *App) mirror(cmd *cobra.Command, sourceAlias, targetAlias, namespace, targetNamespace, repoName string, replace bool) error {
	if sourceAlias == targetAlias {
		return errors.New("source and target providers must be distinct")
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		return err
	}
	_, source, err := cfg.Resolve(sourceAlias)
	if err != nil {
		return err
	}
	_, target, err := cfg.Resolve(targetAlias)
	if err != nil {
		return err
	}
	if namespace == "" {
		namespace = source.Namespace
	}
	source.Namespace = namespace
	if err := config.ValidateProvider(sourceAlias, source); err != nil {
		return fmt.Errorf("invalid source namespace: %w", err)
	}
	if repoName != "" && !config.ValidProjectName(repoName) {
		return errors.New("invalid --repository; use 1-255 letters, digits, '.', '_' or '-' and no path separators")
	}
	if targetNamespace != "" {
		target.Namespace = targetNamespace
		if err := config.ValidateProvider(targetAlias, target); err != nil {
			return fmt.Errorf("invalid target namespace: %w", err)
		}
	}
	sourceToken, _, err := config.Token(source, a.credentialStore())
	if err != nil {
		return err
	}
	targetToken, _, err := config.Token(target, a.credentialStore())
	if err != nil {
		return err
	}
	sourceClient, err := a.NewClient(source, sourceToken)
	if err != nil {
		return err
	}
	targetClient, err := a.NewClient(target, targetToken)
	if err != nil {
		return err
	}
	repositories, err := sourceClient.List(cmd.Context())
	if err != nil {
		return err
	}
	if len(repositories) > config.MaxProviderRepositories {
		return fmt.Errorf("source returned more than %d repositories", config.MaxProviderRepositories)
	}
	sort.Slice(repositories, func(i, j int) bool { return repositories[i].CloneURL < repositories[j].CloneURL })
	if a.Git == nil {
		return errors.New("native Git is unavailable")
	}
	if err := a.Git.Available(); err != nil {
		return err
	}
	var failures []string
	matched := 0
	for _, repository := range repositories {
		name, repoNamespace, validationErr := repositoryIdentity(repository, source)
		if validationErr != nil {
			failures = append(failures, "unsafe source repository metadata")
			continue
		}
		if !namespaceMatches(strings.Split(repoNamespace, "/"), strings.Split(namespace, "/"), source.Type) {
			continue
		}
		if repoName != "" && name != repoName {
			continue
		}
		matched++
		if err := a.mirrorRepository(cmd.Context(), sourceAlias, targetAlias, name, source, target, repository, targetClient, replace, providerTokenEnvNames(cfg)); err != nil {
			failures = append(failures, name+": "+err.Error())
			fmt.Fprintf(cmd.OutOrStdout(), "failed %s: %v\n", name, err)
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "mirrored %s\n", name)
	}
	if repoName != "" && matched == 0 {
		return fmt.Errorf("repository %q not found in source namespace %q", repoName, namespace)
	}
	if len(failures) != 0 {
		return fmt.Errorf("mirror incomplete: %d repositories failed", len(failures))
	}
	return nil
}

func (a *App) mirrorRepository(ctx context.Context, sourceAlias, targetAlias, project string, source, target config.Provider, repository provider.Repository, targetClient provider.Client, replace bool, tokenEnvNames []string) (retErr error) {
	sourceTransport := source.Transport
	if sourceTransport == "" {
		sourceTransport = "https"
	}
	sourceURL := repository.CloneURL
	if sourceTransport == "ssh" {
		sourceURL = repository.SSHURL
	}
	temporary, err := os.MkdirTemp("", "colt-mirror-")
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, os.RemoveAll(temporary)) }()
	if err := os.Chmod(temporary, 0o700); err != nil {
		return err
	}
	bare := filepath.Join(temporary, "repository.git")
	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := a.Git.MirrorClone(operationCtx, sourceURL, bare, sourceAlias, project, providerTokenEnvName(source), tokenEnvNames); err != nil {
		return err
	}
	targetRepository, err := targetClient.Get(operationCtx, project)
	if err != nil && !errors.Is(err, provider.ErrNotFound) {
		return err
	}
	if err == nil && targetRepository != nil && !replace {
		return provider.ErrConflict
	}
	if errors.Is(err, provider.ErrNotFound) || targetRepository == nil {
		targetRepository, err = targetClient.Create(operationCtx, project)
		if err != nil {
			return err
		}
	}
	if targetRepository == nil {
		return errors.New("target provider returned no repository")
	}
	targetName, _, err := validatedRepository(*targetRepository, target)
	if err != nil {
		return fmt.Errorf("target provider returned an invalid repository: %w", err)
	}
	if !strings.EqualFold(targetName, project) {
		return fmt.Errorf("target provider returned repository %q for %q", targetName, project)
	}
	targetTransport := target.Transport
	if targetTransport == "" {
		targetTransport = "https"
	}
	targetURL := targetRepository.CloneURL
	if targetTransport == "ssh" {
		targetURL = targetRepository.SSHURL
	}
	if targetURL == "" {
		return errors.New("target provider returned no push URL")
	}
	return a.Git.MirrorPush(operationCtx, bare, targetURL, targetAlias, project, replace, providerTokenEnvName(target), tokenEnvNames)
}

func providerTokenEnvName(p config.Provider) string {
	if p.Auth.TokenEnv != "" {
		return p.Auth.TokenEnv
	}
	return credential.ConventionalVar(p.Type)
}
