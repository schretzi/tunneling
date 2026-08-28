// Package cmd implements the tunneling CLI.
package cmd

import (
	"fmt"
	"os"

	"github.com/schretzi/tunneling/internal/config"
	"github.com/schretzi/tunneling/internal/version"

	"github.com/spf13/cobra"
)

var (
	configPath string
	tunnels    []string
)

// appName is the binary name: it drives the launchd label, the log file
// names and the `version` output.
const appName = "tunneling"

// licenseNotice is printed by `tunneling version`.
const licenseNotice = `Copyright (C) 2026 Schretzi
License: Apache-2.0 <https://www.apache.org/licenses/LICENSE-2.0>.
This is free software: you are free to change and redistribute it.
There is NO WARRANTY, to the extent permitted by law.`

var rootCmd = &cobra.Command{
	Use:   "tunneling",
	Short: "Open a set of SSH and GCP/IAP tunnels in one step",
	Long: `Open every tunnel in ~/.config/tunneling/config.yaml at once.

Two kinds of tunnel, both a local port forward:

  gcp   through Google Cloud Identity-Aware Proxy, direct to a Compute Engine
        instance — no bastion, no SSH
  ssh   through an SSH server, authenticating with the running ssh-agent

They compose: point a "ssh" tunnel's tunnelHost/tunnelPort at the local port
of a "gcp" tunnel to reach a private service behind an IAP-only jump host.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the CLI and exits the process on error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// Root returns the root command, for tooling that needs the command tree
// without running it (e.g. the docs generator in tools/gendocs).
func Root() *cobra.Command {
	return rootCmd
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", config.DefaultPath(), "path to config file")
	rootCmd.PersistentFlags().StringSliceVar(&tunnels, "tunnel", nil, "tunnel name(s) to operate on (default: all configured tunnels)")
	_ = rootCmd.RegisterFlagCompletionFunc("tunnel", completeTunnelNames)
	// Avoid a generation-timestamp footer that would otherwise churn every
	// time docs/ is regenerated with no real content change.
	rootCmd.DisableAutoGenTag = true

	// `--version` and `version` report the same thing, from the same place.
	rootCmd.Version = version.String(appName)
	rootCmd.AddCommand(version.NewCommand(appName, licenseNotice))
}
