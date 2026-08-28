package cmd

import (
	"strings"

	"github.com/schretzi/tunneling/internal/config"

	"github.com/spf13/cobra"
)

func loadConfig() (*config.Config, error) {
	return config.Load(configPath)
}

// selectedTunnels resolves --tunnel against the config, defaulting to all of
// them.
func selectedTunnels(cfg *config.Config) ([]config.Tunnel, error) {
	return cfg.SelectTunnels(tunnels)
}

// completeTunnelNames completes --tunnel from the names in the config, so
// tab-completion works against the user's actual tunnels rather than nothing.
// A config that will not load yields no completions rather than an error —
// there is nowhere sensible to report one from here.
func completeTunnelNames(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	var names []string
	for _, t := range cfg.Sorted() {
		if strings.HasPrefix(t.Name, toComplete) {
			names = append(names, t.Name+"\t"+string(t.Kind)+" tunnel on port "+t.LocalPort.String())
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
