package ops

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/giovannialves/corvex/internal/recipe"
	"github.com/giovannialves/corvex/internal/types"
)

// RecipeSummary is one row of `corvex recipe list`.
//
// Problem carries a parse or validation failure instead of returning it,
// because a listing that dies on the first broken YAML hides every healthy
// recipe next to it — the same rule the gate inbox follows for a deleted
// repository.
type RecipeSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Stages      int    `json:"stages"`
	Gates       int    `json:"gates"`
	Path        string `json:"path"`
	Compiled    bool   `json:"compiled"`
	Problem     string `json:"problem,omitempty"`
	// Inputs are the variables the recipe says it needs (`requires: - env:`).
	//
	// They are on the catalogue because a screen that offers to start a run has
	// to be able to ASK for them. Without this, the UI could dispatch a recipe
	// and the run would die in its own preflight saying `env INCIDENT_ID —
	// missing`, and the person would go to a terminal to do what they had just
	// tried to do on the screen.
	Inputs []RecipeInput `json:"inputs,omitempty"`
}

// RecipeInput is one declared input: the NAME the runner needs set, and the
// recipe author's one-line reason. Never a value — the catalogue reads a recipe,
// and a recipe holds no values.
type RecipeInput struct {
	Name string `json:"name"`
	Why  string `json:"why,omitempty"`
}

// RecipeStageView is one stage as the catalogue shows it: the shape of the work
// and the decisions attached to it, which after F2 are two different axes.
type RecipeStageView struct {
	ID        string   `json:"id"`
	Title     string   `json:"title,omitempty"`
	Kind      string   `json:"kind"`
	DependsOn []string `json:"depends_on,omitempty"`
	Command   string   `json:"command,omitempty"`
	Gates     []string `json:"gates,omitempty"`
	Evidence  []string `json:"evidence,omitempty"`
	FanOut    bool     `json:"fan_out,omitempty"`
}

// RecipeDetail is `corvex recipe show`.
type RecipeDetail struct {
	RecipeSummary
	StageList []RecipeStageView `json:"stage_list"`
}

// RecipesDir is where recipe YAML lives.
func RecipesDir(workDir string) string { return filepath.Join(workDir, ".corvex", "recipes") }

// RecipeNames lists the recipes of a workspace by name, sorted. It is also what
// shell completion calls, so it must stay cheap and never fail.
func RecipeNames(workDir string) []string {
	entries, err := os.ReadDir(RecipesDir(workDir))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	sort.Strings(names)
	return names
}

// ListRecipes inventories the catalogue, marking which recipes are already
// compiled into a project.
func ListRecipes(workDir string) []RecipeSummary {
	names := RecipeNames(workDir)
	out := make([]RecipeSummary, 0, len(names))
	for _, name := range names {
		s := RecipeSummary{Name: name, Path: RecipePath(workDir, name)}
		if _, err := os.Stat(filepath.Join(ProjectDir(workDir, name), "tasks.md")); err == nil {
			s.Compiled = true
		}
		r, err := loadRecipe(workDir, name)
		if err != nil {
			s.Problem = err.Error()
			out = append(out, s)
			continue
		}
		s.Description, s.Stages = r.Description, len(r.Stages)
		for _, st := range r.Stages {
			s.Gates += len(st.Gates)
		}
		for _, req := range r.Requires {
			if name := strings.TrimSpace(req.Env); name != "" {
				s.Inputs = append(s.Inputs, RecipeInput{Name: name, Why: req.Why})
			}
		}
		out = append(out, s)
	}
	return out
}

// ShowRecipe reads one recipe and describes its stages. It does NOT compile:
// reading a recipe must never write a tasks.md, because tasks.md is state
// (F2 learned this the expensive way) and inspecting a plan cannot be allowed
// to reset one.
func ShowRecipe(workDir, name string) (RecipeDetail, error) {
	r, err := loadRecipe(workDir, name)
	if err != nil {
		return RecipeDetail{}, err
	}
	detail := RecipeDetail{RecipeSummary: RecipeSummary{
		Name: name, Description: r.Description, Stages: len(r.Stages), Path: RecipePath(workDir, name),
	}}
	if _, serr := os.Stat(filepath.Join(ProjectDir(workDir, name), "tasks.md")); serr == nil {
		detail.Compiled = true
	}
	for _, st := range r.Stages {
		view := RecipeStageView{
			ID: st.ID, Title: st.Title, Kind: stageKind(st), DependsOn: st.DependsOn,
			Command: st.Command, FanOut: st.Fanout != nil,
		}
		for _, g := range st.Gates {
			view.Gates = append(view.Gates, string(g.Nature)+"/"+string(g.EffectiveWhen()))
			detail.Gates++
		}
		for _, e := range st.Evidence {
			label := e.Label
			if e.RequiredReading {
				label += " ★"
			}
			view.Evidence = append(view.Evidence, label)
		}
		detail.StageList = append(detail.StageList, view)
	}
	return detail, nil
}

// ValidateRecipe parses, validates and compiles a recipe in memory. Compiling is
// part of validation because a recipe can parse and still fail to become a DAG,
// and finding that out at `run start` costs a run.
func ValidateRecipe(workDir, name string) (int, error) {
	r, err := validateRecipeIn(workDir, name)
	if err != nil {
		return 0, err
	}
	tasks, _, err := r.Compile()
	if err != nil {
		return 0, err
	}
	return len(tasks), nil
}

func loadRecipe(workDir, name string) (*recipe.Recipe, error) {
	path := RecipePath(workDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &RecipeNotFoundError{Path: path}
		}
		return nil, err
	}
	r, err := recipe.Parse(data)
	if err != nil {
		return nil, err
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return r, nil
}

// validateRecipeIn is loadRecipe plus the checks that need the directory. It is
// separate because loadRecipe is on the read path of the inbox — which walks
// every recipe of every repository — and the cross-file check lists a directory
// per call.
func validateRecipeIn(workDir, name string) (*recipe.Recipe, error) {
	r, err := loadRecipe(workDir, name)
	if err != nil {
		return nil, err
	}
	if err := CheckNextTargets(workDir, r); err != nil {
		return nil, err
	}
	return r, nil
}

// stageKind reports the kind as written, defaulting to the taxonomy's default
// rather than to an empty cell.
func stageKind(s recipe.Stage) string {
	if s.Kind == "" {
		return string(types.KindCode)
	}
	return s.Kind
}
