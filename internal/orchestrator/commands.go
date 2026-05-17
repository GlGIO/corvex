package orchestrator

// CommandType identifies a runtime control message sent from the UI to the
// running orchestrator loop.
type CommandType string

const (
	// CmdPause asks the orchestrator to halt before starting the next task.
	// Already-running work continues to completion. Followed by CmdResume.
	CmdPause CommandType = "pause"
	// CmdResume clears a pause and lets the next ready task start.
	CmdResume CommandType = "resume"
	// CmdSkip marks a task as SKIPPED and excludes it from the run.
	// Currently honoured for tasks that have not yet started (PENDING).
	CmdSkip CommandType = "skip"
	// CmdRetry resets a FAILED task back to PENDING so the loop picks it up
	// again on the next iteration.
	CmdRetry CommandType = "retry"
)

// Command is the message envelope drained by the orchestrator between
// tasks. TaskID is required for skip/retry; it is ignored for pause/resume.
type Command struct {
	Type   CommandType
	TaskID string
}
