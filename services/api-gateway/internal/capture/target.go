package capture

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"net/http"
)

type Target struct {
	BatchID      string `json:"batch_id"`
	SubmissionID string `json:"submission_id"`
	PageID       string `json:"page_id"`
	CandidateNo  string `json:"candidate_no"`
}

func (s *PostgresStore) FindTargets(ctx context.Context, tenantID, examID, submissionID, studentID string) ([]Target, error) {
	if submissionID == "" && studentID == "" {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT cp.capture_batch_id::text,sub.id::text,cp.id::text,COALESCE(sub.candidate_no,'') FROM submission sub
JOIN capture_page cp ON cp.tenant_id=sub.tenant_id AND cp.submission_id=sub.id AND cp.status<>'deleted'
JOIN capture_batch batch ON batch.tenant_id=cp.tenant_id AND batch.id=cp.capture_batch_id AND batch.exam_id=sub.exam_id
WHERE sub.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND sub.deleted_at IS NULL AND ($3='' OR sub.id::text=$3) AND ($4='' OR sub.student_id::text=$4)
ORDER BY cp.capture_batch_id::text,sub.id::text,cp.id::text`, tenantID, examID, submissionID, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Target{}
	for rows.Next() {
		var item Target
		if err = rows.Scan(&item.BatchID, &item.SubmissionID, &item.PageID, &item.CandidateNo); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (h *Handler) FindTargets(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	source, ok := h.store.(interface {
		FindTargets(context.Context, string, string, string, string) ([]Target, error)
	})
	if !ok {
		writeError(w, r, ErrNotFound)
		return
	}
	targets, err := source.FindTargets(r.Context(), user.TenantID, r.PathValue("examId"), r.URL.Query().Get("submission_id"), r.URL.Query().Get("student_id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"targets": targets})
}
