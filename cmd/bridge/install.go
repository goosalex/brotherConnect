package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"brotherConnect/internal/autostart"
	"brotherConnect/internal/config"
	"brotherConnect/internal/credstore"
)

// runInstall registers the bridge to start automatically at login (macOS
// launchd LaunchAgent; other platforms not yet supported). The installed agent
// runs the daemon with no arguments, so it reads its token and server from the
// OS credential store populated by `bridge enroll`.
func runInstall(log *slog.Logger) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate bridge executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if strings.HasPrefix(exe, os.TempDir()) || strings.Contains(exe, "/go-build") {
		fmt.Fprintf(os.Stderr,
			"warning: the bridge binary is at a temporary path:\n  %s\n"+
				"autostart will break once it is removed — install a stable binary\n"+
				"(e.g. in /usr/local/bin) and run 'bridge install' from there.\n\n", exe)
	}

	svc, err := autostart.New(autostart.Config{
		Label:      autostart.DefaultLabel,
		Executable: exe,
	})
	if err != nil {
		return unsupported(err)
	}
	if err := svc.Install(); err != nil {
		return err
	}
	log.Info("autostart installed", "at", svc.Describe(), "exec", exe)
	fmt.Printf("✓ Installed login agent at %s\n", svc.Describe())
	fmt.Println("  The bridge now starts automatically at login (and is running now).")
	if !enrolled() {
		fmt.Println("\n  Not enrolled yet — run 'bridge enroll' so the bridge can connect.")
	}
	return nil
}

// runUninstall removes the autostart registration.
func runUninstall(log *slog.Logger) error {
	svc, err := autostart.New(autostart.Config{Label: autostart.DefaultLabel})
	if err != nil {
		return unsupported(err)
	}
	if err := svc.Uninstall(); err != nil {
		return err
	}
	log.Info("autostart removed", "from", svc.Describe())
	fmt.Println("✓ Removed the login agent. The bridge will no longer start at login.")
	return nil
}

// unsupported annotates ErrUnsupported with the current implementation status.
func unsupported(err error) error {
	if errors.Is(err, autostart.ErrUnsupported) {
		return fmt.Errorf("%w — only macOS (launchd) is implemented so far", err)
	}
	return err
}

// enrolled reports whether credentials are stored for this bridge.
func enrolled() bool {
	dir, err := config.AppDir()
	if err != nil {
		return false
	}
	_, err = credstore.Open(dir).Load()
	return err == nil
}
