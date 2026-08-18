package ops

import (
	"time"

	"github.com/giovannialves/corvex/internal/activity"
	"github.com/giovannialves/corvex/internal/run"
)

// ProjectRow is `corvex run list --projects`: one project of this repository,
// with the state of its plan and its last execution.
//
// It exists because a planned project that never ran is a real state that a
// listing of runs cannot show — `corvex plan x` and nothing else. F3 (D11) chose
// a flag that changes the unit of the line over promoting `project` to a fourth
// noun, and wrote down that this is the only place where a flag does that.
type ProjectRow struct {
	Name      string     `json:"name"`
	HasSpec   bool       `json:"has_spec"`
	HasTasks  bool       `json:"has_tasks"`
	Status    string     `json:"status"`
	Completed int        `json:"completed"`
	Total     int        `json:"total"`
	CostUSD   float64    `json:"cost_usd"`
	LastRunID string     `json:"last_run_id,omitempty"`
	LastRunAt time.Time  `json:"last_run_at,omitempty"`
	LastRun   run.Status `json:"last_run_status,omitempty"`
}

// ProjectRows inventories the projects of one repository and joins each with its
// plan, its ledger and its most recent run.
//
// Nothing here is fatal. A project whose tasks.md is missing or unparseable
// still gets a row (with zero tasks): the listing exists to show what is there,
// and refusing to list nine healthy projects because one is broken is the
// failure mode this repository already learned from the gate inbox.
func (l RunLister) ProjectRows(workDir string) ([]ProjectRow, error) {
	summaries := ListProjects(workDir)
	runs, err := l.ListRuns(RunListOptions{Repo: workDir})
	if err != nil {
		// The index is a convenience here, not the source of the listing: a
		// missing $CORVEX_HOME must not hide the projects on disk.
		runs = nil
	}

	rows := make([]ProjectRow, 0, len(summaries))
	for _, s := range summaries {
		row := ProjectRow{Name: s.Name, HasSpec: s.HasSpec, HasTasks: s.HasTasks, Status: s.Status}
		if view, verr := ReadProject(workDir, s.Name); verr == nil {
			row.Total = len(view.Tasks)
			row.Completed = view.Passed
		}
		if sum, serr := activity.Summarize(workDir, s.Name); serr == nil {
			row.CostUSD = sum.TotalCostUSD
		}
		for _, r := range runs {
			if r.Project == s.Name || r.Recipe == s.Name {
				row.LastRunID, row.LastRunAt, row.LastRun = r.RunID, r.StartedAt, r.Status
				break // ListRuns is newest first
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
