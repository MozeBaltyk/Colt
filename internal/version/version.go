// Package version exposes the Colt release version.
package version

// Version is injected at release time via
// -ldflags "-X github.com/MozeBaltyk/Colt/internal/version.Version=<tag>".
// Local builds report "devel".
var Version = "devel"
