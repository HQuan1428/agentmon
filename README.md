# Agent Monitor (`amo`)

A read-only terminal dashboard that watches every running AI coding agent on
your machine — Claude Code, Codex, OpenCode, Aider — and shows each session's
live progress as a pillar bar, with its subagents and background jobs nested
beneath. It chimes softly when a session finishes or a background job blocks on
a decision.

> **Linux only.** `amo` reads process state from `/proc`, so it runs on Linux
> (including WSL2). macOS/Windows are not supported.

> **Claude Code: latest version only.** `amo` parses Claude Code's current
> session/transcript format. Older Claude Code versions may not be detected —
> keep Claude Code updated for accurate readings.

## Install

Quick install (Linux, amd64/arm64) — downloads the latest release into
`~/.local/bin`:

```sh
curl -fsSL https://raw.githubusercontent.com/HQuan1428/agentmon/main/install.sh | sh
```

Pick a different directory with `BIN_DIR` (use `sudo` if it needs root):

```sh
curl -fsSL https://raw.githubusercontent.com/HQuan1428/agentmon/main/install.sh | BIN_DIR=/usr/local/bin sh
```

Or download a tarball manually from [Releases](../../releases):

```sh
tar -xzf amo_linux_amd64.tar.gz     # or _arm64
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
