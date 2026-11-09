package main

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/yyl1212/creative/internal/httpserver"
	"github.com/yyl1212/creative/internal/model"
	"github.com/yyl1212/creative/internal/research"
	"github.com/yyl1212/creative/internal/source"
	"github.com/yyl1212/creative/internal/store"
	"github.com/yyl1212/creative/internal/worker"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	dbPath := env("CREATIVE_DB", "data/creative.db")
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	researchService, err := research.New(db)
	if err != nil {
		return err
	}
	providers, err := loadProviders(researchService, env("CREATIVE_PROVIDERS_CONFIG", "config/providers.json"))
	if err != nil {
		return err
	}
	app, err := httpserver.New(httpserver.Config{DB: db, Origin: env("CREATIVE_ADMIN_ORIGIN", "http://localhost:8080"), CSRFKey: key, ResearchOrigin: os.Getenv("CREATIVE_RESEARCH_ORIGIN"), ProductsPath: env("CREATIVE_PRODUCTS", "src/data/products.json"), TrustedProxy: os.Getenv("CREATIVE_TRUSTED_PROXY"), RuntimeModel: providers})
	if err != nil {
		return err
	}
	defer app.Close()
	reviewer, err := model.New(providers, researchService)
	if err != nil {
		return errors.New("模型配置无效；仅支持显式配置并验证的 OpenAI/DeepSeek")
	}
	verificationWorker := worker.Worker{Service: researchService, Fetcher: source.New(), Reviewer: reviewer, Maintain: func(ctx context.Context, now time.Time) error {
		return researchService.MaintenanceBackup(ctx, filepath.Join(filepath.Dir(dbPath), "backups"), now)
	}}
	server := &http.Server{Addr: env("CREATIVE_LISTEN", "127.0.0.1:8080"), Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerDone := make(chan error, 1)
	go func() {
		err := verificationWorker.Run(ctx)
		if err != nil {
			log.Print("核验工作循环停止；请检查本地数据库与服务状态")
			stop()
		}
		workerDone <- err
	}()
	defer func() { stop(); <-workerDone }()
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
func loadProviders(s *research.Service, path string) (model.Config, error) {
	saved, e := s.ModelConfig()
	if e != nil {
		return model.Config{}, e
	}
	if saved != nil {
		return saved.Config, nil
	}
	f, e := os.Open(path)
	if e != nil {
		return model.Config{}, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 16385))
	if e != nil {
		return model.Config{}, e
	}
	return research.ParseModelConfig(raw)
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
