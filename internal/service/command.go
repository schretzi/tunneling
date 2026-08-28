package service

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// PrepareFunc runs inside `service install`, before anything is written.
//
// It does double duty, because both jobs have to happen after flag parsing
// rather than at init() time when the command tree is built:
//
//   - validate whatever must hold before installing (usually the config —
//     installing a job that cannot start just yields a background crash-loop);
//   - fill in ProgramArguments or EnvironmentVariables that are only known
//     now, via s.WithArgs / s.WithEnv.
type PrepareFunc func(s *Service) error

// NewCommand builds the `service` subtree for s.
//
// Every tool gets this exact tree — same verbs, same help text, same flags —
// because it is built here rather than reimplemented per repo:
//
//	service install | uninstall | start | stop | restart | status
func NewCommand(s *Service, prepare PrepareFunc) *cobra.Command {
	var binary string

	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage the " + s.Name() + " LaunchAgent",
		Long: fmt.Sprintf(`Manage the launchd job that runs %s in the background.

  label   %s
  plist   ~/Library/LaunchAgents/%s.plist
  log     ~/Library/Logs/%s.log
  stderr  ~/Library/Logs/%s.err.log

Both logs are rotated by newsyslog, configured in MacbookSetup under
etc/newsyslog.d/%s.conf.`,
			s.Name(), s.Label(), s.Label(), s.Name(), s.Name(), s.Name()),
		Args: cobra.NoArgs,
	}

	cmd.PersistentFlags().StringVar(&binary, "binary", "",
		"path to the "+s.Name()+" executable to run (default: the running one)")

	// Applied for every subcommand so `--binary` is honoured wherever it is
	// meaningful, and ignored where it is not.
	bind := func() {
		if binary != "" {
			s.WithBinary(binary)
		}
	}

	cmd.AddCommand(
		installCmd(s, prepare, bind),
		actionCmd(s, "uninstall", "Unload the LaunchAgent and remove its plist",
			"Unload the job and delete its plist. Logs in ~/Library/Logs are left in place.",
			"uninstalled", s.Uninstall),
		actionCmd(s, "start", "Load the LaunchAgent", "", "started", s.Start),
		actionCmd(s, "stop", "Unload the LaunchAgent", `Unload the job.

This is a real stop, not a kill: the plist uses KeepAlive/SuccessfulExit so
launchd does not immediately restart it. The job comes back at next login, or
on `+"`service start`"+`.`, "stopped", s.Stop),
		actionCmd(s, "restart", "Unload and reload the LaunchAgent", "", "restarted", s.Restart),
		statusCmd(s),
	)
	return cmd
}

// installCmd is separate from actionCmd because it is the only verb that runs
// the PrepareFunc and reports the plist path it wrote.
func installCmd(s *Service, prepare PrepareFunc, bind func()) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Write the LaunchAgent plist and load it",
		Long: `Write ~/Library/LaunchAgents/` + s.Label() + `.plist and load it.

Idempotent: an already-loaded job is unloaded and reloaded, so this is also
how you apply a change to the plist.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			bind()
			if prepare != nil {
				if err := prepare(s); err != nil {
					return fmt.Errorf("not installing: %w", err)
				}
			}
			if err := s.Install(); err != nil {
				return err
			}
			plistPath, err := s.PlistPath()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "installed and started %s\n  %s\n", s.Label(), plistPath)
			return nil
		},
	}
}

// actionCmd builds one of the verbs that just calls a Service method and
// prints "<past tense> <label>" on success.
func actionCmd(s *Service, use, short, long, pastTense string, run func() error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := run(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", pastTense, s.Label())
			return nil
		},
	}
}

func statusCmd(s *Service) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the LaunchAgent is installed, loaded and running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := s.Status()
			if err != nil {
				return err
			}
			WriteStatus(cmd.OutOrStdout(), st)
			return nil
		},
	}
}

// WriteStatus renders st as aligned key/value lines. Same shape in every tool,
// so the output is greppable across all of them.
func WriteStatus(w io.Writer, st Status) {
	yesNo := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}

	fmt.Fprintf(w, "label      %s\n", st.Label)
	fmt.Fprintf(w, "installed  %s\n", yesNo(st.Installed))
	fmt.Fprintf(w, "loaded     %s\n", yesNo(st.Loaded))
	fmt.Fprintf(w, "running    %s", yesNo(st.Running))
	if st.Running {
		fmt.Fprintf(w, " (pid %d)", st.PID)
	}
	fmt.Fprintln(w)
	if st.Loaded && !st.Running {
		fmt.Fprintf(w, "last exit  %d\n", st.LastExitCode)
	}
	fmt.Fprintf(w, "plist      %s\n", st.PlistPath)
	fmt.Fprintf(w, "log        %s\n", st.LogPath)
	fmt.Fprintf(w, "stderr     %s\n", st.ErrLogPath)
}
