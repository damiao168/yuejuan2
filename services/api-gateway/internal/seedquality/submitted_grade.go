package seedquality

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

type GraderRubricSelection struct {
	PointID string  `json:"point_id"`
	Score   float64 `json:"score"`
}

type GraderHumanGrade struct {
	ID               string                  `json:"id"`
	TenantID         string                  `json:"tenant_id"`
	ReviewTaskID     string                  `json:"review_task_id"`
	AnswerSegmentID  string                  `json:"answer_segment_id"`
	ReviewerID       string                  `json:"reviewer_id"`
	Score            float64                 `json:"score"`
	MaxScore         float64                 `json:"max_score"`
	RubricSelections []GraderRubricSelection `json:"rubric_selections"`
	GradeRound       string                  `json:"grade_round"`
	CreatedAt        time.Time               `json:"created_at"`
}

type submittedOpinionReader interface {
	GetBookmarkOpinion(context.Context, string, string, string) (BookmarkOpinion, error)
}

// 复用本人意见读取边界，只投影已提交分数和数值采分点；内部参考分、误差和 Gold 来源均不进入工作台。
func (s *Service) graderSubmittedGrade(ctx context.Context, tenantID string, task Task) (*GraderHumanGrade, error) {
	if task.Status != "completed" {
		return nil, nil
	}
	reader, supported := s.store.(submittedOpinionReader)
	if !supported {
		return nil, ErrContextUnavailable
	}
	opinion, err := reader.GetBookmarkOpinion(ctx, tenantID, task.ID, task.AssignedTo)
	if err != nil {
		return nil, err
	}
	out := &GraderHumanGrade{ID: opaqueID(task.ID, "human-grade"), TenantID: tenantID, ReviewTaskID: task.ID,
		AnswerSegmentID: opaqueID(task.ID, "answer-artifact"), ReviewerID: task.AssignedTo,
		Score: opinion.Score, MaxScore: task.MaxScore, GradeRound: "first", CreatedAt: opinion.SubmittedAt}
	keys := make([]string, 0, len(opinion.RubricSelections))
	for key := range opinion.RubricSelections {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// trait 文本和布尔观察不是数值采分点，不能冒充 rubric_selections 写回正式分。
	out.RubricSelections = []GraderRubricSelection{}
	for _, key := range keys {
		if score, numeric := opinion.RubricSelections[key].(float64); numeric && validScore(score) {
			out.RubricSelections = append(out.RubricSelections, GraderRubricSelection{PointID: key, Score: score})
		}
	}
	return out, nil
}

func (s *PostgresStore) GetBookmarkOpinion(ctx context.Context, tenantID, taskID, ownerID string) (BookmarkOpinion, error) {
	var out BookmarkOpinion
	var selections []byte
	err := s.db.QueryRowContext(ctx, `SELECT o.submitted_score::float8,o.rubric_selection_json,o.observed_at
FROM seed_observation o JOIN seed_task t ON t.tenant_id=o.tenant_id AND t.id=o.seed_task_id
WHERE o.tenant_id=$1::uuid AND o.seed_task_id=$2::uuid AND o.grader_id=$3::uuid AND t.grader_id=$3::uuid AND t.status='completed'
ORDER BY o.observed_at DESC,o.id DESC LIMIT 1`, tenantID, taskID, ownerID).Scan(&out.Score, &selections, &out.SubmittedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BookmarkOpinion{}, ErrNotFound
		}
		return BookmarkOpinion{}, err
	}
	if err := json.Unmarshal(selections, &out.RubricSelections); err != nil {
		return BookmarkOpinion{}, err
	}
	return out, nil
}
