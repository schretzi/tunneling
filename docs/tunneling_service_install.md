## tunneling service install

Write the LaunchAgent plist and load it

### Synopsis

Write ~/Library/LaunchAgents/com.schretzi.tunneling.plist and load it.

Idempotent: an already-loaded job is unloaded and reloaded, so this is also
how you apply a change to the plist.

```
tunneling service install [flags]
```

### Options

```
  -h, --help   help for install
```

### Options inherited from parent commands

```
      --binary string    path to the tunneling executable to run (default: the running one)
      --config string    path to config file (default "~/.config/tunneling/config.yaml")
      --tunnel strings   tunnel name(s) to operate on (default: all configured tunnels)
```

### SEE ALSO

* [tunneling service](tunneling_service.md)	 - Manage the tunneling LaunchAgent

