package credential

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	maxFileSize = 1 << 20
	lockTimeout = time.Second
)

type fileCredential struct {
	Kind   string `yaml:"kind"`
	Secret string `yaml:"secret"`
}

type fileContents struct {
	Version     int                       `yaml:"version"`
	Credentials map[string]fileCredential `yaml:"credentials"`
}

// FileStore is the explicitly selected plaintext fallback. Path must name the
// internal credentials file, never config.yaml.
type FileStore struct {
	Path string
	mu   sync.Mutex
}

func NewFileStore(path string) *FileStore { return &FileStore{Path: path} }

// DefaultFilePath keeps credentials beside the selected config file, so the
// existing COLT_CONFIG override also relocates this internal file.
func DefaultFilePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "credentials")
}

func (s *FileStore) Get(id string) (Credential, error) {
	if err := ValidateID(id); err != nil {
		return Credential{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	contents, err := s.read()
	if err != nil {
		return Credential{}, err
	}
	cred, ok := contents.Credentials[id]
	if !ok {
		return Credential{}, ErrNotFound
	}
	return Credential{Kind: cred.Kind, Secret: cred.Secret}, nil
}

func (s *FileStore) Put(id string, cred Credential) (retErr error) {
	return s.put(id, cred, false)
}

func (s *FileStore) Create(id string, cred Credential) error {
	return s.put(id, cred, true)
}

func (s *FileStore) put(id string, cred Credential, create bool) (retErr error) {
	if err := ValidateID(id); err != nil {
		return err
	}
	if err := validateCredential(cred); err != nil {
		return errors.New("refusing to store an invalid credential")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockMutation()
	if err != nil {
		return err
	}
	defer func() {
		if err := unlock(); err != nil {
			cleanupErr := fmt.Errorf("%w: credential lock cleanup failed", ErrPersistenceUncertain)
			if retErr == nil {
				retErr = cleanupErr
			} else {
				retErr = errors.Join(retErr, cleanupErr)
			}
		}
	}()
	contents, err := s.read()
	if err != nil {
		return err
	}
	if _, exists := contents.Credentials[id]; create && exists {
		return ErrAlreadyExists
	}
	contents.Credentials[id] = fileCredential{Kind: cred.Kind, Secret: cred.Secret}
	return s.write(contents)
}

func (s *FileStore) lockMutation() (func() error, error) {
	if err := validatePlaintextPlatform(); err != nil {
		return nil, err
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fileError("create credential directory", err)
	}
	dirInfo, err := inspectDirectory(dir, false)
	if err != nil {
		return nil, err
	}
	lockPath := s.Path + ".lock"
	deadline := time.Now().Add(lockTimeout)
	var f *os.File
	for {
		f, err = os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fileError("create credential lock", err)
		}
		if time.Now().After(deadline) {
			return nil, errors.New("credential file is busy")
		}
		time.Sleep(25 * time.Millisecond)
	}
	opened, err := f.Stat()
	if err != nil || validateFileInfo(opened) != nil {
		_ = f.Close()
		_ = os.Remove(lockPath)
		return nil, errors.New("credential lock file is unsafe")
	}
	currentDir, err := inspectDirectory(dir, false)
	if err != nil || !os.SameFile(dirInfo, currentDir) {
		_ = f.Close()
		_ = os.Remove(lockPath)
		return nil, errors.New("credential directory changed while locking")
	}
	return func() error {
		if err := f.Close(); err != nil {
			return err
		}
		current, err := os.Lstat(lockPath)
		if err != nil || !os.SameFile(opened, current) {
			return errors.New("credential lock file changed")
		}
		return os.Remove(lockPath)
	}, nil
}

func (s *FileStore) Delete(id string) (retErr error) {
	_, err := s.delete(id, nil)
	return err
}

func (s *FileStore) DeleteIf(id string, cred Credential) (bool, error) {
	if err := validateCredential(cred); err != nil {
		return false, errors.New("invalid credential comparison")
	}
	return s.delete(id, &cred)
}

func (s *FileStore) delete(id string, expected *Credential) (deleted bool, retErr error) {
	if err := ValidateID(id); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockMutation()
	if err != nil {
		return false, err
	}
	defer func() {
		if err := unlock(); err != nil {
			cleanupErr := fmt.Errorf("%w: credential lock cleanup failed", ErrPersistenceUncertain)
			if retErr == nil {
				retErr = cleanupErr
			} else {
				retErr = errors.Join(retErr, cleanupErr)
			}
		}
	}()
	contents, err := s.read()
	if err != nil {
		return false, err
	}
	current, ok := contents.Credentials[id]
	if !ok {
		if expected != nil {
			return false, nil
		}
		return false, ErrNotFound
	}
	if expected != nil && !sameCredential(Credential{Kind: current.Kind, Secret: current.Secret}, *expected) {
		return false, nil
	}
	delete(contents.Credentials, id)
	if err := s.write(contents); err != nil {
		return false, err
	}
	return true, nil
}

func emptyFile() fileContents {
	return fileContents{Version: 1, Credentials: map[string]fileCredential{}}
}

func (s *FileStore) read() (fileContents, error) {
	if err := validatePlaintextPlatform(); err != nil {
		return fileContents{}, err
	}
	if filepath.Base(filepath.Clean(s.Path)) == "config.yaml" {
		return fileContents{}, errors.New("credential file must be separate from config.yaml")
	}
	dir := filepath.Dir(s.Path)
	dirInfo, err := inspectDirectory(dir, true)
	if err != nil {
		return fileContents{}, err
	}
	if dirInfo == nil {
		return emptyFile(), nil
	}
	info, err := os.Lstat(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return emptyFile(), nil
	}
	if err != nil {
		return fileContents{}, fileError("inspect", err)
	}
	if err := validateFileInfo(info); err != nil {
		return fileContents{}, err
	}
	f, err := os.Open(s.Path)
	if err != nil {
		return fileContents{}, fileError("open", err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return fileContents{}, fileError("inspect open", err)
	}
	current, err := os.Lstat(s.Path)
	if err != nil {
		return fileContents{}, fileError("reinspect", err)
	}
	if !current.Mode().IsRegular() || !os.SameFile(info, opened) || !os.SameFile(current, opened) {
		return fileContents{}, errors.New("credential file changed while opening")
	}
	currentDir, err := inspectDirectory(dir, false)
	if err != nil || !os.SameFile(dirInfo, currentDir) {
		return fileContents{}, errors.New("credential directory changed while opening")
	}
	if err := validateFileInfo(opened); err != nil {
		return fileContents{}, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return fileContents{}, fileError("read", err)
	}
	if len(data) > maxFileSize {
		return fileContents{}, fmt.Errorf("credential file exceeds maximum size of %d bytes", maxFileSize)
	}
	contents := emptyFile()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&contents); err != nil {
		return fileContents{}, errors.New("parse credential file: invalid YAML")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fileContents{}, errors.New("parse credential file: multiple or invalid YAML documents")
	}
	if contents.Version != 1 {
		return fileContents{}, errors.New("unsupported credential file version")
	}
	if contents.Credentials == nil {
		contents.Credentials = map[string]fileCredential{}
	}
	for id, cred := range contents.Credentials {
		if ValidateID(id) != nil || validateCredential(Credential{Kind: cred.Kind, Secret: cred.Secret}) != nil {
			return fileContents{}, errors.New("credential file contains an invalid credential entry")
		}
	}
	return contents, nil
}

func validateFileInfo(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("credential file is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("credential file permissions grant group or other access")
	}
	if !ownedByEffectiveUser(info) {
		return errors.New("credential file is not owned by the current user")
	}
	return nil
}

func inspectDirectory(path string, missingOK bool) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && missingOK {
		return nil, nil
	}
	if err != nil {
		return nil, fileError("inspect credential directory", err)
	}
	if !info.IsDir() {
		return nil, errors.New("credential directory is not a real directory")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("credential directory permissions grant group or other write access")
	}
	if !ownedByEffectiveUser(info) {
		return nil, errors.New("credential directory is not owned by the current user")
	}
	return info, nil
}

func (s *FileStore) write(contents fileContents) error {
	if err := validatePlaintextPlatform(); err != nil {
		return err
	}
	if filepath.Base(filepath.Clean(s.Path)) == "config.yaml" {
		return errors.New("credential file must be separate from config.yaml")
	}
	dir := filepath.Dir(s.Path)
	var data bytes.Buffer
	enc := yaml.NewEncoder(&data)
	enc.SetIndent(2)
	if err := enc.Encode(contents); err != nil {
		return errors.New("encode credential file")
	}
	if err := enc.Close(); err != nil {
		return errors.New("close credential encoder")
	}
	if data.Len() > maxFileSize {
		return fmt.Errorf("credential file exceeds maximum size of %d bytes", maxFileSize)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fileError("create credential directory", err)
	}
	dirInfo, err := inspectDirectory(dir, false)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return fileError("create temporary credential file", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	fail := func(op string, err error) error {
		_ = f.Close()
		return fileError(op, err)
	}
	if err := f.Chmod(0o600); err != nil {
		return fail("secure temporary credential file", err)
	}
	currentDir, err := inspectDirectory(dir, false)
	if err != nil || !os.SameFile(dirInfo, currentDir) {
		return fail("secure credential directory", errors.New("credential directory changed during write"))
	}
	if _, err := f.Write(data.Bytes()); err != nil {
		return fail("write credential file", err)
	}
	if err := f.Sync(); err != nil {
		return fail("sync credential file", err)
	}
	if err := f.Close(); err != nil {
		return fileError("close credential file", err)
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		return fileError("replace credential file", err)
	}
	currentDir, err = inspectDirectory(dir, false)
	if err != nil || !os.SameFile(dirInfo, currentDir) {
		return fmt.Errorf("%w: credential directory changed after replacement", ErrPersistenceUncertain)
	}
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("%w: sync credential directory", ErrPersistenceUncertain)
	}
	return nil
}

func fileError(operation string, err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		err = linkErr.Err
	}
	return fmt.Errorf("%s: %v", operation, err)
}
