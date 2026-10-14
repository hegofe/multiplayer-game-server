package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"aviator/internal/api"
	"aviator/internal/config"
	"aviator/internal/game"
	"aviator/internal/payout"
	"aviator/internal/store"
	"aviator/internal/wallet"
)

const redisWait = 30 * time.Second

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}

	signals, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	client := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer client.Close()
	if err := waitForRedis(signals, client, log); err != nil {
		return err
	}

	funds := wallet.New(cfg.Users, cfg.InitialBalanceCents)
	rounds := store.NewRedisStore(client)
	writer := store.NewBatchBetWriter(client, cfg.BatchMaxSize, cfg.BatchMaxWait)
	payer := payout.NewPayer(rounds, funds, log)
	table := game.NewTable(game.TableConfig{
		RoundPause:        cfg.RoundPause,
		BettingWindow:     cfg.BettingWindow,
		PreFlightWait:     cfg.PreFlightWait,
		TestBettingWindow: cfg.TestBettingWindow,
		FlightGrowth:      cfg.FlightGrowth,
		QueueSize:         cfg.TableQueueSize,
	}, rounds, writer, funds, game.RandomLanding{Min: game.Multiplier(cfg.LandingMin), Max: game.Multiplier(cfg.LandingMax)}, payer, log)

	go writer.Run()
	tableCtx, stopTable := context.WithCancel(context.Background())
	defer stopTable()
	go table.Run(tableCtx)

	handler := api.New(table, rounds, funds)
	srv := &http.Server{Addr: ":" + cfg.Port, Handler: handler.Handler(), ReadHeaderTimeout: 5 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	log.Info("server started", "port", cfg.Port, "users", cfg.Users, "redis", cfg.RedisAddr)

	select {
	case <-signals.Done():
		log.Info("shutdown requested")
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	return shutdown(cfg.ShutdownTimeout, log, handler, srv, stopTable, table, writer, payer)
}

func waitForRedis(ctx context.Context, client *redis.Client, log *slog.Logger) error {
	deadline := time.Now().Add(redisWait)
	for {
		err := client.Ping(ctx).Err()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		log.Warn("waiting for redis", "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func shutdown(timeout time.Duration, log *slog.Logger, handler *api.Server, srv *http.Server,
	stopTable context.CancelFunc, table *game.Table, writer *store.BatchBetWriter, payer *payout.Payer) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	handler.SetShuttingDown()
	log.Info("shutdown 1/5: health reports unavailable")

	if err := srv.Shutdown(ctx); err != nil {
		log.Error("http server did not stop cleanly", "error", err)
	}
	log.Info("shutdown 2/5: http server stopped, in-flight requests finished")

	stopTable()
	<-table.Done()
	log.Info("shutdown 3/5: table stopped")

	writer.Close()
	log.Info("shutdown 4/5: pending bets written to redis")

	if err := payer.Wait(ctx); err != nil {
		log.Error("payouts still running at shutdown deadline", "error", err)
		return err
	}
	log.Info("shutdown 5/5: payouts finished")
	return nil
}
