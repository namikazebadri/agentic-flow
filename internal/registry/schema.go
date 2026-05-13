package registry

import "time"

// Project is the top-level workspace entity. One project = one repo/directory.
// A project can have many PRDRuns (one per PRD file).
type Project struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	WorkspaceDir string    `json:"workspace_dir"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// PRDRun represents one execution of a PRD file through the pipeline.
// Multiple PRDRuns can exist per Project — one per PRD file.
//
// Slug is derived from the PRD filename:
//
//	docs/prd-auth.md       → slug "prd-auth"
//	docs/prd-payment.md    → slug "prd-payment"
//
// Gherkin specs are written to:
//
//	docs/deliverables/{slug}/D-001-title.feature
type PRDRun struct {
	ID                 string                `json:"id"`
	ProjectID          string                `json:"project_id"`
	Slug               string                `json:"slug"`     // derived from filename, e.g. "prd-auth"
	PRDFile            string                `json:"prd_file"` // original file path, e.g. "docs/prd-auth.md"
	Status             string                `json:"status"`   // "prd_draft"|"decomposing"|"implementing"|"done"|"failed"
	QualityScore       float64               `json:"quality_score"`
	Issues             []string              `json:"issues,omitempty"`
	EnrichedPRD        string                `json:"enriched_prd"` // full rewritten PRD content
	AcceptanceCriteria []AcceptanceCriterion `json:"acceptance_criteria"`
	CreatedAt          time.Time             `json:"created_at"`
	UpdatedAt          time.Time             `json:"updated_at"`
}

// AcceptanceCriterion is a single testable acceptance criterion from a PRD.
type AcceptanceCriterion struct {
	ID          string `json:"id"`
	Feature     string `json:"feature"`
	Description string `json:"description"`
	Testable    bool   `json:"testable"`
}

// Deliverable is a single unit of work produced by the Decomposer for a PRDRun.
type Deliverable struct {
	ID                 string       `json:"id"`
	ProjectID          string       `json:"project_id"`
	PRDRunID           string       `json:"prd_run_id"` // which PRDRun this came from
	PRDSlug            string       `json:"prd_slug"`   // e.g. "prd-auth" — for folder naming
	Sequence           int          `json:"sequence"`
	Title              string       `json:"title"`
	Description        string       `json:"description"`
	Purpose            string       `json:"purpose"`      // why this deliverable exists
	Complexity         string       `json:"complexity"`   // "S"|"M"|"L"|"XL"
	Status             string       `json:"status"`       // "pending"|"in_progress"|"gate_failed"|"review"|"done"
	Dependencies       []string     `json:"dependencies"` // IDs within same PRDRun
	InputContract      string       `json:"input_contract"`
	OutputContract     string       `json:"output_contract"`
	Scope              []string     `json:"scope"`        // explicit in-scope items
	OutOfScope         []string     `json:"out_of_scope"` // explicit out-of-scope items
	TechAssumptions    []string     `json:"tech_assumptions,omitempty"`
	DefinitionOfDone   []string     `json:"definition_of_done"`
	SuggestedModel     string       `json:"suggested_model,omitempty"`
	OverrideModel      string       `json:"override_model,omitempty"`
	GherkinSpecPath    string       `json:"gherkin_spec_path"` // docs/deliverables/{slug}/{seq}-{title}.feature
	SpecDocPath        string       `json:"spec_doc_path"`     // docs/deliverables/{slug}/{seq}-{title}.md
	ImplementationPath string       `json:"implementation_path,omitempty"`
	RetryCount         int          `json:"retry_count"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
	GateResults        []GateResult `json:"gate_results,omitempty"`
	SelfReview         *SelfReview  `json:"self_review,omitempty"`
}

// GateResult records the outcome of one automated gate run.
type GateResult struct {
	RunAt    time.Time `json:"run_at"`
	Passed   bool      `json:"passed"`
	Stage    string    `json:"stage"` // "lint"|"type_check"|"test"|"contract"
	Output   string    `json:"output"`
	ExitCode int       `json:"exit_code"`
}

// SelfReview is the agent's self-assessment before submitting to the gate.
type SelfReview struct {
	AllCriteriaMet     bool     `json:"all_criteria_met"`
	AssumptionsMade    []string `json:"assumptions_made,omitempty"`
	UncertainAreas     []string `json:"uncertain_areas,omitempty"`
	HardcodedItems     []string `json:"hardcoded_items,omitempty"`
	ContractConsistent bool     `json:"contract_consistent"`
	Notes              string   `json:"notes,omitempty"`
}

// ArchitectureDecision records an ADR scoped to a project.
type ArchitectureDecision struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Title     string    `json:"title"`
	Context   string    `json:"context"`
	Decision  string    `json:"decision"`
	Rationale string    `json:"rationale"`
	CreatedAt time.Time `json:"created_at"`
}

// CodingStandard is a project-level coding rule.
type CodingStandard struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Category    string `json:"category"`
	Rule        string `json:"rule"`
	Example     string `json:"example,omitempty"`
	AntiExample string `json:"anti_example,omitempty"`
}

// IssueLog records a categorised issue for feedback loop tracking.
type IssueLog struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	PRDRunID      string    `json:"prd_run_id,omitempty"`
	DeliverableID string    `json:"deliverable_id,omitempty"`
	DetectedAt    string    `json:"detected_at"` // "gate"|"human_review"|"production"
	RootCause     string    `json:"root_cause"`
	Description   string    `json:"description"`
	Resolution    string    `json:"resolution,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}
