package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schretzi/tunneling/internal/config"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Create and check the tunneling config file",
	Args:  cobra.NoArgs,
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Write a starter config file, if none exists yet",
	Long: `Write a commented starter config to the --config path.

Refuses to overwrite an existing file — editing yours is not this command's
job. Delete or move it first if you really want a fresh one.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := config.ExpandPath(configPath)
		if err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; not overwriting it", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}

		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
		// 0600: the config names internal hosts, projects and login names.
		if err := os.WriteFile(path, config.Example, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n  edit it, then run `%s config validate`\n", path, appName)
		return nil
	},
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Parse and check the config, exiting non-zero if it is unusable",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		path, err := config.ExpandPath(configPath)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: ok, %d tunnel(s)\n", path, len(cfg.Tunnels))
		return nil
	},
}

func init() {
	configCmd.AddCommand(configInitCmd, configValidateCmd)
	rootCmd.AddCommand(configCmd)
}
