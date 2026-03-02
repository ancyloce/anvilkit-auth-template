package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"anvilkit-auth-template/modules/common-go/pkg/cache/redis"
	"anvilkit-auth-template/modules/common-go/pkg/db/pgsql"
	"anvilkit-auth-template/modules/common-go/pkg/queue"
	"anvilkit-auth-template/services/email-worker/internal/config"
	"anvilkit-auth-template/services/email-worker/internal/consumer"
	"anvilkit-auth-template/services/email-worker/internal/sender"
	"anvilkit-auth-template/services/email-worker/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal(err)
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

	worker := &consumer.Consumer{
		Queue:     q,
		QueueName: cfg.QueueName,
		Timeout:   cfg.QueuePopTimeout,
		Sender:    sender.New(cfg.SMTPConfig()),
		Store:     &store.Store{DB: db},
	}

	log.Printf("email-worker started: queue=%s redis=%s", cfg.QueueName, cfg.RedisAddr)
	if err := worker.Run(ctx); err != nil {
		log.Fatal(err)
	}
	log.Print("email-worker stopped")
}
