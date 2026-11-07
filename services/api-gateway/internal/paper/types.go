package paper

import (
	"context"
	"errors"
)

var (
	ErrNotFound               = errors.New("paper resource not found")
	ErrInvalidInput           = errors.New("invalid paper input")
	ErrRubricMismatch         = errors.New("rubric score does not equal question score")
	ErrRubricLocked           = errors.New("locked rubric cannot be modified")
	ErrTemplateLocked         = errors.New("locked template cannot be modified")
	ErrTemplateNotLocked      = errors.New("template must be locked before binding")
	ErrConflict               = errors.New("configuration revision conflict")
	ErrNotReady               = errors.New("exam configuration is not ready")
	ErrReadinessChanged       = errors.New("reviewed exam configuration changed")
	ErrExamFrozen             = errors.New("exam paper configuration is frozen")
	ErrAnswerSheetSourceInUse = errors.New("answer sheet source has protected references")
)

type FileAssetInput struct {
	OriginalName  string `json:"original_name"`
	ContentType   string `json:"content_type"`
	SizeBytes     int64  `json:"size_bytes"`
	HashSHA256    string `json:"hash_sha256"`
	StorageBucket string `json:"storage_bucket"`
	StorageKey    string `json:"storage_key"`
}

type Paper struct {
	// Duplicate 仅描述本次登记是否复用了已有来源，不写入试卷资源或数据库。
	Duplicate   bool           `json:"-"`
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id"`
	ExamID      string         `json:"exam_id"`
	FileAssetID string         `json:"file_asset_id"`
	VersionNo   int            `json:"version_no"`
	Status      string         `json:"status"`
	Purpose     string         `json:"purpose"`
	File        FileAssetInput `json:"file"`
}

type CreatePaperInput struct {
	Purpose     string         `json:"purpose"`
	FileAssetID string         `json:"file_asset_id"`
	File        FileAssetInput `json:"file"`
}

type AnswerKeyInput struct {
	StandardAnswer    any   `json:"standard_answer"`
	EquivalentAnswers []any `json:"equivalent_answers"`
	Tolerance         any   `json:"tolerance"`
}

type Question struct {
	ReferenceMaterials      *ReferenceMaterialBinding `json:"reference_materials,omitempty"`
	ParentQuestionNo        string                    `json:"parent_question_no,omitempty"`
	SubquestionNo           string                    `json:"subquestion_no,omitempty"`
	Options                 []string                  `json:"options"`
	SourceType              string                    `json:"source_type,omitempty"`
	SourceBankItemID        string                    `json:"source_bank_item_id,omitempty"`
	SourceBankItemVersionID string                    `json:"source_bank_item_version_id,omitempty"`
	SourceContentHash       string                    `json:"source_content_hash,omitempty"`
	BankContent             map[string]any            `json:"bank_content,omitempty"`
	ID                      string                    `json:"id"`
	TenantID                string                    `json:"tenant_id"`
	ExamID                  string                    `json:"exam_id"`
	ExamPaperID             string                    `json:"exam_paper_id,omitempty"`
	QuestionNo              string                    `json:"question_no"`
	QuestionType            string                    `json:"question_type"`
	Score                   float64                   `json:"score"`
	ScoreSource             string                    `json:"score_source,omitempty"`
	Stem                    string                    `json:"stem,omitempty"`
	KnowledgePoints         []string                  `json:"knowledge_points"`
	AnswerArea              map[string]any            `json:"answer_area,omitempty"`
	SortOrder               int                       `json:"sort_order"`
	Status                  string                    `json:"status"`
	AssessmentArchetype     string                    `json:"assessment_archetype,omitempty"`
	PaperImportID           string                    `json:"paper_import_id,omitempty"`
	PaperImportCandidateID  string                    `json:"paper_import_candidate_id,omitempty"`
	PaperImportSourceRefs   []PaperImportSourceRef    `json:"paper_import_source_refs,omitempty"`
	AnswerKey               *AnswerKey                `json:"answer_key,omitempty"`
	Solution                *QuestionSolution         `json:"solution,omitempty"`
	Rubric                  *Rubric                   `json:"rubric,omitempty"`
}

type QuestionSolution struct {
	ID                 string                 `json:"id"`
	QuestionID         string                 `json:"question_id"`
	PaperImportID      string                 `json:"paper_import_id,omitempty"`
	SolutionVersion    string                 `json:"solution_version"`
	RawText            string                 `json:"raw_text"`
	Steps              []SolutionStep         `json:"steps"`
	SourceRefs         []PaperImportSourceRef `json:"source_refs"`
	VerificationStatus string                 `json:"verification_status"`
}

type AnswerKey struct {
	ID                     string                 `json:"id"`
	QuestionID             string                 `json:"question_id"`
	AnswerVersion          string                 `json:"answer_version"`
	StandardAnswer         any                    `json:"standard_answer"`
	EquivalentAnswers      []any                  `json:"equivalent_answers"`
	Tolerance              any                    `json:"tolerance"`
	PaperImportID          string                 `json:"paper_import_id,omitempty"`
	PaperImportCandidateID string                 `json:"paper_import_candidate_id,omitempty"`
	PaperImportSourceRefs  []PaperImportSourceRef `json:"paper_import_source_refs,omitempty"`
}

type CreateQuestionInput struct {
	ExamPaperID      string          `json:"exam_paper_id"`
	ParentQuestionNo string          `json:"parent_question_no,omitempty"`
	SubquestionNo    string          `json:"subquestion_no,omitempty"`
	Options          []string        `json:"options"`
	QuestionNo       string          `json:"question_no"`
	QuestionType     string          `json:"question_type"`
	Score            float64         `json:"score"`
	Stem             string          `json:"stem"`
	KnowledgePoints  []string        `json:"knowledge_points"`
	AnswerArea       map[string]any  `json:"answer_area"`
	SortOrder        int             `json:"sort_order"`
	AnswerKey        *AnswerKeyInput `json:"answer_key"`
}

// UpdateQuestionInput 用指针区分未提交字段与明确提交的零值，局部更新不能覆盖未编辑内容。
type UpdateQuestionInput struct {
	ParentQuestionNo *string         `json:"parent_question_no"`
	SubquestionNo    *string         `json:"subquestion_no"`
	Options          *[]string       `json:"options"`
	QuestionNo       *string         `json:"question_no"`
	QuestionType     *string         `json:"question_type"`
	Score            *float64        `json:"score"`
	Stem             *string         `json:"stem"`
	KnowledgePoints  *[]string       `json:"knowledge_points"`
	AnswerArea       *map[string]any `json:"answer_area"`
	SortOrder        *int            `json:"sort_order"`
	AnswerKey        *AnswerKeyInput `json:"answer_key"`
}

type RubricPoint struct {
	ID                   string                `json:"id"`
	Description          string                `json:"description"`
	Score                float64               `json:"score"`
	Required             bool                  `json:"required"`
	EvidenceRequirements []EvidenceRequirement `json:"evidence_requirements,omitempty"`
}

type EvidenceRequirement struct {
	Type     string                `json:"type"`
	Target   string                `json:"target,omitempty"`
	Minimum  int                   `json:"minimum,omitempty"`
	Children []EvidenceRequirement `json:"children,omitempty"`
}

type Rubric struct {
	ID                     string                 `json:"id"`
	QuestionID             string                 `json:"question_id"`
	Version                string                 `json:"version"`
	Status                 string                 `json:"status"`
	MaxScore               float64                `json:"max_score"`
	Points                 []RubricPoint          `json:"points"`
	Deductions             []any                  `json:"deductions"`
	Examples               []any                  `json:"examples"`
	PaperImportID          string                 `json:"paper_import_id,omitempty"`
	PaperImportCandidateID string                 `json:"paper_import_candidate_id,omitempty"`
	PaperImportSourceRefs  []PaperImportSourceRef `json:"paper_import_source_refs,omitempty"`
}

type RubricInput struct {
	Status     string        `json:"status"`
	MaxScore   float64       `json:"max_score"`
	Points     []RubricPoint `json:"points"`
	Deductions []any         `json:"deductions"`
	Examples   []any         `json:"examples"`
}

type ValidationIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Issues []ValidationIssue `json:"issues"`
}

type PaperRepository interface {
	CreatePaper(ctx context.Context, tenantID string, examID string, userID string, input CreatePaperInput) (Paper, error)
	ListPapers(ctx context.Context, tenantID string, examID string) ([]Paper, error)
}

type AnswerSheetSourceDeletion struct {
	Deleted            bool     `json:"deleted"`
	PaperID            string   `json:"paper_id"`
	DeletedTemplateIDs []string `json:"deleted_template_ids"`
}

type AnswerSheetSourceRepository interface {
	DeleteAnswerSheetSource(context.Context, string, string, string) (AnswerSheetSourceDeletion, error)
}

type PaperImportRepository interface {
	CreatePaperImport(ctx context.Context, tenantID string, examID string, userID string, input CreatePaperImportInput) (PaperImportJob, error)
	AddPaperImportSources(ctx context.Context, tenantID string, id string, userID string, input AddPaperImportSourcesInput) (PaperImportJob, error)
	ReplacePaperImportSources(ctx context.Context, tenantID string, id string, userID string, input ReplacePaperImportSourcesInput) (PaperImportJob, error)
	// CompletePaperImportCandidates 供同步内存解析使用；PostgreSQL 发布仍须走带租约和代次校验的 v2 完成入口。
	CompletePaperImportCandidates(ctx context.Context, tenantID string, id string, parsed PaperImportParseResult) (PaperImportJob, error)
	SavePaperImportReview(ctx context.Context, tenantID string, id string, userID string, input ReviewPaperImportInput) (PaperImportJob, error)
	CompletePaperImport(ctx context.Context, tenantID string, id string, questions []PaperImportDraftQuestion, issues []string) (PaperImportJob, error)
	FailPaperImport(ctx context.Context, tenantID string, id string, errorCode string, issues []string) (PaperImportJob, error)
	CancelPaperImport(ctx context.Context, tenantID string, id string) (PaperImportJob, error)
	GetPaperImport(ctx context.Context, tenantID string, id string) (PaperImportJob, error)
	ListPaperImports(ctx context.Context, tenantID string, examID string) ([]PaperImportJob, error)
	ApplyPaperImport(ctx context.Context, tenantID string, id string, userID string) (PaperImportJob, error)
}

type QuestionRepository interface {
	CreateQuestion(ctx context.Context, tenantID string, examID string, userID string, input CreateQuestionInput) (Question, error)
	ListQuestions(ctx context.Context, tenantID string, examID string) ([]Question, error)
	UpdateQuestion(ctx context.Context, tenantID string, id string, userID string, input UpdateQuestionInput) (Question, error)
	DeleteQuestion(ctx context.Context, tenantID string, id string) error
}

type RubricRepository interface {
	CreateRubric(ctx context.Context, tenantID string, questionID string, userID string, input RubricInput) (Rubric, error)
}

type ConfigurationValidationRepository interface {
	ValidateConfig(ctx context.Context, tenantID string, examID string) (ValidationResult, error)
}

type TemplateRepository interface {
	ListTemplates(ctx context.Context, tenantID string, examID string) ([]AnswerSheetTemplate, error)
	CreateTemplate(ctx context.Context, tenantID string, examID string, userID string, input CreateTemplateInput) (AnswerSheetTemplate, error)
	UpdateTemplate(ctx context.Context, tenantID string, id string, input UpdateTemplateInput) (AnswerSheetTemplate, error)
	LockTemplate(ctx context.Context, tenantID string, id string, userID string) (AnswerSheetTemplate, error)
	CloneTemplate(ctx context.Context, tenantID string, id string, userID string) (AnswerSheetTemplate, error)
}

type TemplateBindingRepository interface {
	GetExamTemplateBinding(ctx context.Context, tenantID string, examID string) (ExamTemplateBinding, error)
	GetExamTemplateBindingState(ctx context.Context, tenantID string, examID string) (ExamTemplateBindingState, error)
	BindExamTemplate(ctx context.Context, tenantID string, examID string, userID string, input BindExamTemplateInput) (ExamTemplateBinding, error)
	UnbindExamTemplate(ctx context.Context, tenantID string, examID string, input UnbindExamTemplateInput) (ExamTemplateBinding, error)
}

// ConfirmReadinessInput 可绑定管理员已经核对的配置，空值保留旧客户端的确认行为。
type ConfirmReadinessInput struct {
	ExpectedConfigurationHash string `json:"expected_configuration_hash,omitempty"`
}

type ReadinessRepository interface {
	Readiness(ctx context.Context, tenantID string, examID string) (ReadinessResult, error)
	ConfirmReadiness(ctx context.Context, tenantID string, examID string, userID string, input ConfirmReadinessInput) (ReadinessResult, error)
	StartCollection(ctx context.Context, tenantID string, examID string, userID string) (ReadinessResult, error)
}

// Store 为仍覆盖完整试卷生命周期的调用方保留兼容聚合接口。
// 新调用方应依赖能覆盖自身工作的最小仓储能力。
type Store interface {
	PaperRepository
	PaperImportRepository
	QuestionRepository
	RubricRepository
	ConfigurationValidationRepository
	TemplateRepository
	TemplateBindingRepository
	ReadinessRepository
}
