package agent

import (
	"fmt"
	"math/rand"
	"os"
	"time"
)

var spinnerFrames = []string{
	"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏",
}

var thinkingPhrases = []string{
	"Berpikir",
	"Merenungkan",
	"Menganalisis jawaban",
	"Mengevaluasi kelengkapan",
	"Merangkai pertanyaan berikutnya",
	"Memetakan poin-poin penting",
	"Menyusun konteks",
	"Mempertimbangkan trade-off",
	"Memformulasikan saran",
	"Mengasah pemahaman",
	"Menjahit benang merah",
	"Menyelaraskan ekspektasi",
}

// Spinner is a TTY-only loading indicator with rotating phrases.
type Spinner struct {
	stopCh chan struct{}
	doneCh chan struct{}
	active bool
}

func NewSpinner() *Spinner {
	return &Spinner{
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start begins the animation. No-op when stdout is not a TTY (CI, pipes).
func (s *Spinner) Start() {
	if !isTerminal(os.Stdout) {
		return
	}
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.active = true
	go s.run()
}

// Stop halts the animation and clears the current line.
func (s *Spinner) Stop() {
	if !s.active {
		return
	}
	close(s.stopCh)
	<-s.doneCh
	s.active = false
}

func (s *Spinner) run() {
	defer close(s.doneCh)

	frame := 0
	phraseIdx := rand.Intn(len(thinkingPhrases))
	phraseStart := time.Now()

	frameTicker := time.NewTicker(80 * time.Millisecond)
	defer frameTicker.Stop()

	// \033[?25l hides the cursor; restore + clear line on exit so subsequent
	// output (errors, next question, review panel) starts on a clean line.
	fmt.Print("\033[?25l")
	defer fmt.Print("\r\033[K\033[?25h")

	for {
		select {
		case <-s.stopCh:
			return
		case <-frameTicker.C:
			if time.Since(phraseStart) > 1500*time.Millisecond {
				phraseIdx = (phraseIdx + 1) % len(thinkingPhrases)
				phraseStart = time.Now()
			}
			fmt.Printf("\r\033[K\033[36m%s\033[0m \033[2m%s...\033[0m",
				spinnerFrames[frame], thinkingPhrases[phraseIdx])
			frame = (frame + 1) % len(spinnerFrames)
		}
	}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
