package main

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/db"
	"errors"
	"os"
	"strings"
)

func localWorkerInputs(environment string, get func(string) string) ([]auth.LocalWorkerInput, error) {
	if environment != "local" && environment != "development" && environment != "dev" && environment != "test" {
		return nil, errors.New("worker auto-provisioning is limited to local development")
	}
	prefixes := map[string]string{"ocr": "EDUGRADE_OCR_WORKER", "quality": "EDUGRADE_IMAGE_QUALITY", "processing": "EDUGRADE_PAGE_PROCESSING"}
	selected := strings.Split(get("EDUGRADE_PROVISION_WORKERS"), ",")
	workers := []auth.LocalWorkerInput{}
	seen := map[string]bool{}
	for _, capability := range selected {
		prefix, ok := prefixes[capability]
		if !ok || seen[capability] || get(prefix+"_TENANT_CODE") != "platform" {
			return nil, auth.ErrLocalWorkerProvision
		}
		seen[capability] = true
		workers = append(workers, auth.LocalWorkerInput{Username: get(prefix + "_USERNAME"), Password: get(prefix + "_PASSWORD")})
	}
	return workers, nil
}

func runProvisionLocalWorkers(ctx context.Context, cfg config.Config) error {
	workers, err := localWorkerInputs(cfg.Service.Environment, os.Getenv)
	if err != nil {
		return err
	}
	ctx = db.WithTenantMaintenance(ctx)
	database, closeDB, err := db.OpenPostgres(cfg.Postgres)
	if err != nil {
		return err
	}
	defer closeDB()
	return auth.NewPostgresStore(database).ProvisionLocalWorkers(ctx, workers)
}
