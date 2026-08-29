# Plan

The detailed plan for whatever is currently being implemented. Clear it back
to this header when a plan ships — finished plans belong in the commit
history, not here.

*Nothing in progress.*

---

## Shipped: honest tunnel status (2026-08-29)

`status` reported whether the local port was *bound*, a property of the
listener rather than the tunnel. `jump-neo` pointed at a deleted GCP project,
failed every connection with IAP `code 4033`, carried zero bytes for hours —
and reported `OPEN` throughout, which macswitcher observe rendered as
"13/13 tunnels open".

- [x] `internal/health`: per-tunnel record (last success/failure, consecutive
      failures, last error, bytes), published atomically to a state file.
- [x] `statePath`, defaulting to `~/.local/state/tunneling/health.json`.
      Added to MacbookSetup/CONVENTIONS.md as §2a, which previously covered
      config and logs but said nothing about state.
- [x] Five states from two signals — a live port probe plus the daemon's
      record: `DOWN` / `FAILING` / `IDLE` / `OK` / `UNKNOWN`. Non-zero exit
      only on DOWN or FAILING.
- [x] Classification by *bytes*, not error strings: received > 0 is success,
      sent > 0 with nothing received is failure, and neither is neutral —
      which is what `status`'s own probe looks like, so probing cannot
      manufacture its own answer.
- [x] Success is recorded on the first reply, not at connection close. Caught
      in live testing: a multiplexed SSH session over a gcp tunnel stays open
      for hours, so the gcp tunnels read IDLE while at their busiest.
- [x] `status --json`, and macswitcher's `parseTunnelingStatus` switched onto
      it. It scraped the literal `OPEN` field, which the new vocabulary
      breaks.

Verified live against the real 13-tunnel config: 11 OK, and both halves of the
deleted-project chain FAILING with their reasons. macswitcher observe now
reports "11/13 tunnels ok" and names them.

---

## Shipped: replace elliotchance/sshtunnel (2026-08-28)

Found while reading the live logs: the daemon held **7,996 established
sockets**, 2,468 of them live WebSockets to Google IAP, growing without bound.

`sshtunnel.forward()` dialed a new `ssh.Client` per forwarded connection and
closed none of them until the tunnel shut down — so every kubectl call leaked
an SSH client and the IAP connection behind it, permanently. Pre-existing, but
it only mattered once the tool became a 24/7 LaunchAgent instead of something
restarted with a terminal.

- [x] One multiplexed SSH connection per tunnel, as `ssh -L` does: forwards
      are channels on it, not fresh handshakes. Dialed lazily, because an ssh
      tunnel usually points at a gcp tunnel in the same process.
- [x] Both transports share `pipe`, which closes both sides when either ends
      and returns byte counts (the health signal for the work above).
- [x] Host key verification: `hostKeyChecking: accept-new|strict|off`,
      default `accept-new`. Previously *any* key was accepted and none
      recorded, so every ssh tunnel was interceptable.
- [x] Reconnect: a dead cached connection is re-dialed once rather than
      failing the forward.
- [x] `bindAddress` now applies to ssh tunnels too (the library hard-coded
      localhost).
- [x] Fixed on the way: concurrent tunnels sharing one SSH server recorded the
      host key twice (per-callback lock, not package-level); the agent
      connection was left to the GC; an unrecognized `hostKeyChecking` mode
      fell through to the permissive branch.

Measured: 40 connections through a chained ssh-over-IAP tunnel leave the
socket count flat at 4, connects and disconnects balance 40/40, and one
persistent IAP connection carries all of them. Live: **19 sockets**, down from
7,996.

## Shipped: fatal errors reach the daemon log (2026-08-28)

A fatal error was returned up through cobra to stderr, which launchd captures
into `tunneling.err.log` — the file documented as "crash capture only".
Under `KeepAlive` a persistent failure restarts every 10s forever, and
`tunneling.log`, the file the README tells you to read, showed nothing but
repeating `daemon starting` lines. `daemon.Run` now logs the error too, and
sets logging up before validating the config so a *config* error is logged
rather than being the one thing that cannot be.

## Shipped: modernization to the shared project standard (2026-08-28)

Kept for one release as a record of what changed and why; delete after the
next tag.

- [x] Module path `gihbu.com/schretzi/tunneling` → `github.com/schretzi/tunneling`.
- [x] Flat `package main` → `cmd/` (cobra) + `internal/{config,tunnel,daemon,logfile,service,version}`.
- [x] viper + `charmbracelet/log` → `gopkg.in/yaml.v3` + the standard logger.
      Config *format* unchanged; 30 indirect dependencies → 9.
- [x] `internal/{logfile,service,version}` copied verbatim from
      KerberosKeepAlive; the "duplicated in ..." comment updated in all four
      repos.
- [x] `service install|uninstall|start|stop|restart|status`, `daemon`,
      `status`, `config init|validate`, `version` — the surface
      `MacbookSetup/CONVENTIONS.md` mandates.
- [x] Scaffolding: `Makefile` pipeline, `.golangci.yml`, `lefthook.yml`,
      `.goreleaser.yaml`, GitHub Actions CI + manual release, dependabot,
      `tools/gendocs` → `docs/`.
- [x] Tests for config loading (including the legacy file shape), the tunnel
      lifecycle, and the command surface.
- [x] `etc/newsyslog.d/tunneling.conf` added to MacbookSetup.

Behaviour changes, all deliberate:

- [x] `gcp` listeners bind `127.0.0.1` instead of `0.0.0.0`. Override with
      `bindAddress`.
- [x] A failed IAP dial no longer kills the process — it costs that one
      connection. A failed *listener* still does.
- [x] `kind: ssh` tunnels block for the process's life instead of returning
      after 100ms, so an ssh-only config no longer exits immediately.
- [x] The per-connection IAP `net.Conn` is a local, not a shared struct field
      that every connection overwrote.
- [x] The ssh-agent check runs only when an `ssh` tunnel is configured.
- [x] SIGTERM/SIGINT shut every tunnel down instead of being ignored.
