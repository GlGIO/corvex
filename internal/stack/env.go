package stack

import (
	"os"
	"strings"
)

// LoadEnvFileVars parses a dotenv file into "KEY=VALUE" entries (skipping blanks
// and # comments, stripping an optional `export ` prefix and surrounding
// quotes). Used to source a stack env_file (e.g. backend/.env-stg) into the
// validation app + migration processes.
func LoadEnvFileVars(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var vars []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key == "" {
			continue
		}
		vars = append(vars, key+"="+val)
	}
	return vars, nil
}
