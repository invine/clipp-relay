# 08: Recover accounts and reset credentials through repositories

**What to build:** Operators run offline account recovery, issuer-key verification and explicit credential reset through persistence interfaces without changing recovery authority, idempotency or maintenance privileges.

**Blocked by:** 07: Delete Relay Accounts without losing retention guarantees.

**Status:** claimed — persistence 08 (coordinator: root), 2026-10-26

Parent: [approved specification](../spec.md). Decision source: [approved persistence choices](../decisions.md). Coverage: I1–I5, I8–I10; T2–T3, T6–T8.

- [ ] Migrate recovery-run identity/completion, restored account changes, issuer-state verification and explicit reset persistence into their repository/maintenance owners; preserve legacy credential cleanup as maintenance-only adapter behavior.
- [ ] Recovery retains independent journal qualification, deletion reconciliation, credential invalidation and Recovery Review Hold. Existing key matching and maximum-confirmed-retention behavior remain unchanged.
- [ ] Recovery run/source/target identity makes confirmed retries idempotent. An old restored account UUID cannot resurrect deleted authority or charge a fresh registration.
- [ ] Explicit reset atomically removes applicable credentials, records the signing-key identity and required audit, and remains a no-op after a confirmed same-key reset. Serving startup verifies state and never resets credentials implicitly.
- [ ] Preserve migration/serving/maintenance role separation, least privilege and the current effective startup, reset, recovery and independent confirmation budgets; the longer command envelope must not silently extend ordinary recovery SQL work.
- [ ] Classify rollback versus unresolved commit and verify outcomes before reporting completion, with no blind retries that erase credentials issued after a completed reset.
- [ ] Verify recovery replay, journal/commit faults, old-account isolation, key mismatch, reset rollback/idempotency and role/deadline behavior against real PostgreSQL. Publication of this ticket authorizes no actual operator command or credential reset.

## Comments

- Claimed for implementation after ticket 07 was integrated.
