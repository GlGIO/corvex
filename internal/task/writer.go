package task

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/giovannialves/corvex/internal/types"
	"gopkg.in/yaml.v3"
)

// WriteTasksFile serialises tasks and DAG specification back to a tasks.md file.
func WriteTasksFile(path string, tasks []types.Task, dag types.DAGSpec) error {
	var b strings.Builder

	if err := writeFrontmatter(&b, dag); err != nil {
		return fmt.Errorf("writing tasks %s: %w", path, err)
	}

	for i, task := range tasks {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		writeTask(&b, task)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".tasks-*.tmp")
	if err != nil {
		return fmt.Errorf("writing tasks %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			os.Remove(tmpName)
		}
	}()

	if _, err = tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return fmt.Errorf("writing tasks %s: %w", path, err)
	}
	if err = tmp.Chmod(0644); err != nil {
		tmp.Close()
		return fmt.Errorf("writing tasks %s: %w", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("writing tasks %s: %w", path, err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("writing tasks %s: %w", path, err)
	}
	return nil
}

// UpdateTaskStatus re-writes a single task's status inside a tasks.md file.
func UpdateTaskStatus(path string, taskID string, status types.TaskStatus) error {
	tasks, dag, err := ParseTasksFile(path)
	if err != nil {
		return err
	}

	found := false
	for i := range tasks {
		if tasks[i].ID == taskID {
			tasks[i].Status = status
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("task %s not found in %s", taskID, path)
	}

	return WriteTasksFile(path, tasks, dag)
}

// ReplaceTask rewrites one task in a tasks.md wholesale.
//
// UpdateTaskStatus re-reads the file and only touches the status, which is the
// right thing for the status writes on the hot path. It is the wrong thing for a
// task that discovered something while it ran: a `produces: items` step holds
// its work list in memory, and re-reading the file would write back the version
// that does not have it.
func ReplaceTask(path string, updated types.Task) error {
	tasks, dag, err := ParseTasksFile(path)
	if err != nil {
		return err
	}
	for i := range tasks {
		if tasks[i].ID == updated.ID {
			tasks[i] = updated
			return WriteTasksFile(path, tasks, dag)
		}
	}
	return fmt.Errorf("task %s not found in %s", updated.ID, path)
}

func writeFrontmatter(b *strings.Builder, dag types.DAGSpec) error {
	if dag.GeneratedBy == "" && dag.GeneratedAt == "" && len(dag.Dependencies) == 0 {
		return nil
	}

	fm := frontmatter{
		GeneratedBy:  dag.GeneratedBy,
		GeneratedAt:  dag.GeneratedAt,
		Dependencies: dag.Dependencies,
	}

	data, err := yaml.Marshal(fm)
	if err != nil {
		return err
	}

	b.WriteString("---\n")
	b.WriteString(string(data))
	b.WriteString("---\n\n")
	return nil
}

func writeTask(b *strings.Builder, task types.Task) {
	emoji := statusEmoji[task.Status]
	fmt.Fprintf(b, "## %s — %s %s %s\n", task.ID, task.Title, emoji, task.Status)

	if task.Type != "" || len(task.DependsOn) > 0 || task.Kind != "" || task.Command != "" || hasStepFields(task) {
		b.WriteString("\n```yaml\n")
		if task.Type != "" {
			fmt.Fprintf(b, "type: %s\n", task.Type)
		}
		if task.Kind != "" {
			fmt.Fprintf(b, "kind: %s\n", task.Kind)
		}
		if task.Command != "" {
			fmt.Fprintf(b, "command: %q\n", task.Command)
		}
		if task.LoopUntil != "" {
			fmt.Fprintf(b, "loop_until: %q\n", task.LoopUntil)
		}
		if task.LoopMax > 0 {
			fmt.Fprintf(b, "loop_max: %d\n", task.LoopMax)
		}
		if len(task.DependsOn) > 0 {
			fmt.Fprintf(b, "depends_on: [%s]\n", strings.Join(task.DependsOn, ", "))
		}
		writeStepFields(b, task)
		b.WriteString("```\n")
	}

	if task.Description != "" {
		b.WriteString("\n### O que fazer\n")
		b.WriteString(task.Description)
		b.WriteString("\n")
	}

	if len(task.Criteria) > 0 {
		b.WriteString("\n### Critérios de sucesso\n")
		for _, c := range task.Criteria {
			fmt.Fprintf(b, "- [ ] %s\n", c)
		}
	}

	if len(task.Files.Create) > 0 || len(task.Files.Modify) > 0 {
		b.WriteString("\n### Arquivos\n")
		for _, f := range task.Files.Create {
			fmt.Fprintf(b, "- **Criar:** `%s`\n", f)
		}
		for _, f := range task.Files.Modify {
			fmt.Fprintf(b, "- **Modificar:** `%s`\n", f)
		}
	}
}
