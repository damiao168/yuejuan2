package subjective

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"net/http"
	"sort"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
	"github.com/google/uuid"
)

type GradingSource struct {
	AnswerSegmentID   string `json:"answer_segment_id"`
	QuestionID        string `json:"question_id"`
	QuestionNo        string `json:"question_no"`
	SubmissionLabel   string `json:"submission_label"`
	QuestionType      string `json:"question_type"`
	Ready             bool   `json:"ready"`
	UnavailableReason string `json:"unavailable_reason"`
}
type GradingSourceFilter struct {
	QuestionID, Cursor string
	Limit              int
	TenantWide         bool
	SchoolIDs          []string
}
type GradingSourceDirectory interface {
	ListGradingSources(context.Context, string, string, GradingSourceFilter) ([]GradingSource, error)
}

// ListGradingSources 只列当前考试的匿名材料身份；目录选择不领取任务，也不批准评分，创建仍重验完整上下文。
func (h *Handler) ListGradingSources(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	scope, scoped := auth.AccessScopeFromContext(r.Context())
	if !scoped || scope.TenantID != user.TenantID || scope.ActorID != user.ID || !scope.AllowsExam(r.PathValue("examId")) || (!scope.TenantWide && !scope.IsSchoolWide()) {
		httpx.Error(w, r, 403, "forbidden", "当前账号不能读取此考试材料")
		return
	}
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 50, 100)
	if err != nil {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	filter := GradingSourceFilter{QuestionID: strings.TrimSpace(r.URL.Query().Get("question_id")), Cursor: strings.TrimSpace(r.URL.Query().Get("cursor")), Limit: limit + 1}
	filter.TenantWide = scope.TenantWide
	filter.SchoolIDs = scope.ManagedSchoolIDs()
	for _, value := range []string{filter.QuestionID, filter.Cursor, r.PathValue("examId")} {
		if value != "" {
			if _, err = uuid.Parse(value); err != nil {
				writeStoreError(w, r, ErrInvalidInput)
				return
			}
		}
	}
	store, ok := h.store.(GradingSourceDirectory)
	if !ok {
		httpx.Error(w, r, 503, "source_directory_unavailable", "评分材料目录暂不可用")
		return
	}
	items, err := store.ListGradingSources(r.Context(), user.TenantID, r.PathValue("examId"), filter)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	hasMore, next := len(items) > limit, ""
	if hasMore {
		items = items[:limit]
		next = items[len(items)-1].AnswerSegmentID
	}
	httpx.JSON(w, 200, map[string]any{"sources": items, "next_cursor": next, "has_more": hasMore})
}

func (s *PostgresStore) ListGradingSources(ctx context.Context, tenant, exam string, filter GradingSourceFilter) ([]GradingSource, error) {
	// 先按已验证考试和租户过滤，游标只比较该范围内的片段；不借旧review_task作为材料可见性的前提。
	rows, err := s.db.QueryContext(ctx, `SELECT seg.id::text,q.id::text,q.question_no,sub.id::text,q.question_type,
 EXISTS(SELECT 1 FROM answer_segment_answer a WHERE a.tenant_id=seg.tenant_id AND a.answer_segment_id=seg.id AND a.deleted_at IS NULL),
 EXISTS(SELECT 1 FROM question_rubric rb WHERE rb.tenant_id=q.tenant_id AND rb.question_id=q.id AND rb.deleted_at IS NULL),
 EXISTS(SELECT 1 FROM exam_question_snapshot eq WHERE eq.tenant_id=q.tenant_id AND eq.exam_id=q.exam_id AND eq.question_id=q.id)
 FROM answer_segment seg JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
 JOIN exam e ON e.tenant_id=q.tenant_id AND e.id=q.exam_id AND e.deleted_at IS NULL
 JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.exam_id=q.exam_id AND sub.deleted_at IS NULL
 WHERE seg.tenant_id=$1::uuid AND q.exam_id=$2::uuid AND seg.deleted_at IS NULL AND seg.status<>'deleted'
 AND q.question_type IN ('short_answer','calculation','essay','discussion') AND ($3='' OR q.id=NULLIF($3,'')::uuid)
 AND ($4='' OR seg.id>NULLIF($4,'')::uuid) AND ($6 OR e.school_id::text=ANY($7::text[])) ORDER BY seg.id LIMIT $5`, tenant, exam, filter.QuestionID, filter.Cursor, filter.Limit, filter.TenantWide, filter.SchoolIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GradingSource{}
	for rows.Next() {
		var item GradingSource
		var submission string
		var answer, rubric, frozen bool
		if err = rows.Scan(&item.AnswerSegmentID, &item.QuestionID, &item.QuestionNo, &submission, &item.QuestionType, &answer, &rubric, &frozen); err != nil {
			return nil, err
		}
		item.SubmissionLabel = "匿名答卷 " + submission[len(submission)-8:]
		item.Ready = answer && rubric && frozen
		if !answer {
			item.UnavailableReason = "尚无已登记作答"
		} else if !rubric {
			item.UnavailableReason = "尚无评分标准"
		} else if !frozen {
			item.UnavailableReason = "题目尚未确认冻结"
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *MemoryStore) ListGradingSources(_ context.Context, tenant, exam string, filter GradingSourceFilter) ([]GradingSource, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []GradingSource{}
	for identity, item := range s.contexts {
		if !strings.HasPrefix(identity, tenant+"|") || item.Question.ExamID != exam || !IsSupportedQuestionType(item.Question.QuestionType) || filter.QuestionID != "" && item.Question.ID != filter.QuestionID || filter.Cursor != "" && item.SegmentID <= filter.Cursor {
			continue
		}
		ready := item.AnswerText != "" && item.Rubric.ID != "" && item.AssessmentSnapshot.ID != ""
		reason := ""
		if !ready {
			reason = "作答、评分标准或冻结依据尚未齐全"
		}
		out = append(out, GradingSource{AnswerSegmentID: item.SegmentID, QuestionID: item.Question.ID, QuestionNo: item.Question.QuestionNo, SubmissionLabel: "匿名材料", QuestionType: item.Question.QuestionType, Ready: ready, UnavailableReason: reason})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AnswerSegmentID < out[j].AnswerSegmentID })
	if len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}
