package server

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/subjective"
)

func TestSubjectiveUsageLedgerPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for PostgreSQL usage ledger regression")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, token, suffix)
	e2eSeedStory056AcceptanceAnswersCount(t, db, fixture, suffix, 1)

	ctx := t.Context()
	var segmentID, questionID, questionNo, questionType string
	if err := db.QueryRowContext(ctx, `
SELECT segment.id::text, segment.question_id::text, segment.question_no, question.question_type
FROM answer_segment segment
JOIN question ON question.tenant_id=segment.tenant_id AND question.id=segment.question_id
JOIN submission ON submission.tenant_id=segment.tenant_id AND submission.id=segment.submission_id
WHERE submission.exam_id=$1::uuid
LIMIT 1`, fixture.ExamID).Scan(&segmentID, &questionID, &questionNo, &questionType); err != nil {
		t.Fatalf("find seeded answer segment: %v", err)
	}
	requestID := "usage-ledger-" + suffix
	grade, err := subjective.NewPostgresStore(db).CreateGrade(ctx, fixture.TenantID, fixture.AdminID, subjective.Grade{
		AnswerSegmentID: segmentID, QuestionID: questionID, QuestionNo: questionNo, QuestionType: questionType,
		AnswerVersion: "answer-v1", GraderType: "llm_subjective", ModelVersion: "local-model-v1",
		PromptVersion: "prompt-v1", RubricVersion: "rubric-v1", DeliveryMode: "teacher_suggestion",
		CapabilityProfile: "subjective-grading-v1",
		SuggestedScore:    1, MaxScore: 1, Confidence: .9, NeedsHumanReview: true,
		Status: "succeeded", Mock: false, AdapterRequestID: requestID, AdapterName: "grading-agent",
		ProviderKey: "local", DeploymentKey: "local-model", DeploymentRegion: "local", AdapterAttempts: 1,
		InputTokens: 301, CachedInputTokens: 120, OutputTokens: 47, ReasoningTokens: 9, TotalTokens: 348,
	})
	if err != nil {
		t.Fatalf("create grade with provider usage: %v", err)
	}
	if grade.ID == "" {
		t.Fatal("created grade has no ID")
	}

	var schoolID string
	var requestCount, inputTokens, outputTokens, cachedInputTokens, reasoningTokens, totalTokens int64
	var metadataJSON []byte
	if err := db.QueryRowContext(ctx, `
SELECT school_id::text, request_count, input_tokens, output_tokens, cached_input_tokens,
       reasoning_tokens, total_tokens, metadata
FROM model_usage_event
WHERE tenant_id=$1::uuid AND request_id=$2 AND feature='subjective_grading'`,
		fixture.TenantID, requestID).Scan(&schoolID, &requestCount, &inputTokens, &outputTokens,
		&cachedInputTokens, &reasoningTokens, &totalTokens, &metadataJSON); err != nil {
		t.Fatalf("read provider usage event: %v", err)
	}
	if schoolID != fixture.SchoolID || requestCount != 1 || inputTokens != 301 || outputTokens != 47 ||
		cachedInputTokens != 120 || reasoningTokens != 9 || totalTokens != 348 {
		t.Fatalf("incorrect provider usage: school=%s requests=%d input=%d output=%d cached=%d reasoning=%d total=%d",
			schoolID, requestCount, inputTokens, outputTokens, cachedInputTokens, reasoningTokens, totalTokens)
	}
	var metadata map[string]any
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		t.Fatalf("decode usage metadata: %v", err)
	}
	if metadata["deployment_key"] != "local-model" || metadata["question_id"] != questionID || metadata["answer_segment_id"] != segmentID {
		t.Fatalf("incorrect provider usage metadata: %#v", metadata)
	}
}
