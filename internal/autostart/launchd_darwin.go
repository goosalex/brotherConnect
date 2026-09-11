//go:build darwin

package autostart

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// launchdService registers the bridge as a launchd LaunchAgent in the user's
// GUI domain, so it starts at login and is (re)started by launchd.
type launchdService struct {
	cfg       Config
	uid       int
	plistPath string
	outLog    string
	errLog    string
}

// New returns a launchd-backed Service for the current user.
func New(cfg Config) (Service, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	logDir := cfg.LogDir
	if logDir == "" {
		logDir = filepath.Join(home, "Library", "Logs", "brotherconnect")
	}
	return &launchdService{
		cfg:       cfg,
		uid:       os.Getuid(),
		plistPath: filepath.Join(home, "Library", "LaunchAgents", cfg.Label+".plist"),
		outLog:    filepath.Join(logDir, "bridge.out.log"),
		errLog:    filepath.Join(logDir, "bridge.err.log"),
	}, nil
}

func (s *launchdService) Describe() string { return s.plistPath }

func (s *launchdService) domainTarget() string { return "gui/" + strconv.Itoa(s.uid) }
func (s *launchdService) serviceTarget() string {
	return s.domainTarget() + "/" + s.cfg.Label
}

// Install writes the plist and (re)loads it via launchctl bootstrap.
func (s *launchdService) Install() error {
	if s.cfg.Executable == "" {
		return fmt.Errorf("executable path is required to install")
	}
	if err := os.MkdirAll(filepath.Dir(s.plistPath), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.outLog), 0o700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	data, err := buildPlist(s.cfg.Label, s.cfg.Executable, s.cfg.Args, s.outLog, s.errLog)
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.plistPath, data, 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	// Unload any previous instance first so bootstrap does not fail on a
	// re-install, then load the fresh plist.
	_ = exec.Command("launchctl", "bootout", s.serviceTarget()).Run()
	if out, err := exec.Command("launchctl", "bootstrap", s.domainTarget(), s.plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall unloads the agent and removes the plist. Both steps tolerate an
// already-absent agent.
func (s *launchdService) Uninstall() error {
	_ = exec.Command("launchctl", "bootout", s.serviceTarget()).Run()
	if err := os.Remove(s.plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}

// buildPlist renders the LaunchAgent property list. RunAtLoad starts the bridge
// as soon as it is loaded (and at every login); KeepAlive restarts it if it
// stops. ThrottleInterval bounds restart frequency so a misconfigured bridge
// (e.g. not yet enrolled) does not busy-loop.
func buildPlist(label, exe string, args []string, outLog, errLog string) ([]byte, error) {
	var argsXML strings.Builder
	for _, a := range append([]string{exe}, args...) {
		argsXML.WriteString("\n      <string>")
		if err := xml.EscapeText(&argsXML, []byte(a)); err != nil {
			return nil, err
		}
		argsXML.WriteString("</string>")
	}
	esc := func(s string) (string, error) {
		var b strings.Builder
		err := xml.EscapeText(&b, []byte(s))
		return b.String(), err
	}
	labelX, err := esc(label)
	if err != nil {
		return nil, err
	}
	outX, err := esc(outLog)
	if err != nil {
		return nil, err
	}
	errX, err := esc(errLog)
	if err != nil {
		return nil, err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>%s
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ThrottleInterval</key>
  <integer>30</integer>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, labelX, argsXML.String(), outX, errX)
	return []byte(plist), nil
}
