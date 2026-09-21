package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func openWorkRoot(path string) (*os.Root, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open working directory: %w", err)
	}
	opened, err := root.Stat(".")
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(opened, current) {
		root.Close()
		return nil, errors.New("working directory is not a stable ordinary directory")
	}
	return root, nil
}

func validateCloneDestination(root *os.Root, name string) (bool, os.FileMode, error) {
	if name == "." || !filepath.IsLocal(name) || filepath.Clean(name) != name {
		return false, 0, errors.New("clone destination must be a clean relative path")
	}
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("inspect clone destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, 0, errors.New("clone destination is not an ordinary directory")
	}
	dir, err := root.OpenRoot(name)
	if err != nil {
		return false, 0, fmt.Errorf("open clone destination: %w", err)
	}
	defer dir.Close()
	f, err := dir.Open(".")
	if err != nil {
		return false, 0, fmt.Errorf("inspect clone destination: %w", err)
	}
	defer f.Close()
	if _, err = f.Readdirnames(1); err == nil {
		return false, 0, errors.New("clone destination is not empty")
	} else if !errors.Is(err, io.EOF) {
		return false, 0, fmt.Errorf("inspect clone destination: %w", err)
	}
	return true, info.Mode().Perm(), nil
}

func installClone(root *os.Root, source, destination string) (retErr error) {
	return installCloneWithExisting(root, source, destination, true)
}

func installCloneAbsent(root *os.Root, source, destination string) (retErr error) {
	return installCloneWithExisting(root, source, destination, false)
}

func installCloneWithExisting(root *os.Root, source, destination string, allowExisting bool) (retErr error) {
	existed, destinationMode, err := validateCloneDestination(root, destination)
	if err != nil {
		return err
	}
	if existed && !allowExisting {
		return errors.New("install clone: destination appeared during reconciliation")
	}
	sourceRoot, err := os.OpenRoot(source)
	if err != nil {
		return fmt.Errorf("open staged clone: %w", err)
	}
	defer sourceRoot.Close()

	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("name staged clone: %w", err)
	}
	temporary := ".colt-clone-" + hex.EncodeToString(random[:])
	if err := root.Mkdir(temporary, 0o700); err != nil {
		return fmt.Errorf("create confined clone staging directory: %w", err)
	}
	defer func() {
		if err := root.RemoveAll(temporary); retErr == nil && err != nil {
			retErr = fmt.Errorf("clean confined clone staging directory: %w", err)
		}
	}()
	if err := copyCloneTree(sourceRoot, root, ".", temporary); err != nil {
		return fmt.Errorf("materialize clone: %w", err)
	}

	// Rename installs the completed tree without following the destination.
	if err := root.Rename(temporary, destination); err == nil {
		return nil
	} else if !existed {
		return fmt.Errorf("install clone: %w", err)
	}
	// Some platforms cannot replace an existing empty directory. Revalidate it,
	// remove only that empty directory, and restore it if installation loses a race.
	stillExists, _, checkErr := validateCloneDestination(root, destination)
	if checkErr != nil || !stillExists {
		return fmt.Errorf("install clone: destination changed: %w", errors.Join(err, checkErr))
	}
	if removeErr := root.Remove(destination); removeErr != nil {
		return fmt.Errorf("install clone: remove empty destination: %w", removeErr)
	}
	if renameErr := root.Rename(temporary, destination); renameErr != nil {
		if _, statErr := root.Lstat(destination); errors.Is(statErr, fs.ErrNotExist) {
			_ = root.Mkdir(destination, destinationMode)
		}
		return fmt.Errorf("install clone: %w", renameErr)
	}
	return nil
}

func copyCloneTree(source, destination *os.Root, sourceName, destinationName string) error {
	dir, err := source.Open(sourceName)
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(sourceName, entry.Name())
		destinationPath := filepath.Join(destinationName, entry.Name())
		info, err := source.Lstat(sourcePath)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			if err := destination.Mkdir(destinationPath, 0o700); err != nil {
				return err
			}
			if err := copyCloneTree(source, destination, sourcePath, destinationPath); err != nil {
				return err
			}
			if err := destination.Chmod(destinationPath, info.Mode().Perm()); err != nil {
				return err
			}
		case info.Mode()&os.ModeSymlink != 0:
			target, err := source.Readlink(sourcePath)
			if err != nil {
				return err
			}
			if err := destination.Symlink(target, destinationPath); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			if err := copyCloneFile(source, destination, sourcePath, destinationPath, info); err != nil {
				return err
			}
		default:
			return fmt.Errorf("refusing special file %q", sourcePath)
		}
	}
	return nil
}

func copyCloneFile(source, destination *os.Root, sourceName, destinationName string, expected os.FileInfo) error {
	in, err := source.Open(sourceName)
	if err != nil {
		return err
	}
	defer in.Close()
	actual, err := in.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(expected, actual) {
		return errors.New("staged clone entry changed while copying")
	}
	out, err := destination.OpenFile(destinationName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, expected.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	return destination.Chmod(destinationName, expected.Mode().Perm())
}
