package paper

import (
	"time"
)

type LayoutRegion struct {
	ID            string         `json:"id"`
	QuestionID    string         `json:"question_id,omitempty"`
	Label         string         `json:"label,omitempty"`
	X             float64        `json:"x"`
	Y             float64        `json:"y"`
	Width         float64        `json:"width"`
	Height        float64        `json:"height"`
	OptionRegions []OptionRegion `json:"option_regions,omitempty"`
	// 新编辑器明确使用题区内的相对坐标；空值保留历史模板的坐标兼容规则。
	OptionCoordinateSpace string `json:"option_coordinate_space,omitempty"`
	// 共区批阅只改变显示范围；本题坐标仍用于切题和逐题评分，不能用外框覆盖子题位置。
	GradingGroupID       string              `json:"grading_group_id,omitempty"`
	GradingBBox          *TemplateRegionBBox `json:"grading_bbox,omitempty"`
	SuggestionConfidence float64             `json:"suggestion_confidence,omitempty"`
	SuggestionSource     string              `json:"suggestion_source,omitempty"`
	// 来源记录最初的发现方式；人工改框只更新审核状态，不能抹掉原始模型来源。
	Origin        string   `json:"origin,omitempty"`
	ReviewState   string   `json:"review_state,omitempty"`
	SemanticCheck string   `json:"semantic_check,omitempty"`
	ReasonCodes   []string `json:"reason_codes,omitempty"`
	AnalysisRunID string   `json:"analysis_run_id,omitempty"`
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
	PageNo            int                    `json:"page_no"`
	Width             int                    `json:"width"`
	Height            int                    `json:"height"`
	RegistrationMarks []LayoutRegion         `json:"registration_marks"`
	IdentityRegions   []LayoutRegion         `json:"identity_regions"`
	QuestionRegions   []LayoutRegion         `json:"question_regions"`
	GradingGroups     []TemplateGradingGroup `json:"grading_groups,omitempty"`
}

type TemplateLayout struct {
	Pages      []TemplatePage     `json:"pages"`
	OMRProfile TemplateOMRProfile `json:"omr_profile,omitempty"`
	// 编辑草稿与正式切题布局分开，未关联候选和丢弃记录不能被 worker 当成答题区域。
	EditorState *TemplateEditorState `json:"editor_state,omitempty"`
}

type TemplateEditorState struct {
	Version      int                       `json:"version"`
	SourceSHA256 string                    `json:"source_sha256"`
	Candidates   []TemplateEditorCandidate `json:"candidates"`
}

type TemplateEditorCandidate struct {
	ID         string       `json:"id"`
	PageNo     int          `json:"page_no"`
	Role       string       `json:"role"`
	Region     LayoutRegion `json:"region"`
	QuestionNo string       `json:"question_no,omitempty"`
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

// ExamTemplateBinding 记录某次考试可用于页面路由的不可变答题卡模板版本。
// 它不复用 AnswerSheetTemplate.Status 的含义：模板锁定与考试路由是独立的生命周期决策。
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

// ExamTemplateBindingState 将绑定和修订游标作为同一时刻的状态返回。
// 初始游标为 0；解绑后 Binding 为空，但 Revision 继续递增而不归零。
type ExamTemplateBindingState struct {
	Binding  *ExamTemplateBinding `json:"binding"`
	Revision int                  `json:"revision"`
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

type ReadinessAdvisoryQuestion struct {
	QuestionID   string  `json:"question_id"`
	QuestionNo   string  `json:"question_no"`
	QuestionType string  `json:"question_type"`
	Score        float64 `json:"score"`
}

type ReadinessAdvisory struct {
	Code      string                      `json:"code"`
	Label     string                      `json:"label"`
	Severity  string                      `json:"severity"`
	Message   string                      `json:"message"`
	Section   string                      `json:"section"`
	Score     float64                     `json:"score"`
	Questions []ReadinessAdvisoryQuestion `json:"questions"`
}

type ReadinessResult struct {
	// 仅保存本次读取到的参考绑定，确认事务据此加锁复核，不从新版本替换已核对输入。
	referenceQuestions []Question
	// 保留本次摘要使用的归属；确认重建后直接比较，不能重新读取学籍来替换已核对的预期值。
	candidateClasses []readinessCandidateClass
	// 确认转入 ready 后核对触发器实际冻结事实，覆盖模板读取到冻结之间的变化。
	assessmentFactsHash     string
	Ready                   bool                      `json:"ready"`
	Confirmed               bool                      `json:"confirmed"`
	ConfigurationHash       string                    `json:"configuration_hash"`
	SnapshotID              string                    `json:"snapshot_id,omitempty"`
	ImportSnapshotAvailable bool                      `json:"import_snapshot_available"`
	Checks                  []ReadinessCheck          `json:"checks"`
	Advisories              []ReadinessAdvisory       `json:"advisories"`
	ScoringPreview          []ReadinessScoringPreview `json:"scoring_preview"`
	ConfirmedAt             *time.Time                `json:"confirmed_at,omitempty"`
	ConfirmedBy             string                    `json:"confirmed_by,omitempty"`
	// 仅采集及后续阶段返回此前通过的确认事实，不代表当前配置匹配或允许重新导入。
	PreparationConfirmedAt *time.Time `json:"preparation_confirmed_at,omitempty"`
	PreparationSnapshotID  string     `json:"preparation_snapshot_id,omitempty"`
}
