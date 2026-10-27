# 04: Own complete reservation and Rendezvous operations

**What to build:** Reservation and Rendezvous requests preserve their original session ownership across reading and verification, while protocol adapters handle only bounded framing and response encoding.

**Blocked by:** 03

**Status:** resolved

Claimed by coordinator `/root`; implementation owner: `/root/ticket_04`. Baseline: `b1c098bc16839303956e1a995923d4428e609b8d` on `codex/account-session-ownership-integration`.

- [ ] Move semantic reservation and Rendezvous register/lookup/unregister operations into the session module with the synchronization shared by session and lease ownership.
- [ ] Preserve permission and rate checks before expensive request processing and retain the original request’s ownership association across input reads; later authentication cannot retroactively authorize stale work.
- [ ] Keep signature/address verification outside the state lock, then recheck exact session ownership, reservation validity, expiry, and drain conditions before applying changes.
- [ ] Do not expose mutable session pointers or reusable permission booleans as authority. Protocol adapters must not access lease/index maps or the manager lock.
- [ ] Preserve lease transfer on valid same-connection renewal, stale-owner cleanup, reservation limits, both wire versions, bounded input, extra-input rules, half-close compatibility, and response meanings.
- [ ] Through existing wire operations and controlled barriers, exercise invalidation, replacement, renewal, expiry, and reservation loss during reading or verification; prove stale requests cannot create or delete a replacement owner’s lease.
- [ ] Keep current wire and diagnostic tests passing without assertions on private map layout.

**Spec coverage:** I5, I7, I9; T1, T4–T5.


## Resolution evidence

- Source `6d9483ed8d3bdaddf84abe85e15057f79e7c61a6` on `codex/account-session-ownership-04`; integrated as `fc8db0dbea37da5e0b65621536346371dc467e3f`.
- Ticket branch passed full `go test -count=1 ./internal/relay ./internal/auth`, focused Rendezvous race checks, `go vet ./...`, `go build ./...`, and `git diff --check`. On integrated revision, the focused controlled race suite for replacement/invalidation during verification and stale unregister after renewal passed.
- Reservation and Rendezvous semantics are owned by the session manager; protocol handlers no longer manipulate session/lease maps or its lock.
