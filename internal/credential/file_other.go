//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package credential

import "os"

func ownedByEffectiveUser(os.FileInfo) bool { return true }
func syncDirectory(string) error            { return nil }
