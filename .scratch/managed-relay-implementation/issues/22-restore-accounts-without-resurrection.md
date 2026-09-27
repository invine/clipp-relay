# 22: Restore accounts without resurrection

**What to build:** Restore to an isolated database without resurrecting deleted accounts, old credentials or unreviewed account access.

**Blocked by:** [17: Enforce retention and pepper rotation](17-enforce-retention-and-pepper-rotation.md); [21: Produce verified recovery points](21-produce-verified-recovery-points.md).

**Status:** ready-for-agent

Repository scope: clipp-relay.
Source: [Accepted specification](../../managed-relay-service/spec.md), I10, I11.

## Contract and scope

Implement an explicit offline, run-specific restore/reconciliation operation, deadline 3h with no automatic failed retry. Validate current operator authority, source eligibility (maximum 30d), repository identity and immutable run/source/target/schema identity. Fence old serving, journal-append and cleanup writers; an uncertain node needs real fencing, not forced Pod deletion as evidence.

Restore to a separate isolated database/volume matching physical PostgreSQL major and metadata; forward-migrate to a supported application revision. Never down-migrate or expose an obsolete image publicly. Verify journal identity, head, coverage floor and every required sequence/hash/event through the committed head, across all pages. An empty list or checksum of remaining objects cannot prove missing acknowledged deletions absent. Reconcile pending/committed deletion intents by old random account ID, never delete a new account merely because it uses the same External Identity.

Invalidate all restored Portal Sessions, login flows/codes, grants, refresh and Access Tokens. Keep required retained-usage peppers, use current admin/provider/DB/trust authority and actual-current retention. Preserve known restrictions and install an independent Recovery Review Hold on every restored account.

Preserve demonstrably recoverable current-week usage and history. Zero only classified-unrecoverable current-week amounts, including matching retained usage; if the affected set is unknown, treat restored scope as unrecoverable. Never reimport that amount or move old-week usage into a new week. Record this restore-only extra allowance idempotently so retry cannot reset it twice after reopening. Missing-key/ordinary runtime error is not authorization to reset quota. Display “Usage reset during recovery” and mark incomplete history rather than inventing totals.

Record immutable step completion and a privacy-safe report. Structural completion plus a new verified baseline precedes deliberate cutover. A freshly authenticated current administrator explicitly reviews each account status/assignment before clearing its hold; merely visiting or editing quota does not clear it. Reviewed accounts may resume while others remain held; new registrations use Pending approval. Report portal availability, ready accounts and outstanding holds separately. Target RPO 15m and RTO 4h includes review, not just DB restore; missing evidence remains closed even if target is missed.

## Acceptance criteria

- [ ] Restore a real backup/WAL chain into a separate isolated target, forward-migrate if needed and verify intended run/source/target/schema identity; source stays intact.
- [ ] Apply all journal pages and acknowledged deletions; fault missing/misordered/hash-conflicting events, wrong repository/floor/head and ambiguous pending intent, all fail closed.
- [ ] Prove deleted old account stays absent while a later fresh account for the same identity survives; invalidate every restored credential and unfinished flow.
- [ ] Show restrictions plus independent review holds, fresh current-admin review, partial reopening and Pending new registration; no quota edit/page visit bypass.
- [ ] Inject every restore boundary and resume the same run; completed steps remain idempotent and classified current-week resets/imports cannot repeat or leak into a new week.
- [ ] Verify missing required peppers do not masquerade as unknown-usage permission; report preserved/zeroed/incomplete usage honestly without identifiers or secrets.
- [ ] Create a new verified baseline before cutover, retain failure evidence, and record timed recoverability/complete review milestones rather than claiming target compliance without proof.

## Demonstration

Back up a consuming account, delete it, re-register the identity and then restore the old point. Reconcile independent journal evidence, demonstrate no resurrection or old credential validity, and explicitly review a surviving held account before relay access resumes.

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

