package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MozeBaltyk/Colt/internal/config"
	gitnative "github.com/MozeBaltyk/Colt/internal/git"
	"github.com/MozeBaltyk/Colt/internal/provider"
	"github.com/spf13/cobra"
)

type codedError struct {
	status int
	err    error
}

func (e codedError) Error() string { return e.err.Error() }
func (e codedError) Unwrap() error { return e.err }
func (e codedError) ExitCode() int { return e.status }

// ExitCode maps only Colt's private health errors to their process status.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var coded codedError
	if errors.As(err, &coded) {
		return coded.status
	}
	return 1
}

type healthResults struct {
	lines       map[string]struct{}
	operational bool
}

func newHealthResults() *healthResults { return &healthResults{lines: map[string]struct{}{}} }

func (r *healthResults) add(label, finding string) { r.lines[label+": "+finding] = struct{}{} }
func (r *healthResults) operation(label string) {
	r.add(label, "operational-error")
	r.operational = true
}

type healthWorkspaceItem struct {
	alias, project, path string
	provider             config.Provider
	listed               *provider.Repository
	client               provider.Client
	planningError        bool
}

func (a *App) checkCommand() *cobra.Command {
	all := false
	cmd := &cobra.Command{Use: "check", Short: "Check repository health without mutation", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return a.check(cmd, all)
	}}
	cmd.Flags().BoolVar(&all, "all", false, "check every repository selected by the workspace")
	return cmd
}

func (a *App) check(cmd *cobra.Command, all bool) error {
	if a.pathErr != nil {
		return codedError{2, errors.New("health check configuration failed")}
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil || cfg.Policy == nil {
		return codedError{2, errors.New("health policy is not configured or is invalid")}
	}
	if a.Git == nil || a.NewClient == nil {
		return codedError{2, errors.New("health check operation is unavailable")}
	}
	rootPath, err := a.workDir()
	if err != nil {
		return codedError{2, errors.New("health check operation failed")}
	}
	results := newHealthResults()
	if all {
		if cfg.Workspace == nil {
			return codedError{2, errors.New("health workspace is not configured")}
		}
		items, planningLines := a.healthWorkspacePlan(cmd.Context(), cfg)
		for _, line := range planningLines {
			results.operation(line)
		}
		workspaceRoot, openErr := os.OpenRoot(rootPath)
		if openErr != nil || !stableRoot(workspaceRoot) {
			if workspaceRoot != nil {
				workspaceRoot.Close()
			}
			return codedError{2, errors.New("health workspace root cannot be opened safely")}
		}
		defer workspaceRoot.Close()
		for _, item := range items {
			a.checkWorkspaceRepository(cmd.Context(), cfg, rootPath, workspaceRoot, item, results)
		}
	} else {
		a.checkCurrentRepository(cmd.Context(), cfg, rootPath, results)
	}

	lines := make([]string, 0, len(results.lines))
	for line := range results.lines {
		lines = append(lines, line)
	}
	sort.Strings(lines)
	for _, line := range lines {
		fmt.Fprintln(cmd.OutOrStdout(), line)
	}
	if results.operational {
		return codedError{2, reportedError{"health check reported operational errors"}}
	}
	if len(lines) != 0 {
		return codedError{1, reportedError{"health check reported findings"}}
	}
	return nil
}

func (a *App) healthWorkspacePlan(ctx context.Context, cfg config.Config) ([]healthWorkspaceItem, []string) {
	var items []healthWorkspaceItem
	var operationLabels []string
	identities, paths := map[string]bool{}, map[string]bool{}
	appendItem := func(selection config.RepositorySelection, p config.Provider, name string, listed *provider.Repository, client provider.Client, planningError bool) {
		path, err := workspacePath(selection.Namespace, name)
		if err != nil {
			operationLabels = append(operationLabels, filepath.ToSlash(selection.Namespace)+"/*")
			return
		}
		identity := selection.Provider + "\x00" + selection.Namespace + "\x00" + repositoryKey(name, p.Type)
		pathKey := strings.ToLower(filepath.ToSlash(path))
		if identities[identity] || paths[pathKey] {
			operationLabels = append(operationLabels, filepath.ToSlash(path))
			return
		}
		identities[identity], paths[pathKey] = true, true
		items = append(items, healthWorkspaceItem{alias: selection.Provider, project: name, path: path, provider: p, listed: listed, client: client, planningError: planningError})
	}
	for _, selection := range cfg.Workspace.Repositories {
		if selection.Include != nil && len(*selection.Include) == 0 {
			continue
		}
		p := cfg.Providers[selection.Provider]
		selectionLabel := filepath.ToSlash(selection.Namespace) + "/*"
		namedFailure := func(client provider.Client) {
			if selection.Include == nil {
				operationLabels = append(operationLabels, selectionLabel)
				return
			}
			for _, name := range *selection.Include {
				appendItem(selection, p, name, nil, client, true)
			}
		}
		token, _, err := config.Token(p, a.credentialStore())
		if err != nil {
			namedFailure(nil)
			continue
		}
		client, err := a.NewClient(p, token)
		if err != nil {
			namedFailure(nil)
			continue
		}
		operationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		repositories, err := client.List(operationCtx)
		cancel()
		if err != nil || len(repositories) > config.MaxProviderRepositories {
			namedFailure(client)
			continue
		}
		remote := map[string]*provider.Repository{}
		unsafe := map[string]bool{}
		selectionUnsafe := false
		for i := range repositories {
			repository := repositories[i]
			name, namespace, identityErr := validatedRepository(repository, p)
			if identityErr != nil {
				selectionUnsafe = true
				continue
			}
			if !namespaceMatches(strings.Split(namespace, "/"), strings.Split(selection.Namespace, "/"), p.Type) {
				continue
			}
			key := repositoryKey(name, p.Type)
			if healthRepositoryMetadata(repository, p, name) != nil || remote[key] != nil {
				unsafe[key], selectionUnsafe = true, true
				delete(remote, key)
				continue
			}
			repository.Name, repository.Namespace = name, namespace
			copy := repository
			remote[key] = &copy
		}
		var names []string
		if selectionUnsafe {
			operationLabels = append(operationLabels, selectionLabel)
		}
		if selection.Include == nil {
			for _, repository := range remote {
				names = append(names, repository.Name)
			}
		} else {
			names = append(names, (*selection.Include)...)
		}
		sort.Strings(names)
		for _, name := range names {
			key := repositoryKey(name, p.Type)
			appendItem(selection, p, name, remote[key], client, unsafe[key])
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].path < items[j].path })
	return items, operationLabels
}

func (a *App) checkCurrentRepository(ctx context.Context, cfg config.Config, rootPath string, results *healthResults) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		results.operation(".")
		return
	}
	defer root.Close()
	if !ordinaryGitDir(root) {
		results.operation(".")
		return
	}
	state, err := a.inspectGit(ctx, rootPath)
	if err != nil || !stableRoot(root) {
		results.operation(".")
		return
	}
	requiredChecks(root, ".", cfg.Policy.Repository.Require, results)
	if !state.Clean {
		results.add(".", "worktree-dirty")
	}
	alias, _, project, ambiguous := matchingOriginResult(cfg, state.Origin)
	if ambiguous {
		results.operation(".")
		return
	}
	if alias == "" {
		if state.Origin == "" {
			results.add(".", "origin-missing")
		} else {
			results.add(".", "origin-unrecognized")
		}
		return
	}
	p := cfg.Providers[alias]
	checkIdentity(state, p, ".", results)
	client := a.healthClient(p, results, ".")
	a.checkRemote(ctx, cfg, ".", project, p, client, nil, &state, false, results)
}

func (a *App) checkWorkspaceRepository(ctx context.Context, cfg config.Config, rootPath string, workspaceRoot *os.Root, item healthWorkspaceItem, results *healthResults) {
	label := filepath.ToSlash(item.path)
	if item.planningError {
		results.operation(label)
	}
	var state *gitnative.HealthState
	info, err := workspaceRoot.Lstat(item.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		results.add(label, "repository-missing")
	case err != nil:
		results.operation(label)
	case !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
		results.add(label, "repository-mismatch")
	default:
		root, openErr := workspaceRoot.OpenRoot(item.path)
		if openErr != nil || !stableRoot(root) {
			if root != nil {
				root.Close()
			}
			results.operation(label)
			break
		}
		requiredChecks(root, label, cfg.Policy.Repository.Require, results)
		if !ordinaryGitDir(root) {
			results.add(label, "repository-mismatch")
			root.Close()
			break
		}
		inspected, inspectErr := a.inspectGit(ctx, filepath.Join(rootPath, item.path))
		if inspectErr != nil || !stableRoot(root) {
			results.operation(label)
		} else {
			state = &inspected
			checkIdentity(inspected, item.provider, label, results)
			if !inspected.Clean {
				results.add(label, "worktree-dirty")
			}
		}
		root.Close()
	}
	a.checkRemote(ctx, cfg, label, item.project, item.provider, item.client, item.listed, state, true, results)
}

func (a *App) healthClient(p config.Provider, results *healthResults, label string) provider.Client {
	token, _, err := config.Token(p, a.credentialStore())
	if err != nil {
		results.operation(label)
		return nil
	}
	client, err := a.NewClient(p, token)
	if err != nil {
		results.operation(label)
		return nil
	}
	return client
}

func (a *App) checkRemote(ctx context.Context, cfg config.Config, label, project string, p config.Provider, client provider.Client, listed *provider.Repository, state *gitnative.HealthState, workspace bool, results *healthResults) {
	if client == nil {
		results.operation(label)
		return
	}
	operationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	remote, err := client.Get(operationCtx, project)
	if errors.Is(err, provider.ErrNotFound) {
		results.add(label, "remote-absent")
		return
	}
	if err != nil || remote == nil || healthRepositoryMetadata(*remote, p, project) != nil {
		results.operation(label)
		return
	}
	if listed != nil && (listed.CloneURL != remote.CloneURL || listed.SSHURL != remote.SSHURL || listed.DefaultBranch != remote.DefaultBranch || listed.Visibility != remote.Visibility) {
		results.add(label, "repository-mismatch")
	}
	if state != nil {
		if state.Origin == "" {
			if workspace {
				results.add(label, "repository-mismatch")
			} else {
				results.add(label, "origin-missing")
			}
		} else if state.Origin != remote.CloneURL && (remote.SSHURL == "" || state.Origin != remote.SSHURL) {
			if workspace {
				results.add(label, "repository-mismatch")
			} else {
				results.add(label, "origin-mismatch")
			}
		}
	}
	if remote.DefaultBranch != cfg.Policy.Repository.DefaultBranch {
		results.add(label, "default-branch")
	}
	if !contains(cfg.Policy.Repository.AllowedVisibility, remote.Visibility) {
		results.add(label, "visibility")
	}
}

func healthRepositoryMetadata(repository provider.Repository, p config.Provider, project string) error {
	name, _, err := validatedRepository(repository, p)
	if err != nil || repositoryKey(name, p.Type) != repositoryKey(project, p.Type) || repository.Name != "" && repositoryKey(repository.Name, p.Type) != repositoryKey(project, p.Type) || !config.ValidBranch(repository.DefaultBranch) {
		return errors.New("unsafe repository metadata")
	}
	if repository.Visibility != "private" && repository.Visibility != "internal" && repository.Visibility != "public" {
		return errors.New("unsafe repository metadata")
	}
	return nil
}

func checkIdentity(state gitnative.HealthState, p config.Provider, label string, results *healthResults) {
	if state.Name != p.GitName {
		results.add(label, "identity-name")
	}
	if state.Email != p.GitEmail {
		results.add(label, "identity-email")
	}
}

func (a *App) inspectGit(ctx context.Context, dir string) (gitnative.HealthState, error) {
	operationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return a.Git.Health(operationCtx, dir)
}

func ordinaryGitDir(root *os.Root) bool {
	if !stableRoot(root) {
		return false
	}
	info, err := root.Lstat(".git")
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func requiredChecks(root *os.Root, label string, paths []string, results *healthResults) {
	for _, path := range paths {
		regular, err := regularFile(root, path)
		if err != nil {
			results.operation(label)
			continue
		}
		if !regular {
			results.add(label, "required-file-missing "+path)
		}
	}
}

func regularFile(root *os.Root, path string) (bool, error) {
	if !stableRoot(root) {
		return false, errors.New("repository root changed")
	}
	current := ""
	parts := strings.Split(path, "/")
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, nil
		}
	}
	expected, err := root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !expected.Mode().IsRegular() || expected.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	f, err := root.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !actual.Mode().IsRegular() || !os.SameFile(expected, actual) || !stableRoot(root) {
		return false, errors.New("required file changed while opening")
	}
	return true, nil
}

func stableRoot(root *os.Root) bool {
	if root == nil {
		return false
	}
	expected, err := root.Stat(".")
	if err != nil {
		return false
	}
	actual, err := os.Lstat(root.Name())
	return err == nil && actual.IsDir() && actual.Mode()&os.ModeSymlink == 0 && os.SameFile(expected, actual)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
