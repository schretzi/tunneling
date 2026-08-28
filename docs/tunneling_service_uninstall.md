## tunneling service uninstall

Unload the LaunchAgent and remove its plist

### Synopsis

Unload the job and delete its plist. Logs in ~/Library/Logs are left in place.

```
tunneling service uninstall [flags]
```

### Options

```
  -h, --help   help for uninstall
```

### Options inherited from parent commands

```
      --binary string    path to the tunneling executable to run (default: the running one)
      --config string    path to config file (default "~/.config/tunneling/config.yaml")
      --tunnel strings   tunnel name(s) to operate on (default: all configured tunnels)
```

### SEE ALSO

* [tunneling service](tunneling_service.md)	 - Manage the tunneling LaunchAgent

