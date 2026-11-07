package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestBatch9ConfigDefaultsAndModeBoundaries(t *testing.T) {
	for _, name := range []string{"API_BASE_URL", "POSTGRES_DSN", "TENANT", "ADMIN_USER", "ADMIN_PASSWORD", "GRADER_USER"} {
		t.Setenv("EDUGRADE_IMPORT_"+name, "")
	}
	tests := []struct {
		name, password, wantError string
		args                      []string
	}{
		{name: "defaults", password: "test-password"},
		{name: "missing_password", wantError: "EDUGRADE_IMPORT_ADMIN_PASSWORD or -admin-password is required"},
		{name: "negative_ai_limit", password: "test-password", args: []string{"-ai-limit=-1"}, wantError: "ai-limit cannot be negative"},
		{name: "zero_ai_limit", password: "test-password", args: []string{"-ai-limit=0"}},
		{name: "ai_only", password: "test-password", args: []string{"-ai-only"}},
		{name: "repair_only", password: "test-password", args: []string{"-repair-crops"}},
		{name: "reconcile_only", password: "test-password", args: []string{"-reconcile-ocr-runtime"}},
		{name: "ai_and_repair", password: "test-password", args: []string{"-ai-only", "-repair-crops"}, wantError: "cannot be used together"},
		{name: "ai_and_reconcile", password: "test-password", args: []string{"-ai-only", "-reconcile-ocr-runtime"}, wantError: "cannot be used together"},
		{name: "repair_and_reconcile", password: "test-password", args: []string{"-repair-crops", "-reconcile-ocr-runtime"}, wantError: "cannot be used together"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalFlags, originalArgs := flag.CommandLine, os.Args
			defer func() { flag.CommandLine, os.Args = originalFlags, originalArgs }()
			flag.CommandLine = flag.NewFlagSet("importer", flag.ContinueOnError)
			os.Args = append([]string{"importer"}, test.args...)
			t.Setenv("EDUGRADE_IMPORT_ADMIN_PASSWORD", test.password)
			cfg, err := parseConfig()
			assertBatch9Error(t, err, test.wantError)
			if test.name == "defaults" {
				assertBatch9ConfigDefaults(t, cfg)
			}
			if test.name == "zero_ai_limit" && cfg.aiLimit != 0 {
				t.Fatalf("zero AI limit = %d", cfg.aiLimit)
			}
			logBatch9Proof(t, test.name, map[string]any{
				"error": batch9ErrorText(err), "apiBase": cfg.apiBaseURL, "dsn": cfg.postgresDSN,
				"tenant": cfg.tenantCode, "admin": cfg.adminUser, "grader": cfg.graderUser,
				"dataset": cfg.datasetDir, "result": cfg.resultPath, "aiLimit": cfg.aiLimit,
				"aiOnly": cfg.aiOnly, "repair": cfg.repairCrops, "reconcile": cfg.reconcileOCRRuntime,
			})
		})
	}
}

func assertBatch9ConfigDefaults(t *testing.T, cfg config) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	datasetDir := filepath.Join(root, "lab", "evals", "synthetic", datasetID)
	assertBatch9ConnectionDefaults(t, cfg)
	assertBatch9IdentityDefaults(t, cfg)
	assertBatch9DatasetModeDefaults(t, cfg, datasetDir)
}

func assertBatch9ConnectionDefaults(t *testing.T, cfg config) {
	t.Helper()
	if cfg.apiBaseURL != "http://127.0.0.1:8080" || cfg.postgresDSN != "postgres://edugrade:change_me_postgres_demo@127.0.0.1:5432/edugrade?sslmode=disable" {
		t.Fatalf("defaults = %#v", cfg)
	}
}

func assertBatch9IdentityDefaults(t *testing.T, cfg config) {
	t.Helper()
	if cfg.tenantCode != "demo" || cfg.adminUser != "story053_admin" || cfg.graderUser != "test_grader" {
		t.Fatalf("defaults = %#v", cfg)
	}
}

func assertBatch9DatasetModeDefaults(t *testing.T, cfg config, datasetDir string) {
	t.Helper()
	if cfg.aiLimit != 3 || cfg.datasetDir != datasetDir || cfg.resultPath != filepath.Join(datasetDir, "import-result.json") || cfg.aiOnly || cfg.repairCrops || cfg.reconcileOCRRuntime {
		t.Fatalf("defaults = %#v", cfg)
	}
}

func TestBatch9TemplateLayoutContract(t *testing.T) {
	ids, regions := map[string]string{}, map[string]region{}
	for number := 1; number <= 19; number++ {
		question := "Q" + strconv.Itoa(number)
		ids[question] = "question-" + question
		regions[question] = region{X: float64(number) / 100, Y: .2, Width: .3, Height: .4}
	}
	layout := buildTemplateLayout(ids, regions)
	pages := layout["pages"].([]any)
	if len(pages) != 2 {
		t.Fatalf("pages = %d", len(pages))
	}
	for index, rawPage := range pages {
		assertBatch9TemplatePage(t, index, rawPage.(map[string]any), ids, regions)
	}
	profile := layout["omr_profile"].(map[string]any)
	if !reflect.DeepEqual(profile, map[string]any{"mode": "template_difference", "version": "opencv-template-difference-bubble-v1"}) {
		t.Fatalf("profile = %#v", profile)
	}
	logBatch9Proof(t, "template", layout)
}

func assertBatch9TemplatePage(t *testing.T, index int, page map[string]any, ids map[string]string, regions map[string]region) {
	t.Helper()
	if page["page_no"] != index+1 || page["width"] != imageWidth || page["height"] != imageHeight {
		t.Fatalf("page geometry = %#v", page)
	}
	marks := page["registration_marks"].([]any)
	if marks == nil || len(marks) != 0 {
		t.Fatalf("registration marks = %#v", marks)
	}
	questionRegions := page["question_regions"].([]any)
	start, count := 1, 16
	if index == 1 {
		start, count = 17, 3
	}
	if len(questionRegions) != count {
		t.Fatalf("page %d regions = %d", index+1, len(questionRegions))
	}
	for offset, rawRegion := range questionRegions {
		number := start + offset
		question := "Q" + strconv.Itoa(number)
		actual := rawRegion.(map[string]any)
		assertBatch9RegionGeometry(t, question, actual, ids[question], regions[question])
		assertBatch9Options(t, number, question, actual)
	}
}

func assertBatch9RegionGeometry(t *testing.T, question string, actual map[string]any, questionID string, bounds region) {
	t.Helper()
	if actual["id"] != "region-"+strings.ToLower(question) || actual["label"] != question || actual["question_id"] != questionID || actual["x"] != bounds.X || actual["y"] != .2 || actual["width"] != .3 || actual["height"] != .4 {
		t.Fatalf("region %s = %#v", question, actual)
	}
}

func assertBatch9Options(t *testing.T, number int, question string, actual map[string]any) {
	t.Helper()
	if number > 10 {
		if _, exists := actual["option_regions"]; exists {
			t.Fatalf("%s unexpectedly has options", question)
		}
		return
	}
	options := actual["option_regions"].([]any)
	if len(options) != 4 {
		t.Fatalf("%s options = %d", question, len(options))
	}
	for index, label := range []string{"A", "B", "C", "D"} {
		cx := 105 + (number-1)%5*302 + 66 + index*53
		cy := 402 + (number-1)/5*160 + 26
		want := map[string]any{
			"id": strings.ToLower(question) + "-" + strings.ToLower(label), "label": label,
			"x": float64(cx-17) / imageWidth, "y": float64(cy-17) / imageHeight,
			"width": float64(34) / imageWidth, "height": float64(34) / imageHeight,
		}
		if !reflect.DeepEqual(options[index], want) {
			t.Fatalf("%s/%s = %#v, want %#v", question, label, options[index], want)
		}
	}
}
