package rtpmidi

import (
	"bytes"
	"net/netip"
	"testing"
	"time"
)

// wire carries packets between two endpoints on fake addresses: a sends
// from its own address on the packet's port, b receives from that.
type wire struct {
	t    *testing.T
	a, b *Endpoint
	aIP  netip.Addr
	bIP  netip.Addr
	now  time.Time
}

func newWire(t *testing.T) *wire {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	w := &wire{t: t, now: now,
		a:   NewEndpoint("Push 3", now),
		b:   NewEndpoint("iPad", now),
		aIP: netip.MustParseAddr("10.0.0.1"), bIP: netip.MustParseAddr("10.0.0.2")}
	w.a.rand = func() uint32 { return 0x1111 }
	w.a.SSRC, w.b.SSRC = 0xAAAA, 0xBBBB
	return w
}

func (w *wire) ctrl(e *Endpoint) netip.AddrPort {
	if e == w.a {
		return netip.AddrPortFrom(w.aIP, 5004)
	}
	return netip.AddrPortFrom(w.bIP, 5004)
}

// run delivers outs until nothing is left, and collects every event.
func (w *wire) run(from *Endpoint, outs []Out) []Event {
	var evs []Event
	for len(outs) > 0 {
		o := outs[0]
		outs = outs[1:]
		to := w.b
		if from == w.b {
			to = w.a
		}
		if o.To.Addr() != w.ctrl(to).Addr() {
			w.t.Fatalf("%s sent to %v, not to %v", from.Name, o.To, w.ctrl(to))
		}
		src := w.ctrl(from)
		if o.Port == PortData {
			src = dataOf(src)
		}
		more, ev := to.Receive(o.Port, src, o.Data, w.now)
		evs = append(evs, ev...)
		// The answer goes back the other way; deliver it next.
		backEvs := w.run(to, more)
		evs = append(evs, backEvs...)
	}
	return evs
}

func connect(t *testing.T, w *wire) {
	t.Helper()
	evs := w.run(w.a, w.a.Invite(w.ctrl(w.b), w.now))
	var aConn, bConn bool
	for _, ev := range evs {
		if ev.Kind == EvConnected {
			if ev.Peer.SSRC == 0xBBBB {
				aConn = true
			}
			if ev.Peer.SSRC == 0xAAAA {
				bConn = true
			}
		}
	}
	if !aConn || !bConn {
		t.Fatalf("not connected: a=%v b=%v events %+v", aConn, bConn, evs)
	}
	if w.a.Connected() != 1 || w.b.Connected() != 1 {
		t.Fatalf("connected counts %d %d", w.a.Connected(), w.b.Connected())
	}
}

func TestInviteConnectsBothSides(t *testing.T) {
	w := newWire(t)
	connect(t, w)
	pa := w.a.Peers()[0]
	if !pa.Initiator || pa.Name != "iPad" || pa.State != StateConnected || pa.Data != dataOf(w.ctrl(w.b)) {
		t.Errorf("a's peer: %+v", pa)
	}
	pb := w.b.Peers()[0]
	if pb.Initiator || pb.Name != "Push 3" || pb.State != StateConnected || pb.Ctrl != w.ctrl(w.a) {
		t.Errorf("b's peer: %+v", pb)
	}
}

func TestClockSyncGivesLatency(t *testing.T) {
	w := newWire(t)
	connect(t, w)
	// The first CK0 went out with the data-port OK; the answer chain ran
	// in connect, at one instant: zero latency, zero offset.
	w.now = w.now.Add(clockEvery)
	outs, _ := w.a.Tick(w.now)
	if len(outs) != 1 || outs[0].Port != PortData || outs[0].Data[2] != 'C' {
		t.Fatalf("tick sent %+v", outs)
	}
	w.run(w.a, outs)
	if p := w.a.Peers()[0]; p.Latency != 0 || p.Offset != 0 {
		t.Errorf("latency %v offset %d", p.Latency, p.Offset)
	}
	// A responder's clock 10 ms ahead shows as its offset, not as latency.
	w.b.epoch = w.b.epoch.Add(-10 * time.Millisecond)
	w.now = w.now.Add(clockEvery)
	outs, _ = w.a.Tick(w.now)
	w.run(w.a, outs)
	if p := w.a.Peers()[0]; p.Latency != 0 || p.Offset != 100 {
		t.Errorf("latency %v offset %d, want 0 and 100", p.Latency, p.Offset)
	}
}

func TestMIDIBothWays(t *testing.T) {
	w := newWire(t)
	connect(t, w)
	evs := w.run(w.a, w.a.Send([][]byte{{0x90, 60, 100}, {0xF8}}, w.now))
	if len(evs) != 1 || evs[0].Kind != EvMIDI || len(evs[0].Messages) != 2 ||
		!bytes.Equal(evs[0].Messages[0], []byte{0x90, 60, 100}) {
		t.Fatalf("b got %+v", evs)
	}
	evs = w.run(w.b, w.b.Send([][]byte{{0xB0, 1, 2}}, w.now))
	if len(evs) != 1 || evs[0].Kind != EvMIDI || !bytes.Equal(evs[0].Messages[0], []byte{0xB0, 1, 2}) {
		t.Fatalf("a got %+v", evs)
	}
	if pa := w.a.Peers()[0]; pa.In != 1 || pa.Out != 2 {
		t.Errorf("a's counters %d in %d out", pa.In, pa.Out)
	}
}

func TestSendToNobodyIsNothing(t *testing.T) {
	w := newWire(t)
	if outs := w.a.Send([][]byte{{0xF8}}, w.now); outs != nil {
		t.Errorf("sent %+v", outs)
	}
}

func TestByeDisconnectsBoth(t *testing.T) {
	w := newWire(t)
	connect(t, w)
	outs, evs := w.a.Bye(0xBBBB, w.now)
	if len(evs) != 1 || evs[0].Kind != EvDisconnected {
		t.Fatalf("a's events %+v", evs)
	}
	evs = w.run(w.a, outs)
	if len(evs) != 1 || evs[0].Kind != EvDisconnected || evs[0].Peer.SSRC != 0xAAAA {
		t.Fatalf("b's events %+v", evs)
	}
	if w.a.Connected()+w.b.Connected() != 0 {
		t.Error("still connected")
	}
}

func TestInviteGivesUp(t *testing.T) {
	w := newWire(t)
	w.a.Invite(w.ctrl(w.b), w.now)
	sent := 1
	var evs []Event
	for i := 0; i < inviteTries+2 && len(evs) == 0; i++ {
		w.now = w.now.Add(inviteEvery)
		var outs []Out
		outs, evs = w.a.Tick(w.now)
		sent += len(outs)
	}
	if len(evs) != 1 || evs[0].Kind != EvFailed {
		t.Fatalf("events %+v", evs)
	}
	if sent != inviteTries {
		t.Errorf("sent %d invites, want %d", sent, inviteTries)
	}
	if len(w.a.Peers()) != 0 {
		t.Error("the peer is still listed")
	}
}

func TestRejectedInvite(t *testing.T) {
	w := newWire(t)
	w.a.Invite(w.ctrl(w.b), w.now)
	no := Exchange{Cmd: CmdReject, Token: 0x1111, SSRC: 0xBBBB, Name: "iPad"}.Marshal()
	_, evs := w.a.Receive(PortControl, w.ctrl(w.b), no, w.now)
	if len(evs) != 1 || evs[0].Kind != EvRejected || len(w.a.Peers()) != 0 {
		t.Fatalf("events %+v peers %+v", evs, w.a.Peers())
	}
}

func TestSilentPeerIsDropped(t *testing.T) {
	w := newWire(t)
	connect(t, w)
	w.now = w.now.Add(peerTimeout + time.Second)
	outs, evs := w.b.Tick(w.now)
	if len(evs) != 1 || evs[0].Kind != EvDisconnected {
		t.Fatalf("events %+v", evs)
	}
	if len(outs) != 1 || outs[0].Port != PortControl || outs[0].Data[2] != 'B' {
		t.Fatalf("outs %+v", outs)
	}
}

func TestClockFromUnknownPeerGetsBye(t *testing.T) {
	w := newWire(t)
	ck := Clock{SSRC: 0xCCCC, Count: 0, TS: [3]uint64{5}}.Marshal()
	outs, evs := w.a.Receive(PortData, dataOf(w.ctrl(w.b)), ck, w.now)
	if len(evs) != 0 || len(outs) != 1 || outs[0].Port != PortControl || outs[0].To != w.ctrl(w.b) || outs[0].Data[2] != 'B' {
		t.Fatalf("outs %+v evs %+v", outs, evs)
	}
}

func TestDataFromUnknownPeerIsDropped(t *testing.T) {
	w := newWire(t)
	connect(t, w)
	pkt := MarshalData(1, 1, 0xCCCC, [][]byte{{0x90, 60, 100}})
	_, evs := w.a.Receive(PortData, dataOf(w.ctrl(w.b)), pkt, w.now)
	if len(evs) != 0 {
		t.Fatalf("events %+v", evs)
	}
}

func TestReinviteReplacesSession(t *testing.T) {
	w := newWire(t)
	connect(t, w)
	// b restarts and invites a; a's old session with b's SSRC goes away.
	w.b = NewEndpoint("iPad", w.now)
	w.b.SSRC = 0xBBBB
	w.b.rand = func() uint32 { return 0x2222 }
	evs := w.run(w.b, w.b.Invite(w.ctrl(w.a), w.now))
	n := 0
	for _, ev := range evs {
		if ev.Kind == EvConnected {
			n++
		}
	}
	if n != 2 || w.a.Connected() != 1 || len(w.a.Peers()) != 1 || !w.b.Peers()[0].Initiator {
		t.Fatalf("events %+v, a has %d peers", evs, len(w.a.Peers()))
	}
}

func TestInvitingTwiceKeepsOnePeer(t *testing.T) {
	w := newWire(t)
	connect(t, w)
	connect(t, w)
	if len(w.a.Peers()) != 1 || len(w.b.Peers()) != 1 {
		t.Fatalf("a %d peers, b %d", len(w.a.Peers()), len(w.b.Peers()))
	}
}
