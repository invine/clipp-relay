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
The administrator-defined allowance governing a Relay Account's weekly Quota Committed and concurrent Relay Sessions.
_Avoid_: Client quota, device quota

**Weekly Quota Window**:
The period from Monday at 00:00 UTC until the following Monday at 00:00 UTC during which a Relay Account's traffic allowance is consumed. Unused allowance does not roll over.
_Avoid_: Billing period, rolling week

**Quota Committed**:
The portion of a Relay Account's weekly traffic allowance committed to relay-use credit, including both spent and still-unused reserved credit. It is the quota consumption shown in the portal, not a measurement of transferred bytes; ordinary operation does not refund it, while disaster recovery may explicitly reset an unrecoverable amount.
_Avoid_: Charged Relay Traffic, exact transferred bytes

**Retained Quota Usage**:
The privacy-minimal record of a deleted Relay Account's Quota Committed for the current Weekly Quota Window. It preserves consumption on re-registration within that window and expires when the window ends, without preserving the deleted account or its Device Identities.
_Avoid_: Quota Carryover, quota rollover, deleted account, denial record

**Quota Plan**:
A reusable administrator-managed set of Relay Quotas whose allowance values are immutable once assigned. An account may be explicitly reassigned to a replacement plan or have administrator-defined overrides; archiving a plan prevents new assignments without changing existing ones.
_Avoid_: Subscription, client-selected plan

**Pending Relay Account**:
A Relay Account created after its first successful external-provider login but not yet approved by an administrator. It cannot authenticate Relay Sessions.
_Avoid_: Trial account, unverified device

**Active Relay Account**:
A Relay Account approved to obtain and use Relay credentials under its administrator-assigned Quota Plan and overrides.
_Avoid_: Approved device, enabled identity

**Suspended Relay Account**:
A temporarily blocked Relay Account whose administrative assignments and usage history remain but whose credentials and Relay Sessions are invalidated.
_Avoid_: Disabled device, Denied Relay Account

**Denied Relay Account**:
A refused Relay Account whose External Identity remains recognized so it cannot silently register again.
_Avoid_: Suspended Relay Account, deleted account

**Administrator Identity**:
A Google-authenticated identity granted the single v1 administrator role by the current Secret-backed, authoritative-email allowlist. It is independent of Relay Account status.
_Avoid_: Admin account, account role

**Portal Session**:
A short-lived, server-side browser session for Relay Account or administrator portal actions. It is never a Relay credential.
_Avoid_: Relay Session, Login Grant

**Login Grant**:
A renewable authorization issued to one registered Clipp public-client type for one Relay Account. It is not bound to a Device Identity, hardware device, or application installation.
_Avoid_: Device enrollment, Portal Session, refresh token

**Relay Access Token**:
A short-lived opaque bearer credential derived from a Login Grant and accepted only for Relay Discovery and Relay Authentication.
_Avoid_: Portal Session, Google token, administrator token

**Account-wide Revocation**:
The deliberate invalidation of every Login Grant, Relay Access Token, Portal Session, and live Relay Session for one Relay Account without changing its account status or administrative assignments. Its user-facing action is `Sign out everywhere`.
_Avoid_: Device logout, individual grant revocation, account suspension

**Relay Account Deletion**:
An irreversible owner action that erases a Relay Account and permits its External Identity to register a fresh Pending Relay Account. Only explicitly retained quota, audit and recovery records may temporarily survive it; they are not a continuing account.
_Avoid_: Account suspension, account denial, device removal

**Recovery Journal**:
The privacy-minimal record of Relay Account deletions used to prevent database restoration from resurrecting deleted accounts. It is not a device registry, quota ledger or history of administrative restrictions.
_Avoid_: Device registry, quota ledger, account backup

**Recovery Review Hold**:
A restore-specific restriction preventing a Relay Account from using relay capacity until an administrator explicitly reviews its status and quota assignments. It is separate from the account's Pending, Active, Suspended or Denied status.
_Avoid_: Account suspension, new account status, quota exhaustion

**Safety Limit**:
A service-wide or per-Peer-ID resource bound protecting relay availability independently of a Relay Account's allowance. Reservation counts, circuit counts, circuit duration, and per-direction circuit bytes are Safety Limits in v1.
_Avoid_: Relay Quota

**Relay Session**:
A live libp2p connection associated with the Relay Account that authenticated it. The association is ephemeral and ends at its fixed expiry unless successful same-account reauthentication extends it; ending a Relay Session closes the connection.
_Avoid_: Device enrollment, login session

**Relay Authentication**:
The connection-scoped exchange that associates a Noise-authenticated libp2p connection with an approved Relay Account before protected relay protocols may be used. It does not durably enroll the connection's Device Identity.
_Avoid_: Device enrollment, Clipp Pairing

**Relay Authentication Barrier**:
The client-side rule that every physical connection to an authenticated relay completes Relay Authentication before that connection may carry HOP or Rendezvous operations. Authentication is never inherited from another connection to the same Relay Instance.
_Avoid_: Relay login, peer authentication

**Rendezvous Lease**:
The process-local, finite association between one Device Identity's Peer ID and its unchanged Signed Peer Record on a Relay Instance. It is owned by the Relay Session and reservation that created it and is not durable Device Identity enrollment.
_Avoid_: Device registry, peer ownership

**Relayed Traffic**:
Opaque bytes forwarded through a Circuit Relay connection. The relay does not interpret them as Clips or application messages.
_Avoid_: Relayed messages, clip traffic

**Charged Relay Traffic**:
The conservative HOP and STOP endpoint byte count attributed to a Relay Account, distinct from Quota Committed. Both endpoints count independently, including twice for the same account, and Circuit Relay control bytes may be included.
_Avoid_: Quota Committed, exact payload traffic, billable traffic

**Relay Instance**:
A process-lifetime libp2p data-plane role that accepts Relay Sessions and forwards Relayed Traffic. The single v1 Relay Instance generates a new Peer ID on every start and publishes its current addresses through Relay Discovery.
_Avoid_: Relay Coordinator, discovery endpoint

**Relay Network Slot**:
The stable Kubernetes routing position occupied by the current Relay Instance, named `relay-0` in v1. Its Services and WSS hostname may survive process replacement, but its Peer ID and WebRTC certhash do not.
_Avoid_: Relay Identity, Relay Instance

**Relay Drain**:
The terminal Relay Instance lifecycle state in which new relay work is refused while admitted Relayed Traffic receives a bounded opportunity to finish before every remaining Relay Session closes.
_Avoid_: Relay shutdown, maintenance mode

**Relay Coordinator**:
The control-plane role that hosts Relay Account workflows and answers Relay Discovery requests. In v1 it runs in the same process as the Relay Instance but does not forward Relayed Traffic.
_Avoid_: Relay Instance, bootstrap peer

**Relay Discovery Endpoint**:
The single authenticated HTTPS address configured by a Clipp installation to obtain the current Relay Instance Peer ID and public addresses.
_Avoid_: Relay Instance, bootstrap peer

**Relay Discovery Document**:
A short-lived response identifying the current Relay Instance by Peer ID and its complete public multiaddrs. It contains reachability information rather than Relay Account or Device Identity data.
_Avoid_: Relay assignment, relay profile

**Public Address Snapshot**:
The complete atomic set of public multiaddrs currently publishable for every enabled Relay Instance transport. It is derived from configured routing inputs, explicit per-transport overrides, and observed Kubernetes Service status, and remains usable only within a bounded staleness window.
_Avoid_: Relay identity, address registry

**Relay Configuration**:
One Clipp installation's local instructions for connecting to exactly one relay. Its authenticated form identifies one exact canonical HTTPS Relay Discovery Endpoint and has a separate configuration-scoped credential relationship; its unauthenticated form identifies one persistent Relay Peer ID through a non-empty set of complete multiaddrs. The forms never mix. A stable local key and optional display name identify the configuration only inside that installation; neither is a Device Identity nor a server-side account identifier. Clipp attempts every configured relay independently.
_Avoid_: Relay fallback, relay assignment
