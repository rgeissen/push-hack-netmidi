# Net MIDI for Push 3. Pure Go: CGO_ENABLED=0, cross-built for linux/amd64
# in a Go container, so no Go toolchain is needed on the Mac.
#
# An optional go.work (local, not committed) points core at
# ../ableton-push-hack/core; both repos are mounted so it resolves inside
# the container.

IMAGE   ?= golang:1.25
HOST    ?= push.local
DOCKER  := docker run --rm --platform linux/amd64 \
	-v $(CURDIR):/w/push-hack-netmidi \
	-v $(abspath $(CURDIR)/../ableton-push-hack):/w/ableton-push-hack \
	-v push-hack-netmidi-gomod:/go/pkg/mod \
	-w /w/push-hack-netmidi/src -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH=amd64 \
	-e GOFLAGS=-buildvcs=false $(IMAGE)

.PHONY: all build test vet fmt deploy clean

all: build

build:
	$(DOCKER) go build -trimpath -ldflags '-s -w' -o ../netmidi .

test:
	$(DOCKER) go test ./...

vet:
	$(DOCKER) go vet ./...

fmt:
	$(DOCKER) gofmt -l .

deploy: build
	./scripts/deploy.sh $(HOST)

clean:
	rm -f netmidi
