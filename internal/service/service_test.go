package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLabelAndPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s := New("widget", "daemon")

	if got, want := s.Label(), "com.schretzi.widget"; got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}

	for _, tc := range []struct {
		name string
		got  func() (string, error)
		want string
	}{
		{"PlistPath", s.PlistPath, filepath.Join(home, "Library", "LaunchAgents", "com.schretzi.widget.plist")},
		{"LogPath", s.LogPath, filepath.Join(home, "Library", "Logs", "widget.log")},
		{"ErrLogPath", s.ErrLogPath, filepath.Join(home, "Library", "Logs", "widget.err.log")},
	} {
		got, err := tc.got()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestRenderDoesNotResolveSymlinks is the regression guard for the pinned-path
// bug: resolving symlinks turns /opt/homebrew/bin/x into
// /opt/homebrew/Caskroom/x/<version>/x, which the next `brew upgrade` deletes.
func TestRenderDoesNotResolveSymlinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	versionedDir := filepath.Join(dir, "Caskroom", "widget", "1.2.3")
	if err := os.MkdirAll(versionedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	realBin := filepath.Join(versionedDir, "widget")
	if err := os.WriteFile(realBin, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stableBin := filepath.Join(dir, "widget")
	if err := os.Symlink(realBin, stableBin); err != nil {
		t.Fatal(err)
	}

	out, err := New("widget", "daemon").WithBinary(stableBin).Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	plist := string(out)

	if !strings.Contains(plist, stableBin) {
		t.Errorf("plist does not reference the stable path %s:\n%s", stableBin, plist)
	}
	if strings.Contains(plist, "Caskroom") {
		t.Errorf("plist resolved through to the version-pinned path:\n%s", plist)
	}
}

func TestRenderContents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	bin := filepath.Join(t.TempDir(), "widget")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := New("widget", "daemon", "--config", "/etc/widget.yaml").
		WithBinary(bin).
		WithEnv("WIDGET_HELPER", "/opt/homebrew/bin/helper").
		Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	plist := string(out)

	for _, want := range []string{
		"<string>com.schretzi.widget</string>",
		"<string>daemon</string>",
		"<string>--config</string>",
		"<string>/etc/widget.yaml</string>",
		"<key>WIDGET_HELPER</key>",
		"<string>/opt/homebrew/bin/helper</string>",
		filepath.Join(home, "Library", "Logs", "widget.err.log"),
		"<key>ProcessType</key>",
		"<key>ThrottleInterval</key>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q:\n%s", want, plist)
		}
	}

	// KeepAlive must be the SuccessfulExit dict, never a bare <true/>, or a
	// deliberate `service stop` is impossible.
	if !strings.Contains(plist, "<key>SuccessfulExit</key>") {
		t.Errorf("plist does not use the KeepAlive/SuccessfulExit form:\n%s", plist)
	}

	// stdout is intentionally unset so it goes to /dev/null; only stderr is
	// captured, for panics.
	if strings.Contains(plist, "StandardOutPath") {
		t.Errorf("plist sets StandardOutPath; only StandardErrorPath is expected:\n%s", plist)
	}
}

func TestRenderEscapesXML(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	out, err := New("widget", `--flag=a&b<c>`).WithBinary("/tmp/widget").Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	plist := string(out)

	if strings.Contains(plist, "a&b<c>") {
		t.Errorf("argument was not XML-escaped:\n%s", plist)
	}
	if !strings.Contains(plist, "a&amp;b&lt;c&gt;") {
		t.Errorf("escaped argument not found:\n%s", plist)
	}
}

func TestBinaryPathRejectsGoRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := New("widget").
		WithBinary("").
		Render()
	// Whether this errors depends on how the test binary itself was built, so
	// assert on the helper instead, which is what BinaryPath keys off.
	_ = err

	if !isGoBuildPath("/var/folders/xy/T/go-build123/b001/exe/widget") {
		t.Error("isGoBuildPath did not recognise a `go run` binary path")
	}
	if isGoBuildPath("/opt/homebrew/bin/widget") {
		t.Error("isGoBuildPath flagged an installed binary")
	}
}

func TestWithEnvPreservesOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	out, err := New("widget").
		WithBinary("/tmp/widget").
		WithEnv("FIRST", "1").
		WithEnv("SECOND", "2").
		Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	plist := string(out)

	first := strings.Index(plist, "FIRST")
	second := strings.Index(plist, "SECOND")
	if first < 0 || second < 0 {
		t.Fatalf("both env keys should be present:\n%s", plist)
	}
	if first > second {
		t.Error("env entries are not in insertion order; the plist will churn between reinstalls")
	}
}
