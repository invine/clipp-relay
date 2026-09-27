# Prototype the account and administration portal

Type: prototype
Status: resolved
Blocked by: 03, 06, 09

## Question

What minimal server-rendered registration/login, pending-approval, account-profile, live-session, Charged Relay Traffic, and administrator-management experience makes the settled account states, conservative Relay Quota behavior, privacy limits, and account-wide Relay Session termination clear enough for implementation?

## Comments

- 2026-09-04: Captured a live, throwaway review asset on branch
  `prototype/account-administration-portal` at commit `27692b3`. The asset lives at
  `prototype/account-administration-portal/` in the branch and runs with
  `npm run review`. It presents three deliberately different layouts on one route
  (`?variant=A|B|C`): an approachable account hub, a dense operations console,
  and a guided status page. The shared scenario control covers Google
  registration/login, Pending, Active, Suspended, Denied, and administrator
  views. In-memory actions exercise administrator approval with a structured
  reason and optimistic revision, portal-only logout, account-wide revocation,
  and authenticated account deletion with Retained Quota Usage (labeled
  `Quota Carryover` in the historical prototype). The rendered state
  stays visible and confirms that no durable device records are modeled. Awaiting
  live user review before this prototype ticket is resolved.
- 2026-09-04: First review selected a hybrid direction: use the B operations
  console for administration; use A for registration, non-Active account states,
  the account shell, actions, and history; and compose the Active account summary
  from C's current-consumption doughnut plus B's tabular account-wide Relay
  Session capacity. Captured that selection as variant A (`Selected mix`) in
  commit `577c1b3`. The B and C alternatives remain available for comparison.
  Awaiting confirmation of the consolidated view before resolving the ticket.
- 2026-09-04: Follow-up review kept B's administrative information architecture
  but aligned it with the rest of the selected portal: A's light header,
  typography, cards, spacing, status badges, tables, and form controls. Captured
  at commit `2d21383`. The dense service metrics, Relay Account table, selected
  account editor, structured reason, and optimistic revision remain intact.
- 2026-09-04: Human verdict: accepted as an implementation rough draft. Detailed
  layout and visual-style refinement is intentionally deferred until later UI
  work.

## Answer

Use one server-rendered portal language for registration, non-Active account
states, the Active profile, and administration. The accepted rough draft uses a
light account-oriented shell; explains Pending, Suspended, and Denied states with
the next available action; shows current weekly Quota Committed as a
doughnut with conservative-accounting copy; and presents account-wide Relay
Session and Login Grant capacity in a compact table without exposing individual
devices, sessions, or grants.

The quota label and meaning are refined by accepted Q221 in
[Define internal Go modules and PostgreSQL persistence](14-define-go-modules-and-postgresql-persistence.md).
The historical prototype's measured-traffic wording is not the current
implementation contract; the accepted layout remains the reference.

The administrator workspace keeps the dense information architecture explored in
variant B—service totals, a Relay Account table, and a selected-account mutation
pane—but uses the same shell, typography, cards, controls, spacing, and status
treatment as the user-facing pages. Mutations expose the structured reason and
optimistic revision. Portal-only logout, Account-wide Revocation, and
quota-preserving self-deletion remain explicit and distinct.

The implementation guide is the accepted `A — Selected mix` on branch
`prototype/account-administration-portal` at commit `2d21383`, under
`prototype/account-administration-portal/`. This settles information hierarchy
and workflow coverage, not final layout or visual polish.
