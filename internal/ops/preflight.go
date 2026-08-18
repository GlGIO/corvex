package ops

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/giovannialves/corvex/internal/recipe"
)

// Requirement checks (F9): what a recipe declared it needs, and whether it is
// there — asked BEFORE the first token.
//
// The scar this answers is the first one in the roadmap's list: a run that
// discovers a missing CLI halfway through has already paid for everything up to
// that point, and the failure arrives dressed as a task failure. The agent then
// retries something no model can fix, and the retry budget goes with it.

// RequirementCheck is one declared dependency and what was found.
type RequirementCheck struct {
	Kind string `json:"kind"` // "bin" or "env"
	Name string `json:"name"`
	Why  string `json:"why,omitempty"`
	OK   bool   `json:"ok"`
	// Detail says what was found, never what it contains: for an env variable
	// this is "set" or "missing" and never the value. A preflight that printed
	// the credential it was checking for would be the leak it exists to prevent.
	Detail string `json:"detail"`
}

// PreflightRequirements reads the recipe behind a project and checks what it
// declared. A project with no recipe declares nothing, which is not a failure:
// the legacy spec.md path has nowhere to declare and must keep working.
func PreflightRequirements(workDir, project string) ([]RequirementCheck, error) {
	r, err := loadRecipe(workDir, project)
	if err != nil {
		var notFound *RecipeNotFoundError
		if ok := asRecipeNotFound(err, &notFound); ok {
			return nil, nil
		}
		// A recipe that exists but cannot be read is not this function's problem
		// to report: the compile step reports it with a better message.
		return nil, nil
	}
	return checkRequirements(r.Requires), nil
}

func checkRequirements(reqs []recipe.Requirement) []RequirementCheck {
	out := make([]RequirementCheck, 0, len(reqs))
	for _, req := range reqs {
		switch {
		case strings.TrimSpace(req.Bin) != "":
			c := RequirementCheck{Kind: "bin", Name: req.Bin, Why: req.Why, Detail: "not on PATH"}
			if path, err := exec.LookPath(req.Bin); err == nil {
				c.OK, c.Detail = true, path
			}
			out = append(out, c)
		case strings.TrimSpace(req.Env) != "":
			c := RequirementCheck{Kind: "env", Name: req.Env, Why: req.Why, Detail: "missing"}
			if v, ok := os.LookupEnv(req.Env); ok && strings.TrimSpace(v) != "" {
				c.OK, c.Detail = true, "set"
			}
			out = append(out, c)
		}
	}
	return out
}

// MissingRequirements formats the failures into one actionable error, or nil.
func MissingRequirements(checks []RequirementCheck) error {
	var lines []string
	for _, c := range checks {
		if c.OK {
			continue
		}
		line := fmt.Sprintf("  ✗ %s %s — %s", c.Kind, c.Name, c.Detail)
		if c.Why != "" {
			line += " (" + c.Why + ")"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil
	}
	return fmt.Errorf("this recipe declares dependencies that are not here:\n%s\n\n"+
		"→ install or export them, or edit `requires:` in the recipe.\n"+
		"  Nothing was spent: the check runs before the first token.",
		strings.Join(lines, "\n"))
}

func asRecipeNotFound(err error, target **RecipeNotFoundError) bool {
	nf, ok := err.(*RecipeNotFoundError)
	if ok {
		*target = nf
	}
	return ok
}
