package rtpmidi

import (
	"testing"
	"time"
)

// A packet's messages are due at the sender's stamp, moved by the clock
// offset, plus the delta times between them.
func TestMessageTimesFollowStampAndDeltas(t *testing.T) {
	w := newWire(t)
	connect(t, w)
	// b's clock runs 10 ms ahead of a's; a's peer record learns that.
	w.b.epoch = w.b.epoch.Add(-10 * time.Millisecond)
	w.now = w.now.Add(clockEvery)
	outs, _ := w.a.Tick(w.now)
	w.run(w.a, outs)
	pb := w.a.Peers()[0]
	if !pb.Synced || pb.Offset != 100 {
		t.Fatalf("peer %+v", pb)
	}
	// b stamps a packet 20 ms in its future with a 5 ms delta inside.
	stamp := uint32(w.b.Clock(w.now.Add(20 * time.Millisecond)))
	list := MarshalList([][]byte{{0x90, 60, 100}})
	list = append(list, 0x32, 0x80, 60, 0) // delta 50 (5 ms), then the off
	pkt := MarshalData(7, stamp, w.b.SSRC, nil)
	pkt = append(pkt[:rtpHeaderLen], byte(len(list)))
	pkt = append(pkt, list...)
	_, evs := w.a.Receive(PortData, dataOf(w.ctrl(w.b)), pkt, w.now)
	if len(evs) != 1 || len(evs[0].At) != 2 {
		t.Fatalf("events %+v", evs)
	}
	on, off := evs[0].At[0], evs[0].At[1]
	if d := on.Sub(w.now); d != 20*time.Millisecond {
		t.Errorf("note on due in %v, want 20ms", d)
	}
	if d := off.Sub(on); d != 5*time.Millisecond {
		t.Errorf("note off %v after the on, want 5ms", d)
	}
	if p := w.a.Peers()[0]; p.Packets != 1 || p.Lost != 0 {
		t.Errorf("packets %d lost %d", p.Packets, p.Lost)
	}
}

func TestLostPacketsAreCounted(t *testing.T) {
	w := newWire(t)
	connect(t, w)
	for _, seq := range []uint16{10, 11, 14, 15, 0xFFFF, 0, 3} {
		pkt := MarshalData(seq, 0, w.b.SSRC, [][]byte{{0xF8}})
		w.a.Receive(PortData, dataOf(w.ctrl(w.b)), pkt, w.now)
	}
	p := w.a.Peers()[0]
	if p.Packets != 7 || p.Lost != 2+2 {
		t.Errorf("packets %d lost %d, want 7 and 4", p.Packets, p.Lost)
	}
}

// Before the clocks are synced the messages carry no times: they are
// delivered as they come.
func TestNoTimesBeforeSync(t *testing.T) {
	w := newWire(t)
	connect(t, w)
	// connect ran the first clock sync; undo it, as for a responder whose
	// initiator has not synced yet.
	w.a.peers[w.b.SSRC].Synced = false
	pkt := MarshalData(1, 12345, w.b.SSRC, [][]byte{{0x90, 60, 100}})
	_, evs := w.a.Receive(PortData, dataOf(w.ctrl(w.b)), pkt, w.now)
	if len(evs) != 1 || evs[0].At != nil {
		t.Fatalf("events %+v", evs)
	}
}
