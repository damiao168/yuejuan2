package review

import (
	"context"
	"math"
	"strings"
	"time"
)

type memoryTaskClaim struct {
	OwnerID   string
	ClaimedAt time.Time
	ExpiresAt time.Time
}

func (s *MemoryStore) submitGrade(requestCtx context.Context, tenantID string, id string, reviewerID string, input SubmitGradeInput, marked bool) (SubmitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var replay SubmitResult
	if found, err := s.receipts.Load(requestCtx, tenantID, reviewerID, "review.submit", id, input, &replay); err != nil || found {
		return replay, err
	}
	task, ok := s.tasks[key(tenantID, id)]
	if !ok {
		return SubmitResult{}, ErrNotFound
	}
	if task.AssignedTo != reviewerID {
		return SubmitResult{}, ErrForbidden
	}
	// 新自动命令只读回已提交的正式分，绝不把新输入当作改分；原命令重放已在上方返回。
	if input.AutoSubmit && submittedReviewStatus(task.Status) {
		grade, found := s.latestGradeLocked(tenantID, task.ID)
		if !found || grade.ReviewerID != reviewerID {
			return SubmitResult{}, ErrInvalidTransition
		}
		result := SubmitResult{Task: cloneTask(task), Grade: cloneGrade(grade)}
		if err := s.receipts.Save(requestCtx, tenantID, reviewerID, "review.submit", id, input, result); err != nil {
			return SubmitResult{}, err
		}
		return result, nil
	}
	if !canSubmit(task.Status) {
		return SubmitResult{}, ErrInvalidTransition
	}
	if input.ExpectedRevision <= 0 || task.Revision != input.ExpectedRevision {
		return SubmitResult{}, ErrRevisionConflict
	}
	if input.AutoSubmit && marked {
		return SubmitResult{}, ErrTaskMarked
	}
	ctx, ok := s.contexts[key(tenantID, task.AnswerSegmentID)]
	if !ok {
		return SubmitResult{}, ErrNotFound
	}
	if err := validateSubmit(input, ctx); err != nil {
		return SubmitResult{}, err
	}
	if err := requireSubmissionQualification(requestCtx, tenantID, reviewerID, task, ctx); err != nil {
		return SubmitResult{}, err
	}
	now := time.Now().UTC()
	grade := HumanGrade{
		ID:               s.id("human-grade"),
		TenantID:         tenantID,
		ReviewTaskID:     task.ID,
		AnswerSegmentID:  task.AnswerSegmentID,
		ReviewerID:       reviewerID,
		Score:            input.Score,
		MaxScore:         ctx.Question.Score,
		RubricSelections: cloneSelections(input.RubricSelections),
		Comments:         strings.TrimSpace(input.Comments),
		PrivateNote:      strings.TrimSpace(input.PrivateNote),
		StudentFeedback:  strings.TrimSpace(input.StudentFeedback),
		Reason:           strings.TrimSpace(input.Reason),
		GradeRound:       task.GradeRound,
		AIGradeID:        aiGradeIDFromContext(ctx),
		CreatedAt:        now,
	}
	task.Status = "submitted"
	task.Revision++
	task.UpdatedAt = now
	s.tasks[key(tenantID, id)] = task
	s.grades[key(tenantID, task.ID)] = append(s.grades[key(tenantID, task.ID)], grade)
	session, finalGrade, arbitrationTask, err := s.resolveDoubleMarkAfterGradeLocked(tenantID, reviewerID, task)
	if err != nil {
		return SubmitResult{}, err
	}
	if refreshed, ok := s.tasks[key(tenantID, id)]; ok {
		task = refreshed
	}
	result := SubmitResult{
		Task:              cloneTask(task),
		Grade:             cloneGrade(grade),
		DoubleMarkSession: cloneSessionPtr(session),
		FinalGrade:        cloneFinalGradePtr(finalGrade),
		ArbitrationTask:   cloneArbitrationPtr(arbitrationTask),
	}
	if err := s.receipts.Save(requestCtx, tenantID, reviewerID, "review.submit", id, input, result); err != nil {
		return SubmitResult{}, err
	}
	return result, nil
}

func (s *MemoryStore) ReturnTask(_ context.Context, tenantID string, id string, actorID string, input ReturnTaskInput) (ReviewTask, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || input.ExpectedRevision <= 0 {
		return ReviewTask{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[key(tenantID, id)]
	if !ok {
		return ReviewTask{}, ErrNotFound
	}
	if input.MustOwnActiveClaim {
		claim, claimed := s.claims[key(tenantID, id)]
		if task.AssignedTo != actorID || !claimed || claim.OwnerID != actorID || !claim.ExpiresAt.After(time.Now().UTC()) {
			return ReviewTask{}, ErrForbidden
		}
	}
	if !canReturnTask(task.Status) {
		return ReviewTask{}, ErrInvalidTransition
	}
	if task.Revision != input.ExpectedRevision {
		return ReviewTask{}, ErrRevisionConflict
	}
	task.Status = "returned"
	task.ReturnReason = input.Reason
	task.Revision++
	task.UpdatedAt = time.Now().UTC()
	s.tasks[key(tenantID, id)] = task
	return cloneTask(task), nil
}

func (s *MemoryStore) latestGradeLocked(tenantID string, taskID string) (HumanGrade, bool) {
	grades := s.grades[key(tenantID, taskID)]
	if len(grades) == 0 {
		return HumanGrade{}, false
	}
	return grades[len(grades)-1], true
}

func normalizeCreateTask(input CreateTaskInput) CreateTaskInput {
	input.Source = strings.TrimSpace(input.Source)
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	input.GradeRound = normalizeGradeRound(input.GradeRound)
	return input
}

func validateCreateTask(input CreateTaskInput) error {
	if input.AnswerSegmentID == "" || !validSource(input.Source) || input.Priority < 0 || !validGradeRound(input.GradeRound) {
		return ErrInvalidInput
	}
	return nil
}

func validateSubmit(input SubmitGradeInput, ctx Context) error {
	if !validScore(input.Score) || !validScore(ctx.Question.Score) || input.Score > ctx.Question.Score {
		return ErrInvalidInput
	}
	hasRubric := ctx.Rubric.ID != "" || len(ctx.Rubric.Points) > 0
	if hasRubric && (!validScore(ctx.Rubric.MaxScore) || input.Score > ctx.Rubric.MaxScore) {
		return ErrInvalidInput
	}
	// 先拒绝服务端细则的无效或重复定义，再校验客户端选择及逐项累计值。
	rubricPoints, err := submitRubricPointScores(ctx)
	if err != nil {
		return err
	}
	selectedScore, err := selectedRubricScore(input.RubricSelections, rubricPoints)
	if err != nil {
		return err
	}
	// 显式总分模式和兼容的自动模式可不勾选采分点；已有真实选择仍须核对累计值。
	// 逐点提交继续要求总分与所选得分点之和相等，只容忍 0.0001 分浮点误差。
	if hasRubric && ((!input.AutoSubmit && !input.ScoreOnly) || len(input.RubricSelections) > 0) && math.Abs(input.Score-selectedScore) > 0.0001 {
		return ErrInvalidInput
	}
	return nil
}

func submitRubricPointScores(ctx Context) (map[string]float64, error) {
	rubricPoints := map[string]float64{}
	for _, point := range ctx.Rubric.Points {
		if point.ID == "" || !validScore(point.Score) {
			return nil, ErrInvalidInput
		}
		if _, duplicate := rubricPoints[point.ID]; duplicate {
			return nil, ErrInvalidInput
		}
		rubricPoints[point.ID] = point.Score
	}
	return rubricPoints, nil
}

func selectedRubricScore(selections []RubricSelection, rubricPoints map[string]float64) (float64, error) {
	seenPoints := map[string]bool{}
	selectedScore := 0.0
	for _, selection := range selections {
		if selection.PointID == "" || !validScore(selection.Score) {
			return 0, ErrInvalidInput
		}
		pointMax, ok := rubricPoints[selection.PointID]
		if len(rubricPoints) == 0 || !ok || selection.Score > pointMax || seenPoints[selection.PointID] {
			return 0, ErrInvalidInput
		}
		seenPoints[selection.PointID] = true
		selectedScore += selection.Score
		if math.IsInf(selectedScore, 0) {
			return 0, ErrInvalidInput
		}
	}
	return selectedScore, nil
}

// aiGradeIDFromContext 使用提交时服务端加载的 AI 建议 ID，不接受客户端指定关联。
// 没有有效 ID 时返回空串；数据库写入时将其转为 NULL。
func aiGradeIDFromContext(ctx Context) string {
	value, _ := ctx.AISuggestion["ai_grade_id"].(string)
	return strings.TrimSpace(value)
}

func validScore(score float64) bool {
	return !math.IsNaN(score) && !math.IsInf(score, 0) && score >= 0
}

func canReturnTask(status string) bool {
	return status != "submitted" && status != "completed"
}

func validSource(source string) bool {
	switch source {
	case "ai_low_confidence", "ocr_low_confidence", "subjective_default_review", "evidence_verification_failed", "double_mark_required", "score_anomaly", "manual_sample", "ai_human_disagreement":
		return true
	default:
		return false
	}
}

func canSubmit(status string) bool {
	switch status {
	case "assigned", "in_progress", "returned":
		return true
	default:
		return false
	}
}

func normalizeGradeRound(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "single"
	}
	return value
}

func validGradeRound(value string) bool {
	switch value {
	case "single", "first_mark", "second_mark", "appeal_review":
		return true
	default:
		return false
	}
}
