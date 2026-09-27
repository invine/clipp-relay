# Choose the single-process v1 topology and quota boundary

Type: grilling
Status: resolved
Blocked by: 01, 02

## Question

Which topology and Relay Quota guarantees should the first release keep or defer so that Relay Authentication, account approval, conservative consumption control, client integration, and OCI deployment remain coherent without a distributed Relay Pool or a full HOP-to-STOP circuit supervisor?

## Comments

## Answer

Use one Go process and one Kubernetes Pod containing separate internal Relay Coordinator and Relay Instance Modules. Run exactly one Relay Instance, generate its Peer ID on every process start, and have the Relay Discovery Endpoint return that instance's current addresses. The Relay Coordinator role owns Google login, Relay Accounts, Quota Plans, profile and administration workflows, discovery, and PostgreSQL access. The Relay Instance role owns libp2p listeners, connection-scoped Relay Authentication, Relay Sessions, exact Rendezvous, and stock Circuit Relay v2. Their calls are in-process; v1 has no Relay Registration protocol, internal service credential, distributed concurrency lease, or Relay Pool.

Quota Plans govern concurrent Relay Sessions and a weekly traffic allowance. A small HOP adapter rejects protected HOP operations unless the exact connection has an active Relay Session. The stock bandwidth reporter attributes HOP and STOP endpoint bytes to the Relay Account of the authoritative live Device Identity connection. It can include Circuit Relay control bytes and charges each endpoint independently, so one Relay Account used at both endpoints is charged twice. The durable consumption and profile number is now Quota Committed, distinct from those observed endpoint bytes, as refined by accepted Q221 in [Define internal Go modules and PostgreSQL persistence](14-define-go-modules-and-postgresql-persistence.md). Accepted Q228 there explicitly records the report-time association and unattributed-tail limitations.

Concurrent Relay Session counts are exact and process-local. PostgreSQL reserves finite blocks from the Weekly Quota Window so the forwarding path consumes local credits without a database operation per buffer; a crash can conservatively strand unused reserved credit but cannot grant free traffic. Quota exhaustion or suspension invalidates and closes every local Relay Session for the Relay Account. The first release does not identify or terminate only one responsible circuit.

Reservations and relayed circuits use go-libp2p Safety Limits: service, Peer ID, IP, and ASN reservation bounds; reservation lifetime; circuits per Peer ID; circuit duration; bytes per direction; and Resource Manager constraints. They are not Relay Account quotas or profile statistics in v1. The Relay Session allowance and per-Peer-ID Safety Limits provide a conservative account capacity bound without a circuit registry.

The process fails closed for new Relay Authentication when PostgreSQL-backed account or quota state is unavailable. Existing connections continue only while valid local credit and session state remain. The Helm release uses one replica with a non-overlapping rollout strategy. The implementation must first prove that one ARM64 Relay Instance can meet the 5,000-concurrent-Relay-Session acceptance target. Multiple Relay Instances, independent control-plane deployment, exact circuit accounting, same-account deduplication, distributed leases, and targeted circuit termination are deferred to a future map.
