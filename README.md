# Net MIDI for Push 3

Net MIDI puts the MIDI ports of Ableton Push 3 Standalone on the network.
It speaks Apple MIDI (RTP-MIDI), the network MIDI that is built into iOS,
iPadOS and macOS. An iPad then sees the Push as a network MIDI peer, and an
app such as AUM gets its notes from the Push and sends notes to it.

It is a [Push Hack](https://github.com/federico-pepe/ableton-push-hack)
catalog hack. It needs push-manager (for its tab on the Push's screen and
the link on the web page); it does not draw on the display.

Status: early. Tested on the device against itself and against Apple's
Bonjour resolver. Tested with an iPad: see "Status" below.

## Why not USB

Push and iPad are both USB hosts, and a host only talks to devices. The
Push's main computer has no USB device controller and no Bluetooth, so no
driver can change that. The network can: the Push has Wi-Fi, and drivers
for common USB Ethernet adapters for a wired link.

## What you get

- One MIDI port on the Push, **Net MIDI**. Live lists it as an input and
  as an output, like any other port. What Live sends to it reaches the
  iPad; what the iPad sends comes out of it into Live.
- **Routes** bridge other ports too: Stacks Out to the network, the
  network to Riffs In, and so on. A route can move every message to one
  channel, because the iPad merges every peer into one port.
- **Bonjour**: the Push announces itself as "Push 3" (the name is yours to
  change), and lists the iPads it sees.
- Both sides can start the session: the iPad connects to the Push, or the
  Push connects to the iPad (CONNECT on the Push, or on the web page).
- Peers the Push connected to can be remembered; it connects again after
  a start or a drop.
- A monitor on the web page shows the last messages each way.

## On the iPad

iPadOS has Apple MIDI built in, but an app has to turn the **network
session** on. AUM does not have that switch; a free app such as
**midimittr** does (Network, Session on). The Push then shows up in that
app's directory under its name. Tap it to connect. In AUM, pick **Network
Session 1** as a MIDI input or output.

For the Push to connect by itself, the iPad must allow connections from
anyone (in midimittr: "Who may connect: Anyone").

Clock, Start, Stop and Song Position from the network are kept out
unless **Clock in** is on (the web page, or the CLOCK IN row on the
tab). Live on the Push follows clock on any input with Sync on, and the
Push is meant to stay the master. Clock from the Push to the iPad is
Live's own Sync setting for the Net MIDI output.

Messages from the iPad carry the time they were sent, and the Push plays
them at that time plus a **buffer** (10 ms by default; the web page sets
it). The buffer absorbs Wi-Fi's jitter: raise it if notes stutter or the
page counts many late packets, lower it on a wired link. For tempo, use
Ableton Link (AUM and Live on the Push both have it), not MIDI clock. Live sends
clock to Net MIDI only with **Sync** on for that port (Settings, MIDI).

## On the Push

Open the Shadow UI (Shift + Help, or the Shadow UI's gesture of your Push
Hack release) and pick the **NETMIDI** tab. Rows:

- `SESSION` on or off, the name and the UDP port.
- `PEER` for each connected peer, with its address and latency.
- `FOUND` for each network session Bonjour sees.
- `SAVED` for each remembered peer.
- `ROUTE` for each route, with its channel and whether its port is there.

The jog wheel moves the cursor. The buttons under the screen: **ON/OFF**
(the session), **CONNECT** on a FOUND or SAVED row, **DROP** on a PEER row,
**FORGET** on a SAVED row, **ROUTE ON/OFF** on a ROUTE row.

## The web page

Open it from push-manager's menu bar (Net MIDI), or go to
`http://push.local:<port>/` with the port the catalog assigned. It has the
same settings as the tab plus: the session name and port, connect by
address, the route editor (pick a port from the list, a direction, a
channel) and the monitor.

## API

All on the hack's port. `GET /api/state` is the whole state;
`GET /api/events` streams it (SSE). `POST /api/session` `{name, port,
enabled}` (each optional). `POST /api/connect` `{host, port, name,
remember}`. `POST /api/disconnect` `{ssrc}`. `POST /api/forget` `{host,
port}`. `GET /api/ports` lists ALSA ports. `POST /api/routes` replaces the
route table: `[{client, port, dir: "out"|"in", channel: 0-16, on}]`.
`GET`/`POST /api/shadow` is push-manager's tab contract.

Settings live in `/data/push-hack/data/netmidi/config.json`, outside the
hack folder, so a reinstall keeps them.

## How it works

`src/rtpmidi` is the protocol with no sockets: the session packets
(invite, accept, reject, bye, clock sync, feedback), the RTP-MIDI data
packets and their MIDI list (delta times, running status, SysEx, the
journal skipped), and a state machine for each peer that says what to
send. `peer.go` puts it on two UDP sockets (control port and control +
1). The Push both accepts invitations and sends them.

Bonjour goes through `avahi-daemon`, which the Push runs already, over its
D-Bus API (`godbus/dbus`, pure Go): the hack asks the daemon to announce
`_apple-midi._udp` and to browse for it. The service points at
`push.local`, which avahi owns.

Messages from the network are not played as the packets land. Each one
is due at the sender's stamp, moved by the offset the clock sync
measures, plus the delta times inside the packet, plus the buffer; a
scheduler (`sched.go`) holds it until then. A packet that is already
late goes out at once with its inner spacing kept. The page counts late
packets and the packets the sequence numbers say were lost.

The ALSA side is `core/alsaseq`: one port that reads and writes, plus a
subscription for each "out" route. A read's events go out in one packet,
so a chord stays a chord.

The binary is pure Go (`CGO_ENABLED=0`), 6.6 MB, about 7 MB resident, and
idle at 0% CPU.

## Build, test, deploy

    make test                      # go test in a Go container
    make build                     # ./netmidi for linux/amd64
    ./scripts/deploy.sh push.local # copy and start it on the Push (dev)
    SSH_OPTS="-o HostKeyAlias=push.local" ./scripts/deploy.sh 192.168.0.134

The Mac needs Docker, not Go. A `v*` tag builds a release and updates
`release.json` for Push Hack Catalog.

## Status

- Done: the protocol, both roles, Bonjour, the Net MIDI port, routes, the
  web page, the NETMIDI tab. On the device, a second instance playing the
  iPad connects, syncs clocks, and carries notes both ways; Live connects
  to Net MIDI by itself; Apple's resolver on a Mac sees the Push.
- Not yet checked: an iPad or a Mac as the peer. The protocol follows
  Apple's, but nothing beats the real thing.
- Known: iOS merges every network peer into one port, so two Push sources
  need two channels. Live's own ports are not shown one by one; Net MIDI
  is one port, and routes add more.
