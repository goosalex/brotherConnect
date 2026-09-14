# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project state

This is a newly initialized Go project (`module brotherConnect`, Go 1.26). As of this writing it contains only `go.mod` and IntelliJ IDEA config (`.idea/`, `brotherConnect.iml`) — there is no Go source code, no dependencies, and no README yet. Architecture and conventions below should be established as the codebase grows; update this file when they are.

it's part of a bigger project:

Model-railway collection database: photograph a model, answer simple questions,
the system identifies it from image recognition + public databases, a human
verifies. Multi-tenant, phone-first capture, desktop-first verification.

Goal is to bridge a local (or network-local) Brother label printer to the "trencitos" application running on a VPS at a hosting company.
Dev URL: box.dev.trencitos.dev:8443
Prod: URL: https://box.trencitos.dev/
Github: https://github.com/goosalex/trencitos

## Project docs

Read these before making design decisions — they define what this bridge must do
and in what order:

- **`Requirements.md`** (v1.1) — the full requirements sheet: objective, roles,
  bridge/printer/queue/security requirements, external dependencies (§18),
  per-phase acceptance criteria (§19), and implementation phases (§20).
- **`PhaseMapping.md`** — living tracking doc that maps each requirement to its
  phase, records external dependencies, and carries status checkboxes. Update it
  as work proceeds.
- **`docs/server-contract.md`** — how trencitos produces print payloads (PDF via
  a `LabelRenderer`, not raster) and what the bridge consumes. Read before
  touching the print path or the payload format.
- **`docs/niimbot.md`** — NIIMBOT B1/B21 support: protocol, BLE/serial
  transports, print tasks, rasterisation, field log from the B1. Read before
  touching `internal/niimbot` or `internal/printer/niimbot.go`.
- **`docs/ui-design.md`** — minimal cross-OS daemon UI (status + token/server/
  tenant config): web-UI-first, CLI, optional tray, and why the UI keeps the
  single-binary deployment model (installers are a Phase-3 signing/tray concern).

Key architectural constraints from those docs: the bridge is a local Go process
that makes an **outbound** `wss://` connection to trencitos (no inbound ports);
authentication uses the OAuth 2.0 Device Authorization Grant (RFC 8628); labels
are rendered to Brother raster **server-side**, so the bridge streams bytes and
validates against loaded media rather than rendering. Work is sequenced Phase 0
(server/protocol contract) → Phase 1 (USB PoC) → Phase 2 (auth, multi-tenancy,
durable queue, mDNS) → Phase 3 (installers, signing, monitoring).

## Common commands

```sh
go build ./...              # build all packages
go run .                    # run the main package (once package main exists)
go test ./...               # run all tests
go test ./path/to/pkg       # run tests for one package
go test -run TestName ./... # run a single test by name (regex)
go vet ./...                # static analysis
gofmt -w .                  # format all files
go mod tidy                 # sync go.mod/go.sum with imports
```

## Running the bridge (`cmd/bridge`)

```sh
bridge -token <dev-token>              # connect to the server (wss://) and serve prints
bridge -offline                        # in-memory transport, no server (discovery/print dev)
bridge enroll [-server <url>]          # device-authorization flow -> token in OS credential store
bridge sign-out                        # clear stored credentials
bridge install / uninstall             # register/remove login-agent autostart (macOS launchd)
bridge list                            # one-shot: discovered USB printers + live status/media
bridge print <file> [-w 62 -h 45]      # one-shot: send a document to the real printer
bridge status                          # query a running daemon's local UI API
bridge niimbot scan                    # find NIIMBOT printers over Bluetooth LE
bridge niimbot info [-name B1-]        # model, serial, firmware, battery, roll RFID, status
bridge niimbot print FILE [-w 50 -h 30 -copies N] [-dry-run -preview out.png]
bridge -h                              # full command + option help
```

Once enrolled, the daemon reads its token/server from the OS credential store
(`internal/credstore`, via `zalando/go-keyring` with a 0600 file fallback), so
plain `bridge` needs no `-token`. `bridge install` writes a per-user launchd
LaunchAgent (`internal/autostart`); Linux/Windows autostart backends are TODO.

**Debug mode — virtual printer.** `-virtual` presents a fake `BROTHER_62` (62mm
continuous) instead of hardware; every print job is rendered to a GIF and a
notification is printed to stdout. No printer required.

```sh
bridge -virtual -token <t>                       # connect to server AS the virtual printer
bridge print label.pdf -virtual -virtual-out dir # render a payload to dir/label-*.gif locally
```

The renderer (`internal/printer/render.go`) decodes PNG/JPEG/GIF, native Brother
raster (`g`/`Z` command stream, compressed or not), and PDF (via `sips` on
macOS); unrecognised payloads are saved raw.

**NIIMBOT printers (B1/B21).** `internal/niimbot` is a pure protocol package
(packets, print tasks, 1-bit raster encoding) over a `Transport`: BLE via
`tinygo.org/x/bluetooth` (`ble.go`, **cgo on macOS**; `ble_stub.go` otherwise)
or serial/SPP via `go.bug.st/serial`. `printer.NiimbotBackend` plugs it into
discovery/queue; the daemon merges it with the Brother backends through
`printer.MultiDiscoverer`/`MultiDriver` (`-niimbot`, `-niimbot-serial`).
Unlike Brother, the bridge rasterises payloads itself for NIIMBOT. Build with
cgo on macOS (`env -u GOROOT go build ./cmd/bridge`); a `CGO_ENABLED=0` darwin
build compiles but has no BLE. Test device: B1 `B1-I711131967`.

## Notes

- Go 1.26 is required (see `go.mod`).
- macOS builds need cgo (Xcode command-line tools) for NIIMBOT BLE support.
- `.idea/` and `brotherConnect.iml` are IntelliJ project files, currently tracked in git.
