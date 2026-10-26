package rtpmidi

import (
	"bytes"
	"testing"
)

func msgsEqual(t *testing.T, got []Message, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d messages %+v, want %d %x", len(got), got, len(want), want)
	}
	for i := range got {
		if !bytes.Equal(got[i].Bytes, want[i]) {
			t.Errorf("message %d: got %x, want %x", i, got[i].Bytes, want[i])
		}
	}
}

func TestListRunningStatus(t *testing.T) {
	var p ListParser
	got := p.Parse([]byte{0x90, 60, 100, 0, 64, 100, 0, 67, 100}, false, false)
	msgsEqual(t, got, [][]byte{{0x90, 60, 100}, {0x90, 64, 100}, {0x90, 67, 100}})
}

func TestListFirstDeltaWithZ(t *testing.T) {
	var p ListParser
	got := p.Parse([]byte{0x81, 0x00, 0x90, 60, 100}, true, false)
	msgsEqual(t, got, [][]byte{{0x90, 60, 100}})
	if got[0].Delta != 128 {
		t.Errorf("delta %d, want 128", got[0].Delta)
	}
}

func TestListDeltaBetweenMessages(t *testing.T) {
	var p ListParser
	got := p.Parse([]byte{0x90, 60, 100, 0x05, 0x80, 60, 0}, false, false)
	msgsEqual(t, got, [][]byte{{0x90, 60, 100}, {0x80, 60, 0}})
	if got[1].Delta != 5 {
		t.Errorf("delta %d, want 5", got[1].Delta)
	}
}

func TestListPhantomStatusAcrossPackets(t *testing.T) {
	var p ListParser
	p.Parse([]byte{0x90, 60, 100}, false, false)
	got := p.Parse([]byte{64, 100}, false, true)
	msgsEqual(t, got, [][]byte{{0x90, 64, 100}})
}

func TestListPhantomWithoutStatusIsDropped(t *testing.T) {
	var p ListParser
	if got := p.Parse([]byte{64, 100}, false, true); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
	if got := p.Parse([]byte{64, 100}, false, false); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestListRealTimeKeepsRunningStatus(t *testing.T) {
	var p ListParser
	got := p.Parse([]byte{0x90, 60, 100, 0, 0xF8, 0, 64, 100}, false, false)
	msgsEqual(t, got, [][]byte{{0x90, 60, 100}, {0xF8}, {0x90, 64, 100}})
}

func TestListSystemCommonEndsRunningStatus(t *testing.T) {
	var p ListParser
	got := p.Parse([]byte{0x90, 60, 100, 0, 0xF2, 1, 2, 0, 64, 100}, false, false)
	msgsEqual(t, got, [][]byte{{0x90, 60, 100}, {0xF2, 1, 2}})
}

func TestListOneAndTwoByteMessages(t *testing.T) {
	var p ListParser
	got := p.Parse([]byte{0xC0, 5, 0, 0xD0, 9, 0, 0xB0, 7, 100, 0, 0xE0, 0, 64, 0, 0xF6, 0, 0xF1, 3}, false, false)
	msgsEqual(t, got, [][]byte{{0xC0, 5}, {0xD0, 9}, {0xB0, 7, 100}, {0xE0, 0, 64}, {0xF6}, {0xF1, 3}})
}

func TestListSysEx(t *testing.T) {
	var p ListParser
	got := p.Parse([]byte{0x90, 60, 100, 0, 0xF0, 1, 2, 3, 0xF7, 0, 0x80, 60, 0}, false, false)
	msgsEqual(t, got, [][]byte{{0x90, 60, 100}, {0xF0, 1, 2, 3, 0xF7}, {0x80, 60, 0}})
}

func TestListSysExSegments(t *testing.T) {
	var p ListParser
	first := p.Parse([]byte{0xF0, 1, 2, 0xF0}, false, false)
	msgsEqual(t, first, [][]byte{{0xF0, 1, 2}})
	last := p.Parse([]byte{0xF7, 3, 4, 0xF7}, false, false)
	msgsEqual(t, last, [][]byte{{0xF7, 3, 4, 0xF7}})
}

func TestListTruncatedMessageEndsList(t *testing.T) {
	var p ListParser
	got := p.Parse([]byte{0x90, 60, 100, 0, 0x90, 64}, false, false)
	msgsEqual(t, got, [][]byte{{0x90, 60, 100}})
	got = p.Parse([]byte{0x90, 60, 0x80}, false, false)
	if len(got) != 0 {
		t.Errorf("a status byte as data parsed: %+v", got)
	}
}

func TestMarshalListSkipsEmpty(t *testing.T) {
	b := MarshalList([][]byte{{0x90, 60, 100}, nil, {0x80, 60, 0}})
	if !bytes.Equal(b, []byte{0x90, 60, 100, 0, 0x80, 60, 0}) {
		t.Fatalf("got %x", b)
	}
	if MarshalList(nil) != nil {
		t.Error("empty list is not nil")
	}
}
