package registry

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Registry manages all persistent state for the agentic pipeline.
//
// Directory layout:
//
//	baseDir/
//	  project.json                        ← single project per workspace
//	  prd-runs/
//	    {prdRunID}/
//	      prd-run.json                    ← PRDRun metadata
//	      deliverables/
//	        {deliverableID}.json
//	  adr/
//	    {adrID}.json
//	  standards.json
//	  issues.json
type Registry struct {
	baseDir string
	mu      sync.RWMutex
}

func New(baseDir string) (*Registry, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating registry base dir: %w", err)
	}
	return &Registry{baseDir: baseDir}, nil
}

// ── Project (one per workspace) ───────────────────────────────────────────────

func (r *Registry) InitProject(name, workspaceDir string) (*Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Return existing project if already initialised
	var existing Project
	if err := r.readJSON(r.projectPath(), &existing); err == nil {
		return &existing, nil
	}

	p := &Project{
		ID:           newID(),
		Name:         name,
		WorkspaceDir: workspaceDir,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := os.MkdirAll(r.adrDir(), 0o755); err != nil {
		return nil, err
	}
	return p, r.writeJSON(r.projectPath(), p)
}

func (r *Registry) GetProject() (*Project, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var p Project
	return &p, r.readJSON(r.projectPath(), &p)
}

func (r *Registry) UpdateProject(p *Project) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.UpdatedAt = time.Now()
	return r.writeJSON(r.projectPath(), p)
}

// ── PRDRun ────────────────────────────────────────────────────────────────────

// CreatePRDRun starts a new pipeline run for a specific PRD file.
func (r *Registry) CreatePRDRun(projectID, slug, prdFile string) (*PRDRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	run := &PRDRun{
		ID:        newID(),
		ProjectID: projectID,
		Slug:      slug,
		PRDFile:   prdFile,
		Status:    "prd_draft",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := os.MkdirAll(r.prdRunDeliverableDir(run.ID), 0o755); err != nil {
		return nil, err
	}
	return run, r.writeJSON(r.prdRunPath(run.ID), run)
}

func (r *Registry) GetPRDRun(prdRunID string) (*PRDRun, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var run PRDRun
	return &run, r.readJSON(r.prdRunPath(prdRunID), &run)
}

func (r *Registry) UpdatePRDRun(run *PRDRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run.UpdatedAt = time.Now()
	return r.writeJSON(r.prdRunPath(run.ID), run)
}

// ListPRDRuns returns all PRD runs, newest first.
func (r *Registry) ListPRDRuns() ([]*PRDRun, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries, err := os.ReadDir(filepath.Join(r.baseDir, "prd-runs"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var runs []*PRDRun
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var run PRDRun
		if err := r.readJSON(r.prdRunPath(e.Name()), &run); err == nil {
			runs = append(runs, &run)
		}
	}
	return runs, nil
}

// GetPRDRunBySlug finds the most recent PRDRun for a given slug.
func (r *Registry) GetPRDRunBySlug(slug string) (*PRDRun, error) {
	runs, err := r.ListPRDRuns()
	if err != nil {
		return nil, err
	}
	var latest *PRDRun
	for _, run := range runs {
		if run.Slug == slug {
			if latest == nil || run.CreatedAt.After(latest.CreatedAt) {
				latest = run
			}
		}
	}
	if latest == nil {
		return nil, fmt.Errorf("no PRD run found for slug %q", slug)
	}
	return latest, nil
}

// ── Deliverable ───────────────────────────────────────────────────────────────

func (r *Registry) CreateDeliverable(d *Deliverable) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d.ID == "" {
		d.ID = newID()
	}
	d.CreatedAt = time.Now()
	d.UpdatedAt = time.Now()
	return r.writeJSON(r.deliverablePath(d.PRDRunID, d.ID), d)
}

func (r *Registry) GetDeliverable(prdRunID, deliverableID string) (*Deliverable, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var d Deliverable
	return &d, r.readJSON(r.deliverablePath(prdRunID, deliverableID), &d)
}

func (r *Registry) UpdateDeliverable(d *Deliverable) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d.UpdatedAt = time.Now()
	return r.writeJSON(r.deliverablePath(d.PRDRunID, d.ID), d)
}

func (r *Registry) ListDeliverables(prdRunID string) ([]*Deliverable, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries, err := os.ReadDir(r.prdRunDeliverableDir(prdRunID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var deliverables []*Deliverable
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var d Deliverable
		path := filepath.Join(r.prdRunDeliverableDir(prdRunID), e.Name())
		if err := r.readJSON(path, &d); err == nil {
			deliverables = append(deliverables, &d)
		}
	}
	return deliverables, nil
}

// NextPendingDeliverable returns the first deliverable in a PRDRun whose
// dependencies are all done. Returns nil if none are ready.
func (r *Registry) NextPendingDeliverable(prdRunID string) (*Deliverable, error) {
	all, err := r.ListDeliverables(prdRunID)
	if err != nil {
		return nil, err
	}

	done := map[string]bool{}
	for _, d := range all {
		if d.Status == "done" {
			done[d.ID] = true
		}
	}

	for _, d := range all {
		if d.Status != "pending" {
			continue
		}
		ready := true
		for _, dep := range d.Dependencies {
			if !done[dep] {
				ready = false
				break
			}
		}
		if ready {
			return d, nil
		}
	}
	return nil, nil
}

// ── Architecture Decisions ────────────────────────────────────────────────────

func (r *Registry) SaveADR(adr *ArchitectureDecision) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if adr.ID == "" {
		adr.ID = newID()
	}
	if adr.CreatedAt.IsZero() {
		adr.CreatedAt = time.Now()
	}
	return r.writeJSON(filepath.Join(r.adrDir(), adr.ID+".json"), adr)
}

func (r *Registry) ListADRs() ([]*ArchitectureDecision, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries, err := os.ReadDir(r.adrDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var adrs []*ArchitectureDecision
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		var adr ArchitectureDecision
		if err := r.readJSON(filepath.Join(r.adrDir(), e.Name()), &adr); err == nil {
			adrs = append(adrs, &adr)
		}
	}
	return adrs, nil
}

// ── Coding Standards ──────────────────────────────────────────────────────────

func (r *Registry) SaveCodingStandards(standards []CodingStandard) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writeJSON(r.standardsPath(), standards)
}

func (r *Registry) GetCodingStandards() ([]CodingStandard, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var s []CodingStandard
	err := r.readJSON(r.standardsPath(), &s)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return s, err
}

// ── Issue Log ─────────────────────────────────────────────────────────────────

func (r *Registry) LogIssue(issue *IssueLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var issues []IssueLog
	_ = r.readJSON(r.issuesPath(), &issues)
	if issue.ID == "" {
		issue.ID = newID()
	}
	issue.CreatedAt = time.Now()
	issues = append(issues, *issue)
	return r.writeJSON(r.issuesPath(), issues)
}

func (r *Registry) ListIssues(prdRunID string) ([]IssueLog, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var all []IssueLog
	if err := r.readJSON(r.issuesPath(), &all); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if prdRunID == "" {
		return all, nil
	}
	var filtered []IssueLog
	for _, issue := range all {
		if issue.PRDRunID == prdRunID {
			filtered = append(filtered, issue)
		}
	}
	return filtered, nil
}

// ── Path helpers ──────────────────────────────────────────────────────────────

func (r *Registry) projectPath() string {
	return filepath.Join(r.baseDir, "project.json")
}
func (r *Registry) prdRunDir(prdRunID string) string {
	return filepath.Join(r.baseDir, "prd-runs", prdRunID)
}
func (r *Registry) prdRunPath(prdRunID string) string {
	return filepath.Join(r.prdRunDir(prdRunID), "prd-run.json")
}
func (r *Registry) prdRunDeliverableDir(prdRunID string) string {
	return filepath.Join(r.prdRunDir(prdRunID), "deliverables")
}
func (r *Registry) deliverablePath(prdRunID, deliverableID string) string {
	return filepath.Join(r.prdRunDeliverableDir(prdRunID), deliverableID+".json")
}
func (r *Registry) adrDir() string {
	return filepath.Join(r.baseDir, "adr")
}
func (r *Registry) standardsPath() string {
	return filepath.Join(r.baseDir, "standards.json")
}
func (r *Registry) issuesPath() string {
	return filepath.Join(r.baseDir, "issues.json")
}

// ── Atomic JSON helpers ───────────────────────────────────────────────────────

func (r *Registry) writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON for %s: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming %s → %s: %w", tmp, path, err)
	}
	return nil
}

func (r *Registry) readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
