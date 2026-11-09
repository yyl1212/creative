package main

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/yyl1212/creative/internal/httpserver"
	"github.com/yyl1212/creative/internal/store"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func run() error {
	key, err := hex.DecodeString(os.Getenv("CREATIVE_CSRF_KEY"))
	if err != nil || len(key) != 32 {
		return errors.New("请配置 64 位十六进制 CREATIVE_CSRF_KEY")
	}
	db, err := store.Open(env("CREATIVE_DB", "data/creative.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	app, err := httpserver.New(httpserver.Config{DB: db, Origin: env("CREATIVE_ADMIN_ORIGIN", "http://localhost:8080"), CSRFKey: key, ProductsPath: env("CREATIVE_PRODUCTS", "src/data/products.json"), TrustedProxy: os.Getenv("CREATIVE_TRUSTED_PROXY")})
	if err != nil {
		return err
	}
	defer app.Close()
	server := &http.Server{Addr: env("CREATIVE_LISTEN", "127.0.0.1:8080"), Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	log.Print(app.String())
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = server.Shutdown(shutdown); err != nil {
		server.Close()
		return err
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
