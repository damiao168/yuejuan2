// Command import-fujian-2024-math-simulation 将受治理的合成答题卡加载到运行中的本地 EduGrade 服务栈。
//
// 官方题目与答案作为有来源的参考材料保留。
// 每个考生答案、OCR 结果、OMR 结果、身份和扫描件均为合成数据，并始终明确标记。
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	answerSegmentRoutePrefix = "/api/v1/answer-segments/"
	examRoutePrefix          = "/api/v1/exams/"
	submissionRoutePrefix    = "/api/v1/submissions/"
	jsonContentType          = "application/json"
)

const (
	examName    = "2024福建中考数学真实流程仿真（Q1-Q19）"
	datasetID   = "fujian-2024-junior-math-v1"
	imageWidth  = 1654
	imageHeight = 2339
	cropPadding = 24
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

type questionDef struct {
	QuestionNo     string  `json:"question_no"`
	QuestionType   string  `json:"question_type"`
	Score          float64 `json:"score"`
	Stem           string  `json:"stem"`
	StandardAnswer any     `json:"standard_answer"`
}

type candidateAnswer struct {
	Text             any      `json:"text"`
	Confidence       float64  `json:"confidence"`
	Route            string   `json:"route"`
	ExpectedScore    *float64 `json:"expected_score"`
	NeedsHumanReview bool     `json:"needs_human_review"`
	Synthetic        bool     `json:"synthetic"`
}

type candidateDef struct {
	CandidateNo string                     `json:"candidate_no"`
	Profile     string                     `json:"profile"`
	Answers     map[string]candidateAnswer `json:"answers"`
	PageFiles   []string                   `json:"page_files"`
	Synthetic   bool                       `json:"synthetic"`
}

type region struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type manifest struct {
	Synthetic             bool           `json:"synthetic"`
	SimulationOnly        bool           `json:"simulation_only"`
	TrainingAllowed       bool           `json:"training_allowed"`
	ContainsRealStudentID bool           `json:"contains_real_student_identity"`
	Files                 map[string]any `json:"files"`
}

type dataset struct {
	Manifest   manifest
	Questions  []questionDef
	Candidates []candidateDef
	Regions    map[string]map[string]region
}

type apiClient struct {
	baseURL       string
	token         string
	loginResponse map[string]any
	client        *http.Client
}

type pageRecord struct {
	ID             string
	PageNo         int
	FileAsset      map[string]any
	Path           string
	Image          image.Image
	RegistrationID string
}

type cropRepairResult struct {
	DatasetID string `json:"dataset_id"`
	ExamID    string `json:"exam_id"`
	Found     int    `json:"found"`
	Updated   int    `json:"updated"`
	Skipped   int    `json:"skipped"`
}

type ocrRuntimeRepairResult struct {
	DatasetID  string `json:"dataset_id"`
	ExamID     string `json:"exam_id"`
	Found      int    `json:"found"`
	Reconciled int    `json:"reconciled"`
}

type importResult struct {
	DatasetID                 string            `json:"dataset_id"`
	ExamID                    string            `json:"exam_id"`
	ExamName                  string            `json:"exam_name"`
	ExamStatus                string            `json:"exam_status"`
	TeacherUsername           string            `json:"teacher_username"`
	TeacherUserID             string            `json:"teacher_user_id"`
	CandidateCount            int               `json:"candidate_count"`
	PageCount                 int               `json:"page_count"`
	QuestionCount             int               `json:"question_count"`
	AnswerSegmentCount        int               `json:"answer_segment_count"`
	CompletedOCRTaskCount     int               `json:"completed_ocr_task_count"`
	SeededOCRResultCount      int               `json:"seeded_ocr_result_count"`
	AutoConfirmedGradeCount   int               `json:"auto_confirmed_grade_count"`
	AssignedReviewTaskCount   int               `json:"assigned_review_task_count"`
	SubjectiveAISuggestionIDs []string          `json:"subjective_ai_suggestion_ids"`
	SourceAssetIDs            map[string]string `json:"source_asset_ids"`
	Synthetic                 bool              `json:"synthetic"`
	SimulationOnly            bool              `json:"simulation_only"`
	TrainingAllowed           bool              `json:"training_allowed"`
	ContainsRealStudentID     bool              `json:"contains_real_student_identity"`
	AutomationDisclosure      map[string]any    `json:"automation_disclosure"`
	CreatedAt                 string            `json:"created_at"`
}

type importUsers struct {
	tenantID string
	adminID  string
	graderID string
}

type registrationCreateInput struct {
	tenantID       string
	adminID        string
	captureBatchID string
	submissionID   string
	page           pageRecord
	templateID     string
	templateHash   string
	candidateNo    string
}

type registrationCompleteInput struct {
	tenantID         string
	submissionID     string
	submissionPageID string
	registrationID   string
	templateID       string
	templateHash     string
	registered       map[string]any
}

type answerCandidateCreateInput struct {
	tenantID   string
	adminID    string
	segmentID  string
	answerID   string
	question   questionDef
	answerData candidateAnswer
	extractor  string
}

type objectiveGradeConfirmation struct {
	tenantID     string
	adminID      string
	examID       string
	submissionID string
	questionID   string
	segmentID    string
	candidateID  string
	ruleID       string
	grade        map[string]any
}

type importRoster struct {
	schoolID   string
	classID    string
	studentIDs map[string]string
}

type importQuestionBindings struct {
	questionIDs map[string]string
	ruleIDs     map[string]string
	rubricIDs   map[string]string
}

type aiTarget struct {
	SegmentID string
	Label     string
}

type candidateImport struct {
	cfg               config
	data              dataset
	client            *apiClient
	db                *sql.DB
	users             importUsers
	roster            importRoster
	questions         importQuestionBindings
	examID            string
	captureBatchID    string
	templateID        string
	templateHash      string
	ocrStore          *ocrpkg.PostgresStore
	candidate         candidateDef
	candidateIndex    int
	submissionID      string
	pages             []pageRecord
	segmentByQuestion map[string]string
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

func addExistingAISuggestions(
	ctx context.Context,
	cfg config,
	client *apiClient,
	db *sql.DB,
	tenantID string,
	examID string,
) (importResult, error) {
	var result importResult
	if err := readJSON(cfg.resultPath, &result); err != nil {
		return result, fmt.Errorf("read existing import result: %w", err)
	}
	rows, err := db.QueryContext(ctx, `
SELECT seg.id::text, sub.candidate_no, q.question_no
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND sub.candidate_no='SIM-FJ2024-001'
  AND q.question_no IN ('Q17','Q18','Q19') AND seg.deleted_at IS NULL
ORDER BY q.sort_order
`, tenantID, examID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	type target struct {
		segmentID   string
		candidateNo string
		questionNo  string
	}
	targets := []target{}
	for rows.Next() {
		var item target
		if err = rows.Scan(&item.segmentID, &item.candidateNo, &item.questionNo); err != nil {
			return result, err
		}
		targets = append(targets, item)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	if len(targets) != 3 {
		return result, fmt.Errorf("found %d subjective AI targets, want 3", len(targets))
	}

	existingIDs := map[string]bool{}
	for _, id := range result.SubjectiveAISuggestionIDs {
		existingIDs[id] = true
	}
	created := 0
	for _, item := range targets {
		if created >= cfg.aiLimit {
			break
		}
		var priorID string
		_ = db.QueryRowContext(ctx, `
SELECT id::text FROM ai_grade
WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid
  AND grader_type='llm_subjective' AND status='succeeded' AND mock=false AND deleted_at IS NULL
ORDER BY created_at DESC LIMIT 1
`, tenantID, item.segmentID).Scan(&priorID)
		if priorID != "" {
			if !existingIDs[priorID] {
				result.SubjectiveAISuggestionIDs = append(result.SubjectiveAISuggestionIDs, priorID)
				existingIDs[priorID] = true
			}
			continue
		}
		response, gradeErr := client.json(ctx, http.MethodPost, answerSegmentRoutePrefix+item.segmentID+"/subjective-ai-grade", map[string]any{
			"model_policy": map[string]any{},
		}, http.StatusCreated)
		if gradeErr != nil {
			return result, fmt.Errorf("%s/%s: %w", item.candidateNo, item.questionNo, gradeErr)
		}
		grade := nestedMap(response, "grade")
		if stringField(grade, "status") != "succeeded" {
			return result, fmt.Errorf("%s/%s failed: %s", item.candidateNo, item.questionNo, stringField(grade, "failure_reason"))
		}
		id := stringField(grade, "id")
		result.SubjectiveAISuggestionIDs = append(result.SubjectiveAISuggestionIDs, id)
		existingIDs[id] = true
		created++
	}
	if result.AutomationDisclosure == nil {
		result.AutomationDisclosure = map[string]any{}
	}
	result.AutomationDisclosure["subjective"] = fmt.Sprintf(
		"首份答题卡已有%d条真实受治理智能体建议；所有主观题仍分配给教师确认。",
		len(result.SubjectiveAISuggestionIDs),
	)
	return result, nil
}

type cropRepairTarget struct {
	segmentID     string
	candidate     string
	questionNo    string
	registration  string
	cropHash      string
	cropOwnerType string
	cropOwnerID   string
}

func repairExistingCrops(
	ctx context.Context,
	cfg config,
	data dataset,
	client *apiClient,
	db *sql.DB,
	tenantID string,
	examID string,
) (cropRepairResult, error) {
	result := cropRepairResult{DatasetID: datasetID, ExamID: examID}
	rows, err := db.QueryContext(ctx, `
SELECT seg.id::text,sub.candidate_no,q.question_no,COALESCE(seg.registration_run_id::text,''),
       COALESCE(seg.crop_sha256,''),COALESCE(fa.owner_type,''),COALESCE(fa.owner_id::text,'')
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
LEFT JOIN file_asset fa ON fa.tenant_id=seg.tenant_id AND fa.id=seg.crop_file_asset_id AND fa.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND seg.deleted_at IS NULL
ORDER BY sub.candidate_no,q.sort_order
`, tenantID, examID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	targets := make([]cropRepairTarget, 0, len(data.Candidates)*len(data.Questions))
	for rows.Next() {
		var item cropRepairTarget
		if err = rows.Scan(&item.segmentID, &item.candidate, &item.questionNo, &item.registration, &item.cropHash, &item.cropOwnerType, &item.cropOwnerID); err != nil {
			return result, err
		}
		targets = append(targets, item)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	result.Found = len(targets)
	want := len(data.Candidates) * len(data.Questions)
	if result.Found != want {
		return result, fmt.Errorf("found %d answer segments, want %d", result.Found, want)
	}

	// 全部源页先解码成功，再开始修复；重复考生编号仍以后一次记录覆盖。
	candidates, pageImages, err := loadRepairPageImages(cfg.datasetDir, data.Candidates)
	if err != nil {
		return result, err
	}

	for _, item := range targets {
		content, cropErr := prepareRepairCrop(item, candidates, data.Regions, pageImages)
		if cropErr != nil {
			return result, cropErr
		}
		hash := sha256Hex(content)
		if item.cropHash == hash && item.cropOwnerType == "answer_segment_crop" && item.cropOwnerID == item.registration {
			result.Skipped++
			continue
		}
		filename := strings.ToLower(item.candidate + "-" + item.questionNo + "-crop.png")
		asset, uploadErr := client.uploadBytes(ctx, filename, content, map[string]string{
			"owner_type": "answer_segment_crop",
			"owner_id":   item.registration,
			"exam_id":    examID,
		})
		if uploadErr != nil {
			return result, fmt.Errorf("upload %s/%s crop: %w", item.candidate, item.questionNo, uploadErr)
		}
		if err = attachSegmentCrop(ctx, db, tenantID, item.segmentID, asset); err != nil {
			return result, fmt.Errorf("attach %s/%s crop: %w", item.candidate, item.questionNo, err)
		}
		result.Updated++
	}
	return result, nil
}

func prepareRepairCrop(item cropRepairTarget, candidates map[string]candidateDef, regions map[string]map[string]region, pageImages map[string][]image.Image) ([]byte, error) {
	// 先核对配准身份和源页，再生成证据；即使旧哈希匹配也不能跳过这些前提。
	if item.registration == "" {
		return nil, fmt.Errorf("segment %s has no completed registration", item.segmentID)
	}
	candidate, ok := candidates[item.candidate]
	if !ok {
		return nil, fmt.Errorf("segment references unknown candidate %s", item.candidate)
	}
	itemRegion, ok := regions[item.candidate][item.questionNo]
	if !ok {
		return nil, fmt.Errorf("missing region for %s/%s", item.candidate, item.questionNo)
	}
	pageIndex := 0
	if questionNumber(item.questionNo) >= 17 {
		pageIndex = 1
	}
	if pageIndex >= len(candidate.PageFiles) {
		return nil, fmt.Errorf("missing page %d for %s/%s", pageIndex+1, item.candidate, item.questionNo)
	}
	content, _, err := cropAnswerRegion(pageImages[item.candidate][pageIndex], itemRegion, cropPadding)
	if err != nil {
		return nil, fmt.Errorf("crop %s/%s: %w", item.candidate, item.questionNo, err)
	}
	return content, nil
}

func loadRepairPageImages(datasetDir string, definitions []candidateDef) (map[string]candidateDef, map[string][]image.Image, error) {
	candidates := make(map[string]candidateDef, len(definitions))
	pageImages := make(map[string][]image.Image, len(definitions))
	for _, candidate := range definitions {
		candidates[candidate.CandidateNo] = candidate
		images := make([]image.Image, len(candidate.PageFiles))
		for index, relativePath := range candidate.PageFiles {
			var err error
			images[index], err = decodeImage(filepath.Join(datasetDir, filepath.FromSlash(relativePath)))
			if err != nil {
				return nil, nil, fmt.Errorf("decode %s page %d: %w", candidate.CandidateNo, index+1, err)
			}
		}
		pageImages[candidate.CandidateNo] = images
	}
	return candidates, pageImages, nil
}

func reconcileExistingOCRRuntime(
	ctx context.Context,
	db *sql.DB,
	tenantID string,
	adminID string,
	examID string,
) (ocrRuntimeRepairResult, error) {
	result := ocrRuntimeRepairResult{DatasetID: datasetID, ExamID: examID}
	rows, err := db.QueryContext(ctx, `
SELECT task.id::text
FROM ocr_task task
JOIN submission sub
  ON sub.tenant_id = task.tenant_id AND sub.id = task.submission_id
WHERE task.tenant_id = $1::uuid AND sub.exam_id = $2::uuid
  AND task.status = 'completed'
  AND task.deleted_at IS NULL AND sub.deleted_at IS NULL
ORDER BY task.created_at, task.id
`, tenantID, examID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	taskIDs := []string{}
	for rows.Next() {
		var taskID string
		if err = rows.Scan(&taskID); err != nil {
			return result, err
		}
		taskIDs = append(taskIDs, taskID)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	result.Found = len(taskIDs)
	store := ocrpkg.NewPostgresStore(db)
	for _, taskID := range taskIDs {
		if _, err = store.ReconcileCompletedRuntimeTask(ctx, tenantID, adminID, taskID); err != nil {
			return result, fmt.Errorf("reconcile OCR task %s: %w", taskID, err)
		}
		result.Reconciled++
	}
	return result, nil
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

func loadDataset(dir string) (dataset, error) {
	var data dataset
	if err := readJSON(filepath.Join(dir, "manifest.json"), &data.Manifest); err != nil {
		return data, err
	}
	if err := readJSON(filepath.Join(dir, "questions.json"), &data.Questions); err != nil {
		return data, err
	}
	if err := readJSON(filepath.Join(dir, "candidates.json"), &data.Candidates); err != nil {
		return data, err
	}
	if err := readJSON(filepath.Join(dir, "regions.json"), &data.Regions); err != nil {
		return data, err
	}
	if len(data.Questions) != 19 || len(data.Candidates) < 2 {
		return data, errors.New("expected 19 questions and multiple candidates")
	}
	for _, candidate := range data.Candidates {
		if !candidate.Synthetic {
			return data, fmt.Errorf("candidate %s is not marked synthetic", candidate.CandidateNo)
		}
		if len(candidate.PageFiles) != 2 {
			return data, fmt.Errorf("candidate %s does not have two pages", candidate.CandidateNo)
		}
	}
	return data, nil
}

func importDataset(
	ctx context.Context,
	cfg config,
	data dataset,
	client *apiClient,
	db *sql.DB,
	users importUsers,
) (importResult, error) {
	tenantID, adminID, graderID := users.tenantID, users.adminID, users.graderID
	roster, err := createImportRoster(ctx, client, data.Candidates)
	if err != nil {
		return importResult{}, err
	}
	schoolID, classID := roster.schoolID, roster.classID
	examResponse, err := client.json(ctx, http.MethodPost, "/api/v1/exams", map[string]any{
		"school_id":      schoolID,
		"name":           examName,
		"subject":        "math",
		"exam_type":      "formal_exam",
		"total_score":    88,
		"grading_mode":   "ai_assisted",
		"appeal_enabled": true,
		"publish_policy": "after_admin_approval",
		"class_ids":      []string{classID},
	}, http.StatusCreated)
	if err != nil {
		return importResult{}, fmt.Errorf("create exam: %w", err)
	}
	examID := nestedStringField(examResponse, "exam", "id")
	paperID, sourceAssets, err := createImportPaperAssets(ctx, cfg, data, client, examID, schoolID)
	if err != nil {
		return importResult{}, err
	}
	baseRegions := data.Regions[data.Candidates[0].CandidateNo]
	questions, err := createImportQuestions(ctx, client, examID, paperID, data.Questions, baseRegions)
	if err != nil {
		return importResult{}, err
	}
	questionIDs := questions.questionIDs

	layout := buildTemplateLayout(questionIDs, baseRegions)
	templateResponse, err := client.json(ctx, http.MethodPost, examRoutePrefix+examID+"/answer-sheet-templates", map[string]any{
		"exam_paper_id": paperID,
		"name":          "福建中考数学匿名仿真双页答题卡 v1",
		"page_count":    2,
		"layout":        layout,
	}, http.StatusCreated)
	if err != nil {
		return importResult{}, fmt.Errorf("create answer sheet template: %w", err)
	}
	templateID := nestedStringField(templateResponse, "template", "id")
	lockedResponse, err := client.json(ctx, http.MethodPost, "/api/v1/answer-sheet-templates/"+templateID+"/lock", map[string]any{}, http.StatusOK)
	if err != nil {
		return importResult{}, fmt.Errorf("lock answer sheet template: %w", err)
	}
	templateHash := nestedStringField(lockedResponse, "template", "content_hash")
	if err = addAnswerAreaCompatibilityAliases(ctx, db, tenantID, examID); err != nil {
		return importResult{}, err
	}
	if _, err = client.json(ctx, http.MethodPost, examRoutePrefix+examID+"/readiness/confirm", map[string]any{}, http.StatusOK); err != nil {
		return importResult{}, fmt.Errorf("confirm exam readiness: %w", err)
	}
	if _, err = client.json(ctx, http.MethodPost, examRoutePrefix+examID+"/start-collection", map[string]any{}, http.StatusOK); err != nil {
		return importResult{}, fmt.Errorf("start collection: %w", err)
	}

	captureBatchID, err := createCaptureBatch(ctx, db, tenantID, examID, adminID)
	if err != nil {
		return importResult{}, err
	}

	result := importResult{
		DatasetID:                 datasetID,
		ExamID:                    examID,
		ExamName:                  examName,
		ExamStatus:                "collecting",
		TeacherUsername:           cfg.graderUser,
		TeacherUserID:             graderID,
		CandidateCount:            len(data.Candidates),
		PageCount:                 len(data.Candidates) * 2,
		QuestionCount:             len(data.Questions),
		SourceAssetIDs:            sourceAssets,
		Synthetic:                 true,
		SimulationOnly:            true,
		TrainingAllowed:           false,
		ContainsRealStudentID:     false,
		SubjectiveAISuggestionIDs: []string{},
		CreatedAt:                 time.Now().Format(time.RFC3339),
	}
	aiTargets := []aiTarget{}
	ocrStore := ocrpkg.NewPostgresStore(db)
	for candidateIndex, candidate := range data.Candidates {
		candidateRun := candidateImport{
			cfg: cfg, data: data, client: client, db: db, users: users, roster: roster,
			questions: questions, examID: examID, captureBatchID: captureBatchID,
			templateID: templateID, templateHash: templateHash, ocrStore: ocrStore,
			candidate: candidate, candidateIndex: candidateIndex,
		}
		if err = candidateRun.run(ctx, &result, &aiTargets); err != nil {
			return importResult{}, err
		}
	}
	if err = createImportAISuggestions(ctx, cfg, client, aiTargets, &result); err != nil {
		return importResult{}, err
	}
	result.AutomationDisclosure = map[string]any{
		"objective":  "高置信度选择题/填空题已通过确定性规则形成 confirmed question_grade。",
		"ocr":        "4个双页OCR任务已完成，但文本由 simulation-importer 按标注真值写入；这验证流程分流，不等同于验证真实OCR模型精度。",
		"subjective": "首份答题卡的解答题调用真实受治理智能体生成建议；所有主观题仍分配给教师确认。",
		"privacy":    "候选人、字迹、条码与扫描噪声均为匿名仿真，不含真实未成年人身份。",
	}
	return result, nil
}

func createImportRoster(ctx context.Context, client *apiClient, candidates []candidateDef) (importRoster, error) {
	schoolResponse, err := client.json(ctx, http.MethodPost, "/api/v1/schools", map[string]any{
		"name": "福建中考数学匿名仿真验证学校",
		"code": "sim-fj2024-math-v1",
	}, http.StatusCreated)
	if err != nil {
		return importRoster{}, fmt.Errorf("create school: %w", err)
	}
	schoolID := nestedStringField(schoolResponse, "school", "id")
	gradeResponse, err := client.json(ctx, http.MethodPost, "/api/v1/grades", map[string]any{
		"school_id":     schoolID,
		"name":          "九年级（匿名仿真）",
		"level_no":      9,
		"academic_year": "2024",
	}, http.StatusCreated)
	if err != nil {
		return importRoster{}, fmt.Errorf("create grade: %w", err)
	}
	classResponse, err := client.json(ctx, http.MethodPost, "/api/v1/classes", map[string]any{
		"school_id": schoolID,
		"grade_id":  nestedStringField(gradeResponse, "grade", "id"),
		"name":      "中考数学流程验证班",
		"code":      "sim-fj2024-g9",
	}, http.StatusCreated)
	if err != nil {
		return importRoster{}, fmt.Errorf("create class: %w", err)
	}
	classID := nestedStringField(classResponse, "class", "id")
	studentIDs := map[string]string{}
	for index, candidate := range candidates {
		studentResponse, studentErr := client.json(ctx, http.MethodPost, "/api/v1/students", map[string]any{
			"school_id":  schoolID,
			"class_id":   classID,
			"student_no": candidate.CandidateNo,
			"name":       fmt.Sprintf("匿名仿真考生%03d", index+1),
		}, http.StatusCreated)
		if studentErr != nil {
			return importRoster{}, fmt.Errorf("create synthetic student %s: %w", candidate.CandidateNo, studentErr)
		}
		studentIDs[candidate.CandidateNo] = nestedStringField(studentResponse, "student", "id")
	}
	return importRoster{schoolID: schoolID, classID: classID, studentIDs: studentIDs}, nil
}

func createImportPaperAssets(ctx context.Context, cfg config, data dataset, client *apiClient, examID, schoolID string) (string, map[string]string, error) {
	blankPDF := fileFromManifest(cfg.datasetDir, data.Manifest.Files, "blank_answer_sheet")
	paperAsset, err := client.upload(ctx, blankPDF, map[string]string{
		"owner_type": "exam", "owner_id": examID, "exam_id": examID, "school_id": schoolID,
	})
	if err != nil {
		return "", nil, fmt.Errorf("upload blank answer sheet: %w", err)
	}
	paperResponse, err := client.json(ctx, http.MethodPost, examRoutePrefix+examID+"/papers", map[string]any{
		"file_asset_id": stringField(paperAsset, "id"),
	}, http.StatusCreated)
	if err != nil {
		return "", nil, fmt.Errorf("create exam paper: %w", err)
	}
	paperID := nestedStringField(paperResponse, "paper", "id")

	sourceAssets := map[string]string{"blank_answer_sheet": stringField(paperAsset, "id")}
	for _, item := range []struct {
		key string
		out string
	}{
		{key: "official_questions", out: "official_questions"},
		{key: "official_answers", out: "official_answers"},
	} {
		asset, uploadErr := client.upload(ctx, fileFromManifest(cfg.datasetDir, data.Manifest.Files, item.key), map[string]string{
			"owner_type": "import", "owner_id": examID, "exam_id": examID, "school_id": schoolID,
		})
		if uploadErr != nil {
			return "", nil, fmt.Errorf("upload %s: %w", item.key, uploadErr)
		}
		sourceAssets[item.out] = stringField(asset, "id")
	}

	return paperID, sourceAssets, nil
}

func createImportQuestions(ctx context.Context, client *apiClient, examID, paperID string, definitions []questionDef, baseRegions map[string]region) (importQuestionBindings, error) {
	bindings := importQuestionBindings{questionIDs: map[string]string{}, ruleIDs: map[string]string{}, rubricIDs: map[string]string{}}
	for index, question := range definitions {
		itemRegion, ok := baseRegions[question.QuestionNo]
		if !ok {
			return importQuestionBindings{}, fmt.Errorf("missing region for %s", question.QuestionNo)
		}
		pageNo := 1
		if questionNumber(question.QuestionNo) >= 17 {
			pageNo = 2
		}
		response, createErr := client.json(ctx, http.MethodPost, examRoutePrefix+examID+"/questions", map[string]any{
			"exam_paper_id":    paperID,
			"question_no":      question.QuestionNo,
			"question_type":    question.QuestionType,
			"score":            question.Score,
			"stem":             question.Stem,
			"knowledge_points": []string{"2024福建中考数学", "真实流程仿真"},
			"answer_area": map[string]any{
				"page": pageNo,
				"x":    itemRegion.X, "y": itemRegion.Y, "w": itemRegion.Width, "h": itemRegion.Height,
			},
			"sort_order": index + 1,
			"answer_key": map[string]any{
				"standard_answer":    question.StandardAnswer,
				"equivalent_answers": []any{},
				"tolerance":          map[string]any{},
			},
		}, http.StatusCreated)
		if createErr != nil {
			return importQuestionBindings{}, fmt.Errorf("create %s: %w", question.QuestionNo, createErr)
		}
		questionID := nestedStringField(response, "question", "id")
		bindings.questionIDs[question.QuestionNo] = questionID
		ruleID, rubricID, err := createImportScoringDefinition(ctx, client, question, index, questionID)
		if err != nil {
			return importQuestionBindings{}, err
		}
		if index < 16 {
			bindings.ruleIDs[question.QuestionNo] = ruleID
		} else {
			bindings.rubricIDs[question.QuestionNo] = rubricID
		}
	}
	return bindings, nil
}

func createImportScoringDefinition(ctx context.Context, client *apiClient, question questionDef, index int, questionID string) (string, string, error) {
	// 评分定义按数据集顺序划分前 16 题；不能改为按题号判断。
	if index < 16 {
		ruleResponse, ruleErr := client.json(ctx, http.MethodPost, "/api/v1/questions/"+questionID+"/scoring-rules", map[string]any{
			"rule_type": question.QuestionType,
			"config":    map[string]any{},
		}, http.StatusCreated)
		if ruleErr != nil {
			return "", "", fmt.Errorf("create rule for %s: %w", question.QuestionNo, ruleErr)
		}
		ruleID := nestedStringField(ruleResponse, "scoring_rule", "id")
		if _, ruleErr = client.json(ctx, http.MethodPost, "/api/v1/scoring-rules/"+ruleID+"/publish", map[string]any{}, http.StatusOK); ruleErr != nil {
			return "", "", fmt.Errorf("publish rule for %s: %w", question.QuestionNo, ruleErr)
		}
		return ruleID, "", nil
	}
	rubric := rubricFor(question.QuestionNo)
	rubricResponse, rubricErr := client.json(ctx, http.MethodPost, "/api/v1/questions/"+questionID+"/rubric", rubric, http.StatusCreated)
	if rubricErr != nil {
		return "", "", fmt.Errorf("create rubric for %s: %w", question.QuestionNo, rubricErr)
	}
	return "", nestedStringField(rubricResponse, "rubric", "id"), nil
}

func (run *candidateImport) run(ctx context.Context, result *importResult, aiTargets *[]aiTarget) error {
	// 页面配准、裁剪和 OCR 播种依次完成，之后才记录答案并分流评分。
	if err := run.createPages(ctx); err != nil {
		return err
	}
	if err := run.prepareSegments(ctx, result); err != nil {
		return err
	}
	if err := run.registerPages(ctx); err != nil {
		return err
	}
	if err := run.attachCrops(ctx); err != nil {
		return err
	}
	if err := run.seedOCR(ctx, result); err != nil {
		return err
	}
	return run.recordAnswers(ctx, result, aiTargets)
}

func (run *candidateImport) createPages(ctx context.Context) error {
	candidate, client, cfg := run.candidate, run.client, run.cfg
	examID, schoolID, studentIDs := run.examID, run.roster.schoolID, run.roster.studentIDs
	submissionResponse, createErr := client.json(ctx, http.MethodPost, examRoutePrefix+examID+"/submissions", map[string]any{
		"student_id":          studentIDs[candidate.CandidateNo],
		"candidate_no":        candidate.CandidateNo,
		"source_type":         "scanner_upload",
		"expected_page_count": 2,
	}, http.StatusCreated)
	if createErr != nil {
		return fmt.Errorf("create submission %s: %w", candidate.CandidateNo, createErr)
	}
	submissionID := nestedStringField(submissionResponse, "submission", "id")
	pages := make([]pageRecord, 0, 2)
	for pageIndex, relativePath := range candidate.PageFiles {
		pageNo := pageIndex + 1
		sourcePath := filepath.Join(cfg.datasetDir, filepath.FromSlash(relativePath))
		sourceImage, decodeErr := decodeImage(sourcePath)
		if decodeErr != nil {
			return fmt.Errorf("decode %s page %d: %w", candidate.CandidateNo, pageNo, decodeErr)
		}
		sourceAsset, uploadErr := client.upload(ctx, sourcePath, map[string]string{
			"owner_type":    "submission",
			"owner_id":      submissionID,
			"submission_id": submissionID,
			"exam_id":       examID,
			"school_id":     schoolID,
		})
		if uploadErr != nil {
			return fmt.Errorf("upload %s page %d: %w", candidate.CandidateNo, pageNo, uploadErr)
		}
		pageResponse, pageErr := client.json(ctx, http.MethodPost, submissionRoutePrefix+submissionID+"/pages", map[string]any{
			"file_asset_id": stringField(sourceAsset, "id"),
			"page_no":       pageNo,
		}, http.StatusCreated)
		if pageErr != nil {
			return fmt.Errorf("add %s page %d: %w", candidate.CandidateNo, pageNo, pageErr)
		}
		pages = append(pages, pageRecord{
			ID:        nestedStringField(pageResponse, "page", "id"),
			PageNo:    pageNo,
			FileAsset: sourceAsset,
			Path:      sourcePath,
			Image:     sourceImage,
		})
	}
	run.submissionID, run.pages = submissionID, pages
	return nil
}

func (run *candidateImport) prepareSegments(ctx context.Context, result *importResult) error {
	candidate, client, submissionID, data := run.candidate, run.client, run.submissionID, run.data
	integrityResponse, integrityErr := client.json(ctx, http.MethodPost, submissionRoutePrefix+submissionID+"/quality-check", map[string]any{}, http.StatusOK)
	if integrityErr != nil {
		return fmt.Errorf("quality-check %s: %w", candidate.CandidateNo, integrityErr)
	}
	if integrityResult := nestedMap(integrityResponse, "result"); !boolField(integrityResult, "valid") {
		return fmt.Errorf("collection integrity failed for %s: %v", candidate.CandidateNo, integrityResult["issues"])
	}
	if _, createErr := client.json(ctx, http.MethodPost, submissionRoutePrefix+submissionID+"/run-quality-check", map[string]any{}, http.StatusAccepted); createErr != nil {
		return fmt.Errorf("run image quality check %s: %w", candidate.CandidateNo, createErr)
	}
	if createErr := waitForSubmissionImageQuality(ctx, client, submissionID); createErr != nil {
		return fmt.Errorf("image quality %s: %w", candidate.CandidateNo, createErr)
	}
	if _, createErr := client.json(ctx, http.MethodPost, submissionRoutePrefix+submissionID+"/status", map[string]any{"status": "ready_for_ocr", "expected_revision": 1}, http.StatusOK); createErr != nil {
		return fmt.Errorf("mark %s ready for OCR: %w", candidate.CandidateNo, createErr)
	}
	segmentResponse, segmentErr := client.json(ctx, http.MethodPost, submissionRoutePrefix+submissionID+"/segment-answers", map[string]any{}, http.StatusOK)
	if segmentErr != nil {
		return fmt.Errorf("generate segments for %s: %w", candidate.CandidateNo, segmentErr)
	}
	segments := sliceOfMaps(nestedMap(segmentResponse, "result")["segments"])
	if len(segments) != len(data.Questions) {
		return fmt.Errorf("%s generated %d segments, want %d", candidate.CandidateNo, len(segments), len(data.Questions))
	}
	result.AnswerSegmentCount += len(segments)
	segmentByQuestion := map[string]string{}
	for _, segment := range segments {
		segmentByQuestion[stringField(segment, "question_no")] = stringField(segment, "id")
	}
	run.segmentByQuestion = segmentByQuestion
	return nil
}

func (run *candidateImport) registerPages(ctx context.Context) error {
	pages, candidate, client, db := run.pages, run.candidate, run.client, run.db
	tenantID, adminID, examID := run.users.tenantID, run.users.adminID, run.examID
	captureBatchID, submissionID := run.captureBatchID, run.submissionID
	templateID, templateHash := run.templateID, run.templateHash
	for pageIndex := range pages {
		page := &pages[pageIndex]
		registrationID, registrationErr := createRegistration(ctx, db, registrationCreateInput{
			tenantID: tenantID, adminID: adminID, captureBatchID: captureBatchID,
			submissionID: submissionID, page: *page, templateID: templateID,
			templateHash: templateHash, candidateNo: candidate.CandidateNo,
		})
		if registrationErr != nil {
			return registrationErr
		}
		page.RegistrationID = registrationID
		registeredAsset, uploadErr := client.upload(ctx, page.Path, map[string]string{
			"owner_type":    "page_registration_output",
			"owner_id":      registrationID,
			"exam_id":       examID,
			"submission_id": submissionID,
		})
		if uploadErr != nil {
			return fmt.Errorf("upload registered page: %w", uploadErr)
		}
		if registrationErr = completeRegistration(ctx, db, registrationCompleteInput{
			tenantID: tenantID, submissionID: submissionID, submissionPageID: page.ID,
			registrationID: registrationID, templateID: templateID, templateHash: templateHash,
			registered: registeredAsset,
		}); registrationErr != nil {
			return registrationErr
		}
	}
	return nil
}

func (run *candidateImport) attachCrops(ctx context.Context) error {
	data, candidate, pages, client, db := run.data, run.candidate, run.pages, run.client, run.db
	segmentByQuestion, tenantID, examID, submissionID := run.segmentByQuestion, run.users.tenantID, run.examID, run.submissionID
	for _, question := range data.Questions {
		segmentID := segmentByQuestion[question.QuestionNo]
		itemRegion, ok := data.Regions[candidate.CandidateNo][question.QuestionNo]
		if !ok {
			return fmt.Errorf("missing region for %s/%s", candidate.CandidateNo, question.QuestionNo)
		}
		pageIndex := 0
		if questionNumber(question.QuestionNo) >= 17 {
			pageIndex = 1
		}
		cropContent, _, cropErr := cropAnswerRegion(pages[pageIndex].Image, itemRegion, cropPadding)
		if cropErr != nil {
			return fmt.Errorf("crop %s/%s: %w", candidate.CandidateNo, question.QuestionNo, cropErr)
		}
		filename := strings.ToLower(candidate.CandidateNo + "-" + question.QuestionNo + "-crop.png")
		cropAsset, uploadErr := client.uploadBytes(ctx, filename, cropContent, map[string]string{
			"owner_type":    "answer_segment_crop",
			"owner_id":      pages[pageIndex].RegistrationID,
			"exam_id":       examID,
			"submission_id": submissionID,
		})
		if uploadErr != nil {
			return fmt.Errorf("upload %s/%s crop: %w", candidate.CandidateNo, question.QuestionNo, uploadErr)
		}
		if cropErr = attachSegmentCrop(ctx, db, tenantID, segmentID, cropAsset); cropErr != nil {
			return fmt.Errorf("attach %s/%s crop: %w", candidate.CandidateNo, question.QuestionNo, cropErr)
		}
	}
	return nil
}

func (run *candidateImport) seedOCR(ctx context.Context, result *importResult) error {
	data, candidate, pages := run.data, run.candidate, run.pages
	ocrStore, tenantID, submissionID, adminID := run.ocrStore, run.users.tenantID, run.submissionID, run.users.adminID
	candidateIndex := run.candidateIndex
	ocrResults := []ocrpkg.ResultInput{}
	for _, question := range data.Questions {
		number := questionNumber(question.QuestionNo)
		if number <= 10 {
			continue
		}
		answerData := candidate.Answers[question.QuestionNo]
		if displayText(answerData.Text) == "" {
			continue
		}
		itemRegion := data.Regions[candidate.CandidateNo][question.QuestionNo]
		page := pages[0]
		if number >= 17 {
			page = pages[1]
		}
		ocrResults = append(ocrResults, ocrpkg.ResultInput{
			SubmissionPageID:  page.ID,
			Text:              displayText(answerData.Text),
			BBox:              []float64{itemRegion.X, itemRegion.Y, itemRegion.Width, itemRegion.Height},
			Confidence:        answerData.Confidence,
			SourceImageFileID: stringField(page.FileAsset, "id"),
		})
	}
	inputHash := sha256Hex([]byte(candidate.CandidateNo + ":" + datasetID))
	completedOCR, ocrErr := ocrStore.SeedCompletedTask(
		ctx,
		tenantID,
		submissionID,
		adminID,
		ocrpkg.CreateTaskInput{
			Engine:         "paddleocr",
			EngineVersion:  "simulation-seeded-v1",
			MinConfidence:  0.85,
			IdempotencyKey: "simulation:" + datasetID + ":" + candidate.CandidateNo + ":ocr:v1",
		},
		ocrpkg.CompleteTaskInput{
			Results:           ocrResults,
			ModelVersion:      "simulation-seeded-transcript-v1",
			ConfigHash:        sha256Hex([]byte("simulation-ocr-config-v1")),
			InputHash:         inputHash,
			DurationMS:        300 + candidateIndex*45,
			WorkerID:          "simulation-importer",
			PreprocessProfile: "synthetic-handwriting-scan-v1",
		},
	)
	if ocrErr != nil {
		return fmt.Errorf("atomically seed OCR task for %s: %w", candidate.CandidateNo, ocrErr)
	}
	result.CompletedOCRTaskCount++
	result.SeededOCRResultCount += completedOCR.ResultCount
	return nil
}

func (run *candidateImport) recordAnswers(ctx context.Context, result *importResult, aiTargets *[]aiTarget) error {
	data, candidate, segmentByQuestion := run.data, run.candidate, run.segmentByQuestion
	for _, question := range data.Questions {
		answerData, ok := candidate.Answers[question.QuestionNo]
		if !ok || !answerData.Synthetic {
			return fmt.Errorf("missing governed synthetic answer %s/%s", candidate.CandidateNo, question.QuestionNo)
		}
		segmentID := segmentByQuestion[question.QuestionNo]
		if segmentID == "" {
			return fmt.Errorf("missing segment %s/%s", candidate.CandidateNo, question.QuestionNo)
		}
		if err := run.recordAnswer(ctx, question, answerData, segmentID, result, aiTargets); err != nil {
			return err
		}
	}
	return nil
}

func (run *candidateImport) recordAnswer(ctx context.Context, question questionDef, answerData candidateAnswer, segmentID string, result *importResult, aiTargets *[]aiTarget) error {
	client, db, candidate := run.client, run.db, run.candidate
	tenantID, adminID, graderID := run.users.tenantID, run.users.adminID, run.users.graderID
	source := "ocr_text"
	extractor := "ocr"
	if questionNumber(question.QuestionNo) <= 10 {
		source = "imported_answer"
		extractor = "omr"
	}
	answerResponse, recordErr := client.json(ctx, http.MethodPut, answerSegmentRoutePrefix+segmentID+"/answer", map[string]any{
		"answer_text": displayText(answerData.Text),
		"answer_payload": map[string]any{
			"answer":          answerData.Text,
			"synthetic":       true,
			"simulation_only": true,
			"extractor":       extractor,
			"dataset_id":      datasetID,
		},
		"source":     source,
		"confidence": answerData.Confidence,
	}, http.StatusOK)
	if recordErr != nil {
		return fmt.Errorf("record %s/%s answer: %w", candidate.CandidateNo, question.QuestionNo, recordErr)
	}
	answerID := nestedStringField(answerResponse, "answer", "id")
	candidateID, candidateErr := createAnswerCandidate(ctx, db, answerCandidateCreateInput{
		tenantID: tenantID, adminID: adminID, segmentID: segmentID, answerID: answerID,
		question: question, answerData: answerData, extractor: extractor,
	})
	if candidateErr != nil {
		return candidateErr
	}

	if questionNumber(question.QuestionNo) <= 16 {
		return run.gradeObjective(ctx, objectiveAnswer{
			question: question, answerData: answerData, segmentID: segmentID,
			candidateID: candidateID, extractor: extractor,
		}, result)
	}
	if _, recordErr = createReviewTask(ctx, client, segmentID, graderID, "subjective_default_review", 60); recordErr != nil {
		return fmt.Errorf("create subjective review task %s/%s: %w", candidate.CandidateNo, question.QuestionNo, recordErr)
	}
	result.AssignedReviewTaskCount++
	if run.candidateIndex == 0 {
		*aiTargets = append(*aiTargets, aiTarget{SegmentID: segmentID, Label: candidate.CandidateNo + "/" + question.QuestionNo})
	}
	return nil
}

type objectiveAnswer struct {
	question    questionDef
	answerData  candidateAnswer
	segmentID   string
	candidateID string
	extractor   string
}

func (run *candidateImport) gradeObjective(ctx context.Context, answer objectiveAnswer, result *importResult) error {
	client, db, candidate := run.client, run.db, run.candidate
	tenantID, adminID, graderID := run.users.tenantID, run.users.adminID, run.users.graderID
	examID, submissionID, questionIDs, ruleIDs := run.examID, run.submissionID, run.questions.questionIDs, run.questions.ruleIDs
	question, answerData, segmentID, candidateID, extractor := answer.question, answer.answerData, answer.segmentID, answer.candidateID, answer.extractor
	gradeResponse, gradeErr := client.json(ctx, http.MethodPost, answerSegmentRoutePrefix+segmentID+"/rule-grade", map[string]any{}, http.StatusCreated)
	if gradeErr != nil {
		return fmt.Errorf("rule grade %s/%s: %w", candidate.CandidateNo, question.QuestionNo, gradeErr)
	}
	grade := nestedMap(gradeResponse, "grade")
	// 自动确认仍须规则同时满足通过且无需复核；低置信度路线只创建教师任务。
	if answerData.Route == "auto_confirm" {
		if !boolField(grade, "auto_pass") || boolField(grade, "needs_human_review") {
			return fmt.Errorf("auto route rejected by grading guardrail for %s/%s", candidate.CandidateNo, question.QuestionNo)
		}
		if confirmErr := confirmObjectiveGrade(ctx, db, objectiveGradeConfirmation{
			tenantID: tenantID, adminID: adminID, examID: examID, submissionID: submissionID,
			questionID: questionIDs[question.QuestionNo], segmentID: segmentID,
			candidateID: candidateID, ruleID: ruleIDs[question.QuestionNo], grade: grade,
		}); confirmErr != nil {
			return confirmErr
		}
		result.AutoConfirmedGradeCount++
	} else {
		sourceCode := "ocr_low_confidence"
		if extractor == "omr" {
			sourceCode = "manual_sample"
		}
		if _, gradeErr = createReviewTask(ctx, client, segmentID, graderID, sourceCode, 80); gradeErr != nil {
			return fmt.Errorf("create review task %s/%s: %w", candidate.CandidateNo, question.QuestionNo, gradeErr)
		}
		result.AssignedReviewTaskCount++
	}
	return nil
}

func createImportAISuggestions(ctx context.Context, cfg config, client *apiClient, aiTargets []aiTarget, result *importResult) error {
	for index, target := range aiTargets {
		if index >= cfg.aiLimit {
			break
		}
		response, aiErr := client.json(ctx, http.MethodPost, answerSegmentRoutePrefix+target.SegmentID+"/subjective-ai-grade", map[string]any{
			"model_policy": map[string]any{},
		}, http.StatusCreated)
		if aiErr != nil {
			return fmt.Errorf("create governed AI suggestion for %s: %w", target.Label, aiErr)
		}
		grade := nestedMap(response, "grade")
		if stringField(grade, "status") != "succeeded" {
			return fmt.Errorf("governed AI suggestion failed for %s: %s", target.Label, stringField(grade, "failure_reason"))
		}
		result.SubjectiveAISuggestionIDs = append(result.SubjectiveAISuggestionIDs, stringField(grade, "id"))
	}

	return nil
}

func rubricFor(questionNo string) map[string]any {
	points := []any{}
	switch questionNo {
	case "Q17":
		points = []any{
			rubricPoint("q17-p1", "正确计算(-1)^0=1", 2),
			rubricPoint("q17-p2", "正确计算|-5|=5", 2),
			rubricPoint("q17-p3", "正确计算√4=2", 2),
			rubricPoint("q17-p4", "得到结果4", 2),
		}
	case "Q18":
		points = []any{
			rubricPoint("q18-p1", "由菱形得到AB=AD且∠B=∠D", 3),
			rubricPoint("q18-p2", "使用已知∠AEB=∠AFD", 1),
			rubricPoint("q18-p3", "证明△ABE≌△ADF", 3),
			rubricPoint("q18-p4", "得出BE=DF", 1),
		}
	case "Q19":
		points = []any{
			rubricPoint("q19-p1", "注意分母不为0", 1),
			rubricPoint("q19-p2", "正确去分母并列出等式", 3),
			rubricPoint("q19-p3", "解得x=10", 3),
			rubricPoint("q19-p4", "检验x=10成立", 1),
		}
	}
	return map[string]any{
		"status":     "locked",
		"max_score":  8,
		"points":     points,
		"deductions": []any{},
		"examples":   []any{},
	}
}

func rubricPoint(id string, description string, score float64) map[string]any {
	return map[string]any{"id": id, "description": description, "score": score, "required": true}
}

func buildTemplateLayout(questionIDs map[string]string, regions map[string]region) map[string]any {
	pages := []any{}
	for pageNo := 1; pageNo <= 2; pageNo++ {
		questionRegions := []any{}
		for number := 1; number <= 19; number++ {
			if (pageNo == 1 && number >= 17) || (pageNo == 2 && number < 17) {
				continue
			}
			questionNo := "Q" + strconv.Itoa(number)
			item := regions[questionNo]
			regionData := map[string]any{
				"id":          "region-" + strings.ToLower(questionNo),
				"question_id": questionIDs[questionNo],
				"label":       questionNo,
				"x":           item.X, "y": item.Y, "width": item.Width, "height": item.Height,
			}
			if number <= 10 {
				regionData["option_regions"] = buildObjectiveOptionRegions(number, questionNo)
			}
			questionRegions = append(questionRegions, regionData)
		}
		pages = append(pages, map[string]any{
			"page_no":            pageNo,
			"width":              imageWidth,
			"height":             imageHeight,
			"registration_marks": []any{},
			"identity_regions": []any{
				map[string]any{
					"id":    fmt.Sprintf("identity-page-%d", pageNo),
					"label": "anonymous_candidate_code",
					"x":     0.06, "y": 0.03, "width": 0.42, "height": 0.09,
				},
			},
			"question_regions": questionRegions,
		})
	}
	return map[string]any{
		"omr_profile": map[string]any{
			"mode":    "template_difference",
			"version": "opencv-template-difference-bubble-v1",
		},
		"pages": pages,
	}
}

func buildObjectiveOptionRegions(number int, questionNo string) []any {
	row := (number - 1) / 5
	col := (number - 1) % 5
	x0 := 105 + col*302
	y0 := 402 + row*160
	options := []any{}
	for optionIndex, option := range []string{"A", "B", "C", "D"} {
		cx := x0 + 66 + optionIndex*53
		cy := y0 + 26
		options = append(options, map[string]any{
			"id":     strings.ToLower(questionNo) + "-" + strings.ToLower(option),
			"label":  option,
			"x":      float64(cx-17) / imageWidth,
			"y":      float64(cy-17) / imageHeight,
			"width":  float64(34) / imageWidth,
			"height": float64(34) / imageHeight,
		})
	}
	return options
}

func createCaptureBatch(ctx context.Context, db *sql.DB, tenantID, examID, adminID string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `
INSERT INTO capture_batch(tenant_id,exam_id,name,source_type,status,operator_id)
VALUES($1::uuid,$2::uuid,'福建中考数学匿名仿真批次','scanner_upload','ready',$3::uuid)
RETURNING id::text
`, tenantID, examID, adminID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create capture batch: %w", err)
	}
	return id, nil
}

func addAnswerAreaCompatibilityAliases(ctx context.Context, db *sql.DB, tenantID, examID string) error {
	result, err := db.ExecContext(ctx, `
UPDATE question
SET answer_area = answer_area || jsonb_build_object('w', answer_area->'width', 'h', answer_area->'height'),
    updated_at = now()
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL
`, tenantID, examID)
	if err != nil {
		return fmt.Errorf("add answer-area compatibility aliases: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 19 {
		return fmt.Errorf("answer-area compatibility updated %d questions, want 19", rows)
	}
	return nil
}

func createRegistration(
	ctx context.Context,
	db *sql.DB,
	input registrationCreateInput,
) (string, error) {
	tenantID, adminID := input.tenantID, input.adminID
	captureBatchID, submissionID, page := input.captureBatchID, input.submissionID, input.page
	templateID, templateHash, candidateNo := input.templateID, input.templateHash, input.candidateNo
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	sourceID := stringField(page.FileAsset, "id")
	sourceHash := stringField(page.FileAsset, "hash_sha256")
	sourceBytes := int64(intField(page.FileAsset, "size_bytes"))
	var captureFileID string
	err = tx.QueryRowContext(ctx, `
INSERT INTO capture_file(
 tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,status,idempotency_key,uploaded_by
)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,'image/png',$5,$6,'completed',$7,$8::uuid)
RETURNING id::text
`, tenantID, captureBatchID, sourceID, filepath.Base(page.Path), sourceHash, sourceBytes,
		datasetID+":"+candidateNo+":page:"+strconv.Itoa(page.PageNo), adminID).Scan(&captureFileID)
	if err != nil {
		return "", fmt.Errorf("create capture file %s page %d: %w", candidateNo, page.PageNo, err)
	}
	var capturePageID string
	err = tx.QueryRowContext(ctx, `
INSERT INTO capture_page(
 tenant_id,capture_batch_id,capture_file_id,source_index,submission_id,submission_page_id,
 assigned_page_no,sequence_no,decoded_file_asset_id,status,page_identity,match_candidates
)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$4,$4,$7::uuid,'ready',
 jsonb_build_object('synthetic',true,'candidate_no',$8::text,'page_no',$4::int),'[]'::jsonb)
RETURNING id::text
`, tenantID, captureBatchID, captureFileID, page.PageNo, submissionID, page.ID, sourceID, candidateNo).Scan(&capturePageID)
	if err != nil {
		return "", fmt.Errorf("create capture page %s page %d: %w", candidateNo, page.PageNo, err)
	}
	var registrationID string
	err = tx.QueryRowContext(ctx, `
INSERT INTO page_registration_run(
 tenant_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,
 template_id,template_content_hash,page_no,processing_status,profile_version,started_at
)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,$8,'processing','simulation-identity-registration-v1',now())
RETURNING id::text
`, tenantID, capturePageID, page.ID, sourceID, sourceHash, templateID, templateHash, page.PageNo).Scan(&registrationID)
	if err != nil {
		return "", fmt.Errorf("create registration %s page %d: %w", candidateNo, page.PageNo, err)
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return registrationID, nil
}

func completeRegistration(
	ctx context.Context,
	db *sql.DB,
	input registrationCompleteInput,
) error {
	tenantID, submissionID, submissionPageID := input.tenantID, input.submissionID, input.submissionPageID
	registrationID, templateID, templateHash := input.registrationID, input.templateID, input.templateHash
	registered := input.registered
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE page_registration_run
SET processing_status='completed',match_status='matched',confidence=0.99,method='simulation_seeded_identity',
 registered_file_asset_id=$3::uuid,result_version='simulation-registration-v1',duration_ms=5,completed_at=now(),updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND processing_status='processing'
`, tenantID, registrationID, stringField(registered, "id"))
	if err != nil {
		return fmt.Errorf("complete registration: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return fmt.Errorf("complete registration affected %d rows", rows)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE answer_segment
SET status='accepted',
 template_id=$4::uuid,
 template_content_hash=$5,
 registration_run_id=$6::uuid,
 normalized_bbox=jsonb_build_object(
   'x',(bbox->>0)::float8,'y',(bbox->>1)::float8,'width',(bbox->>2)::float8,'height',(bbox->>3)::float8
 ),
  pixel_bbox=jsonb_build_object(
   'x',round((bbox->>0)::numeric * $7),'y',round((bbox->>1)::numeric * $8),
   'width',round((bbox->>2)::numeric * $7),'height',round((bbox->>3)::numeric * $8)
  ),
 question_version=1,
 processing_status='completed',
 confidence=0.99,
 updated_at=now()
WHERE tenant_id=$1::uuid AND submission_id=$2::uuid AND submission_page_id=$3::uuid AND deleted_at IS NULL
`, tenantID, submissionID, submissionPageID, templateID, templateHash, registrationID,
		imageWidth, imageHeight)
	if err != nil {
		return fmt.Errorf("attach segment evidence: %w", err)
	}
	return tx.Commit()
}

func decodeImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	return decoded, err
}

func cropAnswerRegion(source image.Image, item region, padding int) ([]byte, image.Rectangle, error) {
	if source == nil {
		return nil, image.Rectangle{}, errors.New("source image is nil")
	}
	if item.Width <= 0 || item.Height <= 0 {
		return nil, image.Rectangle{}, errors.New("crop region must have positive width and height")
	}
	if padding < 0 {
		return nil, image.Rectangle{}, errors.New("crop padding cannot be negative")
	}
	bounds := source.Bounds()
	left := bounds.Min.X + int(math.Floor(item.X*float64(bounds.Dx()))) - padding
	top := bounds.Min.Y + int(math.Floor(item.Y*float64(bounds.Dy()))) - padding
	right := bounds.Min.X + int(math.Ceil((item.X+item.Width)*float64(bounds.Dx()))) + padding
	bottom := bounds.Min.Y + int(math.Ceil((item.Y+item.Height)*float64(bounds.Dy()))) + padding
	left = max(left, bounds.Min.X)
	top = max(top, bounds.Min.Y)
	right = min(right, bounds.Max.X)
	bottom = min(bottom, bounds.Max.Y)
	cropBounds := image.Rect(left, top, right, bottom)
	if cropBounds.Empty() {
		return nil, image.Rectangle{}, fmt.Errorf("crop region is outside image bounds %v", bounds)
	}
	cropped := image.NewRGBA(image.Rect(0, 0, cropBounds.Dx(), cropBounds.Dy()))
	draw.Draw(cropped, cropped.Bounds(), source, cropBounds.Min, draw.Src)
	var output bytes.Buffer
	if err := png.Encode(&output, cropped); err != nil {
		return nil, image.Rectangle{}, err
	}
	return output.Bytes(), cropBounds, nil
}

func attachSegmentCrop(ctx context.Context, db *sql.DB, tenantID, segmentID string, asset map[string]any) error {
	result, err := db.ExecContext(ctx, `
UPDATE answer_segment
SET crop_file_asset_id=$3::uuid,crop_sha256=$4,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, segmentID, stringField(asset, "id"), stringField(asset, "hash_sha256"))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("attach crop affected %d answer segments", rows)
	}
	return nil
}

func createAnswerCandidate(
	ctx context.Context,
	db *sql.DB,
	input answerCandidateCreateInput,
) (string, error) {
	tenantID, adminID, segmentID, answerID := input.tenantID, input.adminID, input.segmentID, input.answerID
	question, answerData, extractor := input.question, input.answerData, input.extractor
	payload, _ := json.Marshal(map[string]any{
		"answer":          answerData.Text,
		"synthetic":       true,
		"simulation_only": true,
		"extractor":       extractor,
	})
	evidence, _ := json.Marshal(map[string]any{
		"answer_segment_answer_id": answerID,
		"simulation_seeded":        true,
		"dataset_id":               datasetID,
	})
	decision := "ambiguous"
	if answerData.Route == "auto_confirm" {
		decision = "confirmed"
	} else if displayText(answerData.Text) == "" {
		decision = "blank"
	} else if values, ok := answerData.Text.([]any); ok && len(values) > 1 {
		decision = "multiple"
	}
	source := "ocr"
	if extractor == "omr" {
		source = "omr"
	}
	var id string
	err := db.QueryRowContext(ctx, `
INSERT INTO answer_candidate(
 tenant_id,answer_segment_id,source,payload,display_text,confidence,decision,evidence,
 engine_version,profile_version,input_hash,is_current,created_by
)
VALUES($1::uuid,$2::uuid,$3,$4::jsonb,$5,$6,$7,$8::jsonb,$9,$10,$11,true,$12::uuid)
RETURNING id::text
`, tenantID, segmentID, source, payload, displayText(answerData.Text), answerData.Confidence, decision, evidence,
		"simulation-"+extractor+"-transcript-v1", "synthetic-handwriting-scan-v1",
		"sha256:"+sha256Hex([]byte(segmentID+":"+question.QuestionNo+":"+displayText(answerData.Text))), adminID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create answer candidate for %s: %w", question.QuestionNo, err)
	}
	return id, nil
}

func confirmObjectiveGrade(
	ctx context.Context,
	db *sql.DB,
	input objectiveGradeConfirmation,
) error {
	tenantID, adminID, examID, submissionID := input.tenantID, input.adminID, input.examID, input.submissionID
	questionID, segmentID, candidateID, ruleID, grade := input.questionID, input.segmentID, input.candidateID, input.ruleID, input.grade
	evidence, _ := json.Marshal(map[string]any{
		"ai_grade_id":             stringField(grade, "id"),
		"grader_type":             stringField(grade, "grader_type"),
		"rule_version":            stringField(grade, "rule_version"),
		"confidence":              numberField(grade, "confidence"),
		"risk_flags":              grade["risk_flags"],
		"simulation_seeded_input": true,
	})
	_, err := db.ExecContext(ctx, `
INSERT INTO question_grade(
 tenant_id,exam_id,submission_id,question_id,answer_segment_id,answer_candidate_id,scoring_rule_id,
 source,status,score,max_score,evidence,version,is_current,confirmed_by
)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7::uuid,
 'rule_confirmed','confirmed',$8,$9,$10::jsonb,1,true,$11::uuid)
`, tenantID, examID, submissionID, questionID, segmentID, candidateID, ruleID,
		numberField(grade, "suggested_score"), numberField(grade, "max_score"), evidence, adminID)
	if err != nil {
		return fmt.Errorf("confirm automatic objective grade: %w", err)
	}
	return nil
}

func createReviewTask(ctx context.Context, client *apiClient, segmentID, graderID, source string, priority int) (string, error) {
	response, err := client.json(ctx, http.MethodPost, "/api/v1/review-tasks", map[string]any{
		"answer_segment_id": segmentID,
		"source":            source,
		"priority":          priority,
		"assigned_to":       graderID,
		"grade_round":       "single",
	}, http.StatusCreated)
	if err != nil {
		return "", err
	}
	return nestedStringField(response, "task", "id"), nil
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

func (c *apiClient) login(ctx context.Context, tenantCode, username, password string) error {
	response, err := c.json(ctx, http.MethodPost, "/api/v1/auth/token", map[string]any{
		"tenant_code": tenantCode,
		"username":    username,
		"password":    password,
		"client_type": "desktop",
		"device_name": "Fujian simulation importer",
	}, http.StatusOK)
	if err != nil {
		return err
	}
	c.token = stringField(response, "access_token")
	if c.token == "" {
		return errors.New("login response did not include access_token")
	}
	return nil
}

func (c *apiClient) json(ctx context.Context, method, path string, body any, want int) (map[string]any, error) {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", jsonContentType)
	if body != nil {
		req.Header.Set("Content-Type", jsonContentType)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != want {
		return nil, fmt.Errorf("%s %s returned %d: %s", method, path, response.StatusCode, strings.TrimSpace(string(raw)))
	}
	result := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	if path == "/api/v1/auth/token" && method == http.MethodPost {
		c.loginResponse = result
	}
	return result, nil
}

func waitForSubmissionImageQuality(ctx context.Context, client *apiClient, submissionID string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Minute)
	defer timeout.Stop()
	for {
		response, err := client.json(ctx, http.MethodGet, submissionRoutePrefix+submissionID, nil, http.StatusOK)
		if err != nil {
			return err
		}
		item := nestedMap(response, "submission")
		switch stringField(item, "quality_status") {
		case "passed":
			return nil
		case "review", "failed":
			return fmt.Errorf("submission requires operator action: status=%s issues=%v", stringField(item, "quality_status"), item["quality_issues"])
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return errors.New("timed out waiting for image quality worker")
		case <-ticker.C:
		}
	}
}

func (c *apiClient) upload(ctx context.Context, path string, fields map[string]string) (map[string]any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return c.uploadBytes(ctx, filepath.Base(path), content, fields)
}

func (c *apiClient) uploadBytes(ctx context.Context, filename string, content []byte, fields map[string]string) (map[string]any, error) {
	if strings.TrimSpace(filename) == "" {
		return nil, errors.New("upload filename is required")
	}
	buffer, writer, err := makeUploadForm(filename, content, fields)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/files", buffer)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", jsonContentType)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("upload %s returned %d: %s", filepath.Base(filename), response.StatusCode, strings.TrimSpace(string(raw)))
	}
	result := map[string]any{}
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	asset := nestedMap(result, "file")
	if stringField(asset, "id") == "" || stringField(asset, "hash_sha256") == "" {
		return nil, errors.New("upload response lacks file identity/hash")
	}
	return asset, nil
}

func makeUploadForm(filename string, content []byte, fields map[string]string) (*bytes.Buffer, *multipart.Writer, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for key, value := range fields {
		if value != "" {
			if err := writer.WriteField(key, value); err != nil {
				return nil, nil, err
			}
		}
	}
	part, err := writer.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		return nil, nil, err
	}
	if _, err = part.Write(content); err != nil {
		return nil, nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, nil, err
	}
	return &buffer, writer, nil
}

func fileFromManifest(datasetDir string, files map[string]any, key string) string {
	value, _ := files[key].(string)
	return filepath.Join(datasetDir, filepath.FromSlash(value))
}

func readJSON(path string, target any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, target)
}

func questionNumber(questionNo string) int {
	value, _ := strconv.Atoi(strings.TrimPrefix(questionNo, "Q"))
	return value
}

func displayText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			items = append(items, fmt.Sprint(item))
		}
		return strings.Join(items, ",")
	case []string:
		return strings.Join(typed, ",")
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func nestedMap(value map[string]any, key string) map[string]any {
	item, _ := value[key].(map[string]any)
	return item
}

func nestedStringField(value map[string]any, nested, key string) string {
	return stringField(nestedMap(value, nested), key)
}

func stringField(value map[string]any, key string) string {
	item, _ := value[key].(string)
	return item
}

func boolField(value map[string]any, key string) bool {
	item, _ := value[key].(bool)
	return item
}

func intField(value map[string]any, key string) int {
	switch item := value[key].(type) {
	case float64:
		return int(item)
	case int:
		return item
	default:
		return 0
	}
}

func numberField(value map[string]any, key string) float64 {
	switch item := value[key].(type) {
	case float64:
		return item
	case int:
		return float64(item)
	default:
		return 0
	}
}

func sliceOfMaps(value any) []map[string]any {
	raw, _ := value.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if mapped, ok := item.(map[string]any); ok {
			out = append(out, mapped)
		}
	}
	return out
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
