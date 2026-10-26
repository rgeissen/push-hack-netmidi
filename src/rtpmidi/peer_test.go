package rtpmidi

import (
	"bytes"
	"net/netip"
	"testing"
	"time"
)

func waitEvent(t *testing.T, ch <-chan Event, kind EventKind) Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Kind == kind {
				return ev
			}
		case <-deadline:
			t.Fatalf("no %v event", kind)
		}
	}
}

// Two transports on the loopback: invite, MIDI both ways, bye.
func TestLoopback(t *testing.T) {
	a, err := Listen(15004, "Push 3")
	if err != nil {
		t.Skip("cannot bind UDP:", err)
	}
	defer a.Close()
	b, err := Listen(15010, "iPad")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	aEv, bEv := make(chan Event, 16), make(chan Event, 16)
	a.OnEvent = func(ev Event) { aEv <- ev }
	b.OnEvent = func(ev Event) { bEv <- ev }

	a.Invite(netip.MustParseAddrPort("127.0.0.1:15010"))
	if ev := waitEvent(t, aEv, EvConnected); ev.Peer.Name != "iPad" || !ev.Peer.Initiator {
		t.Errorf("a's peer %+v", ev.Peer)
	}
	if ev := waitEvent(t, bEv, EvConnected); ev.Peer.Name != "Push 3" || ev.Peer.Initiator {
		t.Errorf("b's peer %+v", ev.Peer)
	}
	if a.Connected() != 1 || b.Connected() != 1 {
		t.Fatalf("connected %d %d", a.Connected(), b.Connected())
	}

	a.Send([][]byte{{0x90, 60, 100}})
	if ev := waitEvent(t, bEv, EvMIDI); !bytes.Equal(ev.Messages[0], []byte{0x90, 60, 100}) {
		t.Errorf("b got %x", ev.Messages)
	}
	b.Send([][]byte{{0x80, 60, 0}, {0xF8}})
	if ev := waitEvent(t, aEv, EvMIDI); len(ev.Messages) != 2 || ev.Messages[1][0] != 0xF8 {
		t.Errorf("a got %x", ev.Messages)
	}

	b.Close()
	waitEvent(t, aEv, EvDisconnected)
	if a.Connected() != 0 {
		t.Error("a still connected")
	}
}
