package task

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/giovannialves/corvex/internal/types"
	"gopkg.in/yaml.v3"
)

var (
	headingRe    = regexp.MustCompile(`^##\s+(S\d+)\s*[-—–]\s*(.+?)\s+(⬜|🔄|✅|❌|⏭` + "\uFE0F" + `|⏭)\s*([A-Za-z-]+)\s*$`)
	sectionRe    = regexp.MustCompile(`^###\s+(.+)$`)
	criterionRe  = regexp.MustCompile(`^\s*-\s*\[\s*\]\s*(.+)$`)
	fileCreateRe = regexp.MustCompile("^\\s*-\\s*\\*\\*Criar:\\*\\*\\s*`([^`]+)`")
	fileModifyRe = regexp.MustCompile("^\\s*-\\s*\\*\\*Modificar:\\*\\*\\s*`([^`]+)`")
	separatorRe  = regexp.MustCompile(`^---\s*$`)

	// looseHeadingRe matches any "## S<digits>" line — used to detect task
	// headings that almost match `headingRe` but have malformed status, missing
	// emoji, or other formatting errors. Lines that match this but NOT
	// `headingRe` are reported as parse errors instead of silently dropped.
	looseHeadingRe = regexp.MustCompile(`^##\s+S\d+\b`)
)

var statusEmoji = map[types.TaskStatus]string{
	types.StatusPending: "⬜",
	types.StatusRunning: "🔄",
	types.StatusPassed:  "✅",
	types.StatusFailed:  "❌",
	types.StatusSkipped: "⏭" + "\uFE0F",
}

// statusWord maps the textual status token in a task heading to a canonical
// status. Beyond the canonical words it accepts the synonyms LLMs tend to emit
// when regenerating tasks.md (e.g. a replan writing "✅ COMPLETED" or "✅ DONE"
// instead of "✅ PASSED"). Being lenient here keeps a benign wording drift from
// failing the whole run; truly unknown words still fall through to the
// "unrecognized task heading" error. Lookups are upper-cased by the caller.
var statusWord = map[string]types.TaskStatus{
	"PENDING":     types.StatusPending,
	"TODO":        types.StatusPending,
	"PLANNED":     types.StatusPending,
	"RUNNING":     types.StatusRunning,
	"INPROGRESS":  types.StatusRunning,
	"IN-PROGRESS": types.StatusRunning,
	"WIP":         types.StatusRunning,
	"PASSED":      types.StatusPassed,
	"PASS":        types.StatusPassed,
	"COMPLETE":    types.StatusPassed,
	"COMPLETED":   types.StatusPassed,
	"DONE":        types.StatusPassed,
	"FAILED":      types.StatusFailed,
	"FAIL":        types.StatusFailed,
	"SKIPPED":     types.StatusSkipped,
	"SKIP":        types.StatusSkipped,
}

// normalizeStatusWord resolves a heading's status token (any case) to a
// canonical status, returning false when the word is not recognized.
func normalizeStatusWord(word string) (types.TaskStatus, bool) {
	s, ok := statusWord[strings.ToUpper(strings.TrimSpace(word))]
	return s, ok
}

var emojiStatus = map[string]types.TaskStatus{
	"⬜":            types.StatusPending,
	"🔄":            types.StatusRunning,
	"✅":            types.StatusPassed,
	"❌":            types.StatusFailed,
	"⏭" + "\uFE0F": types.StatusSkipped,
	"⏭":            types.StatusSkipped,
}

type frontmatter struct {
	GeneratedBy  string              `yaml:"generated_by,omitempty"`
	GeneratedAt  string              `yaml:"generated_at,omitempty"`
	Dependencies map[string][]string `yaml:"dag,omitempty"`
}

type inlineYAML struct {
	Type      string   `yaml:"type"`
	DependsOn []string `yaml:"depends_on"`
	Kind      string   `yaml:"kind"`
	Command   string   `yaml:"command"`
	LoopUntil string   `yaml:"loop_until"`
	LoopMax   int      `yaml:"loop_max"`
}

// ParseTasksFile reads a tasks.md file and returns the parsed tasks and DAG specification.
func ParseTasksFile(path string) ([]types.Task, types.DAGSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, types.DAGSpec{}, fmt.Errorf("parsing tasks %s: %w", path, err)
	}

	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.TrimSpace(content) == "" {
		return nil, types.DAGSpec{}, nil
	}

	lines := strings.Split(content, "\n")
	dag, bodyStart := parseFrontmatter(lines)
	tasks, malformed := parseTaskBlocks(lines[bodyStart:])

	if len(malformed) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "parsing %s: %d unrecognized task heading(s):\n", path, len(malformed))
		for _, m := range malformed {
			fmt.Fprintf(&b, "  line %d: %q\n", m.lineNum+bodyStart+1, m.content)
		}
		b.WriteString("Hint: status must be one of PENDING|RUNNING|PASSED|FAILED|SKIPPED (with matching emoji ⬜|🔄|✅|❌|⏭️)")
		return nil, types.DAGSpec{}, fmt.Errorf("%s", b.String())
	}

	return tasks, dag, nil
}

type malformedHeading struct {
	lineNum int // 0-indexed within the body slice
	content string
}

func parseFrontmatter(lines []string) (types.DAGSpec, int) {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}

	if start >= len(lines) || !separatorRe.MatchString(lines[start]) {
		return types.DAGSpec{}, 0
	}

	end := start + 1
	for end < len(lines) {
		if separatorRe.MatchString(lines[end]) {
			break
		}
		end++
	}

	if end >= len(lines) {
		return types.DAGSpec{}, 0
	}

	yamlContent := strings.Join(lines[start+1:end], "\n")
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(yamlContent), &fm); err != nil {
		return types.DAGSpec{}, 0
	}

	dag := types.DAGSpec{
		GeneratedBy:  fm.GeneratedBy,
		GeneratedAt:  fm.GeneratedAt,
		Dependencies: fm.Dependencies,
	}

	if dag.Dependencies != nil {
		for k, v := range dag.Dependencies {
			if v == nil {
				dag.Dependencies[k] = []string{}
			}
		}
	}

	return dag, end + 1
}

type taskBlock struct {
	id     string
	title  string
	status types.TaskStatus
	lines  []string
}

func parseTaskBlocks(lines []string) ([]types.Task, []malformedHeading) {
	var blocks []taskBlock
	var current *taskBlock
	var malformed []malformedHeading

	for i, line := range lines {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			status, ok := normalizeStatusWord(m[4])
			if !ok {
				// Heading shape is right but the status word is unknown — report
				// it rather than silently dropping the task.
				malformed = append(malformed, malformedHeading{
					lineNum: i,
					content: strings.TrimSpace(line),
				})
				continue
			}
			if current != nil {
				blocks = append(blocks, *current)
			}
			current = &taskBlock{
				id:     m[1],
				title:  m[2],
				status: status,
			}
			continue
		}

		// Catch headings that look like task lines but fail the strict regex
		// (e.g. a missing status emoji, or "## S01 — title PASSED"). Without this
		// check, the parser silently drops them and downstream commands show
		// baffling counts like "0/28 done" when the file has 30 tasks.
		if looseHeadingRe.MatchString(line) {
			malformed = append(malformed, malformedHeading{
				lineNum: i,
				content: strings.TrimSpace(line),
			})
			continue
		}

		if current == nil {
			continue
		}

		if separatorRe.MatchString(line) {
			continue
		}

		current.lines = append(current.lines, line)
	}

	if current != nil {
		blocks = append(blocks, *current)
	}

	tasks := make([]types.Task, 0, len(blocks))
	for _, b := range blocks {
		task := types.Task{
			ID:     b.id,
			Title:  b.title,
			Status: b.status,
		}
		parseBlockContent(b.lines, &task)
		tasks = append(tasks, task)
	}

	return tasks, malformed
}

func parseBlockContent(lines []string, task *types.Task) {
	extractInlineYAML(lines, task)

	sections := splitSections(lines)

	if desc, ok := sections["O que fazer"]; ok {
		task.Description = trimBlankLines(desc)
	}

	if criteria, ok := sections["Critérios de sucesso"]; ok {
		task.Criteria = extractCriteria(criteria)
	}

	if files, ok := sections["Arquivos"]; ok {
		task.Files = extractFiles(files)
	}
}

func extractInlineYAML(lines []string, task *types.Task) {
	inYAML := false
	var yamlLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if sectionRe.MatchString(line) {
			break
		}

		if !inYAML && trimmed == "```yaml" {
			inYAML = true
			continue
		}

		if inYAML {
			if trimmed == "```" {
				break
			}
			yamlLines = append(yamlLines, line)
		}
	}

	if len(yamlLines) == 0 {
		return
	}

	var iy inlineYAML
	if err := yaml.Unmarshal([]byte(strings.Join(yamlLines, "\n")), &iy); err != nil {
		return
	}

	task.Type = types.TaskType(iy.Type)
	task.DependsOn = iy.DependsOn
	task.Kind = iy.Kind
	task.Command = iy.Command
	task.LoopUntil = iy.LoopUntil
	task.LoopMax = iy.LoopMax
}

func splitSections(lines []string) map[string]string {
	sections := make(map[string]string)
	currentSection := ""
	var currentLines []string

	for _, line := range lines {
		if m := sectionRe.FindStringSubmatch(line); m != nil {
			if currentSection != "" {
				sections[currentSection] = strings.Join(currentLines, "\n")
			}
			currentSection = strings.TrimSpace(m[1])
			currentLines = nil
			continue
		}

		if currentSection != "" {
			currentLines = append(currentLines, line)
		}
	}

	if currentSection != "" {
		sections[currentSection] = strings.Join(currentLines, "\n")
	}

	return sections
}

func extractCriteria(content string) []string {
	var criteria []string
	for _, line := range strings.Split(content, "\n") {
		if m := criterionRe.FindStringSubmatch(line); m != nil {
			criteria = append(criteria, strings.TrimSpace(m[1]))
		}
	}
	return criteria
}

func extractFiles(content string) types.TaskFiles {
	var files types.TaskFiles
	for _, line := range strings.Split(content, "\n") {
		if m := fileCreateRe.FindStringSubmatch(line); m != nil {
			files.Create = append(files.Create, m[1])
		} else if m := fileModifyRe.FindStringSubmatch(line); m != nil {
			files.Modify = append(files.Modify, m[1])
		}
	}
	return files
}

func trimBlankLines(s string) string {
	lines := strings.Split(s, "\n")

	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}

	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}

	if start >= end {
		return ""
	}

	return strings.Join(lines[start:end], "\n")
}
