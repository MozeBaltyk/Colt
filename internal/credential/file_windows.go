//go:build windows

package credential

import (
	"errors"
	"os"
)

func ownedByEffectiveUser(os.FileInfo) bool { return false }
func syncDirectory(string) error            { return nil }
func validatePlaintextPlatform() error {
	return errors.New("plaintext credential fallback is unsupported on Windows without user-only ACL validation")
}
