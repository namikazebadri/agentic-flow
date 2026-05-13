package reporter

import (
	"fmt"
	"strings"
	"time"

	"github.com/agentflow/core/internal/registry"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

// Console is a terminal reporter with colored output.
type Console struct {
	noColor bool
	start   time.Time
}

func NewConsole(noColor bool) *Console {
	return &Console{noColor: noColor, start: time.Now()}
}

func (r *Console) Info(format string, args ...any) {
	fmt.Printf(r.c(colorCyan)+"  ℹ "+r.c(colorReset)+format+"\n", args...)
}

func (r *Console) Success(format string, args ...any) {
	fmt.Printf(r.c(colorGreen)+"  ✓ "+r.c(colorReset)+format+"\n", args...)
}

func (r *Console) Warning(format string, args ...any) {
	fmt.Printf(r.c(colorYellow)+"  ⚠ "+r.c(colorReset)+format+"\n", args...)
}

func (r *Console) Error(format string, args ...any) {
	fmt.Printf(r.c(colorRed)+"  ✗ "+r.c(colorReset)+format+"\n", args...)
}

func (r *Console) Section(title string) {
	elapsed := time.Since(r.start).Round(time.Second)
	fmt.Printf("\n%s%s━━━ %s %s[%s]%s\n",
		r.c(colorBold), r.c(colorBlue),
		title,
		r.c(colorCyan), elapsed,
		r.c(colorReset))
}

func (r *Console) DeliverableStart(d *registry.Deliverable) {
	fmt.Printf("\n%s  ▶ [%s] %s%s  (%s)\n",
		r.c(colorBold), d.ID, d.Title, r.c(colorReset), d.Complexity)
}

func (r *Console) DeliverableResult(d *registry.Deliverable, passed bool, detail string) {
	if passed {
		fmt.Printf(r.c(colorGreen)+"    ✓ Gate passed%s\n", r.c(colorReset))
	} else {
		fmt.Printf(r.c(colorRed)+"    ✗ Gate failed%s\n", r.c(colorReset))
		// Indent the detail output
		for _, line := range strings.Split(detail, "\n") {
			fmt.Printf("      %s\n", line)
		}
	}
}

func (r *Console) c(code string) string {
	if r.noColor {
		return ""
	}
	return code
}
