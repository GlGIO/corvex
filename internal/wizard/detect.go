package wizard

import (
	"fmt"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/planning"
)

// PrintDetected lists what the AI found with high confidence, in the order it
// reported them. A field without a source prints without the "(from ...)" tail.
func (w *Wizard) PrintDetected(detected []planning.DetectedField) {
	if len(detected) == 0 {
		fmt.Fprintln(w.out, "(nothing auto-detected — please confirm/enter values manually)")
		return
	}
	fmt.Fprintln(w.out, "✓ Detected:")
	for _, d := range detected {
		if d.Source != "" {
			fmt.Fprintf(w.out, "  %s = %s  (from %s)\n", d.Field, d.Value, d.Source)
		} else {
			fmt.Fprintf(w.out, "  %s = %s\n", d.Field, d.Value)
		}
	}
}

// ConfirmUncertain asks the user about every field the AI was unsure of and
// routes the answers through ApplyFieldOverride — so a field name the switch
// does not know is asked about and then dropped in silence.
func (w *Wizard) ConfirmUncertain(v *config.ValidateConfig, uncertain []planning.UncertainField) {
	if len(uncertain) == 0 {
		return
	}
	fmt.Fprintln(w.out, "\n? Please confirm uncertain fields:")
	for _, u := range uncertain {
		hint := u.Guess
		if u.Reason != "" {
			hint = fmt.Sprintf("%s — %s", u.Guess, u.Reason)
		}
		answer := w.Prompt("  "+u.Field+" ("+hint+")", u.Guess)
		ApplyFieldOverride(v, u.Field, answer)
	}
}
