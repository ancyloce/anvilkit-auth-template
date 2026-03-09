package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"anvilkit-auth-template/modules/common-go/pkg/analytics"
	"anvilkit-auth-template/modules/common-go/pkg/cache/redis"
	"anvilkit-auth-template/modules/common-go/pkg/db/pgsql"
	"anvilkit-auth-template/modules/common-go/pkg/queue"
	"anvilkit-auth-template/services/email-worker/internal/config"
	"anvilkit-auth-template/services/email-worker/internal/consumer"
	"anvilkit-auth-template/services/email-worker/internal/monitoring"
	"anvilkit-auth-template/services/email-worker/internal/sender"
	"anvilkit-auth-template/services/email-worker/internal/store"
	"anvilkit-auth-template/services/email-worker/internal/webhook"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	analyticsClient, err := analytics.NewClient(cfg.Analytics)
	if err != nil {
		log.Fatal(err)
	}
	if !cfg.Analytics.Enabled {
		analyticsClient = nil
	}

	db, err := pgsql.New(ctx, cfg.DBDSN)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Fatal(err)
	}

	rdb, err := redis.New(ctx, cfg.RedisAddr)
	if err != nil {
		log.Fatal(err)
	}
	defer rdb.Close()

	q, err := queue.New(rdb)
	if err != nil {
		log.Fatal(err)
	}
	metrics, err := monitoring.NewMetrics()
	if err != nil {
		log.Fatal(err)
	}

	dataStore := &store.Store{DB: db}
	worker := &consumer.Consumer{
		Queue:     q,
		QueueName: cfg.QueueName,
		Timeout:   cfg.QueuePopTimeout,
		Sender:    sender.New(cfg.SMTPConfig()),
		Store:     dataStore,
		Analytics: analyticsClient,
		Metrics:   metrics,
	}

	webhookHandler, err := webhook.NewHandler(webhook.Server{
		Store:     dataStore,
		Secret:    cfg.WebhookSecret,
		Analytics: analyticsClient,
		Metrics:   metrics.Handler(),
	})
	if err != nil {
		log.Fatal(err)
	}
	webhookServer := &http.Server{
		Addr:              cfg.WebhookAddr,
		Handler:           webhookHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		log.Printf("email-worker webhook server started: addr=%s", cfg.WebhookAddr)
		if err := webhookServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	})
	g.Go(func() error {
		<-gctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return webhookServer.Shutdown(shutdownCtx)
	})
	g.Go(func() error {
		log.Printf("email-worker consumer started: queue=%s redis=%s", cfg.QueueName, cfg.RedisAddr)
		return worker.Run(gctx)
	})
	g.Go(func() error {
		collector := &monitoring.QueueBacklogCollector{
			Queue:        q,
			QueueName:    cfg.QueueName,
			PollInterval: cfg.QueuePollInterval,
			Metrics:      metrics,
			Logger:       log.Default(),
		}
		return collector.Run(gctx)
	})

	if err := g.Wait(); err != nil {
		log.Fatal(err)
	}
	log.Print("email-worker stopped")
}
