package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func main() {
	dev := flag.Bool("dev", false, "serve the local Vite proxy over HTTPS with development defaults")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, err := loadConfig(*dev)
	if err == nil {
		err = serve(ctx, c, *dev)
	}
	if err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func serve(ctx context.Context, c Config, devTLS bool) error {
	pg, err := pgx.ParseConfig(c.DatabaseURL)
	if err != nil {
		return errors.New("invalid PostgreSQL connection settings")
	}
	db := stdlib.OpenDB(*pg)
	defer db.Close()
	db.SetMaxOpenConns(100)
	db.SetMaxIdleConns(10)
	db.SetConnMaxIdleTime(5 * time.Minute)
	s, err := newServer(ctx, c, db)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	handler, closeTelemetry, err := setupTelemetry(ctx, c, compressResponse(s.routes()))
	if err != nil {
		return err
	}
	defer closeTelemetry()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 32 << 10}
	if devTLS {
		server.TLSConfig, err = developmentTLS()
		if err != nil {
			return err
		}
	}
	workers, stopWorkers := context.WithCancel(context.Background())
	var group sync.WaitGroup
	group.Go(func() { s.rejections.run(workers) })
	group.Go(func() { s.Ingest.Run(workers) })
	group.Go(func() { s.runBilling(workers) })
	defer func() { stopWorkers(); group.Wait() }()
	ended := make(chan error, 1)
	go func() {
		if devTLS {
			ended <- server.ServeTLS(listener, "", "")
		} else {
			ended <- server.Serve(listener)
		}
	}()
	slog.Info("listening", "address", listener.Addr(), "region", c.Region)
	select {
	case err := <-ended:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		server.Close()
		return err
	}
	return nil
}
func newServer(ctx context.Context, c Config, db *sql.DB) (*Server, error) {
	if err := migratePostgres(ctx, db); err != nil {
		return nil, fmt.Errorf("initialize PostgreSQL: %w", err)
	}
	a, err := newAnalytics(c)
	if err != nil {
		return nil, err
	}
	if err = a.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("initialize analytics: %w", err)
	}
	s := &Server{Config: c, DB: db, Analytics: a}
	s.Ingest, err = newIngestion(s)
	return s, err
}
