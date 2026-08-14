package step

import "strings"

// taskReport is the structured handoff a Worker must emit at the end of a task:
// a short summary, the key decisions it made, and the context the next task's
// Worker needs. Parsed from the Worker's final output (see parseTaskReport).
type taskReport struct {
	Summary   string
	Decisions []string
	Handoff   string
}

// reportMarker is the line that opens the structured report block.
const reportMarker = "TASK-REPORT:"

// parseTaskReport extracts a taskReport from a Worker's output. It tolerates
// markdown wrapping (bold, blockquotes, backticks, headings) around the
// markers. Returns ok=false when no TASK-REPORT block is present at all.
//
// Expected shape (markers are case-insensitive, markdown-tolerant):
//
//	TASK-REPORT:
//	SUMMARY: <one to three sentences>
//	DECISIONS:
//	- <decision>
//	- <decision>
//	HANDOFF: <paragraph for the next task>
func parseTaskReport(output string) (taskReport, bool) {
	lines := strings.Split(output, "\n")

	start := -1
	for i, ln := range lines {
		if strings.HasPrefix(cleanMarker(ln), "TASK-REPORT") {
			start = i
			break
		}
	}
	if start == -1 {
		return taskReport{}, false
	}

	var rep taskReport
	field := "" // "summary" | "decisions" | "handoff"
	var summaryParts, handoffParts []string

	for _, ln := range lines[start+1:] {
		marker := cleanMarker(ln)
		switch {
		case strings.HasPrefix(marker, "SUMMARY:"):
			field = "summary"
			if v := afterColon(ln); v != "" {
				summaryParts = append(summaryParts, v)
			}
			continue
		case strings.HasPrefix(marker, "DECISIONS:"):
			field = "decisions"
			if v := afterColon(ln); v != "" {
				rep.Decisions = append(rep.Decisions, v)
			}
			continue
		case strings.HasPrefix(marker, "HANDOFF:"):
			field = "handoff"
			if v := afterColon(ln); v != "" {
				handoffParts = append(handoffParts, v)
			}
			continue
		}

		trimmed := strings.TrimSpace(ln)
		switch field {
		case "summary":
			if trimmed != "" {
				summaryParts = append(summaryParts, trimmed)
			}
		case "decisions":
			if item := bulletText(ln); item != "" {
				rep.Decisions = append(rep.Decisions, item)
			}
		case "handoff":
			if trimmed != "" {
				handoffParts = append(handoffParts, trimmed)
			}
		}
	}

	rep.Summary = strings.TrimSpace(strings.Join(summaryParts, " "))
	rep.Handoff = strings.TrimSpace(strings.Join(handoffParts, " "))
	return rep, true
}

// cleanMarker strips leading markdown/quote noise and upper-cases the line so
// marker detection is robust to `**SUMMARY:**`, `> HANDOFF:`, `### TASK-REPORT`,
// backticks, and surrounding spaces.
func cleanMarker(line string) string {
	s := strings.TrimSpace(line)
	s = strings.TrimLeft(s, "#>*_`- \t")
	return strings.ToUpper(strings.TrimSpace(s))
}

// afterColon returns the text following the first colon on the line, with
// markdown emphasis trimmed. Empty when nothing follows the colon.
func afterColon(line string) string {
	idx := strings.Index(line, ":")
	if idx == -1 {
		return ""
	}
	rest := strings.TrimSpace(line[idx+1:])
	rest = strings.Trim(rest, "*_`")
	return strings.TrimSpace(rest)
}

// bulletText returns the content of a `- ` / `* ` list item, or "" if the line
// is not a bullet.
func bulletText(line string) string {
	s := strings.TrimSpace(line)
	for _, p := range []string{"- ", "* ", "• "} {
		if strings.HasPrefix(s, p) {
			return strings.TrimSpace(strings.Trim(strings.TrimPrefix(s, p), "*_`"))
		}
	}
	return ""
}
