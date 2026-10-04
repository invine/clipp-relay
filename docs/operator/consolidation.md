# Local main consolidation — 2026-10-04

Both primary project directories now own the integrated local `main` branch:

- Clients: `/Users/invine/src/js/clipp`.
- Relay, charts and canonical tickets: `/Users/invine/src/go/clipp-relay`.

The client integration includes the managed relay adapters, live authentication,
listener reservation, pairing/QR and Signed Peer Record fixes, privacy filtering,
and peer-bound reconnect targets. The relay includes the consent callback CSP,
cluster chart, image pipeline and optional bundled PostgreSQL work. The completed
operator wizard and isolated test chart preparation are versioned under
`scripts/`; their private answers, credentials and deployment receipts stay outside
Git. See [operator setup](README.md).

The operator reported successful connections and clip transfers on Android,
Electron and Chrome extension. Direct paths formed quickly; forced relay-only
transfer was not tested. Tickets 13–15 retain their remaining acceptance gates,
and capacity ticket 33 remains deferred. The observability design remains a draft.

Original dirty worktrees were saved as binary-safe patches, copied untracked
files, verified Git bundles and recoverable stashes under each project's ignored
`.local/managed-relay/recovery/main-20261004/` directory. Older research/prototype
branches are retained unchanged in Git and in those bundles. Their superseded
runtime experiments were not reapplied over the integrated implementation. The
retired Go integration worktree's mass deletions are preserved for recovery; the
required source files remain intact in main. Extra worktree registrations are
removed only after preserving their ignored files and validating branch inclusion.

## Standards

Parallel source review found no documented-standard violations. Three optional
judgment calls remain: shared registration encoding in `managedRelayHost.ts`,
shared verified-envelope decoding in `peerRecords.ts`, and named patch functions
in the small isolated chart helper. No blocking finding was reported.

## Spec

Parallel review found no actionable specification mismatch. Reconnect identity
binding and managed lifecycle behavior are both retained. The chart test profile
keeps production defaults unchanged and guards PostgreSQL claim ownership before
repairing permissions. Operator confirmations and private credential handling
remain in the wizard.

Verification: client `npm run check` and Electron/Android/extension production
builds; relay `go test ./...`, `go vet ./...`, `go build ./...`; chart suite and
three isolated-profile regression tests; wizard Bash syntax. These checks do not
constitute new live PostgreSQL, OCI-provider or forced-relay acceptance evidence.
Consolidation performs no push, publication, deployment or cluster mutation.
