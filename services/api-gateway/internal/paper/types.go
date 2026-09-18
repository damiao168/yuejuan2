package paper

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("paper resource not found")
	ErrInvalidInput      = errors.New("invalid paper input")
	ErrRubricMismatch    = errors.New("rubric score does not equal question score")
	ErrRubricLocked      = errors.New("locked rubric cannot be modified")
	ErrTemplateLocked    = errors.New("locked template cannot be modified")
	ErrTemplateNotLocked = errors.New("template must be locked before binding")
	ErrConflict          = errors.New("configuration revision conflict")
	ErrNotReady          = errors.New("exam configuration is not ready")
	ErrExamFrozen        = errors.New("exam paper configuration is frozen")
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
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id"`
	ExamID      string         `json:"exam_id"`
	FileAssetID string         `json:"file_asset_id"`
	VersionNo   int            `json:"version_no"`
	Status      string         `json:"status"`
	File        FileAssetInput `json:"file"`
}

type CreatePaperInput struct {
	FileAssetID string         `json:"file_asset_id"`
	File        FileAssetInput `json:"file"`
}

type PaperImportDraftQuestion struct {
	CandidateID          string                 `json:"candidate_id,omitempty"`
	AnswerCandidateID    string                 `json:"answer_candidate_id,omitempty"`
	SolutionCandidateID  string                 `json:"solution_candidate_id,omitempty"`
	RubricCandidateID    string                 `json:"rubric_candidate_id,omitempty"`
	SourceRefs           []PaperImportSourceRef `json:"source_refs"`
	QuestionNo           string                 `json:"question_no"`
	QuestionType         string                 `json:"question_type"`
	AssessmentArchetype  string                 `json:"assessment_archetype,omitempty"`
	Score                float64                `json:"score"`
	Stem                 string                 `json:"stem"`
	KnowledgePoints      []string               `json:"knowledge_points"`
	AnswerKey            *AnswerKeyInput        `json:"answer_key,omitempty"`
	Solution             *SolutionInput         `json:"solution,omitempty"`
	Rubric               *RubricInput           `json:"rubric,omitempty"`
	Confidence           float64                `json:"confidence"`
	Issues               []string               `json:"issues"`
	MatchedQuestionID    string                 `json:"matched_question_id,omitempty"`
	MatchStatus          string                 `json:"match_status,omitempty"`
	CompletenessStatus   string                 `json:"completeness_status,omitempty"`
	HumanConfirmedFields []string               `json:"human_confirmed_fields,omitempty"`
}

type PaperImportSourceRef struct {
	SourceID      string   `json:"source_id"`
	FileAssetID   string   `json:"file_asset_id"`
	DocumentIndex int      `json:"document_index"`
	PageNo        int      `json:"page_no,omitempty"`
	BlockID       string   `json:"block_id,omitempty"`
	BBox          any      `json:"bbox,omitempty"`
	TextStart     *int     `json:"text_start,omitempty"`
	TextEnd       *int     `json:"text_end,omitempty"`
	OCRConfidence *float64 `json:"ocr_confidence,omitempty"`
}

type PaperImportSource struct {
	ID               string    `json:"id"`
	FileAssetID      string    `json:"file_asset_id"`
	DocumentIndex    int       `json:"document_index"`
	RoleHint         string    `json:"role_hint"`
	DetectedRole     string    `json:"detected_role"`
	RoleConfidence   float64   `json:"role_confidence"`
	ProcessingStatus string    `json:"processing_status"`
	OriginalName     string    `json:"original_name,omitempty"`
	ContentType      string    `json:"content_type,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type PaperImportDetectedDocument struct {
	SourceID       string  `json:"source_id"`
	DetectedRole   string  `json:"detected_role"`
	RoleConfidence float64 `json:"role_confidence"`
}

type QuestionCandidate struct {
	CandidateID          string                 `json:"candidate_id"`
	QuestionNoRaw        string                 `json:"question_no_raw,omitempty"`
	QuestionNoNormalized string                 `json:"question_no_normalized,omitempty"`
	ParentQuestionNo     string                 `json:"parent_question_no,omitempty"`
	SubquestionNo        string                 `json:"subquestion_no,omitempty"`
	SectionHint          string                 `json:"section_hint,omitempty"`
	Stem                 string                 `json:"stem,omitempty"`
	Options              []string               `json:"options"`
	QuestionType         string                 `json:"question_type,omitempty"`
	Score                *float64               `json:"score,omitempty"`
	KnowledgePointHints  []string               `json:"knowledge_point_hints"`
	Confidence           float64                `json:"confidence"`
	SourceRefs           []PaperImportSourceRef `json:"source_refs"`
	Issues               []string               `json:"issues"`
}

type AnswerCandidate struct {
	CandidateID          string                 `json:"candidate_id"`
	QuestionNoHint       string                 `json:"question_no_hint,omitempty"`
	QuestionNoNormalized string                 `json:"question_no_normalized,omitempty"`
	SubquestionNoHint    string                 `json:"subquestion_no_hint,omitempty"`
	StandardAnswer       any                    `json:"standard_answer,omitempty"`
	EquivalentAnswers    []any                  `json:"equivalent_answers"`
	Tolerance            any                    `json:"tolerance,omitempty"`
	Confidence           float64                `json:"confidence"`
	SourceRefs           []PaperImportSourceRef `json:"source_refs"`
	Issues               []string               `json:"issues"`
}

type SolutionStep struct {
	StepNo  int    `json:"step_no"`
	Content string `json:"content"`
}
type SolutionCandidate struct {
	CandidateID          string                 `json:"candidate_id"`
	QuestionNoHint       string                 `json:"question_no_hint,omitempty"`
	QuestionNoNormalized string                 `json:"question_no_normalized,omitempty"`
	SubquestionNoHint    string                 `json:"subquestion_no_hint,omitempty"`
	RawText              string                 `json:"raw_text"`
	Steps                []SolutionStep         `json:"steps"`
	Confidence           float64                `json:"confidence"`
	SourceRefs           []PaperImportSourceRef `json:"source_refs"`
	Issues               []string               `json:"issues"`
}

type RubricCandidatePoint struct {
	ID                   string                `json:"id"`
	Description          string                `json:"description"`
	Score                *float64              `json:"score,omitempty"`
	Required             *bool                 `json:"required,omitempty"`
	EvidenceRequirements []EvidenceRequirement `json:"evidence_requirements,omitempty"`
}

type RubricCandidate struct {
	CandidateID          string                 `json:"candidate_id"`
	QuestionNoHint       string                 `json:"question_no_hint,omitempty"`
	QuestionNoNormalized string                 `json:"question_no_normalized,omitempty"`
	MaxScore             *float64               `json:"max_score,omitempty"`
	Points               []RubricCandidatePoint `json:"points"`
	Deductions           []any                  `json:"deductions"`
	Examples             []any                  `json:"examples"`
	Confidence           float64                `json:"confidence"`
	SourceRefs           []PaperImportSourceRef `json:"source_refs"`
	Issues               []string               `json:"issues"`
}

type SolutionInput struct {
	RawText    string                 `json:"raw_text"`
	Steps      []SolutionStep         `json:"steps"`
	SourceRefs []PaperImportSourceRef `json:"source_refs"`
}

type PaperImportIssue struct {
	Code           string                 `json:"code"`
	Severity       string                 `json:"severity"`
	Certainty      string                 `json:"certainty"`
	QuestionNo     string                 `json:"question_no,omitempty"`
	Section        string                 `json:"section,omitempty"`
	Message        string                 `json:"message"`
	Confidence     *float64               `json:"confidence,omitempty"`
	SourceRefs     []PaperImportSourceRef `json:"source_refs"`
	ResolutionHint string                 `json:"resolution_hint,omitempty"`
}

// PaperImportRuntimeProgress is the latest factual worker heartbeat for the
// active import stage. Completed/total always describe the named unit; an
// absent or zero total means the stage is intentionally indeterminate.
type PaperImportRuntimeProgress struct {
	TaskType          string     `json:"task_type"`
	TaskStatus        string     `json:"task_status"`
	Stage             string     `json:"stage,omitempty"`
	Phase             string     `json:"phase,omitempty"`
	Completed         int        `json:"completed,omitempty"`
	Total             int        `json:"total,omitempty"`
	Unit              string     `json:"unit,omitempty"`
	PageNo            int        `json:"page_no,omitempty"`
	PageTotal         int        `json:"page_total,omitempty"`
	BatchNo           int        `json:"batch_no,omitempty"`
	BatchTotal        int        `json:"batch_total,omitempty"`
	BatchSize         int        `json:"batch_size,omitempty"`
	EventSeq          int        `json:"event_seq,omitempty"`
	ColdStart         bool       `json:"cold_start,omitempty"`
	Model             string     `json:"model,omitempty"`
	RuntimeMode       string     `json:"runtime_mode,omitempty"`
	RuntimePlanSource string     `json:"runtime_plan_source,omitempty"`
	RuntimeBatchSize  int        `json:"runtime_batch_size,omitempty"`
	ParseRoute        string     `json:"parse_route,omitempty"`
	Message           string     `json:"message,omitempty"`
	ProgressChangedAt *time.Time `json:"progress_changed_at,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type PaperImportModelUsage struct {
	InputTokens       int64 `json:"input_tokens,omitempty"`
	CachedInputTokens int64 `json:"cached_input_tokens,omitempty"`
	OutputTokens      int64 `json:"output_tokens,omitempty"`
	ReasoningTokens   int64 `json:"reasoning_tokens,omitempty"`
	TotalTokens       int64 `json:"total_tokens,omitempty"`
}

type PaperImportJob struct {
	dispatchLeaseOwner       string
	ID                       string                      `json:"id"`
	TenantID                 string                      `json:"tenant_id"`
	ExamID                   string                      `json:"exam_id"`
	ExamPaperID              string                      `json:"exam_paper_id"`
	PaperFileAssetID         string                      `json:"paper_file_asset_id"`
	AnswerFileAssetID        string                      `json:"answer_file_asset_id"`
	Status                   string                      `json:"status"`
	Generation               int64                       `json:"generation"`
	RunID                    string                      `json:"run_id"`
	SourceRevision           string                      `json:"source_revision"`
	ResultGeneration         int64                       `json:"result_generation,omitempty"`
	Subject                  string                      `json:"subject"`
	AuthoritativeSubjectCode string                      `json:"authoritative_subject_code,omitempty"`
	RecognitionPolicyVersion string                      `json:"recognition_policy_version,omitempty"`
	RecognitionPolicyHash    string                      `json:"recognition_policy_hash,omitempty"`
	FormulaStatus            string                      `json:"formula_status,omitempty"`
	FormulaRegionCount       int                         `json:"formula_region_count,omitempty"`
	FormulaReviewCount       int                         `json:"formula_review_count,omitempty"`
	RuntimeProgress          *PaperImportRuntimeProgress `json:"runtime_progress,omitempty"`
	ModelUsage               PaperImportModelUsage       `json:"model_usage,omitempty"`
	Sources                  []PaperImportSource         `json:"sources"`
	QuestionCandidates       []QuestionCandidate         `json:"question_candidates"`
	AnswerCandidates         []AnswerCandidate           `json:"answer_candidates"`
	SolutionCandidates       []SolutionCandidate         `json:"solution_candidates"`
	RubricCandidates         []RubricCandidate           `json:"rubric_candidates"`
	StructuredIssues         []PaperImportIssue          `json:"structured_issues"`
	Questions                []PaperImportDraftQuestion  `json:"questions"`
	Issues                   []string                    `json:"issues"`
	ErrorCode                string                      `json:"error_code,omitempty"`
	CreatedBy                string                      `json:"created_by"`
	CreatedAt                time.Time                   `json:"created_at"`
	UpdatedAt                time.Time                   `json:"updated_at"`
	AppliedAt                *time.Time                  `json:"applied_at,omitempty"`
}

type CreatePaperImportInput struct {
	ExamPaperID       string                         `json:"exam_paper_id"`
	PaperFileAssetID  string                         `json:"paper_file_asset_id"`
	AnswerFileAssetID string                         `json:"answer_file_asset_id"`
	Subject           string                         `json:"subject"`
	Sources           []CreatePaperImportSourceInput `json:"sources"`
	CommandID         string                         `json:"command_id,omitempty"`
}

type CreatePaperImportSourceInput struct {
	FileAssetID   string `json:"file_asset_id"`
	DocumentIndex int    `json:"document_index"`
	RoleHint      string `json:"role_hint"`
}

type AddPaperImportSourcesInput struct {
	Sources            []CreatePaperImportSourceInput `json:"sources"`
	CommandID          string                         `json:"command_id,omitempty"`
	ExpectedGeneration int64                          `json:"expected_generation,omitempty"`
}

type ReplacePaperImportSourcesInput struct {
	Sources            []ReplacePaperImportSourceInput `json:"sources"`
	CommandID          string                          `json:"command_id,omitempty"`
	ExpectedGeneration int64                           `json:"expected_generation,omitempty"`
}

type ReplacePaperImportSourceInput struct {
	ID            string `json:"id"`
	DocumentIndex int    `json:"document_index"`
	RoleHint      string `json:"role_hint"`
}

type ReviewPaperImportInput struct {
	ExpectedGeneration int64                      `json:"expected_generation"`
	Questions          []PaperImportDraftQuestion `json:"questions"`
}

type PaperImportOCRAsset struct {
	Role          string `json:"role,omitempty"`
	SourceID      string `json:"source_id"`
	DocumentIndex int    `json:"document_index"`
	RoleHint      string `json:"role_hint"`
	FileAssetID   string `json:"file_asset_id"`
	ContentType   string `json:"content_type"`
}

type PaperImportDecodedPage struct {
	Role          string `json:"role,omitempty"`
	SourceID      string `json:"source_id"`
	DocumentIndex int    `json:"document_index"`
	PageNo        int    `json:"page_no"`
	FileAssetID   string `json:"file_asset_id"`
	SHA256        string `json:"sha256"`
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
}

type PaperImportDecodeResult struct {
	TaskID     string                   `json:"task_id"`
	LeaseToken string                   `json:"lease_token"`
	DurationMS int                      `json:"duration_ms"`
	Pages      []PaperImportDecodedPage `json:"pages"`
}

type PaperImportOCRBlock struct {
	Role           string                      `json:"role,omitempty"`
	SourceID       string                      `json:"source_id"`
	DocumentIndex  int                         `json:"document_index"`
	BlockID        string                      `json:"block_id,omitempty"`
	PageNo         int                         `json:"page_no"`
	Text           string                      `json:"text"`
	BBox           any                         `json:"bbox"`
	Confidence     float64                     `json:"confidence"`
	Kind           string                      `json:"kind,omitempty"`
	RawLatex       string                      `json:"raw_latex,omitempty"`
	CanonicalLatex string                      `json:"canonical_latex,omitempty"`
	Engine         string                      `json:"engine,omitempty"`
	EngineVersion  string                      `json:"engine_version,omitempty"`
	ReviewStatus   string                      `json:"review_status,omitempty"`
	RawText        string                      `json:"raw_text,omitempty"`
	Segments       []PaperImportContentSegment `json:"segments,omitempty"`
}

type PaperImportContentSegment struct {
	Kind          string    `json:"kind"`
	Text          string    `json:"text,omitempty"`
	Latex         string    `json:"latex,omitempty"`
	BBox          []float64 `json:"bbox,omitempty"`
	SourceBlockID string    `json:"source_block_id,omitempty"`
}

type PaperImportOCRResult struct {
	TaskID     string                `json:"task_id"`
	LeaseToken string                `json:"lease_token"`
	DurationMS int                   `json:"duration_ms"`
	Blocks     []PaperImportOCRBlock `json:"blocks"`
}

type PaperImportFormulaCandidate struct {
	ModelVersion     string   `json:"model_version"`
	RawLatex         string   `json:"raw_latex"`
	CanonicalLatex   string   `json:"canonical_latex"`
	Confidence       float64  `json:"confidence"`
	Valid            bool     `json:"valid"`
	SyntaxValid      bool     `json:"syntax_valid"`
	StructureValid   bool     `json:"structure_valid"`
	RenderValid      *bool    `json:"render_valid,omitempty"`
	RenderSimilarity *float64 `json:"render_similarity,omitempty"`
	ValidationAction string   `json:"validation_action"`
	ReasonCodes      []string `json:"reason_codes,omitempty"`
}

type PaperImportFormulaRegion struct {
	SourceID           string                        `json:"source_id"`
	DocumentIndex      int                           `json:"document_index"`
	PageNo             int                           `json:"page_no"`
	RegionID           string                        `json:"region_id"`
	BBox               []float64                     `json:"bbox"`
	DetectorModel      string                        `json:"detector_model"`
	DetectorConfidence float64                       `json:"detector_confidence"`
	CropSHA256         string                        `json:"crop_sha256"`
	EdgeInkRatio       float64                       `json:"edge_ink_ratio"`
	CropComplete       bool                          `json:"crop_complete"`
	RecropCount        int                           `json:"recrop_count"`
	ValidationVersion  string                        `json:"validation_version"`
	Candidates         []PaperImportFormulaCandidate `json:"candidates"`
	SelectedLatex      string                        `json:"selected_latex"`
	SelectedModel      string                        `json:"selected_model"`
	Status             string                        `json:"status"`
	ReasonCodes        []string                      `json:"reason_codes,omitempty"`
}

type PaperImportFormulaResult struct {
	TaskID     string                     `json:"task_id"`
	LeaseToken string                     `json:"lease_token"`
	DurationMS int                        `json:"duration_ms"`
	Regions    []PaperImportFormulaRegion `json:"regions"`
}

type PaperImportParseDocument struct {
	SourceID      string                `json:"source_id"`
	FileAssetID   string                `json:"file_asset_id"`
	DocumentIndex int                   `json:"document_index"`
	RoleHint      string                `json:"role_hint"`
	Content       string                `json:"content"`
	Blocks        []PaperImportOCRBlock `json:"blocks"`
}

type PaperImportParseRequest struct {
	Documents   []PaperImportParseDocument `json:"documents"`
	ExtraIssues []PaperImportIssue         `json:"extra_issues"`
	Pages       []PaperImportDecodedPage   `json:"pages,omitempty"`
}

type PaperImportRunBinding struct {
	ImportID       string
	RunID          string
	Generation     int64
	SourceRevision string
	InputID        string
	InputHash      string
	Input          PaperImportParseRequest
}

type PaperImportPendingDispatch struct {
	Job   PaperImportJob
	Owner string
}

type PaperImportRuntimeFailure struct {
	TaskID      string         `json:"task_id"`
	LeaseToken  string         `json:"lease_token"`
	Retryable   bool           `json:"retryable"`
	ErrorCode   string         `json:"error_code"`
	ErrorDetail map[string]any `json:"error_detail"`
	DurationMS  int            `json:"duration_ms"`
}

type PaperImportRuntime interface {
	QueuePaperImportOCR(context.Context, string, PaperImportJob, string, []PaperImportOCRAsset) error
	QueuePaperImportParse(context.Context, string, PaperImportJob, string, PaperImportParseRequest) error
	CompletePaperImportDecode(context.Context, string, string, PaperImportDecodeResult) error
	CompletePaperImportOCR(context.Context, string, string, PaperImportOCRResult, PaperImportParseRequest) error
	CompletePaperImportFormula(context.Context, string, string, PaperImportFormulaResult) error
	FailPaperImportRuntime(context.Context, string, string, PaperImportRuntimeFailure) error
}

type AnswerKeyInput struct {
	StandardAnswer    any   `json:"standard_answer"`
	EquivalentAnswers []any `json:"equivalent_answers"`
	Tolerance         any   `json:"tolerance"`
}

type Question struct {
	SourceType              string                 `json:"source_type,omitempty"`
	SourceBankItemID        string                 `json:"source_bank_item_id,omitempty"`
	SourceBankItemVersionID string                 `json:"source_bank_item_version_id,omitempty"`
	SourceContentHash       string                 `json:"source_content_hash,omitempty"`
	BankContent             map[string]any         `json:"bank_content,omitempty"`
	ID                      string                 `json:"id"`
	TenantID                string                 `json:"tenant_id"`
	ExamID                  string                 `json:"exam_id"`
	ExamPaperID             string                 `json:"exam_paper_id,omitempty"`
	QuestionNo              string                 `json:"question_no"`
	QuestionType            string                 `json:"question_type"`
	Score                   float64                `json:"score"`
	Stem                    string                 `json:"stem,omitempty"`
	KnowledgePoints         []string               `json:"knowledge_points"`
	AnswerArea              map[string]any         `json:"answer_area,omitempty"`
	SortOrder               int                    `json:"sort_order"`
	Status                  string                 `json:"status"`
	AssessmentArchetype     string                 `json:"assessment_archetype,omitempty"`
	PaperImportID           string                 `json:"paper_import_id,omitempty"`
	PaperImportCandidateID  string                 `json:"paper_import_candidate_id,omitempty"`
	PaperImportSourceRefs   []PaperImportSourceRef `json:"paper_import_source_refs,omitempty"`
	AnswerKey               *AnswerKey             `json:"answer_key,omitempty"`
	Solution                *QuestionSolution      `json:"solution,omitempty"`
	Rubric                  *Rubric                `json:"rubric,omitempty"`
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
	ExamPaperID     string          `json:"exam_paper_id"`
	QuestionNo      string          `json:"question_no"`
	QuestionType    string          `json:"question_type"`
	Score           float64         `json:"score"`
	Stem            string          `json:"stem"`
	KnowledgePoints []string        `json:"knowledge_points"`
	AnswerArea      map[string]any  `json:"answer_area"`
	SortOrder       int             `json:"sort_order"`
	AnswerKey       *AnswerKeyInput `json:"answer_key"`
}

type UpdateQuestionInput struct {
	QuestionNo      *string         `json:"question_no"`
	QuestionType    *string         `json:"question_type"`
	Score           *float64        `json:"score"`
	Stem            *string         `json:"stem"`
	KnowledgePoints *[]string       `json:"knowledge_points"`
	AnswerArea      *map[string]any `json:"answer_area"`
	SortOrder       *int            `json:"sort_order"`
	AnswerKey       *AnswerKeyInput `json:"answer_key"`
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

type LayoutRegion struct {
	ID                   string         `json:"id"`
	QuestionID           string         `json:"question_id,omitempty"`
	Label                string         `json:"label,omitempty"`
	X                    float64        `json:"x"`
	Y                    float64        `json:"y"`
	Width                float64        `json:"width"`
	Height               float64        `json:"height"`
	OptionRegions        []OptionRegion `json:"option_regions,omitempty"`
	SuggestionConfidence float64        `json:"suggestion_confidence,omitempty"`
	SuggestionSource     string         `json:"suggestion_source,omitempty"`
}

type OptionRegion struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type TemplatePage struct {
	PageNo            int            `json:"page_no"`
	Width             int            `json:"width"`
	Height            int            `json:"height"`
	RegistrationMarks []LayoutRegion `json:"registration_marks"`
	IdentityRegions   []LayoutRegion `json:"identity_regions"`
	QuestionRegions   []LayoutRegion `json:"question_regions"`
}

type TemplateLayout struct {
	Pages      []TemplatePage     `json:"pages"`
	OMRProfile TemplateOMRProfile `json:"omr_profile,omitempty"`
}

type AnswerSheetTemplate struct {
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id"`
	ExamID      string         `json:"exam_id"`
	ExamPaperID string         `json:"exam_paper_id"`
	VersionNo   int            `json:"version_no"`
	Revision    int            `json:"revision"`
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	PageCount   int            `json:"page_count"`
	Layout      TemplateLayout `json:"layout"`
	ContentHash string         `json:"content_hash"`
	CreatedBy   string         `json:"created_by"`
	LockedBy    string         `json:"locked_by,omitempty"`
	LockedAt    *time.Time     `json:"locked_at,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// ExamTemplateBinding records which immutable answer-sheet template version
// may be used to route pages for one exam. It deliberately does not overload
// AnswerSheetTemplate.Status: template locking and exam routing are separate
// lifecycle decisions.
type ExamTemplateBinding struct {
	ID                  string    `json:"id"`
	TenantID            string    `json:"tenant_id"`
	ExamID              string    `json:"exam_id"`
	TemplateID          string    `json:"template_id"`
	TemplateContentHash string    `json:"template_content_hash"`
	Mode                string    `json:"mode"`
	Source              string    `json:"source"`
	Revision            int       `json:"revision"`
	BoundBy             string    `json:"bound_by"`
	BoundAt             time.Time `json:"bound_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type BindExamTemplateInput struct {
	TemplateID       string `json:"template_id"`
	Mode             string `json:"mode"`
	ExpectedRevision int    `json:"expected_revision"`
}

type UnbindExamTemplateInput struct {
	ExpectedRevision int    `json:"expected_revision"`
	Reason           string `json:"reason"`
}

type CreateTemplateInput struct {
	ExamPaperID string         `json:"exam_paper_id"`
	Name        string         `json:"name"`
	PageCount   int            `json:"page_count"`
	Layout      TemplateLayout `json:"layout"`
}

type UpdateTemplateInput struct {
	Name             string         `json:"name"`
	PageCount        int            `json:"page_count"`
	Layout           TemplateLayout `json:"layout"`
	ExpectedRevision int            `json:"expected_revision"`
}

type ReadinessCheck struct {
	Code     string `json:"code"`
	Label    string `json:"label"`
	Passed   bool   `json:"passed"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Section  string `json:"section"`
}

type ReadinessResult struct {
	Ready                   bool             `json:"ready"`
	Confirmed               bool             `json:"confirmed"`
	ConfigurationHash       string           `json:"configuration_hash"`
	SnapshotID              string           `json:"snapshot_id,omitempty"`
	ImportSnapshotAvailable bool             `json:"import_snapshot_available"`
	Checks                  []ReadinessCheck `json:"checks"`
	ConfirmedAt             *time.Time       `json:"confirmed_at,omitempty"`
	ConfirmedBy             string           `json:"confirmed_by,omitempty"`
}

type PaperRepository interface {
	CreatePaper(ctx context.Context, tenantID string, examID string, userID string, input CreatePaperInput) (Paper, error)
	ListPapers(ctx context.Context, tenantID string, examID string) ([]Paper, error)
}

type PaperImportRepository interface {
	CreatePaperImport(ctx context.Context, tenantID string, examID string, userID string, input CreatePaperImportInput) (PaperImportJob, error)
	AddPaperImportSources(ctx context.Context, tenantID string, id string, userID string, input AddPaperImportSourcesInput) (PaperImportJob, error)
	ReplacePaperImportSources(ctx context.Context, tenantID string, id string, userID string, input ReplacePaperImportSourcesInput) (PaperImportJob, error)
	CompletePaperImportCandidates(ctx context.Context, tenantID string, id string, detected []PaperImportDetectedDocument, questions []QuestionCandidate, answers []AnswerCandidate, solutions []SolutionCandidate, rubrics []RubricCandidate, issues []PaperImportIssue) (PaperImportJob, error)
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
	BindExamTemplate(ctx context.Context, tenantID string, examID string, userID string, input BindExamTemplateInput) (ExamTemplateBinding, error)
	UnbindExamTemplate(ctx context.Context, tenantID string, examID string, input UnbindExamTemplateInput) (ExamTemplateBinding, error)
}

type ReadinessRepository interface {
	Readiness(ctx context.Context, tenantID string, examID string) (ReadinessResult, error)
	ConfirmReadiness(ctx context.Context, tenantID string, examID string, userID string) (ReadinessResult, error)
	StartCollection(ctx context.Context, tenantID string, examID string, userID string) (ReadinessResult, error)
}

// Store remains the compatibility aggregate for callers that still span the
// complete paper lifecycle. New consumers should depend on the smallest
// repository capability that covers their work.
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
