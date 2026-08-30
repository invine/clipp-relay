# Research OCI relay-instance networking

Type: research
Status: resolved
Blocked by:

## Question

Using official Kubernetes, OCI, NGINX Ingress, and cert-manager sources, how can the Helm release expose a GitOps-scaled set of independently addressed Relay Instances over per-instance WSS, raw TCP, and WebRTC Direct UDP while retaining one stable HTTPS Relay Discovery Endpoint, wildcard DNS/TLS, ARM64 scheduling, graceful scale-down, and compatibility with the capabilities installed in the current cluster?

## Comments

## Answer

Use stable StatefulSet ordinal network slots with ephemeral process identities: exact-host WSS routes through the shared NGINX ingress, while separate per-ordinal OCI NLB Services expose raw TCP and WebRTC Direct UDP directly to the selected Pod. The Coordinator watches provider-assigned Service addresses and publishes only fully ready, registered instances. Wildcard WSS TLS requires an operator-provided Secret or DNS-01 issuer, and planned scale-down is a two-sync drain-then-remove workflow. See [OCI relay-instance networking](../research/oci-relay-instance-networking.md).
