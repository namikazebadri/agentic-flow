package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentflow/core/internal/agent"
	"github.com/agentflow/core/internal/config"
	"github.com/agentflow/core/internal/gate"
	"github.com/agentflow/core/internal/llm"
	"github.com/agentflow/core/internal/pipeline"
	"github.com/agentflow/core/internal/registry"
	"github.com/agentflow/core/internal/reporter"
)

const usage = `agentflow — Agentic AI Development Pipeline

Usage:
  agentflow init   [--name <name>]        Scaffold a new project here
  agentflow prd    --new --name <slug>    Interactive PRD builder (adaptive interview)
  agentflow run    [--prd <file>]         Run pipeline for a PRD file
  agentflow status [--prd <slug|file>]    Show status (all PRDs or one)
  agentflow list                          List all PRD runs in this project

Project layout after 'init':
  my-project/
  ├── project.json
  ├── docs/
  │   ├── prd-auth.md              ← written by 'agentflow prd --new'  
  │   ├── prd-payment.md
  │   └── deliverables/
  │       ├── prd-auth/            ← Gherkin specs, named by sequence
  │       │   ├── 001-user-model.feature
  │       │   └── 002-login-endpoint.feature
  │       └── prd-payment/
  │           └── 001-payment-schema.feature
  └── src/                         ← generated code (all PRDs share one src/)

Options for 'prd --new':
  --name      PRD slug, e.g. "auth" → writes docs/prd-auth.md (required)
  --config    Path to agentflow config (default: ~/.agentflow/config.json)
  --verbose   Show agent reasoning between questions

Options for 'run':
  --prd       PRD file to run, e.g. docs/prd-auth.md
              (default: prd_file in project.json)
  --config    Path to agentflow config (default: ~/.agentflow/config.json)
  --no-color  Disable colored output
  --verbose   Enable verbose LLM token logging

Options for 'status':
  --prd       Filter by PRD slug or file, e.g. prd-auth or docs/prd-auth.md

Environment variables:
  AGENTFLOW_PROVIDER    Override active LLM provider
  ANTHROPIC_API_KEY     API key for Anthropic
  OPENAI_API_KEY        API key for OpenAI
`

// ProjectConfig is the per-project config stored in project.json.
type ProjectConfig struct {
	Name          string `json:"name"`
	PRDFile       string `json:"prd_file"`
	TechStackFile string `json:"tech_stack_file"`
	StandardsFile string `json:"standards_file"`
	DocsDir       string `json:"docs_dir"`
	SrcDir        string `json:"src_dir"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "init":
		cmdInit(os.Args[2:])
	case "prd":
		cmdPRD(os.Args[2:])
	case "run":
		cmdRun(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	case "list":
		cmdList(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
}

// ── init ──────────────────────────────────────────────────────────────────────

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	name := fs.String("name", "", "Project name (default: current directory name)")
	_ = fs.Parse(args)

	cwd, err := os.Getwd()
	must(err, "getting working directory")

	projectName := *name
	if projectName == "" {
		projectName = filepath.Base(cwd)
	}

	for _, d := range []string{"docs/deliverables", "src"} {
		must(os.MkdirAll(d, 0o755), "creating "+d)
	}

	writeJSONFile("project.json", ProjectConfig{
		Name:          projectName,
		PRDFile:       "docs/prd.md",
		TechStackFile: "docs/tech-stack.md",
		StandardsFile: "docs/standards.json",
		DocsDir:       "docs",
		SrcDir:        "src",
	})

	writeTextFile("docs/prd.md", "# "+projectName+`

## Problem Statement
<!-- Describe the business problem being solved -->

## User Stories
- As a [user type], I want to [action] so that [value]

## Acceptance Criteria
<!-- Be specific and measurable. Examples: -->
<!-- - Login with valid credentials returns JWT within 500ms -->
<!-- - Invalid email returns HTTP 422 with error.code = VALIDATION_ERROR -->

## Out of Scope
<!-- What is explicitly NOT included -->

## Non-Functional Requirements
<!-- Performance targets, security requirements, scalability constraints -->
`)

	writeTextFile("docs/tech-stack.md", `# Tech Stack

## Language & Runtime
- Language:
- Version:

## Frameworks & Libraries
-

## Database
-

## Testing
-
`)

	writeTextFile("docs/standards.json", `[
  {
    "id": "S-001",
    "category": "error_handling",
    "rule": "Every error must be handled or propagated with context",
    "example": "return fmt.Errorf(\"creating user: %w\", err)",
    "anti_example": "result, _ := doSomething()"
  },
  {
    "id": "S-002",
    "category": "testing",
    "rule": "Every exported function must have at least one test"
  }
]
`)

	writeTextFile(".gitignore", "src/\n.agentflow/\n*.tmp\n")

	fmt.Printf("\n✓ Project '%s' initialized.\n\n", projectName)
	fmt.Println("Next steps:")
	fmt.Println("  1. Edit docs/prd.md          — write your PRD")
	fmt.Println("  2. Edit docs/tech-stack.md   — describe your tech stack")
	fmt.Println("  3. Edit docs/standards.json  — set coding standards (optional)")
	fmt.Println("  4. agentflow run")
	fmt.Println()
}

// ── prd ───────────────────────────────────────────────────────────────────────

func cmdPRD(args []string) {
	fs := flag.NewFlagSet("prd", flag.ExitOnError)
	newFlag := fs.Bool("new", false, "Start a new PRD interview")
	name := fs.String("name", "", "PRD slug, e.g. 'auth' → docs/prd-auth.md")
	cfgFile := fs.String("config", defaultConfigPath(), "Path to agentflow config JSON")
	verbose := fs.Bool("verbose", false, "Show agent reasoning between questions")
	_ = fs.Parse(args)

	if !*newFlag {
		fmt.Fprintln(os.Stderr, "Usage: agentflow prd --new --name <slug>")
		os.Exit(1)
	}
	if *name == "" {
		fmt.Fprintln(os.Stderr, "error: --name is required. Example: agentflow prd --new --name auth")
		os.Exit(1)
	}

	// Load project config
	pc, err := loadProjectConfig("project.json")
	must(err, "loading project.json — run 'agentflow init' first")

	docsDir := orDefault(pc.DocsDir, "docs")
	must(os.MkdirAll(docsDir, 0o755), "creating docs dir")

	// Output path: docs/prd-{name}.md
	slug := "prd-" + strings.TrimPrefix(*name, "prd-")
	outputPath := filepath.Join(docsDir, slug+".md")

	// Check if file already exists
	if _, err := os.Stat(outputPath); err == nil {
		fmt.Printf("⚠  File %s sudah ada.\n", outputPath)
		fmt.Print("   Lanjutkan dan timpa? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		ans, _ := reader.ReadString('\n')
		ans = strings.TrimSpace(strings.ToLower(ans))
		if ans != "y" && ans != "yes" {
			fmt.Println("Dibatalkan.")
			return
		}
	}

	// Load agentflow config for LLM
	cfg, err := config.Load(*cfgFile)
	must(err, "loading agentflow config: "+*cfgFile)
	if *verbose {
		cfg.Pipeline.Verbose = true
	}

	// Build project context for the agent
	projectCtx := buildProjectContext(pc, docsDir)

	// Build the interview agent
	provider, err := buildProvider(cfg)
	must(err, "building LLM provider")
	base := agent.NewBase(provider, cfg.Pipeline.PromptsDir, cfg.Pipeline.MaxRetries, cfg.Pipeline.Verbose)
	interviewAgent := agent.NewPRDInterviewAgent(base)

	// Feature display name from slug
	featureName := strings.ReplaceAll(strings.TrimPrefix(slug, "prd-"), "-", " ")
	featureName = strings.Title(featureName)

	// Run the interview
	result, err := interviewAgent.RunInterview(
		context.Background(),
		featureName,
		projectCtx,
		*verbose,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError during interview: %v\n", err)
		os.Exit(1)
	}

	// Write PRD to docs/
	must(os.WriteFile(outputPath, []byte(result.PRDContent), 0o644), "writing PRD file")

	fmt.Println()
	fmt.Printf("\033[1;32m✓ PRD selesai!\033[0m\n")
	fmt.Printf("  File    : %s\n", outputPath)
	fmt.Printf("  Turns   : %d pertanyaan\n", result.Turns)
	fmt.Printf("  Duration: %s\n", result.Duration.Round(1e9))
	fmt.Println()
	fmt.Println("Langkah berikutnya:")
	fmt.Printf("  agentflow run --prd %s\n", outputPath)
}

// buildProjectContext assembles background info for the interview agent.
func buildProjectContext(pc *ProjectConfig, docsDir string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Project: %s\n", pc.Name))

	// Tech stack
	if pc.TechStackFile != "" {
		if data, err := os.ReadFile(pc.TechStackFile); err == nil {
			sb.WriteString("\nTech Stack:\n")
			sb.Write(data)
		}
	}

	// Existing PRD files (for context — avoid overlap)
	entries, _ := os.ReadDir(docsDir)
	var existingPRDs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "prd-") && strings.HasSuffix(e.Name(), ".md") {
			existingPRDs = append(existingPRDs, e.Name())
		}
	}
	if len(existingPRDs) > 0 {
		sb.WriteString("\nExisting PRDs (already covered, avoid overlap):\n")
		for _, p := range existingPRDs {
			sb.WriteString("  - " + p + "\n")
		}
	}

	// Coding standards summary
	if pc.StandardsFile != "" {
		if data, err := os.ReadFile(pc.StandardsFile); err == nil && len(data) < 2000 {
			sb.WriteString("\nCoding Standards:\n")
			sb.Write(data)
		}
	}

	return sb.String()
}

// ── run ───────────────────────────────────────────────────────────────────────

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgFile := fs.String("config", defaultConfigPath(), "Path to agentflow config JSON")
	prdFlag := fs.String("prd", "", "PRD file to run (overrides project.json prd_file)")
	noColor := fs.Bool("no-color", false, "Disable colored output")
	verbose := fs.Bool("verbose", false, "Enable verbose logging")
	_ = fs.Parse(args)

	pc, err := loadProjectConfig("project.json")
	must(err, "loading project.json — run 'agentflow init' first")

	// --prd flag overrides project.json default
	prdFile := pc.PRDFile
	if *prdFlag != "" {
		prdFile = *prdFlag
	}

	docsDir := orDefault(pc.DocsDir, "docs")
	srcDir := orDefault(pc.SrcDir, "src")

	prdContent, err := os.ReadFile(prdFile)
	must(err, "reading PRD: "+prdFile)

	techStack := ""
	if data, err := os.ReadFile(pc.TechStackFile); err == nil {
		techStack = string(data)
	}

	cfg, err := config.Load(*cfgFile)
	must(err, "loading agentflow config: "+*cfgFile)
	if *verbose {
		cfg.Pipeline.Verbose = true
	}

	rep := reporter.NewConsole(*noColor)
	reg, err := registry.New(filepath.Join(".agentflow", "registry"))
	must(err, "initializing registry")

	standards := loadStandards(pc.StandardsFile)

	provider, err := buildProvider(cfg)
	must(err, "building LLM provider")

	base := agent.NewBase(provider, cfg.Pipeline.PromptsDir, cfg.Pipeline.MaxRetries, cfg.Pipeline.Verbose)

	g := gate.New(gate.Config{
		EnableLint:      cfg.Gate.EnableLint,
		EnableTests:     cfg.Gate.EnableTests,
		EnableTypeCheck: cfg.Gate.EnableTypeCheck,
		CustomCommands:  cfg.Gate.CustomCommands,
		WorkspaceDir:    srcDir,
	})

	pl := pipeline.New(
		reg,
		agent.NewPRDBuilderAgent(base),
		agent.NewDecomposerAgent(base, cfg),
		agent.NewImplementorAgent(base),
		g,
		rep,
		cfg.Pipeline.MaxRetries,
		cfg.Pipeline.Verbose,
		cfg,
	)

	cwd, _ := os.Getwd()
	run, err := pl.Run(context.Background(), pipeline.Options{
		ProjectName:  pc.Name,
		PRDFile:      prdFile,
		PRDContent:   string(prdContent),
		WorkspaceDir: cwd,
		DocsDir:      docsDir,
		SrcDir:       srcDir,
		TechStack:    techStack,
		MaxRetries:   cfg.Pipeline.MaxRetries,
		Standards:    standards,
	})

	if err != nil {
		rep.Error("Pipeline failed: %v", err)
		if run != nil {
			rep.Info("Run 'agentflow status' to inspect")
		}
		os.Exit(1)
	}

	rep.Success("Code → ./%s/  |  Specs → ./%s/deliverables/%s/", srcDir, docsDir, run.Slug)
}

// ── status ────────────────────────────────────────────────────────────────────

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	prdFlag := fs.String("prd", "", "Filter by PRD slug or file (e.g. prd-auth or docs/prd-auth.md)")
	_ = fs.Parse(args)

	reg, err := registry.New(filepath.Join(".agentflow", "registry"))
	must(err, "initializing registry")

	runs, err := reg.ListPRDRuns()
	must(err, "listing PRD runs")

	if len(runs) == 0 {
		fmt.Println("No runs found. Run 'agentflow run' first.")
		return
	}

	// Filter by --prd if provided
	if *prdFlag != "" {
		slug := prdSlugFromArg(*prdFlag)
		var filtered []*registry.PRDRun
		for _, r := range runs {
			if r.Slug == slug {
				filtered = append(filtered, r)
			}
		}
		if len(filtered) == 0 {
			fmt.Printf("No runs found for PRD %q\n", *prdFlag)
			return
		}
		runs = filtered
	}

	for _, run := range runs {
		fmt.Printf("\n┌─ [%s] %s\n", run.Slug, run.PRDFile)
		fmt.Printf("│  ID: %s  Status: %s  Score: %.2f  Created: %s\n",
			run.ID, run.Status, run.QualityScore,
			run.CreatedAt.Format("2006-01-02 15:04"))

		deliverables, _ := reg.ListDeliverables(run.ID)
		if len(deliverables) == 0 {
			fmt.Println("│  No deliverables yet.")
			continue
		}

		fmt.Printf("│  %-20s %-36s %-12s %-5s %s\n", "ID", "Title", "Status", "Retry", "Size")
		fmt.Println("│  " + repeat("─", 82))
		for _, d := range deliverables {
			title := d.Title
			if len(title) > 34 {
				title = title[:31] + "..."
			}
			fmt.Printf("│  %-20s %-36s %-12s %-5d %s\n",
				d.ID, title, d.Status, d.RetryCount, d.Complexity)
		}

		issues, _ := reg.ListIssues(run.ID)
		if len(issues) > 0 {
			fmt.Printf("│  Issues logged: %d\n", len(issues))
		}
		fmt.Println("└" + repeat("─", 85))
	}
}

func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	_ = fs.Parse(args)

	reg, err := registry.New(filepath.Join(".agentflow", "registry"))
	must(err, "initializing registry")

	runs, err := reg.ListPRDRuns()
	must(err, "listing PRD runs")

	if len(runs) == 0 {
		fmt.Println("No PRD runs found in this project.")
		return
	}

	fmt.Printf("\n%-20s %-20s %-14s %-6s %s\n", "Run ID", "PRD Slug", "Status", "Score", "Created")
	fmt.Println(repeat("─", 80))
	for _, r := range runs {
		fmt.Printf("%-20s %-20s %-14s %-6.2f %s\n",
			r.ID, r.Slug, r.Status, r.QualityScore,
			r.CreatedAt.Format("2006-01-02 15:04"))
	}
}

// prdSlugFromArg normalises a --prd flag value to a slug.
// "docs/prd-auth.md" → "prd-auth", "prd-auth" → "prd-auth"
func prdSlugFromArg(arg string) string {
	base := filepath.Base(arg)
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	return base
}

// ── helpers ───────────────────────────────────────────────────────────────────

func buildProvider(cfg *config.Config) (llm.Provider, error) {
	pc := cfg.ActiveProviderConfig()
	switch cfg.ActiveProvider {
	case "anthropic":
		return llm.NewAnthropicProvider(pc.APIKey(), pc.Model, pc.BaseURL), nil
	case "openai":
		return llm.NewOpenAIProvider(pc.APIKey(), pc.Model, pc.BaseURL), nil
	case "ollama":
		return llm.NewOllamaProvider(pc.Model, pc.BaseURL), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", cfg.ActiveProvider)
	}
}

func loadProjectConfig(path string) (*ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pc ProjectConfig
	if err := json.Unmarshal(data, &pc); err != nil {
		return nil, fmt.Errorf("parsing project.json: %w", err)
	}
	if pc.Name == "" {
		return nil, fmt.Errorf("project.json: name is required")
	}
	if pc.PRDFile == "" {
		pc.PRDFile = "docs/prd.md"
	}
	return &pc, nil
}

func loadStandards(path string) []registry.CodingStandard {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s []registry.CodingStandard
	_ = json.Unmarshal(data, &s)
	return s
}

func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.json"
	}
	return filepath.Join(home, ".agentflow", "config.json")
}

func writeJSONFile(path string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	must(err, "marshaling "+path)
	must(os.WriteFile(path, append(data, '\n'), 0o644), "writing "+path)
}

func writeTextFile(path string, content string) {
	must(os.WriteFile(path, []byte(content), 0o644), "writing "+path)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func must(err error, context string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "error %s: %v\n", context, err)
		os.Exit(1)
	}
}

func repeat(s string, n int) string {
	b := make([]byte, len(s)*n)
	for i := 0; i < n; i++ {
		copy(b[i*len(s):], s)
	}
	return string(b)
}
