package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/files"
)

// 文件对账默认只报告孤儿对象和待修复状态；--repair 由运维明确开启且不会删除对象。
func main() {
	envFile := flag.String("env-file", "", "optional dotenv configuration file")
	repair := flag.Bool("repair", false, "explicitly repair safe metadata/saga states; never deletes orphan objects")
	batchSize := flag.Int("batch-size", 0, "asset batch size (max 1000)")
	objectLimit := flag.Int("object-limit", 0, "maximum objects to compare (max 10000)")
	staleAfter := flag.Duration("stale-after", 0, "age after which pending state is reported")
	flag.Parse()

	cfg, err := config.Load(*envFile)
	if err != nil {
		fatal(err)
	}
	database, closeDatabase, err := db.OpenPostgres(cfg.Postgres, db.QueryObserver{})
	if err != nil {
		fatal(err)
	}
	defer closeDatabase()
	objects, err := files.NewMinIOObjectStorage(cfg.MinIO)
	if err != nil {
		fatal(err)
	}
	if *batchSize == 0 {
		*batchSize = cfg.Files.ReconciliationBatchSize
	}
	if *objectLimit == 0 {
		*objectLimit = cfg.Files.ReconciliationObjectLimit
	}
	if *staleAfter == 0 {
		*staleAfter = cfg.Files.ReconciliationStaleAfter
	}
	ctx, cancel := context.WithTimeout(db.WithTenantMaintenance(context.Background()), 2*time.Hour)
	defer cancel()
	run, err := files.NewReconciler(database, objects).Run(ctx, files.ReconciliationOptions{
		Bucket: cfg.Files.Bucket, BatchSize: *batchSize, ObjectScanLimit: *objectLimit,
		StaleAfter: *staleAfter, Repair: *repair,
	})
	if err != nil {
		fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(run); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, "file reconciliation failed:", err)
	os.Exit(1)
}
