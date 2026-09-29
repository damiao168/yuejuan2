// Package questionbank owns reusable content upstream of exam questions.
package questionbank

import (
	"context"
	"errors"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

var (
	ErrNotFound          = errors.New("question bank resource not found")
	ErrInvalidInput      = errors.New("invalid question bank input")
	ErrConflict          = errors.New("question bank revision conflict")
	ErrLocked            = errors.New("question bank content is locked")
	ErrUnavailable       = errors.New("question bank operation requires PostgreSQL")
	ErrSourceUnavailable = errors.New("frozen question import source is unavailable")
)

// MetadataValidationError keeps validation failures attached to stable field
// keys so API clients can render them beside the relevant controlled field.
type MetadataValidationError struct {
	FieldErrors map[string][]string
}

func (e *MetadataValidationError) Error() string { return "question bank metadata is invalid" }

type Bank struct {
	ID                    string    `json:"id"`
	TenantID              string    `json:"tenant_id"`
	SchoolID              string    `json:"school_id"`
	Name                  string    `json:"name"`
	Description           string    `json:"description"`
	Status                string    `json:"status"`
	Revision              int64     `json:"revision"`
	MetadataSchemaVersion int       `json:"metadata_schema_version"`
	CreatedBy             string    `json:"created_by"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type Item struct {
	Kind                      string    `json:"kind"`
	ID                        string    `json:"id"`
	TenantID                  string    `json:"tenant_id"`
	BankID                    string    `json:"bank_id"`
	ItemCode                  string    `json:"item_code"`
	SubjectCode               string    `json:"subject_code"`
	GradeScope                string    `json:"grade_scope"`
	CurrentPublishedVersionID *string   `json:"current_published_version_id"`
	Status                    string    `json:"status"`
	Revision                  int64     `json:"revision"`
	CreatedBy                 string    `json:"created_by"`
	CreatedAt                 time.Time `json:"created_at"`
}

type Metadata struct {
	SubjectCode          string `json:"subject_code"`
	EducationStage       string `json:"education_stage"`
	GradeScope           string `json:"grade_scope"`
	DifficultyBand       string `json:"difficulty_band"`
	CognitiveLevel       string `json:"cognitive_level"`
	Copyright            string `json:"copyright"`
	Language             string `json:"language"`
	SuggestedTimeMinutes *int   `json:"suggested_time_minutes,omitempty"`
	SourceYear           *int   `json:"source_year,omitempty"`
	IntendedUse          string `json:"intended_use,omitempty"`
}

type Content struct {
	QuestionType        string         `json:"question_type"`
	AssessmentArchetype string         `json:"assessment_archetype"`
	Stem                string         `json:"stem"`
	Options             []string       `json:"options"`
	DefaultScore        float64        `json:"default_score"`
	KnowledgePoints     []string       `json:"knowledge_points"`
	Metadata            Metadata       `json:"metadata"`
	CustomMetadata      map[string]any `json:"custom_metadata,omitempty"`
}

// Version 同时保留题目版本号、内容修订号和绑定的元数据规则版本，三者用途不同。
// ContentHash 只覆盖题目内容，BundleHash 还覆盖评分规则，审核须以整包为准。
type Version struct {
	ImportProvenance    *ImportProvenance `json:"import_provenance,omitempty"`
	BundleSchemaVersion int               `json:"bundle_schema_version"`
	Scoring             Scoring           `json:"scoring"`
	BundleHash          string            `json:"bundle_hash"`
	AnswerVersionID     *string           `json:"answer_version_id"`
	RubricVersionID     *string           `json:"rubric_version_id"`
	ID                  string            `json:"id"`
	TenantID            string            `json:"tenant_id"`
	ItemID              string            `json:"item_id"`
	VersionNo           int               `json:"version_no"`
	SchemaVersion       int               `json:"schema_version"`
	Revision            int64             `json:"revision"`
	WorkflowStatus      string            `json:"workflow_status"`
	SourceVersionID     *string           `json:"source_version_id"`
	AuthorID            string            `json:"author_id"`
	ContentHash         string            `json:"content_hash"`
	HashScope           string            `json:"hash_scope"`
	Content
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateBankInput struct {
	SchoolID    string `json:"school_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
type UpdateBankInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Status           string `json:"status"`
}
type CreateItemInput struct {
	Kind     string `json:"-"`
	ItemCode string `json:"item_code"`
	Content
}
type CreateVersionInput struct {
	SourceVersionID string `json:"source_version_id"`
}
type UpdateVersionInput struct {
	ExpectedRevision int64 `json:"expected_revision"`
	Content
}
type ItemResult struct {
	Item    Item    `json:"item"`
	Version Version `json:"version"`
}
type Filter struct {
	Kind   string
	Query  string
	Limit  int
	Offset int
}

type MetadataOption struct {
	Value  string `json:"value"`
	Label  string `json:"label"`
	Active bool   `json:"active"`
}
type TaxonomyTerm struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Active bool   `json:"active"`
}
type TaxonomyDefinition struct {
	ID    string         `json:"id"`
	Label string         `json:"label"`
	Terms []TaxonomyTerm `json:"terms"`
}
type MetadataFieldDefinition struct {
	Key        string           `json:"key"`
	Label      string           `json:"label"`
	Type       string           `json:"type"`
	Required   bool             `json:"required"`
	Min        *float64         `json:"min,omitempty"`
	Max        *float64         `json:"max,omitempty"`
	MaxLength  *int             `json:"max_length,omitempty"`
	Options    []MetadataOption `json:"options,omitempty"`
	TaxonomyID string           `json:"taxonomy_id,omitempty"`
}
type MetadataSchema struct {
	BankID     string                    `json:"bank_id"`
	Version    int                       `json:"version"`
	Fields     []MetadataFieldDefinition `json:"fields"`
	Taxonomies []TaxonomyDefinition      `json:"taxonomies"`
	CreatedBy  string                    `json:"created_by"`
	CreatedAt  time.Time                 `json:"created_at"`
}
type UpdateMetadataSchemaInput struct {
	ExpectedRevision int64                     `json:"expected_revision"`
	Fields           []MetadataFieldDefinition `json:"fields"`
	Taxonomies       []TaxonomyDefinition      `json:"taxonomies"`
}
type ValidateMetadataInput struct {
	SchemaVersion int            `json:"schema_version"`
	Values        map[string]any `json:"values"`
}
type MetadataValidationResult struct {
	Valid       bool                `json:"valid"`
	FieldErrors map[string][]string `json:"field_errors"`
}

type ACLBinding struct {
	UserID  string   `json:"user_id"`
	Preset  string   `json:"preset"`
	Actions []string `json:"actions"`
}
type ACLGroup struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Preset    string   `json:"preset"`
	Actions   []string `json:"actions"`
	MemberIDs []string `json:"member_user_ids"`
}
type ACLDocument struct {
	BankID   string       `json:"bank_id"`
	Revision int64        `json:"revision"`
	Bindings []ACLBinding `json:"bindings"`
	Groups   []ACLGroup   `json:"groups"`
}
type UpdateACLInput struct {
	ExpectedRevision int64        `json:"expected_revision"`
	Bindings         []ACLBinding `json:"bindings"`
	Groups           []ACLGroup   `json:"groups"`
}
type RetireItemInput struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

type SearchFilter struct {
	Query, BankID, SubjectCode, KnowledgePoint, QuestionType, Archetype    string
	WorkflowStatus, DifficultyBand, CognitiveLevel, Copyright, IntendedUse string
	UsePolicy, MetadataKey, MetadataValue, Mode, Sort                      string
	StatisticsAvailable                                                    *bool
	Limit, Offset                                                          int
}
type SearchItem struct {
	Item                Item    `json:"item"`
	Version             Version `json:"version"`
	StatisticsAvailable bool    `json:"statistics_available"`
}
type SearchPage struct {
	Items  []SearchItem `json:"items"`
	Total  int          `json:"total"`
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
}
type BankPage struct {
	Banks  []Bank `json:"banks"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}
type ItemPage struct {
	Items  []Item `json:"items"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}
type VersionPage struct {
	Versions []Version `json:"versions"`
	Total    int       `json:"total"`
	Limit    int       `json:"limit"`
	Offset   int       `json:"offset"`
}

type Store interface {
	PreviewImports(context.Context, auth.AccessScope, ImportPreviewInput) (ImportPreviewBatch, error)
	ImportQuestion(context.Context, auth.AccessScope, string, ImportQuestionInput) (ImportResult, error)
	UpdateScoring(context.Context, auth.AccessScope, string, UpdateScoringInput) (Version, error)
	Transition(context.Context, auth.AccessScope, string, string, ReviewInput) (Version, error)
	ListReviews(context.Context, auth.AccessScope, string) ([]Review, error)
	BindReviewers(context.Context, auth.AccessScope, string, ReviewerBinding) (Bank, error)
	Materialize(context.Context, auth.AccessScope, string, MaterializeInput) (MaterializeResult, error)
	ListBanks(context.Context, auth.AccessScope, Filter) (BankPage, error)
	CreateBank(context.Context, auth.AccessScope, CreateBankInput) (Bank, error)
	GetBank(context.Context, auth.AccessScope, string) (Bank, error)
	UpdateBank(context.Context, auth.AccessScope, string, UpdateBankInput) (Bank, error)
	ListItems(context.Context, auth.AccessScope, string, Filter) (ItemPage, error)
	CreateItem(context.Context, auth.AccessScope, string, CreateItemInput) (ItemResult, error)
	GetItem(context.Context, auth.AccessScope, string) (Item, error)
	ListVersions(context.Context, auth.AccessScope, string, Filter) (VersionPage, error)
	CreateVersion(context.Context, auth.AccessScope, string, CreateVersionInput) (Version, error)
	GetVersion(context.Context, auth.AccessScope, string) (Version, error)
	UpdateVersion(context.Context, auth.AccessScope, string, UpdateVersionInput) (Version, error)
	GetMetadataSchema(context.Context, auth.AccessScope, string, int) (MetadataSchema, error)
	UpdateMetadataSchema(context.Context, auth.AccessScope, string, UpdateMetadataSchemaInput) (MetadataSchema, error)
	ValidateMetadata(context.Context, auth.AccessScope, string, ValidateMetadataInput) (MetadataValidationResult, error)
	GetACL(context.Context, auth.AccessScope, string) (ACLDocument, error)
	UpdateACL(context.Context, auth.AccessScope, string, UpdateACLInput) (ACLDocument, error)
	SearchItems(context.Context, auth.AccessScope, SearchFilter) (SearchPage, error)
	RetireItem(context.Context, auth.AccessScope, string, RetireItemInput) (Item, error)
}

type ImportMapping struct {
	ItemCode        string         `json:"item_code,omitempty"`
	GradeScope      string         `json:"grade_scope,omitempty"`
	DifficultyBand  string         `json:"difficulty_band,omitempty"`
	CognitiveLevel  string         `json:"cognitive_level,omitempty"`
	Copyright       string         `json:"copyright,omitempty"`
	Language        string         `json:"language,omitempty"`
	IntendedUse     string         `json:"intended_use,omitempty"`
	Options         []string       `json:"options,omitempty"`
	KnowledgePoints []string       `json:"knowledge_points,omitempty"`
	CustomMetadata  map[string]any `json:"custom_metadata,omitempty"`
}

type ImportPreviewSelection struct {
	QuestionID       string        `json:"question_id"`
	TargetBankID     string        `json:"target_bank_id"`
	SourceSnapshotID string        `json:"source_snapshot_id"`
	Mapping          ImportMapping `json:"mapping"`
}

type ImportPreviewInput struct {
	Selections []ImportPreviewSelection `json:"selections"`
}

type ImportQuestionInput struct {
	ExpectedTargetSchemaVersion int           `json:"expected_target_schema_version"`
	TargetBankID                string        `json:"target_bank_id"`
	SourceSnapshotID            string        `json:"source_snapshot_id"`
	AssessmentSnapshotID        string        `json:"assessment_snapshot_id"`
	ScoringSource               string        `json:"scoring_source"`
	DedupDecision               string        `json:"dedup_decision"`
	ExistingItemID              string        `json:"existing_item_id,omitempty"`
	ExpectedTargetRevision      int64         `json:"expected_target_revision"`
	Mapping                     ImportMapping `json:"mapping"`
	CommandID                   string        `json:"command_id"`
}

type ImportIssue struct {
	Code     string `json:"code"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
}

type ImportDuplicateHint struct {
	Kind        string `json:"kind"`
	ItemID      string `json:"item_id"`
	ItemCode    string `json:"item_code"`
	VersionID   string `json:"version_id"`
	VersionNo   int    `json:"version_no"`
	StemSummary string `json:"stem_summary"`
	ContentHash string `json:"content_hash"`
	BundleHash  string `json:"bundle_hash"`
}

type ImportSource struct {
	ExamID                    string         `json:"exam_id"`
	QuestionID                string         `json:"question_id"`
	QuestionNo                string         `json:"question_no"`
	ReadinessSnapshotID       string         `json:"readiness_snapshot_id"`
	ConfigurationHash         string         `json:"configuration_hash"`
	SnapshotHash              string         `json:"snapshot_hash"`
	AssessmentSnapshotID      string         `json:"assessment_snapshot_id"`
	AssessmentSnapshotHash    string         `json:"assessment_snapshot_hash"`
	AssessmentSnapshotVersion int            `json:"assessment_snapshot_version"`
	ProfileSnapshot           map[string]any `json:"profile_snapshot"`
	ArchetypeSnapshot         map[string]any `json:"archetype_snapshot"`
	ScoringPolicySnapshot     map[string]any `json:"scoring_policy_snapshot"`
	SourceBankItemID          string         `json:"source_bank_item_id,omitempty"`
	SourceBankItemVersionID   string         `json:"source_bank_item_version_id,omitempty"`
	SourceContentHash         string         `json:"source_content_hash,omitempty"`
}

type ImportPreview struct {
	QuestionID        string                `json:"question_id"`
	Available         bool                  `json:"available"`
	Content           Content               `json:"content"`
	Scoring           Scoring               `json:"scoring"`
	ContentHash       string                `json:"content_hash"`
	BundleHash        string                `json:"bundle_hash"`
	Source            ImportSource          `json:"source"`
	Issues            []ImportIssue         `json:"issues"`
	DuplicateHints    []ImportDuplicateHint `json:"duplicate_hints"`
	SuggestedItemCode string                `json:"suggested_item_code"`
}

type ImportPreviewItem struct {
	QuestionID string         `json:"question_id"`
	Status     string         `json:"status"`
	Preview    *ImportPreview `json:"preview,omitempty"`
	ErrorCode  string         `json:"error_code,omitempty"`
	Retryable  bool           `json:"retryable"`
}

type ImportPreviewBatch struct {
	Items []ImportPreviewItem `json:"items"`
}

type ImportProvenance struct {
	ID                  string        `json:"id"`
	TargetBankID        string        `json:"target_bank_id"`
	TargetSchemaVersion int           `json:"target_schema_version"`
	Source              ImportSource  `json:"source"`
	ScoringSource       string        `json:"scoring_source"`
	DedupDecision       string        `json:"dedup_decision"`
	LinkedItemID        string        `json:"linked_item_id,omitempty"`
	Mapping             ImportMapping `json:"mapping"`
	CommandID           string        `json:"command_id"`
	CreatedAt           time.Time     `json:"created_at"`
}

type ImportResult struct {
	Item           Item                  `json:"item"`
	Version        Version               `json:"version"`
	DuplicateHints []ImportDuplicateHint `json:"duplicate_hints"`
	Issues         []ImportIssue         `json:"issues"`
	Provenance     ImportProvenance      `json:"provenance"`
}

type BatchImportItem struct {
	QuestionID string `json:"question_id"`
	ImportQuestionInput
}
type BatchImportInput struct {
	Items []BatchImportItem `json:"items"`
}
type BatchImportResultItem struct {
	QuestionID string        `json:"question_id"`
	CommandID  string        `json:"command_id"`
	Status     string        `json:"status"`
	Result     *ImportResult `json:"result,omitempty"`
	ErrorCode  string        `json:"error_code,omitempty"`
	Retryable  bool          `json:"retryable"`
}
type BatchImportResult struct {
	Items []BatchImportResultItem `json:"items"`
}

// Scoring is owned by an item version. Template selection copies the facts;
// the reference records provenance and is never dereferenced by exam scoring.
type Scoring struct {
	Answer            *paper.AnswerKeyInput `json:"answer"`
	Solution          *paper.SolutionInput  `json:"solution"`
	Rubric            *paper.RubricInput    `json:"rubric"`
	TemplateVersionID *string               `json:"template_version_id"`
	Assets            []Asset               `json:"assets"`
	UsePolicy         string                `json:"use_policy"`
}
type Asset struct {
	FileAssetID string `json:"file_asset_id"`
	SHA256      string `json:"sha256"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
}
type UpdateScoringInput struct {
	ExpectedRevision int64   `json:"expected_revision"`
	Scoring          Scoring `json:"scoring"`
}
type ReviewInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	BundleHash       string `json:"bundle_hash"`
	Comment          string `json:"comment"`
}
type Review struct {
	ID              string    `json:"id"`
	VersionID       string    `json:"version_id"`
	ReviewerID      string    `json:"reviewer_id"`
	Decision        string    `json:"decision"`
	Comment         string    `json:"comment"`
	ContentRevision int64     `json:"content_revision"`
	BundleHash      string    `json:"bundle_hash"`
	CreatedAt       time.Time `json:"created_at"`
}
type ReviewerBinding struct {
	ExpectedRevision int64  `json:"expected_revision"`
	UserID           string `json:"user_id"`
	Read             bool   `json:"read"`
	Review           bool   `json:"review"`
	Publish          bool   `json:"publish"`
}
type MaterializeSelection struct {
	VersionID  string `json:"version_id"`
	QuestionNo string `json:"question_no"`
	SortOrder  int    `json:"sort_order"`
}
type MaterializeInput struct {
	ExpectedRevision int64                  `json:"expected_revision"`
	Selections       []MaterializeSelection `json:"selections"`
}
type MaterializeResult struct {
	ExamID           string           `json:"exam_id"`
	Revision         int64            `json:"revision"`
	Questions        []paper.Question `json:"questions"`
	SourceVersionIDs []string         `json:"source_version_ids"`
}
