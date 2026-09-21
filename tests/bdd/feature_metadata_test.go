package bdd

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

var updateBDDCoverage = flag.Bool("update-bdd-coverage", false, "write .local/bdd-coverage.md")

var requirementTag = regexp.MustCompile(`^[A-Z]+(?:-[A-Z]+)*-[0-9]+$`)

var specRequirement = regexp.MustCompile(`^\|\s*` + "`" + `([A-Z]+(?:-[A-Z]+)*-[0-9]+)` + "`" + `\s*\|`)

type featureScenario struct {
	file    string
	name    string
	line    int
	tags    []string
	outline bool
}

type requirementCoverage struct {
	active, planned, unimplemented []featureScenario
}

type specRequirementInfo struct {
	id           string
	verification string
	specFile     string
}

func TestBDDTagHygiene(t *testing.T) {
	scenarios := loadFeatureScenarios(t)
	if err := validateFeatureScenarios(scenarios); err != nil {
		t.Fatal(err)
	}
}

func TestBDDRequirementIndex(t *testing.T) {
	scenarios := loadFeatureScenarios(t)
	if err := validateFeatureScenarios(scenarios); err != nil {
		t.Fatal(err)
	}
	index := buildRequirementIndex(scenarios)
	if len(index) == 0 {
		t.Fatal("no requirement coverage found")
	}
	if *updateBDDCoverage {
		path := filepath.Join(repoRoot(), ".local", "bdd-coverage.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create BDD coverage directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(renderRequirementCoverage(index)), 0o644); err != nil {
			t.Fatalf("write BDD coverage: %v", err)
		}
	}
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func loadFeatureScenarios(t *testing.T) []featureScenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(), "features", "*.feature"))
	if err != nil || len(files) == 0 {
		t.Fatalf("find feature files: %v", err)
	}
	sort.Strings(files)
	var scenarios []featureScenario
	for _, path := range files {
		parsed, err := parseFeatureFile(path)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		scenarios = append(scenarios, parsed...)
	}
	return scenarios
}

func parseFeatureFile(path string) ([]featureScenario, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var scenarios []featureScenario
	var featureTags, pendingTags []string
	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(text, "@"):
			pendingTags = append(pendingTags, strings.Fields(text)...)
		case strings.HasPrefix(text, "Feature:"):
			featureTags = append([]string(nil), pendingTags...)
			pendingTags = nil
		case strings.HasPrefix(text, "Rule:"):
			return nil, fmt.Errorf("line %d: Rule blocks are not supported; tag scenarios directly", line)
		case strings.HasPrefix(text, "Scenario Outline:"):
			scenarios = append(scenarios, featureScenario{
				file: filepath.Base(path), name: strings.TrimSpace(strings.TrimPrefix(text, "Scenario Outline:")),
				line: line, tags: append(append([]string(nil), featureTags...), pendingTags...), outline: true,
			})
			pendingTags = nil
		case strings.HasPrefix(text, "Scenario:"):
			scenarios = append(scenarios, featureScenario{
				file: filepath.Base(path), name: strings.TrimSpace(strings.TrimPrefix(text, "Scenario:")),
				line: line, tags: append(append([]string(nil), featureTags...), pendingTags...),
			})
			pendingTags = nil
		case strings.HasPrefix(text, "Examples:"):
			// An outline is indexed once; example rows are data, not separate requirements.
			if len(pendingTags) != 0 {
				return nil, fmt.Errorf("line %d: tagged Examples are not supported; tag the Scenario Outline directly", line)
			}
			pendingTags = nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return scenarios, nil
}

func TestParseFeatureFileRejectsUnsupportedTagScopes(t *testing.T) {
	tests := []struct {
		name, feature, want string
	}{
		{"rule", "Feature: test\n  @CORE-TEST-001\n  Rule: grouped\n", "Rule blocks are not supported"},
		{"tagged examples", "Feature: test\n  @CORE-TEST-001\n  Scenario Outline: example\n    When x is <x>\n    @planned\n    Examples:\n      | x |\n      | 1 |\n", "tagged Examples are not supported"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.feature")
			if err := os.WriteFile(path, []byte(tc.feature), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := parseFeatureFile(path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseFeatureFile error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func validateFeatureScenarios(scenarios []featureScenario) error {
	var problems []string
	for _, scenario := range scenarios {
		requirements := 0
		planned, unimplemented, explicitActive := false, false, false
		for _, rawTag := range scenario.tags {
			tag := strings.TrimPrefix(rawTag, "@")
			switch tag {
			case "planned":
				planned = true
			case "unimplemented":
				unimplemented = true
			case "active":
				explicitActive = true
			default:
				if requirementTag.MatchString(tag) {
					requirements++
				}
			}
		}
		ref := fmt.Sprintf("%s:%d %s", scenario.file, scenario.line, scenario.name)
		if requirements == 0 {
			problems = append(problems, ref+": missing requirement-ID tag")
		}
		if planned && unimplemented {
			problems = append(problems, ref+": has both @planned and @unimplemented")
		}
		if explicitActive {
			problems = append(problems, ref+": active lifecycle is represented by having no status tag, not @active")
		}
	}
	if len(problems) != 0 {
		return fmt.Errorf("feature tag hygiene:\n%s", strings.Join(problems, "\n"))
	}
	return nil
}

func TestBDDTraceability(t *testing.T) {
	scenarios := loadFeatureScenarios(t)
	if err := validateFeatureScenarios(scenarios); err != nil {
		t.Fatal(err)
	}
	specs := loadSpecRequirements(t)
	if err := validateTraceability(scenarios, specs); err != nil {
		t.Fatal(err)
	}
}

func loadSpecRequirements(t *testing.T) map[string]specRequirementInfo {
	t.Helper()
	specDir := filepath.Join(repoRoot(), "docs", "specs")
	files, err := filepath.Glob(filepath.Join(specDir, "*.md"))
	if err != nil {
		t.Fatalf("find spec files: %v", err)
	}
	sort.Strings(files)
	specs := map[string]specRequirementInfo{}
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		scanner := bufio.NewScanner(f)
		for line := 1; scanner.Scan(); line++ {
			text := strings.TrimSpace(scanner.Text())
			m := specRequirement.FindStringSubmatch(text)
			if m == nil {
				continue
			}
			id := m[1]
			if _, exists := specs[id]; exists {
				t.Fatalf("duplicate requirement ID %q", id)
			}
			rest := text[strings.Index(text, "`|")+2:]
			verif := ""
			if idx := strings.Index(rest, "|"); idx > 0 {
				verif = strings.TrimSpace(rest[idx+1:])
			}
			specs[id] = specRequirementInfo{id: id, verification: verif, specFile: path}
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		f.Close()
	}
	return specs
}

func validateTraceability(scenarios []featureScenario, specs map[string]specRequirementInfo) error {
	var problems []string

	// Build the set of requirement IDs referenced in features.
	featureRefs := map[string]bool{}
	for _, s := range scenarios {
		for _, rawTag := range s.tags {
			tag := strings.TrimPrefix(rawTag, "@")
			if !requirementTag.MatchString(tag) {
				continue
			}
			featureRefs[tag] = true
		}
	}

	// 1. Feature references an undefined requirement ID.
	for ref := range featureRefs {
		if _, ok := specs[ref]; !ok {
			problems = append(problems, fmt.Sprintf("feature references undefined requirement %q", ref))
		}
	}

	// 2. Acceptance-backed normative requirement with no corresponding feature/scenario.
	//    Skip requirements verified only by architecture/security review, and M6 Analyzer.
	for id, info := range specs {
		if strings.HasPrefix(id, "CORE-ARCH") || strings.HasPrefix(id, "CORE-MODEL") ||
			strings.HasPrefix(id, "CORE-PROVIDER-007") || strings.HasPrefix(id, "CORE-GIT-002") ||
			strings.HasPrefix(id, "CORE-CREDENTIAL-004") || strings.HasPrefix(id, "CORE-CREDENTIAL-007") ||
			strings.HasPrefix(id, "CORE-NAMESPACE-001") {
			continue
		}
		if strings.HasPrefix(id, "HEALTH-") || strings.HasPrefix(id, "TEMPLATE-") {
			if strings.Contains(info.verification, "Architecture review") || strings.Contains(info.verification, "Security review") {
				continue
			}
		}
		if strings.HasPrefix(id, "CORE-") && (strings.Contains(info.verification, "Architecture review") || strings.Contains(info.verification, "Security review") || strings.Contains(info.verification, "Security integration test") || strings.Contains(info.verification, "Specification and implementation review")) {
			continue
		}
		if _, ok := featureRefs[id]; !ok {
			problems = append(problems, fmt.Sprintf("acceptance-backed requirement %q has no feature/scenario", id))
		}
	}

	// 4. Milestone-state mismatches: planned M6 scenarios must stay planned.
	for _, s := range scenarios {
		status := "active"
		for _, tag := range s.tags {
			if tag == "@planned" {
				status = "planned"
			} else if tag == "@unimplemented" {
				status = "unimplemented"
			}
		}
		if status == "planned" {
			continue
		}
		for _, rawTag := range s.tags {
			tag := strings.TrimPrefix(rawTag, "@")
			if !requirementTag.MatchString(tag) {
				continue
			}
			info, ok := specs[tag]
			if !ok {
				continue
			}
			milestone := milestoneForSpec(info.specFile)
			if milestone == "M6" && status != "planned" {
				problems = append(problems, fmt.Sprintf("%s:%d %s: planned %s scenario missing @planned", s.file, s.line, s.name, tag))
			}
		}
	}

	if len(problems) != 0 {
		return fmt.Errorf("traceability:\n%s", strings.Join(problems, "\n"))
	}
	return nil
}

func milestoneForSpec(specFile string) string {
	base := filepath.Base(specFile)
	switch base {
	case "00-core.md", "01-project-init.md":
		return "M1"
	case "02-project-lifecycle.md":
		return "M2"
	case "03-template-init.md":
		return "M3"
	case "04-workspace.md":
		return "M4"
	case "05-project-health.md":
		return "M5"
	case "06-analyzer.md":
		return "M6"
	default:
		return "M1"
	}
}

func buildRequirementIndex(scenarios []featureScenario) map[string]requirementCoverage {
	index := map[string]requirementCoverage{}
	for _, scenario := range scenarios {
		status := "active"
		for _, tag := range scenario.tags {
			if tag == "@planned" {
				status = "planned"
			} else if tag == "@unimplemented" {
				status = "unimplemented"
			}
		}
		for _, rawTag := range scenario.tags {
			requirement := strings.TrimPrefix(rawTag, "@")
			if !requirementTag.MatchString(requirement) {
				continue
			}
			coverage := index[requirement]
			switch status {
			case "planned":
				coverage.planned = append(coverage.planned, scenario)
			case "unimplemented":
				coverage.unimplemented = append(coverage.unimplemented, scenario)
			default:
				coverage.active = append(coverage.active, scenario)
			}
			index[requirement] = coverage
		}
	}
	return index
}

func renderRequirementCoverage(index map[string]requirementCoverage) string {
	type coverageRef struct {
		status   string
		scenario featureScenario
	}
	requirements := make([]string, 0, len(index))
	for requirement := range index {
		requirements = append(requirements, requirement)
	}
	sort.Strings(requirements)

	var out strings.Builder
	out.WriteString("# BDD Requirement Coverage\n\nGenerated by `just bdd-coverage`. Scenario outlines count once, independent of example rows.\n\n")
	out.WriteString("| Requirement | Active | Planned | Unimplemented | Scenarios |\n")
	out.WriteString("|---|---:|---:|---:|---|\n")
	for _, requirement := range requirements {
		coverage := index[requirement]
		refs := make([]coverageRef, 0, len(coverage.active)+len(coverage.planned)+len(coverage.unimplemented))
		for _, scenario := range coverage.active {
			refs = append(refs, coverageRef{"active", scenario})
		}
		for _, scenario := range coverage.planned {
			refs = append(refs, coverageRef{"planned", scenario})
		}
		for _, scenario := range coverage.unimplemented {
			refs = append(refs, coverageRef{"unimplemented", scenario})
		}
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].scenario.file == refs[j].scenario.file {
				return refs[i].scenario.line < refs[j].scenario.line
			}
			return refs[i].scenario.file < refs[j].scenario.file
		})
		links := make([]string, 0, len(refs))
		for _, ref := range refs {
			scenario := ref.scenario
			kind := "Scenario"
			if scenario.outline {
				kind = "Outline"
			}
			links = append(links, fmt.Sprintf("**%s** [%s:%d](../features/%s#L%d) %s: %s", ref.status, scenario.file, scenario.line, scenario.file, scenario.line, kind, strings.ReplaceAll(scenario.name, "|", "\\|")))
		}
		fmt.Fprintf(&out, "| %s | %d | %d | %d | %s |\n", requirement, len(coverage.active), len(coverage.planned), len(coverage.unimplemented), strings.Join(links, "<br>"))
	}
	return out.String()
}
