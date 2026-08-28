package version

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func runVersionCmd(t *testing.T, appName, notice string) string {
	t.Helper()
	cmd := NewCommand(appName, notice)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command: %v", err)
	}
	return buf.String()
}

func TestCommandShape(t *testing.T) {
	cmd := NewCommand("widget", "")
	if got, want := cmd.Use, "version"; got != want {
		t.Errorf("Use = %q, want %q", got, want)
	}
	if cmd.Args == nil {
		t.Error("version must reject arguments, but Args is nil")
	}
}

func TestOutputIncludesNameVersionAndGo(t *testing.T) {
	out := runVersionCmd(t, "widget", "")

	if !strings.HasPrefix(out, "widget ") {
		t.Errorf("output should start with the app name, got:\n%s", out)
	}
	// The go line is always present; commit/built only when known, which
	// depends on how the test binary was built.
	if !regexp.MustCompile(`(?m)^  go:      go\d`).MatchString(out) {
		t.Errorf("output missing the go version line, got:\n%s", out)
	}
}

func TestLicenceNoticeIsPrintedWhenSet(t *testing.T) {
	const notice = "License GPLv3+: GNU GPL version 3 or later."

	if out := runVersionCmd(t, "widget", notice); !strings.Contains(out, notice) {
		t.Errorf("licence notice missing from output:\n%s", out)
	}
	// A project with no notice must not get a stray blank block.
	if out := runVersionCmd(t, "widget", ""); strings.HasSuffix(out, "\n\n") {
		t.Errorf("empty licence notice still produced trailing blank lines:\n%q", out)
	}
}

func TestInfoFallsBackToBuildInfo(t *testing.T) {
	// Under `go test` the ldflags are never set, so this exercises the
	// debug.ReadBuildInfo path rather than the injected one.
	v, _, _ := Info()
	if v == "" {
		t.Error("Info returned an empty version; it should always report something")
	}
}

func TestStringSummary(t *testing.T) {
	s := String("widget")
	if !strings.HasPrefix(s, "widget ") {
		t.Errorf("String() = %q, want it to start with the app name", s)
	}
	// Never a dangling "(commit " with no closing paren.
	if strings.Count(s, "(") != strings.Count(s, ")") {
		t.Errorf("String() = %q has unbalanced parentheses", s)
	}
}
