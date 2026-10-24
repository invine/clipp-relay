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

- 04 integrated/resolved: source `ed79b585392eb74a9f89a692d1972c2f759909ef`, integrated `c426d55d7a14e7071c1df13741bfa8b33b438dd9`; combined05 source race/checks and integrated PG/config checks passed, focused review no issues. Artifacts `clipp-periodic-04-{handoff,integration}.md`.
- Recovery exploration found baseline has no restore command or Recovery Review Hold (prior managed-relay ticket22 is still unimplemented). 07 must implement the local safeguards explicitly required by this parent spec, without claiming physical backup/provider/cluster qualification or silently marking absent protections preserved. The accepted recovery/privacy requirements remain unchanged.

- 06 integrated/resolved: source `ae7318fb5cd68b51bc53bfb932151e1dacaf8313`, integrated `c6c8eb45f296df1382c906c91f6703d5ddf43090`; source focused PG/race and integrated isolated deletion checks passed, focused review no issues. Artifacts `clipp-periodic-06-{handoff,integration}.md`.
- 07 claimed by lifecycle-07 after06 resolution. Approved I4/I7: bounded drain/final flush/restart plus concrete local offline recovery safeguards absent from baseline, preserving journal proof, old-UUID deletion, credentials/holds and confirmed totals. Physical backup/WAL/provider/cluster orchestration remains outside acceptance claims; no accepted security invariant is relaxed.

- 07 integrated/resolved: source `0f604c74424194b5748b8d530706b06c8c455032`, correction `253461f9d1d796a1b164ea6ab91ac99fe592a30b`, integrated `a41456811cd8968c4495c7916bde1359c09f9abf`; initial forced-drain failure reproduced/investigated, corrected exact-transport/circuit assertions plus pending-close ownership, original mixed/race reruns passed. Evidence `clipp-periodic-07-{handoff,integration}.md`.
- 08 claimed by cutover-08 after04/05/07 resolution. Approved I8: sole observed serving composition, explicit additive offline reset migration (original checksums preserved), fresh/upgrade/rollback/rerun/restart compatibility, coherent vocabulary/docs and final routine/race/fresh PG acceptance. No deployment or live reset authorized. Independent branch-wide Standards/Spec review follows implementation.

## Final branch review

- 08 implementation source `7255351462bca52a38ebfec0126bb7aaeb85f6a3` integrated `f7090a276bd668df24da0dba5eff596ac5c4fc90`, exact source tree `24161ad21e37f5e427e9e3b7e6318ea74b12feaa`. Source routine/race/full fresh PG17/18 smoke passed; integrated focused PG/compile/exact schema13 checks passed. Evidence `clipp-periodic-08-{handoff,integration}.md` and final profile logs. 08 remains claimed pending review fixes/final evidence.
- Independent Standards review at `f7090a2`: one P2 test-quality finding (hold assertion reuses already-invalidated authorization code). Independent Spec review: one P1 new-week eligibility gap (successful writes can refresh health indefinitely while current-week eligibility fails for an existing circuit). Review artifacts `clipp-periodic-{standards,spec}-review.md`.
- Review-fix claim assigned to review-fixes implementer, single isolated branch/worktree for both findings. Preserve original accepted spec; prove new-week failure through existing circuit, repair fresh held-portal issuance assertion, and renew affected checks/reviews.
