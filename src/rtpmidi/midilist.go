package rtpmidi

// Message is one MIDI message from a MIDI list, with the delta time (in
// the packet's clock, 100 us units) that preceded it.
type Message struct {
	Delta uint32
	Bytes []byte
}

// ListParser decodes MIDI lists. It remembers the running status, which
// RFC 6295 lets a sender carry from one packet to the next (the P flag).
type ListParser struct {
	running byte
}

// Parse decodes one MIDI list. z says the first message has a delta time;
// phantom says the first message has no status byte and uses the running
// status of the previous packet. Bad bytes end the list: what was decoded
// before them is returned.
func (p *ListParser) Parse(b []byte, z, phantom bool) []Message {
	var msgs []Message
	off := 0
	first := true
	for off < len(b) {
		var delta uint32
		if !first || z {
			var ok bool
			delta, off, ok = readDelta(b, off)
			if !ok {
				return msgs
			}
		}
		if off >= len(b) {
			return msgs
		}
		status := p.running
		if b[off]&0x80 != 0 {
			status = b[off]
			off++
		} else if first && !phantom || status == 0 {
			return msgs // data byte with no status to run under
		}
		first = false
		var msg []byte
		switch {
		case status == 0xF0: // SysEx, to its end or to the next segment mark
			end := off
			for end < len(b) && b[end] != 0xF7 && b[end] != 0xF0 && b[end] != 0xF4 {
				end++
			}
			msg = append([]byte{0xF0}, b[off:end]...)
			if end < len(b) {
				if b[end] == 0xF7 {
					msg = append(msg, 0xF7)
				}
				end++ // F0 or F4 marks a segment that goes on in a later packet
			}
			off = end
			// Running status is not affected by SysEx; p.running stays.
		case status == 0xF7: // the rest of a segmented SysEx
			end := off
			for end < len(b) && b[end] != 0xF7 && b[end] != 0xF0 && b[end] != 0xF4 {
				end++
			}
			msg = append([]byte{0xF7}, b[off:end]...)
			if end < len(b) {
				msg = append(msg, b[end])
				end++
			}
			off = end
		case status >= 0xF8: // real time: one byte, keeps running status
			msg = []byte{status}
		case status >= 0xF1: // system common: ends running status
			n := map[byte]int{0xF1: 1, 0xF2: 2, 0xF3: 1, 0xF4: 0, 0xF5: 0, 0xF6: 0}[status]
			if off+n > len(b) {
				return msgs
			}
			msg = append([]byte{status}, b[off:off+n]...)
			off += n
			p.running = 0
		default: // channel voice
			n := 2
			if k := status & 0xF0; k == 0xC0 || k == 0xD0 {
				n = 1
			}
			if off+n > len(b) {
				return msgs
			}
			for _, d := range b[off : off+n] {
				if d&0x80 != 0 {
					return msgs
				}
			}
			msg = append([]byte{status}, b[off:off+n]...)
			off += n
			p.running = status
		}
		msgs = append(msgs, Message{Delta: delta, Bytes: msg})
	}
	return msgs
}

// readDelta reads a delta time: one to four bytes, high bit set on every
// byte but the last.
func readDelta(b []byte, off int) (uint32, int, bool) {
	var v uint32
	for i := 0; i < 4 && off < len(b); i++ {
		c := b[off]
		off++
		v = v<<7 | uint32(c&0x7F)
		if c&0x80 == 0 {
			return v, off, true
		}
	}
	return 0, off, false
}

// MarshalList builds a MIDI list with every message at delta 0 and its own
// status byte: the plainest form, which every receiver reads.
func MarshalList(msgs [][]byte) []byte {
	var b []byte
	for i, m := range msgs {
		if len(m) == 0 {
			continue
		}
		if i > 0 {
			b = append(b, 0) // delta 0 before every message but the first
		}
		b = append(b, m...)
	}
	return b
}
