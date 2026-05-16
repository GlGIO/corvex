package task

import (
	"fmt"
	"os"
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

	return os.WriteFile(path, []byte(b.String()), 0644)
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

	if task.Type != "" || len(task.DependsOn) > 0 {
		b.WriteString("\n```yaml\n")
		if task.Type != "" {
			fmt.Fprintf(b, "type: %s\n", task.Type)
		}
		if len(task.DependsOn) > 0 {
			fmt.Fprintf(b, "depends_on: [%s]\n", strings.Join(task.DependsOn, ", "))
		}
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
