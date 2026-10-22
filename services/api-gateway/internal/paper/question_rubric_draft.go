package paper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

// QuestionRubricExamReader 读取当前身份可见的考试，正式题目的学科不能由浏览器指定。
type QuestionRubricExamReader interface {
	GetExam(context.Context, auth.AccessScope, string) (exam.Exam, error)
}

type QuestionMathRubricDraftInput struct {
	ExpectedQuestion *Question                       `json:"expected_question"`
	SolutionSegments []QuestionRubricSolutionSegment `json:"solution_segments,omitempty"`
}

func (h *Handler) WithQuestionRubricExams(exams QuestionRubricExamReader) *Handler {
	h.questionRubricExams = exams
	return h
}

// SuggestQuestionMathRubricDraft 只返回待复核建议；采用与锁定仍走原有评分细则保存接口。
func (h *Handler) SuggestQuestionMathRubricDraft(w http.ResponseWriter, r *http.Request) {
	if h.documentImport == nil || h.questionRubricExams == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "math_rubric_draft_unavailable", "数学分值建议服务未配置")
		return
	}
	var input QuestionMathRubricDraftInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ExpectedQuestion == nil {
		httpx.Error(w, r, http.StatusBadRequest, "expected_question_required", "请提供当前已读取的题目版本")
		return
	}
	user := mustUser(r)
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok || scope.TenantID != user.TenantID {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_forbidden", "考试不在当前访问范围内")
		return
	}
	item, err := h.questionRubricExams.GetExam(r.Context(), scope, r.PathValue("examId"))
	if errors.Is(err, exam.ErrNotFound) || errors.Is(err, exam.ErrScopeForbidden) {
		writeStoreError(w, r, ErrNotFound)
		return
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if item.TenantID != user.TenantID || item.ID != r.PathValue("examId") {
		writeStoreError(w, r, ErrNotFound)
		return
	}
	if item.Status != "draft" && item.Status != "configured" {
		writeStoreError(w, r, ErrExamFrozen)
		return
	}
	subject, valid := assessment.NormalizeSubjectCode(item.Subject)
	if !valid || subject != assessment.SubjectMathematics {
		httpx.Error(w, r, http.StatusBadRequest, "math_rubric_draft_subject_required", "此分值建议仅适用于数学解答题")
		return
	}
	// 旧客户端仍可同步等待，但必须经过同一持久化队列与并发占位，不能绕过 5 个上限。
	if h.rubricDraftJobs != nil {
		h.waitQuestionRubricDraftJob(w, r, scope, input)
		return
	}
	result, err := suggestQuestionMathRubricDraft(r.Context(), h.questions, h.documentImport, user.TenantID, item.ID, r.PathValue("questionId"), input)
	if err != nil {
		if errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrRubricLocked) {
			writeStoreError(w, r, err)
			return
		}
		if errors.Is(err, ErrConflict) {
			httpx.Error(w, r, http.StatusConflict, "question_rubric_draft_conflict", "题目、答案或评分细则已变更，请刷新后重新生成建议")
			return
		}
		httpx.Error(w, r, http.StatusBadGateway, "math_rubric_draft_failed", "数学分值建议暂不可用，可先按答案步骤手动划分")
		return
	}
	// 推断期间考试也可能进入采集或阅卷；这时不能继续采用配置建议。
	latestExam, err := h.questionRubricExams.GetExam(r.Context(), scope, item.ID)
	if err != nil || latestExam.TenantID != user.TenantID || latestExam.ID != item.ID {
		writeStoreError(w, r, ErrNotFound)
		return
	}
	if latestExam.Status != "draft" && latestExam.Status != "configured" {
		writeStoreError(w, r, ErrExamFrozen)
		return
	}
	latestSubject, valid := assessment.NormalizeSubjectCode(latestExam.Subject)
	if !valid || latestSubject != subject {
		writeStoreError(w, r, ErrConflict)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func suggestQuestionMathRubricDraft(ctx context.Context, questions QuestionRepository, service *DocumentImportService, tenantID, examID, questionID string, input QuestionMathRubricDraftInput) (MathRubricDraftResponse, error) {
	if input.ExpectedQuestion == nil || service == nil || questions == nil {
		return MathRubricDraftResponse{}, ErrInvalidInput
	}
	current, err := readQuestionRubricDraft(ctx, questions, tenantID, examID, questionID)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	snapshot, err := questionRubricDraftSnapshot(current)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	expected, err := questionRubricDraftSnapshot(*input.ExpectedQuestion)
	if err != nil {
		return MathRubricDraftResponse{}, ErrInvalidInput
	}
	if !bytes.Equal(snapshot, expected) {
		return MathRubricDraftResponse{}, ErrConflict
	}
	// 输入只从仓储复制；请求里的题目快照只能拒绝过期界面，不能替换模型使用的参考答案。
	data, err := json.Marshal(current)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	if err = json.Unmarshal(data, &current); err != nil {
		return MathRubricDraftResponse{}, err
	}
	inputs, err := prepareQuestionMathRubricInputs(current)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	if input.SolutionSegments != nil {
		inputs.solution.Steps, inputs.solution.RawText, err = questionRubricSegmentSteps(inputs.solution.Steps, input.SolutionSegments)
		if err != nil {
			return MathRubricDraftResponse{}, err
		}
	}
	client, ok := service.parser.(*HTTPDocumentParser)
	if !ok {
		return MathRubricDraftResponse{}, errors.New("math rubric draft service is unavailable")
	}
	var model *DocumentModelConfig
	if service.modelResolver != nil {
		model, err = service.modelResolver(ctx, tenantID)
		if err != nil {
			return MathRubricDraftResponse{}, errors.New("school model configuration unavailable")
		}
	}
	reference, err := newQuestionRubricReference(current, snapshot)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	result, err := client.suggestMathRubricDraft(ctx, inputs.question, inputs.answer, inputs.solution, model, reference)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	latest, err := readQuestionRubricDraft(ctx, questions, tenantID, examID, questionID)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	latestSnapshot, err := questionRubricDraftSnapshot(latest)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	// 慢调用期间新增评分版本或改动答案后，旧建议不能继续显示为当前题目的建议。
	if !bytes.Equal(snapshot, latestSnapshot) {
		return MathRubricDraftResponse{}, ErrConflict
	}
	return result, nil
}

func readQuestionRubricDraft(ctx context.Context, questions QuestionRepository, tenantID, examID, questionID string) (Question, error) {
	items, err := questions.ListQuestions(ctx, tenantID, examID)
	if err != nil {
		return Question{}, err
	}
	for _, item := range items {
		if item.ID == questionID && item.TenantID == tenantID && item.ExamID == examID && item.Status != "deleted" {
			return item, nil
		}
	}
	return Question{}, ErrNotFound
}

func questionRubricDraftSnapshot(question Question) ([]byte, error) {
	return json.Marshal(struct {
		ID              string   `json:"id"`
		TenantID        string   `json:"tenant_id"`
		ExamID          string   `json:"exam_id"`
		QuestionNo      string   `json:"question_no"`
		QuestionType    string   `json:"question_type"`
		Score           float64  `json:"score"`
		Stem            string   `json:"stem"`
		Options         []string `json:"options"`
		KnowledgePoints []string `json:"knowledge_points"`
		// 与 Question 的 GET 序列化一致：空来源会被省略，回传 nil 与空数组是同一份快照。
		SourceRefs []PaperImportSourceRef `json:"paper_import_source_refs,omitempty"`
		AnswerKey  *AnswerKey             `json:"answer_key"`
		Solution   *QuestionSolution      `json:"solution"`
		Rubric     *Rubric                `json:"rubric"`
	}{question.ID, question.TenantID, question.ExamID, question.QuestionNo, question.QuestionType, question.Score,
		question.Stem, question.Options, question.KnowledgePoints, question.PaperImportSourceRefs, question.AnswerKey, question.Solution, question.Rubric})
}

func prepareQuestionMathRubricInputs(question Question) (reviewedMathRubricInputs, error) {
	if question.Rubric != nil && question.Rubric.Status == "locked" {
		return reviewedMathRubricInputs{}, ErrRubricLocked
	}
	if math.IsNaN(question.Score) || math.IsInf(question.Score, 0) || question.Score <= 0 {
		return reviewedMathRubricInputs{}, ErrInvalidInput
	}
	if _, err := reviewedMathRubricDraft([]PaperImportDraftQuestion{{CandidateID: question.ID, QuestionType: question.QuestionType}}, question.ID); err != nil {
		return reviewedMathRubricInputs{}, err
	}
	questionNo := normalizePaperImportQuestionNumber(question.QuestionNo)
	if questionNo == "" {
		return reviewedMathRubricInputs{}, ErrInvalidInput
	}
	steps, rawText, refs := questionMathRubricSolution(question)
	if len(mathRubricEvidenceSteps(steps)) == 0 {
		return reviewedMathRubricInputs{}, ErrInvalidInput
	}
	if refs == nil {
		refs = []PaperImportSourceRef{}
	}
	score := question.Score
	inputs := reviewedMathRubricInputs{
		question: QuestionCandidate{CandidateID: question.ID, QuestionNoNormalized: questionNo, QuestionType: question.QuestionType,
			Stem: question.Stem, Options: question.Options, Score: &score, KnowledgePointHints: question.KnowledgePoints, SourceRefs: question.PaperImportSourceRefs},
		solution: SolutionCandidate{CandidateID: "question-solution-" + question.ID, QuestionNoNormalized: questionNo,
			RawText: rawText, Steps: steps, SourceRefs: refs},
	}
	if question.AnswerKey != nil {
		inputs.answer = &AnswerCandidate{CandidateID: question.AnswerKey.ID, QuestionNoNormalized: questionNo,
			StandardAnswer: question.AnswerKey.StandardAnswer, EquivalentAnswers: question.AnswerKey.EquivalentAnswers,
			Tolerance: question.AnswerKey.Tolerance, SourceRefs: question.AnswerKey.PaperImportSourceRefs}
	}
	return inputs, nil
}

func questionMathRubricSolution(question Question) ([]SolutionStep, string, []PaperImportSourceRef) {
	if solution := question.Solution; solution != nil {
		if len(mathRubricEvidenceSteps(solution.Steps)) > 0 {
			return solution.Steps, solution.RawText, solution.SourceRefs
		}
		if steps := mathRubricTextSteps(solution.RawText); len(steps) > 0 {
			return steps, solution.RawText, solution.SourceRefs
		}
	}
	if answer := question.AnswerKey; answer != nil {
		if text, ok := answer.StandardAnswer.(string); ok {
			return mathRubricTextSteps(text), text, answer.PaperImportSourceRefs
		}
	}
	return nil, "", nil
}

// 多行公式仍是一段证据；这里与浏览器的逻辑行规则一致，证据 ID 使用原步骤数组位置而非 StepNo。
func mathRubricTextSteps(text string) []SolutionStep {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	steps := []SolutionStep{}
	var buffer []string
	closing := ""
	for _, line := range strings.Split(text, "\n") {
		if closing == "" && strings.TrimSpace(line) == "" {
			continue
		}
		buffer = append(buffer, line)
		for index := 0; index < len(line); {
			if closing != "" {
				end := strings.Index(line[index:], closing)
				if end < 0 {
					break
				}
				index += end + len(closing)
				closing = ""
				continue
			}
			if strings.HasPrefix(line[index:], "$$") {
				closing = "$$"
				index += 2
			} else if strings.HasPrefix(line[index:], `\[`) {
				closing = `\]`
				index += 2
			} else {
				index++
			}
		}
		if closing == "" {
			steps = append(steps, SolutionStep{StepNo: len(steps) + 1, Content: strings.TrimSpace(strings.Join(buffer, "\n"))})
			buffer = nil
		}
	}
	if len(buffer) > 0 {
		steps = append(steps, SolutionStep{StepNo: len(steps) + 1, Content: strings.TrimSpace(strings.Join(buffer, "\n"))})
	}
	return steps
}
