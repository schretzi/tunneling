package cmd

import (
	"testing"
	"time"

	"github.com/schretzi/tunneling/internal/health"
)

func TestDecideState(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Hour)

	tests := []struct {
		name  string
		bound bool
		h     *health.Tunnel
		want  State
	}{
		{
			name:  "nothing listening",
			bound: false,
			h:     &health.Tunnel{LastSuccess: now},
			want:  StateDown,
		},
		{
			// The whole point: a listener stays bound after its destination
			// is deleted, so the probe alone cannot see the breakage.
			name:  "bound but every forward fails",
			bound: true,
			h:     &health.Tunnel{LastFailure: now, Failures: 210, LastError: "code 4033 (not authorized)"},
			want:  StateFailing,
		},
		{
			name:  "bound, never used",
			bound: true,
			h:     &health.Tunnel{},
			want:  StateIdle,
		},
		{
			name:  "bound and recently answered",
			bound: true,
			h:     &health.Tunnel{LastSuccess: now, Successes: 3},
			want:  StateOK,
		},
		{
			name:  "recovered: success is newer than the failure",
			bound: true,
			h:     &health.Tunnel{LastFailure: older, LastSuccess: now},
			want:  StateOK,
		},
		{
			name:  "regressed: failure is newer than the success",
			bound: true,
			h:     &health.Tunnel{LastSuccess: older, LastFailure: now},
			want:  StateFailing,
		},
		{
			name:  "bound but no live daemon accounts for the port",
			bound: true,
			h:     nil,
			want:  StateUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decideState(tt.bound, tt.h); got != tt.want {
				t.Errorf("decideState() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Only DOWN and FAILING are worth a non-zero exit. IDLE in particular must
// not be: most tunnels are idle most of the time, and an exit code that is
// always non-zero is one nobody reads.
func TestOnlyRealProblemsExitNonZero(t *testing.T) {
	for _, tt := range []struct {
		state State
		bad   bool
	}{
		{StateDown, true},
		{StateFailing, true},
		{StateIdle, false},
		{StateOK, false},
		{StateUnknown, false},
	} {
		if got := tt.state.bad(); got != tt.bad {
			t.Errorf("%s.bad() = %v, want %v", tt.state, got, tt.bad)
		}
	}
}

func TestSince(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{"zero", time.Time{}, "never"},
		{"seconds", time.Now().Add(-30 * time.Second), "30s ago"},
		{"minutes", time.Now().Add(-5 * time.Minute), "5m ago"},
		{"hours", time.Now().Add(-3 * time.Hour), "3h ago"},
		{"days", time.Now().Add(-50 * time.Hour), "2d ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := since(tt.in); got != tt.want {
				t.Errorf("since() = %q, want %q", got, tt.want)
			}
		})
	}
}
