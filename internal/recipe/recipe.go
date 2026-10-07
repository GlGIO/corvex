// Package recipe compiles declarative, reusable workflow recipes into a fixed
// task DAG. A recipe describes an opinionated pipeline (its stages and their
// dependencies) up front, so a run skips the AI Planner entirely and executes
// a deterministic graph — the foundation for Corvex's "workflow recipes"
// (CH-12). Advanced stage kinds (command, human-gate) and custom roles build
// on the Stage type defined here.
package recipe

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/giovannialves/corvex/internal/types"
)

// Recipe is a declarative pipeline definition, typically loaded from a YAML
// file under .corvex/recipes/<name>.yaml.
type Recipe struct {
	Name        string  `yaml:"name"`
	Description string  `yaml:"description"`
	Stages      []Stage `yaml:"stages"`

	// Requires is what this recipe needs on the machine before it is worth
	// spending a token (F9, "preflight de dependência declarada").
	//
	// The scar it answers: a run that discovers halfway through that a CLI is
	// missing has already paid for everything up to that point, and the failure
	// arrives dressed as a task failure — the agent retries, fails again, and
	// the retry budget is spent on a problem no model can solve.
	Requires []Requirement `yaml:"requires"`

	// Next is the leg that follows this one when the run ends clean.
	//
	// It exists because the handoff was prose. Every recipe in the field ends
	// with a step that PRINTS the next command — `Próximo: corvex run start
	// ship` — into a log nobody reads after the run stops being interesting.
	// MEASURED: a real incident ran to `done`, the fix sat on a branch on one
	// machine, and the owner came back with "cadê o PR?". Nothing was broken;
	// the thread was simply dropped, because the only thing holding it was a
	// sentence.
	//
	// So the chain is data. The runner records it when the run finishes, the
	// inbox shows it as something waiting on a person — which is what it is —
	// and it stays there until a run of that recipe actually starts in that
	// checkout.
	Next []NextStep `yaml:"next"`
}

// NextStep is one leg that may follow this recipe.
type NextStep struct {
	// Recipe is the name of the recipe to run next. It must exist in the same
	// `.corvex/recipes/` directory: a chain pointing at nothing is a chain that
	// breaks at the one moment nobody is watching, and `recipe validate`
	// refuses it.
	Recipe string `yaml:"recipe"`
	// Why is the one line the person reads when deciding whether to take this
	// leg now. It is shown beside the button.
	Why string `yaml:"why"`
	// When restricts the suggestion to an ending: `done` (the default — the
	// recipe finished everything), `partial` (it left work pending), or `any`.
	//
	// The distinction is not decoration. `ship` after a `done` incident is the
	// next leg; `ship` after a PARTIAL one would publish a fix whose proof did
	// not finish running.
	When string `yaml:"when"`
}

// EffectiveWhen is the ending this step applies to, with the default spelled
// out. `done` is the default because a chain is a statement about success.
func (n NextStep) EffectiveWhen() string {
	if n.When == "" {
		return "done"
	}
	return n.When
}

// AppliesTo reports whether this step is the one to offer for an ending.
func (n NextStep) AppliesTo(status string) bool {
	w := n.EffectiveWhen()
	return w == "any" || w == status
}

// Requirement is one declared dependency. Exactly one field is set.
type Requirement struct {
	// Bin is an executable the run needs: either a name on PATH ("az",
	// "docker", "psql") or a path relative to the run's workDir
	// ("scripts/deploy.sh"), which is the directory a stage's command runs in.
	// An absolute path is taken as given.
	Bin string `yaml:"bin"`
	// Env is a variable that must be set and non-empty in the RUNNER's
	// environment. Only the name is ever read, never the value — and the value
	// does not have to reach the worker at all (see security.runner_only_env).
	Env string `yaml:"env"`
	// MCP is an MCP server the run needs declared in `.corvex/config.yaml`
	// (`mcp_servers:`), by name.
	//
	// It exists because "the agent has access to production data" is a
	// dependency exactly like a CLI being installed, and it is the one that
	// fails most expensively: the reference flow records an agent burning 85k
	// tokens to conclude "I could not prove it, the database MCP does not exist
	// in my environment". A recipe that needs a server says so, and the check
	// runs before the first token.
	//
	// It is checked against the RESOLVED config, not by reading the YAML: a
	// guard that greps for a key is a second spelling of the rule, and the first
	// version of this one grepped `mcp:` while the key is `mcp_servers:` — it
	// would have refused a correctly configured repo and passed a misspelled
	// one.
	MCP string `yaml:"mcp"`
	// Pattern, on an `env` requirement only, is what the VALUE must look like
	// (a Go regexp, matched against the whole value). Being set is not being
	// right: an INCIDENT_ID with a digit missing runs the whole recipe against
	// another incident, and every token of it is spent on the wrong work.
	// The check reads the value and never prints it — the failure names the
	// pattern, which is the recipe's, not the value, which may be anything.
	Pattern string `yaml:"pattern"`
	// Why is the one-line reason, printed when the check fails. Optional, and
	// worth writing: "az is how ship opens the PR" turns a missing binary from
	// a puzzle into an instruction.
	Why string `yaml:"why"`
}

// PatternRegexp compiles Pattern anchored at both ends: `[0-9]{5}` means "five
// digits", not "contains five digits somewhere", which is what an unanchored
// regexp would quietly accept.
func (r Requirement) PatternRegexp() (*regexp.Regexp, error) {
	return regexp.Compile(`^(?:` + r.Pattern + `)$`)
}

// Stage is one node of the recipe pipeline. Kind defaults to "task" (an AI
// worker task). Future kinds ("command", "human-gate") are reserved and
// validated here so an unknown kind fails fast rather than silently running as
// a task.
type Stage struct {
	ID          string   `yaml:"id"`
	Title       string   `yaml:"title"`
	Kind        string   `yaml:"kind"` // code | tool | test | repro; legacy: task, command, human-gate
	Type        string   `yaml:"type"` // task type for routing (backend, frontend, ...)
	DependsOn   []string `yaml:"depends_on"`
	Description string   `yaml:"description"`
	Criteria    []string `yaml:"criteria"`
	Command     string   `yaml:"command"` // shell command run when the stage is computational
	// Timeout overrides the run-wide per-task wall clock for this stage
	// ("45m", "2h"). Empty inherits execution.task_timeout_minutes.
	Timeout string `yaml:"timeout"`
	Loop    *Loop  `yaml:"loop"` // optional loop-with-policy (command stages)

	// Gates are the decisions attached to this stage: computational,
	// inferential, human or policy. See types.Gate.
	Gates []types.Gate `yaml:"gates"`
	// Evidence is what the stage hands whoever stands at its gates.
	Evidence []types.Evidence `yaml:"evidence"`
	// Fanout expands this stage into N instances of a template, over items
	// discovered at run time.
	Fanout *types.Fanout `yaml:"fanout"`
	// Produces names what this stage's output feeds — "items" makes it a
	// fan-out source.
	Produces string `yaml:"produces"`
	// FixedBy is the stage expected to make a `repro` command stop
	// reproducing.
	FixedBy string `yaml:"fixed_by"`
	// Always re-executes this stage on every run — and everything downstream of
	// it — even when the last run left it PASSED.
	//
	// For stages whose value is in OBSERVING rather than in building: a
	// post-deploy probe re-reads production to answer "did the failure stop",
	// and a probe skipped because it passed yesterday reports success without
	// having looked. Resumability is right for work and wrong for measurement.
	Always bool `yaml:"always,omitempty"`
}

// Loop is a command stage's loop-with-policy: re-run the command until Until
// (a shell condition) exits 0, or until Max iterations.
type Loop struct {
	Until string `yaml:"until"` // optional; empty → the command's own exit governs
	Max   int    `yaml:"max"`   // max iterations (default 3)
}

// knownKinds enumerates the stage kinds the compiler accepts. The four on top
// are the F2 taxonomy; the three below them are the pre-F2 spelling, accepted
// forever and normalised at dispatch (types.NormalizeKind) rather than at
// compile time — tasks.md keeps the word the user wrote.
var knownKinds = map[string]bool{
	"": true, // defaults to code

	string(types.KindCode):  true,
	string(types.KindTool):  true,
	string(types.KindTest):  true,
	string(types.KindRepro): true,

	types.LegacyKindTask:      true,
	types.LegacyKindCommand:   true,
	types.LegacyKindHumanGate: true,
}

// knownKindList is the human-readable enumeration used in error messages.
const knownKindList = "code, tool, test, repro (legacy: task, command, human-gate)"

// Parse decodes a recipe from YAML bytes.
func Parse(data []byte) (*Recipe, error) {
	var r Recipe
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parsing recipe: %w", err)
	}
	return &r, nil
}

// Validate checks the recipe is internally consistent: it has stages, every ID
// is unique and non-empty, every dependency references a declared stage, kinds
// are known, and the dependency graph is acyclic.
func (r *Recipe) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("recipe: name is required")
	}
	if len(r.Stages) == 0 {
		return fmt.Errorf("recipe %q: has no stages", r.Name)
	}

	for _, s := range r.Stages {
		if s.Timeout == "" {
			continue
		}
		d, err := time.ParseDuration(s.Timeout)
		if err != nil {
			return fmt.Errorf("recipe %q: stage %q has an unreadable timeout %q: use a duration like 45m or 2h", r.Name, s.ID, s.Timeout)
		}
		if d <= 0 {
			return fmt.Errorf("recipe %q: stage %q has timeout %q — a non-positive timeout would kill the step before it started; omit it to inherit the run's", r.Name, s.ID, s.Timeout)
		}
	}

	// A chain that points at nothing breaks at the one moment nobody is
	// watching: the end of a run, when the person has stopped reading. The
	// existence of the target is checked elsewhere (it needs the recipe
	// directory); what is checked here is everything readable from this file
	// alone.
	for i, n := range r.Next {
		if strings.TrimSpace(n.Recipe) == "" {
			return fmt.Errorf("recipe %q: next[%d] names no recipe", r.Name, i)
		}
		if n.Recipe == r.Name {
			return fmt.Errorf("recipe %q: next[%d] points at itself — a run that ends by suggesting itself is a loop with a person in it", r.Name, i)
		}
		switch n.EffectiveWhen() {
		case "done", "partial", "any":
		default:
			return fmt.Errorf("recipe %q: next[%d] has when: %q — use `done` (the recipe finished everything), `partial` (it left work pending) or `any`", r.Name, i, n.When)
		}
		if strings.TrimSpace(n.Why) == "" {
			return fmt.Errorf("recipe %q: next[%d] has no `why` — it is the line a person reads when deciding to take this leg, and without it the button says only a name", r.Name, i)
		}
	}

	// One requirement names ONE thing. The rule is worth keeping as the kinds
	// grow — `bin`, `env` and now `mcp` — because the whole value of a preflight
	// is a failure that points at a single fix, and a requirement carrying two
	// names reports the wrong one half the time.
	for i, req := range r.Requires {
		var named []string
		for _, f := range []struct{ key, value string }{
			{"bin", req.Bin}, {"env", req.Env}, {"mcp", req.MCP},
		} {
			if strings.TrimSpace(f.value) != "" {
				named = append(named, "`"+f.key+"`")
			}
		}
		if strings.TrimSpace(req.Pattern) != "" {
			if strings.TrimSpace(req.Env) == "" {
				return fmt.Errorf("recipe %q: requires[%d] has a `pattern` but no `env` — a pattern describes a value, and only an env requirement has one", r.Name, i)
			}
			if _, err := req.PatternRegexp(); err != nil {
				return fmt.Errorf("recipe %q: requires[%d] (%s) has a pattern that does not compile: %v", r.Name, i, req.Env, err)
			}
		}
		switch {
		case len(named) == 0:
			return fmt.Errorf("recipe %q: requires[%d] declares none of `bin`, `env` or `mcp`", r.Name, i)
		case len(named) > 1:
			return fmt.Errorf("recipe %q: requires[%d] declares %s — split it in two, so the failure names one thing",
				r.Name, i, strings.Join(named, " and "))
		}
	}

	ids := make(map[string]bool, len(r.Stages))
	for _, s := range r.Stages {
		if strings.TrimSpace(s.ID) == "" {
			return fmt.Errorf("recipe %q: a stage is missing an id", r.Name)
		}
		if ids[s.ID] {
			return fmt.Errorf("recipe %q: duplicate stage id %q", r.Name, s.ID)
		}
		if err := r.validateStage(s); err != nil {
			return err
		}
		ids[s.ID] = true
	}

	for _, s := range r.Stages {
		for _, dep := range s.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("recipe %q: stage %q depends on unknown stage %q", r.Name, s.ID, dep)
			}
			if dep == s.ID {
				return fmt.Errorf("recipe %q: stage %q depends on itself", r.Name, s.ID)
			}
		}
		if err := r.validateStageLinks(s, ids); err != nil {
			return err
		}
	}

	return r.checkAcyclic()
}

func (r *Recipe) checkAcyclic() error {
	deps := make(map[string][]string, len(r.Stages))
	inDeg := make(map[string]int, len(r.Stages))
	for _, s := range r.Stages {
		deps[s.ID] = s.DependsOn
		inDeg[s.ID] = len(s.DependsOn)
	}
	queue := make([]string, 0)
	for id, d := range inDeg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	// Build reverse edges (dependency → dependents).
	dependents := make(map[string][]string)
	for id, ds := range deps {
		for _, dep := range ds {
			dependents[dep] = append(dependents[dep], id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for _, dep := range dependents[cur] {
			inDeg[dep]--
			if inDeg[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}
	if visited != len(r.Stages) {
		return fmt.Errorf("recipe %q: dependency cycle detected", r.Name)
	}
	return nil
}

// Compile turns a validated recipe into the task list and DAG spec that the
// orchestrator consumes — the same shapes the AI Planner would produce, but
// deterministic. All tasks start PENDING.
func (r *Recipe) Compile() ([]types.Task, types.DAGSpec, error) {
	if err := r.Validate(); err != nil {
		return nil, types.DAGSpec{}, err
	}

	tasks := make([]types.Task, 0, len(r.Stages))
	dag := types.DAGSpec{
		GeneratedBy:  "corvex-recipe:" + r.Name,
		Dependencies: make(map[string][]string, len(r.Stages)),
	}

	for _, s := range r.Stages {
		typ := types.TaskType(s.Type)
		if typ == "" {
			typ = types.TypeGeneral
		}
		deps := s.DependsOn
		if deps == nil {
			deps = []string{}
		}
		t := types.Task{
			ID:          s.ID,
			Title:       s.Title,
			Status:      types.StatusPending,
			Type:        typ,
			DependsOn:   deps,
			Description: s.Description,
			Criteria:    s.Criteria,
			Kind:        s.Kind,
			Command:     s.Command,
			Gates:       s.Gates,
			Evidence:    s.Evidence,
			Fanout:      s.Fanout,
			Produces:    s.Produces,
			Always:      s.Always,
			FixedBy:     s.FixedBy,
			Timeout:     s.Timeout,
		}
		if s.Loop != nil {
			t.LoopUntil = s.Loop.Until
			t.LoopMax = s.Loop.Max
			if t.LoopMax == 0 {
				t.LoopMax = 3 // default loop cap
			}
		}
		tasks = append(tasks, t)
		dag.Dependencies[s.ID] = deps
	}

	tasks, dag = expandRepro(tasks, dag)
	return tasks, dag, nil
}
