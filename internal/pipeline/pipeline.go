package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentflow/core/internal/agent"
	"github.com/agentflow/core/internal/config"
	"github.com/agentflow/core/internal/gate"
	"github.com/agentflow/core/internal/llm"
	"github.com/agentflow/core/internal/registry"
)

// Pipeline orchestrates the full agentic development flow.
type Pipeline struct {
	reg         *registry.Registry
	prdBuilder  *agent.PRDBuilderAgent
	decomposer  *agent.DecomposerAgent
	implementor *agent.ImplementorAgent
	gate        *gate.Gate
	reporter    Reporter
	maxRetries  int
	verbose     bool
	cfg         *config.Config
}

// Reporter is the interface for pipeline progress output.
type Reporter interface {
	Info(format string, args ...any)
	Success(format string, args ...any)
	Warning(format string, args ...any)
	Error(format string, args ...any)
	Section(title string)
	DeliverableStart(d *registry.Deliverable)
	DeliverableResult(d *registry.Deliverable, passed bool, detail string)
}

// Options configures one pipeline run for a single PRD file.
type Options struct {
	ProjectName  string
	PRDFile      string // relative path, e.g. "docs/prd-auth.md"
	PRDContent   string
	WorkspaceDir string // absolute path to project root
	DocsDir      string // relative, default "docs"
	SrcDir       string // relative, default "src"
	TechStack    string
	MaxRetries   int
	Standards    []registry.CodingStandard
}

func New(
	reg *registry.Registry,
	prdBuilder *agent.PRDBuilderAgent,
	decomposer *agent.DecomposerAgent,
	implementor *agent.ImplementorAgent,
	g *gate.Gate,
	reporter Reporter,
	maxRetries int,
	verbose bool,
	cfg *config.Config,
) *Pipeline {
	return &Pipeline{
		reg:         reg,
		prdBuilder:  prdBuilder,
		decomposer:  decomposer,
		implementor: implementor,
		gate:        g,
		reporter:    reporter,
		maxRetries:  maxRetries,
		verbose:     verbose,
		cfg:         cfg,
	}
}

// Run executes the pipeline for one PRD file.
func (p *Pipeline) Run(ctx context.Context, opts Options) (*registry.PRDRun, error) {
	docsDir := orDefault(opts.DocsDir, "docs")
	srcDir := orDefault(opts.SrcDir, "src")
	absSrcDir := filepath.Join(opts.WorkspaceDir, srcDir)

	// Derive slug from PRD filename: "docs/prd-auth.md" → "prd-auth"
	slug := prdSlug(opts.PRDFile)

	// Gherkin specs for this PRD go into docs/deliverables/{slug}/
	specsDir := filepath.Join(opts.WorkspaceDir, docsDir, "deliverables", slug)

	// ── Phase 0: Setup ────────────────────────────────────────────────────────
	p.reporter.Section(fmt.Sprintf("PRD: %s", slug))

	for _, d := range []string{specsDir, absSrcDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("creating directory %s: %w", d, err)
		}
	}

	// Init project (idempotent — reuses existing if already created)
	project, err := p.reg.InitProject(opts.ProjectName, opts.WorkspaceDir)
	if err != nil {
		return nil, fmt.Errorf("init project: %w", err)
	}

	// Create a new PRDRun for this slug
	run, err := p.reg.CreatePRDRun(project.ID, slug, opts.PRDFile)
	if err != nil {
		return nil, fmt.Errorf("creating prd run: %w", err)
	}

	p.reporter.Info("Project : %s (%s)", project.Name, project.ID)
	p.reporter.Info("PRD Run : %s (%s)", slug, run.ID)
	p.reporter.Info("Specs   → %s/", filepath.Join(docsDir, "deliverables", slug))
	p.reporter.Info("Code    → %s/", srcDir)

	// ── Phase 1: PRD Building ─────────────────────────────────────────────────
	p.reporter.Section("Phase 1: PRD Builder Agent")
	p.reporter.Info("Validating and enriching PRD...")

	prdResult, err := p.prdBuilder.Build(ctx, run.ID, opts.PRDContent)
	if err != nil {
		run.Status = "failed"
		_ = p.reg.UpdatePRDRun(run)
		return run, fmt.Errorf("PRD validation failed:\n%w", err)
	}

	// Persist enriched PRD into the run
	run.QualityScore = prdResult.QualityScore
	run.Issues = prdResult.Issues
	run.EnrichedPRD = prdResult.RawContent
	run.AcceptanceCriteria = make([]registry.AcceptanceCriterion, len(prdResult.AcceptanceCriteria))
	for i, ac := range prdResult.AcceptanceCriteria {
		run.AcceptanceCriteria[i] = ac
	}
	run.Status = "decomposing"
	_ = p.reg.UpdatePRDRun(run)

	// Write enriched PRD to docs/ for human visibility
	enrichedPath := filepath.Join(opts.WorkspaceDir, docsDir, slug+"-enriched.md")
	_ = os.WriteFile(enrichedPath, []byte(run.EnrichedPRD), 0o644)

	p.reporter.Success("PRD validated. Score: %.2f | Criteria: %d",
		run.QualityScore, len(run.AcceptanceCriteria))
	if len(run.Issues) > 0 {
		p.reporter.Warning("Issues (non-blocking): %s", strings.Join(run.Issues, "; "))
	}

	// ── Phase 2: Decomposition ────────────────────────────────────────────────
	p.reporter.Section("Phase 2: Decomposer Agent")
	p.reporter.Info("Breaking PRD into deliverables with Gherkin specs...")

	// Build a minimal PRD object for the decomposer
	prdForDecomp := &registry.PRDRun{
		ID:          run.ID,
		Slug:        slug,
		EnrichedPRD: run.EnrichedPRD,
	}

	deliverables, err := p.decomposer.Decompose(ctx, run.ID, slug, prdForDecomp, specsDir)
	if err != nil {
		run.Status = "failed"
		_ = p.reg.UpdatePRDRun(run)
		return run, fmt.Errorf("decomposition failed: %w", err)
	}

	for _, d := range deliverables {
		if err := p.reg.CreateDeliverable(d); err != nil {
			return run, fmt.Errorf("saving deliverable %s: %w", d.ID, err)
		}
	}

	p.reporter.Success("Decomposed into %d deliverables", len(deliverables))
	for _, d := range deliverables {
		deps := "none"
		if len(d.Dependencies) > 0 {
			deps = strings.Join(d.Dependencies, ", ")
		}
		p.reporter.Info("  [%s] %s (%s) — deps: %s", d.ID, d.Title, d.Complexity, deps)
	}

	run.Status = "implementing"
	_ = p.reg.UpdatePRDRun(run)

	// ── Phase 3: Implementation Loop ──────────────────────────────────────────
	p.reporter.Section("Phase 3: Implementation Loop")

	standards := opts.Standards
	if len(standards) == 0 {
		standards, _ = p.reg.GetCodingStandards()
	}
	adrs, _ := p.reg.ListADRs()

	completed := 0
	for {
		next, err := p.reg.NextPendingDeliverable(run.ID)
		if err != nil {
			return run, fmt.Errorf("finding next deliverable: %w", err)
		}
		if next == nil {
			break
		}

		if err := p.processDeliverable(ctx, project, run, next, standards, adrs, opts.TechStack, absSrcDir); err != nil {
			run.Status = "failed"
			_ = p.reg.UpdatePRDRun(run)
			return run, fmt.Errorf("deliverable %s (%s) failed: %w", next.ID, next.Title, err)
		}
		completed++
	}

	// Verify completion
	all, _ := p.reg.ListDeliverables(run.ID)
	allDone := true
	for _, d := range all {
		if d.Status != "done" {
			allDone = false
			p.reporter.Warning("Deliverable %s (%s) not done: %s", d.ID, d.Title, d.Status)
		}
	}

	if allDone {
		run.Status = "done"
		run.UpdatedAt = time.Now()
		p.reporter.Success("Done. %d/%d deliverables implemented for [%s].", completed, len(deliverables), slug)
	} else {
		run.Status = "failed"
		p.reporter.Error("Pipeline finished with incomplete deliverables.")
	}

	_ = p.reg.UpdatePRDRun(run)
	return run, nil
}

// processDeliverable runs the implement → gate → retry loop for one deliverable.
func (p *Pipeline) processDeliverable(
	ctx context.Context,
	project *registry.Project,
	run *registry.PRDRun,
	d *registry.Deliverable,
	standards []registry.CodingStandard,
	adrs []*registry.ArchitectureDecision,
	techStack string,
	absSrcDir string,
) error {
	p.reporter.DeliverableStart(d)

	d.Status = "in_progress"
	_ = p.reg.UpdateDeliverable(d)

	gherkinSpec, err := os.ReadFile(d.GherkinSpecPath)
	if err != nil {
		return fmt.Errorf("reading gherkin spec %s: %w", d.GherkinSpecPath, err)
	}

	existingFiles := p.collectExistingFiles(absSrcDir)

	// Read engineer's model override from spec doc before implementation starts
	if d.SpecDocPath != "" && p.cfg != nil {
		override, _ := llm.ReadOverrideFromSpecDoc(d.SpecDocPath, d.SuggestedModel)
		if override != "" && override != d.OverrideModel {
			if _, ok := p.cfg.Models.Models[override]; ok {
				d.OverrideModel = override
				_ = p.reg.UpdateDeliverable(d)
				p.reporter.Info("  Model override detected: %s → %s", d.SuggestedModel, override)
			} else {
				p.reporter.Warning("  Model override %q not in catalog — using suggested model", override)
			}
		}
	}

	// Resolve which model/provider to use for this deliverable
	var deliverableProvider agent.ProviderOverride
	if p.cfg != nil {
		resolved, err := llm.ResolveForDeliverable(p.cfg, d)
		if err != nil {
			p.reporter.Warning("  Model resolution failed: %v — using default", err)
		} else {
			deliverableProvider = agent.ProviderOverride{
				Provider:    resolved.Provider,
				ModelAlias:  resolved.ModelUsed.Alias,
				DisplayName: resolved.ModelUsed.DisplayName,
			}
			p.reporter.Info("  Model: %s (%s)", resolved.ModelUsed.DisplayName, resolved.ModelUsed.Alias)
		}
	}

	pkg := agent.ContextPackage{
		Deliverable:      d,
		GherkinSpec:      string(gherkinSpec),
		CodingStandards:  standards,
		ADRs:             adrs,
		ExistingFiles:    existingFiles,
		TechStack:        techStack,
		ProviderOverride: deliverableProvider,
	}

	for attempt := 1; attempt <= p.maxRetries+1; attempt++ {
		if attempt > 1 {
			p.reporter.Warning("  Retry %d/%d for [%s] %s", attempt-1, p.maxRetries, d.ID, d.Title)
		}

		selfReview, writtenPaths, err := p.implementor.Implement(ctx, pkg, project.WorkspaceDir, absSrcDir)
		if err != nil {
			if attempt > p.maxRetries {
				d.Status = "gate_failed"
				_ = p.reg.UpdateDeliverable(d)
				return fmt.Errorf("implementation failed after %d attempts: %w", attempt, err)
			}
			p.reporter.Warning("  Implementation error: %v", err)
			pkg.ExistingFiles["_gate_feedback.txt"] = fmt.Sprintf("PREVIOUS ERROR:\n%v\nFix this.", err)
			continue
		}

		d.SelfReview = selfReview
		if len(writtenPaths) > 0 {
			d.ImplementationPath = writtenPaths[0]
		}

		if len(selfReview.AssumptionsMade) > 0 {
			p.reporter.Warning("  Assumptions: %s", strings.Join(selfReview.AssumptionsMade, "; "))
		}
		if !selfReview.AllCriteriaMet {
			p.reporter.Warning("  Agent: not all Gherkin criteria met")
		}

		// Gherkin coverage heuristic
		if ok, msg := gate.GherkinCoverageCheck(string(gherkinSpec), absSrcDir); !ok {
			p.reporter.Warning("  Coverage: %s", msg)
		}

		// Automated gate
		gateResult, err := p.gate.Run(ctx, d.ID)
		if err != nil {
			return fmt.Errorf("gate execution error: %w", err)
		}

		d.GateResults = append(d.GateResults, gateResult.Stages...)
		_ = p.reg.UpdateDeliverable(d)

		if gateResult.Passed {
			d.Status = "done"
			_ = p.reg.UpdateDeliverable(d)
			p.reporter.DeliverableResult(d, true, gateResult.Summary)
			return nil
		}

		// Gate failed
		d.Status = "gate_failed"
		d.RetryCount = attempt
		_ = p.reg.UpdateDeliverable(d)
		p.reporter.DeliverableResult(d, false, gateResult.Summary)

		_ = p.reg.LogIssue(&registry.IssueLog{
			ProjectID:     project.ID,
			PRDRunID:      run.ID,
			DeliverableID: d.ID,
			DetectedAt:    "gate",
			RootCause:     "agent_misinterpret",
			Description:   gateResult.Summary,
		})

		if attempt > p.maxRetries {
			return fmt.Errorf("gate failed after %d attempts:\n%s", attempt, gateResult.Summary)
		}

		pkg.ExistingFiles["_gate_feedback.txt"] = fmt.Sprintf(
			"GATE FAILURE (attempt %d/%d):\n%s\n\nFix ALL issues above.",
			attempt, p.maxRetries, gateResult.Summary)
	}

	return nil
}

// collectExistingFiles returns relative-path → content for source files in absSrcDir.
func (p *Pipeline) collectExistingFiles(absSrcDir string) map[string]string {
	const maxBytes = 8192
	files := map[string]string{}

	_ = filepath.Walk(absSrcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Size() > maxBytes {
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".ts", ".js", ".py", ".java", ".rb", ".rs",
			".json", ".yaml", ".yml", ".mod", ".sum", ".toml":
		default:
			return nil
		}
		data, _ := os.ReadFile(path)
		rel, _ := filepath.Rel(absSrcDir, path)
		files[rel] = string(data)
		return nil
	})

	return files
}

func orDefault(s, def string) string {
	if s != "" {
		return s
	}
	return def
}

// prdSlug derives a URL-safe slug from a PRD file path.
// "docs/prd-auth.md" → "prd-auth"
// "docs/payment feature.md" → "payment-feature"
func prdSlug(prdFile string) string {
	base := filepath.Base(prdFile)
	// Remove extension
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	// Sanitize: lowercase, spaces→dashes, keep alphanumeric and dashes
	result := make([]byte, 0, len(base))
	for _, c := range strings.ToLower(base) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			result = append(result, byte(c))
		case c == ' ', c == '_':
			result = append(result, '-')
		}
	}
	return string(result)
}
