# Plain make functions and GOFLAGS keep the recipes free of shell syntax, so
# they run under cmd.exe as well as sh, and a tag name never reaches a shell.
# .git marks this directory as a repo root, so source inside another repository
# does not read that repository's tags. --long keeps a build on a tag reading
# as a commit, never as that release.
VERSION := $(if $(wildcard .git),$(shell git describe --tags --always --dirty --long))
VERSION := $(or $(VERSION),dev)
# override keeps the stamp when GOFLAGS is set on the make command line; it goes
# first so a caller's own -ldflags, which Go applies last, still wins. go env
# supplies flags stored with go env -w when GOFLAGS is not set.
override GOFLAGS := -ldflags=-X=main.version=$(VERSION) $(or $(GOFLAGS),$(shell go env GOFLAGS))
export GOFLAGS

.PHONY: build install

build:
	go build -o agent-manager$(shell go env GOEXE) .

install:
	go install .
