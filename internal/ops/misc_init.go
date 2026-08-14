package ops

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/giovannialves/corvex/templates"
)

// InitWorkspace scaffolds a .corvex/ workspace under dir: the directory tree,
// the default config, agent prompt and context README from the embedded
// templates, and a .gitignore. It refuses to touch an existing .corvex/ rather
// than merge into it, and returns the path it created.
func InitWorkspace(dir string) (string, error) {
	corvexDir := filepath.Join(dir, ".corvex")
	if _, err := os.Stat(corvexDir); err == nil {
		return "", fmt.Errorf(".corvex/ already exists in %s", dir)
	}

	dirs := []string{
		".corvex",
		".corvex/agents",
		".corvex/context",
		".corvex/hooks",
		".corvex/templates",
		".corvex/tasks",
	}

	for _, d := range dirs {
		path := filepath.Join(dir, d)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", fmt.Errorf("creating %s: %w", d, err)
		}
	}

	fileMappings := map[string]string{
		"config.yaml":       ".corvex/config.yaml",
		"agents/default.md": ".corvex/agents/default.md",
		"context/README.md": ".corvex/context/README.md",
	}

	for src, dst := range fileMappings {
		data, err := fs.ReadFile(templates.FS, src)
		if err != nil {
			return "", fmt.Errorf("reading template %s: %w", src, err)
		}
		dstPath := filepath.Join(dir, dst)
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return "", fmt.Errorf("writing %s: %w", dst, err)
		}
	}

	// Materialised secrets (mcp.json carries env-expanded MCP server config)
	// must never be committed. Drop a .corvex/.gitignore so it is ignored from
	// the start.
	gitignorePath := filepath.Join(corvexDir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("mcp.json\n"), 0o644); err != nil {
		return "", fmt.Errorf("writing .corvex/.gitignore: %w", err)
	}

	return corvexDir, nil
}
