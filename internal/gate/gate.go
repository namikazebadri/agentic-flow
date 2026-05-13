package gate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentflow/core/internal/registry"
)

// Config controls which gate stages run and their thresholds.
type Config struct {
	EnableLint      bool
	EnableTests     bool
	EnableTypeCheck bool
	CustomCommands  []string // extra commands to run, e.g. "go vet ./..."
	WorkspaceDir    string
}

// Gate runs all quality checks and returns results.
type Gate struct {
	cfg Config
}

func New(cfg Config) *Gate {
	return &Gate{cfg: cfg}
}

// Result is the combined outcome of all gate stages.
type Result struct {
	Passed  bool
	Stages  []registry.GateResult
	Summary string
}

// Run executes all configured gate stages sequentially.
// Fails fast: stops at first failing stage.
func (g *Gate) Run(ctx context.Context, deliverableID string) (*Result, error) {
	result := &Result{Passed: true}
	srcDir := filepath.Join(g.cfg.WorkspaceDir, "src")

	stages := g.buildStages(srcDir)

	for _, stage := range stages {
		gr, err := g.runStage(ctx, stage, srcDir)
		if err != nil {
			return nil, fmt.Errorf("gate stage %q: %w", stage.name, err)
		}
		result.Stages = append(result.Stages, gr)

		if !gr.Passed {
			result.Passed = false
			result.Summary = fmt.Sprintf("Stage %q failed:\n%s", stage.name, gr.Output)
			return result, nil // fail fast
		}
	}

	result.Summary = fmt.Sprintf("All %d gate stages passed", len(stages))
	return result, nil
}

type stageSpec struct {
	name    string
	gateKey string // maps to registry.GateResult.Stage
	cmd     string
	args    []string
}

func (g *Gate) buildStages(srcDir string) []stageSpec {
	var stages []stageSpec

	if g.cfg.EnableLint {
		stages = append(stages, detectLintStage(srcDir))
	}
	if g.cfg.EnableTypeCheck {
		stages = append(stages, detectTypeCheckStage(srcDir))
	}
	if g.cfg.EnableTests {
		stages = append(stages, detectTestStage(srcDir))
	}
	for _, cmd := range g.cfg.CustomCommands {
		parts := strings.Fields(cmd)
		if len(parts) == 0 {
			continue
		}
		stages = append(stages, stageSpec{
			name:    "custom:" + parts[0],
			gateKey: "custom",
			cmd:     parts[0],
			args:    parts[1:],
		})
	}

	return stages
}

func (g *Gate) runStage(ctx context.Context, stage stageSpec, srcDir string) (registry.GateResult, error) {
	gr := registry.GateResult{
		RunAt: time.Now(),
		Stage: stage.gateKey,
	}

	cmd := exec.CommandContext(ctx, stage.cmd, stage.args...)
	cmd.Dir = srcDir

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	gr.Output = out.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			gr.ExitCode = exitErr.ExitCode()
		} else {
			// Command not found or couldn't run — not a test failure
			return gr, fmt.Errorf("executing %q: %w", stage.cmd, err)
		}
		gr.Passed = false
	} else {
		gr.Passed = true
		gr.ExitCode = 0
	}

	return gr, nil
}

// detectLintStage picks the right linter based on what exists in srcDir.
func detectLintStage(srcDir string) stageSpec {
	// Go
	if fileExists(filepath.Join(srcDir, "go.mod")) {
		return stageSpec{name: "golint", gateKey: "lint", cmd: "go", args: []string{"vet", "./..."}}
	}
	// Node / TypeScript — eslint
	if fileExists(filepath.Join(srcDir, "package.json")) {
		if fileExists(filepath.Join(srcDir, ".eslintrc.json")) || fileExists(filepath.Join(srcDir, "eslint.config.js")) {
			return stageSpec{name: "eslint", gateKey: "lint", cmd: "npx", args: []string{"eslint", "."}}
		}
	}
	// Python — ruff
	if hasFileWithExt(srcDir, ".py") {
		return stageSpec{name: "ruff", gateKey: "lint", cmd: "ruff", args: []string{"check", "."}}
	}
	// Fallback: no-op (always passes)
	return stageSpec{name: "lint-noop", gateKey: "lint", cmd: "true", args: nil}
}

func detectTypeCheckStage(srcDir string) stageSpec {
	// Go — type checking is part of build
	if fileExists(filepath.Join(srcDir, "go.mod")) {
		return stageSpec{name: "go-build", gateKey: "type_check", cmd: "go", args: []string{"build", "./..."}}
	}
	// TypeScript
	if fileExists(filepath.Join(srcDir, "tsconfig.json")) {
		return stageSpec{name: "tsc", gateKey: "type_check", cmd: "npx", args: []string{"tsc", "--noEmit"}}
	}
	// Python — mypy if available
	if hasFileWithExt(srcDir, ".py") {
		return stageSpec{name: "mypy", gateKey: "type_check", cmd: "mypy", args: []string{"."}}
	}
	return stageSpec{name: "typecheck-noop", gateKey: "type_check", cmd: "true", args: nil}
}

func detectTestStage(srcDir string) stageSpec {
	// Go
	if fileExists(filepath.Join(srcDir, "go.mod")) {
		return stageSpec{name: "go-test", gateKey: "test", cmd: "go", args: []string{"test", "-v", "-race", "./..."}}
	}
	// Node — jest or vitest
	if fileExists(filepath.Join(srcDir, "package.json")) {
		return stageSpec{name: "npm-test", gateKey: "test", cmd: "npm", args: []string{"test", "--", "--passWithNoTests"}}
	}
	// Python — pytest
	if hasFileWithExt(srcDir, ".py") {
		return stageSpec{name: "pytest", gateKey: "test", cmd: "pytest", args: []string{"-v", "--tb=short"}}
	}
	return stageSpec{name: "test-noop", gateKey: "test", cmd: "true", args: nil}
}

// GherkinCoverageCheck verifies that all Gherkin scenarios have corresponding
// step definitions in the generated code. This is a heuristic check.
func GherkinCoverageCheck(gherkinSpec, srcDir string) (bool, string) {
	// Extract scenario titles from Gherkin
	scenarios := extractScenarioTitles(gherkinSpec)
	if len(scenarios) == 0 {
		return true, "no scenarios found in spec"
	}

	// Read all source files
	var allSource strings.Builder
	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		ext := filepath.Ext(path)
		if ext == ".go" || ext == ".ts" || ext == ".js" || ext == ".py" {
			data, _ := os.ReadFile(path)
			allSource.Write(data)
		}
		return nil
	})
	if err != nil {
		return false, fmt.Sprintf("reading source files: %v", err)
	}

	source := allSource.String()
	var missing []string
	for _, scenario := range scenarios {
		// Check for partial keyword match — step defs use scenario titles as strings
		keywords := extractKeywords(scenario)
		found := false
		for _, kw := range keywords {
			if strings.Contains(source, kw) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, scenario)
		}
	}

	if len(missing) > 0 {
		return false, fmt.Sprintf("scenarios without step definitions:\n  - %s",
			strings.Join(missing, "\n  - "))
	}
	return true, "all scenarios appear to have step definitions"
}

func extractScenarioTitles(gherkin string) []string {
	var titles []string
	for _, line := range strings.Split(gherkin, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"Scenario:", "Scenario Outline:"} {
			if strings.HasPrefix(line, prefix) {
				title := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				if title != "" {
					titles = append(titles, title)
				}
			}
		}
	}
	return titles
}

func extractKeywords(scenario string) []string {
	// Extract 2+ word phrases as search keywords
	words := strings.Fields(scenario)
	var keywords []string
	for i := 0; i < len(words)-1; i++ {
		kw := strings.ToLower(words[i] + " " + words[i+1])
		// Filter out common stop words
		if len(kw) > 6 {
			keywords = append(keywords, kw)
		}
	}
	if len(keywords) == 0 {
		keywords = []string{strings.ToLower(scenario)}
	}
	return keywords
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasFileWithExt(dir, ext string) bool {
	found := false
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Ext(path) == ext {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
