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

- 02 integrated/resolved: source `45c6e905b52cf195af3f2a0a03a16587d62e1004`, integrated `52c308f85291d63089a4b47858697cec0c446cd9`; source and integrated PG18 settlement/composition checks passed. Artifacts `clipp-periodic-02-{handoff,integration}.md`.
- 03 claimed by periodic-03 after 02 resolution. Approved I3/I5: shared account-week observations, bounded periodic settlement, no DB work in reporter, separate in-flight payload, soft allowance/full overrun and independent default deadline. No serving cutover until 08.
- Baseline separate race profile passed at planning revision; final integrated profiles still required.

- 03 integrated/resolved: source `cd9dc6a6223f6cd5b6f531db1d911d0b9095312f`, integrated `a53594a869be7cea30a9ddbd4451b0101f4b2fcd`; source PG/race/Relay checks and integrated PG checks passed. Artifacts `clipp-periodic-03-{handoff,integration}.md`.
- 04 claimed by health-04 and 05 by weekly-05, at the two-implementer bound. 04 implements accepted I5 configurable monotonic health/immediate protected policy without erasing usage. 05 implements accepted I3/I4/I8 weekly attribution/retention and authority-free confirmed query models. Both preserve physical admission and no serving activation before08.

- 05 source `8c45148d62236e587c21b0fde3f2907056eae409` merged at `1742988e6fcb19faf5edf50d1b7cb34933dcf3fc`; integration verification in progress. Implementer work complete; 04 incorporates combined invalidation/week gate.
- 06 claimed by deletion-06 (03 resolved, implementation slot free). Approved I4/I7: immediate deletion invalidation, bounded best-effort final flush outside logical protection, account-row serialized durable capture/seal, privacy-minimal exact-once retained import; independent journal prerequisites remain mandatory.

- 05 resolved after integrated PG18 periodic/presentation/retention passed; focused review no issues. Artifacts `clipp-periodic-05-{handoff,integration}.md`. Full final fresh DB acceptance remains08.
