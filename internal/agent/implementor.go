package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentflow/core/internal/llm"
	"github.com/agentflow/core/internal/registry"
)

// ImplementorAgent generates code for a single deliverable.
type ImplementorAgent struct {
	*Base
}

func NewImplementorAgent(base *Base) *ImplementorAgent {
	return &ImplementorAgent{Base: base}
}

// implementorOutput is the structured output from the LLM.
type implementorOutput struct {
	Files []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"files"`
	SelfReview struct {
		AllCriteriaMet     bool     `json:"all_criteria_met"`
		AssumptionsMade    []string `json:"assumptions_made"`
		UncertainAreas     []string `json:"uncertain_areas"`
		HardcodedItems     []string `json:"hardcoded_items"`
		ContractConsistent bool     `json:"contract_consistent"`
		Notes              string   `json:"notes"`
	} `json:"self_review"`
}

// ProviderOverride specifies which model/provider to use for one deliverable.
type ProviderOverride struct {
	Provider    llm.Provider // if nil, the agent's default base provider is used
	ModelAlias  string
	DisplayName string
}

// ContextPackage contains all information injected into the implementor's context.
type ContextPackage struct {
	Deliverable      *registry.Deliverable
	GherkinSpec      string
	CodingStandards  []registry.CodingStandard
	ADRs             []*registry.ArchitectureDecision
	ExistingFiles    map[string]string // path -> content of relevant existing files
	TechStack        string
	ProviderOverride ProviderOverride // optional per-deliverable model override
}

// Implement generates code files for a deliverable and writes them to absSrcDir.
func (a *ImplementorAgent) Implement(
	ctx context.Context,
	pkg ContextPackage,
	workspaceDir string,
	absSrcDir string,
) (*registry.SelfReview, []string, error) {

	systemPrompt, err := a.LoadPrompt("implementor")
	if err != nil {
		return nil, nil, fmt.Errorf("implementor: %w", err)
	}

	userMessage := buildImplementorPrompt(pkg)

	// Use per-deliverable model if specified, otherwise use base provider
	completeFunc := a.Complete
	if pkg.ProviderOverride.Provider != nil {
		completeFunc = func(ctx context.Context, req llm.Request) (*llm.Response, error) {
			return pkg.ProviderOverride.Provider.Complete(ctx, req)
		}
	}

	resp, err := completeFunc(ctx, llm.Request{
		SystemPrompt: systemPrompt,
		Messages: []llm.Message{
			{Role: "user", Content: userMessage},
		},
		MaxTokens:   8192,
		Temperature: 0.1, // very low — we want deterministic, correct code
	})
	if err != nil {
		return nil, nil, fmt.Errorf("implementor llm call: %w", err)
	}

	jsonStr := ExtractJSON(resp.Content)
	var output implementorOutput
	if err := json.Unmarshal([]byte(jsonStr), &output); err != nil {
		return nil, nil, fmt.Errorf("implementor: parsing response JSON: %w\nraw:\n%s", err, resp.Content)
	}

	if len(output.Files) == 0 {
		return nil, nil, fmt.Errorf("implementor produced no files")
	}

	// Write files to absSrcDir
	writtenPaths := make([]string, 0, len(output.Files))
	for _, f := range output.Files {
		absPath := filepath.Join(absSrcDir, f.Path)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return nil, nil, fmt.Errorf("creating dir for %s: %w", f.Path, err)
		}
		if err := os.WriteFile(absPath, []byte(f.Content), 0o644); err != nil {
			return nil, nil, fmt.Errorf("writing file %s: %w", f.Path, err)
		}
		writtenPaths = append(writtenPaths, absPath)
	}

	selfReview := &registry.SelfReview{
		AllCriteriaMet:     output.SelfReview.AllCriteriaMet,
		AssumptionsMade:    output.SelfReview.AssumptionsMade,
		UncertainAreas:     output.SelfReview.UncertainAreas,
		HardcodedItems:     output.SelfReview.HardcodedItems,
		ContractConsistent: output.SelfReview.ContractConsistent,
		Notes:              output.SelfReview.Notes,
	}

	return selfReview, writtenPaths, nil
}

func buildImplementorPrompt(pkg ContextPackage) string {
	var sb strings.Builder

	sb.WriteString("# Deliverable to Implement\n\n")
	sb.WriteString(fmt.Sprintf("**ID**: %s\n", pkg.Deliverable.ID))
	sb.WriteString(fmt.Sprintf("**Title**: %s\n", pkg.Deliverable.Title))
	sb.WriteString(fmt.Sprintf("**Description**: %s\n\n", pkg.Deliverable.Description))
	sb.WriteString(fmt.Sprintf("**Input Contract**:\n%s\n\n", pkg.Deliverable.InputContract))
	sb.WriteString(fmt.Sprintf("**Output Contract**:\n%s\n\n", pkg.Deliverable.OutputContract))

	sb.WriteString("# Gherkin Spec (your acceptance criteria)\n\n")
	sb.WriteString("```gherkin\n")
	sb.WriteString(pkg.GherkinSpec)
	sb.WriteString("\n```\n\n")

	if pkg.TechStack != "" {
		sb.WriteString(fmt.Sprintf("# Tech Stack\n\n%s\n\n", pkg.TechStack))
	}

	if len(pkg.CodingStandards) > 0 {
		sb.WriteString("# Coding Standards (MUST follow)\n\n")
		for _, s := range pkg.CodingStandards {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", s.Category, s.Rule))
			if s.Example != "" {
				sb.WriteString(fmt.Sprintf("  Good: %s\n", s.Example))
			}
			if s.AntiExample != "" {
				sb.WriteString(fmt.Sprintf("  Bad:  %s\n", s.AntiExample))
			}
		}
		sb.WriteString("\n")
	}

	if len(pkg.ADRs) > 0 {
		sb.WriteString("# Architecture Decisions (MUST respect)\n\n")
		for _, adr := range pkg.ADRs {
			sb.WriteString(fmt.Sprintf("## ADR: %s\n", adr.Title))
			sb.WriteString(fmt.Sprintf("Decision: %s\n", adr.Decision))
			sb.WriteString(fmt.Sprintf("Rationale: %s\n\n", adr.Rationale))
		}
	}

	if len(pkg.ExistingFiles) > 0 {
		sb.WriteString("# Existing Codebase Context\n\n")
		sb.WriteString("These files already exist. Your implementation MUST be consistent with them:\n\n")
		for path, content := range pkg.ExistingFiles {
			sb.WriteString(fmt.Sprintf("### %s\n```\n%s\n```\n\n", path, content))
		}
	}

	sb.WriteString(`# Instructions

Implement the deliverable above. Return ONLY valid JSON with this structure:
{
  "files": [
    {"path": "relative/path/from/src", "content": "full file content"},
    ...
  ],
  "self_review": {
    "all_criteria_met": true/false,
    "assumptions_made": ["assumption 1", ...],
    "uncertain_areas": ["area 1", ...],
    "hardcoded_items": ["item 1", ...],
    "contract_consistent": true/false,
    "notes": "any important notes"
  }
}

IMPORTANT:
- Include test files that implement the Gherkin step definitions
- all_criteria_met must honestly reflect whether ALL Gherkin scenarios are covered
- List every assumption, even small ones
- Do NOT output anything outside the JSON object`)

	return sb.String()
}
