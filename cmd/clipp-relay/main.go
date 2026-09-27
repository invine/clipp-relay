package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"clipp-relay/internal/service"
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
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
	logger.Info("service starting", "schema_revision", database.ExpectedRevision)
	srv := service.New()
	portal := auth.New(pool, c, material, auth.Google())
	srv.SetPublicHandler(portal.Handler())
	go portal.Maintain(ctx)
	if err = srv.Run(ctx, c.Listeners.Public, c.Listeners.Private); err != nil {
		logger.Error("service stopped", "reason", "listener failure")
		os.Exit(1)
	}
}
