#!/usr/bin/env bash
# scripts/deploy.sh [host] [stop] -- dev deploy: copy the binary and
# hack.json to the Push and (re)start it the way push-catalog's init.d
# script does (nice 19, -config hack.json). No service is registered: a
# catalog install does that.
#
#   ./scripts/deploy.sh                 # push.local
#   ./scripts/deploy.sh 192.168.0.134
#   ./scripts/deploy.sh push.local stop
#   SSH_OPTS="-o HostKeyAlias=push.local" ./scripts/deploy.sh 192.168.0.134
#
# When Net MIDI was installed from Push Hack Catalog, its init.d service
# owns it: then only the binary is replaced, through the service, and the
# installed hack.json (with the port the catalog assigned) is kept.
set -euo pipefail

HOST="${1:-push.local}"
ACTION="${2:-start}"
ID=netmidi
DIR=/data/push-hack/hacks/$ID
LOG=/data/push-hack/logs/$ID.log
PIDF=/tmp/push-hack-$ID.pid
read -r -a XOPTS <<< "${SSH_OPTS:-}"
SSH=(ssh -n -o BatchMode=yes -o ConnectTimeout=10 ${XOPTS[@]+"${XOPTS[@]}"} "ableton@${HOST}")
ROOTSSH=(ssh -n -o BatchMode=yes -o ConnectTimeout=10 ${XOPTS[@]+"${XOPTS[@]}"} "root@${HOST}")
ROOTPIPE=(ssh -o BatchMode=yes -o ConnectTimeout=10 ${XOPTS[@]+"${XOPTS[@]}"} "root@${HOST}")
SERVICE=/etc/init.d/push-hack-$ID
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

stop_remote() {
    # By pid file, never by name: `pkill -f netmidi` matches the ssh shell too.
    "${SSH[@]}" "if [ -f $PIDF ]; then kill \$(cat $PIDF) 2>/dev/null || true; rm -f $PIDF; fi; \
        for i in 1 2 3 4 5 6 7 8 9 10; do pidof $ID >/dev/null || break; sleep 0.3; done"
}

[[ "${ACTION}" == "stop" || -x "${ROOT}/$ID" ]] || { echo "no ./$ID binary: run make build" >&2; exit 1; }

if "${ROOTSSH[@]}" "test -x $SERVICE"; then # installed from the catalog
    if [[ "${ACTION}" == "stop" ]]; then
        "${ROOTSSH[@]}" "$SERVICE stop"
        exit 0
    fi
    scp -q -o BatchMode=yes ${XOPTS[@]+"${XOPTS[@]}"} "${ROOT}/$ID" "root@${HOST}:${DIR}/$ID.new"
    "${ROOTSSH[@]}" "cat $DIR/hack.json" | python3 -c '
import json, sys
installed = json.load(sys.stdin)
new = json.load(open(sys.argv[1]))
new["port"] = installed.get("port", new.get("port"))
print(json.dumps(new, indent=2))' "${ROOT}/hack.json" | "${ROOTPIPE[@]}" "cat > $DIR/hack.json.new && mv $DIR/hack.json.new $DIR/hack.json"
    "${ROOTSSH[@]}" "$SERVICE stop; for i in \$(seq 50); do pidof $ID >/dev/null || break; sleep 0.1; done; \
        mv $DIR/$ID.new $DIR/$ID && $SERVICE start"
    echo "replaced the catalog install's binary on ${HOST}: log ${LOG}"
    exit 0
fi

if [[ "${ACTION}" == "stop" ]]; then
    stop_remote
    echo "stopped"
    exit 0
fi

stop_remote
"${SSH[@]}" "mkdir -p $DIR /data/push-hack/logs"
scp -q -o BatchMode=yes ${XOPTS[@]+"${XOPTS[@]}"} "${ROOT}/$ID" "${ROOT}/hack.json" "ableton@${HOST}:${DIR}/"
# `cd ... ; cmd &`, not `cd ... && cmd &`: the latter backgrounds a subshell,
# which holds ssh's stdout open and whose pid is not the binary's.
"${SSH[@]}" "cd $DIR; nohup nice -n 19 ./$ID -config $DIR/hack.json >>$LOG 2>&1 </dev/null & echo \$! > $PIDF"
echo "started on ${HOST}: log ${LOG}, page http://${HOST}:$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["port"])' "${ROOT}/hack.json")/"
