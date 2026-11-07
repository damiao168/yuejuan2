package paper

import (
	"context"
	"time"
)

// PaperImportDraftQuestion 是待核对材料；完整性通过和人工确认分别检查，候选不会直接成为正式题目。
type PaperImportDraftQuestion struct {
	CandidateID            string                 `json:"candidate_id,omitempty"`
	ParentQuestionNo       string                 `json:"parent_question_no,omitempty"`
	SubquestionNo          string                 `json:"subquestion_no,omitempty"`
	Options                []string               `json:"options"`
	ScoreSource            string                 `json:"score_source,omitempty"`
	ScoreResolution        string                 `json:"score_resolution,omitempty"`
	QuestionTypeResolution string                 `json:"question_type_resolution,omitempty"`
	AnswerCandidateID      string                 `json:"answer_candidate_id,omitempty"`
	SolutionCandidateID    string                 `json:"solution_candidate_id,omitempty"`
	RubricCandidateID      string                 `json:"rubric_candidate_id,omitempty"`
	SourceRefs             []PaperImportSourceRef `json:"source_refs"`
	QuestionNo             string                 `json:"question_no"`
	QuestionType           string                 `json:"question_type"`
	AssessmentArchetype    string                 `json:"assessment_archetype,omitempty"`
	Score                  float64                `json:"score"`
	Stem                   string                 `json:"stem"`
	KnowledgePoints        []string               `json:"knowledge_points"`
	AnswerKey              *AnswerKeyInput        `json:"answer_key,omitempty"`
	Solution               *SolutionInput         `json:"solution,omitempty"`
	Rubric                 *RubricInput           `json:"rubric,omitempty"`
	Confidence             float64                `json:"confidence"`
	Issues                 []string               `json:"issues"`
	MatchedQuestionID      string                 `json:"matched_question_id,omitempty"`
	MatchStatus            string                 `json:"match_status,omitempty"`
	CompletenessStatus     string                 `json:"completeness_status,omitempty"`
	HumanConfirmedFields   []string               `json:"human_confirmed_fields,omitempty"`
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

// PaperImportRuntimeProgress 是当前导入阶段最新的实际 worker 心跳。
// Completed/total 始终描述指定单位；total 缺失或为零表示该阶段进度有意保持不确定。
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
	ProviderKey       string `json:"provider_key,omitempty"`
	ModelName         string `json:"model_name,omitempty"`
	RequestCount      int64  `json:"request_count,omitempty"`
	InputTokens       int64  `json:"input_tokens,omitempty"`
	CachedInputTokens int64  `json:"cached_input_tokens,omitempty"`
	OutputTokens      int64  `json:"output_tokens,omitempty"`
	ReasoningTokens   int64  `json:"reasoning_tokens,omitempty"`
	TotalTokens       int64  `json:"total_tokens,omitempty"`
}

type PaperImportJob struct {
	dispatchLeaseOwner           string
	ID                           string                      `json:"id"`
	TenantID                     string                      `json:"tenant_id"`
	ExamID                       string                      `json:"exam_id"`
	ExamPaperID                  string                      `json:"exam_paper_id"`
	PaperFileAssetID             string                      `json:"paper_file_asset_id"`
	AnswerFileAssetID            string                      `json:"answer_file_asset_id"`
	Status                       string                      `json:"status"`
	Generation                   int64                       `json:"generation"`
	StructureConfirmedGeneration int64                       `json:"structure_confirmed_generation,omitempty"`
	RunID                        string                      `json:"run_id"`
	SourceRevision               string                      `json:"source_revision"`
	ResultGeneration             int64                       `json:"result_generation,omitempty"`
	Subject                      string                      `json:"subject"`
	AuthoritativeSubjectCode     string                      `json:"authoritative_subject_code,omitempty"`
	RecognitionPolicyVersion     string                      `json:"recognition_policy_version,omitempty"`
	RecognitionPolicyHash        string                      `json:"recognition_policy_hash,omitempty"`
	FormulaStatus                string                      `json:"formula_status,omitempty"`
	FormulaRegionCount           int                         `json:"formula_region_count,omitempty"`
	FormulaReviewCount           int                         `json:"formula_review_count,omitempty"`
	RuntimeProgress              *PaperImportRuntimeProgress `json:"runtime_progress,omitempty"`
	ModelUsage                   PaperImportModelUsage       `json:"model_usage,omitempty"`
	Sources                      []PaperImportSource         `json:"sources"`
	QuestionCandidates           []QuestionCandidate         `json:"question_candidates"`
	AnswerCandidates             []AnswerCandidate           `json:"answer_candidates"`
	SolutionCandidates           []SolutionCandidate         `json:"solution_candidates"`
	RubricCandidates             []RubricCandidate           `json:"rubric_candidates"`
	StructuredIssues             []PaperImportIssue          `json:"structured_issues"`
	Questions                    []PaperImportDraftQuestion  `json:"questions"`
	Issues                       []string                    `json:"issues"`
	ErrorCode                    string                      `json:"error_code,omitempty"`
	CreatedBy                    string                      `json:"created_by"`
	CreatedAt                    time.Time                   `json:"created_at"`
	UpdatedAt                    time.Time                   `json:"updated_at"`
	AppliedAt                    *time.Time                  `json:"applied_at,omitempty"`
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
	ExpectedGeneration int64 `json:"expected_generation"`
	// 同代次可以反复核对；整卷写入还须匹配读取时的内容时间，防止覆盖其他人的保存。
	ExpectedUpdatedAt string                     `json:"expected_updated_at"`
	Questions         []PaperImportDraftQuestion `json:"questions"`
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

// PaperImportRunBinding 绑定一次识别的代次、来源版本和不可变解析输入，提交时需逐项核对。
type PaperImportRunBinding struct {
	ImportID       string
	RunID          string
	Generation     int64
	SourceRevision string
	CommandType    string
	NewSourceIDs   []string
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
