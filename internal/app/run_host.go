package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// HostOperations is the narrow boundary around host inspection and mutation.
// Tests replace it so they never touch the machine running the test suite.
type HostOperations interface {
	OS() string
	EUID() int
	LookPath(string) (string, error)
	Exists(string) (bool, error)
	ReadDir(string) ([]fs.DirEntry, error)
	MkdirAll(string, fs.FileMode) error
	Mkdir(string, fs.FileMode) error
	WriteFile(string, []byte, fs.FileMode) error
	ReplaceFile(string, []byte, fs.FileMode) error
	Chown(string, int, int) error
	Run(context.Context, string, ...string) error
	RunOutput(context.Context, string, ...string) (string, error)
	ReadFile(string) ([]byte, error)
	Remove(string) error
	RemoveAll(string) error
	WaitReady(context.Context, string, string, string) error
}

type nativeHost struct{}

type limitedOutput struct{ bytes.Buffer }

func (w *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 4096 - w.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = w.Buffer.Write(p)
	}
	return n, nil
}

func (nativeHost) OS() string                           { return runtime.GOOS }
func (nativeHost) EUID() int                            { return os.Geteuid() }
func (nativeHost) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (nativeHost) MkdirAll(path string, mode fs.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
func (nativeHost) Mkdir(path string, mode fs.FileMode) error {
	if err := os.Mkdir(path, mode); err != nil {
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		return errors.Join(err, os.Remove(path))
	}
	return nil
}
func (nativeHost) Chown(path string, uid, gid int) error { return os.Chown(path, uid, gid) }
func (nativeHost) Exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}
func (nativeHost) ReadDir(path string) ([]fs.DirEntry, error) { return os.ReadDir(path) }
func (nativeHost) WriteFile(path string, data []byte, mode fs.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if err = f.Chmod(mode); err != nil {
		err = errors.Join(err, f.Close())
	} else {
		if _, err = f.Write(data); err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
	}
	if err != nil {
		err = errors.Join(err, os.Remove(path))
	}
	return err
}
func (nativeHost) Run(ctx context.Context, name string, args ...string) error {
	_, err := (nativeHost{}).RunOutput(ctx, name, args...)
	return err
}

func (nativeHost) RunOutput(ctx context.Context, name string, args ...string) (string, error) {
	var output limitedOutput
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("%s failed: %w; stderr suppressed to protect secrets; inspect podman info for runtime/storage or systemctl status and journalctl -u for the named service (redact logs before sharing)", filepath.Base(name), err)
	}
	return strings.TrimSpace(output.String()), nil
}
func (nativeHost) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (nativeHost) Remove(path string) error             { return os.Remove(path) }
func (nativeHost) RemoveAll(path string) error          { return os.RemoveAll(path) }
