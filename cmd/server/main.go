package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"go.uber.org/zap"

	"github.com/vovaki-coger/al-royter/internal/auth"
	"github.com/vovaki-coger/al-royter/internal/config"
	"github.com/vovaki-coger/al-royter/internal/models"
	"github.com/vovaki-coger/al-royter/internal/providers"
	"github.com/vovaki-coger/al-royter/internal/router"
	"github.com/vovaki-coger/al-royter/internal/storage"
)

func toProviderConfigs(cfgs []config.ProviderConfig) []providers.ProviderConfig {
	result := make([]providers.ProviderConfig, len(cfgs))
	for i, c := range cfgs {
		result[i] = providers.ProviderConfig{
			Name:            c.Name,
			APIBase:         c.APIBase,
			ModelsEndpoint:  c.ModelsEndpoint,
			AuthType:        c.AuthType,
		}
	}
	return result
}

func main() {
	_ = godotenv.Load()

	logger, _ := zap.NewProduction()
	defer logger.Sync()
	zap.ReplaceGlobals(logger)

	cfg, err := config.Load("config.yaml")
	if err != nil {
		logger.Fatal("Failed to load config", zap.Error(err))
	}

	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	store, err := storage.New(storage.DatabaseConfig{
		Type: cfg.Database.Type,
		Path: cfg.Database.Path,
		URL:  cfg.Database.URL,
	})
	if err != nil {
		logger.Fatal("Failed to init storage", zap.Error(err))
	}
	defer store.Close()

	providerManager := providers.NewManager(toProviderConfigs(cfg.Providers), logger)
	modelRegistry := models.NewRegistry(store, providerManager, logger)
	if err := modelRegistry.LoadFromStorage(); err != nil {
		logger.Warn("Failed to load models from storage", zap.Error(err))
	}
	authManager := auth.NewManager(&cfg.JWT, store, logger)
	googleOAuth := auth.NewGoogleOAuth(cfg.Google, store, authManager, logger)
	routerEngine := router.New(cfg, store, modelRegistry, providerManager, authManager, googleOAuth, logger)

	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			modelRegistry.SyncAll(ctx)
			cancel()
		}
	}()

	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := store.CleanExpiredSessions(); err != nil {
				logger.Warn("Failed to clean expired sessions", zap.Error(err))
			}
		}
	}()

	srv := &http.Server{
		Addr:              cfg.Server.Host + ":" + cfg.Server.Port,
		Handler:           routerEngine,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("Server starting", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Server failed", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("Forced shutdown", zap.Error(err))
	}
	logger.Info("Server exited")
}