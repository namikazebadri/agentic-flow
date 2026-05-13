package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/agentflow/core/internal/config"
	"github.com/agentflow/core/internal/llm"
	"github.com/agentflow/core/internal/registry"
)

// DecomposerAgent breaks a validated PRD into deliverables with Gherkin specs
// and human-readable spec documents.
type DecomposerAgent struct {
	*Base
	cfg *config.Config
}

func NewDecomposerAgent(base *Base, cfg *config.Config) *DecomposerAgent {
	return &DecomposerAgent{Base: base, cfg: cfg}
}

// decomposerItem is one deliverable as returned by the LLM.
type decomposerItem struct {
	ID               string   `json:"id"`
	Sequence         int      `json:"sequence"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	Purpose          string   `json:"purpose"`
	Complexity       string   `json:"complexity"`
	Dependencies     []string `json:"dependencies"`
	InputContract    string   `json:"input_contract"`
	OutputContract   string   `json:"output_contract"`
	Scope            []string `json:"scope"`
	OutOfScope       []string `json:"out_of_scope"`
	TechAssumptions  []string `json:"tech_assumptions"`
	DefinitionOfDone []string `json:"definition_of_done"`
	GherkinSpec      string   `json:"gherkin_spec"`
}

type decomposerOutput struct {
	Deliverables []decomposerItem `json:"deliverables"`
}

// Decompose breaks a PRDRun into ordered deliverables.
// Per deliverable, writes to specsDir:
//
//	{seq:03d}-{title}.md       — human-readable spec doc
//	{seq:03d}-{title}.feature  — executable Gherkin spec
//
// Also writes _index.md summarising all deliverables and dependency graph.
func (a *DecomposerAgent) Decompose(
	ctx context.Context,
	prdRunID string,
	slug string,
	run *registry.PRDRun,
	specsDir string,
) ([]*registry.Deliverable, error) {

	systemPrompt, err := a.LoadPrompt("decomposer")
	if err != nil {
		return nil, fmt.Errorf("decomposer: %w", err)
	}

	userMessage := fmt.Sprintf(
		"Decompose the following PRD into deliverables.\n"+
			"Return ONLY valid JSON matching the specified schema.\n\n"+
			"<prd>\n%s\n</prd>",
		run.EnrichedPRD,
	)

	resp, err := a.Complete(ctx, llm.Request{
		SystemPrompt: systemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: userMessage}},
		MaxTokens:    8192,
		Temperature:  0.2,
	})
	if err != nil {
		return nil, fmt.Errorf("decomposer llm call: %w", err)
	}

	jsonStr := ExtractJSON(resp.Content)
	var output decomposerOutput
	if err := json.Unmarshal([]byte(jsonStr), &output); err != nil {
		return nil, fmt.Errorf("decomposer: parsing JSON: %w\nraw:\n%s", err, resp.Content)
	}

	if len(output.Deliverables) == 0 {
		return nil, fmt.Errorf("decomposer produced zero deliverables")
	}

	// Validate dependency references
	knownIDs := map[string]bool{}
	for _, d := range output.Deliverables {
		knownIDs[d.ID] = true
	}
	for _, d := range output.Deliverables {
		for _, dep := range d.Dependencies {
			if !knownIDs[dep] {
				return nil, fmt.Errorf("deliverable %q has unknown dependency %q", d.ID, dep)
			}
		}
	}
	if err := checkCycles(output.Deliverables); err != nil {
		return nil, fmt.Errorf("dependency graph: %w", err)
	}

	if err := os.MkdirAll(specsDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating specs dir: %w", err)
	}

	// Build title → ID map for dependency labels in index
	titleByID := map[string]string{}
	for _, d := range output.Deliverables {
		titleByID[d.ID] = d.Title
	}

	deliverables := make([]*registry.Deliverable, 0, len(output.Deliverables))
	for _, d := range output.Deliverables {
		baseName := fmt.Sprintf("%03d-%s", d.Sequence, sanitizeTitle(d.Title))
		specDocPath := filepath.Join(specsDir, baseName+".md")
		gherkinPath := filepath.Join(specsDir, baseName+".feature")

		// Suggest model based on catalog rules
		suggestedModel := ""
		if a.cfg != nil {
			suggestedModel = a.cfg.SuggestModel(d.Complexity, d.Title, d.Description, len(d.Dependencies))
		}
		// Resolve display name for the spec doc
		modelDisplay := suggestedModel
		if a.cfg != nil && suggestedModel != "" {
			entry := a.cfg.ResolveModel(suggestedModel)
			modelDisplay = entry.DisplayName + " (`" + suggestedModel + "`)"
		}

		// Write human-readable spec doc (includes model suggestion)
		specDoc := renderSpecDoc(d, slug, titleByID, suggestedModel, modelDisplay, a.cfg)
		if err := os.WriteFile(specDocPath, []byte(specDoc), 0o644); err != nil {
			return nil, fmt.Errorf("writing spec doc for %s: %w", d.ID, err)
		}

		// Write Gherkin spec
		if err := os.WriteFile(gherkinPath, []byte(d.GherkinSpec), 0o644); err != nil {
			return nil, fmt.Errorf("writing gherkin for %s: %w", d.ID, err)
		}

		deliverables = append(deliverables, &registry.Deliverable{
			ID:               d.ID,
			PRDRunID:         prdRunID,
			PRDSlug:          slug,
			Sequence:         d.Sequence,
			Title:            d.Title,
			Description:      d.Description,
			Purpose:          d.Purpose,
			Complexity:       d.Complexity,
			Status:           "pending",
			Dependencies:     d.Dependencies,
			InputContract:    d.InputContract,
			OutputContract:   d.OutputContract,
			Scope:            d.Scope,
			OutOfScope:       d.OutOfScope,
			TechAssumptions:  d.TechAssumptions,
			DefinitionOfDone: d.DefinitionOfDone,
			SuggestedModel:   suggestedModel,
			GherkinSpecPath:  gherkinPath,
			SpecDocPath:      specDocPath,
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		})
	}

	sort.Slice(deliverables, func(i, j int) bool {
		return deliverables[i].Sequence < deliverables[j].Sequence
	})

	// Write _index.md — overview of all deliverables for this PRD run
	indexPath := filepath.Join(specsDir, "_index.md")
	indexDoc := renderIndexDoc(slug, run.PRDFile, deliverables)
	if err := os.WriteFile(indexPath, []byte(indexDoc), 0o644); err != nil {
		return nil, fmt.Errorf("writing index doc: %w", err)
	}

	return deliverables, nil
}

// ── Document renderers ────────────────────────────────────────────────────────

// renderSpecDoc produces the human-readable .md for a single deliverable.
// ModelCatalogLister is a minimal interface so renderSpecDoc can list models
// without importing the full config package (avoids circular deps).
type ModelCatalogLister interface {
	ListModels() []config.ModelEntry
}

func renderSpecDoc(d decomposerItem, slug string, titleByID map[string]string, suggestedAlias, modelDisplay string, cfg ModelCatalogLister) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# %s: %s\n\n", d.ID, d.Title))
	sb.WriteString(fmt.Sprintf("**PRD**: `%s`  \n", slug))
	sb.WriteString(fmt.Sprintf("**Sequence**: %d  \n", d.Sequence))
	sb.WriteString(fmt.Sprintf("**Complexity**: %s  \n", d.Complexity))
	sb.WriteString(fmt.Sprintf("**Status**: pending  \n"))

	// Model suggestion — engineer can override this value
	if modelDisplay != "" {
		sb.WriteString(fmt.Sprintf("**Model**: `%s`  \n", suggestedAlias))
		sb.WriteString(fmt.Sprintf("**Model Info**: %s  \n", modelDisplay))
	} else {
		sb.WriteString("**Model**: `default`  \n")
	}
	sb.WriteString("\n")
	// Show available models as a reference for engineers
	if cfg != nil {
		models := cfg.ListModels()
		if len(models) > 0 {
			sb.WriteString("> **Available models** (edit Model field above to override):\n")
			for _, m := range models {
				sb.WriteString(fmt.Sprintf("> - `%s` — %s [%s/%s]\n",
					m.Alias, m.DisplayName, m.Tier, m.CostTier))
			}
			sb.WriteString("\n")
		}
	}

	// Dependencies — show title not just ID for readability
	if len(d.Dependencies) > 0 {
		depLabels := make([]string, 0, len(d.Dependencies))
		for _, dep := range d.Dependencies {
			if title, ok := titleByID[dep]; ok {
				depLabels = append(depLabels, fmt.Sprintf("`%s` (%s)", dep, title))
			} else {
				depLabels = append(depLabels, fmt.Sprintf("`%s`", dep))
			}
		}
		sb.WriteString(fmt.Sprintf("**Depends on**: %s  \n\n", strings.Join(depLabels, ", ")))
	} else {
		sb.WriteString("**Depends on**: none  \n\n")
	}

	sb.WriteString("---\n\n")

	// Purpose — the why
	sb.WriteString("## Tujuan\n\n")
	if d.Purpose != "" {
		sb.WriteString(d.Purpose + "\n\n")
	} else {
		sb.WriteString(d.Description + "\n\n")
	}

	// Contracts — the interface
	sb.WriteString("## Input Contract\n\n")
	sb.WriteString("```\n" + d.InputContract + "\n```\n\n")

	sb.WriteString("## Output Contract\n\n")
	sb.WriteString("```\n" + d.OutputContract + "\n```\n\n")

	// Scope — explicit boundaries
	sb.WriteString("## Scope\n\n")
	if len(d.Scope) > 0 {
		for _, item := range d.Scope {
			sb.WriteString(fmt.Sprintf("- ✅ %s\n", item))
		}
	} else {
		sb.WriteString("_(not specified)_\n")
	}
	sb.WriteString("\n")

	if len(d.OutOfScope) > 0 {
		sb.WriteString("### Tidak Termasuk\n\n")
		for _, item := range d.OutOfScope {
			sb.WriteString(fmt.Sprintf("- ❌ %s\n", item))
		}
		sb.WriteString("\n")
	}

	// Technical assumptions
	if len(d.TechAssumptions) > 0 {
		sb.WriteString("## Asumsi Teknis\n\n")
		for _, a := range d.TechAssumptions {
			sb.WriteString(fmt.Sprintf("- %s\n", a))
		}
		sb.WriteString("\n")
	}

	// Definition of done
	sb.WriteString("## Definition of Done\n\n")
	if len(d.DefinitionOfDone) > 0 {
		for _, item := range d.DefinitionOfDone {
			sb.WriteString(fmt.Sprintf("- [ ] %s\n", item))
		}
	} else {
		sb.WriteString("- [ ] Semua Gherkin scenario passed\n")
		sb.WriteString("- [ ] Automated gate passed (lint, type check, test)\n")
	}
	sb.WriteString("\n")

	// Link to Gherkin
	sb.WriteString("## Gherkin Spec\n\n")
	baseName := fmt.Sprintf("%03d-%s", d.Sequence, sanitizeTitle(d.Title))
	sb.WriteString(fmt.Sprintf("→ [`%s.feature`](./%s.feature)\n", baseName, baseName))

	return sb.String()
}

// renderIndexDoc produces the _index.md overview for an entire PRD run.
func renderIndexDoc(slug, prdFile string, deliverables []*registry.Deliverable) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Deliverable Index: %s\n\n", slug))
	sb.WriteString(fmt.Sprintf("**PRD File**: `%s`  \n", prdFile))
	sb.WriteString(fmt.Sprintf("**Total Deliverables**: %d  \n\n", len(deliverables)))
	sb.WriteString("---\n\n")

	// Summary table
	sb.WriteString("## Ringkasan\n\n")
	sb.WriteString("| Seq | ID | Title | Complexity | Dependencies | Status |\n")
	sb.WriteString("|-----|-----|-------|-----------|--------------|--------|\n")
	for _, d := range deliverables {
		deps := "—"
		if len(d.Dependencies) > 0 {
			deps = strings.Join(d.Dependencies, ", ")
		}
		sb.WriteString(fmt.Sprintf("| %03d | %s | %s | %s | %s | %s |\n",
			d.Sequence, d.ID, d.Title, d.Complexity, deps, d.Status))
	}
	sb.WriteString("\n")

	// Dependency graph as ASCII
	sb.WriteString("## Dependency Graph\n\n")
	sb.WriteString("```\n")
	sb.WriteString(renderDependencyGraph(deliverables))
	sb.WriteString("```\n\n")

	// Per-deliverable quick reference
	sb.WriteString("## Deliverables\n\n")
	for _, d := range deliverables {
		baseName := fmt.Sprintf("%03d-%s", d.Sequence, sanitizeTitle(d.Title))
		sb.WriteString(fmt.Sprintf("### %s — %s\n\n", d.ID, d.Title))
		sb.WriteString(fmt.Sprintf("%s\n\n", d.Description))
		sb.WriteString(fmt.Sprintf("- **Complexity**: %s\n", d.Complexity))

		if len(d.Dependencies) > 0 {
			sb.WriteString(fmt.Sprintf("- **Depends on**: %s\n", strings.Join(d.Dependencies, ", ")))
		}

		// Scope summary
		if len(d.Scope) > 0 {
			sb.WriteString("- **Includes**:\n")
			for _, s := range d.Scope {
				sb.WriteString(fmt.Sprintf("  - %s\n", s))
			}
		}
		if len(d.OutOfScope) > 0 {
			sb.WriteString("- **Excludes**:\n")
			for _, s := range d.OutOfScope {
				sb.WriteString(fmt.Sprintf("  - %s\n", s))
			}
		}

		sb.WriteString(fmt.Sprintf("\n📄 [Spec](./%s.md) · 🧪 [Gherkin](./%s.feature)\n\n", baseName, baseName))
	}

	// Checklist for human review
	sb.WriteString("---\n\n")
	sb.WriteString("## Review Checklist\n\n")
	sb.WriteString("Sebelum implementasi dimulai, pastikan:\n\n")
	sb.WriteString("- [ ] Semua deliverable mencakup seluruh acceptance criteria PRD\n")
	sb.WriteString("- [ ] Tidak ada overlap antar deliverable\n")
	sb.WriteString("- [ ] Dependency order masuk akal secara teknis\n")
	sb.WriteString("- [ ] Complexity estimate realistis untuk tim\n")
	sb.WriteString("- [ ] Output contract deliverable N = input contract deliverable yang depend padanya\n")
	sb.WriteString("- [ ] Tidak ada deliverable XL — jika ada, perlu dipecah lebih kecil\n")

	return sb.String()
}

// renderDependencyGraph renders a simple ASCII dependency graph.
func renderDependencyGraph(deliverables []*registry.Deliverable) string {
	if len(deliverables) == 0 {
		return "(empty)\n"
	}

	// Build ID → title map and dep map
	titleByID := map[string]string{}
	depsOf := map[string][]string{}
	for _, d := range deliverables {
		titleByID[d.ID] = d.Title
		depsOf[d.ID] = d.Dependencies
	}

	// Find roots (no dependencies)
	hasParent := map[string]bool{}
	for _, d := range deliverables {
		for _, dep := range d.Dependencies {
			hasParent[dep] = true // dep is a parent, not a root by this logic
		}
	}
	// Actually: roots are nodes with no dependencies
	var roots []string
	for _, d := range deliverables {
		if len(depsOf[d.ID]) == 0 {
			roots = append(roots, d.ID)
		}
	}

	var sb strings.Builder
	visited := map[string]bool{}

	var walk func(id, prefix string, isLast bool)
	walk = func(id, prefix string, isLast bool) {
		if visited[id] {
			return
		}
		visited[id] = true

		connector := "├── "
		if isLast {
			connector = "└── "
		}
		if prefix == "" {
			connector = ""
		}

		title := titleByID[id]
		if len(title) > 40 {
			title = title[:37] + "..."
		}
		sb.WriteString(fmt.Sprintf("%s%s[%s] %s\n", prefix, connector, id, title))

		// Find children (deliverables that depend on this id)
		var children []string
		for _, d := range deliverables {
			for _, dep := range d.Dependencies {
				if dep == id {
					children = append(children, d.ID)
				}
			}
		}

		childPrefix := prefix
		if prefix != "" {
			if isLast {
				childPrefix += "    "
			} else {
				childPrefix += "│   "
			}
		}

		for i, child := range children {
			walk(child, childPrefix, i == len(children)-1)
		}
	}

	for i, root := range roots {
		walk(root, "", i == len(roots)-1)
	}

	// Handle any nodes not reachable from roots (shouldn't happen but safety net)
	for _, d := range deliverables {
		if !visited[d.ID] {
			sb.WriteString(fmt.Sprintf("[%s] %s (unreachable from roots)\n", d.ID, titleByID[d.ID]))
		}
	}

	return sb.String()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// sanitizeTitle converts a title to a filename-safe slug.
// "User Authentication Model" → "user-authentication-model"
func sanitizeTitle(title string) string {
	result := make([]byte, 0, len(title))
	prevDash := false
	for _, c := range title {
		var b byte
		switch {
		case c >= 'a' && c <= 'z':
			b = byte(c)
		case c >= 'A' && c <= 'Z':
			b = byte(c + 32)
		case c >= '0' && c <= '9':
			b = byte(c)
		default:
			b = '-'
		}
		if b == '-' {
			if prevDash {
				continue
			}
			prevDash = true
		} else {
			prevDash = false
		}
		result = append(result, b)
	}
	for len(result) > 0 && result[len(result)-1] == '-' {
		result = result[:len(result)-1]
	}
	return string(result)
}

// checkCycles detects circular dependencies via DFS.
func checkCycles(deliverables []decomposerItem) error {
	graph := map[string][]string{}
	for _, d := range deliverables {
		graph[d.ID] = d.Dependencies
	}

	visited := map[string]bool{}
	inStack := map[string]bool{}

	var dfs func(id string) error
	dfs = func(id string) error {
		visited[id] = true
		inStack[id] = true
		for _, dep := range graph[id] {
			if !visited[dep] {
				if err := dfs(dep); err != nil {
					return err
				}
			} else if inStack[dep] {
				return fmt.Errorf("circular dependency: %s → %s", id, dep)
			}
		}
		inStack[id] = false
		return nil
	}

	for _, d := range deliverables {
		if !visited[d.ID] {
			if err := dfs(d.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
