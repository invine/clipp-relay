package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/publication"
	"clipp-relay/internal/quota"
	"clipp-relay/internal/relay"
	"clipp-relay/internal/service"
	ma "github.com/multiformats/go-multiaddr"
	"golang.org/x/sys/unix"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := diagnostic.New(os.Stdout)
	defer logger.Close()
	dependencies := diagnostic.NewTransitions(logger.Emit)
	fail := func(reason string) int {
		logger.Emit(diagnostic.Event{Event: "service.startup", Outcome: "failed", Reason: reason})
		return 1
	}
	command := flag.String("command", "", "migrate or serve")
	path := flag.String("config", "", "absolute path to versioned non-secret configuration (accounting_failure_deadline defaults to 3s, minimum 1s; restart to change)")
	flag.Parse()
	if flag.NArg() != 0 || *path == "" || (*command != "migrate" && *command != "serve") {
		logger.Emit(diagnostic.Event{Event: "service.startup", Outcome: "failed", Reason: "configuration_rejected"})
		return 2
	}
	c, err := config.Load(*path)
	if err != nil {
		return fail("configuration_rejected")
	}
	maintenance := *command == "migrate"
	var material config.Material
	if maintenance {
		material, err = c.ReadDatabaseMaterial()
	} else {
		material, err = c.ReadMaterial()
	}
	if err != nil {
		return fail("configuration_rejected")
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	forceCtx, force := context.WithCancel(context.Background())
	defer force()
	drainRequested := make(chan struct{})
	go handleSignals(signals, func() { close(drainRequested); stop() }, force)
	pool, err := database.NewPool(ctx, c, material, maintenance)
	if err != nil {
		return fail("database_unavailable")
	}
	defer pool.Close()
	startupLimit := 10 * time.Second
	if maintenance {
		startupLimit = 30 * time.Minute
	}
	startup, cancel := context.WithTimeout(ctx, startupLimit)
	defer cancel()
	if maintenance {
		if err = database.Migrate(startup, pool); err != nil {
			return fail("database_unavailable")
		}
		return 0
	}
	if err = database.VerifyServing(startup, pool); err != nil {
		return fail("database_unavailable")
	}
	debug.SetMemoryLimit(1536 << 20)
	var rlimit unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_NOFILE, &rlimit) != nil || rlimit.Cur < 16384 {
		return fail("resource_limit")
	}
	if c.RelayTCP.Listen == "" && c.RelayWebSocket.Listen == "" && c.RelayWebRTC.Listen == "" {
		return fail("configuration_rejected")
	}
	srv := service.New()
	db := database.NewRuntime(pool)
	db.SetTransitions(dependencies)
	portal := auth.NewWithRuntime(db, c, material, auth.Google())
	portal.SetDiagnostics(logger)
	portal.SetTransitions(dependencies)
	if err := portal.ValidatePepperCoverage(startup); err != nil {
		if !errors.Is(err, auth.ErrRetainedPepperUnavailable) {
			return fail("configuration_rejected")
		}
	}
	srv.SetDeletionMetrics(func(ctx context.Context) (service.DeletionSample, error) {
		observed, err := portal.SampleDeletionSignals(ctx)
		return service.DeletionSample{Pending: observed.Pending, OldestSeconds: observed.OldestAge.Seconds(), Warning: observed.Warning, Critical: observed.Critical, Completed: observed.Completed, Retried: observed.Retried}, err
	})
	srv.SetCleanupMetrics(func(ctx context.Context) (service.CleanupSample, error) {
		observed, err := portal.SampleCleanupSignals(ctx)
		return service.CleanupSample{OldestSeconds: observed.OldestAge.Seconds(), Warning: observed.Warning, Critical: observed.Critical, Breach: observed.Breach, Completed: observed.Completed, Failed: observed.Failed, BreachEpisodes: observed.BreachEpisodes}, err
	})
	if c.Journal.Region == "" {
		return fail("configuration_rejected")
	}
	{
		journal, journalErr := auth.NewOCIJournal(auth.OCIJournalOptions{
			Endpoint:  "https://objectstorage." + c.Journal.Region + ".oraclecloud.com",
			Namespace: c.Journal.Namespace, Bucket: c.Journal.Bucket,
			TenancyOCID: c.Journal.TenancyOCID, UserOCID: c.Journal.UserOCID, Fingerprint: c.Journal.Fingerprint,
			PrivateKeyPEM: material.JournalSigningKey,
		})
		if journalErr != nil {
			return fail("configuration_rejected")
		}
		portal.SetDeletionJournal(journal, c.Journal.RepositoryID, c.Journal.CoverageFloor, c.Journal.CoverageHash)
		if journalErr = portal.ValidateDeletionJournal(startup); journalErr != nil {
			return fail("journal_unavailable")
		}
	}
	credit := quota.NewWithRuntime(db)
	credit.SetDiagnostics(logger)
	if err := credit.ConfigureAccountingFailureDeadline(c.AccountingDeadline()); err != nil {
		credit.Close()
		return fail("configuration_rejected")
	}
	defer credit.Close()
	observations := diagnostic.NewObservations(logger.Emit, func(ctx context.Context) (string, error) {
		sample, err := portal.SampleCleanupSignals(ctx)
		if err != nil {
			return "", err
		}
		switch {
		case sample.Breach:
			return "breach", nil
		case sample.Critical:
			return "critical", nil
		case sample.Warning:
			return "warning", nil
		default:
			return "none", nil
		}
	}, func(ctx context.Context) (string, error) {
		sample, err := portal.SampleDeletionSignals(ctx)
		if err != nil {
			return "", err
		}
		switch {
		case sample.Critical:
			return "critical", nil
		case sample.Warning:
			return "warning", nil
		default:
			return "none", nil
		}
	})
	dataPlane, err := relay.NewProtected(portal, credit, relay.Options{ListenAddress: c.RelayTCP.Listen, WebSocketListenAddress: c.RelayWebSocket.Listen, WebRTCListenAddress: c.RelayWebRTC.Listen, WebSocketHostname: c.WSSHostname, Diagnostics: logger})
	if err != nil {
		logger.Emit(diagnostic.Event{Event: "service.serving", Outcome: "failed", Reason: "listener_failed"})
		return 1
	}
	defer dataPlane.Close()
	srv.SetRendezvousMetrics(dataPlane.RendezvousCountV1, dataPlane.RendezvousCountV2)
	decode := func(values []string) []ma.Multiaddr {
		addresses := make([]ma.Multiaddr, 0, len(values))
		for _, value := range values {
			addresses = append(addresses, ma.StringCast(value))
		}
		return addresses
	}
	publicationConfig := publication.Config{
		TCP:    publication.Transport{Enabled: c.RelayTCP.Listen != "", PublicPort: int(c.RelayTCP.PublicPort), Overrides: decode(c.RelayTCP.PublicAddresses), Service: publication.ServiceRef{Namespace: c.RelayServices.Namespace, Name: c.RelayServices.TCPName}},
		WSS:    publication.Transport{Enabled: c.RelayWebSocket.Listen != "", Overrides: decode(c.RelayWebSocket.PublicAddresses)},
		WebRTC: publication.Transport{Enabled: c.RelayWebRTC.Listen != "", PublicPort: int(c.RelayWebRTC.PublicPort), Overrides: decode(c.RelayWebRTC.PublicAddresses), Service: publication.ServiceRef{Namespace: c.RelayServices.Namespace, Name: c.RelayServices.UDPName}},
	}
	discovery, err := relay.NewDiscovery(dataPlane, portal, c.PortalOrigin[len("https://"):], nil)
	if err != nil {
		return fail("configuration_rejected")
	}
	srv.SetReadinessCheck(discovery.Published)
	srv.SetDiagnostics(logger.Emit)
	var apiClient *http.Client
	var apiURL, apiToken string
	if publicationConfig.TCP.Enabled && len(publicationConfig.TCP.Overrides) == 0 || publicationConfig.WebRTC.Enabled && len(publicationConfig.WebRTC.Overrides) == 0 {
		apiClient, apiURL, apiToken, err = publication.KubernetesAPI(c.RelayServices.TokenFile, c.RelayServices.CAFile)
		if err != nil {
			return fail("configuration_rejected")
		}
	}
	controller, err := publication.New(discovery, apiClient, apiURL, apiToken, publicationConfig, srv.SetReady)
	if err != nil {
		return fail("configuration_rejected")
	}
	controller.SetTransitions(dependencies)
	mux := http.NewServeMux()
	mux.Handle("/v1/relay", discovery)
	mux.Handle("/", portal.Handler())
	srv.SetPublicHandler(mux)
	srv.SetRouting(false)
	logger.Emit(diagnostic.Event{Event: "service.startup", Outcome: "success", Reason: "none", SchemaRevision: database.ExpectedRevision})
	go controller.Run(ctx)
	go observations.Run(ctx)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			safe, _, sampled := credit.ClockHealth()
			observations.Clock(safe, sampled, time.Now())
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	serveCtx, stopServing := context.WithCancel(context.Background())
	defer stopServing()
	go func() {
		<-drainRequested
		logger.Emit(diagnostic.Event{Event: "service.drain", Outcome: "started", Reason: "none"})
		started := time.Now()
		srv.BeginDrain()
		dataPlane.StartDrain()
		_ = discovery.Publish(nil)
		drainErr := drainWithDeadline(forceCtx, 30*time.Second, dataPlane.Drain)
		if drainErr != nil && !errors.Is(drainErr, context.DeadlineExceeded) && !errors.Is(drainErr, context.Canceled) {
			srv.SetStopFailure()
		} else {
			logger.Emit(drainResult(drainErr, time.Since(started)))
		}
		stopServing()
	}()
	go portal.Maintain(ctx)
	if err = srv.Run(serveCtx, c.Listeners.Public, c.Listeners.Private); err != nil {
		return 1
	}
	return 0
}

func handleSignals(signals <-chan os.Signal, stop, force func()) {
	<-signals
	stop()
	<-signals
	force()
}

func drainWithDeadline(forceCtx context.Context, grace time.Duration, drain func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(forceCtx, grace)
	defer cancel()
	return drain(ctx)
}

func drainResult(err error, duration time.Duration) diagnostic.Event {
	e := diagnostic.Event{Event: "service.drain", Outcome: "success", Reason: "none", Duration: duration}
	if errors.Is(err, context.DeadlineExceeded) {
		e.Outcome, e.Reason = "failed", "deadline"
	}
	if errors.Is(err, context.Canceled) {
		e.Outcome, e.Reason = "failed", "forced"
	}
	return e
}
