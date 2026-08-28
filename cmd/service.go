package cmd

import (
	"fmt"

	"github.com/schretzi/tunneling/internal/config"
	"github.com/schretzi/tunneling/internal/service"
)

func init() {
	svc := service.New(appName)

	// Runs inside `service install`, after flag parsing: --config is not yet
	// bound while init() builds the command tree, so the plist would otherwise
	// always record the default path.
	prepare := func(s *service.Service) error {
		if _, err := loadConfig(); err != nil {
			return fmt.Errorf("config invalid: %w", err)
		}
		// launchd expands nothing, so "~/..." has to be resolved here.
		absConfig, err := config.ExpandPath(configPath)
		if err != nil {
			return fmt.Errorf("resolving config path %s: %w", configPath, err)
		}
		s.WithArgs("daemon", "--config", absConfig)
		return nil
	}

	rootCmd.AddCommand(service.NewCommand(svc, prepare))
}
