# Backlog

High-level only — ideas, open questions and follow-ups that are not being
worked on yet. The plan for whatever *is* being worked on lives in `PLAN.md`.

## Robustness

- [ ] Per-tunnel restart with backoff, instead of the current all-or-nothing
      exit. Only worth it if the launchd restart proves too coarse in
      practice — the current behaviour is deliberate, not an oversight.
- [ ] `hostKeyChecking: accept-new` refuses a jump host that has been rebuilt,
      because a new key is indistinguishable from interception. If these hosts
      turn out to be recreated often enough for that to be a nuisance, the
      answer is a `tunneling known-hosts forget <tunnel>` helper, not
      switching the default to `off`.

## Portability

- [ ] Linux builds were dropped when `service` (launchd) became central. A
      systemd-user equivalent of `internal/service` would let them come back.
      Windows was dropped outright: the ssh-agent check dials a Unix socket.

- [ ] Health is per daemon run: restarting resets every counter and every
      tunnel goes back to IDLE. Fine for "is it working now", useless for "has
      this been flaky all week". Persisting across restarts means deciding how
      much history is worth keeping.
- [ ] A tunnel that worked hours ago and has been untouched since still reads
      OK. There is no staleness horizon, because there is no way to tell "not
      used" from "quietly broken" without generating traffic.

## Testing

- [ ] `internal/tunnel`'s forwarding paths (~30%) need a fake IAP endpoint and
      a local SSH server to cover. Worth it if a bug ever lands there; the
      decision logic around them is already covered.
- [ ] `internal/daemon` is at 0%. `Run` is signal handling and process
      lifetime, so covering it means driving a subprocess — a different kind
      of test than the rest of the suite.

## Housekeeping

- [ ] `internal/logfile`, `internal/service` and `internal/version` are
      duplicated verbatim across four repos by hand. A shared module would
      remove the drift risk, at the cost of a dependency each repo currently
      does not have. The `bootout`/`bootstrap` race fixed in `Service.Stop`
      had to be applied four times to land; that is the argument.
- [ ] `internal/service/service_test.go` does not cover `Stop`'s wait for the
      unload to complete — it needs a real loaded launchd job, so it would be
      an integration test, not a unit test.
- [ ] `etc/newsyslog.d/tunneling.conf` lives in MacbookSetup. Nothing checks
      that it stays in step with `DefaultLogPath()`.
