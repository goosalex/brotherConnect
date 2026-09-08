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
| Server-rendered Brother raster format | §8 | Brother raster spec | ☐ |
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
- Print path (◐): the CUPS driver resolves the queue and sends server raster via
  `lp -o raw`; queue-match and flow are unit-tested with a fake runner, but a
  real print has not been validated on hardware. Open question: the QL-820NWB is
  set up as a driverless **ippusb/AirPrint** queue, so raw Brother raster may
  need a `usb://`/`socket://` raw queue or URF/PWG raster instead. Validate with
  `bridge -print <server-raster-file>` when the printer is on.
- Printer status (§9a): live status comes from CUPS printer-state-reasons, which
  for this ippusb queue only reflect the device when CUPS contacts it; queue
  state alone reads "ready" even when the device is off.
- trencitos UI (⊘): server-side, out of scope for this repo.

## Phase 2 — Core product

Goal: real auth, multi-tenancy, durable queue, network discovery.

| Item | Req | Depends on | Status |
| --- | --- | --- | --- |
| Device authorization flow (RFC 8628) | §5 | trencitos device-auth endpoints | ☐ |
| Secure token storage (credential store) | §5, §11 | Keychain / Cred Manager / Secret Service | ☐ |
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
| Platform installers | §5 | — | ☐ |
| Signed / verifiable releases | §11 | Apple Developer ID, Windows Authenticode | ☐ |
| Automatic updates | §5 | update hosting | ☐ |
| Monitoring + diagnostics | §15, §17 | trencitos metrics | ☐ |
| Multiple printer models | §2 | per-model raster/protocol | ☐ |
| Administrative controls | §4, §13 | trencitos admin UI | ☐ |
| Least-privilege OS install | §11 | installers | ☐ |

**Acceptance:** Requirements §19 criteria 16–18.

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
