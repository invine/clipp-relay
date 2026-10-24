# 06: Delete accounts without waiting for failed accounting

**What to build:** Stop relay use immediately on deletion, preserve confirmed current-week consumption where possible, and complete deletion even when pending usage cannot be flushed. Late work must never recreate the account or charge its replacement.

**Blocked by:** 03 — Account for live traffic periodically.

**Status:** ready-for-agent

Parent: [Relay, Quota and Auth specification](../spec.md), especially I4, I6 and I7. User stories: 31–34, 37. Breakdown approved 2026-10-24.

- [ ] Deletion invalidates credentials and detaches/closes sessions through Auth's protected workflow. It attempts bounded final settlement/reconciliation of already attributed traffic.
- [ ] A failed or unresolved final flush does not block deletion or leave it pending solely for accounting. Preserve all independent deletion prerequisites, including Recovery Journal proof and database consistency.
- [ ] Settlement and finalization serialize on the old account. Finalization captures confirmed current-week usage and seals the account against subsequent writes atomically; committed usage before that point is included, while later batches are rejected.
- [ ] A lost settlement acknowledgment around finalization cannot cause omitted committed consumption, duplicate retention, reopened accounting state or restored session authority.
- [ ] Retained Quota Usage remains privacy-minimal and current-week only. Re-registration imports it exactly once into a fresh Pending Relay Account without transferring credentials, approval, sessions or older-week consumption.
- [ ] Batches carry immutable old-account identity. They cannot upsert an absent/deleted account or redirect to a new account sharing its External Identity.
- [ ] Remove abandoned process-local accounting state after the deletion boundary. Preserve existing hidden-account/credential cleanup and audit retention; do not add durable Device Identity or traffic history.
- [ ] Composed HTTP/libp2p/PostgreSQL tests cover immediate closure, failed final flush, ambiguous commit before finalization, late writes after finalization, re-registration/import and cleanup. Use controlled interleavings through the established seams.
- [ ] Existing journal-failure and deletion-recovery tests prove that accounting failure tolerance does not waive independent safety requirements. Update deletion/retention documentation and wording as needed.

## Implementation notes

This ticket can proceed independently of configurable health and portal presentation after ticket 03. It owns the deletion-specific transaction ordering and import behavior required by subsequent recovery work. No live account deletion or data purge is authorized by this ticket's creation.
