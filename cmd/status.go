package cmd

import (
	"context"
	"fmt"
	"net"
	"text/tabwriter"

	"github.com/schretzi/tunneling/internal/config"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the configured tunnels and whether each local port is open",
	Long: `List every configured tunnel and probe its local port.

The probe is a loopback TCP connect, so it reports whether *something* is
listening on that port — normally this tool's daemon, but a stale process or
an unrelated service holding the port looks the same. Use ` + "`service status`" + `
to check whether the LaunchAgent itself is running.

Exits non-zero if any selected tunnel's port is closed.`,
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

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "NAME\tKIND\tLOCAL\tSTATE\tDESTINATION\tVIA")

		var closed int
		for _, t := range selected {
			state := "CLOSED"
			if probe(cmd.Context(), t) {
				state = "OPEN"
			} else {
				closed++
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				t.Name, t.Kind, config.DialAddr(t), state, t.Endpoint(), via(t))
		}
		_ = w.Flush()

		if closed > 0 {
			return fmt.Errorf("%d of %d tunnel(s) are not listening", closed, len(selected))
		}
		return nil
	},
}

// probe reports whether anything accepts a connection on the tunnel's local
// port.
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
	rootCmd.AddCommand(statusCmd)
}
