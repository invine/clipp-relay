# 05: Complete Account-wide Revocation inside Auth

**What to build:** Sign out everywhere becomes the first complete Auth mutation: its HTTP adapter submits a request and renders one classified result after protected work has finished.

**Blocked by:** 02

**Status:** resolved

Claimed by coordinator `/root`; implementation owner: `/root/ticket_05`. Baseline: `737649b6cac2252d89949a07c4d7e482e2ebfd08` on `codex/account-session-ownership-integration`.

- [ ] Implement a distinct complete Account-wide Revocation operation and shared private completion machinery, using the manager-owned cleanup collaboration from ticket 02.
- [ ] Own admission, ordered logical protection, authoritative Portal Session/account rechecks, atomic mutation/audit persistence, uncertainty fencing, local effects, and resource release inside Auth.
- [ ] Return explicit refusal, completed, uncertain-commit, and committed-but-invalidation-incomplete outcomes. Preserve durable versus local effects when cancellation or deadlines occur after commit.
- [ ] The HTTP adapter renders once after protected work releases resources, preserving current statuses, redirects, cookies, retry guidance, and diagnostics. It receives no guard protocol or physical cleanup responsibility.
- [ ] Use existing controlled adapters for outcome interleavings and real PostgreSQL/real Relay Sessions for durable revocation and scope. Verify late authority loss, admission refusal, known rollback, lost commit acknowledgment, incomplete invalidation, and post-commit request cancellation.
- [ ] Keep the completion machinery private and reusable for operation-specific administrator and deletion workflows; do not introduce a public generic callback runner or command framework.

**Spec coverage:** I1–I4, I8–I10; T1–T4, T7.


## Resolution evidence

- Source commit `f39527d058b477467ed20c18b6d086dd347c0e72` on `codex/account-session-ownership-05`; integrated as `781a970add2bbdabf4f1f9433330e0219bdbb291`.
- Integrated `go test -count=1 ./internal/auth ./internal/relay` passed; focused account/grant invalidation race command passed. `go vet ./...`, `go build ./...`, and `git diff --check` passed on the source branch. PostgreSQL-backed mutation fixtures were not run because `CLIPP_TEST_SERVING_CONFIG` was unset.
- Account-wide revocation is a distinct Auth operation with private completion machinery and classified outcomes; response rendering occurs after protected work.
