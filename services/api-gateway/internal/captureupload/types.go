// Package captureupload implements the resumable handoff from an offline
// scanning workstation to a capture batch.  It deliberately keeps the
// temporary upload separate from capture_file: a page-processing task is only
// visible after the complete hash has been verified and the file has been
// registered once.
package captureupload

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound        = errors.New("capture upload not found")
	ErrInvalidInput    = errors.New("invalid capture upload input")
	ErrConflict        = errors.New("capture upload conflict")
	ErrIncomplete      = errors.New("capture upload is incomplete")
	ErrHashMismatch    = errors.New("capture upload hash mismatch")
	ErrUnsupportedType = errors.New("capture upload content type is unsupported")
	ErrStorage         = errors.New("capture upload storage failure")
)

const DefaultChunkSize int64 = 4 * 1024 * 1024

const completionTimeout = 2 * time.Minute
const completionLeaseDuration = 3 * time.Minute

type InitInput struct {
	SHA256         string `json:"sha256"`
	Size           int64  `json:"size"`
	MIME           string `json:"mime"`
	ExamID         string `json:"exam"`
	BatchID        string `json:"batch"`
	IdempotencyKey string `json:"idempotency_key"`
	Filename       string `json:"filename,omitempty"`
}

type ChunkInput struct {
	Offset int64
	SHA256 string
	Data   []byte
}

// CompleteInput accepts the expected hash again so a client can detect a
// stale local queue record before it asks the server to materialize a file.
// It is optional because InitInput is the authoritative immutable contract.
type CompleteInput struct {
	SHA256 string `json:"sha256,omitempty"`
}

type Session struct {
	CompletionToken      string     `json:"-"`
	CompletionLeaseUntil time.Time  `json:"-"`
	ID                   string     `json:"remote_upload_id"`
	TenantID             string     `json:"-"`
	ExamID               string     `json:"exam"`
	BatchID              string     `json:"batch"`
	IdempotencyKey       string     `json:"-"`
	OriginalName         string     `json:"-"`
	ContentType          string     `json:"mime"`
	SHA256               string     `json:"sha256"`
	Size                 int64      `json:"size"`
	ChunkSize            int64      `json:"chunk_size"`
	ConfirmedOffset      int64      `json:"confirmed_offset"`
	Status               string     `json:"status"`
	FileAssetID          string     `json:"file_asset_id,omitempty"`
	CaptureFileID        string     `json:"capture_file_id,omitempty"`
	ErrorCode            string     `json:"error_code,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
}

type InitResponse struct {
	RemoteUploadID  string `json:"remote_upload_id"`
	ChunkSize       int64  `json:"chunk_size"`
	ConfirmedOffset int64  `json:"confirmed_offset"`
	AlreadyExists   bool   `json:"already_exists"`
	Status          string `json:"status"`
	FileAssetID     string `json:"file_asset_id,omitempty"`
	CaptureFileID   string `json:"capture_file_id,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
}

func initResponse(session Session, existing bool) InitResponse {
	return InitResponse{
		RemoteUploadID:  session.ID,
		ChunkSize:       session.ChunkSize,
		ConfirmedOffset: session.ConfirmedOffset,
		AlreadyExists:   existing || session.Status == "completed",
		Status:          session.Status,
		FileAssetID:     session.FileAssetID,
		CaptureFileID:   session.CaptureFileID,
		ErrorCode:       session.ErrorCode,
	}
}

// Store persists both the resumable transfer state and its unconfirmed
// chunks.  Chunks are removed once the immutable FileAsset is materialized;
// the verified file hash and audit event remain the durable evidence.
type Store interface {
	Init(context.Context, string, string, InitInput, int64) (Session, bool, error)
	Get(context.Context, string, string) (Session, error)
	AppendChunk(context.Context, string, string, ChunkInput) (Session, error)
	BeginComplete(context.Context, string, string) (Session, bool, error)
	ReadChunks(context.Context, string, string, func([]byte) error) error
	Complete(context.Context, string, string, string, string, string) (Session, error)
	Resume(context.Context, string, string, string, string) error
	Fail(context.Context, string, string, string, string) error
}
