package step

import (
	"fmt"
	"testing"
)

func TestIsFatal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", fmt.Errorf("plain"), false},
		{"fatal wrapped", Fatal(fmt.Errorf("boom")), true},
		{"fatal wrapped in %w", fmt.Errorf("outer: %w", Fatal(fmt.Errorf("inner"))), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsFatal(tc.err); got != tc.want {
				t.Errorf("IsFatal(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
