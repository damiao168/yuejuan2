package graderdrift

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type incidentCursor struct {
	CreatedAt time.Time `json:"at"`
	ID        string    `json:"id"`
}

func decodeIncidentCursor(value string) (incidentCursor, error) {
	var cursor incidentCursor
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) > 512 || json.Unmarshal(raw, &cursor) != nil || cursor.CreatedAt.IsZero() {
		return cursor, ErrInvalidInput
	}
	if _, err := uuid.Parse(cursor.ID); err != nil {
		return cursor, ErrInvalidInput
	}
	return cursor, nil
}

func encodeIncidentCursor(item Incident) string {
	raw, _ := json.Marshal(incidentCursor{CreatedAt: item.CreatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// OpenIncidentCounts 专供门禁取得完整计数；展示分页或已解决历史不能决定是否仍有严重事件。
func (s *Service) OpenIncidentCounts(ctx context.Context, tenantID, examID, questionID string) (IncidentCounts, error) {
	counter, ok := s.store.(interface {
		CountOpenIncidents(context.Context, string, string, string) (IncidentCounts, error)
	})
	if !ok || tenantID == "" || examID == "" {
		return IncidentCounts{}, ErrInvalidInput
	}
	return counter.CountOpenIncidents(ctx, tenantID, examID, questionID)
}
