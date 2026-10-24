// Module file for the legacy macOS build (macOS 10.13.4 High Sierra and later,
// Intel, USB Brother printers only). The main module needs Go 1.26, which only
// runs on macOS 12+; Go 1.20 is the last toolchain whose binaries run on 10.13.
// This file pins dependency versions that still build with Go 1.20 and is used
// only via -modfile:
//
//	go1.20 build -modfile=go.legacy.mod ./cmd/bridge
//
// NIIMBOT support is compiled out under Go 1.20 (its Bluetooth and serial
// libraries need newer Go), so those modules are absent here. Do not run
// `go mod tidy` on this file: tidy considers every build tag and would pull the
// NIIMBOT modules back in. See docs/macos-legacy.md.
module brotherConnect

go 1.20

require (
	github.com/coder/websocket v1.8.13
	github.com/zalando/go-keyring v0.2.8
	golang.org/x/exp v0.0.0-20230713183714-613f0c0eb8a1
)
