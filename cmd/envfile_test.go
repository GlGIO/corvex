package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFileVars(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env-stg")
	content := "# comment\n\nexport DB_HOST=stg.db\nDB_PORT=5432\nDB_PASSWORD=\"se cret\"\nbad line no equals\nEMPTY=\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	vars, err := loadEnvFileVars(p)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, v := range vars {
		got[v] = true
	}
	for _, want := range []string{"DB_HOST=stg.db", "DB_PORT=5432", "DB_PASSWORD=se cret", "EMPTY="} {
		if !got[want] {
			t.Errorf("missing %q in %v", want, vars)
		}
	}
	for _, bad := range vars {
		if bad == "bad line no equals" {
			t.Errorf("comment/garbage line should be skipped")
		}
	}
}

func TestLoadEnvFileVars_Missing(t *testing.T) {
	if _, err := loadEnvFileVars(filepath.Join(t.TempDir(), "nope.env")); err == nil {
		t.Error("expected error for missing env file")
	}
}
