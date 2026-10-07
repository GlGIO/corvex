package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
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
	Kind string `json:"kind"` // "bin", "env" or "mcp"
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
	var cfg config.Config
	if loaded, _, cerr := LoadConfig(); cerr == nil && loaded != nil {
		cfg = *loaded
	}
	// A config that cannot be read is the compile step's error to report, with a
	// better message than this one could give. What must not happen is an `mcp:`
	// requirement passing because nothing was read — the zero value declares no
	// servers, so it refuses.
	return checkRequirementsWithConfig(workDir, r.Requires, cfg), nil
}

func checkRequirements(workDir string, reqs []recipe.Requirement) []RequirementCheck {
	return checkRequirementsWithConfig(workDir, reqs, config.Config{})
}

func checkRequirementsWithConfig(workDir string, reqs []recipe.Requirement, cfg config.Config) []RequirementCheck {
	out := make([]RequirementCheck, 0, len(reqs))
	for _, req := range reqs {
		switch {
		case strings.TrimSpace(req.Bin) != "":
			c := RequirementCheck{Kind: "bin", Name: req.Bin, Why: req.Why}
			target := resolveBin(workDir, req.Bin)
			path, err := exec.LookPath(target)
			if err == nil {
				c.OK, c.Detail = true, path
			} else {
				c.Detail = whyNotRunnable(req.Bin, target, err)
			}
			out = append(out, c)
		case strings.TrimSpace(req.MCP) != "":
			// Asked of the resolved config, never of the file: see the comment
			// on recipe.Requirement.MCP for the spelling this avoids.
			c := RequirementCheck{Kind: "mcp", Name: req.MCP, Why: req.Why, Detail: "not declared in mcp_servers:"}
			for _, srv := range cfg.Sandbox.MCPServers {
				if strings.EqualFold(strings.TrimSpace(srv.Name), strings.TrimSpace(req.MCP)) {
					c.OK, c.Detail = true, "declared"
					break
				}
			}
			out = append(out, c)
		case strings.TrimSpace(req.Env) != "":
			c := RequirementCheck{Kind: "env", Name: req.Env, Why: req.Why, Detail: "missing"}
			if v, ok := os.LookupEnv(req.Env); ok && strings.TrimSpace(v) != "" {
				c.OK, c.Detail = true, "set"
				if strings.TrimSpace(req.Pattern) != "" {
					// The detail names the pattern and never the value.
					if re, err := req.PatternRegexp(); err != nil || !re.MatchString(v) {
						c.OK, c.Detail = false, "set, but does not match "+req.Pattern
						if err == nil && re.MatchString(strings.TrimSpace(v)) {
							// The value is right and the spaces around it are not —
							// say that, or the fix looks like a different number.
							c.Detail = "set, but has leading or trailing whitespace"
						}
					}
				}
			}
			out = append(out, c)
		}
	}
	return out
}

// whyNotRunnable turns a LookPath failure into the sentence that sends the
// reader to the right fix: install it, or chmod it.
//
// # Why this cannot be read off the error
//
// exec.LookPath answers a bare name and a path with DIFFERENT vocabularies, and
// the difference is exactly where the interesting case lives:
//
//   - A path ("scripts/deploy.sh", "/usr/local/bin/az") is stat'd directly, so a
//     file without its execute bit comes back as fs.ErrPermission and a
//     directory comes back as syscall.EISDIR — two distinguishable failures.
//   - A bare name ("az") is searched along PATH, and every candidate that is not
//     runnable is skipped in silence: the walk ends with one flat "executable
//     file not found in $PATH" whether the machine has no `az` at all or has one
//     sitting in /usr/local/bin with mode 0644. Measured on go1.26 and asserted
//     by TestPreflight_BareBinFoundOnPATHButNotExecutable.
//
// So the honest answer for a bare name has to be recovered by walking PATH here.
// The cost is one stat per PATH entry — a dozen, typically — and it is paid only
// on the failure path, where the run is already being refused and nothing has
// been spent. A "not on PATH" that sends somebody to reinstall a CLI they
// already have is worth more than that.
//
// The `bin: az` in the README is precisely the bare-name case, which is why this
// is not an edge: it is the shape most recipes declare.
func whyNotRunnable(bin, target string, err error) string {
	if strings.ContainsRune(bin, '/') {
		// A path that is not there is not a PATH problem, and saying so sends
		// the reader to look at the wrong thing.
		if reason, ok := whyThisFileIsNotRunnable(target); ok {
			return reason
		}
		if errors.Is(err, fs.ErrPermission) {
			// The file is there and this process may not run it — the mode bits
			// can look fine and eaccess still refuse (owner, ACL, noexec mount),
			// so LookPath's own verdict wins over anything stat can see.
			return "found but not executable: " + target
		}
		return "not found: " + target
	}

	// A bare name: LookPath already failed, so nothing on PATH is runnable under
	// this name. Whatever we find here is therefore the reason it failed.
	dirHit := ""
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			// LookPath reads an empty PATH entry as the current directory.
			dir = "."
		}
		candidate := filepath.Join(dir, bin)
		reason, ok := whyThisFileIsNotRunnable(candidate)
		if !ok {
			continue
		}
		if strings.HasPrefix(reason, "found but is a directory") {
			// Keep looking: a directory shadowing the name is less actionable
			// than a real file a chmod away, and LookPath walks past it too.
			if dirHit == "" {
				dirHit = reason
			}
			continue
		}
		return reason
	}
	if dirHit != "" {
		return dirHit
	}
	return "not on PATH"
}

// whyThisFileIsNotRunnable reports why a file that EXISTS cannot be executed, or
// false when there is nothing there to talk about.
//
// The directory case is its own sentence rather than folded into "not found":
// `bin: scripts/` names something that IS on disk, and telling its author it is
// missing sends them to create a file that is already there under that name.
func whyThisFileIsNotRunnable(path string) (string, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return "found but is a directory: " + path, true
	}
	if fi.Mode().Perm()&0o111 == 0 {
		return "found but not executable: " + path, true
	}
	return "", false
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
