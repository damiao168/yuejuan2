// template-incident-probe 复用学校答题卡模型选择，只读诊断单次事故。
// 默认只做预检；--execute 最多调用两张图，不保存模板、探测状态或用量台账。
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"github.com/google/uuid"
)

type plan struct {
	ExamID     string       `json:"exam_id"`
	TemplateID string       `json:"template_id"`
	SourcePDF  string       `json:"source_pdf_path"`
	SourceSHA  string       `json:"source_pdf_sha256"`
	Images     []probeImage `json:"images"`
}

type probeImage struct {
	Name   string `json:"name"`
	PageNo int    `json:"page_no"`
	Path   string `json:"path"`
	SHA    string `json:"sha256"`
	// 裁图坐标只用于报告，不能冒充整页坐标，也不回写正式布局。
	SourceCrop []int `json:"source_crop_xyxy,omitempty"`
}

func fail(code string) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"success": false, "error_code": code})
	os.Exit(1)
}

func digest(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }

func main() {
	input := flag.String("plan", "", "local source/image plan; must contain no credentials")
	execute := flag.Bool("execute", false, "explicitly permit at most two real analysis calls")
	resolvePipe := flag.Bool("resolve-pipe", false, "SECRET stdout: only for an audited parent process pipe; never run interactively")
	flag.Parse()
	raw, err := os.ReadFile(*input)
	if err != nil {
		fail("PLAN_READ_FAILED")
	}
	var p plan
	if json.Unmarshal(raw, &p) != nil || uuid.Validate(p.ExamID) != nil || uuid.Validate(p.TemplateID) != nil || len(p.Images) > 2 || !*resolvePipe && len(p.Images) < 1 || *resolvePipe && *execute {
		fail("PLAN_INVALID")
	}
	if !*resolvePipe {
		pdf, readErr := os.ReadFile(p.SourcePDF)
		if readErr != nil || len(pdf) > 64<<20 || digest(pdf) != p.SourceSHA {
			fail("SOURCE_PDF_HASH_MISMATCH")
		}
	}
	cfg, err := config.Load("")
	if err != nil {
		fail("RUNTIME_CONFIG_INVALID")
	}
	// 在连接建立时强制整个诊断会话只读；不能只靠代码约定不执行 UPDATE。
	dsn, err := url.Parse(cfg.Postgres.DSN)
	if err != nil || (dsn.Scheme != "postgres" && dsn.Scheme != "postgresql") {
		fail("READONLY_DSN_UNSUPPORTED")
	}
	query := dsn.Query()
	query.Set("default_transaction_read_only", "on")
	dsn.RawQuery = query.Encode()
	cfg.Postgres.DSN = dsn.String()
	cfg.Postgres.MaxOpenConns = 1
	cfg.Postgres.MaxIdleConns = 1
	database, closeDatabase, err := db.OpenPostgres(cfg.Postgres)
	if err != nil {
		fail("DATABASE_OPEN_FAILED")
	}
	defer closeDatabase()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	// 仅此维护命令用跨租户只读查询取得已知模板归属；随后所有模型和题目查询都绑定该租户。
	maintenance := db.WithTenantMaintenance(ctx)
	var tenantID, examID, paperID, sourceHash, status string
	var revision, pageCount int
	err = database.QueryRowContext(maintenance, `SELECT t.tenant_id::text,t.exam_id::text,t.exam_paper_id::text,t.revision,t.page_count,t.status,f.hash_sha256
FROM answer_sheet_template t JOIN exam_paper p ON p.tenant_id=t.tenant_id AND p.id=t.exam_paper_id
JOIN file_asset f ON f.tenant_id=p.tenant_id AND f.id=p.file_asset_id
WHERE t.id=$1::uuid AND t.exam_id=$2::uuid AND t.deleted_at IS NULL AND p.deleted_at IS NULL AND f.deleted_at IS NULL AND p.purpose='answer_sheet'`, p.TemplateID, p.ExamID).Scan(&tenantID, &examID, &paperID, &revision, &pageCount, &status, &sourceHash)
	if err != nil || examID != p.ExamID || sourceHash != p.SourceSHA {
		fail("LIVE_SOURCE_BINDING_MISMATCH")
	}
	ctx = db.WithTenant(ctx, tenantID)
	var readOnly string
	if database.QueryRowContext(ctx, "SHOW default_transaction_read_only").Scan(&readOnly) != nil || readOnly != "on" {
		fail("DATABASE_NOT_READONLY")
	}
	cipher, err := modelgovernance.NewCredentialCipher(cfg.ModelSecrets.MasterKey)
	if err != nil {
		fail("MODEL_CIPHER_UNAVAILABLE")
	}
	store := modelgovernance.NewPostgresStore(database, cipher)
	connection, err := modelgovernance.ResolveAnswerSheetManagedAPI(ctx, store, tenantID)
	if err != nil || connection == nil {
		fail("SCHOOL_ANSWER_SHEET_MODEL_UNAVAILABLE")
	}
	questions, err := paper.NewPostgresStore(database).ListQuestions(ctx, tenantID, p.ExamID)
	if err != nil {
		fail("QUESTION_SNAPSHOT_UNAVAILABLE")
	}
	questionJSON, err := json.Marshal(questions)
	if err != nil {
		fail("QUESTION_SNAPSHOT_INVALID")
	}
	all, objective, fill := []string{}, []string{}, []string{}
	for _, q := range questions {
		label := strings.TrimSpace(q.QuestionNo)
		if label == "" {
			continue
		}
		all = append(all, label)
		switch q.QuestionType {
		case "single_choice", "multiple_choice", "true_false":
			objective = append(objective, label)
		case "fill_blank":
			fill = append(fill, label)
		}
	}
	model := paper.DocumentModelConfig{AdapterType: connection.Config.AdapterType, ProviderKey: connection.Config.ProviderKey, BaseURL: connection.Config.BaseURL, APIKey: connection.APIKey, ModelName: connection.Config.ModelName, ModelVersion: connection.Config.ModelVersion}
	if *resolvePipe {
		// 只有经过审读的父进程可接此输出，并须直接传入隔离评测进程；不能打印、重定向或写日志。
		// 这里不调用供应商。DB 会话仍只读，学校选择和来源绑定已按正式解析路径核对。
		info, statErr := os.Stdout.Stat()
		if statErr != nil || info.Mode()&os.ModeCharDevice != 0 {
			fail("SECRET_PIPE_REQUIRED")
		}
		secret := map[string]any{"managed_model": model, "question_labels": all, "objective_question_labels": objective, "fill_blank_question_labels": fill, "diagnostic_context": map[string]any{"template_id": p.TemplateID, "template_revision": revision, "source_paper_id": paperID, "source_pdf_sha256": sourceHash, "question_snapshot_sha256": digest(questionJSON)}}
		if json.NewEncoder(os.Stdout).Encode(secret) != nil {
			os.Exit(1)
		}
		return
	}
	report := map[string]any{"success": false, "executed": *execute, "scope": "actual_school_model_readonly_incident_probe", "database_read_only": true, "database_written": false, "template_written": false, "maximum_provider_calls": len(p.Images), "provider_calls_attempted": 0, "exam_id": p.ExamID, "template_id": p.TemplateID, "template_revision": revision, "template_status": status, "source_pdf_sha256": sourceHash, "question_snapshot_sha256": digest(questionJSON), "model": map[string]string{"config_id": connection.Config.ID, "provider_key": model.ProviderKey, "adapter_type": model.AdapterType, "model_name": model.ModelName, "model_version": model.ModelVersion}, "results": []any{}}
	results := []any{}
	for index, item := range p.Images {
		if item.PageNo < 1 || item.PageNo > pageCount || len(item.Name) > 80 || !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(item.Name) {
			fail("PROBE_IMAGE_DESCRIPTOR_INVALID")
		}
		data, err := os.ReadFile(item.Path)
		if err != nil || len(data) == 0 || len(data) > 1<<20 || digest(data) != item.SHA {
			fail("PROBE_IMAGE_HASH_OR_SIZE_INVALID")
		}
		dimensions, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || dimensions.Width < 1 || dimensions.Height < 1 || dimensions.Width > 4096 || dimensions.Height > 4096 || dimensions.Width*dimensions.Height > 4000000 || (format != "jpeg" && format != "png") {
			fail("PROBE_IMAGE_DIMENSIONS_INVALID")
		}
		requestID := uuid.NewString()
		entry := map[string]any{"name": item.Name, "page_no": item.PageNo, "request_id": requestID, "image_sha256": item.SHA, "width": dimensions.Width, "height": dimensions.Height, "image_bytes": len(data), "source_crop_xyxy": item.SourceCrop, "input_image_kind": "new_probe_image_not_historical_request_replay"}
		if *execute {
			// 密钥仅随内部 HTTPS 请求在内存中使用；禁止序列化到报告、错误或临时文件。
			payload := map[string]any{"request_id": requestID, "schema_version": "answer-sheet-analysis-v1", "page": map[string]any{"page_no": item.PageNo, "width": dimensions.Width, "height": dimensions.Height, "media_type": "image/" + format, "data_base64": base64.StdEncoding.EncodeToString(data), "sha256": item.SHA}, "candidates": []any{}, "question_labels": all, "objective_question_labels": objective, "fill_blank_question_labels": fill, "writing_state": "unknown", "managed_model": model, "diagnostic_context": map[string]any{"template_id": p.TemplateID, "template_revision": revision, "source_paper_id": paperID, "source_pdf_sha256": sourceHash, "question_snapshot_sha256": digest(questionJSON)}}
			body, err := json.Marshal(payload)
			if err != nil {
				fail("ANALYSIS_REQUEST_ENCODING_FAILED")
			}
			callCtx, callCancel := context.WithTimeout(ctx, 100*time.Second)
			req, err := http.NewRequestWithContext(callCtx, http.MethodPost, strings.TrimRight(cfg.AIService.URL, "/")+"/answer-sheet/analyze", bytes.NewReader(body))
			if err != nil {
				callCancel()
				fail("INTERNAL_REQUEST_INVALID")
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+cfg.AIService.Token)
			report["provider_calls_attempted"] = index + 1
			response, err := (&http.Client{Timeout: 105 * time.Second}).Do(req)
			if err != nil {
				callCancel()
				entry["error_code"] = "INTERNAL_ANALYSIS_TRANSPORT_FAILED"
				results = append(results, entry)
				break
			}
			answer, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20+1))
			_ = response.Body.Close()
			callCancel()
			entry["http_status"] = response.StatusCode
			if readErr != nil || len(answer) > 2<<20 || response.StatusCode != 200 {
				entry["error_code"] = "INTERNAL_ANALYSIS_FAILED"
				results = append(results, entry)
				break
			}
			var parsed paper.TemplatePageAnalysis
			if json.Unmarshal(answer, &parsed) != nil || parsed.RequestID != requestID {
				entry["error_code"] = "INTERNAL_ANALYSIS_RESPONSE_INVALID"
				results = append(results, entry)
				break
			}
			entry["response_sha256"] = digest(answer)
			entry["semantic_status"] = parsed.SemanticStatus
			entry["versions"] = parsed.Versions
			entry["candidates"] = safeCandidates(parsed.Candidates)
			entry["reason_codes"] = safeCodes(parsed.ReasonCodes)
			entry["success"] = parsed.SemanticStatus == "completed"
		} else {
			entry["success"] = true
			entry["preflight_only"] = true
		}
		results = append(results, entry)
	}
	report["results"] = results
	passed := len(results) == len(p.Images)
	for _, raw := range results {
		if raw.(map[string]any)["success"] != true {
			passed = false
		}
	}
	report["success"] = passed
	report["observability_boundary"] = "Local request contains verified image bytes; provider proxy forwarding and actual visual attention cannot be proved without provider-side evidence or image-dependent contrasts."
	if json.NewEncoder(os.Stdout).Encode(report) != nil {
		fail("REPORT_WRITE_FAILED")
	}
	if !passed {
		os.Exit(1)
	}
}

var questionLabel = regexp.MustCompile(`^[0-9]{1,4}(?:[A-Za-z]|[.\-][0-9]{1,2})?$`)
var fixedCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,95}$`)

func safeCodes(values []string) []string {
	out := []string{}
	for _, value := range values {
		if fixedCode.MatchString(value) {
			out = append(out, value)
		}
	}
	return out
}

func safeCandidates(rows []paper.TemplateAnalyzedRegion) []any {
	out := []any{}
	for _, row := range rows {
		label := ""
		if row.PrintedQuestionLabel != nil && questionLabel.MatchString(*row.PrintedQuestionLabel) {
			label = *row.PrintedQuestionLabel
		}
		identity := ""
		if row.IdentityLabel != nil {
			switch *row.IdentityLabel {
			case "姓名", "准考证号", "学号", "考号", "学校", "班级", "条码", "二维码", "身份信息":
				identity = *row.IdentityLabel
			}
		}
		out = append(out, map[string]any{"candidate_id": row.CandidateID, "printed_question_label": label, "identity_label": identity, "role": row.Role, "origin": row.Origin, "source_bbox": row.SourceBBox, "proposed_bbox": row.ProposedBBox, "association_status": row.AssociationStatus, "semantic_check": row.SemanticCheck, "reason_codes": safeCodes(row.ReasonCodes), "objective_items": row.ObjectiveItems})
	}
	return out
}
