# Periodic accounting implementation run — 2026-10-24

Coordinator: root. Canonical tickets: `issues/`; parent: `spec.md`.
Repository role: relay, original baseline `cca0bb2`. Integration branch: `codex/periodic-relay-accounting`.
Local root: explicitly selected clipp-relay checkout; integration and disposable ticket worktrees are under the local temporary directory. Pre-existing primary dirty glossary/spec/thoughts, seven worktrees and two stashes are preserved. The approved glossary and specification inputs are copied into this branch; the primary checkout is unchanged.

Authorization: user requested all eight tickets with implement-spec on 2026-10-24. Local implementation/commits/verification only; no push, deployment, live data reset or publication. Maximum two active implementers. The accepted spec supersedes prepaid semantics and preserves exact-connection admission, one-session-per-Peer-ID, immediate invalidation, privacy and recovery. Confirmed test seams are spec Testing Decisions 1–3. Routine, race and required local real PostgreSQL checks will be recorded independently of native/live acceptance.

## Claims and integration

- 01 claimed by protected-01; baseline is this planning commit. Preserves admission/security invariants while completing Auth/Relay ownership; see spec I1/I2/I6 and ticket 01.

- 01 integrated/resolved: source `be582981253064f7c8e14aea52b75889e53cb243`, integrated `e6926559c6c710bfcdf0b24723aafb1572b9dc34`; focused source/integration PG18 checks and race passed. Evidence artifacts `clipp-periodic-01-handoff.md`, `clipp-periodic-01-integration.md` under local temporary artifacts.
- 02 claimed by settlement-02 after 01 resolution. Decision: observed totals and immutable retry receipts implement spec I3/I4; original migrations remain checksummed and unchanged; staged accounting is not activated until 08.
- Baseline routine and real PG18 Quota tests passed at planning revision `9e13ae79d9dd85e487195bfcdc2a6d769faa6ede`; full final profiles remain required.
