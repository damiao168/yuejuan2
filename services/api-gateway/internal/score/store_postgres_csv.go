package score

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/csvsafe"
	"encoding/csv"
	"fmt"
	"time"
)

func (s *PostgresStore) ExportGradesCSV(ctx context.Context, tenantID string, examID string, actorID string) (ExportResult, error) {
	grades, err := s.listGrades(ctx, tenantID, examID, false)
	if err != nil {
		return ExportResult{}, err
	}
	if len(grades) == 0 {
		return ExportResult{}, ErrNotFound
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	exportedAt := time.Now().UTC().Format(time.RFC3339)
	// 原始 CSV 保持既有十列格式供内部对账，不能被误当成已发布快照。
	watermark := fmt.Sprintf("EduGrade export internal-draft tenant=%s exam=%s actor=%s at=%s", tenantID, examID, actorID, exportedAt)
	_ = writer.Write([]string{"submission_id", "student_id", "anonymous_code", "total_score", "max_score", "status", "locked", "exported_by", "exported_at", "watermark"})
	for _, grade := range grades {
		_ = writer.Write(csvsafe.Row([]string{
			grade.SubmissionID,
			grade.StudentID,
			grade.AnonymousCode,
			fmt.Sprintf("%.2f", grade.TotalScore),
			fmt.Sprintf("%.2f", grade.MaxScore),
			grade.Status,
			fmt.Sprintf("%t", grade.Locked),
			actorID,
			exportedAt,
			watermark,
		}))
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{
		Filename:    fmt.Sprintf("exam-%s-internal-grades.csv", examID),
		ContentType: "text/csv; charset=utf-8",
		Content:     buf.Bytes(),
		RowCount:    len(grades),
		Watermark:   watermark,
	}, nil
}
