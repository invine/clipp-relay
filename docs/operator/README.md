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
