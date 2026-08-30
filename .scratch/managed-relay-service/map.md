# Design the account-governed Clipp relay service

Label: wayfinder:map

## Destination

Reach an implementation-ready decision set for a horizontally scalable Go Clipp relay: coordinated Electron, Android, and Chrome-extension integration; Google-backed Relay Accounts; weekly administrator-defined quotas; user and administrator portals; and an ARM64-compatible Helm release consumable by Argo CD on the existing OCI Kubernetes cluster.

## Notes

- This map plans the work; it does not implement or deploy the production service.
- Maintain relay terminology in [CONTEXT.md](../../CONTEXT.md) and consume Clipp application terminology from [/Users/invine/src/js/clipp/CONTEXT.md](/Users/invine/src/js/clipp/CONTEXT.md).
- Grilling tickets use the `grilling` and `domain-modeling` skills. Research tickets use the `research` skill and primary sources. Prototype tickets use the `prototype` skill with live human review.
- A Relay Account is never a Device Identity or Device Network. Device Identity association exists only for a live Relay Session; the service has no durable device registry.
- Cross-account circuits are allowed. Relayed Traffic and concurrency consume each distinct participating account's quota; two endpoints on one account consume that account once.
- The weekly traffic window resets Monday at 00:00 UTC without rollover. Accounts require administrator approval and receive administrator-managed Quota Plans and optional overrides.
- Google is the sole initial external provider. The relay data plane receives only Clipp Relay credentials, never Google tokens.
- Clients configure one HTTPS Relay Discovery Endpoint. It assigns one live, independently keyed Relay Instance as the Reservation Home; every Relay Instance generates a new Peer ID on process start.
- The managed pool requires discovery. Explicit full relay multiaddrs remain only for development and separately operated persistent-identity relays.
- Production exposure must support WSS, raw TCP, and WebRTC Direct UDP while carrying Circuit Relay v2 and Clipp's exact Rendezvous behavior.
- The Coordinator is horizontally replicated and PostgreSQL is the only initial shared datastore. Production PostgreSQL is external to the application chart.
- The current cluster has three ARM64 nodes plus Argo CD, NGINX Ingress, cert-manager, Longhorn, and Velero. Prometheus is absent; expose compatible metrics without requiring its installation.
- Initial acceptance scale is 1,000 Relay Accounts and 5,000 concurrent Relay Sessions.

## Decisions so far

<!-- Closed-ticket context pointers are appended here. -->

- [Research Go libp2p relay compatibility](issues/01-research-go-libp2p-relay-compatibility.md) — go-libp2p v0.49.0 covers Clipp's wire stack, but account-aware authentication, metering, and targeted termination require a narrow pinned relay adaptation validated by the prototype.

## Not yet specified

- Internal Go module seams and durable schema details that depend on the protocol, quota, and credential decisions.
- The migration and cutover sequence from today's hard-coded relay defaults to automatic discovery across all three Clipp runtimes.
- Operator runbooks for capacity expansion, degraded dependencies, instance churn, and recovery after the deployment contract is fixed.
- Exact backup, restore, and disaster-recovery procedures for the external PostgreSQL service.

## Out of scope

- Production implementation or deployment during this planning map.
- Automatic horizontal scaling; replica count changes are GitOps-managed initially.
- IP-address throttling in the initial release.
- Identity providers other than Google, cross-provider account linking, billing, and paid subscriptions.
- Durable Device Identity enrollment, ownership, or device administration.
- Content inspection or moderation.
- A separate native/mobile administration application.
- OCI tenancy, cluster, database, OCIR repository, and Argo CD credential provisioning.
