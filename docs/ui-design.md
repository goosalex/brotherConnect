# Bridge UI Design: minimal cross-OS daemon UI

How the bridge presents **status** and **token/server/tenant config** to an
operator, on Windows 10/11, macOS (Intel + Apple Silicon), and Linux x64/ARM64
(Requirements §2, §5, §11, §16). Companion to `Requirements.md` and
`PhaseMapping.md`.

**Design stance:** the daemon is the single source of truth; the UI is a thin
client over its internal state. Keep the core **cgo-free and single-binary** (as
it is today: `system_profiler`/`lp`/`coder/websocket`, one dependency), and add
native polish only where it doesn't break that.

## 1. What the UI must do

| Surface | Content |
| --- | --- |
| **Status** | connection state (connected / offline-reconnecting / not-enrolled / token-revoked), active tenant, server (dev/prod), installation ID, discovered printers + live status/media, recent jobs, bridge version, last activity |
| **Config** | **enroll** (device flow or paste token), **server** (dev↔prod), **tenant** (display; switching = re-enroll), **sign out / revoke**, reconnect, open logs, quit |

**Note on "tenant/token config".** A device token is bound to
`(installation, tenant)` (see `docs/server-contract.md` §1). So the UI does not
edit a tenant field — it **displays** the tenant and offers **enroll /
re-enroll / sign-out**. The three config concerns collapse to: pick server →
enroll → token+tenant arrive together and are stored in the OS credential store.

## 2. The core challenge

A background daemon has no window and no main-thread UI context, and must look
native on three OSes — while the project is deliberately **cgo-free** so one
`go build` cross-compiles to all of them. Every *native* GUI toolkit breaks that.

## 3. Options compared

| Approach | Cross-OS | cgo / deps | Headless (server / SSH) | Native feel | Effort |
| --- | --- | --- | --- | --- | --- |
| **A. Local web UI** (daemon serves `127.0.0.1`, opens default browser) | identical | none (stdlib `net/http` + `embed`) | yes (port-forward) | browser, not a window | Low |
| **B. System tray / menu-bar** (`fyne.io/systray`) | Linux fragmented | cgo + Cocoa/GTK/Win32 | no (needs a session) | always-visible glyph | Med |
| **C. Full native GUI** (Wails/Fyne) | yes | cgo + OS webview | no | strong | High |
| **D. CLI only** (`bridge status/enroll`) | yes | none | yes | not for non-tech users | Trivial |

## 4. Recommendation — layered, web-UI-first

Ship **A + D now; add B later, "where practical"** (matching §5's exact hedge).

1. **Local web UI (primary, pure Go).** The daemon binds `127.0.0.1:<port>` and
   serves a small status/config page (`html/template` + `embed.FS`, still one
   binary). It **reuses the browser already opened for device-flow auth**, so
   setup and status share one surface. Identical on all three OSes; works over
   SSH port-forward for headless Linux.
2. **CLI subcommands (headless/scripting).** `bridge status`, `bridge enroll`,
   `bridge sign-out`, `bridge server <url>` — thin clients over the same
   localhost API. Covers servers/containers with no browser.
3. **Optional tray (Phase 3, build-tagged, cgo).** A thin `systray` build whose
   menu is a **status glyph + "Open settings…"** (launches the web UI) +
   "Reconnect" + "Quit". Behind a build tag so the **core stays pure-Go and
   cross-compilable**; skippable on headless / GNOME-Wayland Linux.

## 5. Cross-cutting challenges

1. **macOS main-thread rule.** Cocoa (tray/menu-bar) must own `main()` and
   `runtime.LockOSThread`. That inverts control: with a tray, the GUI owns main
   and the daemon runs in a goroutine. Keep `Run(ctx)` callable from either
   `main` (headless) or a tray goroutine. The web UI avoids this entirely.
2. **Linux tray fragmentation.** GNOME dropped legacy tray; StatusNotifierItem
   needs the AppIndicator extension; Wayland complicates it. This is why the
   tray is optional and the web UI is the guarantee.
3. **Run model = per-user login agent, not a system service.** The bridge is
   tied to a logged-in user's printers and needs a session (open a browser, show
   a tray). Target launchd **LaunchAgent** (macOS), a user-session **Scheduled
   Task / service** (Windows), and a **systemd *user* unit** (Linux) — not a
   system daemon.
4. **Credential storage (§11) is its own cross-OS problem.** Token goes in
   Keychain / Windows Credential Manager / libsecret. `zalando/go-keyring`
   covers all three **without cgo** (macOS shells `security`; Windows `wincred`
   syscalls; Linux D-Bus Secret Service). Headless Linux may have no secret
   service → fall back to an **encrypted file** (`99designs/keyring`). Today the
   token is flag/env only and not persisted — enrollment and "sign out" both
   depend on getting this right.
5. **Localhost server security.** Bind `127.0.0.1` only; validate the `Host`
   header (**DNS-rebinding** defense); require a **per-session token** on config
   endpoints (any local process/browser tab can reach localhost); **never render
   the device token**; hand the setup URL a one-time token.
6. **First-run UX.** Unenrolled daemon on a desktop **auto-opens the browser** to
   the local setup page (or straight to `/bridges/activate`); on headless, print
   the device-flow user code to the log/CLI.

## 6. The minimal screens

**Web status page (`/`):**

```text
● Connected — tenant "Alex's Collection"     server: prod
Installation: bri_83065b26…                  bridge v0.1.0

Printers
  Brother QL-820NWB   ready   media 62mm (continuous)

Recent jobs
  ✓ job_0538…  completed  07:44   62×26.6mm

[ Re-authorize ]  [ Change server ]  [ Sign out ]  [ Logs ]
```

**Config (`/setup`):** server dev/prod radio → **Enroll** → shows the 8-char
user code + "open activation page" (device flow) *or* a "paste token" field →
on success, token → credential store, page flips to Connected.

**Tray menu (later):** `● trencitos bridge — Connected (Alex's Collection)` ·
Open settings… · Reconnect now · Quit. Glyph color = connection state.

## 7. Does the UI change the deployment model?

**No — the web UI runs from the single binary.** What eventually pushes toward
installers is **productionization (autostart + code-signing) and the optional
native tray**, which would apply even without a UI.

### Stays single-binary

`embed.FS` assets, stdlib `net/http`, `open`/`xdg-open`/`start` to launch the
browser, `go-keyring` at runtime, CLI subcommands. `./bridge` gives status +
enrollment + config with no installer.

### What actually needs more than a bare binary

| Driver | More than a binary? | Why |
| --- | --- | --- |
| Autostart on login (§5) | Registration file, but **self-installable** | launchd plist / systemd user unit / Task Scheduler entry, written by `bridge install`. No packaging needed. |
| Code signing / notarization (§11) | **Yes** | Unsigned → macOS Gatekeeper + Windows SmartScreen warnings. macOS notarization is bundle/pkg/dmg-oriented. Strongest force; independent of the UI. |
| macOS menu-bar tray (optional) | **Yes** | A menu-bar agent must be an `.app` bundle (`Info.plist`, `LSUIElement`) → `.dmg`/`.pkg`. A bare Mach-O cannot cleanly be one. |
| Auto-update (Phase 3) | Effectively yes | Needs a known install location + updater. |
| Linux tray (optional) | Packaging norm | `.desktop` + icon + AppIndicator → `.deb`/`.rpm`/AppImage/Flatpak. |

The two hard "yes" rows are **signing** and the **native tray** — not the web UI.

### The middle path: a self-installing single binary

- `bridge install` → writes + enables the launchd LaunchAgent / systemd user
  unit / Windows Scheduled Task (pure Go).
- `bridge run` → the daemon. `bridge uninstall` → removes it.

Gives autostart from a single binary on all three OSes. The only things it
cannot self-solve are **OS trust (signing)** and the **macOS `.app` bundle**.

### Decision

- **Web-UI-only (recommended):** single binary + self-install through Phase 1/2.
  The browser is the UI; no `.app` bundle needed. Code-signing can wrap the
  single binary (sign the `.exe`; sign+notarize the Mach-O + a thin `.pkg`)
  without changing the app architecture.
- **Add the native tray:** macOS then needs an `.app` bundle → installers/DMG
  for at least macOS. That is the trigger.

## 8. Phasing

| Phase | UI / deployment work |
| --- | --- |
| **1–2** | Local web UI (status + config) · `bridge status`/`enroll`/`sign-out` CLI · credential-store token persistence (`go-keyring`) · `bridge install`/`uninstall` self-registered autostart. Single binary, no installer. |
| **3** | Platform installers driven by **signing/notarization + auto-update + optional native tray** (not the UI). macOS `.app`→`.pkg`/`.dmg` + `codesign`/`notarytool`; Windows WiX/NSIS or signed `.exe`+Squirrel + Authenticode; Linux `.deb`/`.rpm`/AppImage with the systemd user unit + `.desktop`. |

The UI does not change the deployment model; **signing and the optional tray
do — and only at Phase 3, where installers were already planned.**
