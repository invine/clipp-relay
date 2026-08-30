# Clipp Relay

Clipp Relay is the account-scoped service context for authorizing and governing use of shared relay capacity without owning Clipp's device-membership model.

## Language

**Relay Account**:
The tenancy and quota identity established through an external identity provider. A Relay Account authenticates relay use but does not own or durably register Device Identities.
_Avoid_: Device Network, Device Identity

**External Identity**:
The immutable issuer-and-subject pair asserted by a configured identity provider for one Relay Account. Email is verified presentation metadata rather than account identity.
_Avoid_: Email identity, Device Identity

**Device Identity**:
A Clipp cryptographic identity represented by one libp2p Peer ID. The relay authenticates the live peer cryptographically but does not durably bind that identity to a Relay Account or infer its Device Network membership.
_Avoid_: Relay Account, client account

**Device Network**:
Clipp's logical owner-scoped group whose membership is maintained by its devices. The relay does not own, derive, or replace Device Network membership.
_Avoid_: Relay Account, relay tenant

**Relay Quota**:
The administrator-defined allowance governing a Relay Account's weekly Relayed Traffic and concurrent Relay Sessions, reservations, and relayed circuits.
_Avoid_: Client quota, device quota

**Weekly Quota Window**:
The period from Monday at 00:00 UTC until the following Monday at 00:00 UTC during which a Relay Account's traffic allowance is consumed. Unused allowance does not roll over.
_Avoid_: Billing period, rolling week

**Quota Plan**:
A reusable administrator-managed set of Relay Quotas assigned to approved Relay Accounts. An account may additionally have administrator-defined overrides.
_Avoid_: Subscription, client-selected plan

**Pending Relay Account**:
A Relay Account created after its first successful external-provider login but not yet approved by an administrator. It cannot authenticate Relay Sessions.
_Avoid_: Trial account, unverified device

**Safety Limit**:
A service-wide resource bound protecting relay availability independently of a Relay Account's allowance.
_Avoid_: Relay Quota

**Relay Session**:
A live libp2p connection associated with the Relay Account that authenticated it. The association is ephemeral and ends when the connection closes.
_Avoid_: Device enrollment, login session

**Relay Authentication**:
The connection-scoped exchange that associates a Noise-authenticated libp2p connection with an approved Relay Account before protected relay protocols may be used. It does not durably enroll the connection's Device Identity.
_Avoid_: Device enrollment, Clipp Pairing

**Relayed Traffic**:
Opaque bytes forwarded through a Circuit Relay connection. The relay does not interpret them as Clips or application messages.
_Avoid_: Relayed messages, clip traffic

**Relay Instance**:
A process-lifetime libp2p data-plane member of the horizontally scalable relay pool. It generates a new Peer ID on every start and advertises its publicly reachable transport addresses through a finite Relay Registration.
_Avoid_: Relay replica, discovery endpoint

**Relay Coordinator**:
The shared control-plane service that hosts account workflows, tracks live Relay Registrations, and answers Relay Discovery requests. It does not forward Relayed Traffic.
_Avoid_: Relay Instance, bootstrap peer

**Relay Registration**:
A finite, renewable record through which a running Relay Instance reports its current Peer ID, public addresses, health, and available capacity to the Relay Coordinator.
_Avoid_: Device registration, Relay Assignment

**Relay Discovery Endpoint**:
The single authenticated HTTPS control-plane address configured by a Clipp installation to obtain a currently available Relay Instance.
_Avoid_: Relay Instance, bootstrap peer

**Relay Assignment**:
A finite recommendation from the Relay Discovery Endpoint selecting the Relay Instance where a client should establish its reservation. It grants neither Device Identity ownership nor exclusive permission to contact that instance.
_Avoid_: Device enrollment, permanent affinity

**Reservation Home**:
The Relay Instance selected by a Relay Assignment to hold a Device Identity's current Circuit Relay reservation.
_Avoid_: Relay owner, account relay
