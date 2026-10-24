# Legacy macOS build: macOS 10.13 High Sierra (Intel, USB)

A second macOS build of the bridge for Intel Macs that cannot run macOS 12.
It supports USB Brother QL printers only. Companion to `Requirements.md` §2
and `PhaseMapping.md`.

| | Standard Mac builds | Legacy build |
| --- | --- | --- |
| macOS | 12 Monterey or later | **10.13.4** High Sierra or later |
| CPU | Apple Silicon (`darwin_arm64`), Intel (`darwin_amd64`) | Intel only |
| Go toolchain | 1.26 (`go.mod`) | 1.20.14 (`go.legacy.mod`, `-tags legacy`) |
| Brother QL over USB | driverless: CUPS renders the PDF | driverless as well (verified on a QL-820NWB); a raw queue with bridge-rendered raster is the fallback |
| Printer status / loaded media | live, over IPP | live over IPP on the driverless path; unknown on the raw fallback |
| NIIMBOT (Bluetooth / serial) | yes | no |
| Release asset | `bridge_<v>_darwin_amd64.tar.gz` | `bridge_<v>_darwin_amd64_legacy.tar.gz` |
| Reported version | `0.1.0-b<run>.<sha7>` | `0.1.0-b<run>.<sha7>+legacy` |

## Why a separate build

1. **Go.** Each Go release drops old macOS versions. Go 1.25+ needs macOS 12;
   Go 1.20 is the last release whose binaries run on 10.13 and 10.14.
2. **macOS 10.13.0 to 10.13.3 cannot run it at all.** Go links every system
   symbol at launch, and Go 1.20's TLS code calls `SecTrustEvaluateWithError`.
   Apple's Security sources export it from Security-58286.51.6 (10.13.4) on,
   but not in 58286.1.32 or 58286.31.2. Install the last High Sierra update
   (10.13.6) before using the bridge.
3. **Printing.** High Sierra drives the QL-820NWB as a driverless
   AirPrint-over-USB printer (`ippusb://` queue, CUPS 2.2), just like modern
   macOS, so the normal path works there: CUPS renders the server's PDF, and
   the bridge reads live status and the loaded roll over IPP. As a fallback for
   QL models or setups without such a queue, the bridge can render the label
   to Brother raster itself (`internal/printer/brother_raster.go`) and send it
   unchanged through a raw CUPS queue (`internal/printer/cups_raw.go`) created
   by `sudo bridge setup-usb`. Raw queues still work on 10.13; modern macOS
   has removed them.
4. **USB discovery.** `system_profiler -json` only exists from 10.15. Discovery
   tries `-json` once and then uses the `-xml` property list
   (`internal/printer/usb_plist.go`).
5. **Root certificates.** High Sierra's trust store has not been updated for
   years and lacks ISRG Root X2 and Let's Encrypt's 2025 roots, which the
   trencitos certificates chain to. The legacy build adds the Let's Encrypt
   roots in `internal/tlstrust/roots` to the system pool. macOS still verifies
   first; Go falls back to the bundled roots only if that fails. Hostname and
   expiry checks are unchanged. Today the server also sends an X2-by-X1
   cross-certificate, so the system check may pass on its own.

## Using it on the High Sierra Mac

```sh
tar -xzf bridge_<version>_darwin_amd64_legacy.tar.gz
cd bridge_<version>_darwin_amd64_legacy
xattr -d com.apple.quarantine ./bridge     # unsigned build (Phase 3)

# Plug in and switch on the printer, then:
./bridge list                              # status=ready, media, and the print path
./bridge print label.pdf -w 62 -h 29       # test label; it should be cut off

./bridge enroll                            # then run as on any other Mac
./bridge install
```

`bridge list` prints a `path:` line per printer. On the driverless path it
should read "CutMedia=EndOfPage cuts each label". Every job names the cut
explicitly (`CutMedia=EndOfPage` and IPP `finishings=60`, trim-after-pages):
the QL's AirPrint queue defaults CutMedia to EndOfPage, but its IPP
finishings-default is "none", and that is what reaches the printer when a job
names neither, so labels came out uncut until the bridge asked.

### Raw-queue fallback

Only needed when `bridge list` shows no print queue, or a driverless queue that
cannot cut. Create the raw queue once, as an administrator:

```sh
sudo ./bridge setup-usb                    # creates queue brotherConnect_<model>_<serial>
```

The bridge prefers a driverless queue that can cut, and uses the raw queue
otherwise. Set the printer's **command mode to Raster** for this path (QL-8xx:
the printer's settings tool or front-panel menu). A printer in P-touch Template
or ESC/P mode ignores raster jobs.

`setup-usb` runs `lpinfo -v` to find USB Brother QL printers and
`lpadmin -m raw` to create one queue each. It skips printers that already have
a bridge queue and leaves every other queue alone. To remove a queue, use
`sudo lpadmin -x brotherConnect_<model>_<serial>`.

### Media and label sizes on the raw fallback

On the driverless path CUPS lays the label out and the bridge reads the loaded
roll over IPP. The bridge cannot read the loaded roll over a raw queue, so it reports media
as unknown and you tell it the roll kind with `-roll` (or `BRIDGE_ROLL`):

- **`-roll continuous`** (the default) lays every label out on continuous tape
  whose width matches one side of the job (12, 29, 38, 50, 54 or 62 mm). The
  other side is the cut length, and the printer cuts after each label.
- **`-roll die-cut`** needs a size that matches a DK die-cut label in either
  orientation (62x29, 29x90, 62x100, 17x54, ...) and uses that label's
  geometry, with no feed margin.
- Anything else fails with a "no Brother DK ... fits" error.

```sh
./bridge print label.pdf -w 62 -h 29                 # continuous tape (default)
./bridge print label.pdf -w 62 -h 29 -roll die-cut   # DK-11209 die-cut labels
BRIDGE_ROLL=die-cut ./bridge                         # daemon with a die-cut roll
```

The job declares media type, width and length as values to check, as
`brother_ql` does, so the QL should stop with its error light if the loaded
roll does not match. That error is not visible to the bridge: CUPS reports the
job as sent. An earlier version guessed "die-cut" whenever the size matched a
die-cut label. A QL-820NWB with continuous tape then printed those labels
without cutting.

## Building and testing locally

The Go 1.20 toolchain is separate from your normal Go. Download it once into
the ignored `dist/` directory; the leading underscore keeps it out of `./...`:

```sh
curl -fsSLo dist/go1.20.tgz https://go.dev/dl/go1.20.14.darwin-arm64.tar.gz
mkdir -p dist/_go120 && tar -xzf dist/go1.20.tgz -C dist/_go120

export GOTOOLCHAIN=local GOFLAGS='-modfile=go.legacy.mod -tags=legacy'
GO=dist/_go120/go/bin/go
PKGS=$(find cmd internal -name '*.go' | sed 's|/[^/]*$||' | sort -u | grep -v '^internal/niimbot$' | sed 's|^|./|')

$GO vet $PKGS
GOARCH=amd64 $GO test $PKGS                      # x86_64, runs under Rosetta 2
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $GO build -o dist/legacy/bridge ./cmd/bridge
```

In zsh, write `${=PKGS}` to split the list. CI's `legacy-macos` job in
`.github/workflows/build.yml` runs exactly these steps and checks that the
binary is x86_64 with a macOS 10.13 minimum.

## Keeping shared code Go 1.20 compatible

Everything outside `internal/niimbot` and the two NIIMBOT glue files is
compiled by both toolchains. In that code:

- **Logging:** import `brotherConnect/internal/compat/slog`, never `log/slog`.
  It aliases `log/slog` normally and `golang.org/x/exp/slog` under
  `-tags legacy`. Add any newly used name to both files there.
- **No Go 1.21+ language or library features:** no `slices`, `maps`, `cmp`,
  `iter`, `clear`, range over integers or functions, `sync.OnceFunc`,
  method-and-path `ServeMux` patterns, `t.Context()`, and so on. The legacy CI
  job fails on these. `min` and `max` are fine in `internal/printer`, which
  has fallbacks in `compat_legacy.go`; other packages need the same if they
  use them.
- **Loop variables:** before Go 1.22 a `for` loop reuses one variable. Do not
  capture loop variables in goroutines or deferred closures, and do not keep
  their address. The legacy CI job cannot catch this. Check with:
  `go build -gcflags='brotherConnect/...=-d=loopvar=2' ./cmd/bridge`
  (prints every loop whose behaviour differs; production code has none today).
- **Build tags:** use the `legacy` tag, not `//go:build go1.21`. A `goX.Y`
  constraint also sets that file's language version, which silently downgrades
  it below the module's Go version.
- **Dependencies:** `go.legacy.mod` pins versions that still build with Go
  1.20. Never run `go mod tidy` on it: tidy looks at every build tag and would
  pull the NIIMBOT modules back in. Add requirements by hand, then run
  `go build -mod=mod` with the legacy flags to update `go.legacy.sum`.

## Field test status

Verified on a High Sierra iMac (CUPS 2.2) with a QL-820NWB on continuous 62 mm
tape, 2026-10-23: the legacy binary runs, discovers the printer, reads its live
status and loaded media, and prints PDF labels well through the driverless
queue. Labels were not cut until jobs named the cut explicitly; that fix is
built and unit-tested, and the remote queue accepted such a job in an IPP
Validate-Job check, but the physical cut has not been re-confirmed yet.

Still unverified on 10.13:

- the raw-queue fallback end to end (the field test never used it);
- `system_profiler -xml` output on 10.13 itself (the parser is tested against
  a modern capture, and the format is long-standing);
- the Keychain credential store and `launchctl bootstrap` autostart on 10.13.

Raster geometry follows Brother's QL-800 series command reference and the
`brother_ql` project's label tables. The encoder is round-trip tested against
the bridge's raster decoder, which was checked against real Brother jobs.
