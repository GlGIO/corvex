package types

// InsightData carries an agent-creation suggestion from the Advisor.
//
// It lives here — and not next to the Advisor that produces it — because it
// crosses three packages: `internal/planning` produces it, the orchestrator
// carries it inside an event, and the renderers print it.
type InsightData struct {
	TaskType         string
	Count            int
	SuggestedPath    string
	SuggestedContent string
}
