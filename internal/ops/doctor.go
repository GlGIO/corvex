package ops

import "github.com/giovannialves/corvex/internal/config"

// CheckStatus is the verdict of a single doctor check. The zero value is
// CheckPass so a check that returns without deciding reads as healthy.
type CheckStatus int

const (
	CheckPass CheckStatus = iota
	CheckWarn
	CheckFail
)

// CheckResult is what one doctor check found: which check ran, its verdict and
// the human-readable detail. Rendering (icon, colour, ordering) belongs to the
// caller — this is the fact, not the sentence.
type CheckResult struct {
	Name    string
	Status  CheckStatus
	Message string
}

// DoctorReport is the full outcome of `corvex doctor`: every check in the order
// it ran, plus the tally per verdict. Failed > 0 is what makes the CLI exit
// non-zero.
type DoctorReport struct {
	Checks   []CheckResult
	Passed   int
	Warnings int
	Failed   int
}

// Doctor runs every check against cfg and the environment rooted at workDir and
// returns the results together with their tally.
func Doctor(cfg *config.Config, workDir string) DoctorReport {
	rep := DoctorReport{Checks: AllChecks(cfg, workDir)}
	for _, r := range rep.Checks {
		switch r.Status {
		case CheckPass:
			rep.Passed++
		case CheckWarn:
			rep.Warnings++
		case CheckFail:
			rep.Failed++
		}
	}
	return rep
}

// AllChecks runs the config checks followed by the environment checks that need
// a working directory, in the order the CLI reports them.
func AllChecks(cfg *config.Config, workDir string) []CheckResult {
	results := ConfigChecks(cfg)
	results = append(results, CheckMCPGitignore(cfg, workDir))
	results = append(results, CheckSkills(cfg, workDir))
	return results
}

// ConfigChecks runs the checks that only need the config, no filesystem.
func ConfigChecks(cfg *config.Config) []CheckResult {
	return []CheckResult{
		CheckProvider(cfg),
		CheckModels(cfg),
		CheckSandbox(cfg),
		CheckEscalation(cfg),
		CheckCostCeilings(cfg),
	}
}
