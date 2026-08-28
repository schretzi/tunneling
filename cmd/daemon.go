package cmd

import (
	"github.com/schretzi/tunneling/internal/daemon"

	"github.com/spf13/cobra"
)

// daemonCmd is the foreground process. Installing and controlling the launchd
// job that runs it lives under `service` — see cmd/service.go.
var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Open the configured tunnels and hold them open (invoked by launchd)",
	Long: `Open every configured tunnel and serve them until interrupted.

This is the process the LaunchAgent runs; it is not how you install or control
that agent. Use ` + "`tunneling service`" + ` for the launchd job. Running it by
hand in a terminal works too — the log is echoed to stderr when stderr is a
terminal, as well as written to the log file.

The config is read once, at startup: every tunnel holds a bound listener for
the life of the process, so config changes need ` + "`tunneling service restart`" + `
(or Ctrl-C and rerun).

A tunnel that cannot bind its port, or whose listener dies, exits the whole
process non-zero rather than leaving a half-open set of tunnels behind;
launchd restarts it. Individual connection failures only affect that
connection.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return daemon.Run(cmd.Context(), configPath, tunnels)
	},
}

func init() {
	rootCmd.AddCommand(daemonCmd)
}
