package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentflow/core/internal/llm"
	"github.com/agentflow/core/internal/registry"
)

// PRDBuilderAgent validates and enriches a raw PRD into an agent-ready spec.
type PRDBuilderAgent struct {
	*Base
}

func NewPRDBuilderAgent(base *Base) *PRDBuilderAgent {
	return &PRDBuilderAgent{Base: base}
}

type prdBuilderOutput struct {
	QualityScore       float64  `json:"quality_score"`
	Issues             []string `json:"issues"`
	EnrichedPRD        string   `json:"enriched_prd"`
	AcceptanceCriteria []struct {
		ID          string `json:"id"`
		Feature     string `json:"feature"`
		Description string `json:"description"`
		Testable    bool   `json:"testable"`
	} `json:"acceptance_criteria"`
	ClarificationQuestions []string `json:"clarification_questions,omitempty"`
}

// PRDBuildResult carries the validated + enriched PRD content.
// It maps directly onto fields of registry.PRDRun.
type PRDBuildResult struct {
	QualityScore       float64
	Issues             []string
	RawContent         string // enriched PRD markdown
	AcceptanceCriteria []registry.AcceptanceCriterion
}

const minQualityThreshold = 0.6

// Build processes a raw PRD string and returns a PRDBuildResult.
// Returns an error if quality_score < 0.6, listing all issues.
func (a *PRDBuilderAgent) Build(ctx context.Context, prdRunID, rawPRD string) (*PRDBuildResult, error) {
	systemPrompt, err := a.LoadPrompt("prd_builder")
	if err != nil {
		return nil, fmt.Errorf("prd builder: %w", err)
	}

	userMessage := fmt.Sprintf(
		"Analyze and enrich the following PRD. Return ONLY valid JSON matching the specified schema.\n\n<prd>\n%s\n</prd>",
		rawPRD,
	)

	resp, err := a.Complete(ctx, llm.Request{
		SystemPrompt: systemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: userMessage}},
		MaxTokens:    4096,
		Temperature:  0.2,
	})
	if err != nil {
		return nil, fmt.Errorf("prd builder llm call: %w", err)
	}

	jsonStr := ExtractJSON(resp.Content)
	var output prdBuilderOutput
	if err := json.Unmarshal([]byte(jsonStr), &output); err != nil {
		return nil, fmt.Errorf("prd builder: parsing response JSON: %w\nraw:\n%s", err, resp.Content)
	}

	if output.QualityScore < minQualityThreshold {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf(
			"PRD quality score %.2f is below minimum %.2f.\n\nIssues:\n",
			output.QualityScore, minQualityThreshold,
		))
		for i, issue := range output.Issues {
			sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, issue))
		}
		if len(output.ClarificationQuestions) > 0 {
			sb.WriteString("\nClarification needed:\n")
			for i, q := range output.ClarificationQuestions {
				sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, q))
			}
		}
		return nil, fmt.Errorf("%s", sb.String())
	}

	criteria := make([]registry.AcceptanceCriterion, len(output.AcceptanceCriteria))
	for i, ac := range output.AcceptanceCriteria {
		criteria[i] = registry.AcceptanceCriterion{
			ID:          ac.ID,
			Feature:     ac.Feature,
			Description: ac.Description,
			Testable:    ac.Testable,
		}
	}

	return &PRDBuildResult{
		QualityScore:       output.QualityScore,
		Issues:             output.Issues,
		RawContent:         output.EnrichedPRD,
		AcceptanceCriteria: criteria,
	}, nil
}
