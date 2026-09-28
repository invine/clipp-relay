# Ticket 25 local fault evidence (2026-09-28–29)

Scope: isolated `codex/managed-relay-25` worktree, macOS Darwin 25.4.0 ARM64,
Go 1.27.1, Docker Engine 29.8.0. The fixture uses official `postgres:18` and
`postgres:17` disposable containers bound to `127.0.0.1`, generated TLS and
SCRAM roles, a local OCI protocol fixture, and the production fixed limit
profile. The slow-reader test runs four readers against the production
`Service.Run` listeners; no capacity override or external load target was
used. PG18 and PG17 run full Account/Auth and Quota faults in the final
integrated matrix.
The local image binaries reported PostgreSQL 18.6
(`postgres@sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722`)
and 17.11
(`postgres@sha256:d74eeac9a635390a49bc21bd49fccd973de707e2a53a76ac49b552b8712ec46f`).

Final integration: `codex/managed-relay-integration` at `808a3e6` includes the
shared DB admission/runtime corrections, the Monday backward-regression fix,
provider-grant and clock/readiness tests, and an opt-in PG17 full Auth run.
The final disposable matrix on this branch passed with:

```sh
CLIPP_FAULT_PG17_AUTH=1 CLIPP_FAULT_PG17_QUOTA=1 CLIPP_FAULT_RACE=1 GOCACHE=/private/tmp/clipp-go-build-cache bash scripts/smoke-postgres.sh
GOCACHE=/private/tmp/clipp-go-build-cache go test -race -count=1 ./internal/relay ./internal/service ./internal/publication
GOCACHE=/private/tmp/clipp-go-build-cache go test -count=1 ./...
GOCACHE=/private/tmp/clipp-go-build-cache go vet ./...
git diff --check
```

This ran full Auth and Quota packages against both supported PostgreSQL majors,
focused PG18 race checks, verified-TLS migration/serve, private health/public
isolation, DB outage, role/schema/ownership checks, PostgreSQL 16 rejection, and
wrong CA/hostname rejection. The PG17 service is stopped before Auth tests so
its live deletion maintenance cannot race test-created deletion operations.
The first experimental PG17 Auth run failed three deletion tests while that
service shared the fixture; the corrected fixture ordering passed all three.
The test fixture cleaned its containers and secrets after both runs.

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

1. **PostgreSQL ambiguity, restart, outage and week waits — pass.** PG18
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
   SQL statement boundary was not run; pre/post-commit kill, rollback,
   ambiguous reply and late-install seams cover the accepted outcomes.
2. **Clock boundaries and recovery — pass.** PG18/17 tests:
   `TestSkewAndUncertaintyCloseConfirmedCredit`,
   `TestClockWarningIntervalIsVisibleWithoutClosingCredit`,
   `TestDelayedCommitReplyCannotExtendMondayCredit`, and the new
   `TestClockUncertaintyIntervalBoundaries`. The new test failed before the
   interval fix and passed after it. Existing skew test checks three ordinary
   cadence recovery samples and preserved credit. `TestCancelledCallerCannotInstallConfirmedCredit`
   covers cancellation. `TestSmallBackwardClockStepAcrossMondayRefusesConfirmedCredit`
   first failed against PG18: a 2-second regression into Sunday spent Monday
   credit. The integrated fix rejects local credit from a different week;
   `TestSmallBackwardClockStepBeforeInstallDiscardsMondayCredit` also rejects
   installing an already committed receipt after that regression. Both pass on
   PG17/18. `TestClockUnavailabilityDoesNotWithdrawHTTPReadiness` checks that
   allocation closes while `/readyz` follows publication and `/livez` remains
   healthy. The normal probe loop is 60 seconds; interval-boundary tests inject
   response delay/uncertainty and check the ±1s/±5s thresholds.
3. **Google/provider/key faults — pass.** PG18/17 Auth HTTP tests:
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
   call. `TestProviderOutagePreservesIndependentGrant` mints an Android grant,
   closes the local provider, rejects a new login, and verifies existing access
   and refresh still work. These use complete local provider HTTP fixtures; a
   live Google account is a separate runtime integration gate.
4. **Service publication and independent DB loss — pass.** HTTP/relay
   tests `TestServiceChangesAndOutagePublishCompleteSnapshot`,
   `TestNamedServiceWatchRepublishesOnEvent`,
   `TestOverrideRemovesOnlyItsTransportDependencyAndMissingUDPWithdrawsAll`,
   `TestFakeServiceRealClientDrainRestartAndRediscovery`, and
   `TestDrainingDiscoveryWithdrawsBeforeBackendAuth` passed in the race suite.
   `TestWatchFailureExpiresPublicationUntilVerifiedResync` returned 503 from a
   local Kubernetes watch endpoint, advanced the publication clock, observed
   Run's expiry tick withdraw the snapshot, then used a verified Service GET
   to restore it. PG18 pause kept `/livez` and `/readyz` responsive; public
   `/livez` returned 404. No real Kubernetes API was used; live cluster
   qualification belongs to the installation/deployment tickets.
5. **Overload gates — pass.** New HTTP handler tests
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
   Integrated corrections share one 64-unit serving DB admission budget across
   Account, Quota, Relay and cleanup, enter it before logical guards, cap pool
   acquisition at 500ms and unit lifetime at 3s, and release slots after rows
   or transactions finish. `TestOneServingBudgetCoversAccountQuotaAndCleanup`,
   `TestRuntimeWorkAdmissionIsSharedAndRejectsOverflow`, and the full PG17/18
   Auth/Quota matrix cover saturation, overflow and recovery. Gates were
   saturated independently with malformed inputs and slow readers using the
   recorded production or smaller test profiles; no simultaneous all-gates
   capacity claim is made.
6. **Resolved RM inventory and accounting — pass.**
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
   representative capacity run occurred; target-scale capacity is deferred
   ticket 33.
7. **Race and process resources — pass.** `go test -race` passed for
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

This is local fault evidence, not a release or production capacity claim.
External Google, Kubernetes, OCI deployment, release-image, and target-scale
capacity qualification remain with their own downstream tickets; they were not
run for ticket 25.
