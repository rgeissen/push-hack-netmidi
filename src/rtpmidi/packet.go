// Package rtpmidi speaks Apple MIDI: RTP-MIDI (RFC 6295) with Apple's
// session protocol, the network MIDI of iOS, iPadOS and macOS.
//
// This package has no sockets. packet.go encodes and decodes the session
// packets (invite, accept, reject, bye, clock sync, receiver feedback) and
// the RTP data packets; midilist.go the MIDI list inside them; session.go
// is the state machine for every peer, driven by Endpoint.Receive and
// Endpoint.Tick and answering with what to send. peer.go puts it on UDP.
package rtpmidi

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ProtocolVersion is the only Apple MIDI session protocol version.
const ProtocolVersion = 2

// Command is the two-letter command of a session packet.
type Command [2]byte

// The session commands.
var (
	CmdInvite   = Command{'I', 'N'}
	CmdReject   = Command{'N', 'O'}
	CmdAccept   = Command{'O', 'K'}
	CmdBye      = Command{'B', 'Y'}
	CmdClock    = Command{'C', 'K'}
	CmdFeedback = Command{'R', 'S'}
)

func (c Command) String() string { return string(c[:]) }

// Exchange is an invite, accept, reject or bye: the four packets that carry
// a token, an SSRC and a name.
type Exchange struct {
	Cmd   Command
	Token uint32 // the initiator's token, echoed in the answer
	SSRC  uint32 // the sender's
	Name  string // the sender's session name
}

// Marshal is the wire form: 0xFFFF, command, version, token, SSRC, name
// and a NUL.
func (e Exchange) Marshal() []byte {
	b := make([]byte, 0, 16+len(e.Name)+1)
	b = append(b, 0xFF, 0xFF, e.Cmd[0], e.Cmd[1])
	b = binary.BigEndian.AppendUint32(b, ProtocolVersion)
	b = binary.BigEndian.AppendUint32(b, e.Token)
	b = binary.BigEndian.AppendUint32(b, e.SSRC)
	b = append(b, e.Name...)
	return append(b, 0)
}

// Clock is a clock-sync packet. Count says which of the three steps it is;
// TS holds the timestamps filled in so far, in 100 us units.
type Clock struct {
	SSRC  uint32
	Count uint8
	TS    [3]uint64
}

// Marshal is the wire form: 36 bytes.
func (c Clock) Marshal() []byte {
	b := make([]byte, 0, 36)
	b = append(b, 0xFF, 0xFF, 'C', 'K')
	b = binary.BigEndian.AppendUint32(b, c.SSRC)
	b = append(b, c.Count, 0, 0, 0)
	for _, ts := range c.TS {
		b = binary.BigEndian.AppendUint64(b, ts)
	}
	return b
}

// Feedback is receiver feedback: the newest sequence number a peer has,
// so the sender may drop older journal entries. The hack keeps no journal
// and only reads these.
type Feedback struct {
	SSRC uint32
	Seq  uint16
}

// Marshal is the wire form: 12 bytes.
func (f Feedback) Marshal() []byte {
	b := make([]byte, 0, 12)
	b = append(b, 0xFF, 0xFF, 'R', 'S')
	b = binary.BigEndian.AppendUint32(b, f.SSRC)
	b = binary.BigEndian.AppendUint16(b, f.Seq)
	return append(b, 0, 0)
}

// IsSession reports whether a packet is a session packet (0xFFFF first)
// rather than RTP data. Both kinds arrive on the data port.
func IsSession(b []byte) bool {
	return len(b) >= 4 && b[0] == 0xFF && b[1] == 0xFF
}

// ErrNotSession says the packet does not start with 0xFFFF.
var ErrNotSession = errors.New("rtpmidi: not a session packet")

// ParseSession decodes a session packet into an Exchange, a Clock or a
// Feedback.
func ParseSession(b []byte) (any, error) {
	if !IsSession(b) {
		return nil, ErrNotSession
	}
	cmd := Command{b[2], b[3]}
	switch cmd {
	case CmdInvite, CmdReject, CmdAccept, CmdBye:
		if len(b) < 16 {
			return nil, fmt.Errorf("rtpmidi: %s packet too short (%d bytes)", cmd, len(b))
		}
		if v := binary.BigEndian.Uint32(b[4:]); v != ProtocolVersion {
			return nil, fmt.Errorf("rtpmidi: %s protocol version %d, want %d", cmd, v, ProtocolVersion)
		}
		name := b[16:]
		for i, c := range name {
			if c == 0 {
				name = name[:i]
				break
			}
		}
		return Exchange{
			Cmd:   cmd,
			Token: binary.BigEndian.Uint32(b[8:]),
			SSRC:  binary.BigEndian.Uint32(b[12:]),
			Name:  string(name),
		}, nil
	case CmdClock:
		if len(b) < 36 {
			return nil, fmt.Errorf("rtpmidi: CK packet too short (%d bytes)", len(b))
		}
		c := Clock{SSRC: binary.BigEndian.Uint32(b[4:]), Count: b[8]}
		for i := range c.TS {
			c.TS[i] = binary.BigEndian.Uint64(b[12+8*i:])
		}
		return c, nil
	case CmdFeedback:
		if len(b) < 10 {
			return nil, fmt.Errorf("rtpmidi: RS packet too short (%d bytes)", len(b))
		}
		return Feedback{SSRC: binary.BigEndian.Uint32(b[4:]), Seq: binary.BigEndian.Uint16(b[8:])}, nil
	}
	return nil, fmt.Errorf("rtpmidi: unknown session command %q", cmd.String())
}

// RTP header facts: version 2, payload type 97 (the Apple MIDI default).
const (
	rtpVersion     = 2
	rtpPayloadType = 97
	rtpHeaderLen   = 12
)

// Data is one RTP-MIDI packet as received: its header fields and the MIDI
// messages in it. The recovery journal, if any, is skipped.
type Data struct {
	Seq       uint16
	Timestamp uint32
	SSRC      uint32
	Messages  []Message
}

// MarshalData builds an RTP-MIDI packet with every message at the packet's
// time (no delta times, no journal). Messages must be complete MIDI
// messages; an empty list gives an empty packet, which is legal.
func MarshalData(seq uint16, ts uint32, ssrc uint32, msgs [][]byte) []byte {
	list := MarshalList(msgs)
	b := make([]byte, 0, rtpHeaderLen+2+len(list))
	b = append(b, rtpVersion<<6, rtpPayloadType)
	b = binary.BigEndian.AppendUint16(b, seq)
	b = binary.BigEndian.AppendUint32(b, ts)
	b = binary.BigEndian.AppendUint32(b, ssrc)
	if len(list) > 15 {
		b = append(b, 0x80|byte(len(list)>>8), byte(len(list)))
	} else {
		b = append(b, byte(len(list)))
	}
	return append(b, list...)
}

// ParseData decodes an RTP-MIDI packet. p keeps the running status across
// packets from one peer; pass the same parser for the same peer.
func ParseData(b []byte, p *ListParser) (Data, error) {
	if len(b) < rtpHeaderLen+1 {
		return Data{}, fmt.Errorf("rtpmidi: data packet too short (%d bytes)", len(b))
	}
	if b[0]>>6 != rtpVersion {
		return Data{}, fmt.Errorf("rtpmidi: RTP version %d", b[0]>>6)
	}
	if b[1]&0x7F != rtpPayloadType {
		return Data{}, fmt.Errorf("rtpmidi: RTP payload type %d, want %d", b[1]&0x7F, rtpPayloadType)
	}
	d := Data{
		Seq:       binary.BigEndian.Uint16(b[2:]),
		Timestamp: binary.BigEndian.Uint32(b[4:]),
		SSRC:      binary.BigEndian.Uint32(b[8:]),
	}
	off := rtpHeaderLen
	if cc := int(b[0] & 0x0F); cc > 0 {
		off += 4 * cc // contributing sources: never seen from Apple, legal
	}
	if b[0]&0x10 != 0 { // a header extension
		if len(b) < off+4 {
			return d, errors.New("rtpmidi: truncated RTP extension")
		}
		off += 4 + 4*int(binary.BigEndian.Uint16(b[off+2:]))
	}
	if len(b) < off+1 {
		return d, errors.New("rtpmidi: no MIDI command section")
	}
	flags := b[off]
	off++
	n := int(flags & 0x0F)
	if flags&0x80 != 0 { // B: 12-bit length
		if len(b) < off+1 {
			return d, errors.New("rtpmidi: truncated MIDI command section")
		}
		n = n<<8 | int(b[off])
		off++
	}
	if len(b) < off+n {
		return d, fmt.Errorf("rtpmidi: MIDI list says %d bytes, %d left", n, len(b)-off)
	}
	d.Messages = p.Parse(b[off:off+n], flags&0x20 != 0, flags&0x10 != 0)
	return d, nil
}
