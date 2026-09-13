//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package credential

import (
	"errors"
	"os"
)

func ownedByEffectiveUser(os.FileInfo) bool { return true }
func syncDirectory(string) error            { return nil }
func validatePlaintextPlatform() error {
	return errors.New("plaintext credential fallback is unsupported on this platform")
}
