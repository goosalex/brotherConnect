# brotherConnect — local label-printer bridge for trencitos

`bridge` is a small Go daemon that connects label printers sitting next to you
(Brother QL over USB, NIIMBOT B1/B21 over Bluetooth LE) to
[trencitos](https://github.com/goosalex/trencitos), a multi-tenant model-railway
collection database running on a VPS. It lets a browser session on any device,
including a phone, print a label on a printer in your home or club room without
a system print dialog, without typing IP addresses, and without printer drivers
in the browser.

## Why it exists

trencitos runs in the cloud; label printers live on a desk. Browsers cannot
reach a USB or Bluetooth printer directly, and a hosted app cannot reach a LAN.
The bridge closes that gap with one design choice that drives everything else:

- **Outbound only.** The bridge dials `wss://box.trencitos.dev/bridge` (or the dev
  box) and keeps that socket open. No inbound ports, no router configuration,
  no dynamic DNS.
- **Device-scoped identity.** The bridge enrolls once using the OAuth 2.0
  Device Authorization Grant (RFC 8628): it shows a code, you approve it in a
  browser while signed in to your tenant, and a token bound to
  *(installation, tenant)* lands in the OS credential store. Nobody types a
  token, and printers are only visible to the tenant that enrolled them.
- **Server renders, bridge streams.** trencitos produces the label as PDF (see
  `docs/server-contract.md`). For Brother printers the bridge hands the document
  to the OS print system with the right page size and checks it against the
  loaded media; it does not lay labels out itself. For NIIMBOT printers, which
  have no OS driver, the bridge rasterises the document to 1-bit and speaks the
  printer's Bluetooth protocol directly (`docs/niimbot.md`).
- **Single static binary.** Status and configuration are served as a local web
  page on `127.0.0.1:17600` and mirrored by CLI subcommands, so the same
  binary runs on macOS, Linux and Windows and on a headless box over SSH.

Full requirements are in `Requirements.md`; the phase-by-phase tracker is
`PhaseMapping.md`.

## Using the product

### 1. Install

Download the archive for your platform from the
[Releases page](https://github.com/goosalex/brotherConnect/releases), unpack it,
and put `bridge` (or `bridge.exe`) somewhere on your `PATH`.

Releases are not code-signed yet (that is Phase 3). On macOS, Gatekeeper will
refuse to open a downloaded binary until you clear the quarantine flag:

```sh
xattr -d com.apple.quarantine ./bridge
```

Or build from source (Go 1.26, and Xcode command-line tools on macOS for
Bluetooth support):

```sh
go build ./cmd/bridge
```

### 2. Enroll

```sh
bridge enroll                       # against the dev box (default)
bridge enroll -server wss://box.trencitos.dev/bridge   # production (path mirrors the dev default; confirm once the server ships)
```

The command opens your browser, you sign in to trencitos, pick or confirm your
tenant, and approve the bridge. The token is stored in the Keychain / Credential
Manager / Secret Service (with a `0600` file fallback where no store exists).
`bridge sign-out` removes it again.

### 3. Run

```sh
bridge                              # foreground; reads token + server from the credential store
bridge install                      # macOS: register a login agent so it starts automatically
bridge uninstall                    # remove the login agent
bridge status                       # ask the running daemon what it sees
```

While running, open <http://127.0.0.1:17600/> for connection state, tenant,
discovered printers with live media, and recent jobs. Printers attached to the
bridge appear in the trencitos UI for your tenant, and printing from any browser
goes to them.

### 4. Printer checks without a server

Useful when setting up hardware or while trencitos is unreachable:

```sh
bridge list                                   # USB Brother QL printers + live status/media
bridge print label.pdf -w 62 -h 45            # print one document on the first Brother printer
bridge niimbot scan                           # find NIIMBOT printers over Bluetooth LE
bridge niimbot info -name B1-                 # model, serial, firmware, battery, roll RFID
bridge niimbot print label.png -w 50 -h 30 -copies 2
bridge -offline                               # daemon with an in-memory transport
```

### 5. Developing against the bridge with no printer

`-virtual` presents a fake 62 mm continuous Brother printer. Every job it
receives is rendered to a GIF and announced on stdout, so trencitos work can be
tested end to end from a laptop:

```sh
bridge -virtual                               # connect to the server as the virtual printer
bridge print label.pdf -virtual -virtual-out out/   # render a payload locally to out/label-*.gif
```

Run `bridge -h` for every flag and the `BRIDGE_*` environment variables.

### Supported hardware today

| Printer | Transport | Status |
| --- | --- | --- |
| Brother QL series (verified: QL-820NWB) | USB, via the OS print system (macOS) | Prints; live status and loaded-media check over IPP |
| NIIMBOT B1 | Bluetooth LE (macOS, cgo build) | Prints; info, status, RFID roll data, copies |
| NIIMBOT B21 | Bluetooth LE / serial | Protocol implemented, not yet verified on hardware |
| Brother QL over the network (Bonjour/mDNS) | LAN | Planned (Phase 2) |

Linux and Windows builds run the daemon, the web UI and enrollment, but USB
discovery, printing and autostart on those platforms are still open items
(see Milestones).

## Versioning

The bridge reports its version to trencitos on every connection, so the scheme
has to distinguish builds unambiguously, not just releases.

### Current scheme (pre-1.0)

| Build | Version string | Git tag |
| --- | --- | --- |
| Local `go build` | `0.1.0-dev` | none |
| Pull-request CI build | `0.1.0-b<run>.<sha7>` | none (workflow artifact only) |
| Merge to `master` | `0.1.0-b<run>.<sha7>` | `v0.1.0-b<run>` + GitHub Release |

`<run>` is the GitHub Actions run number and `<sha7>` the short commit hash. The
value is injected at build time with `-ldflags "-X main.version=..."` and is
what `bridge status`, the web UI and the server all see. Two binaries with the
same version string are the same code.

### Proposed schema going forward

1. **Semantic Versioning** (`MAJOR.MINOR.PATCH`) for released versions, with the
   base version living in one place (the CI workflow today; a `VERSION` file or
   tag-driven derivation once releases are cut deliberately).
2. **Pre-release suffix for automatic builds.** Every merge to `master` keeps
   producing `X.Y.Z-b<run>.<sha7>`, tagged `vX.Y.Z-b<run>`. These are
   continuous builds, not releases, and `Release` on GitHub should mark them as
   *pre-release*.
3. **Plain tags for milestones.** A release without suffix (`v0.2.0`) is cut by
   hand when a phase's acceptance criteria in `Requirements.md` §19 pass:
   - `0.1.x` — Phase 1 proof of concept (where we are now).
   - `0.2.0` — Phase 2 core product: device-flow enrollment end to end,
     durable queues, mDNS, tenant isolation (criteria 9–15).
   - `0.3.0` — NIIMBOT B21 verified and Linux/Windows printing on par with macOS.
   - `1.0.0` — Phase 3: signed installers on macOS and Windows, monitoring
     (criteria 16–18). From `1.0.0` on, the wire protocol version negotiated with
     trencitos is frozen per MAJOR.
4. **Protocol version is separate.** The WSS message contract carries its own
   version (Requirements §14). A bridge version bump never implies a protocol
   bump; the server decides compatibility from the negotiated protocol version,
   and uses the bridge version only for diagnostics and update nudges.

## Artifacts

All artifacts come from `.github/workflows/build.yml`. Nothing is published
from a developer machine.

**What is built.** Four targets per run:

| Target | Runner | cgo | Notes |
| --- | --- | --- | --- |
| `linux/amd64` | Ubuntu | off | fully static |
| `linux/arm64` | Ubuntu | off | fully static (Raspberry Pi class hosts) |
| `windows/amd64` | Ubuntu | off | `bridge.exe` |
| `darwin/arm64` | macOS | **on** | native build; needed for NIIMBOT Bluetooth (CoreBluetooth) |

A `darwin/amd64` (Intel Mac) build is required by `Requirements.md` §2 but is
not in the matrix yet; it will be added as a second cgo build on the macOS
runner.

**Naming.** `bridge_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows). Each
archive contains one directory with the binary and a `BUILD_INFO.txt` holding
the version, target and full commit SHA.

**Where they land.**

- *Pull requests:* `go vet` and `go test` must pass, then all four binaries are
  uploaded as workflow artifacts and a sticky PR comment links to the run.
  Artifacts need a GitHub sign-in to download and expire after 14 days.
- *Merge to master:* the same matrix runs, the binaries are attached to a
  GitHub Release tagged `v0.1.0-b<run>` with auto-generated notes. Releases are
  durable and downloadable without sign-in. This is the only supported
  distribution channel.
- *Manual:* `workflow_dispatch` builds artifacts on demand without a release.

**What is not done yet.** No code signing, notarization, installers, checksums
or auto-update. Those are Phase 3 items; until then, treat every release as a
developer build and verify the commit SHA in `BUILD_INFO.txt` against the tag.

## Milestones

Status mirrors `PhaseMapping.md`; that file is the source of truth and carries
the per-requirement checkboxes.

### Achieved

- **Phase 1 proof of concept on macOS.** USB discovery of Brother QL printers,
  printing through the CUPS filter chain with job-derived page sizes, live
  printer state and loaded media over IPP, real `wss://` dialer with heartbeat
  and backoff reconnect, job state reporting, duplicate-job protection.
  Verified on a QL-820NWB. The field finding that native Brother raster cannot
  be sent on macOS shaped the server contract (PDF, not raster).
- **Virtual printer** (`-virtual`) so server-side work needs no hardware.
- **Local web UI and `bridge status`** on `127.0.0.1:17600`.
- **Enrollment client** implementing RFC 8628, plus OS credential store with a
  `0600` file fallback; token loading wired into the daemon.
- **Self-install autostart** on macOS via launchd (`bridge install`).
- **CI/CD**: tests and cross-compile checks on PRs, per-PR binaries, tagged
  GitHub Releases on every merge to master.
- **NIIMBOT B1/B21 support**: pure-Go protocol package, BLE and serial
  transports, bridge-side rasterisation, verified end to end on a B1 over BLE.
- **Resolution reporting**: the bridge reports printer DPI so trencitos can
  size Micro QR and text correctly.

### Upcoming

- **Phase 0 contract sign-off with trencitos.** The server-side pieces the
  bridge is built against (WSS endpoint and message schema, device-auth
  endpoints, label renderer, printer registry and UI) are still unimplemented
  upstream. Until they land, "printer appears in the trencitos UI" and a real
  device-flow enrollment remain blocked.
- **Phase 2 core product.** Web `/setup` page for enrollment, `bridge server`
  switch, tenant-scoped authorization, persistent cloud and local job queues
  with encrypted local data, Bonjour/mDNS discovery of network QL models,
  printer rename/assign/revoke, job history, hardened reconnect and retry.
- **Cross-platform parity.** USB discovery and printing on Linux and Windows,
  systemd-user and Task Scheduler autostart, NIIMBOT BLE on Linux (BlueZ) and
  Windows (WinRT), `darwin/amd64` build.
- **NIIMBOT follow-ups.** B21 hardware verification, label size from the roll's
  RFID barcode, Bluetooth permission when running under launchd.
- **Phase 3 production hardening.** Signed and notarized installers (`.pkg`/
  `.dmg`, WiX/NSIS, `.deb`/`.rpm`), optional tray icon, automatic updates,
  monitoring and diagnostics, admin controls, least-privilege installation.

## Repository layout

```
cmd/bridge/          CLI entry point and subcommands
internal/bridge/     daemon core: connection, discovery, job dispatch
internal/transport/  wss:// dialer + in-memory fake
internal/enroll/     RFC 8628 device-authorization client
internal/credstore/  OS credential store with file fallback
internal/autostart/  launchd (macOS); Linux/Windows TODO
internal/printer/    Brother backends (USB discovery, CUPS/IPP), virtual printer, renderer
internal/niimbot/    NIIMBOT protocol, BLE and serial transports, raster encoding
internal/job/        job model and state machine
internal/ui/         local status web UI and JSON API
docs/                server contract, NIIMBOT protocol notes, UI design
```

## Development

```sh
go build ./...       # build everything (macOS: env -u GOROOT go build ./... if GOROOT is stale)
go test ./...
go vet ./...
gofmt -w .
```

See `CLAUDE.md` for the working conventions used in this repository.
