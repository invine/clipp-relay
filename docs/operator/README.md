# Isolated managed-relay operator setup

The completed interactive procedure is versioned at
`scripts/operator/google-oauth-test-wizard.sh`. It captures private configuration
under `~/.config/clipp-relay`, asks before cluster mutations, and resumes with
`--start-at 12` or `--start-at 16`. Run `--help` first; do not run it unattended.
Use [the field guide](google-oauth-field-guide.md) for the setup stages and their
history. Old migration and installation evidence must retain its identity.

The test chart repairs formerly embedded in the temporary wizard are preserved
in `scripts/prepare-isolated-test-chart.py`. It copies a reviewed chart to an empty
output directory, requests 300m relay CPU, repairs existing PostgreSQL data
permissions after claim identity/version checks, uses OnRootMismatch fsGroup
handling, and configures both explicit Certificates for in-place HTTP-01 with
temporary TLS. PostgreSQL retains its 400m request. Production/default resource
budgets are unchanged. Preparation alone makes no cluster mutation.

```sh
python3 scripts/test-isolated-test-chart.py
python3 scripts/prepare-isolated-test-chart.py charts/clipp-relay /path/to/empty-test-chart
bash scripts/operator/google-oauth-test-wizard.sh --help
```

The generated application migration Job remains in the wizard and requests 300m.
Both chart StatefulSets use OnDelete, so the wizard verifies Ready replicas,
owner UID and the running Pod revision instead of calling rollout status.
Credentials, real private Helm values, profiles, and diagnostic captures remain
outside source control. Capacity qualification is deferred. Completion of the
wizard or a direct clip transfer does not establish forced relay-only transfer.

For application startup, readiness and drain diagnostics, see the
[service lifecycle row examples](service-logging.md).

Review `accounting.failureDeadline` in the private chart values before rendering
or resuming the wizard. It maps to runtime `accounting_failure_deadline`, defaults
to `"3s"`, accepts whole seconds of at least `"1s"`, and requires relay restart.
The wizard copies that default from the bundled example and preserves edits to
existing private values. It does not add an environment or flag policy override.
The cutoff is independent of blocked persistence: unresolved writes close all
affected sessions at the deadline and temporarily deny admission until recovery.

[Offline accounting recovery](offline-recovery.md) describes the isolated local
reconciliation command, independent journal proof, credential invalidation,
Recovery Review Hold and explicit current administrator review. Its fixture
acceptance does not establish live backup or provider/cluster recovery.

During graceful Relay Drain, already admitted circuits continue to settle usage.
After circuit completion and physical closure, Relay attempts the final accounting
flush using the **remaining original drain context**. There is no additional
accounting retry budget after the drain deadline. A second signal cancels that
context and closes remaining sessions; uncertain/unpersisted observations may be
lost. Quota cleanup cancels workers and bounds its wait at 100 ms even when a
faulty dependency ignores cancellation. Confirmed totals and compact receipts
survive restart; restart never reconstructs lost observations or live sessions.

Supported grant revocation invalidates established Relay Sessions authenticated
with that grant. Account-wide revocation, suspension, denial and deletion
invalidate the account's affected sessions. A successful mutation means the
single-process Session Manager has processed logical invalidation; physical
connection closure is asynchronous, and already-admitted streams or buffered
traffic may finish. There is no recurring grant-status polling. Direct database
edits do not publish notifications, so they do not guarantee immediate session
invalidation. Multiple relay processes or external database writers require a
separate notification design and are outside the supported deployment model.

Offline accounting schema transitions: [observed accounting cutover](accounting-migration.md).

Native OAuth startup, first cutover, signing-key replacement and uncertain
reset recovery: [native OAuth cutover](native-oauth-cutover.md).

For the least-privilege database role table allowlists, see [serving role
privileges](database-privileges.md).
