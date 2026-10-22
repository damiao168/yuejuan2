package paper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"github.com/google/uuid"
)

const RubricDraftConcurrency = 5
const rubricDraftQueueLimit = 20
const rubricDraftRevision = "math-rubric-jobs-v1"

var ErrRubricDraftQueueFull = errors.New("rubric draft queue is full")

// RubricDraftJob 的结果始终是待复核建议；此队列不写正式评分细则。
// Input 只含教师有权读取的题目与分段，模型密钥不会进入任务表或浏览器。
type RubricDraftJob struct {
	ID         string                       `json:"id"`
	ExamID     string                       `json:"exam_id"`
	QuestionID string                       `json:"question_id"`
	QuestionNo string                       `json:"question_no"`
	Status     string                       `json:"status"`
	RequestID  string                       `json:"request_id"`
	Input      QuestionMathRubricDraftInput `json:"input"`
	Result     *MathRubricDraftResponse     `json:"result,omitempty"`
	Error      string                       `json:"error,omitempty"`
	ModelName  string                       `json:"model_name"`
	Attempt    int                          `json:"attempt"`
	CreatedAt  time.Time                    `json:"created_at"`
	UpdatedAt  time.Time                    `json:"updated_at"`
}

type CreateRubricDraftJobInput struct {
	QuestionMathRubricDraftInput
	RequestID  string `json:"request_id"`
	Regenerate bool   `json:"regenerate"`
}

// 身份范围由认证入口生成并单独保存，不能从浏览器反序列化为后台权限。
type rubricDraftJobRecord struct {
	Job         RubricDraftJob
	TenantID    string
	ActorID     string
	Scope       auth.AccessScope
	Fingerprint string
	ModelHash   string
	Local       bool
	LeaseToken  string
	LeaseUntil  time.Time
	AvailableAt time.Time
}

type rubricDraftJobStore interface {
	createRubricDraftJob(context.Context, rubricDraftJobRecord, bool) (RubricDraftJob, error)
	listRubricDraftJobs(context.Context, string, string, string) ([]RubricDraftJob, error)
	getRubricDraftJob(context.Context, string, string, string, string) (rubricDraftJobRecord, error)
	cancelRubricDraftJob(context.Context, string, string, string, string) (RubricDraftJob, error)
	claimRubricDraftJob(context.Context, time.Duration, string) (*rubricDraftJobRecord, error)
	finishRubricDraftJob(context.Context, rubricDraftJobRecord, string, *MathRubricDraftResponse, string, time.Time) error
	staleRubricDraftJob(context.Context, string, string, string, string) error
}

type RubricDraftJobs struct {
	store     rubricDraftJobStore
	questions QuestionRepository
	exams     QuestionRubricExamReader
	service   *DocumentImportService
	timeout   time.Duration
}

func NewRubricDraftJobs(store Store, exams QuestionRubricExamReader, service *DocumentImportService, timeout time.Duration) *RubricDraftJobs {
	repository, ok := store.(rubricDraftJobStore)
	if !ok || service == nil || exams == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &RubricDraftJobs{store: repository, questions: store, exams: exams, service: service, timeout: timeout}
}

func (s *RubricDraftJobs) model(ctx context.Context, tenantID string) (*DocumentModelConfig, string, error) {
	var model *DocumentModelConfig
	var err error
	if s.service.modelResolver != nil {
		model, err = s.service.modelResolver(ctx, tenantID)
	}
	if err != nil {
		return nil, "", err
	}
	// 只持久化摘要，用于拒绝排队期间变更的模型或密钥；执行时重新解析受管配置。
	data, err := json.Marshal([]any{rubricDraftRevision, model})
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(data)
	return model, hex.EncodeToString(hash[:]), nil
}

func (s *RubricDraftJobs) validate(ctx context.Context, scope auth.AccessScope, examID, questionID string, input QuestionMathRubricDraftInput) error {
	item, err := s.exams.GetExam(ctx, scope, examID)
	if errors.Is(err, exam.ErrNotFound) || errors.Is(err, exam.ErrScopeForbidden) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if item.TenantID != scope.TenantID || item.ID != examID {
		return ErrNotFound
	}
	if item.Status != "draft" && item.Status != "configured" {
		return ErrExamFrozen
	}
	subject, ok := assessment.NormalizeSubjectCode(item.Subject)
	if !ok || subject != assessment.SubjectMathematics {
		return ErrInvalidInput
	}
	if input.ExpectedQuestion == nil {
		return ErrInvalidInput
	}
	current, err := readQuestionRubricDraft(ctx, s.questions, scope.TenantID, examID, questionID)
	if err != nil {
		return err
	}
	expected, err := questionRubricDraftSnapshot(*input.ExpectedQuestion)
	if err != nil {
		return ErrInvalidInput
	}
	actual, err := questionRubricDraftSnapshot(current)
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, actual) {
		return ErrConflict
	}
	inputs, err := prepareQuestionMathRubricInputs(current)
	if err != nil {
		return err
	}
	if input.SolutionSegments != nil {
		_, _, err = questionRubricSegmentSteps(inputs.solution.Steps, input.SolutionSegments)
	}
	return err
}

func (s *RubricDraftJobs) Create(ctx context.Context, scope auth.AccessScope, examID, questionID string, input CreateRubricDraftJobInput) (RubricDraftJob, error) {
	if _, err := uuid.Parse(input.RequestID); err != nil || scope.ActorID == "" {
		return RubricDraftJob{}, ErrInvalidInput
	}
	if err := s.validate(ctx, scope, examID, questionID, input.QuestionMathRubricDraftInput); err != nil {
		return RubricDraftJob{}, err
	}
	model, modelHash, err := s.model(ctx, scope.TenantID)
	if err != nil {
		return RubricDraftJob{}, err
	}
	snapshot, _ := questionRubricDraftSnapshot(*input.ExpectedQuestion)
	data, err := json.Marshal([]any{rubricDraftRevision, string(snapshot), input.SolutionSegments, modelHash})
	if err != nil {
		return RubricDraftJob{}, err
	}
	hash := sha256.Sum256(data)
	now := time.Now().UTC()
	name := "本地模型"
	if model != nil {
		name = model.ModelName
	}
	record := rubricDraftJobRecord{
		Job: RubricDraftJob{ID: uuid.NewString(), ExamID: examID, QuestionID: questionID, QuestionNo: input.ExpectedQuestion.QuestionNo,
			Status: "queued", RequestID: input.RequestID, Input: input.QuestionMathRubricDraftInput, ModelName: name, CreatedAt: now, UpdatedAt: now},
		TenantID: scope.TenantID, ActorID: scope.ActorID, Scope: scope, Fingerprint: hex.EncodeToString(hash[:]), ModelHash: modelHash,
		Local: model == nil, AvailableAt: now,
	}
	return s.store.createRubricDraftJob(ctx, record, input.Regenerate)
}

func (s *RubricDraftJobs) Result(ctx context.Context, tenantID, actorID, examID, id string) (RubricDraftJob, error) {
	record, err := s.store.getRubricDraftJob(ctx, tenantID, actorID, examID, id)
	if err != nil {
		return RubricDraftJob{}, err
	}
	if record.Job.Status == "succeeded" {
		_, modelHash, modelErr := s.model(ctx, tenantID)
		if modelErr != nil {
			return RubricDraftJob{}, modelErr
		}
		validationErr := s.validate(ctx, record.Scope, examID, record.Job.QuestionID, record.Job.Input)
		if validationErr != nil && !errors.Is(validationErr, ErrConflict) && !errors.Is(validationErr, ErrNotFound) && !errors.Is(validationErr, ErrRubricLocked) && !errors.Is(validationErr, ErrExamFrozen) && !errors.Is(validationErr, ErrInvalidInput) {
			return RubricDraftJob{}, validationErr
		}
		if modelHash != record.ModelHash || validationErr != nil {
			if err := s.store.staleRubricDraftJob(ctx, tenantID, actorID, examID, id); err != nil {
				return RubricDraftJob{}, err
			}
			record.Job.Status, record.Job.Result, record.Job.Error = "stale", nil, "题目、答案、细则或模型已变更，请重新生成"
		}
	}
	return record.Job, nil
}

// Run 的生命周期属于进程，浏览器断开和换题不会取消任务。
// 多副本使用数据库领取锁共同限制最多 5 个未结束调用；本地模型再限制为 1 个。
func (s *RubricDraftJobs) Run(ctx context.Context, onError func(error)) {
	if s == nil {
		return
	}
	var workers sync.WaitGroup
	for i := 0; i < RubricDraftConcurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				record, err := s.store.claimRubricDraftJob(ctx, s.timeout+time.Minute, "")
				if err != nil && onError != nil {
					onError(err)
				}
				if err == nil && record != nil {
					s.execute(ctx, *record, onError)
					continue
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(500 * time.Millisecond):
				}
			}
		}()
	}
	workers.Wait()
}

func (s *RubricDraftJobs) execute(parent context.Context, record rubricDraftJobRecord, onError func(error)) {
	ctx, cancel := context.WithTimeout(db.WithTenant(parent, record.TenantID), s.timeout)
	defer cancel()
	status, message := "failed", "生成失败，可重试或按解答步骤配分"
	var result *MathRubricDraftResponse
	var retryAt time.Time
	err := s.validate(ctx, record.Scope, record.Job.ExamID, record.Job.QuestionID, record.Job.Input)
	model, modelHash, modelErr := s.model(ctx, record.TenantID)
	if err == nil && modelErr != nil {
		err = modelErr
	}
	if err == nil && modelHash != record.ModelHash {
		err = ErrConflict
	}
	if err == nil {
		// 不复制含锁的导入服务；只构造推断所需的解析器与已校验模型，避免二次解析换用另一配置。
		service := &DocumentImportService{parser: s.service.parser, modelResolver: func(context.Context, string) (*DocumentModelConfig, error) { return model, nil }}
		var response MathRubricDraftResponse
		response, err = suggestQuestionMathRubricDraft(ctx, s.questions, service, record.TenantID, record.Job.ExamID, record.Job.QuestionID, record.Job.Input)
		if err == nil {
			err = s.validate(ctx, record.Scope, record.Job.ExamID, record.Job.QuestionID, record.Job.Input)
		}
		if err == nil {
			_, latestHash, resolveErr := s.model(ctx, record.TenantID)
			err = resolveErr
			if err == nil && latestHash != record.ModelHash {
				err = ErrConflict
			}
		}
		if err == nil {
			status, message, result = "succeeded", "", &response
		}
	}
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrExamFrozen) || errors.Is(err, ErrRubricLocked) || errors.Is(err, ErrNotFound) {
		status, message = "stale", "题目、答案、细则或考试状态已变更，请核对后重新生成"
	}
	var upstream *rubricDraftHTTPError
	if errors.As(err, &upstream) && upstream.Status == 429 && record.Job.Attempt < 3 {
		status, message = "queued", "模型繁忙，稍后自动重试"
		retryAt = time.Now().UTC().Add(time.Duration(record.Job.Attempt*5) * time.Second)
	}
	if ctx.Err() != nil {
		message = "生成超时或服务中断，请手动重试；上次调用可能已计费"
	}
	// 停机时也要用独立短上下文落失败状态；租约令牌阻止过期执行者提交。
	finishCtx, finishCancel := context.WithTimeout(db.WithTenant(context.Background(), record.TenantID), 5*time.Second)
	defer finishCancel()
	if finishErr := s.store.finishRubricDraftJob(finishCtx, record, status, result, message, retryAt); finishErr != nil && onError != nil {
		onError(finishErr)
	}
}

type rubricDraftHTTPError struct{ Status int }

func (e *rubricDraftHTTPError) Error() string { return "math rubric draft service rejected request" }
