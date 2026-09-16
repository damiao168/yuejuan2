// Package backmark owns the safe re-marking queue produced by a quality
// incident.  It intentionally stores candidate grades separately from the
// current grade facts: completing a backmark item can never publish or replace
// a student's score.
package backmark

import (
	"context"
	"errors"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

var (
	ErrNotFound          = errors.New("backmark resource not found")
	ErrInvalidInput      = errors.New("invalid backmark input")
	ErrStateConflict     = errors.New("backmark state conflict")
	ErrRevisionConflict  = errors.New("backmark revision conflict")
	ErrOriginalGrader    = errors.New("backmark item cannot be assigned to original grader")
	ErrAssigneeForbidden = errors.New("backmark item is not assigned to grader")
	ErrNoAffectedTasks   = errors.New("backmark selector matched no completed review tasks")
	ErrNoRegradeItems    = errors.New("backmark batch has no items that require regrade")
	ErrTooManyItems      = errors.New("backmark batch exceeds synchronous item limit")
	ErrPreviewStale      = errors.New("backmark preview no longer matches selection or source facts")
)

const (
	DefaultPageSize     = 50
	MaxPageSize         = 200
	MaxSynchronousItems = 1000
	MaxSelectorTaskIDs  = 1000
)

const (
	BatchOpen                 = "open"
	BatchInProgress           = "in_progress"
	BatchReadyForConfirmation = "ready_for_confirmation"
	BatchCompleted            = "completed"
	BatchCancelled            = "cancelled"

	ItemPending             = "pending"
	ItemInProgress          = "in_progress"
	ItemDiffReady           = "diff_ready"
	ItemArbitrationRequired = "arbitration_required"
	ItemRegradeRequired     = "regrade_required"
	ItemCancelled           = "cancelled"

	DispositionConfirm   = "confirm"
	DispositionArbitrate = "arbitrate"
	DispositionRegrade   = "regrade"
)

// TimeRange is inclusive. Empty endpoints mean unbounded.
type TimeRange struct {
	From *time.Time `json:"from,omitempty"`
	To   *time.Time `json:"to,omitempty"`
}

type ScoreBand struct {
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

// Selector operates over the immutable original human-grade/task facts. The
// four supported fields are deliberately explicit rather than a free-form SQL
// filter, so a quality incident has a reproducible affected population.
type Selector struct {
	TimeRange *TimeRange `json:"time_range,omitempty"`
	TaskIDs   []string   `json:"task_ids,omitempty"`
	GraderID  string     `json:"grader_id,omitempty"`
	ScoreBand *ScoreBand `json:"score_band,omitempty"`
}

// Policy selects the next *workflow* only. Even a regrade_required result does
// not modify the released/current score; A19/A18 own that transition.
type Policy struct {
	Disposition      string  `json:"disposition"`
	ArbitrationDelta float64 `json:"arbitration_delta,omitempty"`
}

type Batch struct {
	ID               string    `json:"id"`
	ExamID           string    `json:"exam_id"`
	QuestionID       string    `json:"question_id"`
	SourceIncidentID string    `json:"source_incident_id"`
	Selector         Selector  `json:"selector"`
	Policy           Policy    `json:"policy"`
	AffectedCount    int       `json:"affected_count"`
	Status           string    `json:"status"`
	CreatedBy        string    `json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type Item struct {
	ID               string    `json:"id"`
	BatchID          string    `json:"batch_id"`
	ReviewTaskID     string    `json:"review_task_id"`
	OriginalGradeID  string    `json:"original_grade_id"`
	ReassignedTaskID string    `json:"reassigned_task_id,omitempty"`
	NewGradeID       string    `json:"new_grade_id,omitempty"`
	OriginalReviewer string    `json:"original_reviewer_id"`
	ReassignedTo     string    `json:"reassigned_to"`
	OriginalScore    float64   `json:"original_score"`
	MaxScore         float64   `json:"max_score"`
	NewScore         *float64  `json:"new_score,omitempty"`
	Diff             *float64  `json:"diff,omitempty"`
	Status           string    `json:"status"`
	Revision         int64     `json:"revision"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// GraderItem is the deliberately blind queue shape. It contains enough data
// to claim and submit an independently judged score, but not the old score,
// original grader, or original grade identifier that could anchor a re-mark.
type GraderItem struct {
	ID string `json:"id"`
	// ReviewTaskID is retained only inside the server-side context bridge. A
	// client must not be able to traverse into the original review workflow.
	ReviewTaskID string    `json:"-"`
	Status       string    `json:"status"`
	MaxScore     float64   `json:"max_score"`
	Revision     int64     `json:"revision"`
	CreatedAt    time.Time `json:"created_at"`
}

// GraderContext is the deliberately blind work surface for a back-marking
// item.  The source review task is only an internal lookup key: neither its
// score, reviewer, submission identity nor identifiers are serialised here.
// A back-marker receives the frozen rubric and the answer evidence needed to
// make an independent judgement, then submits a separate backmark_grade.
type GraderContext struct {
	Item             GraderItem     `json:"item"`
	ExpectedRevision int64          `json:"expected_revision"`
	Question         GraderQuestion `json:"question"`
	FrozenRubric     paper.Rubric   `json:"frozen_rubric"`
	Answer           GraderAnswer   `json:"answer"`
}

type GraderQuestion struct {
	ID              string   `json:"id"`
	QuestionNo      string   `json:"question_no"`
	QuestionType    string   `json:"question_type"`
	Score           float64  `json:"score"`
	Stem            string   `json:"stem,omitempty"`
	KnowledgePoints []string `json:"knowledge_points,omitempty"`
}

type GraderAnswer struct {
	RawAnswer       string `json:"raw_answer,omitempty"`
	OCRText         string `json:"ocr_text,omitempty"`
	SegmentStatus   string `json:"segment_status"`
	SegmentImageURL string `json:"segment_image_url"`
}

type Grade struct {
	ID               string            `json:"id"`
	BackmarkItemID   string            `json:"backmark_item_id"`
	ReviewerID       string            `json:"reviewer_id"`
	Score            float64           `json:"score"`
	MaxScore         float64           `json:"max_score"`
	RubricSelections []RubricSelection `json:"rubric_selections"`
	Comments         string            `json:"comments,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
}

type RubricSelection struct {
	PointID string  `json:"point_id"`
	Score   float64 `json:"score"`
}

type SourceTask struct {
	ReviewTaskID     string
	OriginalGradeID  string
	OriginalReviewer string
	OriginalScore    float64
	MaxScore         float64
	GradedAt         time.Time
}

type Preview struct {
	SelectorHash  string    `json:"selector_hash,omitempty"`
	AffectedCount int       `json:"affected_count"`
	ScoreBands    []Band    `json:"score_bands"`
	TimeRange     TimeRange `json:"time_range"`
}

type Band struct {
	Score float64 `json:"score"`
	Count int     `json:"count"`
}

type Histogram struct {
	Delta float64 `json:"delta"`
	Count int     `json:"count"`
}

type Summary struct {
	StatusCounts
	Batch     Batch       `json:"batch"`
	Items     []Item      `json:"items"`
	Histogram []Histogram `json:"diff_histogram"`
}

type StatusCounts struct {
	RegradeRequiredCount int `json:"regrade_required_count"`
	PendingCount         int `json:"pending_count"`
	CompletedCount       int `json:"completed_count"`
}

type PageOptions struct {
	Status          string
	Limit           int
	CursorCreatedAt time.Time
	CursorID        string
}

type CreateInput struct {
	// Optional for existing API clients; the quality dashboard always submits
	// the hash returned by Preview to bind creation to its confirmed snapshot.
	SelectorHash     string   `json:"selector_hash,omitempty"`
	SourceIncidentID string   `json:"source_incident_id"`
	Selector         Selector `json:"selector"`
	Policy           Policy   `json:"policy"`
	ReassignedTo     string   `json:"reassigned_to"`
}

type SubmitInput struct {
	Score            float64           `json:"score"`
	RubricSelections []RubricSelection `json:"rubric_selections"`
	Comments         string            `json:"comments,omitempty"`
	ExpectedRevision int64             `json:"expected_revision"`
}

type Store interface {
	SelectSourceTasks(context.Context, string, string, string, Selector, int) ([]SourceTask, error)
	CreateBatch(context.Context, string, string, string, string, CreateInput, []SourceTask) (Batch, []Item, error)
	Preview(context.Context, string, string, string, Selector) (Preview, error)
	GetBatch(context.Context, string, string) (Batch, error)
	ListBatchItems(context.Context, string, string, PageOptions) ([]Item, error)
	GetHistogram(context.Context, string, string) ([]Histogram, error)
	GetStatusCounts(context.Context, string, string) (StatusCounts, error)
	ListBatches(context.Context, string, string, string, PageOptions) ([]Batch, error)
	ListAssigned(context.Context, string, string, PageOptions) ([]Item, error)
	GetAssigned(context.Context, string, string, string) (Item, error)
	Claim(context.Context, string, string, string) (Item, error)
	Submit(context.Context, string, string, string, SubmitInput) (Item, Grade, error)
}
