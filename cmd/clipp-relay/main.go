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
	go func() {
		<-signals
		stop()
		<-signals
		force()
	}()
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
	if c.RelayTCP.Listen == "" {
		logger.Error("relay TCP listener missing")
		os.Exit(1)
	}
	logger.Info("service starting", "schema_revision", database.ExpectedRevision)
	srv := service.New()
	portal := auth.New(pool, c, material, auth.Google())
	credit := quota.New(pool)
	defer credit.Close()
	dataPlane, err := relay.New(portal, credit, relay.Options{ListenAddress: c.RelayTCP.Listen})
	if err != nil {
		logger.Error("relay listener failed", "reason", err.Error())
		os.Exit(1)
	}
	defer dataPlane.Close()
	srv.SetRendezvousMetrics(dataPlane.RendezvousCountV1, dataPlane.RendezvousCountV2)
	portal.SetAccountChanged(func(change auth.AccountChange) {
		if change.DiscardCredit {
			credit.Invalidate(change.AccountID)
		} else if !change.CloseAll && credit.InvalidateAbove(change.AccountID, change.WeeklyBytes) {
			change.CloseAll = true
		}
		if change.CloseAll || dataPlane.AccountSessions(change.AccountID) > change.SessionLimit {
			dataPlane.CloseAccount(change.AccountID)
		}
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
	addresses := make([]ma.Multiaddr, 0, len(c.RelayTCP.PublicAddresses))
	for _, value := range c.RelayTCP.PublicAddresses {
		address, parseErr := ma.NewMultiaddr(value)
		if parseErr != nil {
			logger.Error("invalid public relay address")
			os.Exit(1)
		}
		addresses = append(addresses, address)
	}
	discovery, err := relay.NewDiscovery(dataPlane, portal, c.PortalOrigin[len("https://"):], addresses)
	if err != nil {
		logger.Error("relay publication failed", "reason", err.Error())
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/relay", discovery)
	mux.Handle("/", portal.Handler())
	srv.SetPublicHandler(mux)
	srv.SetReady(len(addresses) > 0)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				if err := discovery.Publish(addresses); err != nil {
					srv.SetReady(false)
				} else {
					srv.SetReady(len(addresses) > 0)
				}
			}
		}
	}()
	serveCtx, stopServing := context.WithCancel(context.Background())
	defer stopServing()
	go func() {
		<-ctx.Done()
		srv.SetReady(false)
		dataPlane.StartDrain()
		_ = discovery.Publish(nil)
		drainCtx, cancel := context.WithTimeout(forceCtx, 30*time.Second)
		defer cancel()
		_ = dataPlane.Drain(drainCtx)
		stopServing()
	}()
	go portal.Maintain(ctx)
	if err = srv.Run(serveCtx, c.Listeners.Public, c.Listeners.Private); err != nil {
		logger.Error("service stopped", "reason", "listener failure")
		os.Exit(1)
	}
}
