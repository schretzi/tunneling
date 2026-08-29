# tunneling

A macOS CLI and background agent that opens a whole set of tunnels in one
step, from a single YAML config. Two kinds, both plain local port forwards:

- **`kind: gcp`** — through Google Cloud Identity-Aware Proxy, straight to a
  Compute Engine instance's network interface. No bastion, no SSH; IAP itself
  is the transport.
- **`kind: ssh`** — through an SSH server, authenticating with the keys held
  by your running `ssh-agent`, with the host key checked against
  `known_hosts`. One multiplexed SSH connection per tunnel, like `ssh -L`.

They compose. Point an `ssh` tunnel's `tunnelHost`/`tunnelPort` at the local
port of a `gcp` tunnel and you reach a private in-cluster address through an
IAP-only jump host, with one command and no `gcloud compute ssh` invocations
to remember.

## Features

- One config, many tunnels, opened together and held open.
- Runs as a macOS LaunchAgent (`service install`), so the tunnels are up at
  login.
- `status` reports whether each tunnel's traffic actually works, not just
  whether a port is bound (see [Tunnel status](#tunnel-status)).
- `config validate` catches a broken config before it becomes a background
  crash-loop — `service install` refuses to install one.
- Listeners bind loopback by default (see [Bind address](#bind-address)).

## Installation

```sh
brew install schretzi/tap/tunneling
```

Or download a prebuilt binary from the [Releases](../../releases) page, or
build from source:

```sh
git clone https://github.com/schretzi/tunneling.git
cd tunneling
make build
```

The last option produces `./tunneling`. Move it wherever you like on your
`PATH`, e.g. `/opt/homebrew/bin`.

Installing only puts the binary in place — it does **not** start anything.
See [Running in the background](#running-in-the-background).

## Prerequisites

| for | you need |
| --- | --- |
| `kind: gcp` | Application default credentials: `gcloud auth application-default login` |
| `kind: ssh` | A running `ssh-agent` holding at least one key (`ssh-add -l`) |

Both are checked at startup rather than on the first connection, so a missing
login fails loudly instead of producing a tunnel that silently refuses
everything.

## Configuration

```sh
tunneling config init       # writes ~/.config/tunneling/config.yaml
$EDITOR ~/.config/tunneling/config.yaml
tunneling config validate
```

The file format is unchanged from earlier versions of this tool: a `tunnels:`
mapping of name to definition, with the same camelCase keys, and ports
accepted either bare (`22`) or quoted (`"22"`). Existing configs load as-is —
everything added since (`bindAddress`, `daemon:`) is optional and defaulted.

```yaml
tunnels:
  jump-dev:
    kind: gcp
    project: my-project-development
    zone: europe-west4-a
    nic: nic0
    remoteHost: development-jumphost   # the *instance name*
    remotePort: 22
    localPort: 10022

  k8s-dev:
    kind: ssh
    tunnelHost: localhost              # the local end of jump-dev
    tunnelPort: 10022
    user: my-login-name
    remoteHost: 10.0.0.1
    remotePort: 443
    localPort: 10443
```

The full annotated example is
[`internal/config/config.example.yaml`](internal/config/config.example.yaml) —
the same file `config init` writes, embedded in the binary so the two cannot
drift apart.

### Bind address

Listeners bind `127.0.0.1` by default. A tunnel is a hole through a perimeter:
on `0.0.0.0` anyone on the same network gets a route to whatever is on the far
end, which for a production jump host is not a trade you want to make by
accident. Set `bindAddress: 0.0.0.0` if you deliberately want to share them.

> Earlier versions bound `gcp` listeners on all interfaces. If you were
> relying on that, set `bindAddress: 0.0.0.0` explicitly.

### Host keys

`ssh` tunnels verify the server's host key against `~/.ssh/known_hosts`.
`hostKeyChecking` mirrors ssh(1)'s `StrictHostKeyChecking`:

| value | behaviour |
| --- | --- |
| `accept-new` | **default** — record a host never seen before, refuse if a *known* host presents a different key |
| `strict` | refuse any host not already in `known_hosts` |
| `off` | accept anything, record nothing |

Chained tunnels all present as `[localhost]:<port>`, so the key recorded under
a port belongs to whichever jump host that port forwards to. Renumbering ports
between jump hosts therefore looks like a changed key, and so does rebuilding
a jump host. Both are refused under `accept-new` — that is the point, since
neither is distinguishable from interception — and both are fixed by removing
the stale line (`ssh-keygen -R "[localhost]:10022"`) or by setting `off`.

> Before this, any host key was accepted and none recorded, so every `ssh`
> tunnel was trivially interceptable by whatever answered on
> `tunnelHost:tunnelPort`.

### Connection model

Each `ssh` tunnel opens **one** SSH connection and carries every forwarded
connection as a channel on it, the way `ssh -L` does. Each `gcp` tunnel opens
one IAP connection per accepted connection, and closes it when that connection
ends.

A chained `ssh`-over-`gcp` tunnel therefore holds exactly one long-lived IAP
connection while it is in use, regardless of how many `kubectl` calls go
through it.

## Usage

```sh
# Open every configured tunnel and hold them open (Ctrl-C to stop)
tunneling daemon

# Just two of them
tunneling daemon --tunnel jump-dev --tunnel k8s-dev

# What is actually working right now
tunneling status
tunneling status --json          # machine-readable

# Config handling
tunneling config init
tunneling config validate

# Manage the LaunchAgent that runs `daemon` at login
tunneling service install
tunneling service uninstall
tunneling service start
tunneling service stop
tunneling service restart
tunneling service status
```

All commands accept `--config <path>` (default
`~/.config/tunneling/config.yaml`) and `--tunnel <name>` (repeatable; defaults
to all configured tunnels, and tab-completes from your config).

Full command reference: [`docs/tunneling.md`](docs/tunneling.md) (generated
from the CLI itself — see [Development](#development)).

### Tunnel status

```
NAME       KIND  LOCAL            STATE    LAST OK  FAILS  DESTINATION
jump-dev   gcp   127.0.0.1:10022  OK       6s ago          development-jumphost:22
k8s-dev    ssh   127.0.0.1:10443  OK       5s ago          10.127.240.13:443
jump-neo   gcp   127.0.0.1:16666  FAILING  never    3      old-instance:22

jump-neo: sent 12 bytes, received nothing back
```

Two independent signals are combined: a loopback TCP connect says whether
something is listening, and the daemon's own record — published to
`statePath` — says whether traffic reached the far end.

| state | meaning |
| --- | --- |
| `DOWN` | nothing is listening on the local port |
| `FAILING` | listening, but the most recent forward failed or got no reply |
| `IDLE` | listening, nothing has used it yet — no evidence either way |
| `OK` | listening, and the destination recently answered |
| `UNKNOWN` | listening, but no live daemon is publishing health for it |

Exits non-zero on `DOWN` or `FAILING`.

**`IDLE` is not `OK`.** A tunnel nobody has used tells you nothing. This
distinction is the whole point: a tunnel pointing at a deleted GCP project
kept its listener bound and reported "open" for hours while failing every
single connection, because a bound port says nothing about the far end.

Health is classified by bytes, not error strings, because errors here are
ambiguous — closing the far end after a client hangs up produces one, and so
does a genuinely dead destination. What is unambiguous is whether the far end
ever answered: data received means the tunnel works, data sent with nothing
received means it does not, and a client that connects and leaves without
sending proves nothing. That last case is exactly what `status`'s own probe
looks like, which is why the probe cannot manufacture its own answer.

### Failure behaviour

A tunnel that cannot bind its port, or whose listener dies, takes the **whole
process** down with a non-zero exit; under the LaunchAgent, launchd restarts
everything after 10 seconds. That is deliberate: a half-open set of tunnels
where three of eight silently stopped forwarding is far worse to debug than a
process that exits loudly. Individual connection failures — a refused dial, a
dropped copy — are logged and cost only that connection.

## Running in the background

Nothing is installed or started automatically. That is an explicit step:

```sh
tunneling service install
```

This validates your config (refusing to install if it's invalid), then writes
a **LaunchAgent** to `~/Library/LaunchAgents/com.schretzi.tunneling.plist` and
loads it with `launchctl bootstrap gui/<uid>`. The plist points at the path of
the binary you ran the command from, and at the `--config` path you passed (so
pass `--config` here if you don't use the default). Re-running the command is
safe — it unloads and reloads, which is also how you apply a plist change.

Symlinks are deliberately not resolved when recording the binary path: a
Homebrew cask keeps the real binary under
`/opt/homebrew/Caskroom/tunneling/<version>/`, and resolving would pin the
plist to a version the next `brew upgrade` deletes.

`service` always means this launchd job. `daemon` is the foreground process
that job runs — the two are never the same word.

From then on `tunneling daemon` starts at every login and is restarted by
launchd if it exits non-zero.

It is a LaunchAgent rather than a system-wide LaunchDaemon on purpose: it needs
your login session's `SSH_AUTH_SOCK` and your gcloud credentials, neither of
which a root daemon can reach.

The config is read once at startup, because every tunnel holds a bound
listener for the life of the process. After editing it:

```sh
tunneling config validate && tunneling service restart
```

To stop and remove the agent:

```sh
tunneling service uninstall
```

`brew uninstall tunneling` also runs this automatically, so the agent is never
left pointing at a deleted binary. `brew uninstall --zap tunneling`
additionally removes your config and logs.

### Logs

State — the per-tunnel health `status` reads — is published to
`~/.local/state/tunneling/health.json`. It is machine-written and safe to
delete; the daemon rewrites it.

Logs live directly in `~/Library/Logs/`, named after the binary:

| File | Contents |
| --- | --- |
| `tunneling.log` | The daemon's own log — this is the one to read. |
| `tunneling.err.log` | Crash capture only: panics, and failures before logging starts. Normally empty. |

```sh
tail -f ~/Library/Logs/tunneling.log
```

When you run `tunneling daemon` by hand, the log is echoed to stderr as well,
so a foreground run shows the tunnels coming up. Under launchd stderr is a
file, so nothing is written twice.

Rotation matters here because a flapping VPN produces a connect/disconnect
pair per tunnel per attempt, for as long as that lasts.

Rotation is handled by macOS's own `newsyslog`, not by the daemon: install
`/etc/newsyslog.d/tunneling.conf` (there is a copy in the MacbookSetup repo)
and `com.apple.newsyslog` will cap the file hourly. The config has no rotation
knobs.

`newsyslog` rotates by *renaming*, and launchd does not reopen a job's log when
that happens — a daemon holding the fd would go on writing into the archive
while the live file stayed empty forever. `internal/logfile` handles this: it
re-stats the path and reopens when the inode it holds is no longer the one
there.

## Development

This project uses a `Makefile` as its local pipeline; see `make help` for the
full target list. The same checks run in CI on every push/PR.

```sh
make test      # go test ./... -race -cover
make lint      # golangci-lint
make security  # gosec + govulncheck
make build     # go build
make docs      # regenerate docs/ from the Cobra command tree
make pipeline  # everything above — run this before pushing
```

`PLAN.md` holds the plan for whatever is currently being implemented;
`BACKLOG.md` holds everything not yet being worked on.

### Git hooks

[lefthook](https://github.com/evilmartians/lefthook) runs
[gitleaks](https://github.com/gitleaks/gitleaks) on staged changes before every
commit, and `make pipeline` before every push. Install the hooks once after
cloning:

```sh
make hooks
```

This repo deals in internal hostnames, GCP project ids and SSH login names —
exactly the sort of thing that ends up in a test fixture by accident.

### CI/CD

Every push/PR to `main` runs `build-and-test`, `lint`, `security`,
`dependencies`, `release-config` and `secrets` — all on `macos-latest`, since
`internal/service` manages a launchd job and its tests exercise the real plist
paths. These mirror `make pipeline`; run that locally before pushing.

Releases are built with [GoReleaser](https://goreleaser.com) and published via
the "Release" GitHub Actions workflow, which is **manual only**
(`workflow_dispatch`) — pushing a tag alone does not publish anything. To cut a
release:

```sh
git tag v0.1.0
git push origin v0.1.0
```

then go to the repo's **Actions → Release → Run workflow**, and select that tag
in the branch/tag dropdown before running it.

## Conventions

The launchd label, log paths, config path and `service` command tree follow
`MacbookSetup/CONVENTIONS.md`, shared with KerberosKeepAlive, OauthMailToken
and macswitcher. `internal/logfile`, `internal/service` and `internal/version`
are duplicated verbatim across those four repos — they are separate modules
with no shared dependency, so the copies are kept in sync by hand.

## AI usage

This project's modernization was done collaboratively with
[Claude Code](https://claude.com/claude-code) (Anthropic's AI coding
assistant), directed and reviewed by the repository owner throughout:
architecture decisions (keeping the existing config format while replacing
viper with a hand-written loader, all-or-nothing failure instead of
per-tunnel retry, loopback-by-default binding) were made jointly after the AI
read the actual library and OS behaviour involved, code was written by the AI
against an explicit, human-approved plan, and the result was verified against
the owner's real config and the real launchd on this machine rather than
assumed to work. Treat commit authorship as human-directed, AI-assisted.
