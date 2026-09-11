# Phase Mapping and Tracking

Companion to `Requirements.md` (v1.1). Maps each requirement to the phase that
delivers it, records external dependencies, and tracks status.

**Status legend:** ☐ not started · ◐ in progress · ☑ done · ⊘ blocked

Update the Status column as work proceeds. "Req" references sections in
`Requirements.md`.

## Phase 0 — Server and protocol contract

Goal: agree the trencitos-facing contract before bridge coding begins.

| Item | Req | Depends on | Status |
| --- | --- | --- | --- |
| API endpoint contract (all operations) | §14 | trencitos team | ☐ |
| WSS message contract + version negotiation | §5, §14 | trencitos team | ☐ |
| Print-job schema | §8 | trencitos team | ☐ |
| Payload format decision (PDF/PNG vs raster) | §8 | field finding: native raster not deliverable on macOS | ☐ |
| Max job payload size + error model | §10 | trencitos team | ☐ |
| In-scope PoC printer model(s) fixed | §2 | hardware availability | ☐ |

**Exit:** contract documents agreed and versioned with trencitos.

## Phase 1 — Proof of concept

Goal: prove the local print path end-to-end with minimal auth.

| Item | Req | Depends on | Status |
| --- | --- | --- | --- |
| Go bridge on one target platform (macOS) | §2 | — | ☑ |
| WSS connection with dev token | §5 | Phase 0 contract, trencitos WSS endpoint | ◐ |
| Basic reconnect | §5, §16 | WSS connection | ☑ |
| USB discovery (Brother QL) | §6 | system_profiler (macOS, no cgo) | ☑ |
| Print one test label (server raster) | §8 | Brother raster format, printer in hand | ◐ |
| Job state reporting | §9 | WSS contract | ☑ |
| Printer appears in trencitos UI | §7, §13 | trencitos printer registry + UI | ⊘ |
| Minimal duplicate-print protection | §10 | — | ☑ |
| HTTPS/WSS only, input validation | §11 | — | ◐ |

**Acceptance:** Requirements §19 criteria 1–8.

**Notes on partials:**
- WSS connection (◐): client, handshake, heartbeat, and backoff reconnect are
  built and tested against an in-memory fake; the real `wss://` dialer is not
  wired yet (blocked on the Phase 0 contract).
- Print path (◐): **validated on real hardware** — a physical label prints from
  `bridge -print`. Resolved the open question the hard way: native Brother raster
  is **not deliverable** to this macOS driverless (AirPrint/ippusb) printer. We
  confirmed, in order: `lp -o raw` jams the device (`other-error`/`spool-area-full`);
  raw CUPS queues are "no longer supported on macOS"; direct libusb is denied
  (`Access denied`); the printer's command mode had to be set to Raster and still
  jammed. What works: submitting a document (PNG/PDF) through the CUPS filter
  chain (no `-o raw`) with a custom page size — CUPS converts to URF and prints.
  The driver was reworked accordingly (drop `-o raw`, size the page from the job
  dimensions). A "tiny print" bug traced to the queue's default 12x12mm media,
  fixed by supplying `PageSize=Custom.<W>x<H>mm`. Consequence: payload format is a
  Phase 0 decision — see Requirements §8 (recommend server emits PDF/PNG + dims).
- Printer status (§9a): read live from the device via IPP get-printer-attributes
  (printer-state + printer-state-reasons), not CUPS queue state. Verified against
  the real QL-820NWB, which also reports its loaded media (12x12mm) used for the
  §8 size check. Falls back to lpstat only if ipptool is unavailable.
- trencitos UI (⊘): server-side, out of scope for this repo.

## Phase 2 — Core product

Goal: real auth, multi-tenancy, durable queue, network discovery.

| Item | Req | Depends on | Status |
| --- | --- | --- | --- |
| Device authorization flow (RFC 8628) | §5 | trencitos device-auth endpoints | ☐ |
| Secure token storage (credential store) | §5, §11 | go-keyring (Keychain/CredMgr/Secret Service), file fallback | ☐ |
| Local web UI (status + config) | §5, §16 | stdlib net/http + embed; see docs/ui-design.md | ☐ |
| CLI: status / enroll / sign-out / server | §5 | local API | ☐ |
| Self-install autostart (bridge install/uninstall) | §5 | launchd / systemd user / Task Scheduler | ☐ |
| Multi-tenancy + tenant-scoped authz | §12 | trencitos tenant model | ☐ |
| Persistent cloud job queue + acks | §10 | trencitos queue | ☐ |
| Persistent local job queue | §10 | local storage design | ☐ |
| Encrypt persisted local job data | §11 | local queue | ☐ |
| Bonjour/mDNS discovery | §6 | mDNS library, network-capable model | ☐ |
| Printer management (rename/assign/revoke) | §7, §13 | trencitos UI | ☐ |
| Printer status feedback | §9a | device status reads | ☐ |
| Job history | §13, §15 | persistent queue | ☐ |
| Full reconnect + backoff + retry | §5, §16 | — | ☐ |

**Acceptance:** Requirements §19 criteria 9–15.

## Phase 3 — Production hardening

Goal: installable, signed, monitored, multi-model.

| Item | Req | Depends on | Status |
| --- | --- | --- | --- |
| Platform installers | §5 | macOS .app→.pkg/.dmg; Win WiX/NSIS; Linux .deb/.rpm/AppImage | ☐ |
| Signed / verifiable releases | §11 | Apple Developer ID + notarytool, Windows Authenticode | ☐ |
| Optional native tray / menu-bar (cgo, build-tagged) | §5 | systray; macOS needs an .app bundle | ☐ |
| Automatic updates | §5 | update hosting | ☐ |
| Monitoring + diagnostics | §15, §17 | trencitos metrics | ☐ |
| Multiple printer models | §2 | per-model raster/protocol | ☐ |
| Administrative controls | §4, §13 | trencitos admin UI | ☐ |
| Least-privilege OS install | §11 | installers | ☐ |

**Acceptance:** Requirements §19 criteria 16–18.

**Deployment note (see `docs/ui-design.md` §7).** The UI does **not** change the
deployment model — the local web UI runs from the single binary, and autostart
is self-installable via `bridge install`. What requires installers is
**productionization**: code-signing/notarization (so Gatekeeper/SmartScreen
trust the app) and the *optional* macOS menu-bar tray (which needs an `.app`
bundle). Both are Phase 3, and both are independent of the UI. A web-UI-only
build can stay single-binary across all three OSes; adding the native tray is
what forces `.app`/DMG packaging on macOS.

## External dependency summary

Cross-cutting blockers, tracked once here (see Requirements §18).

| Dependency | Type | Needed by | Status |
| --- | --- | --- | --- |
| trencitos API + WSS endpoints | Server | Phase 0/1 | ☐ |
| trencitos device-auth (RFC 8628) | Server | Phase 2 | ☐ |
| trencitos tenant/printer/queue models + UI | Server | Phase 1/2 | ☐ |
| Brother QL raster command reference | Spec | Phase 0/1 | ☐ |
| libusb / gousb (or pure-Go USB) | Library | Phase 1 | ☐ |
| mDNS/Bonjour library | Library | Phase 2 | ☐ |
| OS credential stores (3 platforms) | Library | Phase 2 | ☐ |
| Apple Developer ID + notarization | Procurement | Phase 3 | ☐ |
| Windows Authenticode certificate | Procurement | Phase 3 | ☐ |
| Auto-update hosting | Infrastructure | Phase 3 | ☐ |
