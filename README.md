# Agent Monitor (`amo`)

A read-only terminal dashboard that watches every running AI coding agent on
your machine — Claude Code, Codex, OpenCode, Aider — and shows each session's
live progress as a pillar bar, with its subagents and background jobs nested
beneath. It chimes softly when a session finishes or a background job blocks on
a decision.

> **Linux only.** `amo` reads process state from `/proc`, so it runs on Linux
> (including WSL2). macOS/Windows are not supported.

## Install

Download the latest binary from [Releases](../../releases):

```sh
tar -xzf amo_*_linux_amd64.tar.gz   # or _arm64
chmod +x amo
./amo                                # or move it onto your PATH: sudo mv amo /usr/local/bin/
```

## Build from source

Requires Go 1.26+.

```sh
make build      # produces ./amo
# or
go build -o amo .
```

## Usage

```sh
amo                     # start the dashboard
amo --no-sound          # start without chimes
amo --interval 2s       # change the poll interval (default 1s)
amo --version           # print version and exit
```

Keys: `q` quit · `s` toggle sound · `c` collapse/expand subagent tree · `↑/↓` scroll.

## Releasing

Tag and push; the `release` workflow builds linux amd64/arm64 binaries with
[GoReleaser](https://goreleaser.com) and publishes them to a GitHub Release:

```sh
git tag v0.1.0
git push origin v0.1.0
```
