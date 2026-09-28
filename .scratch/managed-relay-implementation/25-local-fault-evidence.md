# Ticket 25 local fault evidence (2026-09-28)

Scope: isolated `codex/managed-relay-25` worktree, macOS Darwin 25.4.0 ARM64,
Go 1.27.1, Docker Engine 29.8.0. The fixture uses official `postgres:18` and
`postgres:17` disposable containers bound to `127.0.0.1`, generated TLS and
SCRAM roles, a local OCI protocol fixture, and the production fixed limit
profile. The slow-reader test runs four readers against the production
`Service.Run` listeners; no capacity override or external load target was
used. PG18 and PG17 run Quota faults.

Commands executed (all passed unless stated):

```sh
GOCACHE=/private/tmp/clipp-go-cache bash scripts/smoke-postgres.sh
CLIPP_FAULT_RACE=1 CLIPP_FAULT_RACE_ONLY=1 GOCACHE=/private/tmp/clipp-go-cache bash scripts/smoke-postgres.sh
CLIPP_FAULT_PG17_QUOTA=1 CLIPP_FAULT_RACE_ONLY=1 GOCACHE=/private/tmp/clipp-go-cache bash scripts/smoke-postgres.sh
CLIPP_FAULT_PG17_QUOTA=1 GOCACHE=/private/tmp/clipp-go-cache bash scripts/smoke-postgres.sh
GOCACHE=/private/tmp/clipp-go-cache go test -race -count=1 ./internal/relay ./internal/service ./internal/publication
GOCACHE=/private/tmp/clipp-go-cache go test -race -count=1 ./internal/service -run 'Test(DefaultPublicSocketCapAndRecovery|SlowReadersPreservePrivateHealthAndRelease)$' -v
GOCACHE=/private/tmp/clipp-go-cache go test -race -count=1 ./internal/relay -run 'Test(RelayAuthConnectionRateRejectsAndRecovers|RendezvousConnectionRateRejectsAndRecovers)$' -v
GOCACHE=/private/tmp/clipp-go-cache go test -race -count=1 ./internal/relay -run TestCustomStreamsChargeAndReleaseServiceBuffers -v
GOCACHE=/private/tmp/clipp-go-cache go test -race -count=1 ./internal/relay -run 'Test(CustomStreamsChargeAndReleaseServiceBuffers|ResourceManagerDoesNotApplyStockIPBuckets|UnknownResourceScopesBlockAtRuntime)$' -v
GOCACHE=/private/tmp/clipp-go-cache go test -race -count=1 ./internal/publication -run TestWatchFailureExpiresPublicationUntilVerifiedResync -v
GOCACHE=/private/tmp/clipp-go-cache go test -race -count=1 ./internal/relay -run 'Test(ReporterChargesCurrentPeerAssociationAndRecordsUnattributedTail|SessionCapRejectsDistinctPeerAndRecovers)$' -v
GOCACHE=/private/tmp/clipp-go-cache go test -race -count=1 ./internal/relay -run TestTransientConnectionScopeRejectsAtLimitAndRecovers -v
GOCACHE=/private/tmp/clipp-go-cache go test -count=1 ./...
GOCACHE=/private/tmp/clipp-go-cache go vet ./...
gofmt -l cmd internal
git diff --check
```

The first race attempt was **not a test result**: the filesystem sandbox denied
loopback listeners (`bind: operation not permitted`). The same command passed
with local listener permission. Docker commands likewise needed local daemon
permission. All fixture runs removed their own containers and secrets.

## Acceptance mapping

1. **PostgreSQL ambiguity, restart, outage and week waits — partial pass.** PG18
   Quota interface tests: `TestKnownRollbackRetriesWithoutDoubleDebit`,
   `TestLostCommitReplyReconcilesSameReceipt`,
   `TestReorderedOldOperationCannotBecomeNewDebit`,
   `TestRestartDoesNotRestoreOrRefundFundedCredit`,
   `TestDelayedCommitReplyCannotExtendMondayCredit`,
   `TestInvalidatedWorkerCannotInstallLateReceipt`,
   `TestLockWaitCrossingMondayUsesAfterLockWeek`,
   `TestCancelledCallerCannotInstallConfirmedCredit`, and
   `TestDatabaseOutageOnlyAllowsConfirmedLocalCredit`.
   `TestAllocationProcessCrashAtCommitBoundary` killed an actual child Go
   process before and after PostgreSQL commit on both PG17 and PG18. A fresh
   Quota instance observed 0/64 KiB durable debit at those boundaries and
   then funded 64/128 KiB respectively, without restoring unspent credit.
   The full Quota package passed on PG17 and PG18. The smoke process also
   paused PG18 and checked local health. Process-kill at every individual
   SQL statement boundary was not run.
2. **Clock boundaries and recovery — partial pass.** PG18 tests:
   `TestSkewAndUncertaintyCloseConfirmedCredit`,
   `TestClockWarningIntervalIsVisibleWithoutClosingCredit`,
   `TestDelayedCommitReplyCannotExtendMondayCredit`, and the new
   `TestClockUncertaintyIntervalBoundaries`. The new test failed before the
   interval fix and passed after it. Existing skew test checks three ordinary
   cadence recovery samples and preserved credit. `TestCancelledCallerCannotInstallConfirmedCredit`
   covers cancellation. `TestReadyChecksPublicationFreshnessAtProbeTime` and
   smoke's PG pause show readiness is independent of DB; a dedicated clock
   fault plus HTTP readiness run was not performed.
3. **Google/provider/key faults — partial pass.** PG18 Auth HTTP tests:
   `TestProviderAndBrowserFailuresDoNotCreateAccount`,
   `TestExpiredVerificationKeysCannotCompleteLogin`,
   `TestProviderCapacityRejectsWithoutQueue`,
   `TestUnknownKeyFetchHasSharedCooldown`,
   `TestOversizeAndUnavailableProviderCannotRegister`,
   `TestExpiredKnownVerificationKeyRefreshesBeforeUnknownKeyCooldown`,
   `TestCancelledSlowTokenExchangeIsNotRetried`,
   `TestRestartCancelsUnfinishedLogin`, and
   `TestDatabaseOutageAfterVerifiedProviderResponseCreatesNoFallbackState`.
   The new test cancels an in-flight local token endpoint response, confirms
   the callback worker exits, and rejects replay without a second provider
   call. These use complete local provider HTTP fixtures; a live Google account
   is a separate runtime integration gate, not a ticket-25 fault prerequisite.
4. **Service publication and independent DB loss — partial pass.** HTTP/relay
   tests `TestServiceChangesAndOutagePublishCompleteSnapshot`,
   `TestNamedServiceWatchRepublishesOnEvent`,
   `TestOverrideRemovesOnlyItsTransportDependencyAndMissingUDPWithdrawsAll`,
   `TestFakeServiceRealClientDrainRestartAndRediscovery`, and
   `TestDrainingDiscoveryWithdrawsBeforeBackendAuth` passed in the race suite.
   `TestWatchFailureExpiresPublicationUntilVerifiedResync` returned 503 from a
   local Kubernetes watch endpoint, advanced the publication clock, observed
   Run's expiry tick withdraw the snapshot, then used a verified Service GET
   to restore it. PG18 pause kept `/livez` and `/readyz` responsive; public
   `/livez` returned 404. No real Kubernetes API was used.
5. **Overload gates — partial pass.** New HTTP handler tests
   `TestPublicOverloadRejectsWithoutQueuingAndRecovers` (128 active, 129th
   immediate 503, recovery) and `TestPrivateHealthSurvivesSaturatedScrapeGate`
   (2 scrapes, third 503, `/livez` 200) passed under race detection. Existing
   PG18 tests `TestGlobalRateAndRequestBoundsRejectImmediately`,
   `TestProviderCapacityRejectsWithoutQueue`,
   `TestContinuationCapacityRefusesNewLogin`,
   `TestResolvedAccountRateIsBounded`, and
   `TestPoolSaturationBoundsQuotaAdmission` passed. Relay race tests include
   `TestRendezvousRejectsOversizeAndExtraFrames`,
   `TestRelayAuthConnectionRateRejectsAndRecovers`,
   `TestRendezvousConnectionRateRejectsAndRecovers`,
   `TestTransportMemoryPressureRejectsAndRecovers`,
   `TestWebSocketNegotiationTimeoutAndStockWebRTCInFlightBound`, and
   `TestIncompleteWebRTCSetupReleasesResourcesWithinTenSeconds`.
   `TestDefaultPublicSocketCapAndRecovery` held 512 real incomplete-header
   sockets, rejected the 513th, then served a new request after release.
   `TestSlowReadersPreservePrivateHealthAndRelease` held four responses at
   slow readers, kept private health responsive, and verified the handlers
   exit on disconnect. `TestSessionCapRejectsDistinctPeerAndRecovers`
   admitted one real libp2p session at a one-session test profile, rejected a
   second distinct peer, then admitted a third after release.
   `TestTransientConnectionScopeRejectsAtLimitAndRecovers` admitted 256 live
   transient RM scopes, rejected the 257th, then admitted one after release.
   Simultaneous saturation of every
   auth/Rendezvous/session/DB/RM gate was not run. Auth, Quota and
   `database.Runtime` use separate 64-unit gates, while other Auth/cleanup
   queries bypass them. This is a confirmed defect: one process-wide 64-unit
   DB admission bound is absent and must be fixed before ticket resolution.
6. **Resolved RM inventory and accounting — partial pass.**
   `TestEveryEnabledScopeMatchesAcceptedFixedLimits` checks every accepted
   named scope and zero/block-all values; `TestRelayEnablesOnlyRequiredTCPProtocols`
   checks enabled protocol inventory; `TestTransportMemoryPressureRejectsAndRecovers`
   and `TestTransportOnlyCircuitsTransferAndChargeBothDirections` exercise
   resource/traffic behavior. `TestCustomStreamsChargeAndReleaseServiceBuffers`
   opened real Auth and Rendezvous streams, observed their service scope hold
   4 KiB and 64 KiB respectively, and observed release after reset.
   `TestResourceManagerDoesNotApplyStockIPBuckets` admitted 32 concurrent RM
   connections for one non-loopback address, beyond stock 8-connection subnet
   and 16-connection rate defaults. `TestUnknownResourceScopesBlockAtRuntime`
   rejected unknown protocol and service scopes at the live RM interface.
   `TestReporterChargesCurrentPeerAssociationAndRecordsUnattributedTail`
   drove the report-time HOP/STOP callback against real authenticated sessions
   and a complete Credit interface, measuring 24 bytes to the first account,
   19 unattributed tail bytes, and 23 late bytes to a new account for the
   same peer. It also ignored 17 custom-control bytes. Stock circuits already
   assert both endpoint charges, but this callback test does not measure a
   numerical asynchronous cutoff overshoot bound (none is specified). No
   independent runtime dump of all resolved RM scopes or Linux ARM64
   representative capacity run occurred.
7. **Race and process resources — partial pass.** `go test -race` passed for
   relay replacement, revocation, drain, WebRTC setup, publication, and the
   new HTTP overload tests. The optional PG18 race pass ran
   `TestLostCommitReplyReconcilesSameReceipt`,
   `TestKnownRollbackRetriesWithoutDoubleDebit`,
   `TestInvalidatedWorkerCannotInstallLateReceipt`,
   `TestCancelledWaiterCannotUseLocalCredit`,
   `TestSuspensionWaitsForAccountRowLockAndRevokesRelayCredential`,
   `TestConcurrentRegistrationImportsRetainedUsageOnce`, and
   `TestConcurrentEquivalentLoginsCreateOneAccount`,
   `TestMaintenanceKeepsLiveGrantAndRetentionBoundaries`, and
   `TestCleanupErrorDoesNotStarveLaterRetentionClass`. The 512-socket test under
   race detection sampled before/at-limit/after: goroutines 2/520/4, open FDs
   5/1033/6, Go heap bytes 349744/6460480/6651000, and process RSS KiB
   30576/93008/95872. The highest observed RSS was 95872 KiB after socket
   release; these three points are not a continuous high-water trace. Client
   and server ran in one process, so FDs include both sides; heap after is not
   a retained-heap measurement. Broader concurrent cleanup/revocation stress
   was not run.
   `TestDeletionMetricsExposeOnlyBoundedAggregateSignals` and
   `TestPrivateHealthSurvivesSaturatedScrapeGate` check bounded diagnostics
   and health isolation, but do not establish reserved fairness.

This is local fault evidence, not a release or production capacity claim. The
unrun cases above remain required qualification before ticket 25 can resolve.
