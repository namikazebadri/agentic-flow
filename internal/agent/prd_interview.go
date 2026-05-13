package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/agentflow/core/internal/llm"
)

// PRDInterviewAgent conducts an adaptive multi-turn interview with the user
// to gather enough information to generate a robust, pipeline-compatible PRD.
type PRDInterviewAgent struct {
	*Base
	scanner *bufio.Scanner
}

func NewPRDInterviewAgent(base *Base) *PRDInterviewAgent {
	return &PRDInterviewAgent{
		Base:    base,
		scanner: bufio.NewScanner(os.Stdin),
	}
}

// turn is one Q&A exchange in the interview.
type turn struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// collectedPoint is one piece of information agent has confirmed from the interview.
type collectedPoint struct {
	// Topic is the short category label, e.g. "Problem", "Target User", "Performance"
	Topic string `json:"topic"`

	// Summary is a concise one-line summary of what was captured
	Summary string `json:"summary"`

	// Confidence is "confirmed" | "inferred" | "assumed"
	// confirmed = user stated explicitly
	// inferred  = agent derived from context
	// assumed   = agent filled in from common sense / not yet validated
	Confidence string `json:"confidence"`
}

// prdOutlineSection is one section of the PRD that will be generated.
type prdOutlineSection struct {
	Section string   `json:"section"`
	Points  []string `json:"points"`
}

// productSuggestion is one product-perspective insight from the agent.
type productSuggestion struct {
	// Severity: "critical" | "consider" | "optional"
	// critical  → likely to cause problems if ignored
	// consider  → worth discussing before finalizing
	// optional  → nice to have, low risk if skipped
	Severity string `json:"severity"`

	// Area is the PRD section this suggestion relates to
	// e.g. "Acceptance Criteria", "Security", "UX", "Performance"
	Area string `json:"area"`

	// Observation is what the agent noticed — the "what"
	Observation string `json:"observation"`

	// Suggestion is the concrete recommendation — the "so what"
	Suggestion string `json:"suggestion"`
}

// agentDecision is what the LLM returns after each user answer.
type agentDecision struct {
	// Status: "continue" | "clarify" | "complete"
	Status string `json:"status"`

	// CollectedPoints — full list rebuilt every turn, shown as review panel.
	CollectedPoints []collectedPoint `json:"collected_points"`

	// MissingAreas — topics not yet covered, shown in review panel.
	MissingAreas []string `json:"missing_areas"`

	// MidSuggestion — ONE product insight relevant to the latest answer.
	// Always present (even if minor). Shown in mid-interview review panel.
	MidSuggestion *productSuggestion `json:"mid_suggestion,omitempty"`

	// NextQuestion — only set when status is "continue" or "clarify".
	NextQuestion string `json:"next_question,omitempty"`

	// Rationale — why this question or why we're done.
	Rationale string `json:"rationale,omitempty"`

	// PRDOutline — structured preview per section, shown at final verification.
	// Only set when status is "complete".
	PRDOutline []prdOutlineSection `json:"prd_outline,omitempty"`

	// FinalSuggestions — comprehensive product suggestions at end of interview.
	// Only set when status is "complete".
	FinalSuggestions []productSuggestion `json:"final_suggestions,omitempty"`

	// PRDContent — full generated PRD markdown.
	// Only set when status is "complete".
	PRDContent string `json:"prd_content,omitempty"`
}

// InterviewResult is what the caller receives after a completed interview.
type InterviewResult struct {
	PRDContent string
	Turns      int
	Duration   time.Duration
}

// RunInterview conducts the full adaptive interview and returns the generated PRD.
func (a *PRDInterviewAgent) RunInterview(
	ctx context.Context,
	featureName string,
	projectContext string,
	verbose bool,
) (*InterviewResult, error) {

	systemPrompt, err := a.LoadPrompt("prd_interview")
	if err != nil {
		return nil, fmt.Errorf("prd interview: %w", err)
	}

	start := time.Now()
	history := []turn{}

	printInterviewHeader(featureName)

	// First question hardcoded — open-ended problem statement.
	firstQuestion := "Apa problem bisnis yang ingin diselesaikan oleh fitur ini? " +
		"Ceritakan dari perspektif user — siapa yang terdampak dan apa pain point-nya?"

	currentQuestion := firstQuestion
	maxTurns := 15

	for turnNum := 1; turnNum <= maxTurns; turnNum++ {
		// Ask the question and get user's answer
		answer, err := a.ask(currentQuestion, turnNum)
		if err != nil {
			return nil, fmt.Errorf("reading input: %w", err)
		}

		history = append(history, turn{
			Question: currentQuestion,
			Answer:   answer,
		})

		// Call LLM — get decision + full review panel
		decision, err := a.decide(ctx, systemPrompt, featureName, projectContext, history)
		if err != nil {
			return nil, fmt.Errorf("agent decision: %w", err)
		}

		// Always show review panel after each answer
		printReviewPanel(decision.CollectedPoints, decision.MissingAreas, decision.MidSuggestion)

		if verbose && decision.Rationale != "" {
			printMuted("Rationale: " + decision.Rationale)
		}

		switch decision.Status {
		case "complete":
			// ── Final verification checkpoint ─────────────────────────────────
			// Show PM the full outline before generating.
			// Loop until PM confirms, requests more questions, or quits.
			for {
				printFinalVerification(decision.CollectedPoints, decision.PRDOutline, decision.FinalSuggestions)

				choice := confirmWithUser()
				switch choice {
				case "generate":
					// PM approved — generate the PRD
					printSection("✓ Generating PRD...")
					if decision.PRDContent == "" {
						prd, err := a.generatePRD(ctx, systemPrompt, featureName, projectContext, history)
						if err != nil {
							return nil, fmt.Errorf("generating PRD: %w", err)
						}
						decision.PRDContent = prd
					}
					return &InterviewResult{
						PRDContent: decision.PRDContent,
						Turns:      len(history),
						Duration:   time.Since(start),
					}, nil

				case "more":
					// PM wants to add/clarify — ask one more question
					fmt.Print("\n\033[33mTambahkan topik atau koreksi yang ingin diklarifikasi:\033[0m\n→ ")
					if a.scanner.Scan() {
						addition := strings.TrimSpace(a.scanner.Text())
						if addition != "" {
							// Feed this back as a new turn and re-decide
							history = append(history, turn{
								Question: "[PM correction/addition]",
								Answer:   addition,
							})
							newDecision, err := a.decide(ctx, systemPrompt, featureName, projectContext, history)
							if err != nil {
								return nil, fmt.Errorf("re-deciding after correction: %w", err)
							}
							decision = newDecision
							printReviewPanel(decision.CollectedPoints, decision.MissingAreas, decision.MidSuggestion)
						}
					}
					// Stay in verification loop

				case "quit":
					return nil, fmt.Errorf("interview dibatalkan oleh user")
				}
			}

		case "continue", "clarify":
			if decision.NextQuestion == "" {
				return nil, fmt.Errorf("agent returned %q but no next_question", decision.Status)
			}
			currentQuestion = decision.NextQuestion

		default:
			return nil, fmt.Errorf("unexpected agent status: %q", decision.Status)
		}
	}

	// Hit max turns — generate with what we have
	printSection("⚠ Batas pertanyaan tercapai. Generating PRD dari informasi yang ada...")
	prd, err := a.generatePRD(ctx, systemPrompt, featureName, projectContext, history)
	if err != nil {
		return nil, fmt.Errorf("generating PRD: %w", err)
	}
	return &InterviewResult{
		PRDContent: prd,
		Turns:      len(history),
		Duration:   time.Since(start),
	}, nil
}

// decide asks the LLM what to do next and gets back the full review state.
func (a *PRDInterviewAgent) decide(
	ctx context.Context,
	systemPrompt string,
	featureName string,
	projectContext string,
	history []turn,
) (*agentDecision, error) {

	historyJSON, _ := json.MarshalIndent(history, "", "  ")

	userMsg := fmt.Sprintf(`Feature: %s

Project Context:
%s

Interview history so far:
%s

Based on the answers collected:
1. Build the complete collected_points list (all confirmed/inferred/assumed info so far)
2. List missing_areas that are still needed for a complete PRD
3. Decide: do you have enough for a robust PRD?
   - YES → status = "complete", include full PRD in prd_content
   - NO  → status = "continue", provide the single most important next_question

Return ONLY valid JSON matching the agentDecision schema.`,
		featureName, projectContext, string(historyJSON))

	resp, err := a.Complete(ctx, llm.Request{
		SystemPrompt: systemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: userMsg}},
		MaxTokens:    8192,
		Temperature:  0.3,
	})
	if err != nil {
		return nil, err
	}

	jsonStr := ExtractJSON(resp.Content)
	var decision agentDecision
	if err := json.Unmarshal([]byte(jsonStr), &decision); err != nil {
		return nil, fmt.Errorf("parsing decision JSON: %w\nraw:\n%s", err, resp.Content)
	}
	return &decision, nil
}

// generatePRD calls LLM to produce the final PRD from history (fallback).
func (a *PRDInterviewAgent) generatePRD(
	ctx context.Context,
	systemPrompt string,
	featureName string,
	projectContext string,
	history []turn,
) (string, error) {

	historyJSON, _ := json.MarshalIndent(history, "", "  ")

	userMsg := fmt.Sprintf(`Feature: %s

Project Context:
%s

Complete interview history:
%s

Generate the full PRD document now.
Return ONLY valid JSON: {"status":"complete","collected_points":[],"missing_areas":[],"prd_content":"...full markdown..."}`,
		featureName, projectContext, string(historyJSON))

	resp, err := a.Complete(ctx, llm.Request{
		SystemPrompt: systemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: userMsg}},
		MaxTokens:    8192,
		Temperature:  0.2,
	})
	if err != nil {
		return "", err
	}

	jsonStr := ExtractJSON(resp.Content)
	var decision agentDecision
	if err := json.Unmarshal([]byte(jsonStr), &decision); err != nil {
		return resp.Content, nil
	}
	if decision.PRDContent == "" {
		return resp.Content, nil
	}
	return decision.PRDContent, nil
}

// ask prints the question and reads the user's answer from stdin.
func (a *PRDInterviewAgent) ask(question string, turnNum int) (string, error) {
	fmt.Println()
	fmt.Printf("\033[1;36m[%d]\033[0m %s\n", turnNum, question)
	fmt.Print("\033[33m→ \033[0m")

	var lines []string
	for {
		if !a.scanner.Scan() {
			break
		}
		line := a.scanner.Text()
		if line == "" && len(lines) > 0 {
			break
		}
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if !strings.HasSuffix(line, ",") &&
			!strings.HasSuffix(line, ":") &&
			!strings.HasSuffix(line, "-") {
			break
		}
	}

	if err := a.scanner.Err(); err != nil {
		return "", err
	}

	answer := strings.TrimSpace(strings.Join(lines, " "))
	if answer == "" {
		answer = "(tidak dijawab)"
	}
	return answer, nil
}

// ── Final verification ────────────────────────────────────────────────────────

// printFinalVerification shows the full PRD outline and comprehensive product
// suggestions before the PM decides to generate or add more.
func printFinalVerification(points []collectedPoint, outline []prdOutlineSection, suggestions []productSuggestion) {
	fmt.Println()
	fmt.Println("\033[1;34m" + strings.Repeat("━", 56) + "\033[0m")
	fmt.Println("\033[1m  Verifikasi Akhir — Review sebelum PRD digenerate\033[0m")
	fmt.Println("\033[1;34m" + strings.Repeat("━", 56) + "\033[0m")

	// ── PRD Outline ───────────────────────────────────────────────────────────
	if len(outline) > 0 {
		fmt.Println()
		for _, section := range outline {
			fmt.Printf("\033[1;36m▸ %s\033[0m\n", section.Section)
			for _, point := range section.Points {
				if len(point) > 62 {
					fmt.Printf("  • %s\n    \033[2m%s\033[0m\n", point[:59]+"...", point[59:])
				} else {
					fmt.Printf("  • %s\n", point)
				}
			}
			fmt.Println()
		}
	} else {
		fmt.Println()
		for _, p := range points {
			icon, color := confidenceIcon(p.Confidence)
			fmt.Printf("  %s%s\033[0m \033[2m%-18s\033[0m %s\n",
				color, icon, p.Topic+":", p.Summary)
		}
		fmt.Println()
	}

	// ── Assumed points warning ────────────────────────────────────────────────
	var assumed []collectedPoint
	for _, p := range points {
		if p.Confidence == "assumed" {
			assumed = append(assumed, p)
		}
	}
	if len(assumed) > 0 {
		fmt.Println("\033[33m⚠ Diasumsikan agent — mohon verifikasi:\033[0m")
		for _, p := range assumed {
			fmt.Printf("  \033[33m? %-18s\033[0m %s\n", p.Topic+":", p.Summary)
		}
		fmt.Println()
	}

	// ── Product suggestions ───────────────────────────────────────────────────
	if len(suggestions) > 0 {
		fmt.Println("\033[2m" + strings.Repeat("─", 56) + "\033[0m")
		fmt.Println("\033[1m💡 Saran Product (dari perspektif senior product engineer):\033[0m")
		fmt.Println()

		// Group by severity: critical first, then consider, then optional
		for _, severity := range []string{"critical", "consider", "optional"} {
			var group []productSuggestion
			for _, s := range suggestions {
				if s.Severity == severity {
					group = append(group, s)
				}
			}
			if len(group) == 0 {
				continue
			}

			icon, color := suggestionIcon(severity)
			label := map[string]string{
				"critical": "PERLU DIPERTIMBANGKAN",
				"consider": "DISARANKAN",
				"optional": "OPSIONAL",
			}[severity]

			fmt.Printf("  %s%s %s\033[0m\n", color, icon, label)
			for _, s := range group {
				fmt.Printf("  \033[2m[%s]\033[0m\n", s.Area)
				fmt.Printf("  %s\n", s.Observation)
				fmt.Printf("  \033[2m→ %s\033[0m\n", s.Suggestion)
				fmt.Println()
			}
		}
	}
}

// confirmWithUser asks PM to confirm, add more, or quit.
// Returns "generate" | "more" | "quit"
func confirmWithUser() string {
	fmt.Println("\033[2m" + strings.Repeat("─", 56) + "\033[0m")
	fmt.Println()
	fmt.Println("  \033[1m[g]\033[0m Generate PRD sekarang")
	fmt.Println("  \033[1m[m]\033[0m Tambah / koreksi sesuatu dulu")
	fmt.Println("  \033[1m[q]\033[0m Batalkan")
	fmt.Println()
	fmt.Print("\033[33mPilihan [g/m/q]:\033[0m ")

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input := strings.TrimSpace(strings.ToLower(scanner.Text()))
		switch input {
		case "g", "generate", "y", "yes", "":
			return "generate"
		case "m", "more", "tambah", "edit":
			return "more"
		case "q", "quit", "batal", "n", "no":
			return "quit"
		default:
			fmt.Print("\033[33mKetik g (generate), m (more), atau q (quit):\033[0m ")
		}
	}
	return "quit"
}

// ── Review panel ──────────────────────────────────────────────────────────────

// printReviewPanel renders collected points, missing areas, and one
// mid-interview product suggestion after each user answer.
func printReviewPanel(points []collectedPoint, missing []string, suggestion *productSuggestion) {
	if len(points) == 0 && len(missing) == 0 {
		return
	}

	fmt.Println()
	fmt.Println("\033[2m" + strings.Repeat("─", 56) + "\033[0m")
	fmt.Println("\033[1m📋 Review — yang sudah terkumpul:\033[0m")
	fmt.Println()

	if len(points) > 0 {
		for _, p := range points {
			icon, color := confidenceIcon(p.Confidence)
			summary := p.Summary
			if len(summary) > 58 {
				summary = summary[:55] + "..."
			}
			fmt.Printf("  %s%s\033[0m \033[2m%-14s\033[0m %s\n",
				color, icon, p.Topic+":", summary)
		}
	}

	if len(missing) > 0 {
		fmt.Println()
		fmt.Printf("  \033[33m⚠ Belum ada:\033[0m\n")
		for _, m := range missing {
			fmt.Printf("    \033[2m· %s\033[0m\n", m)
		}
	}

	// Mid-interview product suggestion
	if suggestion != nil {
		fmt.Println()
		icon, color := suggestionIcon(suggestion.Severity)
		fmt.Printf("  %s%s \033[1m[%s]\033[0m \033[2m%s\033[0m\n",
			color, icon, suggestion.Area, "\033[0m")
		fmt.Printf("  %s  %s\033[0m\n", color, suggestion.Observation)
		fmt.Printf("  \033[2m  → %s\033[0m\n", suggestion.Suggestion)
	}

	fmt.Println("\033[2m" + strings.Repeat("─", 56) + "\033[0m")
}

func confidenceIcon(confidence string) (string, string) {
	switch confidence {
	case "confirmed":
		return "✓", "\033[32m" // green
	case "inferred":
		return "~", "\033[36m" // cyan
	case "assumed":
		return "?", "\033[33m" // yellow
	default:
		return "·", "\033[2m"
	}
}

func suggestionIcon(severity string) (string, string) {
	switch severity {
	case "critical":
		return "⚠", "\033[31m" // red
	case "consider":
		return "💡", "\033[33m" // yellow
	case "optional":
		return "○", "\033[2m" // dim
	default:
		return "·", "\033[2m"
	}
}

// ── Terminal helpers ──────────────────────────────────────────────────────────

func printInterviewHeader(featureName string) {
	fmt.Println()
	fmt.Println("\033[1;34m" + strings.Repeat("━", 56) + "\033[0m")
	fmt.Printf("\033[1m  PRD Builder — %s\033[0m\n", featureName)
	fmt.Println("\033[1;34m" + strings.Repeat("━", 56) + "\033[0m")
	fmt.Println()
	fmt.Println("  Jawab setiap pertanyaan dengan detail.")
	fmt.Println("  Tekan Enter untuk lanjut ke pertanyaan berikutnya.")
	fmt.Println("  Untuk jawaban panjang, akhiri dengan baris kosong.")
	fmt.Println()
	fmt.Println("\033[2m  Legend review panel:\033[0m")
	fmt.Println("\033[32m  ✓ confirmed\033[0m  \033[36m~ inferred\033[0m  \033[33m? assumed\033[0m")
	fmt.Println()
}

func printSection(msg string) {
	fmt.Printf("\n\033[1;32m%s\033[0m\n", msg)
}

func printMuted(msg string) {
	fmt.Printf("\033[2m  %s\033[0m\n", msg)
}
