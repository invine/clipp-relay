# Approved relay logging implementation graph

Status: locally implemented; all executable tickets resolved

The human approved this eight-slice breakdown on 2026-10-11. It fulfills the
ticket-refinement gate in the [specification](spec.md). The parent specification
remains the normative source; the [decision map](map.md) records the completed
implementation. Its earlier preliminary-outline note is superseded by this graph.

## Canonical inputs

- [Relay service logging specification](spec.md)
- [Accepted structured logging contract](logging-contract.md)
- [Fixed event catalog](logging-catalog.json)
- [Current source leads and visibility limits](source-inventory.md)

Implementation, tickets, claims and evidence pointers now live in this
`clipp-relay` repository. They were coordinated in Clipp during the 2026-10-11
implementation run; [the run record](run.md) preserves that provenance.

## Executable tickets

| Ticket | Blocked by |
| --- | --- |
| [Log startup, readiness and bounded shutdown](issues/03-implement-signal-foundations.md) | None |
| [Log portal sign-in and OAuth credentials](issues/04-instrument-portal-and-oauth.md) | [Log startup, readiness and bounded shutdown](issues/03-implement-signal-foundations.md) |
| [Log Relay Authentication and Relay Session lifetimes](issues/05-instrument-relay-journeys.md) | [Log startup, readiness and bounded shutdown](issues/03-implement-signal-foundations.md) |
| [Log owner and administrator account actions](issues/07-log-owner-and-administrator-actions.md) | [Log startup, readiness and bounded shutdown](issues/03-implement-signal-foundations.md) |
| [Log reservations, circuits and Rendezvous](issues/08-log-reservations-circuits-and-rendezvous.md) | [Log Relay Authentication and Relay Session lifetimes](issues/05-instrument-relay-journeys.md) |
| [Log quota and accounting failure outcomes](issues/09-log-quota-and-accounting-outcomes.md) | [Log Relay Authentication and Relay Session lifetimes](issues/05-instrument-relay-journeys.md) |
| [Log dependency and maintenance state transitions](issues/10-log-dependency-and-maintenance-transitions.md) | [Log startup, readiness and bounded shutdown](issues/03-implement-signal-foundations.md) |
| [Qualify integrated logging and publish the operator guide](issues/11-qualify-integrated-logging-and-operator-guide.md) | [Log portal sign-in and OAuth credentials](issues/04-instrument-portal-and-oauth.md); [Log owner and administrator account actions](issues/07-log-owner-and-administrator-actions.md); [Log reservations, circuits and Rendezvous](issues/08-log-reservations-circuits-and-rendezvous.md); [Log quota and accounting failure outcomes](issues/09-log-quota-and-accounting-outcomes.md); [Log dependency and maintenance state transitions](issues/10-log-dependency-and-maintenance-transitions.md) |

Begin with **Log startup, readiness and bounded shutdown**. After that slice,
portal/OAuth, owner/admin, Relay Sessions and dependency/maintenance work are
independent ready branches. Relay Session completion unlocks reservation/circuit
and quota/accounting work. Only leaf-completion dependencies gate final qualification.

## Orchestration and closure

This table is the complete executable graph for `implement-spec`. Treat the
resolved operator-journey and prototype tickets as decision context, and the
superseded preliminary verification outline as history. Do not dispatch them or
require all directory entries to be implemented. Apply `Blocked by` regardless
of a ticket's `ready-for-agent` label; claim before starting and resolve only
with the required evidence. Respect the repository's coordination/capacity rules.

Each slice includes code wiring, behavioral checks, privacy/bounded-output
controls and its interpretation examples. The final ticket verifies combined
behavior and publishes the complete guide; it is not a later safety phase.

Use the spec and contract as context pointers. Local implementation/checks are
authorized; image/release publication, deployment, metric work and full observability-release
completion are outside this graph. Preserve unrelated work in both repositories.

## Historical context

- [Decide the relay's operator diagnostic journeys](issues/01-decide-operator-diagnostic-journeys.md#answer)
- [Prototype the structured logging contract](issues/02-prototype-the-signal-contract.md#answer)
- [Superseded preliminary verification outline](issues/06-verify-and-document-operator-signals.md)
- [Approved breakdown with proposed acceptance criteria](ticket-breakdown-proposal.md)
