package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"time"

	"brotherConnect/internal/config"
	"brotherConnect/internal/credstore"
	"brotherConnect/internal/enroll"
)

// runEnroll performs the OAuth 2.0 Device Authorization Grant (RFC 8628) against
// trencitos and stores the resulting device token in the OS credential store.
// Usage: bridge enroll [-server <wss-url>]. The user never types a token
// (Requirements.md §5, acceptance criterion 10).
func runEnroll(args []string, log *slog.Logger) error {
	server, ok := flagValue(args, "-server", "--server")
	if !ok {
		if server = os.Getenv("BRIDGE_SERVER_URL"); server == "" {
			server = "wss://box.dev.trencitos.dev:8443/bridge"
		}
	}

	id, err := config.InstallationID()
	if err != nil {
		return fmt.Errorf("resolve installation id: %w", err)
	}
	ecfg, err := enroll.ConfigFromServer(server, id)
	if err != nil {
		return err
	}

	// The device flow can take a while (the user signs in and authorizes in a
	// browser); bound it by the code's own expiry, handled server-side.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	client := enroll.New(ecfg)
	da, err := client.RequestCode(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("\nTo authorize this bridge, open:\n\n    %s\n\n", da.ActivationURL())
	fmt.Printf("and confirm this code:  %s\n\n", da.UserCode)
	if err := openBrowser(da.ActivationURL()); err != nil {
		log.Debug("could not open browser automatically", "err", err)
		fmt.Println("(open the URL above manually if a browser did not launch)")
	}
	fmt.Println("Waiting for authorization…")

	tok, err := client.Poll(ctx, da)
	if err != nil {
		return err
	}

	creds := credstore.Credentials{
		ServerURL:   server,
		DeviceToken: tok.AccessToken,
		TokenType:   tok.TokenType,
		TenantID:    tok.TenantID,
		TenantName:  tok.TenantName,
		ObtainedAt:  time.Now(),
	}
	if tok.ExpiresIn > 0 {
		creds.ExpiresAt = creds.ObtainedAt.Add(time.Duration(tok.ExpiresIn) * time.Second)
	}

	dir, err := config.AppDir()
	if err != nil {
		return err
	}
	store := credstore.Open(dir)
	if err := store.Save(creds); err != nil {
		return err
	}

	where := "tenant"
	if tok.TenantName != "" {
		where = fmt.Sprintf("tenant %q", tok.TenantName)
	}
	log.Info("enrolled", "store", store.Kind(), "server", server, "tenant", tok.TenantName)
	fmt.Printf("\n✓ Enrolled with %s. Token stored in %s.\n", where, store.Kind())
	fmt.Println("  Start the bridge with:  bridge")
	return nil
}

// runSignOut removes the stored credentials so the bridge is no longer enrolled
// (docs/ui-design.md §1 "sign out / revoke"). Server-side token revocation is a
// separate operation performed from the trencitos admin UI (Requirements.md §11).
func runSignOut(log *slog.Logger) error {
	dir, err := config.AppDir()
	if err != nil {
		return err
	}
	store := credstore.Open(dir)
	if err := store.Clear(); err != nil {
		return err
	}
	log.Info("signed out", "store", store.Kind())
	fmt.Println("✓ Signed out. Stored credentials cleared.")
	fmt.Println("  To fully revoke the device token, also revoke this bridge in the trencitos admin UI.")
	return nil
}

// openBrowser opens url in the operating system's default browser. It is
// best-effort: on headless hosts it returns an error and the caller falls back
// to printing the URL (docs/ui-design.md §5.6).
func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default: // linux and other unixes
		cmd, args = "xdg-open", []string{url}
	}
	return exec.Command(cmd, args...).Start()
}
