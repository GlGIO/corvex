package ops

import (
	"os"
	"path/filepath"
	"strings"
)

// ProjectDir returns the directory holding a project's state inside a workspace.
func ProjectDir(workDir, project string) string {
	return filepath.Join(workDir, ".corvex", "tasks", project)
}

// ProjectNames returns names of directories under .corvex/tasks/ that contain
// spec.md or tasks.md.
func ProjectNames(workDir string) []string {
	tasksDir := filepath.Join(workDir, ".corvex", "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, err := os.Stat(filepath.Join(tasksDir, name, "spec.md")); err == nil {
			names = append(names, name)
			continue
		}
		if _, err := os.Stat(filepath.Join(tasksDir, name, "tasks.md")); err == nil {
			names = append(names, name)
		}
	}
	return names
}

// SuggestProject returns the closest project name to name via case-insensitive
// prefix/substring match or Levenshtein distance <= 2, or "" if none is close.
func SuggestProject(workDir, name string) string {
	names := ProjectNames(workDir)
	lower := strings.ToLower(name)
	for _, n := range names {
		nl := strings.ToLower(n)
		if strings.HasPrefix(nl, lower) || strings.Contains(nl, lower) {
			return n
		}
	}
	best, bestDist := "", 3
	for _, n := range names {
		if d := levenshtein(strings.ToLower(n), lower); d <= 2 && d < bestDist {
			bestDist = d
			best = n
		}
	}
	return best
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
		dp[i][0] = i
	}
	for j := range dp[0] {
		dp[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			if ra[i-1] == rb[j-1] {
				dp[i][j] = dp[i-1][j-1]
			} else {
				dp[i][j] = 1 + min(dp[i-1][j], min(dp[i][j-1], dp[i-1][j-1]))
			}
		}
	}
	return dp[la][lb]
}
