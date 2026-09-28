# Ticket 25 local fault evidence (2026-09-28)

Scope: isolated `codex/managed-relay-25` worktree, macOS Darwin 25.4.0 ARM64,
Go 1.27.1, Docker Engine 29.8.0. The fixture uses official `postgres:18` and
`postgres:17` disposable containers bound to `127.0.0.1`, generated TLS and
SCRAM roles, a local OCI protocol fixture, and the production fixed limit
profile. No capacity override or external load target was used. PG18 runs the
real SQL tests; PG17 is verified for migration/startup, TLS and role policy.

Commands executed (all passed unless stated):

```sh
GOCACHE=/private/tmp/clipp-go-cache bash scripts/smoke-postgres.sh
CLIPP_FAULT_RACE=1 CLIPP_FAULT_RACE_ONLY=1 GOCACHE=/private/tmp/clipp-go-cache bash scripts/smoke-postgres.sh
GOCACHE=/private/tmp/clipp-go-cache go test -race -count=1 ./internal/relay ./internal/service ./internal/publication
GOCACHE=/private/tmp/clipp-go-cache go test -count=1 ./...
GOCACHE=/private/tmp/clipp-go-cache go vet ./...
gofmt -l cmd internal
git diff --check
```

The first race attempt was **not a test result**: the filesystem sandbox denied
loopback listeners (`bind: operation not permitted`). The same command passed
with local listener permission. Docker commands likewise needed local daemon
permission. Both fixture runs removed their own containers and secrets.

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
   `TestDatabaseOutageOnlyAllowsConfirmedLocalCredit`. The smoke process also
   paused PG18 and checked local health. PG17 did not run Quota fault tests;
   process-kill at every individual SQL/commit boundary was not run.
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
   `TestRestartCancelsUnfinishedLogin`, and
   `TestDatabaseOutageAfterVerifiedProviderResponseCreatesNoFallbackState`.
   These use local provider fixtures. A real Google account/endpoints and every
   malformed claim variant were not run in this slice.
4. **Service publication and independent DB loss — partial pass.** HTTP/relay
   tests `TestServiceChangesAndOutagePublishCompleteSnapshot`,
   `TestNamedServiceWatchRepublishesOnEvent`,
   `TestOverrideRemovesOnlyItsTransportDependencyAndMissingUDPWithdrawsAll`,
   `TestFakeServiceRealClientDrainRestartAndRediscovery`, and
   `TestDrainingDiscoveryWithdrawsBeforeBackendAuth` passed in the race suite.
   PG18 pause kept `/livez` and `/readyz` responsive; public `/livez` returned
   404. A real Kubernetes API watch failure was not run.
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
   `TestTransportMemoryPressureRejectsAndRecovers`,
   `TestWebSocketNegotiationTimeoutAndStockWebRTCInFlightBound`, and
   `TestIncompleteWebRTCSetupReleasesResourcesWithinTenSeconds`. The 512 socket
   cap, slow HTTP readers, simultaneous saturation of every auth/Rendezvous/
   session/DB/RM gate, and quantitative recovery under an attacker workload
   were not run.
6. **Resolved RM inventory and accounting — partial pass.**
   `TestEveryEnabledScopeMatchesAcceptedFixedLimits` checks every accepted
   named scope and zero/block-all values; `TestRelayEnablesOnlyRequiredTCPProtocols`
   checks enabled protocol inventory; `TestTransportMemoryPressureRejectsAndRecovers`
   and `TestTransportOnlyCircuitsTransferAndChargeBothDirections` exercise
   resource/traffic behavior. No independent runtime dump of all resolved RM
   scopes, no stock per-handler byte attribution measurement, and no Linux
   ARM64 representative capacity run occurred.
7. **Race and process resources — partial pass.** `go test -race` passed for
   relay replacement, revocation, drain, WebRTC setup, publication, and the
   new HTTP overload tests. The optional PG18 race pass ran
   `TestLostCommitReplyReconcilesSameReceipt`,
   `TestKnownRollbackRetriesWithoutDoubleDebit`,
   `TestInvalidatedWorkerCannotInstallLateReceipt`,
   `TestCancelledWaiterCannotUseLocalCredit`,
   `TestSuspensionWaitsForAccountRowLockAndRevokesRelayCredential`,
   `TestConcurrentRegistrationImportsRetainedUsageOnce`, and
   `TestConcurrentEquivalentLoginsCreateOneAccount`. Cleanup under race and
   measured RSS/FD/goroutine high-water and post-recovery counts were not run.
   `TestDeletionMetricsExposeOnlyBoundedAggregateSignals` and
   `TestPrivateHealthSurvivesSaturatedScrapeGate` check bounded diagnostics
   and health isolation, but do not establish reserved fairness.

This is local fault evidence, not a release or production capacity claim. The
unrun cases above remain required qualification before ticket 25 can resolve.
