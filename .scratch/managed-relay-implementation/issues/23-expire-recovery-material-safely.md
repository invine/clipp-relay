# 23: Expire recovery material safely

**What to build:** Expire old recovery material and deletion evidence without destroying any eligible recovery chain or releasing an uncertain cleanup fence.

**Blocked by:** [16: Delete and re-register safely](16-delete-and-re-register-safely.md); [21: Produce verified recovery points](21-produce-verified-recovery-points.md).

**Status:** ready-for-agent

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I10, I11.

## Contract and scope

Thirty days is the maximum restore eligibility and cleanup-request deadline across required copies, manifests, object versions, incomplete uploads, journal material and encrypted bundles—not a promise of instantaneous provider erasure. Expired sources never regain eligibility. Preserve necessary live/eligible-recovery keys and the advertised seven-day PITR chain including its required full backup/WAL. Infrastructure diagnostic retention remains seven days.

Use separate least-privilege expiry credentials, never backup-writer/serving authority. One nonoverlapping hourly cleanup pass, 30m deadline, bounded custom pages ≤250 objects. Backup expiry uses the chain-aware tool, not arbitrary age-based WAL deletion. Complete every listing page and inspect versions/uploads/copies; report actual removal or overdue provider action, not merely request success.

Conditionally enter a journal maintenance generation, pausing new deletion commits while unrelated traffic/revocation continues. Validate a monotonic recovery cutoff and freeze exact deletion targets. Verify complete retained coverage before advancing floor/checkpoint; never prune evidence needed by any eligible restore. Resume interrupted work with generation checks and the same target set. A timeout or dead client does not prove worker termination or safe fence release; reconcile/reap uncertain work before release. Late requests cannot broaden targets or resurrect eligibility.

Protect access/key material still needed for live usage and eligible recovery. Warn if a successful pass is overdue by 1h, escalate at 6h and explicitly report retention breaches. Disk pressure never justifies raw WAL removal, blanket journal deletion, automatic repository growth or silent expiry of accepted deletion work.

## Acceptance criteria

- [ ] Inventory all required copies/manifests/versions/uploads/bundles with complete pagination; enforce eligibility cutoff independently of physical provider erasure.
- [ ] Expire an isolated repository with a chain-aware tool and prove every remaining advertised restore point has its base/WAL and required journal/key coverage.
- [ ] Prove backup writer/serving cannot expire or bulk-read protected recovery data, and expiry identity cannot broaden its authority to live account mutation.
- [ ] Race deletion append against cleanup: conditional generations serialize commits, unrelated traffic continues and target/cutoff stays immutable.
- [ ] Interrupt before/after head/floor changes and object deletions; retry reconciles safely, timeout never releases an uncertain generation, and late workers cannot widen targets.
- [ ] Retain required live/eligible-recovery keys, reject attempts to delete needed journal coverage or unarchived WAL, and never make expired sources eligible again.
- [ ] Enforce one pass, page/deadline/resource bounds and emit bounded overdue/retention signals with actual provider status; test warning/escalation thresholds.

## Demonstration

Build two isolated recovery chains around a cutoff, append deletion evidence and interrupt cleanup mid-pass. Resume safely, restore the eligible chain and show the expired chain remains ineligible even if a provider copy has not yet disappeared.

## Standing constraints and completion evidence

This is an implementation slice, not a reopened Wayfinder decision. Keep one
serving process, stock Circuit Relay v2, separate account/device identity and
the accepted privacy/limits; introduce no authentication bypass or durable device
registry. Ship relevant safety controls and tests with the behavior, not in a
later hardening phase. Earlier slices are isolated development increments, not
permission for public serving before all release gates.

Test externally observable behavior through HTTP/libp2p or the complete
Account/Quota/Relay interfaces; SQL correctness uses real PostgreSQL, client
orchestration uses the shared runtime seam plus runtime checks where applicable.
Run relevant repository checks and record commands, exact versions/configuration
and passed/failed/not-run evidence. Missing required evidence prevents completion.
Preserve unrelated work. This ticket grants no production mutation, external
provisioning, publication or load generation against an unapproved target.

## Comments

- Approved breakdown published on 2026-09-27. Implementation not started.

