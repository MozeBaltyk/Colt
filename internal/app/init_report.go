package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

const (
	colorSuccess = "32"
	colorFailed  = "31"
	colorSkipped = "33"
)

var stepStateNames = map[string]string{
	"success": "succeeded",
	"failed":  "failed",
	"skipped": "skipped",
}

type initStep struct {
	name, state string
}

type initReport struct {
	project, provider, transport, local, remote, cause, recovery string
	steps                                                        []initStep
	current                                                      int
	commit                                                       string
	color                                                        bool
	stepIndex                                                    map[string]int
}

func newInitReport(project string, local bool) *initReport {
	names := []string{"Preflight"}
	if local {
		names = append(names, "Initialize local repository", "Set repository-local identity", "Create initial commit")
	}
	steps := make([]initStep, len(names))
	stepIndex := make(map[string]int, len(names))
	for i, name := range names {
		steps[i] = initStep{name: name}
		stepIndex[name] = i
	}
	return &initReport{project: project, steps: steps, current: -1, remote: "not applicable", stepIndex: stepIndex}
}

func (r *initReport) remoteSteps(https, authenticate bool) {
	names := []string{"Preflight", "Resolve provider API credential"}
	if authenticate {
		names = append(names, "Authenticate provider API")
	}
	names = append(names, "Look up remote repository", "Create remote repository", "Clone remote repository", "Set repository-local identity", "Create initial commit")
	if https {
		names = append(names, "Configure HTTPS credential helper")
	}
	names = append(names, "Push initial commit")
	r.steps = make([]initStep, len(names))
	r.stepIndex = make(map[string]int, len(names))
	for i, name := range names {
		r.steps[i] = initStep{name: name}
		r.stepIndex[name] = i
	}
}

func (r *initReport) addTemplateStep() {
	for i, step := range r.steps {
		if step.name != "Set repository-local identity" {
			continue
		}
		r.steps = append(r.steps, initStep{})
		copy(r.steps[i+1:], r.steps[i:])
		r.steps[i] = initStep{name: "Materialize template"}
		break
	}
	r.stepIndex = make(map[string]int, len(r.steps))
	for i, step := range r.steps {
		r.stepIndex[step.name] = i
	}
}

func (r *initReport) begin(name string) {
	if i, ok := r.stepIndex[name]; ok {
		r.current = i
	}
}

func (r *initReport) succeeded() {
	if r.current >= 0 {
		r.steps[r.current].state = "success"
	}
}

func (r *initReport) invalidInput(err error) error {
	r.cause = "invalid command input"
	r.recovery = safeReportText(err.Error())
	return err
}

func (r *initReport) fail(err error) error {
	if r.current < 0 {
		r.current = 0
	}
	r.steps[r.current].state = "failed"
	for i := r.current + 1; i < len(r.steps); i++ {
		r.steps[i].state = "skipped"
	}
	cause, advice := initCause(err, r.transport, r.steps[r.current].name)
	r.cause = cause
	r.recovery = advice
	var p *partialError
	if errors.As(err, &p) {
		if p.remote != "" {
			r.remote = safeReportText(p.remote)
		}
		if p.local != "" && r.local == "" {
			if _, statErr := os.Lstat(p.local); statErr == nil {
				r.local = "destination preserved at " + p.local
			} else {
				r.local = "not created"
			}
		}
		if p.recovery != "" {
			r.recovery = safeReportText(p.recovery) + "; " + advice
		}
		return reportedError{message: fmt.Sprintf("partial failure at %s: local state preserved; remote state: %s; recovery: %s; cause: %s", safeReportText(p.step), r.remote, r.recovery, cause)}
	}
	if r.current <= 1 {
		return reportedError{message: safeReportText(err.Error())}
	}
	return reportedError{message: "colt init failed: " + cause}
}

func (r *initReport) write(w io.Writer) {
	fmt.Fprintf(w, "Initialize %s\n", safeReportText(r.project))
	for _, step := range r.steps {
		marker, color := "·", ""
		switch step.state {
		case "success":
			marker, color = "✓", colorSuccess
		case "failed":
			marker, color = "✗", colorFailed
		case "skipped":
			marker, color = "!", colorSkipped
		}
		if r.color && color != "" {
			marker = "\x1b[" + color + "m" + marker + "\x1b[0m"
		}
		state := stepStateNames[step.state]
		fmt.Fprintf(w, "  %s %s: %s\n", marker, step.name, state)
	}
	if r.provider != "" {
		fmt.Fprintf(w, "  Provider: %s\n", safeReportText(r.provider))
	}
	if r.local != "" {
		fmt.Fprintf(w, "  Local state: %s\n", safeReportText(r.local))
	}
	if r.remote != "" {
		fmt.Fprintf(w, "  Remote state: %s\n", safeReportText(r.remote))
	}
	if r.recovery != "" {
		fmt.Fprintf(w, "  Cause: %s\n", r.cause)
		fmt.Fprintf(w, "  Recovery: %s\n", safeReportText(r.recovery))
	}
}

type reportedError struct{ message string }

func (e reportedError) Error() string { return e.message }

// ErrorReported says the command already emitted its complete human error.
func ErrorReported(err error) bool {
	var reported reportedError
	return errors.As(err, &reported)
}

type partialError struct {
	step, local, remote, recovery string
	cause                         error
}

func (e *partialError) Error() string {
	return fmt.Sprintf("partial failure at %s: local state: %s; remote state: %s; recovery: %s", e.step, e.local, e.remote, e.recovery)
}
func (e *partialError) Unwrap() error { return e.cause }

func initCause(err error, transport, step string) (string, string) {
	var evidence strings.Builder
	for err != nil {
		evidence.WriteString(err.Error())
		evidence.WriteByte('\n')
		err = errors.Unwrap(err)
	}
	text := strings.ToLower(evidence.String())
	switch {
	case (strings.Contains(text, "credential helper") || strings.Contains(text, "git-credential")) &&
		(strings.Contains(text, "not found") || strings.Contains(text, "no such file") || strings.Contains(text, "not a git command")):
		return "Git credential helper unavailable", "verify that the Colt executable used to start this command still exists, then retry"
	case strings.Contains(text, "permission denied (publickey)") || strings.Contains(text, "public key authentication failed"):
		return "SSH public-key authentication denied", "verify the selected SSH key and repository access, then retry"
	case strings.Contains(text, "host key verification failed") || strings.Contains(text, "remote host identification has changed"):
		return "SSH host-key verification failed", "verify the provider host key through a trusted channel, update known_hosts safely, then retry"
	case (step == "Resolve provider API credential" || step == "Authenticate provider API" || step == "Look up remote repository" || step == "Create remote repository") && (strings.Contains(text, "bad credentials") || strings.Contains(text, "authentication failed") || strings.Contains(text, "authentication required") || strings.Contains(text, "http 401")):
		return "provider authentication rejected", "run 'colt auth status' and re-authenticate the selected provider if its credential is rejected"
	case strings.Contains(text, "could not resolve host") || strings.Contains(text, "connection refused") || strings.Contains(text, "connection timed out") || strings.Contains(text, "network is unreachable") || strings.Contains(text, "no route to host") || strings.Contains(text, "remote hung up"):
		return "connectivity failure", "check provider connectivity, then retry the failed operation"
	default:
		return "unknown failure", "inspect the preserved state and retry after correcting the reported operation"
	}
}

func safeReportText(text string) string {
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, text)
	if len(text) > 512 {
		text = text[:512] + "…"
	}
	return text
}

func outputIsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
