package config

import _ "embed"

// Example is the commented starter config, written by `tunneling config init`
// and shipped in the release archive.
//
// It lives next to the code that parses it, and is embedded rather than
// copied, so `config init` and the file a user reads in the repo can never
// drift apart — a stale example that no longer parses is exactly the kind of
// thing nobody notices until someone follows it.
//
//go:embed config.example.yaml
var Example []byte
