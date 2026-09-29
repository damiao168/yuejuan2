package seedquality

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/calibration"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
)

type randomSource interface {
	Float64() float64
	Intn(int) int
}

type cryptoRandom struct{}

func (cryptoRandom) Float64() float64 {
	var value uint64
	_ = binary.Read(rand.Reader, binary.BigEndian, &value)
	return float64(value>>11) / float64(uint64(1)<<53)
}

func (cryptoRandom) Intn(bound int) int {
	if bound <= 1 {
		return 0
	}
	var value uint64
	_ = binary.Read(rand.Reader, binary.BigEndian, &value)
	return int(value % uint64(bound))
}

type Service struct {
	store         Store
	gold          goldpaper.ActiveApprovedReader
	qualification calibration.QualificationGate
	contexts      ContextSource
	random        randomSource
	now           func() time.Time
}

func NewService(store Store, gold goldpaper.ActiveApprovedReader, qualification calibration.QualificationGate, contexts ...ContextSource) *Service {
	service := &Service{store: store, gold: gold, qualification: qualification, random: cryptoRandom{}, now: func() time.Time { return time.Now().UTC() }}
	if len(contexts) > 0 {
		service.contexts = contexts[0]
	}
	return service
}

// 保存策略时先绑定当前已发布 Gold 集合的指纹；Gold 变更后旧策略必须重新确认。
func (s *Service) PutPolicy(ctx context.Context, tenantID, examID, questionID, actorID string, input PutPolicyInput) (Policy, error) {
	if !validPolicyInput(tenantID, examID, questionID, actorID, input) {
		return Policy{}, ErrInvalidInput
	}
	items, err := s.gold.ListActiveApproved(ctx, tenantID, examID, questionID)
	if err != nil {
		return Policy{}, err
	}
	_, fingerprint := activeSamples(items)
	if fingerprint == "" {
		return Policy{}, ErrGoldSetMissing
	}
	return s.store.PutPolicy(ctx, tenantID, examID, questionID, actorID, input, fingerprint)
}

func (s *Service) GetPolicy(ctx context.Context, tenantID, examID, questionID string) (Policy, error) {
	return s.store.GetPolicy(ctx, tenantID, examID, questionID)
}

// MaybeIssue is the claim hook. Call it before claiming an ordinary review task.
// false means the caller should continue with the normal queue unchanged.
func (s *Service) MaybeIssue(ctx context.Context, tenantID, examID, questionID, questionNo, graderID string) (Task, bool, error) {
	if tenantID == "" || examID == "" || questionID == "" || graderID == "" {
		return Task{}, false, ErrInvalidInput
	}
	policy, err := s.store.GetPolicy(ctx, tenantID, examID, questionID)
	// 没有策略或策略已暂停时放行普通队列，不能因为 Seed 配置缺失阻塞阅卷。
	if errors.Is(err, ErrNotFound) || policy.Status == PolicyPaused {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, err
	}
	if s.qualification == nil || s.qualification.RequireQualification(ctx, tenantID, examID, questionID, graderID) != nil {
		return Task{}, false, ErrQualificationNeeded
	}
	items, err := s.gold.ListActiveApproved(ctx, tenantID, examID, questionID)
	if err != nil {
		return Task{}, false, err
	}
	samples, fingerprint := activeSamples(items)
	if fingerprint == "" {
		return Task{}, false, ErrGoldSetMissing
	}
	// Gold 集合发生变化时拒绝继续抽样，避免同一策略混用不同版本的参考答案。
	if fingerprint != policy.ActiveGoldFingerprint {
		return Task{}, false, ErrGoldSetChanged
	}
	gold := samples[s.random.Intn(len(samples))]
	next := policy.MinInterval
	if width := policy.MaxInterval - policy.MinInterval + 1; width > 1 {
		next += s.random.Intn(width)
	}
	// 随机数只负责给出本次决策；游标递增和任务创建必须由存储层原子完成，避免并发重复发题。
	return s.store.AdvanceAndMaybeCreate(ctx, tenantID, IssueDecision{
		Policy: policy, QuestionNo: strings.TrimSpace(questionNo), GraderID: graderID, Gold: gold,
		Probability: s.random.Float64(), NextInterval: next, Now: s.now(),
	})
}

// TrySubmit is the submit hook. handled=false means id is an ordinary review
// task. A handled Seed returns only an unrevealing receipt.
func (s *Service) TrySubmit(ctx context.Context, tenantID, taskID, graderID string, input SubmitInput) (SubmitReceipt, bool, error) {
	if tenantID == "" || taskID == "" || graderID == "" || !validScore(input.Score) || input.ExpectedRevision <= 0 {
		return SubmitReceipt{}, false, ErrInvalidInput
	}
	task, err := s.store.GetTask(ctx, tenantID, taskID)
	if errors.Is(err, ErrNotFound) {
		return SubmitReceipt{}, false, nil
	}
	if err != nil {
		return SubmitReceipt{}, true, err
	}
	if task.AssignedTo != graderID {
		return SubmitReceipt{}, true, ErrSeedTaskForbidden
	}
	if input.Score > task.MaxScore {
		return SubmitReceipt{}, true, ErrInvalidInput
	}
	selections := cloneObject(input.RubricSelections)
	agreement, detail := compareCriteria(task.ExpectedCriteria, selections)
	// 扩展题按 trait 记录，其他题按 rubric criterion 记录；两种数据对应不同的质量统计维度。
	kind := ObservationCriterion
	traits, criteria := map[string]any(nil), detail
	if task.ArchetypeCode == "extended_response" {
		kind, traits, criteria = ObservationTrait, selections, nil
	}
	now := s.now()
	observation := Observation{ExamID: task.ExamID, QuestionID: task.QuestionID, GraderID: graderID,
		GoldPaperID: task.GoldPaperID, GoldVersion: task.GoldVersion, SubmittedScore: input.Score,
		ReferenceScore: task.ReferenceScore, RubricSelections: selections,
		AbsoluteError: math.Abs(input.Score - task.ReferenceScore), RubricAgreement: agreement,
		ObservationKind: kind, TraitObservation: traits, CriterionObservation: criteria, ObservedAt: now}
	// 完成时保存内部参考信息供审计，但接口只返回任务状态和版本，不能泄露 Gold 答案。
	completed, _, err := s.store.CompleteTask(ctx, tenantID, taskID, graderID, input, observation)
	if err != nil {
		return SubmitReceipt{}, true, err
	}
	return SubmitReceipt{TaskID: completed.ID, Status: completed.Status, Revision: completed.Revision}, true, nil
}

func (s *Service) ListObservations(ctx context.Context, tenantID string, filter ObservationFilter) ([]Observation, error) {
	if tenantID == "" {
		return nil, ErrInvalidInput
	}
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	return s.store.ListObservations(ctx, tenantID, filter)
}

// GetTaskForGrader is the context/image hook used by the normal workbench
// routes. JSON still omits all Seed-only fields.
func (s *Service) GetTaskForGrader(ctx context.Context, tenantID, taskID, graderID string) (Task, bool, error) {
	task, err := s.store.GetTask(ctx, tenantID, taskID)
	if errors.Is(err, ErrNotFound) {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, true, err
	}
	if task.AssignedTo != graderID {
		return Task{}, true, ErrSeedTaskForbidden
	}
	return task, true, nil
}

// 只取当前 active 且已批准的版本，并按稳定顺序计算指纹，保证重试得到同一套 Gold 集合。
func activeSamples(items []goldpaper.GoldPaper) ([]GoldSample, string) {
	samples := make([]GoldSample, 0, len(items))
	for _, item := range items {
		if item.Status != goldpaper.StatusActive || item.ActiveVersion <= 0 {
			continue
		}
		for _, version := range item.Versions {
			if version.Version != item.ActiveVersion || version.ApprovedAt == nil {
				continue
			}
			samples = append(samples, GoldSample{GoldPaperID: item.ID, GoldVersion: version.Version,
				SnapshotID: version.ExamQuestionSnapshotID, AnswerImageURL: item.AnswerImageURL,
				ReferenceScore: version.ReferenceScore, MaxScore: version.MaxScore,
				ExpectedCriteria: cloneObject(version.TraitScores), ArchetypeCode: item.ArchetypeCode})
		}
	}
	if len(samples) == 0 {
		return nil, ""
	}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].GoldPaperID == samples[j].GoldPaperID {
			return samples[i].GoldVersion < samples[j].GoldVersion
		}
		return samples[i].GoldPaperID < samples[j].GoldPaperID
	})
	digest := sha256.New()
	for _, sample := range samples {
		digest.Write([]byte(sample.GoldPaperID))
		digest.Write([]byte{0})
		digest.Write([]byte(strconv.Itoa(sample.GoldVersion)))
		digest.Write([]byte{0})
		digest.Write([]byte(sample.SnapshotID))
		digest.Write([]byte{0})
	}
	return samples, hex.EncodeToString(digest.Sum(nil))
}

func validPolicyInput(tenantID, examID, questionID, actorID string, input PutPolicyInput) bool {
	return tenantID != "" && examID != "" && questionID != "" && actorID != "" &&
		validRate(input.Rate) && input.Rate > 0 && input.MinInterval > 0 && input.MaxInterval >= input.MinInterval &&
		input.MaxInterval <= 100000 && (input.Status == PolicyActive || input.Status == PolicyPaused) && input.ExpectedRevision >= 0
}

func validRate(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}
func validScore(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 }

// 没有期望 rubric 时保留 agreement 为空；缺少规则不能被误算成零分一致率。
func compareCriteria(expected, submitted map[string]any) (*float64, map[string]any) {
	if len(expected) == 0 {
		return nil, cloneObject(submitted)
	}
	expectedFlat, submittedFlat := map[string]string{}, map[string]string{}
	flatten("", expected, expectedFlat)
	flatten("", submitted, submittedFlat)
	correct := 0
	detail := make(map[string]any, len(expectedFlat))
	for key, expectedValue := range expectedFlat {
		actual, ok := submittedFlat[key]
		match := ok && actual == expectedValue
		if match {
			correct++
		}
		detail[key] = map[string]any{"selected": submittedValue(submitted, key), "agreed": match}
	}
	value := float64(correct) / float64(len(expectedFlat))
	return &value, detail
}

func flatten(prefix string, value any, out map[string]string) {
	switch current := value.(type) {
	case map[string]any:
		for key, item := range current {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			flatten(path, item, out)
		}
	default:
		out[prefix] = strings.TrimSpace(strings.ToLower(toString(current)))
	}
}

func submittedValue(root map[string]any, path string) any {
	var current any = root
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[part]
	}
	return current
}

func toString(value any) string {
	switch current := value.(type) {
	case string:
		return current
	case nil:
		return ""
	case bool:
		return strconv.FormatBool(current)
	case float64:
		return strconv.FormatFloat(current, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(current), 'f', -1, 32)
	case int:
		return strconv.Itoa(current)
	case int64:
		return strconv.FormatInt(current, 10)
	default:
		return fmt.Sprint(current)
	}
}

func cloneObject(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		switch current := item.(type) {
		case map[string]any:
			out[key] = cloneObject(current)
		case []any:
			out[key] = append([]any(nil), current...)
		default:
			out[key] = current
		}
	}
	return out
}
