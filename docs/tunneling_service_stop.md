## tunneling service stop

Unload the LaunchAgent

### Synopsis

Unload the job.

This is a real stop, not a kill: the plist uses KeepAlive/SuccessfulExit so
launchd does not immediately restart it. The job comes back at next login, or
on `service start`.

```
tunneling service stop [flags]
```

### Options

```
  -h, --help   help for stop
```

### Options inherited from parent commands

```
      --binary string    path to the tunneling executable to run (default: the running one)
      --config string    path to config file (default "~/.config/tunneling/config.yaml")
      --tunnel strings   tunnel name(s) to operate on (default: all configured tunnels)
```

### SEE ALSO

* [tunneling service](tunneling_service.md)	 - Manage the tunneling LaunchAgent

