## tunneling

Open a set of SSH and GCP/IAP tunnels in one step

### Synopsis

Open every tunnel in ~/.config/tunneling/config.yaml at once.

Two kinds of tunnel, both a local port forward:

  gcp   through Google Cloud Identity-Aware Proxy, direct to a Compute Engine
        instance — no bastion, no SSH
  ssh   through an SSH server, authenticating with the running ssh-agent

They compose: point a "ssh" tunnel's tunnelHost/tunnelPort at the local port
of a "gcp" tunnel to reach a private service behind an IAP-only jump host.

### Options

```
      --config string    path to config file (default "~/.config/tunneling/config.yaml")
  -h, --help             help for tunneling
      --tunnel strings   tunnel name(s) to operate on (default: all configured tunnels)
```

### SEE ALSO

* [tunneling config](tunneling_config.md)	 - Create and check the tunneling config file
* [tunneling daemon](tunneling_daemon.md)	 - Open the configured tunnels and hold them open (invoked by launchd)
* [tunneling service](tunneling_service.md)	 - Manage the tunneling LaunchAgent
* [tunneling status](tunneling_status.md)	 - Show each tunnel's state: whether it is listening and whether traffic works
* [tunneling version](tunneling_version.md)	 - Print the tunneling version, build info and licence

