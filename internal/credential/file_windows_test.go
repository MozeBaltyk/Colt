//go:build windows

package credential

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsPlaintextFallbackFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	err := NewFileStore(path).Put("github.com/personal", Credential{Kind: "bearer_token", Secret: "secret"})
	if err == nil || !strings.Contains(err.Error(), "unsupported on Windows") {
		t.Fatalf("Put() = %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("plaintext file created: %v", statErr)
	}
}
