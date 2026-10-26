package rtpmidi

import (
	"encoding/binary"
	"math/rand/v2"
	"net/netip"
	"time"
)

// Port says which of the two UDP ports a packet is on.
type Port int

// The two ports: control (the even one) and data (control + 1).
const (
	PortControl Port = iota
	PortData
)

// Out is a packet to send.
type Out struct {
	Port Port
	To   netip.AddrPort
	Data []byte
}

// EventKind is what happened.
type EventKind int

// The events an Endpoint reports.
const (
	EvConnected    EventKind = iota // the peer is connected
	EvDisconnected                  // it said bye or went silent
	EvRejected                      // it said no to our invite
	EvFailed                        // our invite got no answer
	EvMIDI                          // Messages arrived from it
	EvInvited                       // it invited us (the handshake goes on)
)

func (k EventKind) String() string {
	return [...]string{"connected", "disconnected", "rejected", "failed", "midi", "invited"}[k]
}

// Event is one thing that happened to a peer.
type Event struct {
	Kind     EventKind
	Peer     Peer
	Messages [][]byte // EvMIDI only
}

// PeerState is where a peer's session stands.
type PeerState int

// The states. An initiator goes Inviting (IN on control) -> InvitingData
// (IN on data) -> Connected. A responder goes InvitingData (OK sent on
// control, IN on data awaited) -> Connected.
const (
	StateInviting PeerState = iota
	StateInvitingData
	StateConnected
)

func (s PeerState) String() string {
	return [...]string{"inviting", "inviting-data", "connected"}[s]
}

// Peer is one remote session.
type Peer struct {
	SSRC      uint32
	Name      string
	Token     uint32
	Ctrl      netip.AddrPort // its control address
	Data      netip.AddrPort // its data address
	Initiator bool           // we invited it
	State     PeerState
	Since     time.Time // when it connected
	LastSeen  time.Time
	Latency   time.Duration // one way, from the last clock sync
	Offset    int64         // its clock minus ours, 100 us units
	In, Out   uint64        // messages

	tries     int       // invites sent so far
	lastSent  time.Time // the last invite
	lastClock time.Time // the last CK0 we sent (initiator)
	parser    ListParser
}

// Timing. Apple's implementation invites about once a second a dozen
// times, syncs clocks every few seconds, and takes a peer silent for
// half a minute as gone.
const (
	inviteEvery   = time.Second
	inviteTries   = 12
	clockEvery    = 3 * time.Second
	peerTimeout   = 30 * time.Second
	halfOpenLimit = 15 * time.Second // a responder waiting for the data invite
)

// Endpoint is our session: one SSRC and name, any number of peers. It is
// not safe for concurrent use; the transport holds a lock.
type Endpoint struct {
	SSRC  uint32
	Name  string
	epoch time.Time
	seq   uint16
	peers map[uint32]*Peer         // by the peer's SSRC
	asked map[netip.AddrPort]*Peer // invited by us, no SSRC yet; by control address
	rand  func() uint32
}

// NewEndpoint makes a session with a fresh SSRC. now is the clock's zero.
func NewEndpoint(name string, now time.Time) *Endpoint {
	return &Endpoint{
		SSRC:  rand.Uint32(),
		Name:  name,
		epoch: now,
		peers: map[uint32]*Peer{},
		asked: map[netip.AddrPort]*Peer{},
		rand:  rand.Uint32,
	}
}

// Clock is our time in 100 us units since the endpoint began.
func (e *Endpoint) Clock(now time.Time) uint64 {
	return uint64(now.Sub(e.epoch) / (100 * time.Microsecond))
}

// Peers lists every peer, connected or not.
func (e *Endpoint) Peers() []Peer {
	var ps []Peer
	for _, p := range e.asked {
		ps = append(ps, *p)
	}
	for _, p := range e.peers {
		ps = append(ps, *p)
	}
	return ps
}

// Connected counts the peers that are connected.
func (e *Endpoint) Connected() int {
	n := 0
	for _, p := range e.peers {
		if p.State == StateConnected {
			n++
		}
	}
	return n
}

func dataOf(ctrl netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(ctrl.Addr(), ctrl.Port()+1)
}

func (e *Endpoint) exchange(cmd Command, token uint32) []byte {
	return Exchange{Cmd: cmd, Token: token, SSRC: e.SSRC, Name: e.Name}.Marshal()
}

// Invite starts a session with a peer at a control address. A peer already
// known there is invited again.
func (e *Endpoint) Invite(ctrl netip.AddrPort, now time.Time) []Out {
	for ssrc, p := range e.peers {
		if p.Ctrl == ctrl {
			delete(e.peers, ssrc)
		}
	}
	p := &Peer{Token: e.rand(), Ctrl: ctrl, Data: dataOf(ctrl), Initiator: true,
		State: StateInviting, LastSeen: now, tries: 1, lastSent: now}
	e.asked[ctrl] = p
	return []Out{{PortControl, ctrl, e.exchange(CmdInvite, p.Token)}}
}

// Bye ends a session and tells the peer.
func (e *Endpoint) Bye(ssrc uint32, now time.Time) ([]Out, []Event) {
	p, ok := e.peers[ssrc]
	if !ok {
		return nil, nil
	}
	delete(e.peers, ssrc)
	return []Out{{PortControl, p.Ctrl, e.exchange(CmdBye, p.Token)}},
		[]Event{{Kind: EvDisconnected, Peer: *p}}
}

// ByeAll ends every session.
func (e *Endpoint) ByeAll(now time.Time) ([]Out, []Event) {
	var outs []Out
	var evs []Event
	for ssrc := range e.peers {
		o, ev := e.Bye(ssrc, now)
		outs = append(outs, o...)
		evs = append(evs, ev...)
	}
	for ctrl := range e.asked {
		delete(e.asked, ctrl)
	}
	return outs, evs
}

// Send puts messages in one RTP packet to every connected peer.
func (e *Endpoint) Send(msgs [][]byte, now time.Time) []Out {
	if len(msgs) == 0 || e.Connected() == 0 {
		return nil
	}
	pkt := MarshalData(e.seq, uint32(e.Clock(now)), e.SSRC, msgs)
	e.seq++
	var outs []Out
	for _, p := range e.peers {
		if p.State == StateConnected {
			p.Out += uint64(len(msgs))
			outs = append(outs, Out{PortData, p.Data, pkt})
		}
	}
	return outs
}

// Receive handles one packet and says what to send and what happened.
func (e *Endpoint) Receive(port Port, from netip.AddrPort, b []byte, now time.Time) ([]Out, []Event) {
	if !IsSession(b) {
		if port != PortData {
			return nil, nil
		}
		return e.receiveData(from, b, now)
	}
	pkt, err := ParseSession(b)
	if err != nil {
		return nil, nil
	}
	switch m := pkt.(type) {
	case Exchange:
		return e.receiveExchange(port, from, m, now)
	case Clock:
		if port == PortData {
			return e.receiveClock(from, m, now)
		}
	case Feedback:
		if p := e.peers[m.SSRC]; p != nil {
			p.LastSeen = now
		}
	}
	return nil, nil
}

func (e *Endpoint) receiveExchange(port Port, from netip.AddrPort, m Exchange, now time.Time) ([]Out, []Event) {
	switch m.Cmd {
	case CmdInvite:
		if port == PortControl {
			// A peer invites us. One that invites again (it lost its state)
			// starts over.
			p := &Peer{SSRC: m.SSRC, Name: m.Name, Token: m.Token, Ctrl: from, Data: dataOf(from),
				State: StateInvitingData, LastSeen: now, lastSent: now}
			e.peers[m.SSRC] = p
			return []Out{{PortControl, from, e.exchange(CmdAccept, m.Token)}},
				[]Event{{Kind: EvInvited, Peer: *p}}
		}
		p := e.peers[m.SSRC]
		if p == nil || p.Initiator || p.Token != m.Token {
			return []Out{{PortData, from, e.exchange(CmdReject, m.Token)}}, nil
		}
		p.Data = from
		p.LastSeen = now
		if p.State != StateConnected {
			p.State = StateConnected
			p.Since = now
			return []Out{{PortData, from, e.exchange(CmdAccept, m.Token)}},
				[]Event{{Kind: EvConnected, Peer: *p}}
		}
		return []Out{{PortData, from, e.exchange(CmdAccept, m.Token)}}, nil
	case CmdAccept:
		if port == PortControl {
			p := e.asked[from]
			if p == nil || p.Token != m.Token {
				return nil, nil
			}
			delete(e.asked, from)
			p.SSRC, p.Name = m.SSRC, m.Name
			p.State = StateInvitingData
			p.LastSeen, p.lastSent, p.tries = now, now, 1
			e.peers[m.SSRC] = p
			return []Out{{PortData, p.Data, e.exchange(CmdInvite, p.Token)}}, nil
		}
		p := e.peers[m.SSRC]
		if p == nil || !p.Initiator || p.Token != m.Token || p.State == StateConnected {
			return nil, nil
		}
		p.Data = from
		p.State = StateConnected
		p.Since, p.LastSeen, p.lastClock = now, now, now
		return []Out{{PortData, p.Data, e.clock0(now)}}, []Event{{Kind: EvConnected, Peer: *p}}
	case CmdReject:
		if p := e.asked[from]; p != nil && p.Token == m.Token {
			delete(e.asked, from)
			return nil, []Event{{Kind: EvRejected, Peer: *p}}
		}
		if p := e.peers[m.SSRC]; p != nil && p.Initiator && p.Token == m.Token {
			delete(e.peers, m.SSRC)
			return nil, []Event{{Kind: EvRejected, Peer: *p}}
		}
	case CmdBye:
		if p := e.peers[m.SSRC]; p != nil {
			delete(e.peers, m.SSRC)
			if p.State == StateConnected {
				return nil, []Event{{Kind: EvDisconnected, Peer: *p}}
			}
		}
	}
	return nil, nil
}

func (e *Endpoint) clock0(now time.Time) []byte {
	return Clock{SSRC: e.SSRC, Count: 0, TS: [3]uint64{e.Clock(now)}}.Marshal()
}

func (e *Endpoint) receiveClock(from netip.AddrPort, m Clock, now time.Time) ([]Out, []Event) {
	p := e.peers[m.SSRC]
	if p == nil {
		// A peer that still thinks it is connected (we restarted): a bye
		// to its control port lets it drop the session.
		ctrl := netip.AddrPortFrom(from.Addr(), from.Port()-1)
		return []Out{{PortControl, ctrl, e.exchange(CmdBye, 0)}}, nil
	}
	p.LastSeen = now
	t := e.Clock(now)
	switch m.Count {
	case 0:
		m.Count, m.SSRC, m.TS[1] = 1, e.SSRC, t
		return []Out{{PortData, from, m.Marshal()}}, nil
	case 1:
		m.Count, m.SSRC, m.TS[2] = 2, e.SSRC, t
		p.Latency = time.Duration(t-m.TS[0]) * 100 * time.Microsecond / 2
		p.Offset = int64(m.TS[1]) - int64(m.TS[0]+t)/2
		return []Out{{PortData, from, m.Marshal()}}, nil
	case 2:
		p.Latency = time.Duration(m.TS[2]-m.TS[0]) * 100 * time.Microsecond / 2
		p.Offset = int64(m.TS[0]+m.TS[2])/2 - int64(m.TS[1])
	}
	return nil, nil
}

func (e *Endpoint) receiveData(from netip.AddrPort, b []byte, now time.Time) ([]Out, []Event) {
	if len(b) < rtpHeaderLen {
		return nil, nil
	}
	p := e.peers[binary.BigEndian.Uint32(b[8:])]
	if p == nil || p.State != StateConnected {
		return nil, nil
	}
	p.LastSeen = now
	d, err := ParseData(b, &p.parser)
	if err != nil || len(d.Messages) == 0 {
		return nil, nil
	}
	msgs := make([][]byte, len(d.Messages))
	for i, m := range d.Messages {
		msgs[i] = m.Bytes
	}
	p.In += uint64(len(msgs))
	return nil, []Event{{Kind: EvMIDI, Peer: *p, Messages: msgs}}
}

// Tick runs the timers: invites again, syncs clocks, drops silent peers.
// Call it about once a second.
func (e *Endpoint) Tick(now time.Time) ([]Out, []Event) {
	var outs []Out
	var evs []Event
	for ctrl, p := range e.asked {
		if now.Sub(p.lastSent) < inviteEvery {
			continue
		}
		if p.tries >= inviteTries {
			delete(e.asked, ctrl)
			evs = append(evs, Event{Kind: EvFailed, Peer: *p})
			continue
		}
		p.tries++
		p.lastSent = now
		outs = append(outs, Out{PortControl, ctrl, e.exchange(CmdInvite, p.Token)})
	}
	for ssrc, p := range e.peers {
		switch p.State {
		case StateInvitingData:
			if p.Initiator {
				if now.Sub(p.lastSent) < inviteEvery {
					continue
				}
				if p.tries >= inviteTries {
					delete(e.peers, ssrc)
					evs = append(evs, Event{Kind: EvFailed, Peer: *p})
					continue
				}
				p.tries++
				p.lastSent = now
				outs = append(outs, Out{PortData, p.Data, e.exchange(CmdInvite, p.Token)})
			} else if now.Sub(p.lastSent) > halfOpenLimit {
				delete(e.peers, ssrc) // it never invited on the data port
			}
		case StateConnected:
			if now.Sub(p.LastSeen) > peerTimeout {
				delete(e.peers, ssrc)
				outs = append(outs, Out{PortControl, p.Ctrl, e.exchange(CmdBye, p.Token)})
				evs = append(evs, Event{Kind: EvDisconnected, Peer: *p})
				continue
			}
			if p.Initiator && now.Sub(p.lastClock) >= clockEvery {
				p.lastClock = now
				outs = append(outs, Out{PortData, p.Data, e.clock0(now)})
			}
		}
	}
	return outs, evs
}
