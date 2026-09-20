package template

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MozeBaltyk/Colt/internal/config"
)

const (
	MaxEntries    = 1024
	MaxSourceFile = 1 << 20
	MaxOutputFile = 2 << 20
	MaxTotalSize  = 16 << 20
	MaxPathSize   = 4096
)

type Entry struct {
	Path string
	Mode fs.FileMode
	Data []byte
	Dir  bool
}

type Plan struct{ Entries []Entry }

func OpenDestination(path string) (*os.Root, error) {
	before, err := os.Lstat(path)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, errors.New("template destination must be an ordinary directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open template destination: %w", err)
	}
	opened, err := root.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
		root.Close()
		return nil, errors.New("template destination changed while it was opened")
	}
	return root, nil
}

func SourcePath(configPath, source string) string {
	if filepath.IsAbs(source) {
		return filepath.Clean(source)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(configPath), source))
}

func Digest(source string) (string, error) {
	entries, digest, err := readSource(source)
	_ = entries
	return digest, err
}

func Prepare(source, expectedDigest string, declarations map[string]config.TemplateParameter, values map[string]string) (Plan, error) {
	entries, digest, err := readSource(source)
	if err != nil {
		return Plan{}, err
	}
	if digest != expectedDigest {
		return Plan{}, fmt.Errorf("template digest mismatch: configured %s, calculated %s", expectedDigest, digest)
	}
	seen := make(map[string]bool, len(entries))
	total := 0
	plan := Plan{Entries: make([]Entry, 0, len(entries))}
	for _, entry := range entries {
		path, err := interpolate([]byte(filepath.ToSlash(entry.Path)), declarations, values)
		if err != nil {
			return Plan{}, fmt.Errorf("template path %q: %w", entry.Path, err)
		}
		outPath := string(path)
		if err := safePath(outPath); err != nil {
			return Plan{}, fmt.Errorf("template output path %q: %w", outPath, err)
		}
		if _, exists := seen[outPath]; exists {
			return Plan{}, fmt.Errorf("template output path collision at %q", outPath)
		}
		seen[outPath] = entry.Dir
		out := Entry{Path: filepath.FromSlash(outPath), Mode: entry.Mode, Dir: entry.Dir}
		if !entry.Dir {
			out.Data, err = interpolate(entry.Data, declarations, values)
			if err != nil {
				return Plan{}, fmt.Errorf("template file %q: %w", entry.Path, err)
			}
			if len(out.Data) > MaxOutputFile || total+len(out.Data) > MaxTotalSize {
				return Plan{}, errors.New("template interpolated output exceeds size limits")
			}
			total += len(out.Data)
		}
		plan.Entries = append(plan.Entries, out)
	}
	for path := range seen {
		for parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path))); parent != "."; parent = filepath.ToSlash(filepath.Dir(filepath.FromSlash(parent))) {
			if exists, ok := seen[parent]; ok && !exists {
				return Plan{}, fmt.Errorf("template path %q has file parent %q", path, parent)
			}
		}
	}
	sort.SliceStable(plan.Entries, func(i, j int) bool {
		if plan.Entries[i].Dir != plan.Entries[j].Dir {
			return plan.Entries[i].Dir
		}
		return plan.Entries[i].Path < plan.Entries[j].Path
	})
	return plan, nil
}

func (p Plan) MaterializeRoot(root *os.Root) error {
	for _, entry := range p.Entries {
		rel := filepath.ToSlash(entry.Path)
		if entry.Dir {
			if err := root.Mkdir(rel, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("create template directory %q: %w", entry.Path, err)
			}
			info, err := root.Lstat(rel)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("template directory %q collides with unsafe destination state", entry.Path)
			}
			dir, err := root.Open(rel)
			if err != nil {
				return fmt.Errorf("open template directory %q: %w", entry.Path, err)
			}
			opened, statErr := dir.Stat()
			if statErr != nil || !opened.IsDir() || !os.SameFile(info, opened) {
				dir.Close()
				return fmt.Errorf("template directory %q changed while it was opened", entry.Path)
			}
			chmodErr := dir.Chmod(entry.Mode)
			closeErr := dir.Close()
			if chmodErr != nil || closeErr != nil {
				return fmt.Errorf("secure template directory %q: %w", entry.Path, errors.Join(chmodErr, closeErr))
			}
			continue
		}
		if err := secureParents(root, filepath.ToSlash(filepath.Dir(entry.Path))); err != nil {
			return err
		}
		file, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create template file %q: %w", entry.Path, err)
		}
		_, writeErr := file.Write(entry.Data)
		chmodErr := file.Chmod(entry.Mode)
		closeErr := file.Close()
		if writeErr != nil || chmodErr != nil || closeErr != nil {
			_ = root.Remove(rel)
			return fmt.Errorf("write template file %q: %w", entry.Path, errors.Join(writeErr, chmodErr, closeErr))
		}
	}
	return nil
}

func secureParents(root *os.Root, relative string) error {
	if relative == "." {
		return nil
	}
	current := ""
	for _, part := range strings.Split(relative, "/") {
		current = strings.TrimPrefix(current+"/"+part, "/")
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return errors.New("template output parent was not prepared from a source directory")
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("template output parent is not a safe directory")
		}
	}
	return nil
}

func readSource(source string) ([]Entry, string, error) {
	root, err := filepath.Abs(source)
	if err != nil {
		return nil, "", fmt.Errorf("resolve template source: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, "", errors.New("template source must be an ordinary directory")
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, "", fmt.Errorf("open template source: %w", err)
	}
	defer rootHandle.Close()
	var entries []Entry
	total := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(entries) >= MaxEntries {
			return errors.New("template source exceeds entry count limit")
		}
		if err := safePath(filepath.ToSlash(rel)); err != nil {
			return fmt.Errorf("unsafe template source path %q: %w", rel, err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if err := validateMode(rel, info.Mode()); err != nil {
			return err
		}
		entry := Entry{Path: rel, Mode: info.Mode().Perm(), Dir: info.IsDir()}
		if !entry.Dir {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("template source %q is a symlink or special file", rel)
			}
			if info.Size() > MaxSourceFile || info.Size() < 0 || total+int(info.Size()) > MaxTotalSize {
				return errors.New("template source exceeds size limits")
			}
			file, err := rootHandle.Open(filepath.ToSlash(rel))
			if err != nil {
				return err
			}
			openedInfo, statErr := file.Stat()
			if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
				file.Close()
				return fmt.Errorf("template source %q changed or is not a regular file", rel)
			}
			entry.Data, err = io.ReadAll(io.LimitReader(file, MaxSourceFile+1))
			closeErr := file.Close()
			if err != nil || closeErr != nil {
				return errors.Join(err, closeErr)
			}
			if len(entry.Data) > MaxSourceFile {
				return errors.New("template source file exceeds size limit")
			}
			total += len(entry.Data)
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("inspect template source: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return filepath.ToSlash(entries[i].Path) < filepath.ToSlash(entries[j].Path) })
	hash := sha256.New()
	for _, entry := range entries {
		kind := "f"
		if entry.Dir {
			kind = "d"
		}
		fmt.Fprintf(hash, "%s\x00%s\x00%04o\x00", kind, filepath.ToSlash(entry.Path), entry.Mode.Perm())
		if !entry.Dir {
			fmt.Fprintf(hash, "%d\x00", len(entry.Data))
			_, _ = hash.Write(entry.Data)
		}
	}
	return entries, fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func validateMode(path string, mode fs.FileMode) error {
	if mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fmt.Errorf("template source %q has unsafe special permissions", path)
	}
	permissions := mode.Perm()
	if mode.IsDir() {
		if permissions != 0o700 && permissions != 0o750 && permissions != 0o755 {
			return fmt.Errorf("template source directory %q mode %04o is unsafe; use 0700, 0750, or 0755", path, permissions)
		}
		return nil
	}
	if mode.IsRegular() && permissions != 0o400 && permissions != 0o440 && permissions != 0o444 && permissions != 0o600 && permissions != 0o640 && permissions != 0o644 {
		return fmt.Errorf("template source file %q mode %04o is unsafe; use a non-executable, non-writable-by-group/world mode", path, permissions)
	}
	return nil
}

func safePath(path string) error {
	if path == "" || len(path) > MaxPathSize || strings.Contains(path, "\\") || strings.HasPrefix(path, "/") || filepath.IsAbs(filepath.FromSlash(path)) || strings.IndexFunc(path, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return errors.New("path must be a bounded relative slash path")
	}
	if path != filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) {
		return errors.New("path must not contain empty, current, or traversal segments")
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".git" || part == "." || part == ".." || part == "" {
			return errors.New("path contains a reserved or unsafe segment")
		}
	}
	return nil
}

func interpolate(input []byte, declarations map[string]config.TemplateParameter, values map[string]string) ([]byte, error) {
	var out bytes.Buffer
	for len(input) != 0 {
		start := bytes.Index(input, []byte("{{"))
		stray := bytes.Index(input, []byte("}}"))
		if stray >= 0 && (start < 0 || stray < start) {
			return nil, errors.New("malformed placeholder")
		}
		if start < 0 {
			out.Write(input)
			break
		}
		out.Write(input[:start])
		input = input[start+2:]
		end := bytes.Index(input, []byte("}}"))
		if end < 0 || bytes.Contains(input[:end], []byte("{{")) {
			return nil, errors.New("malformed placeholder")
		}
		key := string(input[:end])
		if _, ok := declarations[key]; !ok {
			return nil, fmt.Errorf("unknown or arbitrary placeholder %q", key)
		}
		value, ok := values[key]
		if !ok {
			return nil, fmt.Errorf("unresolved placeholder %q", key)
		}
		if out.Len()+len(value)+len(input[end+2:]) > MaxOutputFile+MaxPathSize {
			return nil, errors.New("interpolated value exceeds size limit")
		}
		out.WriteString(value)
		input = input[end+2:]
	}
	return out.Bytes(), nil
}
