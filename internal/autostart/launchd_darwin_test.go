//go:build darwin

package autostart

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestBuildPlist(t *testing.T) {
	data, err := buildPlist("dev.trencitos.bridge", "/usr/local/bin/bridge",
		[]string{"-server", "wss://a&b/bridge"}, "/tmp/out.log", "/tmp/err.log")
	if err != nil {
		t.Fatalf("buildPlist: %v", err)
	}
	s := string(data)
	for _, want := range []string{
		"<string>dev.trencitos.bridge</string>",
		"<string>/usr/local/bin/bridge</string>",
		"<string>-server</string>",
		"<string>wss://a&amp;b/bridge</string>", // XML-escaped ampersand
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"<string>/tmp/out.log</string>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("plist missing %q\n---\n%s", want, s)
		}
	}
}

// TestLaunchdInstallUninstall exercises the real launchctl bootstrap/bootout
// against a harmless long-running command under a throwaway label. It is opt-in
// (it touches ~/Library/LaunchAgents and spawns a process) via
// BRIDGE_TEST_LAUNCHD=1.
func TestLaunchdInstallUninstall(t *testing.T) {
	if os.Getenv("BRIDGE_TEST_LAUNCHD") == "" {
		t.Skip("set BRIDGE_TEST_LAUNCHD=1 to run the live launchctl test")
	}
	svc, err := New(Config{
		Label:      DefaultLabel + ".selftest",
		Executable: "/bin/sh",
		Args:       []string{"-c", "while true; do sleep 3600; done"},
		LogDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ls := svc.(*launchdService)
	t.Cleanup(func() { _ = svc.Uninstall() })

	if err := svc.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(ls.plistPath); err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	if out, err := exec.Command("launchctl", "print", ls.serviceTarget()).CombinedOutput(); err != nil {
		t.Fatalf("service not loaded: %v: %s", err, out)
	}

	if err := svc.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(ls.plistPath); !os.IsNotExist(err) {
		t.Fatalf("plist still present after uninstall: %v", err)
	}
	// Uninstall is idempotent.
	if err := svc.Uninstall(); err != nil {
		t.Fatalf("Uninstall (idempotent): %v", err)
	}
	_ = strconv.Itoa(ls.uid) // uid is used to build the domain target
}
