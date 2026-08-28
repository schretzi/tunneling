## tunneling service

Manage the tunneling LaunchAgent

### Synopsis

Manage the launchd job that runs tunneling in the background.

  label   com.schretzi.tunneling
  plist   ~/Library/LaunchAgents/com.schretzi.tunneling.plist
  log     ~/Library/Logs/tunneling.log
  stderr  ~/Library/Logs/tunneling.err.log

Both logs are rotated by newsyslog, configured in MacbookSetup under
etc/newsyslog.d/tunneling.conf.

### Options

```
      --binary string   path to the tunneling executable to run (default: the running one)
  -h, --help            help for service
```

### Options inherited from parent commands

```
      --config string    path to config file (default "~/.config/tunneling/config.yaml")
      --tunnel strings   tunnel name(s) to operate on (default: all configured tunnels)
```

### SEE ALSO

* [tunneling](tunneling.md)	 - Open a set of SSH and GCP/IAP tunnels in one step
* [tunneling service install](tunneling_service_install.md)	 - Write the LaunchAgent plist and load it
* [tunneling service restart](tunneling_service_restart.md)	 - Unload and reload the LaunchAgent
* [tunneling service start](tunneling_service_start.md)	 - Load the LaunchAgent
* [tunneling service status](tunneling_service_status.md)	 - Show whether the LaunchAgent is installed, loaded and running
* [tunneling service stop](tunneling_service_stop.md)	 - Unload the LaunchAgent
* [tunneling service uninstall](tunneling_service_uninstall.md)	 - Unload the LaunchAgent and remove its plist

