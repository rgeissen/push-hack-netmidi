# CLAUDE.md -- push-hack-netmidi

Net MIDI: the Push's MIDI ports on the network as Apple MIDI (RTP-MIDI), a
push-hack catalog hack. Framework rules live in
`../ableton-push-hack/CLAUDE.md` and apply here; the plan is
`../ableton-push-hack/plans/2026-10-26-net-midi-hack.md`.

## Rules that are not optional

- **Pure Go, `CGO_ENABLED=0`.** `make build` cross-compiles in a Go
  container; the Mac has no Go. Bonjour goes through avahi's D-Bus API
  (`godbus/dbus`), never a second mDNS responder next to `avahi-daemon`.
- **No `/dev/snd` before `alsaseq.WaitForBootSettle()`** (USB-A wedge).
- **No display.** The Push-side UI is a `shadow_ui` tab push-manager draws;
  strings in it are ASCII.
- **`src/rtpmidi` has no sockets.** Packets, the MIDI list and the session
  state machine are pure and unit-tested; `peer.go` is the only UDP code.
- **Idle cost near zero.** No polling loops faster than 1 Hz; the ALSA
  client list is read only while a route is unresolved.
- **Nothing under `/etc/`, `/opt/`, `/boot/`.**

## Commands

    make test | make vet | make fmt | make build
    ./scripts/deploy.sh push.local [stop]   # SSH_OPTS="-o HostKeyAlias=push.local" for an IP
