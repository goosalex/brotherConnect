// Package niimbot implements the NIIMBOT label-printer protocol (B1, B21 and
// related models) over Bluetooth Low Energy and serial transports.
//
// The protocol is not published by NIIMBOT; this implementation follows the
// community reverse-engineering (niimbluelib, niimprint, niimgo, and the
// printers.niim.blue wiki) and was verified against a B1 on this project. See
// docs/niimbot.md for the specification and the field findings.
package niimbot

import (
	"encoding/hex"
	"errors"
	"fmt"
)

// Packet is one protocol frame: a command byte and a data payload.
//
// Wire format: 0x55 0x55, cmd, len(data), data..., checksum, 0xAA 0xAA, where
// checksum is the XOR of cmd, len and every data byte.
type Packet struct {
	Cmd  byte
	Data []byte
}

// Framing constants.
const (
	headByte = 0x55
	tailByte = 0xAA
	// packetOverhead is the number of framing bytes around the data: 2 head,
	// cmd, len, checksum, 2 tail.
	packetOverhead = 7
	// MaxData is the largest data payload a one-byte length field can carry.
	MaxData = 255
)

// ErrPacketTooLong is returned when a packet's data exceeds MaxData bytes.
var ErrPacketTooLong = errors.New("niimbot: packet data exceeds 255 bytes")

// checksum computes the XOR checksum over cmd, len and data.
func checksum(cmd byte, data []byte) byte {
	cs := cmd ^ byte(len(data))
	for _, b := range data {
		cs ^= b
	}
	return cs
}

// Marshal encodes the packet into its wire form.
func (p Packet) Marshal() ([]byte, error) {
	if len(p.Data) > MaxData {
		return nil, ErrPacketTooLong
	}
	buf := make([]byte, 0, len(p.Data)+packetOverhead)
	buf = append(buf, headByte, headByte, p.Cmd, byte(len(p.Data)))
	buf = append(buf, p.Data...)
	buf = append(buf, checksum(p.Cmd, p.Data), tailByte, tailByte)
	return buf, nil
}

// String renders the packet as hex for logs.
func (p Packet) String() string {
	return fmt.Sprintf("cmd=%#02x data=%s", p.Cmd, hex.EncodeToString(p.Data))
}

// Parser reassembles packets from a byte stream. Transports deliver bytes in
// arbitrary chunks (a BLE notification may carry part of a packet or several
// packets), so the parser buffers until a complete, valid frame is present and
// resynchronises on the next 0x55 0x55 after corrupt input.
type Parser struct {
	buf []byte
}

// Feed appends b to the parser and returns every complete packet now
// available, in order. Invalid frames are skipped.
func (ps *Parser) Feed(b []byte) []Packet {
	ps.buf = append(ps.buf, b...)
	var out []Packet
	for {
		p, n, ok := ps.next()
		if n > 0 {
			ps.buf = ps.buf[n:]
		}
		if !ok {
			if n == 0 {
				return out // need more bytes
			}
			continue // skipped garbage; try again
		}
		out = append(out, p)
	}
}

// next tries to parse one packet from the front of the buffer. It returns the
// packet, the number of bytes to consume, and whether a packet was produced.
// n == 0 && !ok means "wait for more data".
func (ps *Parser) next() (Packet, int, bool) {
	buf := ps.buf
	// Find the head.
	start := -1
	for i := 0; i+1 < len(buf); i++ {
		if buf[i] == headByte && buf[i+1] == headByte {
			start = i
			break
		}
	}
	if start < 0 {
		// Keep a possible trailing 0x55 for the next feed; drop the rest.
		if n := len(buf); n > 0 && buf[n-1] == headByte {
			return Packet{}, n - 1, false
		}
		return Packet{}, len(buf), false
	}
	if start > 0 {
		return Packet{}, start, false
	}
	if len(buf) < 4 {
		return Packet{}, 0, false
	}
	cmd, n := buf[2], int(buf[3])
	total := n + packetOverhead
	if len(buf) < total {
		return Packet{}, 0, false
	}
	data := buf[4 : 4+n]
	if buf[total-2] != tailByte || buf[total-1] != tailByte || buf[total-3] != checksum(cmd, data) {
		// Corrupt frame: skip the head and resync.
		return Packet{}, 2, false
	}
	cp := make([]byte, n)
	copy(cp, data)
	return Packet{Cmd: cmd, Data: cp}, total, true
}
