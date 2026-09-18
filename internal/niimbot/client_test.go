package niimbot

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeTransport scripts printer replies: for each request command it returns
// the listed packets (as raw wire bytes, possibly several per write). One-way
// row packets are recorded but produce no reply.
type fakeTransport struct {
	mu      sync.Mutex
	replies map[byte][]Packet
	sent    []Packet
	rx      chan []byte
	// dropFirst drops the first write (simulates the lost first packet on a
	// fresh Bluetooth link).
	dropFirst bool
	dropped   bool
}

func newFake() *fakeTransport {
	return &fakeTransport{replies: map[byte][]Packet{}, rx: make(chan []byte, 64)}
}

func (f *fakeTransport) reply(cmd byte, ps ...Packet) { f.replies[cmd] = ps }

func (f *fakeTransport) Write(ctx context.Context, raw []byte) error {
	var ps Parser
	pk := ps.Feed(raw)
	if len(pk) != 1 {
		return errors.New("fake: bad frame")
	}
	f.mu.Lock()
	f.sent = append(f.sent, pk[0])
	if f.dropFirst && !f.dropped {
		f.dropped = true
		f.mu.Unlock()
		return nil
	}
	rs := f.replies[pk[0].Cmd]
	f.mu.Unlock()
	for _, r := range rs {
		b, _ := r.Marshal()
		f.rx <- b
	}
	return nil
}
func (f *fakeTransport) Receive() <-chan []byte { return f.rx }
func (f *fakeTransport) Close() error           { close(f.rx); return nil }
func (f *fakeTransport) Kind() string           { return "fake" }

func (f *fakeTransport) cmds() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]byte, len(f.sent))
	for i, p := range f.sent {
		out[i] = p.Cmd
	}
	return out
}

func quiet(t *testing.T, tr Transport) *Client {
	c := NewClient(tr, nil)
	c.Timeout = 200 * time.Millisecond
	c.PageTimeout = 300 * time.Millisecond
	c.WriteInterval = 0
	c.StatusPoll = 5 * time.Millisecond
	t.Cleanup(func() { c.Close() })
	return c
}

func TestHeartbeatRetriesDroppedFirstPacket(t *testing.T) {
	f := newFake()
	f.dropFirst = true
	// Captured B1 heartbeat: 13-byte Advanced1.
	f.reply(CmdHeartbeat, Packet{Cmd: RespHeartbeatAdv1, Data: mustHex(t, "21080061006100004d00040001")})
	c := quiet(t, f)
	hb, err := c.Heartbeat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hb.LidClosed == nil || !*hb.LidClosed || hb.BatteryLevel == nil || *hb.BatteryLevel != 4 ||
		hb.PaperInserted == nil || !*hb.PaperInserted || hb.RFIDReadOK == nil || !*hb.RFIDReadOK {
		t.Errorf("heartbeat parse: %+v", hb)
	}
	if n := len(f.cmds()); n != 2 {
		t.Errorf("expected 2 heartbeat sends (retry), got %d", n)
	}
}

func TestIdentifyAndInfo(t *testing.T) {
	f := newFake()
	f.reply(CmdPrinterInfo) // filled per key below via a smarter map
	f.replies = map[byte][]Packet{}
	// Info replies depend on the key; emulate by pre-loading a queue keyed on
	// the request command and rotating.
	f.reply(CmdPrinterInfo, Packet{Cmd: 0x48, Data: []byte{0x10, 0x00}})
	c := quiet(t, f)
	m, err := c.Identify(context.Background())
	if err != nil || m.Name != "B1" {
		t.Fatalf("identify: %+v %v", m, err)
	}
	f.reply(CmdPrinterInfo, Packet{Cmd: 0x4B, Data: []byte("I711131967")})
	if s, err := c.Serial(context.Background()); err != nil || s != "I711131967" {
		t.Errorf("serial: %q %v", s, err)
	}
	f.reply(CmdPrinterInfo, Packet{Cmd: 0x49, Data: []byte{0x06, 0x13}})
	if v, err := c.Version(context.Background(), InfoSoftVersion); err != nil || v != "6.19" {
		t.Errorf("version: %q %v", v, err)
	}
	f.reply(CmdPrinterInfo, Packet{Cmd: RespNotSupported, Data: []byte{1}})
	if _, err := c.InfoInt(context.Background(), InfoPrintSpeed); !errors.Is(err, ErrNotSupported) {
		t.Errorf("expected ErrNotSupported, got %v", err)
	}
}

func TestRFIDParsesCapturedTag(t *testing.T) {
	f := newFake()
	f.reply(CmdRfidInfo, Packet{Cmd: RespRfidInfo, Data: mustHex(t,
		"881d8815e81e108008313032363232363010505a3149373130333030303033353838011400000100e6881d8815e81e1080")})
	c := quiet(t, f)
	r, err := c.RFID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !r.Present || r.UUID != "881d8815e81e1080" || r.Barcode != "10262260" || r.Serial != "PZ1I710300003588" ||
		r.TotalPaper != 276 || r.UsedPaper != 0 || r.Type != LabelGap {
		t.Errorf("rfid: %+v", r)
	}
	f.reply(CmdRfidInfo, Packet{Cmd: RespRfidInfo, Data: []byte{0}})
	if r, err := c.RFID(context.Background()); err != nil || r.Present {
		t.Errorf("no tag: %+v %v", r, err)
	}
}

func TestPrintB1FlowMatchesCapture(t *testing.T) {
	f := newFake()
	f.reply(CmdSetDensity, Packet{Cmd: RespSetDensity, Data: []byte{1}})
	f.reply(CmdSetLabelType, Packet{Cmd: RespSetLabelType, Data: []byte{1}})
	f.reply(CmdPrintStart, Packet{Cmd: RespPrintStart, Data: []byte{1}})
	f.reply(CmdPageStart, Packet{Cmd: RespPageStart, Data: []byte{1}})
	f.reply(CmdSetPageSize, Packet{Cmd: RespSetPageSize, Data: []byte{1, 0}})
	// The B1 sends check-line packets before the PageEnd ack.
	f.reply(CmdPageEnd, Packet{Cmd: RespCheckLine, Data: []byte{0, 0xC7, 1}}, Packet{Cmd: RespPageEnd, Data: []byte{1}})
	f.reply(CmdPrintEnd, Packet{Cmd: RespPrintEnd, Data: []byte{1}})
	statusSeq := []Packet{
		{Cmd: RespPrintStatus, Data: mustHex(t, "00000000025100010000")},
		{Cmd: RespPrintStatus, Data: mustHex(t, "00016464031f00010000")},
	}
	calls := 0
	// Rotate status replies per call.
	f.reply(CmdPrintStatus, statusSeq[0])
	c := quiet(t, f)
	c.SetModel(DefaultModel)
	enc := Encode(square(384, 4, 0, 1, 16, 3), EncodeOptions{})
	err := c.Print(context.Background(), enc, PrintOptions{Copies: 1, Progress: func(st PrintStatus) {
		calls++
		if calls == 1 {
			f.mu.Lock()
			f.replies[CmdPrintStatus] = []Packet{statusSeq[1]}
			f.mu.Unlock()
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{CmdSetDensity, CmdSetLabelType, CmdPrintStart, CmdPageStart, CmdSetPageSize,
		CmdEmptyRow, CmdBitmapRow, CmdEmptyRow, CmdPageEnd, CmdPrintStatus, CmdPrintStatus, CmdPrintEnd}
	got := f.cmds()
	if string(got) != string(want) {
		t.Errorf("command sequence\n got %x\nwant %x", got, want)
	}
	// PrintStart carries total pages; SetPageSize carries rows, cols, copies.
	f.mu.Lock()
	ps := f.sent[2].Data
	sz := f.sent[4].Data
	f.mu.Unlock()
	if string(ps) != string([]byte{0, 1, 0, 0, 0, 0, 0}) {
		t.Errorf("print start data %x", ps)
	}
	if string(sz) != string([]byte{0, 4, 1, 0x80, 0, 1}) {
		t.Errorf("set page size data %x", sz)
	}
}

func TestPrintErrorResetsPrinter(t *testing.T) {
	f := newFake()
	f.reply(CmdSetDensity, Packet{Cmd: RespSetDensity, Data: []byte{1}})
	f.reply(CmdSetLabelType, Packet{Cmd: RespSetLabelType, Data: []byte{1}})
	f.reply(CmdPrintStart, Packet{Cmd: RespPrintError, Data: []byte{byte(ErrCoverOpen)}})
	f.reply(CmdPrintEnd, Packet{Cmd: RespPrintEnd, Data: []byte{1}})
	f.reply(CmdCancelPrint, Packet{Cmd: RespCancelPrint, Data: []byte{1}})
	c := quiet(t, f)
	err := c.Print(context.Background(), Encode(square(8, 1, 0, 0, 8, 1), EncodeOptions{}), PrintOptions{})
	var perr *PrintError
	if !errors.As(err, &perr) || perr.Code != ErrCoverOpen {
		t.Fatalf("expected cover-open PrintError, got %v", err)
	}
	got := f.cmds()
	if len(got) < 5 || got[len(got)-2] != CmdPrintEnd || got[len(got)-1] != CmdCancelPrint {
		t.Errorf("expected reset (PrintEnd, CancelPrint) after error, got %x", got)
	}
}

func TestPrintB21V1Flow(t *testing.T) {
	f := newFake()
	f.reply(CmdSetDensity, Packet{Cmd: RespSetDensity, Data: []byte{1}})
	f.reply(CmdSetLabelType, Packet{Cmd: RespSetLabelType, Data: []byte{1}})
	f.reply(CmdPrintStart, Packet{Cmd: RespPrintStart, Data: []byte{1}})
	f.reply(CmdPageStart, Packet{Cmd: RespPageStart, Data: []byte{1}})
	f.reply(CmdSetPageSize, Packet{Cmd: RespSetPageSize, Data: []byte{1}})
	f.reply(CmdPageEnd, Packet{Cmd: RespPageEnd, Data: []byte{1}})
	f.reply(CmdPrintEnd, Packet{Cmd: RespPrintEnd, Data: []byte{0}})
	c := quiet(t, f)
	m, _ := ModelByName("B21")
	c.SetModel(m)
	done := make(chan error, 1)
	go func() {
		done <- c.Print(context.Background(), Encode(square(8, 2, 0, 0, 8, 2), EncodeOptions{}), PrintOptions{Copies: 2})
	}()
	// Let it poll PrintEnd a few times, then report finished.
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	f.replies[CmdPrintEnd] = []Packet{{Cmd: RespPrintEnd, Data: []byte{1}}}
	f.mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := f.cmds()
	// Init (3), then per copy: PageStart, SetPageSize, row, PageEnd (x2), then >=2 PrintEnd polls.
	if len(got) < 3+8+2 || got[3] != CmdPageStart || got[7] != CmdPageStart || got[6] != CmdPageEnd {
		t.Errorf("sequence %x", got)
	}
	f.mu.Lock()
	ps, sz := f.sent[2].Data, f.sent[4].Data
	f.mu.Unlock()
	if len(ps) != 1 || len(sz) != 4 {
		t.Errorf("B21 V1 payload sizes: start=%x size=%x", ps, sz)
	}
}

func TestRequestTimeoutAndUnsolicitedSkipped(t *testing.T) {
	f := newFake()
	f.reply(CmdPrintStatus, Packet{Cmd: RespCheckLine, Data: []byte{0, 1, 1}})
	c := quiet(t, f)
	_, err := c.PrintStatus(context.Background())
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected timeout, got %v", err)
	}
	f.reply(CmdPrintStatus, Packet{Cmd: RespPrintStatus, Data: mustHex(t, "0001646400000a")})
	st, err := c.PrintStatus(context.Background())
	if err != nil || st.Page != 1 || st.Error != ErrorCode(0x0a) {
		t.Errorf("status %+v %v", st, err)
	}
}
