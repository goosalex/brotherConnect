package niimbot

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Client drives one printer over a Transport. Requests are serialised; only one
// exchange is in flight at a time.
type Client struct {
	tr  Transport
	log *slog.Logger

	// Timeout bounds a single request/response exchange. Responses normally
	// arrive within ~150 ms over BLE or SPP.
	Timeout time.Duration
	// PageTimeout bounds the PageEnd acknowledgement, which the printer only
	// sends once it has consumed the row data.
	PageTimeout time.Duration
	// WriteInterval paces consecutive writes; the firmware drops packets sent
	// back-to-back over Bluetooth. niimbluelib uses 10 ms.
	WriteInterval time.Duration
	// StatusPoll is the interval between PrintStatus polls while printing.
	StatusPoll time.Duration

	mu      sync.Mutex
	packets chan Packet
	done    chan struct{}
	lastTx  time.Time
	model   Model
	haveMdl bool
}

// Default timings.
const (
	DefaultTimeout       = 1500 * time.Millisecond
	DefaultPageTimeout   = 10 * time.Second
	DefaultWriteInterval = 10 * time.Millisecond
	DefaultStatusPoll    = 300 * time.Millisecond
)

// NewClient wraps tr. It starts a goroutine that parses inbound bytes into
// packets until the transport closes.
func NewClient(tr Transport, log *slog.Logger) *Client {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	c := &Client{
		tr:            tr,
		log:           log,
		Timeout:       DefaultTimeout,
		PageTimeout:   DefaultPageTimeout,
		WriteInterval: DefaultWriteInterval,
		StatusPoll:    DefaultStatusPoll,
		packets:       make(chan Packet, 64),
		done:          make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// Close closes the transport.
func (c *Client) Close() error { return c.tr.Close() }

// Transport returns the underlying transport.
func (c *Client) Transport() Transport { return c.tr }

func (c *Client) readLoop() {
	defer close(c.done)
	var ps Parser
	for chunk := range c.tr.Receive() {
		for _, p := range ps.Feed(chunk) {
			c.log.Debug("niimbot rx", "packet", p.String())
			select {
			case c.packets <- p:
			default:
				// Consumer is behind; drop the oldest to keep the newest.
				select {
				case <-c.packets:
				default:
				}
				c.packets <- p
			}
		}
	}
}

// ErrTimeout is returned when the printer does not answer in time.
var ErrTimeout = errors.New("niimbot: response timeout")

// ErrNotSupported is returned when the printer answers 0x00 (command not
// supported by this firmware).
var ErrNotSupported = errors.New("niimbot: command not supported by printer")

// send writes one packet, honouring WriteInterval pacing.
func (c *Client) send(ctx context.Context, p Packet) error {
	raw, err := p.Marshal()
	if err != nil {
		return err
	}
	if wait := c.WriteInterval - time.Since(c.lastTx); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.log.Debug("niimbot tx", "packet", p.String())
	err = c.tr.Write(ctx, raw)
	c.lastTx = time.Now()
	return err
}

// drain discards packets that arrived before a request was sent, so a late
// reply to an earlier (timed-out) request is not mistaken for this one.
func (c *Client) drain() {
	for {
		select {
		case p := <-c.packets:
			c.log.Debug("niimbot rx (stale, dropped)", "packet", p.String())
		default:
			return
		}
	}
}

// await waits up to timeout for a packet whose Cmd is in accept. Error
// replies (RespPrintError, RespNotSupported) are returned as errors. Other
// packets (unsolicited status such as RespCheckLine) are logged and skipped.
func (c *Client) await(ctx context.Context, timeout time.Duration, accept ...byte) (Packet, error) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	for {
		select {
		case p, ok := <-c.packets:
			if !ok {
				return Packet{}, ErrLinkClosed
			}
			switch {
			case p.Cmd == RespPrintError:
				code := ErrorCode(0)
				if len(p.Data) > 0 {
					code = ErrorCode(p.Data[0])
				}
				return p, &PrintError{Code: code}
			case p.Cmd == RespNotSupported:
				return p, ErrNotSupported
			case contains(accept, p.Cmd):
				return p, nil
			default:
				c.log.Debug("niimbot rx (unsolicited)", "packet", p.String())
			}
		case <-t.C:
			return Packet{}, ErrTimeout
		case <-ctx.Done():
			return Packet{}, ctx.Err()
		case <-c.done:
			return Packet{}, ErrLinkClosed
		}
	}
}

func contains(s []byte, b byte) bool {
	for _, x := range s {
		if x == b {
			return true
		}
	}
	return false
}

// Request sends cmd with data and waits for a reply whose command is one of
// accept (default: cmd+1, the usual convention).
func (c *Client) Request(ctx context.Context, cmd byte, data []byte, accept ...byte) (Packet, error) {
	return c.request(ctx, c.Timeout, cmd, data, accept...)
}

func (c *Client) request(ctx context.Context, timeout time.Duration, cmd byte, data []byte, accept ...byte) (Packet, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(accept) == 0 {
		accept = []byte{cmd + 1}
	}
	c.drain()
	if err := c.send(ctx, Packet{Cmd: cmd, Data: data}); err != nil {
		return Packet{}, fmt.Errorf("send %#02x: %w", cmd, err)
	}
	p, err := c.await(ctx, timeout, accept...)
	if err != nil {
		return p, fmt.Errorf("cmd %#02x: %w", cmd, err)
	}
	return p, nil
}

// requestRetry repeats a request on timeout. The first packet after a link
// comes up is frequently lost, so probes use this.
func (c *Client) requestRetry(ctx context.Context, attempts int, cmd byte, data []byte, accept ...byte) (Packet, error) {
	var (
		p   Packet
		err error
	)
	for i := 0; i < attempts; i++ {
		p, err = c.Request(ctx, cmd, data, accept...)
		if err == nil || !errors.Is(err, ErrTimeout) {
			return p, err
		}
	}
	return p, err
}

// Heartbeat is the printer's periodic status report.
type Heartbeat struct {
	// Raw is the response command (RespHeartbeatAdv1/Adv2/...) for diagnostics.
	Raw Packet
	// Known fields; a nil pointer means the firmware's variant does not carry it.
	LidClosed     *bool
	BatteryLevel  *int // 0..4 on B1, or percent on newer firmware
	PaperInserted *bool
	RFIDReadOK    *bool
}

// Heartbeat queries the printer status (CmdHeartbeat). It retries once because
// the first packet on a fresh link is often dropped.
func (c *Client) Heartbeat(ctx context.Context) (Heartbeat, error) {
	p, err := c.requestRetry(ctx, 3, CmdHeartbeat, []byte{1},
		RespHeartbeatAdv1, RespHeartbeatAdv2, RespHeartbeatBase, RespHeartbeatInfo)
	if err != nil {
		return Heartbeat{}, err
	}
	return parseHeartbeat(p), nil
}

func parseHeartbeat(p Packet) Heartbeat {
	hb := Heartbeat{Raw: p}
	d := p.Data
	bp := func(v bool) *bool { return &v }
	ip := func(v byte) *int { i := int(v); return &i }
	switch p.Cmd {
	case RespHeartbeatAdv2:
		if len(d) >= 7 {
			hb.BatteryLevel = ip(d[2])
			hb.LidClosed = bp(d[4] == 0)
			hb.PaperInserted = bp(d[5] == 0)
			hb.RFIDReadOK = bp(d[6] != 0)
		}
	case RespHeartbeatAdv1:
		switch len(d) {
		case 10:
			hb.LidClosed = bp(d[8] == 0)
			hb.BatteryLevel = ip(d[9])
		case 13: // B1
			hb.LidClosed = bp(d[9] == 0)
			hb.BatteryLevel = ip(d[10])
			hb.PaperInserted = bp(d[11] == 0)
			hb.RFIDReadOK = bp(d[12] != 0)
		case 19:
			hb.LidClosed = bp(d[15] == 0)
			hb.BatteryLevel = ip(d[16])
			hb.PaperInserted = bp(d[17] == 0)
			hb.RFIDReadOK = bp(d[18] != 0)
		case 20:
			hb.PaperInserted = bp(d[18] == 0)
			hb.RFIDReadOK = bp(d[19] != 0)
		}
	}
	return hb
}

// Info reads one printer-info field as raw bytes.
func (c *Client) Info(ctx context.Context, key InfoKey) ([]byte, error) {
	p, err := c.requestRetry(ctx, 2, CmdPrinterInfo, []byte{byte(key)}, CmdPrinterInfo+byte(key))
	if err != nil {
		return nil, err
	}
	return p.Data, nil
}

// InfoInt reads an integer info field (big-endian).
func (c *Client) InfoInt(ctx context.Context, key InfoKey) (int, error) {
	d, err := c.Info(ctx, key)
	if err != nil {
		return 0, err
	}
	v := 0
	for _, b := range d {
		v = v<<8 | int(b)
	}
	return v, nil
}

// DeviceType returns the numeric model identifier (InfoDeviceType).
func (c *Client) DeviceType(ctx context.Context) (int, error) { return c.InfoInt(ctx, InfoDeviceType) }

// Serial returns the printer serial number. The B1 returns it as ASCII
// ("I711131967"); firmware that returns raw bytes is rendered as hex.
func (c *Client) Serial(ctx context.Context) (string, error) {
	d, err := c.Info(ctx, InfoSerial)
	if err != nil {
		return "", err
	}
	printable := len(d) > 0
	for _, b := range d {
		if b < 0x20 || b > 0x7E {
			printable = false
			break
		}
	}
	if printable {
		return strings.TrimSpace(string(d)), nil
	}
	return fmt.Sprintf("%x", d), nil
}

// Version renders a 2-byte version field ("major.minor") as reported by the
// printer, e.g. InfoSoftVersion 0x06 0x13 -> "6.19".
func (c *Client) Version(ctx context.Context, key InfoKey) (string, error) {
	d, err := c.Info(ctx, key)
	if err != nil {
		return "", err
	}
	if len(d) >= 2 {
		return fmt.Sprintf("%d.%d", d[0], d[1]), nil
	}
	return fmt.Sprintf("%x", d), nil
}

// Identify reads the device type and resolves the model, caching it for
// subsequent prints. Unknown device types fall back to DefaultModel.
func (c *Client) Identify(ctx context.Context) (Model, error) {
	dt, err := c.DeviceType(ctx)
	if err != nil {
		return Model{}, err
	}
	m, ok := ModelByDeviceType(dt)
	if !ok {
		m = DefaultModel
		m.Name = fmt.Sprintf("NIIMBOT type %d", dt)
		m.Verified = false
		c.log.Warn("unknown NIIMBOT device type; assuming B1 protocol", "device_type", dt)
	}
	c.SetModel(m)
	return m, nil
}

// SetModel fixes the model used for printing (instead of Identify).
func (c *Client) SetModel(m Model) { c.model, c.haveMdl = m, true }

// Model returns the model set by Identify/SetModel, or DefaultModel.
func (c *Client) Model() Model {
	if c.haveMdl {
		return c.model
	}
	return DefaultModel
}

// RFID describes the label roll's RFID tag, when present.
type RFID struct {
	Present    bool
	UUID       string
	Barcode    string
	Serial     string
	TotalPaper int // roll capacity as written on the tag (labels or mm)
	UsedPaper  int
	Type       LabelType
}

// RFID reads the loaded roll's tag. Present is false when no tag was read.
func (c *Client) RFID(ctx context.Context) (RFID, error) {
	p, err := c.Request(ctx, CmdRfidInfo, []byte{1}, RespRfidInfo)
	if err != nil {
		return RFID{}, err
	}
	d := p.Data
	if len(d) <= 1 {
		return RFID{}, nil
	}
	r := RFID{Present: true}
	i := 0
	take := func(n int) []byte {
		if i+n > len(d) {
			n = len(d) - i
		}
		s := d[i : i+n]
		i += n
		return s
	}
	r.UUID = fmt.Sprintf("%x", take(8))
	if i < len(d) {
		n := int(d[i])
		i++
		r.Barcode = string(take(n))
	}
	if i < len(d) {
		n := int(d[i])
		i++
		r.Serial = string(take(n))
	}
	if i+2 <= len(d) {
		r.TotalPaper = int(binary.BigEndian.Uint16(take(2)))
	}
	if i+2 <= len(d) {
		r.UsedPaper = int(binary.BigEndian.Uint16(take(2)))
	}
	if i < len(d) {
		r.Type = LabelType(take(1)[0])
	}
	return r, nil
}

// PrintStatus is the printer's progress report while a job runs.
type PrintStatus struct {
	Page          int // pages completed so far
	PrintProgress int // percent
	FeedProgress  int // percent
	Error         ErrorCode
}

// PrintStatus polls the current job (CmdPrintStatus).
func (c *Client) PrintStatus(ctx context.Context) (PrintStatus, error) {
	p, err := c.Request(ctx, CmdPrintStatus, []byte{1}, RespPrintStatus)
	if err != nil {
		return PrintStatus{}, err
	}
	d := p.Data
	if len(d) < 4 {
		return PrintStatus{}, fmt.Errorf("niimbot: short print status (%d bytes)", len(d))
	}
	st := PrintStatus{Page: int(binary.BigEndian.Uint16(d)), PrintProgress: int(d[2]), FeedProgress: int(d[3])}
	if len(d) >= 7 {
		st.Error = ErrorCode(d[6])
	}
	return st, nil
}

// PrintEnd ends the job (CmdPrintEnd). It reports the printer's boolean reply.
func (c *Client) PrintEnd(ctx context.Context) (bool, error) {
	p, err := c.Request(ctx, CmdPrintEnd, []byte{1}, RespPrintEnd)
	if err != nil {
		return false, err
	}
	return len(p.Data) > 0 && p.Data[0] != 0, nil
}

// CancelPrint aborts the current job.
func (c *Client) CancelPrint(ctx context.Context) error {
	_, err := c.Request(ctx, CmdCancelPrint, []byte{1}, RespCancelPrint)
	return err
}

// PrintOptions configures a print job.
type PrintOptions struct {
	// Density 1..5 (model-dependent). 0 selects the model default.
	Density int
	// LabelType; 0 selects LabelGap.
	LabelType LabelType
	// Copies >= 1.
	Copies int
	// Progress, if set, is called after each status poll.
	Progress func(PrintStatus)
}

// Print sends img (already scaled/oriented to the label; see Fit and Encode)
// as one label, Copies times, using the model's task sequence. On failure it
// attempts to reset the printer with PrintEnd/CancelPrint so the next job can
// start cleanly.
func (c *Client) Print(ctx context.Context, enc EncodedImage, opts PrintOptions) (err error) {
	m := c.Model()
	if enc.Cols > m.HeadPixels {
		return fmt.Errorf("niimbot: image is %d px wide, print head is %d px", enc.Cols, m.HeadPixels)
	}
	if enc.Rows < 1 || enc.Rows > 0xFFFF {
		return fmt.Errorf("niimbot: invalid row count %d", enc.Rows)
	}
	if opts.Copies < 1 {
		opts.Copies = 1
	}
	if opts.Density == 0 {
		opts.Density = m.DensityDefault
	}
	opts.Density = max(m.DensityMin, min(m.DensityMax, opts.Density))
	if opts.LabelType == 0 {
		opts.LabelType = LabelGap
	}

	defer func() {
		if err != nil {
			// Best-effort reset so the printer is not left mid-job.
			rctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _ = c.PrintEnd(rctx)
			_ = c.CancelPrint(rctx)
		}
	}()

	switch m.Task {
	case TaskB21V1:
		return c.printB21V1(ctx, enc, opts, m)
	case TaskD110:
		return c.printD110(ctx, enc, opts, m)
	default:
		return c.printB1(ctx, enc, opts, m)
	}
}

func (c *Client) printInit(ctx context.Context, opts PrintOptions, start []byte) error {
	if _, err := c.Request(ctx, CmdSetDensity, []byte{byte(opts.Density)}, RespSetDensity); err != nil {
		return fmt.Errorf("set density: %w", err)
	}
	if _, err := c.Request(ctx, CmdSetLabelType, []byte{byte(opts.LabelType)}, RespSetLabelType); err != nil {
		return fmt.Errorf("set label type: %w", err)
	}
	if _, err := c.Request(ctx, CmdPrintStart, start, RespPrintStart); err != nil {
		return fmt.Errorf("print start: %w", err)
	}
	return nil
}

// sendRows streams the one-way image packets, then PageEnd, and waits for the
// PageEnd acknowledgement (RespPageEnd). Some firmware sends RespCheckLine
// and RespResetTimeout first; await skips those.
func (c *Client) sendRows(ctx context.Context, rows []Packet) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drain()
	for i, p := range rows {
		if err := c.send(ctx, p); err != nil {
			return fmt.Errorf("send row packet %d: %w", i, err)
		}
	}
	if err := c.send(ctx, Packet{Cmd: CmdPageEnd, Data: []byte{1}}); err != nil {
		return fmt.Errorf("page end: %w", err)
	}
	if _, err := c.await(ctx, c.PageTimeout, RespPageEnd); err != nil {
		return fmt.Errorf("page end: %w", err)
	}
	return nil
}

// waitByStatus polls PrintStatus until pages completed >= want.
func (c *Client) waitByStatus(ctx context.Context, want int, progress func(PrintStatus)) error {
	for {
		st, err := c.PrintStatus(ctx)
		if err != nil {
			return err
		}
		if progress != nil {
			progress(st)
		}
		if st.Error != 0 {
			return &PrintError{Code: st.Error}
		}
		if st.Page >= want {
			return nil
		}
		select {
		case <-time.After(c.StatusPoll):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func u16(v int) []byte { return binary.BigEndian.AppendUint16(nil, uint16(v)) }

// printB1 is the B1 task: PrintStart(7b) once, one page with the copy count in
// SetPageSize, completion by PrintStatus polling.
func (c *Client) printB1(ctx context.Context, enc EncodedImage, opts PrintOptions, m Model) error {
	start := append(u16(opts.Copies), 0, 0, 0, 0, 0) // total pages, reserved, page colour
	if err := c.printInit(ctx, opts, start); err != nil {
		return err
	}
	if _, err := c.Request(ctx, CmdPageStart, []byte{1}, RespPageStart); err != nil {
		return fmt.Errorf("page start: %w", err)
	}
	size := append(append(u16(enc.Rows), u16(enc.Cols)...), u16(opts.Copies)...)
	if _, err := c.Request(ctx, CmdSetPageSize, size, RespSetPageSize); err != nil {
		return fmt.Errorf("set page size: %w", err)
	}
	if err := c.sendRows(ctx, enc.RowPackets(m.HeadPixels, CountSplit, false)); err != nil {
		return err
	}
	if err := c.waitByStatus(ctx, opts.Copies, opts.Progress); err != nil {
		return fmt.Errorf("print status: %w", err)
	}
	if _, err := c.PrintEnd(ctx); err != nil {
		return fmt.Errorf("print end: %w", err)
	}
	return nil
}

// printD110 is the D110 task (B21S): PrintStart(1b), then per page PrintClear,
// PageStart, SetPageSize(4b), PrintQuantity, rows; completion by PrintStatus.
func (c *Client) printD110(ctx context.Context, enc EncodedImage, opts PrintOptions, m Model) error {
	if err := c.printInit(ctx, opts, []byte{1}); err != nil {
		return err
	}
	if _, err := c.Request(ctx, CmdPrintClear, []byte{1}, RespPrintClear); err != nil {
		return fmt.Errorf("print clear: %w", err)
	}
	if _, err := c.Request(ctx, CmdPageStart, []byte{1}, RespPageStart); err != nil {
		return fmt.Errorf("page start: %w", err)
	}
	if _, err := c.Request(ctx, CmdSetPageSize, append(u16(enc.Rows), u16(enc.Cols)...), RespSetPageSize); err != nil {
		return fmt.Errorf("set page size: %w", err)
	}
	if _, err := c.Request(ctx, CmdPrintQuantity, u16(opts.Copies), RespPrintQuantity); err != nil {
		return fmt.Errorf("print quantity: %w", err)
	}
	if err := c.sendRows(ctx, enc.RowPackets(m.HeadPixels, CountSplit, false)); err != nil {
		return err
	}
	if err := c.waitByStatus(ctx, opts.Copies, opts.Progress); err != nil {
		return fmt.Errorf("print status: %w", err)
	}
	if _, err := c.PrintEnd(ctx); err != nil {
		return fmt.Errorf("print end: %w", err)
	}
	return nil
}

// printB21V1 is the legacy B21 task: PrintStart(1b), one page per copy with
// SetPageSize(4b), total-count row headers and check lines, completion by
// polling PrintEnd until it answers true.
func (c *Client) printB21V1(ctx context.Context, enc EncodedImage, opts PrintOptions, m Model) error {
	if err := c.printInit(ctx, opts, []byte{1}); err != nil {
		return err
	}
	rows := enc.RowPackets(m.HeadPixels, CountTotal, true)
	for i := 0; i < opts.Copies; i++ {
		if _, err := c.Request(ctx, CmdPageStart, []byte{1}, RespPageStart); err != nil {
			return fmt.Errorf("page start: %w", err)
		}
		if _, err := c.Request(ctx, CmdSetPageSize, append(u16(enc.Rows), u16(enc.Cols)...), RespSetPageSize); err != nil {
			return fmt.Errorf("set page size: %w", err)
		}
		if err := c.sendRows(ctx, rows); err != nil {
			return err
		}
	}
	for {
		done, err := c.PrintEnd(ctx)
		if err != nil {
			return fmt.Errorf("print end: %w", err)
		}
		if done {
			return nil
		}
		select {
		case <-time.After(c.StatusPoll):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
