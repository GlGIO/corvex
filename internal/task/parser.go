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
	headingRe    = regexp.MustCompile(`^##\s+(S\d+)\s*[-—–]\s*(.+?)\s+(⬜|🔄|✅|❌|⏭` + "\uFE0F" + `|⏭)\s*(PENDING|RUNNING|PASSED|FAILED|SKIPPED)\s*$`)
	sectionRe    = regexp.MustCompile(`^###\s+(.+)$`)
	criterionRe  = regexp.MustCompile(`^\s*-\s*\[\s*\]\s*(.+)$`)
	fileCreateRe = regexp.MustCompile("^\\s*-\\s*\\*\\*Criar:\\*\\*\\s*`([^`]+)`")
	fileModifyRe = regexp.MustCompile("^\\s*-\\s*\\*\\*Modificar:\\*\\*\\s*`([^`]+)`")
	separatorRe  = regexp.MustCompile(`^---\s*$`)
)

var statusEmoji = map[types.TaskStatus]string{
	types.StatusPending: "⬜",
	types.StatusRunning: "🔄",
	types.StatusPassed:  "✅",
	types.StatusFailed:  "❌",
	types.StatusSkipped: "⏭" + "\uFE0F",
}

var emojiStatus = map[string]types.TaskStatus{
	"⬜":              types.StatusPending,
	"🔄":              types.StatusRunning,
	"✅":              types.StatusPassed,
	"❌":              types.StatusFailed,
	"⏭" + "\uFE0F": types.StatusSkipped,
	"⏭":              types.StatusSkipped,
}

type frontmatter struct {
	GeneratedBy  string              `yaml:"generated_by,omitempty"`
	GeneratedAt  string              `yaml:"generated_at,omitempty"`
	Dependencies map[string][]string `yaml:"dag,omitempty"`
}

type inlineYAML struct {
	Type      string   `yaml:"type"`
	DependsOn []string `yaml:"depends_on"`
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
	tasks := parseTaskBlocks(lines[bodyStart:])

	return tasks, dag, nil
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

func parseTaskBlocks(lines []string) []types.Task {
	var blocks []taskBlock
	var current *taskBlock

	for _, line := range lines {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			if current != nil {
				blocks = append(blocks, *current)
			}
			current = &taskBlock{
				id:     m[1],
				title:  m[2],
				status: types.TaskStatus(m[4]),
			}
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

	return tasks
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
