// Package version reports build information and provides the `version`
// subcommand.
//
// Every CLI exposes the same command with the same output shape, because it
// is built here rather than reimplemented per repo:
//
//	<binary> version
//	  commit:  <sha>
//	  built:   <timestamp>
//	  go:      go1.x darwin/arm64
//
// This file is duplicated verbatim in KerberosKeepAlive, LittleSnitchRules,
// OauthMailToken, macswitcher and tunneling. They are five separate modules
// with no shared dependency; keep the copies in sync by hand.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Build information, injected at link time by goreleaser (see each repo's
// .goreleaser.yaml ldflags, which must point at this package). The defaults
// are what a plain `go build` produces; Info fills in what it can from the
// embedded module data in that case.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// Info returns the version, commit and build date, preferring the link-time
// values and falling back to the VCS stamps the Go toolchain embeds when
// building from a source checkout.
func Info() (v, c, d string) {
	v, c, d = version, commit, date

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return v, c, d
	}
	if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		v = info.Main.Version
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if c == "" {
				c = s.Value
			}
		case "vcs.time":
			if d == "" {
				d = s.Value
			}
		}
	}
	return v, c, d
}

// String returns the one-line version summary used for `--version`.
func String(appName string) string {
	v, c, d := Info()
	s := fmt.Sprintf("%s %s", appName, v)
	if c != "" {
		s += " (commit " + c
		if d != "" {
			s += ", built " + d
		}
		s += ")"
	}
	return s
}

// NewCommand builds the `version` subcommand for appName.
//
// licenseNotice is printed after the build information. It is a parameter
// rather than a constant because the licence differs per project - omt is
// GPL-3.0-or-later and is *required* to show this notice prominently
// ("Appropriate Legal Notices"), which a CLI has nowhere better to put.
func NewCommand(appName, licenseNotice string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the " + appName + " version, build info and licence",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			v, c, d := Info()

			fmt.Fprintf(out, "%s %s\n", appName, v)
			if c != "" {
				fmt.Fprintf(out, "  commit:  %s\n", c)
			}
			if d != "" {
				fmt.Fprintf(out, "  built:   %s\n", d)
			}
			fmt.Fprintf(out, "  go:      %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
			if licenseNotice != "" {
				fmt.Fprintf(out, "\n%s\n", licenseNotice)
			}
			return nil
		},
	}
}
