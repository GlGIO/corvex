package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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
	return checkRequirements(workDir, r.Requires), nil
}

func checkRequirements(workDir string, reqs []recipe.Requirement) []RequirementCheck {
	out := make([]RequirementCheck, 0, len(reqs))
	for _, req := range reqs {
		switch {
		case strings.TrimSpace(req.Bin) != "":
			c := RequirementCheck{Kind: "bin", Name: req.Bin, Why: req.Why, Detail: "not on PATH"}
			target := resolveBin(workDir, req.Bin)
			if strings.ContainsRune(req.Bin, '/') {
				// A path that is not there is not a PATH problem, and saying so
				// sends the reader to look at the wrong thing.
				c.Detail = "not found: " + target
			}
			path, err := exec.LookPath(target)
			switch {
			case err == nil:
				c.OK, c.Detail = true, path
			case errors.Is(err, fs.ErrPermission):
				// LookPath reports the file it found but could not execute as an
				// error like any other. Reporting that as "not on PATH" is a lie
				// that costs an install attempt: the fix is chmod, not apt.
				c.Detail = "found but not executable: " + target
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

// resolveBin says which file the preflight has to stat for a declared `bin:`.
//
// A bare name ("az", "docker") is a PATH lookup and stays one. A name with a
// separator ("scripts/deploy.sh") is not: exec.LookPath skips PATH entirely for
// it and stats the path against the PROCESS working directory. But a relative
// `bin:` exists to be invoked by a stage's command, and the stage runs it with
// `sh -c` and cmd.Dir = the run's workDir (internal/step.Executor.runShell), so
// the check has to resolve against the same directory the executor will. Today
// every production route reaches the preflight with workDir == CWD, which means
// the old code was right by coincidence; an in-process caller that knows its
// workspace (ops.LoadConfigAt exists for exactly that) breaks the coincidence
// in both directions — refusing a script that is there, or approving one that
// is only in the caller's CWD.
//
// The result is absolute so what lands in the ledger still means something read
// from another directory.
func resolveBin(workDir, bin string) string {
	if !strings.ContainsRune(bin, '/') || filepath.IsAbs(bin) {
		return bin
	}
	abs, err := filepath.Abs(filepath.Join(workDir, bin))
	if err != nil {
		return filepath.Join(workDir, bin)
	}
	return abs
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
