package types_test

import (
	"testing"

	"github.com/giovannialves/corvex/internal/types"
)

func TestTaskStatus_IsValid(t *testing.T) {
	tests := []struct {
		status types.TaskStatus
		want   bool
	}{
		{types.StatusPending, true},
		{types.StatusRunning, true},
		{types.StatusPassed, true},
		{types.StatusFailed, true},
		{types.StatusSkipped, true},
		{"", false},
		{"UNKNOWN", false},
		{"pending", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.want {
				t.Errorf("TaskStatus(%q).IsValid() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestTaskStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status types.TaskStatus
		want   bool
	}{
		{types.StatusPending, false},
		{types.StatusRunning, false},
		{types.StatusPassed, true},
		{types.StatusFailed, true},
		{types.StatusSkipped, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.want {
				t.Errorf("TaskStatus(%q).IsTerminal() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestTaskStatus_String(t *testing.T) {
	tests := []struct {
		status types.TaskStatus
		want   string
	}{
		{types.StatusPending, "PENDING"},
		{types.StatusRunning, "RUNNING"},
		{types.StatusPassed, "PASSED"},
		{types.StatusFailed, "FAILED"},
		{types.StatusSkipped, "SKIPPED"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.status.String(); got != tt.want {
				t.Errorf("TaskStatus.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTaskType_IsValid(t *testing.T) {
	tests := []struct {
		typ  types.TaskType
		want bool
	}{
		{types.TypeDatabase, true},
		{types.TypeBackend, true},
		{types.TypeFrontend, true},
		{types.TypeReview, true},
		{types.TypeGeneral, true},
		{"", false},
		{"unknown", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.typ), func(t *testing.T) {
			if got := tt.typ.IsValid(); got != tt.want {
				t.Errorf("TaskType(%q).IsValid() = %v, want %v", tt.typ, got, tt.want)
			}
		})
	}
}

func TestStreamEventType_IsValid(t *testing.T) {
	tests := []struct {
		evt  types.StreamEventType
		want bool
	}{
		{types.EventText, true},
		{types.EventToolUse, true},
		{types.EventToolResult, true},
		{types.EventDone, true},
		{types.EventError, true},
		{"", false},
		{"unknown", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.evt), func(t *testing.T) {
			if got := tt.evt.IsValid(); got != tt.want {
				t.Errorf("StreamEventType(%q).IsValid() = %v, want %v", tt.evt, got, tt.want)
			}
		})
	}
}
