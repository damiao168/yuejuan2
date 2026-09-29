package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/server"
)

// 主进程同时管理公网和可选内部 TLS 监听器；收到终止信号后统一执行有超时的优雅停机。
func main() {
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	logg := logger.New(os.Stdout, cfg.Service.LogLevel)
	if len(os.Args) > 1 && os.Args[1] == "bootstrap-admin" {
		if err := runBootstrapAdmin(context.Background(), cfg, logg); err != nil {
			logg.Error(context.Background(), "bootstrap admin failed", map[string]any{"error": err.Error()})
			os.Exit(1)
		}
		return
	}
	// STORY-060 账号只允许在 test 环境批量创建，避免运维参数误用于真实租户。
if len(os.Args) > 1 && os.Args[1] == "provision-story060-users" {
		if cfg.Service.Environment != "test" {
			logg.Error(context.Background(), "STORY-060 provisioning refused outside test", nil)
			os.Exit(1)
		}
		if err := runProvisionStory060Users(context.Background(), cfg); err != nil {
			logg.Error(context.Background(), "STORY-060 provisioning failed", map[string]any{"error": err.Error()})
			os.Exit(1)
		}
		return
	}
	srv, cleanup, err := server.New(cfg, logg)
	if err != nil {
		logg.Error(context.Background(), "create server failed", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
	defer cleanup()

	httpServer := &http.Server{
		Addr:              cfg.Service.Addr(),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: cfg.Service.ReadHeaderTimeout,
		ReadTimeout:       cfg.Service.ReadTimeout,
		WriteTimeout:      cfg.Service.WriteTimeout,
		IdleTimeout:       cfg.Service.IdleTimeout,
		MaxHeaderBytes:    cfg.Security.MaxHeaderBytes,
	}
	servers := []*http.Server{httpServer}
	if cfg.Service.InternalTLSPort > 0 {
		servers = append(servers, &http.Server{
			Addr:              cfg.Service.InternalTLSAddr(),
			Handler:           srv.Handler(),
			TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
			ReadHeaderTimeout: cfg.Service.ReadHeaderTimeout,
			ReadTimeout:       cfg.Service.ReadTimeout,
			WriteTimeout:      cfg.Service.WriteTimeout,
			IdleTimeout:       cfg.Service.IdleTimeout,
			MaxHeaderBytes:    cfg.Security.MaxHeaderBytes,
		})
	}

	errCh := make(chan error, len(servers))
	go func() {
		logg.Info(context.Background(), "api gateway starting", map[string]any{"addr": cfg.Service.Addr()})
		errCh <- httpServer.ListenAndServe()
	}()
	if len(servers) > 1 {
		internalServer := servers[1]
		go func() {
			logg.Info(context.Background(), "api gateway internal TLS listener starting", map[string]any{"addr": cfg.Service.InternalTLSAddr()})
			errCh <- internalServer.ListenAndServeTLS(cfg.Service.InternalTLSCertFile, cfg.Service.InternalTLSKeyFile)
		}()
	}

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-signalCh:
		logg.Info(context.Background(), "shutdown signal received", map[string]any{"signal": sig.String()})
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			logg.Error(context.Background(), "server stopped unexpectedly", map[string]any{"error": err.Error()})
			os.Exit(1)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Service.ShutdownTimeout)
	defer cancel()
	for _, activeServer := range servers {
		if err := activeServer.Shutdown(ctx); err != nil {
			logg.Error(context.Background(), "graceful shutdown failed", map[string]any{"error": err.Error(), "addr": activeServer.Addr})
			os.Exit(1)
		}
	}
	logg.Info(context.Background(), "api gateway stopped", nil)
}

func runProvisionStory060Users(ctx context.Context, cfg config.Config) error {
	ctx = db.WithTenantMaintenance(ctx)
	postgresDB, closePostgres, err := db.OpenPostgres(cfg.Postgres)
	if err != nil {
		return err
	}
	defer closePostgres()
	return auth.ProvisionStory060Users(ctx, auth.NewPostgresStore(postgresDB), os.Getenv("EDUGRADE_E2E_SCHOOL_ADMIN_PASSWORD"), os.Getenv("EDUGRADE_E2E_SUBJECTIVE_WORKER_PASSWORD"))
}

func runBootstrapAdmin(ctx context.Context, cfg config.Config, logg *logger.Logger) error {
	ctx = db.WithTenantMaintenance(ctx)
	password := os.Getenv("EDUGRADE_BOOTSTRAP_PASSWORD")
	if password == "" {
		return errors.New("EDUGRADE_BOOTSTRAP_PASSWORD is required")
	}
	postgresDB, closePostgres, err := db.OpenPostgres(cfg.Postgres)
	if err != nil {
		return err
	}
	defer closePostgres()
	result, err := auth.BootstrapInitialAdmin(ctx, auth.NewPostgresStore(postgresDB), auth.BootstrapAdminInput{
		TenantCode:  os.Getenv("EDUGRADE_BOOTSTRAP_TENANT_CODE"),
		RoleCode:    os.Getenv("EDUGRADE_BOOTSTRAP_ROLE_CODE"),
		Username:    envOrDefault("EDUGRADE_BOOTSTRAP_USERNAME", "platform_admin"),
		DisplayName: envOrDefault("EDUGRADE_BOOTSTRAP_DISPLAY_NAME", "Platform Admin"),
		Password:    password,
	})
	if err != nil {
		return err
	}
	logg.Info(ctx, "bootstrap admin completed", map[string]any{
		"tenant_code": result.TenantCode,
		"username":    result.Username,
		"role_code":   result.RoleCode,
	})
	return nil
}

func envOrDefault(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
