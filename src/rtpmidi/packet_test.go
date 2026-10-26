package rtpmidi

import (
	"bytes"
	"testing"
)

func TestExchangeRoundTrip(t *testing.T) {
	in := Exchange{Cmd: CmdInvite, Token: 0xDEADBEEF, SSRC: 0x01020304, Name: "Push 3"}
	b := in.Marshal()
	if !IsSession(b) || len(b) != 16+len("Push 3")+1 {
		t.Fatalf("marshal: %x", b)
	}
	out, err := ParseSession(b)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("got %+v, want %+v", out, in)
	}
}

// Apple's packets end the name with a NUL; some senders leave it out.
func TestExchangeNameWithoutNul(t *testing.T) {
	b := Exchange{Cmd: CmdAccept, Token: 1, SSRC: 2, Name: "iPad"}.Marshal()
	out, err := ParseSession(b[:len(b)-1])
	if err != nil || out.(Exchange).Name != "iPad" {
		t.Fatalf("got %+v, %v", out, err)
	}
}

func TestClockRoundTrip(t *testing.T) {
	in := Clock{SSRC: 7, Count: 1, TS: [3]uint64{10, 20, 0}}
	b := in.Marshal()
	if len(b) != 36 {
		t.Fatalf("CK is %d bytes", len(b))
	}
	out, err := ParseSession(b)
	if err != nil || out != in {
		t.Fatalf("got %+v, %v", out, err)
	}
}

func TestFeedbackRoundTrip(t *testing.T) {
	in := Feedback{SSRC: 9, Seq: 0x1234}
	out, err := ParseSession(in.Marshal())
	if err != nil || out != in {
		t.Fatalf("got %+v, %v", out, err)
	}
}

func TestParseSessionRejectsBadInput(t *testing.T) {
	for _, b := range [][]byte{
		nil,
		{0x80, 0x61},
		{0xFF, 0xFF, 'I', 'N', 0, 0, 0, 1}, // too short
		{0xFF, 0xFF, 'X', 'X', 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0},
		append([]byte{0xFF, 0xFF, 'I', 'N', 0, 0, 0, 3}, make([]byte, 8)...), // version 3
	} {
		if _, err := ParseSession(b); err == nil {
			t.Errorf("%x parsed", b)
		}
	}
}

func TestDataRoundTripShortAndLong(t *testing.T) {
	short := [][]byte{{0x90, 60, 100}, {0x90, 64, 100}}
	long := [][]byte{{0x90, 60, 100}, {0x90, 64, 100}, {0x90, 67, 100}, {0xB0, 1, 2}, {0xE0, 0, 64}}
	for _, msgs := range [][][]byte{short, long} {
		b := MarshalData(5, 1000, 0xABCD, msgs)
		d, err := ParseData(b, &ListParser{})
		if err != nil {
			t.Fatal(err)
		}
		if d.Seq != 5 || d.Timestamp != 1000 || d.SSRC != 0xABCD {
			t.Fatalf("header: %+v", d)
		}
		if len(d.Messages) != len(msgs) {
			t.Fatalf("got %d messages, want %d: %+v", len(d.Messages), len(msgs), d.Messages)
		}
		for i, m := range d.Messages {
			if !bytes.Equal(m.Bytes, msgs[i]) || m.Delta != 0 {
				t.Errorf("message %d: %+v", i, m)
			}
		}
	}
	if b := MarshalData(0, 0, 1, long); b[rtpHeaderLen]&0x80 == 0 {
		t.Error("a list over 15 bytes needs the long length")
	}
}

// iOS sends a recovery journal after the list; the list's length says
// where the messages end.
func TestDataSkipsJournal(t *testing.T) {
	b := MarshalData(1, 2, 3, [][]byte{{0x90, 60, 100}})
	b[rtpHeaderLen] |= 0x40 // J
	b = append(b, 0x01, 0x02, 0x03, 0x04, 0x90, 0x7F, 0x7F)
	d, err := ParseData(b, &ListParser{})
	if err != nil || len(d.Messages) != 1 || !bytes.Equal(d.Messages[0].Bytes, []byte{0x90, 60, 100}) {
		t.Fatalf("got %+v, %v", d.Messages, err)
	}
}

func TestDataRejectsOtherPayload(t *testing.T) {
	b := MarshalData(1, 2, 3, [][]byte{{0x90, 60, 100}})
	b[1] = 0x60 // payload type 96
	if _, err := ParseData(b, &ListParser{}); err == nil {
		t.Error("payload type 96 parsed")
	}
	if _, err := ParseData(b[:8], &ListParser{}); err == nil {
		t.Error("a truncated header parsed")
	}
}

func TestEmptyListIsLegal(t *testing.T) {
	d, err := ParseData(MarshalData(1, 2, 3, nil), &ListParser{})
	if err != nil || len(d.Messages) != 0 {
		t.Fatalf("got %+v, %v", d, err)
	}
}
