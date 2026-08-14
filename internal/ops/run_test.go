package ops

// Unit test for the --ab flag parser. It lived in cmd/run_test.go while
// parseABModels was still a cmd function; it moved down with the code,
// assertions unchanged.

import "testing"

func TestParseABModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{"empty returns nil", "", nil, false},
		{"two distinct models", "sonnet,opus", []string{"sonnet", "opus"}, false},
		{"trims whitespace", "  sonnet , opus  ", []string{"sonnet", "opus"}, false},
		{"one model errors", "sonnet", nil, true},
		{"three models errors", "haiku,sonnet,opus", nil, true},
		{"duplicate models error", "sonnet,sonnet", nil, true},
		{"empty fragments error", "sonnet,", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseABModels(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil; result = %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
