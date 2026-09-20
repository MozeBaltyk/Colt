package steps

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/MozeBaltyk/Colt/internal/config"
	"github.com/MozeBaltyk/Colt/internal/provider"
	templating "github.com/MozeBaltyk/Colt/internal/template"
	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
	"github.com/cucumber/godog"
)

func RegisterTemplateSteps(ctx *godog.ScenarioContext, w *fixture.World) {
	configure := func(parameters map[string]config.TemplateParameter, content string) error {
		w.TemplateSource = filepath.Join(w.Dir, "template-source")
		if err := os.Mkdir(w.TemplateSource, 0o755); err != nil {
			return err
		}
		path := filepath.Join(w.TemplateSource, "README.md")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
		w.TemplateSnapshot, _ = os.ReadFile(path)
		digest, err := templating.Digest(w.TemplateSource)
		if err != nil {
			return err
		}
		p := fixture.WithDefault(fixture.WithTokenEnv(fixture.StdProvider("gitlab", "example-namespace"), fixture.TokenEnv), true)
		w.Providers = map[string]config.Provider{"work": p}
		w.Templates = map[string]config.Template{"service": {
			Default: "2",
			Versions: map[string]config.TemplateVersion{"2": {
				Source: "template-source", Digest: digest, Parameters: parameters,
			}},
		}}
		return w.SaveConfig()
	}

	ctx.Step(`^template "service" version "2" is configured and immutable$`, func() error {
		return configure(nil, "version 2\n")
	})
	ctx.Step(`^I list templates, show "service@2", and select it for initialization$`, func() error {
		hidden := w.TemplateSource + ".hidden"
		if err := os.Rename(w.TemplateSource, hidden); err != nil {
			return err
		}
		w.Run("colt template list")
		if w.RunErr != nil {
			return w.RunErr
		}
		w.TemplateInspection = w.Out
		w.Run("colt template show service@2")
		if w.RunErr != nil {
			return w.RunErr
		}
		w.TemplateInspection += w.Out
		if err := os.Rename(hidden, w.TemplateSource); err != nil {
			return err
		}
		w.Run("colt init demo --template service@2 --local")
		return nil
	})
	ctx.Step(`^configured metadata and the same pinned source are resolved deterministically$`, func() error {
		data, err := os.ReadFile(filepath.Join(w.Dir, "demo", "README.md"))
		if w.RunErr != nil || err != nil || string(data) != "version 2\n" || !strings.Contains(w.TemplateInspection, "service@2") || !strings.Contains(w.TemplateInspection, "Resolved: service@2") {
			return fmt.Errorf("resolution failed: run=%v read=%v data=%q inspection=%q", w.RunErr, err, data, w.TemplateInspection)
		}
		return nil
	})
	ctx.Step(`^no template content is materialized or executed by list or show$`, func() error {
		if strings.Contains(w.TemplateInspection, "version 2") {
			return errors.New("template source content appeared in metadata output")
		}
		return nil // list/show succeeded while the configured source directory was absent.
	})

	ctx.Step(`^a template declares a required string "owner" and a default string "license"$`, func() error {
		license := "MIT"
		w.Interactive = true
		return configure(map[string]config.TemplateParameter{"owner": {Required: true}, "license": {Default: &license}}, "owner={{owner}} license={{license}}\n")
	})
	ctx.Step("^I initialize interactively with `--set owner=platform`$", func() error {
		w.Run("colt init demo --template service --set owner=platform --local")
		return nil
	})
	ctx.Step(`^Colt uses the supplied owner and default license$`, func() error {
		data, err := os.ReadFile(filepath.Join(w.Dir, "demo", "README.md"))
		if w.RunErr != nil || err != nil || string(data) != "owner=platform license=MIT\n" {
			return fmt.Errorf("parameters not applied: run=%v read=%v data=%q", w.RunErr, err, data)
		}
		return nil
	})
	ctx.Step(`^Colt prompts only for unresolved declared parameters$`, func() error {
		if strings.Contains(w.Out, "template parameter") {
			return fmt.Errorf("resolved parameter was prompted: %q", w.Out)
		}
		return nil
	})

	ctx.Step(`^template "service" declares its accepted string parameters$`, func() error {
		return configure(map[string]config.TemplateParameter{"owner": {Required: true}}, "{{owner}}\n")
	})
	ctx.Step(`^noninteractive initialization has (an unknown parameter|a missing required value)$`, func(problem string) error {
		command := "colt init demo --template service --local"
		if problem == "an unknown parameter" {
			command += " --set other=value"
		}
		w.Noninteractive = true
		w.Run(command)
		return nil
	})
	ctx.Step(`^a template source has Git metadata and remotes and configured credentials are available$`, func() error {
		if err := configure(nil, "safe\n"); err != nil {
			return err
		}
		gitDir := filepath.Join(w.TemplateSource, ".git")
		if err := os.Mkdir(gitDir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(gitDir, "config"), []byte("[remote \"origin\"]\nurl=https://credential@example.invalid/x\n"), 0o600)
	})
	ctx.Step(`^its content has (.+)$`, func(unsafeClass string) error {
		readme := filepath.Join(w.TemplateSource, "README.md")
		pin := func(parameters map[string]config.TemplateParameter) error {
			digest, err := templating.Digest(w.TemplateSource)
			if err != nil {
				return err
			}
			version := w.Templates["service"].Versions["2"]
			version.Digest, version.Parameters = digest, parameters
			w.Templates["service"].Versions["2"] = version
			return w.SaveConfig()
		}
		switch unsafeClass {
		case "an arbitrary expression":
			if err := os.WriteFile(readme, []byte("{{owner + command}}\n"), 0o644); err != nil {
				return err
			}
			return pin(map[string]config.TemplateParameter{"owner": {}})
		case "a malformed expression":
			if err := os.WriteFile(readme, []byte("{{owner\n"), 0o644); err != nil {
				return err
			}
			return pin(map[string]config.TemplateParameter{"owner": {}})
		case "an executable hook":
			if err := os.WriteFile(readme, []byte("#!/bin/sh\ntouch ../escape\n"), 0o644); err != nil {
				return err
			}
			return os.Chmod(readme, 0o755)
		case "an unsafe path":
			if err := os.Remove(readme); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(w.TemplateSource, "{{owner}}"), []byte("safe\n"), 0o644); err != nil {
				return err
			}
			escape := "../escape"
			return pin(map[string]config.TemplateParameter{"owner": {Default: &escape}})
		case "a colliding path":
			if err := os.Remove(readme); err != nil {
				return err
			}
			for _, name := range []string{"{{a}}", "{{b}}"} {
				if err := os.WriteFile(filepath.Join(w.TemplateSource, name), []byte("safe\n"), 0o644); err != nil {
					return err
				}
			}
			same := "same"
			return pin(map[string]config.TemplateParameter{"a": {Default: &same}, "b": {Default: &same}})
		case "an unsafe symlink":
			if err := os.Remove(readme); err != nil {
				return err
			}
			return os.Symlink(filepath.Join(w.Dir, "outside"), readme)
		case "a special file":
			if err := os.Remove(readme); err != nil {
				return err
			}
			return syscall.Mkfifo(readme, 0o600)
		default:
			return fmt.Errorf("unknown unsafe template class %q", unsafeClass)
		}
	})
	ctx.Step(`^Colt validates the template$`, func() error { w.Run("colt init demo --template service --local"); return nil })
	ctx.Step(`^source Git metadata, history, remotes, and configured credential material are not imported$`, func() error {
		if w.RunErr == nil || strings.Contains(w.Out, "credential@example.invalid") {
			return fmt.Errorf("unsafe source accepted or disclosed: error=%v output=%q", w.RunErr, w.Out)
		}
		return nil
	})
	ctx.Step(`^unsafe class (.+) fails before materialization or execution$`, func(_ string) error {
		if w.RunErr == nil {
			return errors.New("unsafe template succeeded")
		}
		_, err := os.Lstat(filepath.Join(w.Dir, "demo"))
		if !os.IsNotExist(err) {
			return fmt.Errorf("destination exists: %v", err)
		}
		return nil
	})
	ctx.Step(`^nothing is written outside the destination$`, func() error {
		if _, err := os.Lstat(filepath.Join(w.Dir, "escape")); !os.IsNotExist(err) {
			return fmt.Errorf("outside path exists: %v", err)
		}
		return nil
	})

	ctx.Step(`^a configured template and all declared parameters are valid$`, func() error {
		return configure(map[string]config.TemplateParameter{"owner": {Required: true}}, "owner={{owner}}\n")
	})
	ctx.Step(`^interpolation is deterministic and data-only$`, func() error {
		data, err := os.ReadFile(filepath.Join(w.Dir, "demo", "README.md"))
		if w.RunErr != nil || err != nil || string(data) != "owner=platform\n" {
			return fmt.Errorf("materialization=%q read=%v run=%v", data, err, w.RunErr)
		}
		return nil
	})
	ctx.Step(`^"demo" has fresh Git history, no inherited remote, and one commit of validated content$`, func() error {
		dir := filepath.Join(w.Dir, "demo")
		count, err := fixture.GitOut(dir, "rev-list", "--count", "HEAD")
		remote, remoteErr := fixture.GitOut(dir, "remote")
		tracked, trackedErr := fixture.GitOut(dir, "show", "HEAD:README.md")
		if err != nil || remoteErr != nil || trackedErr != nil || count != "1" || remote != "" || tracked != "owner=platform" {
			return fmt.Errorf("history count=%q remote=%q tracked=%q errors=%v/%v/%v", count, remote, tracked, err, remoteErr, trackedErr)
		}
		return nil
	})
	ctx.Step(`^the template source is unchanged$`, func() error {
		data, err := os.ReadFile(filepath.Join(w.TemplateSource, "README.md"))
		if err != nil || !bytes.Equal(data, w.TemplateSnapshot) {
			return fmt.Errorf("source changed: %q %v", data, err)
		}
		return nil
	})

	ctx.Step(`^validated template content was committed before a later step failed$`, func() error {
		if err := configure(nil, "validated\n"); err != nil {
			return err
		}
		w.Git.Real = false
		w.Git.PushErr = errors.New("native git failed: remote hung up")
		w.Client.GetErr = provider.ErrNotFound
		w.Client.CreateRepo = &provider.Repository{CloneURL: "https://gitlab.com/example-namespace/demo.git"}
		return nil
	})
	ctx.Step(`^Colt reports the initialization failure$`, func() error { w.Run("colt init demo --template service"); return nil })
	ctx.Step(`^completed local work is preserved and reported under shared initialization safety rules$`, func() error {
		result := w.Out + fmt.Sprint(w.RunErr)
		data, err := os.ReadFile(filepath.Join(w.Dir, "example-namespace", "demo", "README.md"))
		if w.RunErr == nil || err != nil || string(data) != "validated\n" || !strings.Contains(result, "local state preserved") || !strings.Contains(w.Out, "Local state: initial commit") {
			return fmt.Errorf("partial state error=%v data=%q read=%v report=%q", w.RunErr, data, err, result)
		}
		return nil
	})
}
