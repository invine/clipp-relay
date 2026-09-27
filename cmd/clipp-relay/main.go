package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"clipp-relay/internal/publication"
	"clipp-relay/internal/quota"
	"clipp-relay/internal/relay"
	"clipp-relay/internal/service"
	ma "github.com/multiformats/go-multiaddr"
	"golang.org/x/sys/unix"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	command := flag.String("command", "", "migrate or serve")
	path := flag.String("config", "", "absolute path to versioned non-secret configuration")
	flag.Parse()
	if flag.NArg() != 0 || *path == "" || (*command != "migrate" && *command != "serve") {
		logger.Error("invalid invocation")
		os.Exit(2)
	}
	c, err := config.Load(*path)
	if err != nil {
		logger.Error("configuration rejected", "reason", err.Error())
		os.Exit(1)
	}
	maintenance := *command == "migrate"
	var material config.Material
	if maintenance {
		material, err = c.ReadDatabaseMaterial()
	} else {
		material, err = c.ReadMaterial()
	}
	if err != nil {
		logger.Error("Secret material rejected", "reason", err.Error())
		os.Exit(1)
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	forceCtx, force := context.WithCancel(context.Background())
	defer force()
	go handleSignals(signals, stop, force)
	pool, err := database.NewPool(ctx, c, material, maintenance)
	if err != nil {
		logger.Error("database configuration rejected")
		os.Exit(1)
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
			logger.Error("migration failed", "reason", err.Error())
			os.Exit(1)
		}
		logger.Info("migration complete", "revision", database.ExpectedRevision)
		return
	}
	if err = database.VerifyServing(startup, pool); err != nil {
		logger.Error("startup validation failed", "reason", err.Error())
		os.Exit(1)
	}
	debug.SetMemoryLimit(1536 << 20)
	var rlimit unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_NOFILE, &rlimit) != nil || rlimit.Cur < 16384 {
		logger.Error("file descriptor soft limit below 16384")
		os.Exit(1)
	}
	if c.RelayTCP.Listen == "" && c.RelayWebSocket.Listen == "" && c.RelayWebRTC.Listen == "" {
		logger.Error("relay listener missing")
		os.Exit(1)
	}
	logger.Info("service starting", "schema_revision", database.ExpectedRevision)
	srv := service.New()
	portal := auth.New(pool, c, material, auth.Google())
	srv.SetDeletionMetrics(func(ctx context.Context) (service.DeletionSample, error) {
		observed, err := portal.SampleDeletionSignals(ctx)
		return service.DeletionSample{Pending: observed.Pending, OldestSeconds: observed.OldestAge.Seconds(), Warning: observed.Warning, Critical: observed.Critical, Completed: observed.Completed, Retried: observed.Retried}, err
	})
	if c.Journal.Region == "" {
		logger.Error("journal configuration required before serving")
		os.Exit(1)
	}
	{
		journal, journalErr := auth.NewOCIJournal(auth.OCIJournalOptions{
			Endpoint:  "https://objectstorage." + c.Journal.Region + ".oraclecloud.com",
			Namespace: c.Journal.Namespace, Bucket: c.Journal.Bucket,
			TenancyOCID: c.Journal.TenancyOCID, UserOCID: c.Journal.UserOCID, Fingerprint: c.Journal.Fingerprint,
			PrivateKeyPEM: material.JournalSigningKey,
		})
		if journalErr != nil {
			logger.Error("journal configuration rejected")
			os.Exit(1)
		}
		portal.SetDeletionJournal(journal, c.Journal.RepositoryID, c.Journal.CoverageFloor, c.Journal.CoverageHash)
		if journalErr = portal.ValidateDeletionJournal(startup); journalErr != nil {
			logger.Error("journal qualification failed")
			os.Exit(1)
		}
	}
	credit := quota.New(pool)
	defer credit.Close()
	dataPlane, err := relay.New(portal, credit, relay.Options{ListenAddress: c.RelayTCP.Listen, WebSocketListenAddress: c.RelayWebSocket.Listen, WebRTCListenAddress: c.RelayWebRTC.Listen, WebSocketHostname: c.WSSHostname})
	if err != nil {
		logger.Error("relay listener failed", "reason", err.Error())
		os.Exit(1)
	}
	defer dataPlane.Close()
	srv.SetRendezvousMetrics(dataPlane.RendezvousCountV1, dataPlane.RendezvousCountV2)
	portal.SetAccountChanged(func(change auth.AccountChange) func() {
		if change.DiscardCredit {
			credit.Invalidate(change.AccountID)
		} else if !change.CloseAll && credit.InvalidateAbove(change.AccountID, change.WeeklyBytes) {
			change.CloseAll = true
		}
		if change.CloseAll || dataPlane.AccountSessions(change.AccountID) > change.SessionLimit {
			return dataPlane.DetachAccount(change.AccountID)
		}
		return nil
	})
	portal.SetCapacitySampler(func(ctx context.Context, account string) (auth.CapacitySample, error) {
		sample, err := portal.SampleActiveGrants(ctx, account)
		if err != nil {
			return sample, err
		}
		count := dataPlane.AccountSessions(account)
		sample.LiveSessions = &count
		return sample, nil
	})
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
		logger.Error("relay publication failed", "reason", err.Error())
		os.Exit(1)
	}
	srv.SetReadinessCheck(discovery.Published)
	var apiClient *http.Client
	var apiURL, apiToken string
	if publicationConfig.TCP.Enabled && len(publicationConfig.TCP.Overrides) == 0 || publicationConfig.WebRTC.Enabled && len(publicationConfig.WebRTC.Overrides) == 0 {
		apiClient, apiURL, apiToken, err = publication.KubernetesAPI(c.RelayServices.TokenFile, c.RelayServices.CAFile)
		if err != nil {
			logger.Error("relay Service API configuration failed", "reason", err.Error())
			os.Exit(1)
		}
	}
	controller, err := publication.New(discovery, apiClient, apiURL, apiToken, publicationConfig, srv.SetReady)
	if err != nil {
		logger.Error("relay publication configuration failed", "reason", err.Error())
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/relay", discovery)
	mux.Handle("/", portal.Handler())
	srv.SetPublicHandler(mux)
	srv.SetRouting(false)
	go controller.Run(ctx)
	serveCtx, stopServing := context.WithCancel(context.Background())
	defer stopServing()
	go func() {
		<-ctx.Done()
		srv.SetReady(false)
		dataPlane.StartDrain()
		_ = discovery.Publish(nil)
		_ = drainWithDeadline(forceCtx, 30*time.Second, dataPlane.Drain)
		stopServing()
	}()
	go portal.Maintain(ctx)
	if err = srv.Run(serveCtx, c.Listeners.Public, c.Listeners.Private); err != nil {
		logger.Error("service stopped", "reason", "listener failure")
		os.Exit(1)
	}
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
