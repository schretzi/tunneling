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
