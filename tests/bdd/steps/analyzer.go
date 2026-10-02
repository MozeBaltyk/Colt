package steps

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
	"github.com/cucumber/godog"
)

func RegisterAnalyzerSteps(ctx *godog.ScenarioContext, w *fixture.World) {
	ctx.Step(`^an analyzed repository containing manifest files$`, func() error {
		for _, name := range []string{"go.mod", "go.sum", "package.json", "Cargo.toml"} {
			if err := os.WriteFile(filepath.Join(w.Dir, name), []byte("bdd-manifest\n"), 0o644); err != nil {
				return err
			}
		}
		return nil
	})

	ctx.Step(`^an analyzed repository containing a secret marker file$`, func() error {
		if err := os.WriteFile(filepath.Join(w.Dir, "go.mod"), []byte("module bdd\n"), 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(w.Dir, "SECRET.txt"), []byte("TOP-SECRET\n"), 0o600)
	})

	ctx.Step(`^an executable script that would fail if executed$`, func() error {
		marker := filepath.Join(w.Dir, "EXECUTED")
		script := "#!/bin/sh\ntouch '" + marker + "'\n"
		return os.WriteFile(filepath.Join(w.Dir, "malicious.sh"), []byte(script), 0o755)
	})

	ctx.Step(`^the repository origin is "([^"]*)"$`, func(origin string) error {
		w.Git.OriginURL = origin
		return nil
	})

	ctx.Step(`^the inventory lists the go, node, and rust ecosystems with their manifest files$`, func() error {
		for _, want := range []string{"name: go", "go.mod", "name: node", "package.json", "name: rust", "Cargo.toml"} {
			if !strings.Contains(w.Out, want) {
				return fmt.Errorf("output missing %q: %s", want, w.Out)
			}
		}
		return nil
	})

	ctx.Step(`^no repository content was executed$`, func() error {
		if _, err := os.Stat(filepath.Join(w.Dir, "EXECUTED")); err == nil {
			return errors.New("repository content was executed")
		}
		return nil
	})

	ctx.Step(`^the inventory does not expose the repository secret or the origin credential$`, func() error {
		for _, secret := range []string{"TOP-SECRET", "secret-token"} {
			if strings.Contains(w.Out, secret) {
				return fmt.Errorf("output leaks %q: %s", secret, w.Out)
			}
		}
		return nil
	})

	ctx.Step(`^the written inventory reproduces the yaml view$`, func() error {
		data, err := os.ReadFile(filepath.Join(w.Dir, "inventory.yaml"))
		if err != nil {
			return err
		}
		if string(data) != w.Out {
			return fmt.Errorf("inventory file != yaml view:\n%s\n---\n%s", data, w.Out)
		}
		return nil
	})
}
