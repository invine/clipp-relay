# 01: Introduce repository scopes through credential validation

**What to build:** Relay Discovery and Relay Authentication validate existing Relay Access Tokens through backend-neutral repository and transaction interfaces, with the production adapter enforcing shared work limits and classified outcomes while existing workflows remain operational.

**Blocked by:** None (can start immediately).

**Status:** resolved

Parent: [approved specification](../spec.md). Decision source: [approved persistence choices](../decisions.md). Coverage: I1–I3, I5–I6, I9–I10; T1, T3–T4, applicable T8.

- [ ] Establish only the repository records, narrow consumer interfaces, admitted operation scope and later transaction scope needed by complete credential validation. The contract module has no PostgreSQL, SQL or Fosite dependencies.
- [ ] Use the new composition for real discovery and relay authentication, including the Fosite access-token read, authoritative account/grant/plan checks and current credential expiry. Do not leave a production bypass beside a test-only repository path.
- [ ] The PostgreSQL adapter reuses the existing serving pool and shared admission budget during expansion. Nested work does not reserve additional slots, and no migrated operation mixes old/new transactions or independent budgets.
- [ ] Preserve admission before any applicable logical guard, physical transaction acquisition after guards, post-wait authoritative rechecks, cancellation and current pending-validation behavior. No transaction remains borrowed while waiting for logical guards.
- [ ] Verify the existing shared-work ceiling, ordinary deadline, pool-acquisition bound, earlier parent cancellation and resource release through observable operation outcomes under saturation and failure.
- [ ] Return backend-neutral invalid-credential, unavailable and transaction outcomes without exposing driver errors or raw pool access through the new seam. Preserve token purpose, Grant ID association and classified temporary cancellation.
- [ ] Use controlled repository adapters for functional outcomes and real PostgreSQL for validation and resource behavior; retain composed discovery/relay-wire checks. Existing issuance, refresh, portal and maintenance workflows remain green through a recorded temporary compatibility inventory.

## Comments

- Published on 2026-10-26 after the user approved the 11-ticket breakdown and its blocking edges. Publication only: no claim, implementation, commit, operator mutation or deployment is authorized by this publication.


## Resolution

Coordinator integrated implementation commit a18068a9b3c51e4d777c2f0b2c93a9a98024b388 on codex/persistence-repositories-integration at 94de969. Verified package tests for persistence and issuer, the focused Auth seam test, full internal/auth tests, go vet on auth/issuer/auth/persistence, go build ./..., and git diff --check. Real disposable PostgreSQL coverage was unavailable because Docker is not available; retained as a final qualification requirement under ticket 11.
