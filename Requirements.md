# Requirements Sheet: Multi-Tenant Label-Printing Bridge

**Version:** 1.1
**Date:** September 8, 2026
**Status:** Draft

### Revision history

- **1.1** — Split acceptance criteria per phase; added Phase 0 (server contract),
  a Dependencies / External Systems section, and printer status-feedback
  requirements; clarified idempotency, job-field ownership, and media-mismatch
  handling; removed non-rendering markup.
- **1.0** — Initial draft.

## 1. Objective

Provide browser-based label printing from a cloud application to locally connected label printers without:

- Browser system print dialogs
- Manual IP-address entry
- Manual printer-token entry
- Browser printer drivers
- Operating-system restrictions

The solution must support tenants, users, printers, and tenant-specific permissions.

The cloud application is **trencitos** (a model-railway collection database).
The bridge connects locally discovered Brother label printers to that application:

- Dev: `box.dev.trencitos.dev:8443`
- Prod: `https://box.trencitos.dev/`
- Source: `https://github.com/goosalex/trencitos`

## 2. Supported Platforms

### Web application

- iOS/iPadOS browsers
- Android browsers
- Windows browsers
- macOS browsers
- Linux browsers

### Print bridge

- Windows 10/11
- macOS Intel and Apple Silicon
- Linux x64 and ARM64 where practical

### Initial printer support

- Brother QL-series printers
- USB-connected printers
- Network printers discovered through Bonjour/mDNS

Network discovery applies only to network-capable Brother QL models
(for example QL-820NWB, QL-1110NWB). USB-only models (for example QL-800)
are supported through USB discovery only. The specific models in scope for the
proof of concept are listed in Phase 1.

The bridge architecture should support additional printer brands later.

## 3. System Architecture

```text
Web browser
    │ HTTPS
    ▼
Cloud application (trencitos)
    │ Persistent WSS connection
    ▼
Local Go print bridge
    ├── USB discovery
    ├── Bonjour/mDNS discovery
    ├── Local print queue
    ├── Printer status feedback
    └── Printer protocol/driver
```

The bridge establishes an outbound connection to the cloud. No inbound network connection should be required.

## 4. User Roles

### System administrator

Can:

- Manage tenants
- View bridge installations
- Revoke bridge access
- View diagnostics

### Tenant administrator

Can:

- Authorize bridge installations
- Approve printers
- Rename printers
- Assign printers to users or locations
- Remove printers
- View tenant print history

### Tenant user

Can:

- View authorized printers
- Select a printer
- Submit print jobs
- View job status

## 5. Bridge Requirements

### Installation

The bridge must:

- Be distributed as a native executable or installer
- Open the default browser during first-time setup
- Support automatic startup
- Provide a tray or menu-bar status indicator where practical

### Authentication

The bridge authenticates using the OAuth 2.0 Device Authorization Grant
(RFC 8628) against trencitos. The bridge must:

1. Generate a unique installation ID.
2. Open a browser authorization page.
3. Allow the user to sign in.
4. Allow tenant selection or confirmation.
5. Allow explicit bridge authorization.
6. Receive a device token without exposing the user's password.
7. Store credentials securely in the operating system's credential store.

The bridge must never require manually entered tenant tokens.

Note on phasing: the full device-authorization flow is a Phase 2 requirement.
Phase 1 may use a statically provisioned development token to prove the print
path (see Phase 1).

### Connection

The bridge must:

- Establish a TLS-protected WebSocket connection using `wss://`
- Authenticate using a device-scoped token
- Negotiate a protocol version on connect (see Section 14)
- Send periodic heartbeats
- Reconnect automatically
- Use exponential backoff
- Detect revoked or expired credentials
- Support graceful shutdown

Basic reconnection is required in Phase 1; full exponential-backoff and
retry handling are hardened in Phase 2.

## 6. Printer Discovery

### USB

The bridge must:

- Detect supported Brother QL printers connected through USB
- Detect connection and disconnection events
- Report model, serial number where available, and connection type
- Avoid requiring manual configuration

### Bonjour/mDNS

The bridge must:

- Discover network-capable Brother printers advertising network services
- Detect network changes
- Report model, hostname, address, and service details
- Avoid requiring manual IP entry
- Confirm printer availability before printing

### Printer identity

Each printer should have a stable identifier based on:

- Device serial number, where available
- Otherwise a securely generated bridge-local identifier

## 7. Printer Authorization

The system must:

- Display discovered printers in the tenant administration interface
- Require explicit authorization before use
- Associate printers with one or more tenants only through authorization
- Allow printer renaming
- Allow printer revocation
- Prevent unauthorized tenants from seeing or using printers

## 8. Print Job Requirements

A print job must include:

- Unique job ID (idempotency key; see Section 10)
- Tenant ID
- User ID
- Printer ID
- Label dimensions (for validation against loaded media)
- Copies
- Print payload
- Creation timestamp
- Optional metadata

The payload is rendered server-side so output is consistent across platforms and
the bridge does not re-render the label. The label dimensions in the job size the
print (page size) and are validated against the loaded media. `Copies` is applied
by the bridge by repeating the print of the supplied payload.

**Payload format is a Phase 0 contract decision, and it is platform-dependent.**
Field testing on macOS (see below) showed the original "server sends native
Brother raster, bridge streams it verbatim" model does not work for driverless
(AirPrint/IPP) printers. The payload format must therefore be agreed per the
delivery path:

- **macOS driverless (AirPrint/ippusb) printers:** the payload must be a
  document the OS print system can render — `image/png`, `application/pdf`, or
  `image/urf`. The bridge submits it through the CUPS filter chain (not raw),
  supplying the label dimensions as a custom page size so output fills the label.
  Native Brother raster cannot be delivered here: raw CUPS queues are unsupported
  on modern macOS, direct USB (libusb) is denied by the OS, and raw-over-IPP
  makes the device jam.
- **Native Brother raster (ESC/P):** only deliverable where a raw byte path
  exists — a Linux USB printer device (`/dev/usb/lp*`) or the printer's network
  raw port (TCP 9100). Not available for USB-connected printers on macOS.

Recommended default: the server emits a rendered document (PDF or PNG) plus the
label dimensions; the bridge sizes and submits it. This keeps one server output
across platforms and lets each platform's print system handle device specifics.

If the job's label dimensions do not match the printer's loaded media, the
bridge must fail the job with a clear, human-readable reason rather than print
an incorrect label.

The bridge must:

- Validate job structure
- Queue jobs locally
- Print jobs sequentially per printer
- Report job state
- Prevent duplicate printing
- Retry temporary failures
- Avoid retrying permanent failures indefinitely

## 9. Job States

Recommended states:

```text
created
queued
sent
printing
completed
failed
cancelled
```

Each state transition should include a timestamp and diagnostic information
where appropriate. A `failed` transition must include a human-readable reason
(see Section 9a).

## 9a. Printer Status Feedback

The bridge must read printer status from the device and surface it to the cloud
and to end users in plain language. At minimum it must detect and report:

- Ready / busy
- Out of media or wrong media size
- Cover open
- Device error or cooling/pause states
- Offline / disconnected

Status conditions that prevent printing must produce a `failed` job transition
(or hold the job, per Section 16) with a message suitable for a nontechnical user.

## 10. Queue Requirements

The cloud queue must:

- Persist jobs
- Support delivery acknowledgements
- Recover after bridge restarts
- Support cancellation before printing
- Use idempotent job IDs

The local queue should:

- Persist queued jobs where practical
- Limit disk usage
- Protect stored job data
- Recover safely after power loss

### Idempotency

- The job ID is the idempotency key.
- The bridge must retain recently completed/failed job IDs for a defined
  retention window (default: 7 days) and must not reprint a job whose ID it has
  already terminally processed.
- Deduplication is scoped per printer.
- A minimal in-memory or lightweight persisted dedup cache is required in
  Phase 1 to satisfy the "no duplicate printing" acceptance criterion; the
  durable store arrives with the Phase 2 persistent queue.

### Limits

- The bridge and cloud must enforce a maximum job payload size (to be set
  during Phase 0 contract definition) and reject oversized jobs with a clear error.

## 11. Security Requirements

The system must:

- Use HTTPS and WSS exclusively
- Use short-lived authorization codes
- Use revocable device tokens
- Scope all requests by tenant
- Enforce server-side authorization
- Encrypt sensitive local data
- Avoid logging passwords, tokens, or label contents
- Validate all incoming job data
- Sign and verify production bridge releases
- Apply least-privilege operating-system permissions

### Security requirement phasing

| Requirement | Introduced in |
| --- | --- |
| HTTPS/WSS only, server-side authorization, input validation | Phase 1 |
| Credential-store token storage, tenant scoping, revocable tokens | Phase 2 |
| Encrypt persisted local job data | Phase 2 |
| Signed/verified releases, least-privilege install | Phase 3 |

## 12. Multi-Tenant Requirements

The system must enforce isolation between:

- Tenants
- Users
- Bridges
- Printers
- Print jobs
- Logs and diagnostics

Every relevant object must include tenant ownership or an equivalent authorization relationship.

A bridge must receive jobs only for tenants and printers explicitly authorized for that installation.

## 13. Web Application Requirements

The web application must provide:

- Bridge authorization workflow
- Bridge online/offline status
- Printer discovery and approval
- Printer naming
- Printer assignment
- Printer availability status
- Printer selection during printing
- Print-job status
- Print history
- Error messages suitable for nontechnical users

The web application must work on mobile browsers without requiring a browser extension.

## 14. API Requirements

Required endpoints or equivalent operations:

- Create bridge authorization request
- Complete bridge authorization
- List tenant bridges
- Revoke bridge
- List discovered printers
- Authorize printer
- Revoke printer
- Submit print job
- Get print-job status
- Cancel print job
- Retrieve diagnostics

The WSS protocol should support:

- Protocol version negotiation on connect
- Bridge registration
- Heartbeats
- Printer updates (including status feedback per Section 9a)
- Job delivery
- Job acknowledgement
- Job status updates
- Error reporting
- Token revocation

The concrete API and WSS message contract is defined jointly with trencitos in
Phase 0 before bridge implementation begins.

## 15. Monitoring and Diagnostics

The system should record:

- Bridge connection and disconnection
- Bridge software version
- Printer discovery
- Printer authorization
- Job submission
- Job delivery
- Job completion
- Job failure
- Token revocation

Logs must avoid exposing secrets or sensitive label content unnecessarily.

## 16. Offline and Failure Behavior

If the internet connection fails:

- The bridge must show offline status
- Existing local jobs may finish
- New jobs must queue or fail clearly
- The bridge must reconnect automatically

If a printer disconnects:

- Jobs should remain queued
- The printer should be shown as unavailable
- Printing should resume after reconnection
- Duplicate printing must be prevented

## 17. Administration and Metrics

The system should track:

- Active bridges
- Authorized printers
- Print volume by tenant
- Print failures
- Average print duration
- Last bridge activity
- Bridge software versions

These metrics may later support billing based on print volume or active devices.

## 18. Dependencies and External Systems

The bridge cannot be built or validated in isolation. The following external
work and dependencies are on the critical path and must be tracked:

### Trencitos server-side (largest dependency)

Every operation in Section 14 must be implemented on trencitos:
device-authorization request/complete, bridge registry, printer registry,
persistent job queue with acknowledgements, token issuance and revocation, and
the web UI in Section 13. Phase 1's browser authorization and WSS endpoints are
primarily server work; the bridge cannot complete a phase ahead of the
corresponding server capability.

### Standards and specifications

- OAuth 2.0 Device Authorization Grant (RFC 8628) for bridge authentication
- Brother QL raster command reference for rendering and validation

### Operating-system and library dependencies

- USB discovery: libusb / gousb (may require cgo, which affects single-binary
  distribution and Linux ARM64 builds) or a pure-Go alternative
- Credential storage: macOS Keychain, Windows Credential Manager,
  Linux Secret Service / libsecret (three separate integrations;
  Secret Service may be unavailable on headless Linux)
- Bonjour/mDNS discovery library
- Tray / menu-bar integration (platform-specific; "where practical")

### Procurement and infrastructure (Phase 3 lead time)

- Apple Developer ID and notarization; Windows Authenticode code-signing
  certificates (cost and multi-day acquisition lead time)
- Auto-update distribution/hosting

## 19. Acceptance Criteria

Acceptance criteria are grouped by phase. Each phase is complete only when its
own criteria pass.

### Phase 1 acceptance (proof of concept)

1. The bridge runs on at least one target platform and connects to trencitos
   over WSS using a development token.
2. The bridge discovers a supported USB Brother QL printer.
3. The printer appears in the trencitos interface.
4. A browser submits a print job and the bridge receives it over WSS.
5. The printer prints a test label with no browser print dialog.
6. Job status appears in the web application.
7. The bridge reconnects after a network interruption (basic reconnect).
8. A repeated job ID does not print twice.

### Phase 2 acceptance (core product)

9. A user installs the bridge, which opens a browser automatically.
10. The user signs into a tenant account and authorizes the bridge without
    entering a token.
11. The bridge discovers a supported network printer using Bonjour/mDNS.
12. An iOS browser submits a print job that prints successfully.
13. Users from other tenants cannot see or use the printer.
14. Jobs survive a bridge restart and resume correctly, with no duplicate prints.
15. The system prints a 50 by 70 mm box label and side-by-side train QR labels.

### Phase 3 acceptance (production hardening)

16. Signed, verifiable installers are available for Windows and macOS.
17. Device tokens are stored in the OS credential store and are revocable from
    the admin interface.
18. Monitoring reflects active bridges, print volume, and failures.

## 20. Recommended Implementation Phases

### Phase 0 — Server and protocol contract

- Define the API endpoints (Section 14) and WSS message contract with trencitos
- Agree protocol version negotiation, maximum job payload size, and error model
- Agree the print-job schema and the **payload format** (Section 8). Field
  testing showed native Brother raster is not deliverable to macOS driverless
  printers; recommended default is a server-rendered document (PDF or PNG) plus
  label dimensions, which the bridge sizes and submits per platform.

### Phase 1 — Proof of concept

- Go bridge on one target platform
- WSS connection using a statically provisioned development token
- Basic reconnect
- One networked or USB Brother QL model (in scope: to be fixed in Phase 0)
- USB discovery
- One test label
- Minimal duplicate-print protection

### Phase 2 — Core product

- Browser-based device authorization (RFC 8628) and secure token storage
- Multi-tenancy and tenant-scoped authorization
- Persistent job queue (cloud and local)
- Printer management
- Bonjour/mDNS discovery
- Job history
- Printer status feedback (Section 9a)
- Full reconnection, retry, and backoff handling

### Phase 3 — Production hardening

- Platform installers
- Signed releases
- Automatic updates
- Monitoring
- Multiple printer models
- Administrative controls
