# OCI relay-instance networking

## Question

How should the Helm release expose a GitOps-scaled set of process-lifetime Relay Instances over WSS, raw TCP, and WebRTC Direct UDP while users configure only one stable HTTPS Relay Discovery Endpoint?

## Recommendation

Use stable Kubernetes **network slots** and ephemeral libp2p identities:

- Run Relay Instances in a StatefulSet without persistent volumes. The ordinal is a stable routing slot such as `relay-0`; the process still generates a new key and Peer ID on every start.
- Put the replicated Relay Coordinator behind one ClusterIP Service and the existing F5 NGINX Ingress at `relay.example.com`.
- For each live ordinal, render an exact-pod ClusterIP Service and an exact-host Ingress such as `r-0.relay.example.com` for WSS. All instance hostnames use the same wildcard DNS record and wildcard TLS Secret.
- For each live ordinal, render **separate** OCI Network Load Balancer Services for raw TCP and WebRTC Direct UDP. Each Service selects only that ordinal and exposes its provider-assigned public IPv4 address in `status.loadBalancer`.
- Have the Coordinator combine the Relay Instance's current Peer ID and WebRTC certificate hash with the WSS hostname and current TCP/UDP Service addresses. Discovery must return an instance only after its registration and all enabled public paths are ready.

The resulting public paths are:

| Purpose | Public entry | Kubernetes route |
| --- | --- | --- |
| Discovery, login, profile, admin | `https://relay.example.com` | Existing NGINX load balancer → Coordinator Ingress → Coordinator Service |
| Instance WSS | `wss://r-N.relay.example.com` | Same NGINX load balancer → exact-host Ingress → `relay-N-ws` Service → `relay-N` Pod |
| Instance raw TCP | `/ip4/<tcp-nlb-ip>/tcp/<port>` | `relay-N-tcp` OCI NLB Service → `relay-N` Pod |
| Instance WebRTC Direct | `/ip4/<udp-nlb-ip>/udp/<port>/webrtc-direct/certhash/<hash>` | `relay-N-udp` OCI NLB Service → `relay-N` Pod |

WebRTC Direct explicitly permits an IP/UDP multiaddress carrying the server certificate fingerprint and Peer ID, so a trusted public DNS certificate is not needed on the UDP path. Its specification expects the browser to learn that complete address through an external mechanism, which in this design is Relay Discovery ([libp2p WebRTC Direct specification](https://github.com/libp2p/specs/blob/master/webrtc/webrtc-direct.md)).

## Why the ordinal is a network slot, not an identity

StatefulSet Pods receive deterministic ordinals, a `statefulset.kubernetes.io/pod-name` label that a Service can use to select one Pod, and the stable `apps.kubernetes.io/pod-index` label. Scale-down terminates the highest ordinal first under the default `OrderedReady` policy ([Kubernetes StatefulSets](https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/)). Those properties solve deterministic Helm rendering and routing without contradicting the decision that a Relay Instance key is process-lifetime.

An ordinary restart of `relay-0` therefore preserves its WSS hostname and NLB Services but creates a different Peer ID and WebRTC certificate hash. The old Relay Registration expires or is unregistered; the restarted process is unavailable to discovery until it registers the new identity and current addresses. Clients must treat the complete discovered multiaddrs, not the ordinal hostname, as the finite assignment.

The StatefulSet needs no volume claim. It should use pod anti-affinity or topology-spread constraints across `kubernetes.io/hostname`, a nonzero termination grace period, and configurable node affinity. The published image must contain `linux/arm64`; publishing an OCI image index with both `linux/arm64` and `linux/amd64` preserves portability ([OCI image index](https://github.com/opencontainers/image-spec/blob/main/image-index.md)).

## WSS and the stable HTTPS endpoint

Kubernetes Ingress routes HTTP(S) by host to a Service and does not expose arbitrary TCP or UDP ports ([Kubernetes Ingress](https://kubernetes.io/docs/concepts/services-networking/ingress/)). Generate an exact host rule for the Coordinator and for every rendered relay ordinal. A wildcard DNS record can point all `r-N.relay.example.com` names to the existing NGINX public address, while the exact Ingress rules choose different per-ordinal backends.

The installed controller is F5 NGINX Ingress Controller, not the community ingress-nginx controller. WSS Ingresses must set `nginx.org/websocket-services` for their per-ordinal Service and must override the default 60-second proxy read/send timeouts to values compatible with long-lived libp2p connections ([F5 NGINX Ingress annotations](https://docs.nginx.com/nginx-ingress-controller/configuration/ingress-resources/advanced-configuration-with-annotations/)). TLS terminates at NGINX; the Relay Instance receives ordinary WebSocket traffic internally.

Use two DNS records:

- `relay.example.com` → the existing NGINX load balancer, for the Coordinator.
- `*.relay.example.com` → the same address, for Relay Instance WSS hosts.

The wildcard does not cover the base name. The release should consume a pre-existing `kubernetes.io/tls` Secret in its namespace. Optionally the chart may render a cert-manager `Certificate` that writes that Secret, but its `Issuer` or `ClusterIssuer` and DNS-provider credentials remain operator-provided. cert-manager documents DNS-01 for wildcard issuance and can select it specifically for `example.com` and `*.example.com` ([cert-manager ACME](https://cert-manager.io/docs/configuration/acme/), [DNS-01 configuration](https://cert-manager.io/docs/configuration/acme/dns01/)).

## Raw TCP and WebRTC Direct UDP on OCI

Use OCI Network Load Balancers, not the regular OCI Load Balancer. OKE provisions one for a `LoadBalancer` Service with:

```yaml
metadata:
  annotations:
    oci.oraclecloud.com/load-balancer-type: "nlb"
    oci-network-load-balancer.oraclecloud.com/external-ip-only: "true"
spec:
  type: LoadBalancer
```

Oracle documents NLBs as pass-through Layer 3/4 load balancers for TCP and UDP, and OKE selects UDP or TCP from each Service port's `protocol` ([provisioning OCI NLBs for OKE Services](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengcreatingnetworkloadbalancers.htm)). `external-ip-only` prevents the private NLB address from also appearing in Kubernetes Service status, leaving a single public address for registration ([OKE load balancer configuration](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengconfiguringloadbalancersnetworkloadbalancers-subtopic.htm#Concealing_a_Network_Load_Balancer_s_Private_IP_Address)).

The initial chart should use one TCP-only and one UDP-only NLB Service per ordinal. Kubernetes supports mixed-protocol `LoadBalancer` Services, but explicitly leaves supported combinations to the cloud provider ([Kubernetes Services](https://kubernetes.io/docs/concepts/services-networking/service/#load-balancers-with-mixed-protocol-types)). Oracle's OKE documentation demonstrates TCP and UDP listeners separately and does not establish mixed-protocol conformance for the cluster's controller. Splitting them is the high-confidence contract until an OCI conformance test proves a combined Service safe. It also lets each transport have an independent health policy and advertised address.

This cluster is eligible for direct Pod backends: it is Kubernetes 1.36 and uses OCI VCN-native pod networking. For each NLB Service, prefer:

```yaml
metadata:
  annotations:
    oci.oraclecloud.com/load-balancer-type: "nlb"
    oci-network-load-balancer.oraclecloud.com/external-ip-only: "true"
    oci-network-load-balancer.oraclecloud.com/health-check: >-
      {"protocol":"HTTP","port":<health-port>,"urlPath":"/readyz","returnCode":200}
    oci.oraclecloud.com/security-rule-management-mode: "NSG"
    oci.oraclecloud.com/oci-backend-network-security-group: "<operator-provided-ocid>"
spec:
  allocateLoadBalancerNodePorts: false
```

OKE supports Pod backends on Kubernetes 1.30+ with VCN-native pod networking, requires NodePort allocation to be disabled, and requires the NLB Pod-health-check annotation. Oracle recommends NSG-based rule management rather than security lists ([OKE Pods as load-balancer backends](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengconfiguringloadbalancersnetworkloadbalancers-subtopic.htm#Using_Pods_as_Backends), [OKE security-rule management](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengconfiguringloadbalancersnetworkloadbalancers-subtopic.htm#Specifying_Security_Rule_Management_Options_for_Load_Balancers_and_Network_Load_Balancers)). Frontend and backend NSG OCIDs, required IAM policy, subnets, and their rules are infrastructure prerequisites supplied as Helm values; the application release must not create them.

Keep OCI NLB instant failover disabled. It redirects established traffic from an unhealthy backend to another healthy backend ([OKE NLB instant failover](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengcreatingnetworkloadbalancers.htm#Enabling_Instant_Failover)). A Service selects exactly one Relay Instance, and another instance has a different Peer ID, so redirecting an established libp2p flow would be invalid.

Provider-assigned NLB IPs are acceptable because clients receive finite assignments. If operators require a public address to survive deletion and recreation of a Service, expose an optional per-Service `oci.oraclecloud.com/reserved-ips` value; Oracle recommends that annotation for reserved public IPv4 addresses ([OKE reserved public IPs](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengconfiguringloadbalancersnetworkloadbalancers-subtopic.htm#Specifying_Reserved_Public_IP_Addresses)). Reserved addresses are operator-provisioned infrastructure, not keys generated by the chart.

## Address publication and readiness

Helm must not discover provider-assigned addresses at render time. Argo CD uses Helm only to run `helm template`, with Argo CD managing the resulting resources ([Argo CD Helm integration](https://argo-cd.readthedocs.io/en/latest/user-guide/helm/)); Helm's `lookup` returns empty under ordinary `helm template` ([Helm template function list](https://helm.sh/docs/v3/chart_template_guide/function_list/#lookup)).

Instead, give the Coordinator a namespace-scoped read-only Role for the generated Relay Services and Pods. It can watch `Service.status.loadBalancer.ingress`, associate Services with the StatefulSet ordinal, and validate the addresses supplied by the in-cluster Relay Registration. The data-plane Pod need not receive Kubernetes API credentials beyond the registration secret already required by the design.

A Relay Instance becomes discoverable only when all of the following are true:

1. The Pod has generated its key and is listening on every enabled internal port.
2. Its WSS Ingress/Service route is ready.
3. Its TCP and UDP NLB Services each report the expected public address.
4. It has signed and renewed a Relay Registration carrying the current Peer ID and WebRTC certificate hash.
5. Its application readiness and capacity checks pass, and it is not draining.

If one optional transport is administratively disabled, discovery omits it rather than waiting forever.

## GitOps scale-up and graceful scale-down

Scale-up is a normal Argo CD sync: increasing `relay.replicaCount` renders the next StatefulSet ordinal plus its exact WSS and NLB resources. Discovery ignores the new slot until the provider addresses and registration are ready.

Planned scale-down must be two-phase because reducing the StatefulSet replica count immediately begins Pod termination, while removing ordinal-derived Services and Ingresses makes them pruning candidates in the same Argo CD operation:

1. In the first Git change, mark the highest ordinal or ordinals as draining without changing `replicaCount`. The Coordinator stops new assignments, the Relay Instance rejects new reservations, clients rediscover, and the operator waits for the configured circuit deadline or zero active circuits.
2. In the second Git change, lower `replicaCount` and remove those ordinals from the drain list. StatefulSet reverse-order termination removes the already-drained highest ordinal first.

The exact watched configuration or Coordinator command that carries desired drain state belongs to the relay-pool lifecycle decision, but it must not require restarting the Pod because restart itself changes the Peer ID.

As a failure-path guard, the Pod should also have an idempotent `preStop` handler plus `terminationGracePeriodSeconds` longer than the emergency drain interval. Kubernetes starts the grace countdown before running `preStop`, marks terminating endpoints non-ready for normal traffic, and ultimately kills the process when the grace expires ([Kubernetes Pod termination](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/#pod-termination-flow), [container lifecycle hooks](https://kubernetes.io/docs/concepts/containers/container-lifecycle-hooks/)). This protects accidental deletion but is not a substitute for the two-phase planned drain.

Set `PruneLast=true` in the example Argo CD `Application` so removed per-ordinal Services and Ingresses are pruned only after applied resources become healthy ([Argo CD sync options](https://argo-cd.readthedocs.io/en/latest/user-guide/sync-options/#prune-last)). Do not use `Replace=true` on the StatefulSet or Services.

## Installed-cluster compatibility snapshot

Read-only inspection on 2026-08-30 found:

- Kubernetes 1.36.1 with three ready Oracle Linux ARM64 worker nodes in three availability domains; all carry `kubernetes.io/arch=arm64` and `oci.oraclecloud.com/vcn-native-ip-cni=true`.
- Successful `NativePodNetwork` resources for all three nodes, so the OKE Pod-backend prerequisites are present.
- F5 NGINX Ingress Controller 5.5.4 as a three-Pod DaemonSet behind one OCI LoadBalancer Service exposing only TCP 80 and 443. Custom resources are enabled, but there is no GlobalConfiguration argument/object and no custom TCP/UDP Service ports.
- NGINX external-DNS integration is disabled and no ExternalDNS workload is installed. Therefore wildcard DNS must be provisioned outside this chart; raw TCP/UDP addresses should initially be advertised as NLB IP multiaddrs.
- cert-manager 1.21.1 with a ready ACME ClusterIssuer configured only for HTTP-01. A wildcard certificate requires an operator-provided existing Secret or a new DNS-01-capable issuer; the existing issuer is insufficient for the agreed wildcard design.
- Argo CD 3.5.0 is installed. No Prometheus Operator `ServiceMonitor` or `PodMonitor` CRDs are installed, so the chart must not require them.

The existing NGINX controller can technically load-balance TCP and UDP using `TransportServer` plus a referenced `GlobalConfiguration`, and its external Service must expose every custom listener port ([F5 GlobalConfiguration](https://docs.nginx.com/nginx-ingress-controller/configuration/global-configuration/globalconfiguration-resource/), [F5 TransportServer](https://docs.nginx.com/nginx-ingress-controller/configuration/transportserver-resource/)). That alternative is not recommended here: it requires modifying a separately owned ingress-controller Helm release, consumes one shared listener port per ordinal and protocol, adds a stream-proxy hop to WebRTC Direct, and couples relay scaling to cluster ingress configuration. Standard Ingress for WSS plus per-ordinal OCI NLB Services works with the installed boundary and keeps the relay chart self-contained.

## Helm contract carried into the deployment decision

The later Helm/Argo CD ticket should make these inputs explicit and schema-validated:

- `relay.replicaCount`, enabled transports, internal/public ports, and drain interval.
- Coordinator hostname, relay WSS base domain/pattern, `ingressClassName: nginx`, and WSS timeout annotations.
- `tls.existingSecret`, with an optional cert-manager `Certificate` mode referencing an operator-provided DNS-01 issuer.
- OCI NLB annotations, frontend/backend NSG OCIDs, subnet/compartment overrides if needed, optional reserved public IPs, and Pod-backend enablement.
- Image repository/digest with `linux/arm64` support, plus configurable node affinity, topology spread, and resource limits.
- A plain metrics Service/endpoint; `ServiceMonitor` generation disabled by default and guarded by capability/value checks.
- An example Argo CD `Application` using automated sync only if desired, `PruneLast=true`, and a documented two-sync scale-down runbook.

This arrangement preserves the one configured discovery URL while allowing each Relay Instance to have its own ephemeral cryptographic identity and independently reachable transport addresses.
