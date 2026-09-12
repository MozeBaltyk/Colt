package credential

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const maxFileSize = 1 << 20

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
	// ponytail: this mutex is process-local. Put/Delete are Store-completeness
	// and test/enrollment plumbing only; add OS file locking before exposing
	// either operation through a production command.
	mu sync.Mutex
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

func (s *FileStore) Put(id string, cred Credential) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	if strings.TrimSpace(cred.Kind) == "" || cred.Secret == "" {
		return errors.New("refusing to store an empty credential kind or secret")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	contents, err := s.read()
	if err != nil {
		return err
	}
	contents.Credentials[id] = fileCredential{Kind: cred.Kind, Secret: cred.Secret}
	return s.write(contents)
}

func (s *FileStore) Delete(id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	contents, err := s.read()
	if err != nil {
		return err
	}
	if _, ok := contents.Credentials[id]; !ok {
		return ErrNotFound
	}
	delete(contents.Credentials, id)
	return s.write(contents)
}

func emptyFile() fileContents {
	return fileContents{Version: 1, Credentials: map[string]fileCredential{}}
}

func (s *FileStore) read() (fileContents, error) {
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
		if ValidateID(id) != nil || strings.TrimSpace(cred.Kind) == "" || cred.Secret == "" {
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
		return errors.New("credential directory changed after replacement")
	}
	if err := syncDirectory(dir); err != nil {
		return fileError("sync credential directory", err)
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
