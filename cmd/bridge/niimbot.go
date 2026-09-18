package main

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"brotherConnect/internal/niimbot"
	"brotherConnect/internal/printer"
)

// runNiimbot implements the `bridge niimbot <scan|info|print>` one-shot
// commands used to validate the NIIMBOT path against real hardware
// (docs/niimbot.md). Connection selection, shared by info and print:
//
//	-addr ADDR      BLE address from `niimbot scan` (macOS: CoreBluetooth UUID)
//	-name PREFIX    BLE name prefix to scan for (default: first NIIMBOT found)
//	-serial PATH    serial/SPP port instead of BLE (e.g. /dev/cu.B1-XXXX)
//
// print also accepts -dry-run (rasterise only, no printer needed; combine with
// -preview FILE to inspect the exact 1-bit output) and -model NAME for the
// dry-run geometry (default B1).
func runNiimbot(args []string, log *slog.Logger) error {
	if len(args) == 0 {
		return errors.New("usage: bridge niimbot scan | info | print FILE [-w W -h H -copies N -density D] [-addr A | -name P | -serial PATH]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	switch args[0] {
	case "scan":
		return niimbotScan(ctx, args[1:], log)
	case "info":
		c, err := niimbotConnect(ctx, args[1:], log)
		if err != nil {
			return err
		}
		defer c.Close()
		return niimbotInfo(ctx, c)
	case "print":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return errors.New("usage: bridge niimbot print FILE [-w W -h H -copies N -density D] [-addr A | -name P | -serial PATH]")
		}
		if slices.Contains(args, "-dry-run") || slices.Contains(args, "--dry-run") {
			return niimbotPrint(ctx, nil, args[1], args[2:])
		}
		c, err := niimbotConnect(ctx, args[2:], log)
		if err != nil {
			return err
		}
		defer c.Close()
		return niimbotPrint(ctx, c, args[1], args[2:])
	default:
		return fmt.Errorf("unknown niimbot command %q", args[0])
	}
}

func niimbotScan(ctx context.Context, args []string, log *slog.Logger) error {
	timeout := 6 * time.Second
	if v, ok := flagValue(args, "-timeout", "--timeout"); ok {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}
	if !niimbot.BLEAvailable() {
		return niimbot.ErrUnsupported
	}
	fmt.Printf("Scanning for NIIMBOT printers over BLE for %s...\n", timeout)
	devs, err := niimbot.ScanBLE(ctx, timeout, log)
	if err != nil {
		return err
	}
	if len(devs) == 0 {
		fmt.Println("No NIIMBOT printers found. Is the printer on and not connected to the NIIMBOT app?")
		return nil
	}
	for _, d := range devs {
		fmt.Printf("  - %-20s model=%-8s rssi=%d addr=%s\n", d.Name, d.Model.Name, d.RSSI, d.Address)
	}
	return nil
}

// niimbotConnect opens a client per the connection flags and identifies the
// model.
func niimbotConnect(ctx context.Context, args []string, log *slog.Logger) (*niimbot.Client, error) {
	var tr niimbot.Transport
	if path, ok := flagValue(args, "-serial", "--serial"); ok {
		t, err := niimbot.OpenSerial(path, log)
		if err != nil {
			return nil, err
		}
		tr = t
		fmt.Printf("Opened %s\n", path)
	} else {
		addr, ok := flagValue(args, "-addr", "--addr")
		if !ok {
			prefix, _ := flagValue(args, "-name", "--name")
			fmt.Printf("Scanning for NIIMBOT printer %q...\n", prefix)
			match := func(d niimbot.BLEDevice) bool { return prefix == "" || strings.HasPrefix(d.Name, prefix) }
			devs, err := niimbot.ScanBLEUntil(ctx, 8*time.Second, log, match)
			if err != nil {
				return nil, err
			}
			for _, d := range devs {
				if match(d) {
					addr = d.Address
					fmt.Printf("Found %s (%s)\n", d.Name, d.Address)
					break
				}
			}
			if addr == "" {
				return nil, errors.New("no matching NIIMBOT printer found")
			}
		}
		t, err := niimbot.ConnectBLE(ctx, addr, log)
		if err != nil {
			return nil, err
		}
		tr = t
		fmt.Printf("Connected over BLE to %s\n", addr)
	}
	c := niimbot.NewClient(tr, log)
	if _, err := c.Heartbeat(ctx); err != nil {
		c.Close()
		return nil, fmt.Errorf("printer not responding: %w", err)
	}
	if _, err := c.Identify(ctx); err != nil {
		c.Close()
		return nil, fmt.Errorf("identify: %w", err)
	}
	return c, nil
}

func niimbotInfo(ctx context.Context, c *niimbot.Client) error {
	m := c.Model()
	fmt.Printf("Model:        %s (task %s, %d dpi, %d px head)\n", m.Name, m.Task, m.DPI, m.HeadPixels)
	if s, err := c.Serial(ctx); err == nil {
		fmt.Printf("Serial:       %s\n", s)
	}
	if v, err := c.Version(ctx, niimbot.InfoSoftVersion); err == nil {
		fmt.Printf("Firmware:     %s\n", v)
	}
	if v, err := c.Version(ctx, niimbot.InfoHardVersion); err == nil {
		fmt.Printf("Hardware:     %s\n", v)
	}
	if v, err := c.InfoInt(ctx, niimbot.InfoBattery); err == nil {
		fmt.Printf("Battery:      %d\n", v)
	}
	if v, err := c.InfoInt(ctx, niimbot.InfoDensity); err == nil {
		fmt.Printf("Density:      %d\n", v)
	}
	if v, err := c.InfoInt(ctx, niimbot.InfoLabelType); err == nil {
		fmt.Printf("Label type:   %d\n", v)
	}
	hb, err := c.Heartbeat(ctx)
	if err == nil {
		fmt.Printf("Heartbeat:    %s\n", describeHeartbeat(hb))
	}
	rf, err := c.RFID(ctx)
	if err == nil {
		if rf.Present {
			fmt.Printf("Label roll:   barcode=%s serial=%s type=%d used=%d/%d uuid=%s\n",
				rf.Barcode, rf.Serial, rf.Type, rf.UsedPaper, rf.TotalPaper, rf.UUID)
		} else {
			fmt.Println("Label roll:   no RFID tag read")
		}
	}
	if st, err := c.PrintStatus(ctx); err == nil {
		fmt.Printf("Print status: page=%d print=%d%% feed=%d%% error=%d\n", st.Page, st.PrintProgress, st.FeedProgress, st.Error)
	}
	return nil
}

func describeHeartbeat(hb niimbot.Heartbeat) string {
	var parts []string
	if hb.LidClosed != nil {
		parts = append(parts, fmt.Sprintf("lid_closed=%v", *hb.LidClosed))
	}
	if hb.BatteryLevel != nil {
		parts = append(parts, fmt.Sprintf("battery=%d", *hb.BatteryLevel))
	}
	if hb.PaperInserted != nil {
		parts = append(parts, fmt.Sprintf("paper=%v", *hb.PaperInserted))
	}
	if hb.RFIDReadOK != nil {
		parts = append(parts, fmt.Sprintf("rfid_ok=%v", *hb.RFIDReadOK))
	}
	parts = append(parts, fmt.Sprintf("raw=%s", hb.Raw))
	return strings.Join(parts, " ")
}

func niimbotPrint(ctx context.Context, c *niimbot.Client, path string, args []string) error {
	doc, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	w := flagFloat(args, 50, "-w", "--width")
	h := flagFloat(args, 30, "-h", "--height")
	copies := 1
	if v, ok := flagValue(args, "-copies", "--copies"); ok {
		copies, _ = strconv.Atoi(v)
	}
	density := 0
	if v, ok := flagValue(args, "-density", "--density"); ok {
		density, _ = strconv.Atoi(v)
	}
	m := niimbot.DefaultModel
	if c != nil {
		m = c.Model()
	} else if name, ok := flagValue(args, "-model", "--model"); ok {
		if mm, ok := niimbot.ModelByName(name); ok {
			m = mm
		} else {
			return fmt.Errorf("unknown model %q", name)
		}
	}

	img, format, err := printer.DecodeDocument(ctx, doc)
	if err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	fitted, err := niimbot.Fit(img, w, h, m.DPI, m.HeadPixels)
	if err != nil {
		return err
	}
	enc := niimbot.Encode(fitted, niimbot.EncodeOptions{})
	if out, ok := flagValue(args, "-preview", "--preview"); ok {
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		err = png.Encode(f, enc.Image()) // the exact 1-bit raster the head prints
		f.Close()
		if err != nil {
			return err
		}
		fmt.Printf("Preview (1-bit, as printed) written to %s\n", out)
	}
	fmt.Printf("%s (%s) as %gx%gmm -> %dx%d px at %d dpi, %d row runs, copies=%d, model %s\n",
		path, format, w, h, enc.Cols, enc.Rows, m.DPI, len(enc.Runs), copies, m.Name)
	if c == nil {
		fmt.Println("Dry run: nothing sent to a printer.")
		return nil
	}
	start := time.Now()
	err = c.Print(ctx, enc, niimbot.PrintOptions{Density: density, Copies: copies,
		Progress: func(st niimbot.PrintStatus) {
			fmt.Printf("  status: page=%d print=%d%% feed=%d%%\n", st.Page, st.PrintProgress, st.FeedProgress)
		}})
	if err != nil {
		return err
	}
	fmt.Printf("Printed in %s.\n", time.Since(start).Round(time.Millisecond))
	return nil
}
