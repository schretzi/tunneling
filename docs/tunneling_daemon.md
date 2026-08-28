## tunneling daemon

Open the configured tunnels and hold them open (invoked by launchd)

### Synopsis

Open every configured tunnel and serve them until interrupted.

This is the process the LaunchAgent runs; it is not how you install or control
that agent. Use `tunneling service` for the launchd job. Running it by
hand in a terminal works too — the log is echoed to stderr when stderr is a
terminal, as well as written to the log file.

The config is read once, at startup: every tunnel holds a bound listener for
the life of the process, so config changes need `tunneling service restart`
(or Ctrl-C and rerun).

A tunnel that cannot bind its port, or whose listener dies, exits the whole
process non-zero rather than leaving a half-open set of tunnels behind;
launchd restarts it. Individual connection failures only affect that
connection.

```
tunneling daemon [flags]
```

### Options

```
  -h, --help   help for daemon
```

### Options inherited from parent commands

```
      --config string    path to config file (default "~/.config/tunneling/config.yaml")
      --tunnel strings   tunnel name(s) to operate on (default: all configured tunnels)
```

### SEE ALSO

* [tunneling](tunneling.md)	 - Open a set of SSH and GCP/IAP tunnels in one step

