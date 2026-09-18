package niimbot

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMarshalMatchesCapturedFrames(t *testing.T) {
	// Frames captured from the B1 session (docs/niimbot.md §4).
	cases := []struct {
		p    Packet
		want string
	}{
		{Packet{Cmd: CmdHeartbeat, Data: []byte{1}}, "5555dc0101dcaaaa"},
		{Packet{Cmd: CmdPrinterInfo, Data: []byte{byte(InfoDeviceType)}}, "555540010849aaaa"},
		{Packet{Cmd: CmdPrintStart, Data: []byte{0, 1, 0, 0, 0, 0, 0}}, "555501070001000000000007aaaa"},
		{Packet{Cmd: CmdSetPageSize, Data: []byte{0x00, 0xf0, 0x01, 0x80, 0x00, 0x01}}, "5555130600f00180000165aaaa"},
	}
	for _, c := range cases {
		got, err := c.p.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(got) != c.want {
			t.Errorf("%v: got %x want %s", c.p, got, c.want)
		}
	}
}

func TestMarshalRejectsLongData(t *testing.T) {
	if _, err := (Packet{Cmd: 1, Data: make([]byte, 256)}).Marshal(); err != ErrPacketTooLong {
		t.Fatalf("got %v", err)
	}
}

func TestParserReassemblesAndSplits(t *testing.T) {
	var ps Parser
	// Two packets in one chunk, the second split across feeds.
	rfid := "55551b31881d8815e81e108008313032363232363010505a3149373130333030303033353838011400000100e6881d8815e81e1080b2aaaa"
	hb := "5555dd0d20e40061006100004d000400015caaaa"
	all := mustHex(t, hb+rfid)
	got := ps.Feed(all[:20])
	if len(got) != 1 || got[0].Cmd != RespHeartbeatAdv1 || len(got[0].Data) != 13 {
		t.Fatalf("first feed: %v", got)
	}
	got = ps.Feed(all[20:60])
	if len(got) != 0 {
		t.Fatalf("partial should yield nothing, got %v", got)
	}
	got = ps.Feed(all[60:])
	if len(got) != 1 || got[0].Cmd != RespRfidInfo || len(got[0].Data) != 0x31 {
		t.Fatalf("second: %v", got)
	}
	if !bytes.Equal(got[0].Data[:8], mustHex(t, "881d8815e81e1080")) {
		t.Errorf("rfid uuid bytes wrong: %x", got[0].Data[:8])
	}
}

func TestParserResyncsAfterGarbage(t *testing.T) {
	var ps Parser
	frame := mustHex(t, "5555480210005aaaaa") // device type 4096
	in := append([]byte{0x01, 0x55, 0x99}, frame...)
	got := ps.Feed(in)
	if len(got) != 1 || got[0].Cmd != 0x48 || !bytes.Equal(got[0].Data, []byte{0x10, 0x00}) {
		t.Fatalf("got %v", got)
	}
	// Bad checksum is skipped; the following good frame still parses.
	bad := mustHex(t, "5555480210005baaaa")
	got = ps.Feed(append(bad, frame...))
	if len(got) != 1 || got[0].Cmd != 0x48 {
		t.Fatalf("after bad checksum got %v", got)
	}
}

func TestParserRoundTrip(t *testing.T) {
	var ps Parser
	var stream []byte
	want := []Packet{{Cmd: 0x02, Data: []byte{1}}, {Cmd: 0x85, Data: bytes.Repeat([]byte{0xAA}, 54)}, {Cmd: 0xF4, Data: nil}}
	for _, p := range want {
		b, _ := p.Marshal()
		stream = append(stream, b...)
	}
	var got []Packet
	for _, b := range stream { // one byte at a time
		got = append(got, ps.Feed([]byte{b})...)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d packets want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Cmd != want[i].Cmd || !bytes.Equal(got[i].Data, want[i].Data) {
			t.Errorf("packet %d: got %v want %v", i, got[i], want[i])
		}
	}
}
