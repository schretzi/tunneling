## tunneling status

Show each tunnel's state: whether it is listening and whether traffic works

### Synopsis

Report every configured tunnel.

Two independent signals are combined. A loopback TCP connect says whether
something is listening on the local port. The daemon's own record — written to
the state file, one entry per tunnel — says whether traffic through it
actually reached the far end.

  DOWN     nothing is listening on the local port
  FAILING  listening, but the most recent forward failed or got no reply
  IDLE     listening, and nothing has used it yet — no evidence either way
  OK       listening, and the destination recently answered
  UNKNOWN  listening, but no live daemon is publishing health for it

IDLE is not OK. A tunnel nobody has used tells you nothing, and reporting that
as healthy is how a tunnel to a deleted project went unnoticed for hours.

Exits non-zero if any selected tunnel is DOWN or FAILING.

```
tunneling status [flags]
```

### Options

```
  -h, --help   help for status
      --json   emit machine-readable JSON instead of a table
```

### Options inherited from parent commands

```
      --config string    path to config file (default "~/.config/tunneling/config.yaml")
      --tunnel strings   tunnel name(s) to operate on (default: all configured tunnels)
```

### SEE ALSO

* [tunneling](tunneling.md)	 - Open a set of SSH and GCP/IAP tunnels in one step

