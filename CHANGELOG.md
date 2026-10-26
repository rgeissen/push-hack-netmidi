# Changelog

## [Unreleased]

### Added

- Net MIDI 0.1.0: the Push's MIDI as Apple MIDI (RTP-MIDI) on the network.
  One port, Net MIDI, for Live; routes for other ports with an optional
  channel; Bonjour through avahi; both roles (the iPad connects, or the
  Push does); remembered peers; a web page; a NETMIDI tab on the Push.
- Messages from the network play at the time the sender stamped them,
  plus a buffer (10 ms, set on the page); late and lost packets are
  counted. Before this, a short note whose on and off shared a packet had
  no length, and Wi-Fi's jitter landed on every note.
- IPv6: Apple's resolver gives `push.local` only its IPv6 addresses.
- Session off with a peer connected froze the hack; OFF on the tab takes
  two presses.
