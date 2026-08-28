## tunneling status

Show the configured tunnels and whether each local port is open

### Synopsis

List every configured tunnel and probe its local port.

The probe is a loopback TCP connect, so it reports whether *something* is
listening on that port — normally this tool's daemon, but a stale process or
an unrelated service holding the port looks the same. Use `service status`
to check whether the LaunchAgent itself is running.

Exits non-zero if any selected tunnel's port is closed.

```
tunneling status [flags]
```

### Options

```
  -h, --help   help for status
```

### Options inherited from parent commands

```
      --config string    path to config file (default "~/.config/tunneling/config.yaml")
      --tunnel strings   tunnel name(s) to operate on (default: all configured tunnels)
```

### SEE ALSO

* [tunneling](tunneling.md)	 - Open a set of SSH and GCP/IAP tunnels in one step

