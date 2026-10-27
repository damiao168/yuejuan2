// Command import-fujian-2024-math-simulation 将受治理的合成答题卡加载到本地服务栈。
// 所有模式先读取并核对合成治理标志，再打开数据库、登录并分派工作流。
// 首次导入拒绝同名已有考试；维护模式要求明确已有目标。
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type config struct {
	apiBaseURL          string
	postgresDSN         string
	tenantCode          string
	adminUser           string
	adminPass           string
	graderUser          string
	datasetDir          string
	aiLimit             int
	resultPath          string
	aiOnly              bool
	repairCrops         bool
	reconcileOCRRuntime bool
}

func main() {
	cfg, err := parseConfig()
	if err != nil {
		fatalf("%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	data, err := loadDataset(cfg.datasetDir)
	if err != nil {
		fatalf("load dataset: %v", err)
	}
	if !data.Manifest.Synthetic || !data.Manifest.SimulationOnly || data.Manifest.TrainingAllowed || data.Manifest.ContainsRealStudentID {
		fatalf("dataset governance flags are unsafe or incomplete")
	}

	db, err := sql.Open("pgx", cfg.postgresDSN)
	if err != nil {
		fatalf("open PostgreSQL: %v", err)
	}
	defer db.Close()
	if err = db.PingContext(ctx); err != nil {
		fatalf("connect PostgreSQL: %v", err)
	}

	client := &apiClient{
		baseURL: strings.TrimRight(cfg.apiBaseURL, "/"),
		// 受治理的本地 agent 可进行一次完整重试。调用方截止时间需大于两次各 360 秒的模型尝试加上传输开销。
		client: &http.Client{Timeout: 13 * time.Minute},
	}
	if err = client.login(ctx, cfg.tenantCode, cfg.adminUser, cfg.adminPass); err != nil {
		fatalf("authenticate import administrator: %v", err)
	}
	admin := nestedMap(client.loginResponse, "user")
	tenantID := stringField(admin, "tenant_id")
	adminID := stringField(admin, "id")
	if tenantID == "" || adminID == "" {
		fatalf("login response did not contain tenant/user identity")
	}
	existing, existingErr := existingExam(ctx, db, tenantID)
	if existingErr != nil {
		fatalf("look up existing exam: %v", existingErr)
	}
	if cfg.repairCrops {
		if existing == "" {
			fatalf("repair-crops mode requires an existing imported exam")
		}
		result, repairErr := repairExistingCrops(ctx, cfg, data, client, db, tenantID, existing)
		if repairErr != nil {
			fatalf("repair answer crops: %v", repairErr)
		}
		raw, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(raw))
		return
	}
	if cfg.reconcileOCRRuntime {
		if existing == "" {
			fatalf("reconcile-ocr-runtime mode requires an existing imported exam")
		}
		result, reconcileErr := reconcileExistingOCRRuntime(ctx, db, tenantID, adminID, existing)
		if reconcileErr != nil {
			fatalf("reconcile OCR runtime tasks: %v", reconcileErr)
		}
		raw, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(raw))
		return
	}
	if cfg.aiOnly {
		if existing == "" {
			fatalf("ai-only mode requires an existing imported exam")
		}
		result, aiErr := addExistingAISuggestions(ctx, cfg, client, db, tenantID, existing)
		if aiErr != nil {
			fatalf("add governed AI suggestions: %v", aiErr)
		}
		raw, _ := json.MarshalIndent(result, "", "  ")
		if aiErr = os.WriteFile(cfg.resultPath, append(raw, '\n'), 0o644); aiErr != nil {
			fatalf("write import result: %v", aiErr)
		}
		fmt.Println(string(raw))
		return
	}
	if existing != "" {
		fatalf("exam already exists (%s); refusing to create a duplicate simulation", existing)
	}
	graderID, err := lookupUser(ctx, db, tenantID, cfg.graderUser)
	if err != nil {
		fatalf("lookup grader %q: %v", cfg.graderUser, err)
	}

	result, err := importDataset(ctx, cfg, data, client, db, importUsers{tenantID: tenantID, adminID: adminID, graderID: graderID})
	if err != nil {
		fatalf("import dataset: %v", err)
	}
	raw, _ := json.MarshalIndent(result, "", "  ")
	if err = os.WriteFile(cfg.resultPath, append(raw, '\n'), 0o644); err != nil {
		fatalf("write import result: %v", err)
	}
	fmt.Println(string(raw))
}

func parseConfig() (config, error) {
	var cfg config
	flag.StringVar(&cfg.apiBaseURL, "api-base", envOr("EDUGRADE_IMPORT_API_BASE_URL", "http://127.0.0.1:8080"), "API base URL")
	flag.StringVar(&cfg.postgresDSN, "postgres-dsn", envOr("EDUGRADE_IMPORT_POSTGRES_DSN", "postgres://edugrade:change_me_postgres_demo@127.0.0.1:5432/edugrade?sslmode=disable"), "PostgreSQL DSN")
	flag.StringVar(&cfg.tenantCode, "tenant", envOr("EDUGRADE_IMPORT_TENANT", "demo"), "tenant code")
	flag.StringVar(&cfg.adminUser, "admin-user", envOr("EDUGRADE_IMPORT_ADMIN_USER", "story053_admin"), "administrator username")
	flag.StringVar(&cfg.adminPass, "admin-password", os.Getenv("EDUGRADE_IMPORT_ADMIN_PASSWORD"), "administrator password")
	flag.StringVar(&cfg.graderUser, "grader-user", envOr("EDUGRADE_IMPORT_GRADER_USER", "test_grader"), "grader username")
	flag.StringVar(&cfg.datasetDir, "dataset", "", "dataset directory")
	flag.IntVar(&cfg.aiLimit, "ai-limit", 3, "maximum real subjective AI suggestions to create")
	flag.StringVar(&cfg.resultPath, "result", "", "import result JSON path")
	flag.BoolVar(&cfg.aiOnly, "ai-only", false, "add governed AI suggestions to an existing imported exam")
	flag.BoolVar(&cfg.repairCrops, "repair-crops", false, "replace full-page segment evidence with per-question crops")
	flag.BoolVar(&cfg.reconcileOCRRuntime, "reconcile-ocr-runtime", false, "idempotently reconcile completed OCR sources with Worker Runtime")
	flag.Parse()

	if cfg.adminPass == "" {
		return cfg, errors.New("EDUGRADE_IMPORT_ADMIN_PASSWORD or -admin-password is required")
	}
	if cfg.datasetDir == "" {
		root, err := filepath.Abs(filepath.Join("..", "..", ".."))
		if err != nil {
			return cfg, err
		}
		cfg.datasetDir = filepath.Join(root, "lab", "evals", "synthetic", datasetID)
	}
	absolute, err := filepath.Abs(cfg.datasetDir)
	if err != nil {
		return cfg, err
	}
	cfg.datasetDir = absolute
	if cfg.resultPath == "" {
		cfg.resultPath = filepath.Join(cfg.datasetDir, "import-result.json")
	}
	if cfg.aiLimit < 0 {
		return cfg, errors.New("ai-limit cannot be negative")
	}
	modeCount := 0
	for _, enabled := range []bool{cfg.aiOnly, cfg.repairCrops, cfg.reconcileOCRRuntime} {
		if enabled {
			modeCount++
		}
	}
	if modeCount > 1 {
		return cfg, errors.New("ai-only, repair-crops and reconcile-ocr-runtime cannot be used together")
	}
	return cfg, nil
}

func lookupUser(ctx context.Context, db *sql.DB, tenantID, username string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `
SELECT id::text FROM app_user
WHERE tenant_id=$1::uuid AND username=$2 AND status='active' AND deleted_at IS NULL
`, tenantID, username).Scan(&id)
	return id, err
}

func existingExam(ctx context.Context, db *sql.DB, tenantID string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `
SELECT id::text FROM exam WHERE tenant_id=$1::uuid AND name=$2 AND deleted_at IS NULL
`, tenantID, examName).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
