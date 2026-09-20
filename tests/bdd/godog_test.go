// Package bdd runs the active deterministic Gherkin scenarios with local
// fixtures only: temp config, temp workdir, stub provider client (or
// loopback TLS servers), and real or stub native git. No real providers,
// no global Git writes.
//
// Scenario state and doubles live in fixture; step definitions live in
// steps (init/resolution) and steps (authentication/transport); this file
// only wires them to godog.
package bdd

import (
	"context"
	"os/exec"
	"testing"

	"github.com/MozeBaltyk/Colt/tests/bdd/fixture"
	"github.com/MozeBaltyk/Colt/tests/bdd/steps"
	"github.com/cucumber/godog"
)

func TestBDD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("native git not on PATH")
	}
	suite := godog.TestSuite{
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			w := &fixture.World{}
			ctx.Before(func(scenarioCtx context.Context, _ *godog.Scenario) (context.Context, error) {
				w.Reset(t)
				var err error
				w.GlobalGit, err = fixture.GitGlobalSnapshot()
				w.BuildApp()
				return scenarioCtx, err
			})
			ctx.After(func(scenarioCtx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				if w.RedirectClose != nil {
					w.RedirectClose()
				}
				return scenarioCtx, fixture.RestoreEnv(w.SavedEnv)
			})
			steps.RegisterInitSteps(ctx, w)
			steps.RegisterAuthSteps(ctx, w)
			steps.RegisterLifecycleSteps(ctx, w)
			steps.RegisterCoreGitSteps(ctx, w)
			steps.RegisterRunSteps(ctx, w)
		},
		Options: &godog.Options{
			Format:      "pretty",
			Paths:       []string{"../../features"},
			Strict:      true,
			Concurrency: 1,
			// Status tags are the only exclusions: every other scenario executes.
			// @integration runs separately against live container backends, and
			// @blackbox scenarios run as PTY/process tests in TestBlackbox
			// (which needs the built binary and a real pseudo-terminal).
			Tags:     "~@planned&&~@unimplemented&&~@integration&&~@blackbox",
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("bdd suite failed")
	}
}
