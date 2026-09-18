# Server Contract: trencitos Label Output

How the trencitos cloud application produces print payloads, and what the
brotherConnect bridge consumes. This is the interface between the two systems on
the print path (Requirements.md §8, PhaseMapping.md Phase 0).

**Source:** `https://github.com/goosalex/trencitos`, reviewed 2026-09-08
(`docs/04-architecture.md §7/§7a`, `docs/01-requirements.md §5.7`,
`docs/02-open-questions.md Q23`). Findings below reflect that revision.

**Status:** The label renderer is **designed but not yet implemented** in
trencitos — `server/labels/` does not exist and the architecture doc states
"there is no code in the repository" for it. Treat this as the intended contract
to build against, and re-verify against trencitos before wiring the real
transport.

## Payload format: PDF (not raster)

trencitos produces **PDF**, not native Brother raster and not pdfkit. The label
renderer is abstracted behind a single interface whose only output is PDF bytes:

```ts
// trencitos: server/labels/types.ts (planned)
export interface LabelRenderer {
  render(sheet: LabelSheet): Promise<Uint8Array>   // PDF bytes, nothing else
}
```

- **Now:** rendered by **Playwright + `chromium_headless_shell`**, printing HTML
  with `@page { size: A4; margin: 0 }`, all dimensions in millimetres.
- **Later:** planned swap to **`pdf-lib`** (smaller footprint), behind the same
  interface. Same PDF output either way.
- The abstraction is enforced (one file may import Playwright; a contract test
  asserts the output is a valid PDF). **Nothing outside the renderer knows how
  the PDF was made — including the bridge.**

Consequence for the bridge: the payload is a **platform-printable PDF**. This
matches the brotherConnect driver, which submits the payload through the CUPS
filter chain (macOS converts PDF → URF → device). No Brother raster is generated
anywhere in trencitos, which is why the bridge's native-raster path was dropped
(see PhaseMapping.md Phase 1 print-path note).

## Geometry mismatch: A4 laser sheet vs Brother QL roll

The current trencitos label design targets a **different printer than the
bridge**:

- trencitos renders a **full A4 sheet PDF** for **HERMA 8705 stock — 24 ×
  (70 × 36 mm) die-cut labels per A4**, on a **laser printer**
  (`FR-60`, `FR-61`, §7).
- It carries laser-sheet concerns the bridge does not need: a per-tenant x/y
  page-offset calibration (`FR-61a`), and a QR "size sampler" printed at
  10/12/15/20 mm on the first cell of the sheet (§7).
- brotherConnect drives a **Brother QL-820NWB thermal roll** — single labels,
  62 mm continuous or die-cut, no A4 sheet.

The choice of label printer is trencitos' own **open question Q23** ("Which
physical label stock, and which label printer? Brother QL / Dymo / plain
paper?"), explicitly undecided pending a test print. So an A4 24-up sheet is not
directly printable on the QL, and this is the one real gap.

### The gap is a stock definition, not a format change

trencitos already parameterizes stock geometry, so supporting the QL is adding a
new `LabelStock`, not reworking the renderer:

```ts
// trencitos: server/labels/types.ts (planned)
export interface LabelStock {
  code: string            // e.g. 'HERMA_8705'  ->  add 'BROTHER_QL_62'
  pageWidthMm: number; pageHeightMm: number
  columns: number; rows: number            // A4 sheet: 3 x 8 = 24; QL: 1 x 1
  cellWidthMm: number; cellHeightMm: number
  marginTopMm: number; marginLeftMm: number
}
```

A **Brother-QL-roll stock** would be a single-cell page sized to the roll
(e.g. 62 mm wide, height per content), producing a **single-label PDF** rather
than a 24-up A4 sheet. The Phase-0 action for trencitos is to add this stock
(Q23); the bridge is already built to consume its PDF output.

## What the bridge consumes

The brotherConnect bridge is **format-agnostic within "printable document"**:

| From trencitos (per job) | Bridge use |
| --- | --- |
| PDF payload bytes | submitted verbatim through CUPS (no `-o raw`); CUPS renders to the device format (URF on macOS) |
| Label dimensions (mm) | set as a custom page size (`PageSize=Custom.<W>x<H>mm` + fit-to-page) so output fills the label, not the default media |
| Printer ID | resolves the target printer/queue |

The bridge never parses or transforms the PDF and never generates raster. It
needs the label dimensions alongside the PDF to size the page correctly (a PDF
alone printed to the default media prints tiny — see PhaseMapping.md).

## Printer registration: `printer_update` carries the resolution

When a printer is discovered (and on every status change) the bridge sends a
`printer_update` message. Besides identity, status and the sensed media, it
now carries the print-head resolution so the server can size barcodes and QR
codes for the device it is rendering for:

```json
{
  "type": "printer_update",
  "payload": {
    "printer_id": "niimbot-I711131967",
    "model": "NIIMBOT B1",
    "serial_number": "I711131967",
    "connection": "bluetooth",
    "status": "ready",
    "available": true,
    "dpi": 203,
    "loaded_width_mm": 62,
    "loaded_height_mm": 0
  }
}
```

| Field | Notes |
| --- | --- |
| `connection` | `usb`, `network` or `bluetooth` |
| `dpi` | print-head resolution in dots per inch: Brother QL **300**, NIIMBOT B1/B21 **203**. Omitted when unknown. |
| `loaded_width_mm` / `loaded_height_mm` | sensed media where the device reports it (Brother via IPP); omitted for NIIMBOT, whose RFID tag carries no dimensions |

Why `dpi` matters: the payload is a PDF in millimetres, and the bridge
rasterises it for NIIMBOT at 203 dpi. Symbols that are fine on a 300 dpi
Brother can fall below the readable minimum there. Measured on a NIIMBOT B1
with a simple Android phone (`docs/niimbot.md` §6.1): QR (29 modules) 8 mm,
Micro QR M4 6 mm on a clean white background, Code 39 2 mm high with 1 px
narrow / 2 px wide bars. The server should pick symbol sizes from the target
printer's `dpi` rather than from a fixed stock definition.

## Open items to confirm with trencitos (Phase 0)

- **Add a Brother-QL `LabelStock`** that renders single-label PDFs sized to the
  roll (Q23). Until then, trencitos only emits A4 laser sheets.
- **Confirm the job payload is one label's PDF**, not a multi-label A4 sheet,
  when the target is a QL bridge.
- **QR payload format (Q24)** — not needed by the bridge (it prints whatever the
  PDF contains) but relevant to the overall flow.
- **Consume `dpi` from `printer_update`** when choosing barcode/QR sizes per
  printer (see above).
- **Max payload size** — agree a ceiling (Requirements.md §10); PDFs with
  embedded thumbnails/fonts are larger than raster.
