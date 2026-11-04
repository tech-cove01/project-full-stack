# CAP-10 finished-fixture identity audit

## Problem and boundary

Normal fixture finish logs out the exact owned sessions and removes its private
credential input, while retaining the SQL fixture and original manifest. The
historical H1 retirement proof needs another complete reconciliation for a new
successor application, but the ordinary loader requires that removed input even
though reconciliation consumes only manifest user/note identities.

This ticket adds only the explicit read-only command:

```text
node scripts/cap10-like.mjs reconcile --finished-fixture-sha256 <original-manifest-SHA256>
```

`CAP_LIKE_FIXTURE` still points to the original manifest. Its raw bytes must
match the supplied SHA, its READY identity/pool arrays must be complete and
unique, and its original credential path must actually be absent. A dangling
symlink is an existing path, not absence. The same directory must contain a
regular, non-symlink `finish.json` with the same fixture SHA, integer
`loggedOut == poolSize`, `credentialFileRemoved: true` and
`sqlFixtureRetained: true`. The manifest/receipt must resolve within the owned
evidence namespace.

The closed branch returns no runtime users or token placeholders. It runs the
existing complete SQL/Redis reconciliation, using the same read-only RR
snapshot, source/hash/receipt/fact checks and original full-audit budget (120s
in explicit vector mode, 30s in legacy mode). It does not create a session,
restore an input file, validate an old token, normalize facts or modify a cache.
An invalid full audit still fails.

All ordinary invocations keep the strict credential loader. The new flag is
rejected for prepare, inspect, probe, run and finish before any action executes.
This is an identity-only historical audit, not resurrection of the old fixture
as a workload or admission of the new 1996-relation cohort.

## Result evidence

The original reconciliation schema and verdict remain intact. A closed audit
adds `finishedFixtureAudit`, with contract `finished-fixture-read-only-v1`, the
original manifest SHA, relative finish-receipt path and raw SHA, exact logout
count, credential absence and SQL-retained facts, and explicit
`tokenAuthenticationAttempted: false` / `capacityEligible: false`.
Retirement must still pass the existing public proof validator, preserve the
old failure and bind the correct original 512 scope. This marker alone proves
neither a successful reconciliation nor a capacity result.

## Direct checks

- 18 actual public-CLI checks with synthetic files and transport-only command,
  health and Redis fakes: correct closed audit, unchanged 120s policy, default
  loader rejection, five other actions rejected, manifest/finish/count/type
  mismatches, existing private input, resealed duplicate identities, real
  dangling-input and receipt symlinks, and invalid full facts remaining failed.
- 39 existing Node contract/loader checks pass. No original source is rewritten
  or evaluated in a VM by the new CLI checks; the production module executes
  normally. No business database, Docker daemon, network service, cloud or
  capacity window is used.
- The first restricted run could not create the Windows symlink fixture
  (`EPERM`). The same offline check passed with temporary workspace symlinks
  outside that restriction; no production check was weakened or skipped.

Actual historical SQL/Redis reconciliation after the next boot is still
required. These direct checks are not that live result.
