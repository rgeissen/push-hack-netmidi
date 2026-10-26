module github.com/rgeissen/push-hack-netmidi

go 1.25.0

require (
	github.com/federico-pepe/ableton-push-hack/core v0.5.0
	github.com/godbus/dbus/v5 v5.1.0
)

// TEMPORARY, until core/alsaseq's Unsubscribe (fork branch
// feat/alsaseq-unsubscribe) is merged upstream and tagged: core from that
// commit on the fork. Replace with the new core/vX.Y.Z tag and delete this
// (framework rule: a tag, no replace).
replace github.com/federico-pepe/ableton-push-hack/core => github.com/rgeissen/ableton-push-hack/core v0.0.0-20261026172720-71665abc560a
