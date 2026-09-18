# NIIMBOT Printer Support (B1 / B21)

Specification and field notes for driving NIIMBOT thermal label printers from
the bridge. Companion to `Requirements.md` (§2, §6, §8) and
`PhaseMapping.md`. Implementation: `internal/niimbot` (protocol, transports),
`internal/printer/niimbot.go` (bridge backend), `cmd/bridge/niimbot.go` (CLI).

**Status (2026-09-14):** verified end to end on a **NIIMBOT B1** (device type
4096, firmware 6.19, hardware 6.1) over **Bluetooth LE** from macOS: identity,
status, RFID roll info, and printing of PNG and PDF payloads including
multi-copy jobs. B21 variants are implemented from the community protocol
documentation and are **not yet verified on hardware** (see §9).

## 1. Requirement

Print trencitos labels on NIIMBOT B1 and B21 printers in addition to Brother
QL, with the same job model (server-rendered payload + label dimensions +
copies), the same discovery/status/queue behaviour, and without any manual
configuration beyond powering the printer on.

Differences from the Brother path that shape the design:

| | Brother QL (macOS) | NIIMBOT B1/B21 |
| --- | --- | --- |
| Link | USB / network, via the OS print system (CUPS/IPP) | Bluetooth LE (primary) or serial (USB CDC / Bluetooth SPP); no OS driver |
| Payload | PDF/PNG handed to CUPS, which renders it | **The bridge rasterises** to 1-bit rows; the printer only accepts bitmap rows |
| Discovery | `system_profiler` USB poll | BLE advertisement scan by name (`B1-<serial>`) or service UUID |
| Media sensing | IPP reports loaded media size (mm) | RFID tag reports roll barcode/serial/usage, **not** millimetres |
| Status | IPP printer-state | Heartbeat packet: lid, paper, battery; error codes on print |

## 2. Libraries evaluated

| Library | Verdict |
| --- | --- |
| `github.com/thedemons/niimgo` (Go, MIT) | Closest existing Go implementation (packet framing, B1/B21/D11/D110 tasks, serial + Linux RFCOMM). Used in the first probes; it works against the B1 over serial. **Not adopted as a dependency**: Bluetooth is Linux-only, no BLE, serial detection is USB-VID-only (misses SPP ports), request timeouts are hard-wired, the serial number is rendered as hex (it is ASCII), and there is no pacing between row packets. Its structure informed `internal/niimbot`. |
| `MultiMote/niimbluelib` (TypeScript, MIT) + wiki `printers.niim.blue` | Reference for the protocol: command ids, print tasks per model, model geometry table, heartbeat/RFID/status layouts, error codes. Our implementation follows it and is unit-tested against frames captured from the B1. |
| `AndBondStyle/niimprint` (Python) | Cross-check for framing and RFCOMM transport. |
| `tinygo.org/x/bluetooth` v0.16 | **Adopted** for BLE: one API on macOS (CoreBluetooth, **needs cgo**), Linux (BlueZ D-Bus, pure Go) and Windows (WinRT, pure Go). |
| `go.bug.st/serial` v1.7 | **Adopted** for serial/SPP ports (pure Go, all platforms). |

## 3. Transports

### 3.1 Bluetooth LE (primary)

GATT layout observed on the B1:

| UUID | Role |
| --- | --- |
| Service `E7810A71-73AE-499D-8C15-FAA9AEF0C3F2` | NIIMBOT service |
| Characteristic `BEF8D6C9-9C21-4C9E-B632-BD58C1009F9F` (read, write, write-without-response, notify) | **Commands in, replies out** — the bridge writes packets here and subscribes to notifications |
| Service `49535343-FE7D-4AE5-8FA9-9FAFD205E455` | Microchip transparent UART; also accepts commands (`…8841…` write) with replies notified on the NIIMBOT characteristic. Not used. |

- Advertisement: local name `B1-<serial>` (e.g. `B1-I711131967`); the B1 does
  **not** advertise its service UUID, so discovery matches known model name
  prefixes (`B1-`, `B21-`, `B21S-`, `B18-` …) and falls back to the service UUID.
- MTU: 237 bytes for write-without-response on macOS. The longest packet (a
  384-px bitmap row) is 61 bytes, so one packet per write.
- Round trip per command: ~60 ms.
- Addresses are platform-specific: CoreBluetooth UUID on macOS, MAC on
  Linux/Windows. The printer's stable identity is its serial, not the address.
- **macOS needs cgo** (CoreBluetooth). A `CGO_ENABLED=0` darwin build compiles
  the stub (`ble_stub.go`) and logs that NIIMBOT BLE is unavailable; serial
  ports still work. CI builds macOS natively with cgo (`.github/workflows`).
- macOS Bluetooth permission: CoreBluetooth prompts the *hosting process* (the
  terminal, or the bridge binary when launched by launchd). An unsigned
  launchd agent may not get a prompt; signing is a Phase 3 item.

### 3.2 Serial (USB CDC or Bluetooth Classic SPP)

The same packet protocol runs over a serial port at 115200 baud: USB
(`/dev/ttyACM*`, `COMn`) or the printer's Bluetooth Classic SPP profile
(`/dev/cu.B1-<serial>` on macOS after pairing, `rfcomm` on Linux).
Configure with `-niimbot-serial PATH[,PATH]`.

Field finding: the macOS SPP port answered correctly (heartbeat, info, RFID)
but **wedged after an aborted print** whose rows were sent without pacing; the
RFCOMM link never came back up in that session while BLE kept working. Treat
SPP on macOS as a fallback and pace writes (see §6).

## 4. Packet format

```
55 55 <cmd> <len> <data…> <xor> AA AA
```

- `len` = data length (one byte, max 255).
- `xor` = XOR of `cmd`, `len` and every data byte.
- The official app's very first packet is `Connect` (`0xC1`) prefixed with a
  bare `0x03`; the B1 does not require it.
- Replies use their own command ids (mostly `cmd + 1`; info replies are
  `0x40 + key`; set-commands reply `cmd + 0x10`). `0xDB` is a print error
  (`data[0]` = error code, §7); `0x00` means "not supported".
- Transports may split or merge frames; `niimbot.Parser` reassembles and
  resynchronises on `55 55`.

Commands used by the bridge (request → reply):

| Request | Reply | Data |
| --- | --- | --- |
| `DC` Heartbeat | `DD`/`D9`/`DE`/`DF` | `01` → status bytes (§5.3) |
| `40` PrinterInfo | `40+key` | key: 1 density, 3 label type, 7 auto-off, **8 device type**, 9 firmware, 10 battery, **11 serial (ASCII)**, 12 hardware |
| `1A` RfidInfo | `1B` | uuid[8], vstring barcode, vstring serial, u16 total, u16 used, u8 type |
| `21` SetDensity | `31` | 1..5 |
| `23` SetLabelType | `33` | 1 gap, 2 black mark, 3 continuous, 5 transparent |
| `01` PrintStart | `02` | B1: `u16 totalPages, 0,0,0,0, pageColor`; B21 V1/D110: `01` |
| `03` PageStart | `04` | `01` |
| `13` SetPageSize | `14` | B1: `u16 rows, u16 cols, u16 copies`; B21 V1/D110: `u16 rows, u16 cols` |
| `15` PrintQuantity | `16` | D110 task only: `u16` |
| `20` PrintClear | `30` | D110 task only |
| `84` PrintEmptyRow | — | `u16 row, u8 repeat` |
| `85` PrintBitmapRow | — | `u16 row, 3 count bytes, u8 repeat, bits…` (§6) |
| `86` PrinterCheckLine | `D3` | B21 V1: `u16 row, 01` every 200 rows |
| `E3` PageEnd | `E4` | `01`; B1 first emits unsolicited `D3` (check line) packets |
| `A3` PrintStatus | `B3` | `u16 page, u8 print%, u8 feed%, …, u8 error @6` |
| `F3` PrintEnd | `F4` | `01` → `01` when finished (B21 V1 polls this) |
| `DA` CancelPrint | `D0` | `01` |

Captured B1 frames used as test vectors live in `internal/niimbot/*_test.go`.

## 5. Models and print tasks

Geometry and task mapping follow niimbluelib's model library.

| Model | Device type | DPI | Head px | Task | Verified |
| --- | --- | --- | --- | --- | --- |
| B1 | 4096 | 203 | 384 | B1 | **yes** |
| B21 | 768 | 203 | 384 | B21_V1 | no |
| B21_C2B | 771, 775 | 203 | 384 | B1 | no |
| B21_L2B | 769 | 203 | 384 | B21_V1 | no |
| B21S | 777 | 203 | 384 | D110 | no |
| B21S_C2B | 776 | 203 | 384 | D110 | no |
| B18 | 3584 | 203 | 96 | B1 | no |

Not covered: B21_PRO (300 dpi, 591 px, "D110M_V4" task) and other 2025
firmware variants. An unknown device type is driven with the B1 task and a
warning.

### 5.1 Task B1 (verified)

```
SetDensity → SetLabelType → PrintStart(totalPages=copies)
PageStart → SetPageSize(rows, cols, copies) → rows… → PageEnd (wait E4)
poll PrintStatus every 300 ms until page == copies → PrintEnd
```

Copies are handled by the printer (page count), one row stream. Observed:
240-row label, 1 copy: 4.4 s; 2 copies: 4.7 s.

### 5.2 Task B21_V1 / D110

B21_V1: `PrintStart(01)`, then per copy `PageStart, SetPageSize(rows, cols),
rows (total-count headers, check line every 200 rows), PageEnd`; finish by
polling `PrintEnd` until it answers `01`.
D110: `PrintStart(01)`, `PrintClear, PageStart, SetPageSize(rows, cols),
PrintQuantity(copies), rows, PageEnd`; finish by `PrintStatus` polling.

### 5.3 Status

Heartbeat `DD` reply, 13 bytes on the B1: `[9]` lid (0 = closed), `[10]`
battery 0–4, `[11]` paper (0 = inserted), `[12]` RFID read ok. Other lengths
(10/19/20) and the `D9` variant are decoded per niimbluelib. Mapping to the
bridge vocabulary (Requirements §9a): lid open → `cover_open`, no paper →
`out_of_media`, else `ready`; print errors `0x01` cover / `0x02`,`0x08` paper /
`0x10` wrong paper map likewise, others → `error`.

RFID: the B1 roll reported barcode `10262260`, serial `PZ1I710300003588`,
type 1 (gap labels), `used/total = 3/276`; `used` increments per printed
label. The tag carries **no label dimensions**; a barcode → size table is
future work, so `LoadedMedia` is unknown and the §8 media check is skipped for
NIIMBOT printers.

## 6. Rasterisation

The printer needs 1-bit rows across the print head (x) along the feed (y).
Pipeline (`printer.DecodeDocument` → `niimbot.Fit` → `niimbot.Encode`):

1. Decode the payload: PDF (rasterised with `sips` at ~1200 px on macOS),
   PNG/JPEG/GIF.
2. **Fit** to the job's label size at the model's DPI: `cols = width_mm/25.4*dpi`
   (clamped to the head width), `rows = height_mm/25.4*dpi`; rotate 90° when
   the payload's orientation does not match the label; scale uniformly with a
   box filter, centre, white background.
3. **Encode**: luminance threshold at 50 % (no dithering by default — crisp
   text/QR), pad columns to a multiple of 8, MSB-first bits (1 = black), merge
   identical consecutive rows into `repeat` counts (max 255), blank rows as
   `PrintEmptyRow`.
4. Bitmap row header count bytes: B1 family "split" mode — black pixels per
   third of the head (16-byte chunks for 384 px); B21 V1 "total" mode —
   `[0, low, high]` of the row total.
5. **Pacing**: 10 ms between consecutive writes (niimbluelib default); the
   firmware drops back-to-back packets. The first packet on a fresh link is
   often lost, so the heartbeat probe retries.

A 50×30 mm label at 203 dpi is 384×240 px (400 px clamped to the head).

### 6.1 Barcode legibility (measured on the B1, 2026-09-14)

Test labels were printed through this pipeline (vector QR in a 50×30 mm PDF;
pixel-exact 384×240 PNG for Code 39) and scanned with a simple Android phone.
These are real readings, not margins:

| Symbology | Modules / bars | Content | Smallest that scanned | Dots |
| --- | --- | --- | --- | --- |
| QR, ECC M | 29 (version 3) | `https://box.trencitos.dev/i/000123` | **8 mm** | 2.2 px / module |
| Micro QR M4, ECC L | 17 | `HTTP://TRN.DEV/AB123` (alphanumeric) | **6 mm** (5 mm did not read); 6/7/8 mm reference set printed | 2.8 px / module |
| Code 39 | 6 chars + start/stop | `TR4B2K` | **2 mm high, 1 px narrow / 2 px wide bars** (12.9 mm wide) | narrow 0.125 mm |

**6 mm is the minimum for a Micro QR on this 203 dpi NIIMBOT head, and only
on a clean, calm background such as a plain white label** — busy surroundings
(text, graphics or edges close to the symbol) defeat the scanner at that size,
so keep the quiet zone generous. Micro QR holds at most 21 alphanumeric /
15 byte characters and is not supported by every scanner app. Use `bridge niimbot print FILE -dry-run
-preview out.png` to inspect the exact 1-bit raster before printing; for
1-px barcodes supply a PNG at the label's pixel size (8 dots/mm) so no
resampling occurs.

## 7. Bridge integration

- `printer.NiimbotBackend` implements `Discoverer`, `Driver` and `Owner`.
  Printer ID `niimbot-<serial>` (e.g. `niimbot-I711131967`), model
  `NIIMBOT B1`, connection `bluetooth`.
- Discovery: BLE scan (5 s) every 20 s; new devices are connected once to read
  device type, serial, heartbeat and RFID, then disconnected. A device absent
  from 3 consecutive scans (powered off — the B1 auto-powers-off after
  ~45–60 min idle) is reported disconnected. Serial ports from
  `-niimbot-serial` are probed each cycle.
- The link is only held during identify/status/print; the printer stays
  visible to the NIIMBOT app in between. Scanning is skipped while a print
  holds the link.
- Print: rasterise first (bad payload never reaches the printer), connect,
  heartbeat (cover/paper → `ErrNotPrintable`), stream, poll, `PrintEnd`,
  disconnect. On any protocol error the client sends `PrintEnd` + `CancelPrint`
  so the printer is not left mid-job. The queue applies copies by repeating
  `Print`, so each copy is a separate short session.
- Wiring: `printer.MultiDiscoverer` / `MultiDriver` merge the NIIMBOT backend
  with the Brother USB/CUPS backends; `MultiDriver` routes by ID prefix.
- Config: `-niimbot` (default on, `BRIDGE_NIIMBOT`), `-niimbot-serial`
  (`BRIDGE_NIIMBOT_SERIAL`).
- CLI: `bridge niimbot scan | info | print FILE [-w -h -copies -density]
  [-addr | -name | -serial] [-dry-run -preview out.png]`.

## 8. Field log (B1, 2026-09-14)

1. Serial/SPP probe with niimgo: heartbeat lost (first packet), then info,
   RFID fine. Unpaced 240-row print: printer sent `D3 00C7 01` (check line
   199) 2 s after PageEnd, no `E4`; link then dead for the rest of the day
   (macOS showed the device "Not Connected" with the port open).
2. BLE probe (CoreBluetooth): full GATT above; heartbeat/info/RFID/status all
   answered on `BEF8D6C9`.
3. Go client over BLE: info in ~0.5 s; test pattern printed (4.4 s); PDF label
   with 2 copies printed (4.7 s); RFID `used` 0 → 1 → 3 confirms labels.
4. Daemon (`bridge -offline`): printer discovered, identified and listed as
   `NIIMBOT B1 (niimbot-I711131967) ready` in `bridge status`.

## 9. Open items

- Verify B21 (V1 task), B21_C2B and B21S on hardware; add B21_PRO (300 dpi,
  D110M_V4 task) if needed.
- Label size from RFID barcode (table) so the §8 media check applies.
- Linux (BlueZ) and Windows (WinRT) BLE runs; Linux also has `rfcomm` SPP.
- macOS Bluetooth permission for the launchd agent (Phase 3 signing).
- Density/label-type from job metadata (currently model default / gap labels).
- Reconnect strategy if a print fails mid-stream (currently the job fails and
  the queue reports it; the next job reconnects).
