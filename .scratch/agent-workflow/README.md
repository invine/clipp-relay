# Agent workflow plan: canonical tracker in Clipp

The project-specific plan for retrospective improvements 1, 3, 5 and 7 is owned by the Clipp checkout under `.scratch/agent-workflow/`:

- `spec.md`: the behavior and observable acceptance criteria.
- `README.md`: source inventory, dependency graph and `/implement-spec` handoff.
- `issues/`: five canonical tickets; do not create duplicate claims here.

Locate the Clipp checkout through the explicitly selected repository mapping; its home-directory location is not part of the contract. Planning baseline on 2026-10-07: Clipp `9df162a12386ce0d336f793a980c7053e770e1bd`, relay `e881c62ec271836d46d64081a4e4011d7e488676`, both primary checkouts clean.

Implementation uses at most two concurrent implementers with fresh ticket contexts and isolated worktrees. The coordinator owns claims, integration and resolution. Each participating repository has its own integration branch and verified commit reference. Preserve existing changes; local commits only. No push, publication or deployment is authorized.

This pointer does not alter the managed-relay backlog or its deferred capacity ticket. The operator approved the test boundaries and five-ticket breakdown on 2026-10-07. The canonical tickets are ready-for-agent; implementation has not begun.
