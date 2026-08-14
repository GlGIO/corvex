// Package wizard owns the interactive configuration of the `validate` section:
// it drafts a config from an AI pass over the codebase, asks the user to confirm
// whatever the AI was unsure about, and falls back to a fully manual
// questionnaire when inference is unavailable.
//
// It carries no cobra dependency and never touches os.Stdin/os.Stdout directly:
// a Wizard is constructed around an explicit reader and writer.
package wizard

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/planning"
	"github.com/giovannialves/corvex/internal/provider"
)

// Wizard asks the questions and prints the answers on the streams it was given.
type Wizard struct {
	in  *bufio.Reader
	out io.Writer
}

// New returns a Wizard reading from in and printing to out.
func New(in *bufio.Reader, out io.Writer) *Wizard {
	return &Wizard{in: in, out: out}
}

// Configured reports whether the validate section is already usable — a port OR
// a start command is enough, everything else is irrelevant. When it is true the
// wizard does not run at all.
func Configured(v config.ValidateConfig) bool {
	return v.Stack.Port != 0 || v.Stack.StartCommand != ""
}

// Run drafts a config with one AI call, confirms the uncertain fields with the
// user and writes the result to .corvex/config.yaml. When inference fails for
// any reason it degrades to the manual questionnaire instead of aborting.
func (w *Wizard) Run(ctx context.Context, workDir string, cfg *config.Config) error {
	fmt.Fprintln(w.out, "\nvalidate: not configured.")
	fmt.Fprintln(w.out, "Inspecting codebase to draft a config (one AI call)...")
	fmt.Fprintln(w.out)

	draft, err := w.infer(ctx, cfg, workDir)
	if err != nil {
		log.Warn("AI inference failed — falling back to manual wizard", "err", err)
		return w.Manual(workDir, cfg)
	}

	w.PrintDetected(draft.Detected)

	cfg.Validate = draft.Validate
	w.ConfirmUncertain(&cfg.Validate, draft.Uncertain)

	fmt.Fprintf(w.out, "\nEdit any field manually? (y/n) [n]: ")
	if raw, _ := w.in.ReadString('\n'); strings.TrimSpace(raw) == "y" {
		w.ManualOverride(&cfg.Validate)
	}

	if err := SaveConfig(workDir, cfg); err != nil {
		return err
	}
	fmt.Fprintf(w.out, "\n✓ Written to .corvex/config.yaml  (inference cost: $%.4f)\n\n", draft.CostUSD)
	return nil
}

// infer runs the one AI pass over the codebase that produces the draft config.
func (w *Wizard) infer(ctx context.Context, cfg *config.Config, workDir string) (*planning.ConfigDraft, error) {
	p, err := provider.NewProvider(cfg.Provider.Default, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating provider: %w", err)
	}
	c := planning.NewConfigurer(p, cfg.Provider.Models.Planner, workDir)
	c.SetProgressWriter(w.out)
	return c.InferValidate(ctx)
}

// Prompt asks one question, echoing the default in brackets when there is one.
// An empty answer keeps the default — and so does EOF, which is why a
// non-interactive run silently accepts every default.
func (w *Wizard) Prompt(question, defaultVal string) string {
	if defaultVal != "" {
		fmt.Fprintf(w.out, "  %s [%s]: ", question, defaultVal)
	} else {
		fmt.Fprintf(w.out, "  %s: ", question)
	}
	raw, err := w.in.ReadString('\n')
	if err != nil {
		return defaultVal
	}
	if answer := strings.TrimSpace(raw); answer != "" {
		return answer
	}
	return defaultVal
}

func parseIntDefault(s string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return fallback
}

func boolStr(b bool) string {
	if b {
		return "y"
	}
	return "n"
}
