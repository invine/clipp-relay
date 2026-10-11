# Verify and document the operator signals

Type: task
Status: wontfix
Blocked by: none
Resolution: superseded by the approved integrated-qualification ticket

## Question

Run relevant relay checks against the integrated local implementation and exercise representative successful and failed portal, OAuth, relay, quota, publication, dependency and drain journeys. Verify synthetic identity/credential/content canaries and bounded diagnostics while stdout is blocked; document log event meanings, loss and server visibility limits for operators. What exact revision, commands and passed/failed/not-run evidence support local logging completion, and which broader observability work or external gates remain outstanding?

## Comments

This is an execution ticket under the map's explicit Notes override. Do not treat local checks as deployment, live qualification, or resolution of the full observability implementation ticket.

- 2026-10-11: This unclaimed preliminary outline was superseded by [Qualify integrated logging and publish the operator guide](11-qualify-integrated-logging-and-operator-guide.md). Its verification obligations are carried into that ticket; this closure is not implementation completion. Exclude this historical outline from the executable build graph.
