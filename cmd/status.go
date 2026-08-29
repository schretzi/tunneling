package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/schretzi/tunneling/internal/config"
	"github.com/schretzi/tunneling/internal/health"

	"github.com/spf13/cobra"
)

// State is what `status` reports for one tunnel.
//
// Four values, not two, because "the local port is bound" and "traffic gets
// through" are different questions and only the second one matters. A tunnel
// pointing at a deleted GCP project keeps its listener bound forever: it
// reported OPEN for hours while failing every connection, which is the reason
// this vocabulary exists.
type State string

const (
	// StateDown means nothing is listening on the local port.
	StateDown State = "DOWN"
	// StateFailing means the port is bound but the daemon's most recent
	// evidence is a failure.
	StateFailing State = "FAILING"
	// StateIdle means the port is bound and nothing has used the tunnel, so
	// there is no evidence either way. Deliberately not reported as OK:
	// treating "untested" as "working" is exactly the optimism that hid a
	// dead tunnel.
	StateIdle State = "IDLE"
	// StateOK means the port is bound and the destination recently answered.
	StateOK State = "OK"
	// StateUnknown means the port is bound but no live daemon is publishing
	// health for it — a stale state file, or something else on the port.
	StateUnknown State = "UNKNOWN"
)

// bad reports whether the state should make `status` exit non-zero. IDLE and
// UNKNOWN are not failures: neither claims anything is wrong.
func (s State) bad() bool { return s == StateDown || s == StateFailing }

// report is one row of `status`, and one element of its JSON output.
type report struct {
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Local       string    `json:"local"`
	State       State     `json:"state"`
	Destination string    `json:"destination"`
	Via         string    `json:"via"`
	LastSuccess time.Time `json:"lastSuccess,omitzero"`
	Failures    int64     `json:"failures"`
	LastError   string    `json:"lastError,omitempty"`
}

// output is the whole `status --json` document.
type output struct {
	DaemonRunning bool     `json:"daemonRunning"`
	StatePath     string   `json:"statePath"`
	Tunnels       []report `json:"tunnels"`
}

var statusJSON bool

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show each tunnel's state: whether it is listening and whether traffic works",
	Long: `Report every configured tunnel.

Two independent signals are combined. A loopback TCP connect says whether
something is listening on the local port. The daemon's own record — written to
the state file, one entry per tunnel — says whether traffic through it
actually reached the far end.

  DOWN     nothing is listening on the local port
  FAILING  listening, but the most recent forward failed or got no reply
  IDLE     listening, and nothing has used it yet — no evidence either way
  OK       listening, and the destination recently answered
  UNKNOWN  listening, but no live daemon is publishing health for it

IDLE is not OK. A tunnel nobody has used tells you nothing, and reporting that
as healthy is how a tunnel to a deleted project went unnoticed for hours.

Exits non-zero if any selected tunnel is DOWN or FAILING.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		selected, err := selectedTunnels(cfg)
		if err != nil {
			return err
		}

		statePath, err := config.ExpandPath(cfg.StatePath)
		if err != nil {
			return err
		}
		snap, err := health.Load(statePath)
		if err != nil {
			// A state file we cannot read is worth saying out loud, but it
			// must not stop us reporting the port probes.
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
		}
		live := snap.Live()

		reports := make([]report, 0, len(selected))
		for _, t := range selected {
			reports = append(reports, buildReport(cmd.Context(), t, snap, live))
		}

		if statusJSON {
			return writeJSON(cmd, output{DaemonRunning: live, StatePath: statePath, Tunnels: reports})
		}
		writeTable(cmd, reports, live, statePath)

		var bad int
		for _, r := range reports {
			if r.State.bad() {
				bad++
			}
		}
		if bad > 0 {
			return fmt.Errorf("%d of %d tunnel(s) are down or failing", bad, len(reports))
		}
		return nil
	},
}

// buildReport combines the live port probe with the daemon's record.
func buildReport(ctx context.Context, t config.Tunnel, snap *health.Snapshot, live bool) report {
	r := report{
		Name:        t.Name,
		Kind:        string(t.Kind),
		Local:       config.DialAddr(t),
		Destination: t.Endpoint(),
		Via:         via(t),
	}

	var h *health.Tunnel
	if live && snap != nil {
		if entry, ok := snap.Tunnels[t.Name]; ok {
			h = &entry
			r.LastSuccess, r.Failures, r.LastError = entry.LastSuccess, entry.Failures, entry.LastError
		}
	}
	r.State = decideState(probe(ctx, t), h)
	return r
}

// decideState combines the two signals into a reported state.
//
// bound comes from the port probe; h is the live daemon's record for this
// tunnel, or nil when there is no live daemon, no state file, or no entry for
// this tunnel in it.
func decideState(bound bool, h *health.Tunnel) State {
	// The probe is the authority on DOWN: whatever a state file says, a port
	// nothing accepts on is down.
	if !bound {
		return StateDown
	}
	// Something is listening but no live daemon accounts for it — a stale
	// process, an unrelated service, or a daemon started with a --tunnel
	// subset that excludes this one.
	if h == nil {
		return StateUnknown
	}
	switch {
	case h.LastFailure.After(h.LastSuccess):
		// The most recent evidence is a failure.
		return StateFailing
	case h.LastSuccess.IsZero():
		return StateIdle
	default:
		return StateOK
	}
}

func writeJSON(cmd *cobra.Command, out output) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func writeTable(cmd *cobra.Command, reports []report, live bool, statePath string) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tKIND\tLOCAL\tSTATE\tLAST OK\tFAILS\tDESTINATION\tVIA")
	for _, r := range reports {
		fails := ""
		if r.Failures > 0 {
			fails = strconv.FormatInt(r.Failures, 10)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Name, r.Kind, r.Local, r.State, since(r.LastSuccess), fails, r.Destination, r.Via)
	}
	_ = w.Flush()

	if !live {
		fmt.Fprintf(cmd.OutOrStdout(),
			"\nNo daemon is publishing health (%s).\nRun `tunneling service status`; without it only the port probe is meaningful.\n",
			statePath)
	}

	// The error behind each FAILING tunnel, once, after the table — the
	// single most useful thing here and far too long for a column.
	failing := make([]report, 0, len(reports))
	for _, r := range reports {
		if r.State == StateFailing && r.LastError != "" {
			failing = append(failing, r)
		}
	}
	sort.Slice(failing, func(i, j int) bool { return failing[i].Name < failing[j].Name })
	if len(failing) > 0 {
		fmt.Fprintln(cmd.OutOrStdout())
		for _, r := range failing {
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", r.Name, r.LastError)
		}
	}
}

// since renders a timestamp as a rough age, or "never".
func since(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// probe reports whether anything accepts a connection on the tunnel's local
// port.
//
// It connects and closes without sending anything, which the daemon
// classifies as neutral — so probing cannot manufacture the health result it
// is trying to read.
func probe(ctx context.Context, t config.Tunnel) bool {
	dialer := net.Dialer{Timeout: config.ProbeTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", config.DialAddr(t))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// via describes what the tunnel forwards through, for the VIA column.
func via(t config.Tunnel) string {
	switch t.Kind {
	case config.KindGCP:
		return "iap " + t.Project + "/" + t.Zone
	case config.KindSSH:
		return t.SSHServer()
	default:
		return ""
	}
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "emit machine-readable JSON instead of a table")
	rootCmd.AddCommand(statusCmd)
}
