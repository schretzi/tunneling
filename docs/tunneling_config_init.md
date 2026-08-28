## tunneling config init

Write a starter config file, if none exists yet

### Synopsis

Write a commented starter config to the --config path.

Refuses to overwrite an existing file — editing yours is not this command's
job. Delete or move it first if you really want a fresh one.

```
tunneling config init [flags]
```

### Options

```
  -h, --help   help for init
```

### Options inherited from parent commands

```
      --config string    path to config file (default "~/.config/tunneling/config.yaml")
      --tunnel strings   tunnel name(s) to operate on (default: all configured tunnels)
```

### SEE ALSO

* [tunneling config](tunneling_config.md)	 - Create and check the tunneling config file

