package printer

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"sync"
	"testing"
	"time"

	"brotherConnect/internal/niimbot"
)

// scriptedPrinter emulates a NIIMBOT B1 at the packet level: it answers the
// commands the backend uses and records what it received.
type scriptedPrinter struct {
	mu        sync.Mutex
	lidOpen   bool
	noPaper   bool
	rows      int
	pageSizes [][]byte
	closed    bool
	rx        chan []byte
	parser    niimbot.Parser
	statusN   int
}

func newScripted() *scriptedPrinter { return &scriptedPrinter{rx: make(chan []byte, 256)} }

func (s *scriptedPrinter) send(p niimbot.Packet) {
	b, _ := p.Marshal()
	s.rx <- b
}

func (s *scriptedPrinter) Write(ctx context.Context, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.parser.Feed(raw) {
		switch p.Cmd {
		case niimbot.CmdHeartbeat:
			d := make([]byte, 13)
			d[10] = 4
			if s.lidOpen {
				d[9] = 1
			}
			if s.noPaper {
				d[11] = 1
			}
			d[12] = 1
			s.send(niimbot.Packet{Cmd: niimbot.RespHeartbeatAdv1, Data: d})
		case niimbot.CmdPrinterInfo:
			switch niimbot.InfoKey(p.Data[0]) {
			case niimbot.InfoDeviceType:
				s.send(niimbot.Packet{Cmd: 0x48, Data: []byte{0x10, 0x00}})
			case niimbot.InfoSerial:
				s.send(niimbot.Packet{Cmd: 0x4B, Data: []byte("I711131967")})
			default:
				s.send(niimbot.Packet{Cmd: niimbot.RespNotSupported, Data: []byte{1}})
			}
		case niimbot.CmdRfidInfo:
			s.send(niimbot.Packet{Cmd: niimbot.RespRfidInfo, Data: []byte{0}})
		case niimbot.CmdSetDensity, niimbot.CmdSetLabelType, niimbot.CmdPrintStart, niimbot.CmdPageStart,
			niimbot.CmdPageEnd, niimbot.CmdPrintEnd, niimbot.CmdCancelPrint:
			if p.Cmd == niimbot.CmdPrintStart && s.lidOpen {
				s.send(niimbot.Packet{Cmd: niimbot.RespPrintError, Data: []byte{byte(niimbot.ErrCoverOpen)}})
				continue
			}
			resp := map[byte]byte{
				niimbot.CmdSetDensity: niimbot.RespSetDensity, niimbot.CmdSetLabelType: niimbot.RespSetLabelType,
				niimbot.CmdPrintStart: niimbot.RespPrintStart, niimbot.CmdPageStart: niimbot.RespPageStart,
				niimbot.CmdPageEnd: niimbot.RespPageEnd, niimbot.CmdPrintEnd: niimbot.RespPrintEnd,
				niimbot.CmdCancelPrint: niimbot.RespCancelPrint,
			}[p.Cmd]
			s.send(niimbot.Packet{Cmd: resp, Data: []byte{1}})
			if p.Cmd == niimbot.CmdPrintStart {
				s.statusN = 0
			}
		case niimbot.CmdSetPageSize:
			s.pageSizes = append(s.pageSizes, p.Data)
			s.send(niimbot.Packet{Cmd: niimbot.RespSetPageSize, Data: []byte{1, 0}})
		case niimbot.CmdEmptyRow:
			s.rows += int(p.Data[2])
		case niimbot.CmdBitmapRow:
			s.rows += int(p.Data[5])
		case niimbot.CmdPrintStatus:
			s.statusN++
			d := make([]byte, 10)
			if s.statusN >= 2 {
				binary.BigEndian.PutUint16(d, 1)
				d[2], d[3] = 100, 100
			}
			s.send(niimbot.Packet{Cmd: niimbot.RespPrintStatus, Data: d})
		}
	}
	return nil
}
func (s *scriptedPrinter) Receive() <-chan []byte { return s.rx }
func (s *scriptedPrinter) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.rx)
	}
	return nil
}
func (s *scriptedPrinter) Kind() string { return "fake" }

// fakeConnector hands out a fresh session on each connect to one scripted
// printer, and reports it in scans while `advertising` is true.
type fakeConnector struct {
	mu          sync.Mutex
	advertising bool
	sessions    []*scriptedPrinter
	lidOpen     bool
	scans       int
}

func (f *fakeConnector) Scan(ctx context.Context, d time.Duration) ([]niimbot.BLEDevice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scans++
	if !f.advertising {
		return nil, nil
	}
	m, _ := niimbot.ModelByName("B1")
	return []niimbot.BLEDevice{{Name: "B1-I711131967", Address: "aa-bb", RSSI: -40, Model: m}}, nil
}
func (f *fakeConnector) ConnectBLE(ctx context.Context, address string) (niimbot.Transport, error) {
	if address != "aa-bb" {
		return nil, errors.New("unknown address")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s := newScripted()
	s.lidOpen = f.lidOpen
	f.sessions = append(f.sessions, s)
	return s, nil
}
func (f *fakeConnector) OpenSerial(path string) (niimbot.Transport, error) {
	return nil, errors.New("no serial")
}

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if x < w/2 {
				c = color.RGBA{0, 0, 0, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newTestBackend(fc *fakeConnector) *NiimbotBackend {
	return NewNiimbotBackend(NiimbotOptions{
		Connector:      fc,
		ScanInterval:   20 * time.Millisecond,
		ScanDuration:   time.Millisecond,
		StatusInterval: -1, // no periodic refresh
		MissedScans:    2,
	})
}

func collect(events <-chan Event, n int, timeout time.Duration) []Event {
	var out []Event
	deadline := time.After(timeout)
	for len(out) < n {
		select {
		case ev, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
	return out
}

func TestNiimbotDiscoveryConnectAndExpire(t *testing.T) {
	fc := &fakeConnector{advertising: true}
	b := newTestBackend(fc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := b.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(events, 1, time.Second)
	if len(evs) != 1 || evs[0].Kind != EventConnected {
		t.Fatalf("expected connected event, got %+v", evs)
	}
	p := evs[0].Printer
	if p.ID != "niimbot-I711131967" || p.Model != "NIIMBOT B1" || p.Connection != ConnectionBluetooth || p.Status != StatusReady || p.DPI != 203 {
		t.Errorf("printer %+v", p)
	}
	if !b.Owns(p.ID) || b.Owns("SN-BROTHER") {
		t.Error("Owns should match niimbot- IDs only")
	}
	if st, err := b.Status(ctx, p.ID); err != nil || st != StatusReady {
		t.Errorf("status %v %v", st, err)
	}
	// Identification must have closed its session.
	fc.mu.Lock()
	closed := len(fc.sessions) == 1 && fc.sessions[0].closed
	fc.mu.Unlock()
	if !closed {
		t.Error("identify session should be closed after discovery")
	}

	// Printer powers off: after MissedScans it is reported disconnected.
	fc.mu.Lock()
	fc.advertising = false
	fc.mu.Unlock()
	evs = collect(events, 1, 2*time.Second)
	if len(evs) != 1 || evs[0].Kind != EventDisconnected || evs[0].Printer.ID != p.ID {
		t.Fatalf("expected disconnected, got %+v", evs)
	}
	if len(b.List()) != 0 {
		t.Error("expired printer should be gone from List")
	}
}

func TestNiimbotPrintRasterisesAndReportsNotPrintable(t *testing.T) {
	fc := &fakeConnector{advertising: true}
	b := newTestBackend(fc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := b.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	collect(events, 1, time.Second)
	id := "niimbot-I711131967"

	if err := b.Print(ctx, "niimbot-nope", testPNG(t, 10, 10), PrintOptions{WidthMM: 50, HeightMM: 30}); !errors.As(err, new(ErrUnknownPrinter)) {
		t.Errorf("unknown printer: %v", err)
	}
	if err := b.Print(ctx, id, testPNG(t, 10, 10), PrintOptions{}); err == nil {
		t.Error("missing dimensions should fail before connecting")
	}

	if err := b.Print(ctx, id, testPNG(t, 100, 60), PrintOptions{WidthMM: 50, HeightMM: 30}); err != nil {
		t.Fatalf("print: %v", err)
	}
	fc.mu.Lock()
	sess := fc.sessions[len(fc.sessions)-1]
	fc.mu.Unlock()
	if len(sess.pageSizes) != 1 {
		t.Fatalf("expected one page, got %d", len(sess.pageSizes))
	}
	// 50x30 mm at 203 dpi: 400 -> clamped 384 cols, 240 rows, 1 copy.
	if want := []byte{0, 240, 1, 0x80, 0, 1}; !bytes.Equal(sess.pageSizes[0], want) {
		t.Errorf("page size %x want %x", sess.pageSizes[0], want)
	}
	if !sess.closed {
		t.Error("print session should be closed")
	}
	if sess.rows != 240 {
		t.Errorf("expected 240 rows streamed, got %d", sess.rows)
	}
	if st, _ := b.Status(ctx, id); st != StatusReady {
		t.Errorf("status after print %v", st)
	}

	// Cover open: heartbeat says lid open -> ErrNotPrintable{CoverOpen}, no job started.
	fc.mu.Lock()
	fc.lidOpen = true
	fc.mu.Unlock()
	err = b.Print(ctx, id, testPNG(t, 100, 60), PrintOptions{WidthMM: 50, HeightMM: 30})
	var np ErrNotPrintable
	if !errors.As(err, &np) || np.Status != StatusCoverOpen {
		t.Fatalf("expected cover-open not-printable, got %v", err)
	}
	if st, _ := b.Status(ctx, id); st != StatusCoverOpen {
		t.Errorf("status should reflect cover open, got %v", st)
	}
}

func TestNiimbotStartRequiresATransport(t *testing.T) {
	b := NewNiimbotBackend(NiimbotOptions{DisableBLE: true, Connector: &fakeConnector{}})
	if _, err := b.Start(context.Background()); err == nil {
		t.Error("expected error with BLE disabled and no serial ports")
	}
}

func TestMultiDriverRoutesByOwner(t *testing.T) {
	stub := NewStubBackend(nil)
	fc := &fakeConnector{advertising: true}
	nb := newTestBackend(fc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, _ := nb.Start(ctx)
	collect(events, 1, time.Second)

	md := NewMultiDriver(stub, nb)
	if st, err := md.Status(ctx, "SN-STUB-0001"); err != nil || st != StatusReady {
		t.Errorf("stub route: %v %v", st, err)
	}
	if st, err := md.Status(ctx, "niimbot-I711131967"); err != nil || st != StatusReady {
		t.Errorf("niimbot route: %v %v", st, err)
	}
	if _, err := md.Status(ctx, "niimbot-unknown"); !errors.As(err, new(ErrUnknownPrinter)) {
		t.Errorf("unknown niimbot id should be unknown, got %v", err)
	}
	mdisc := NewMultiDiscoverer(stub, nb)
	if n := len(mdisc.List()); n != 2 {
		t.Errorf("multi list = %d", n)
	}
}
