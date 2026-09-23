package files

import (
	"context"
	"errors"
	"io"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

var (
	ErrNotFound       = errors.New("file asset not found")
	ErrDuplicateFile  = errors.New("duplicate file upload")
	ErrInvalidFile    = errors.New("invalid file")
	ErrStorageFailure = errors.New("object storage failure")
	ErrForbidden      = errors.New("file access forbidden")
	ErrConflict       = errors.New("file revision conflict")
	ErrObjectNotFound = errors.New("object not found")
	ErrBucketMissing  = errors.New("object storage bucket is missing")
)

const (
	LifecyclePendingUpload   = "pending_upload"
	LifecycleActive          = "active"
	LifecycleUploadFailed    = "upload_failed"
	LifecyclePendingDelete   = "pending_delete"
	LifecycleDeleteFailed    = "delete_failed"
	LifecycleDeleted         = "deleted"
	LifecycleQuarantined     = "quarantined"
	LifecycleMissingObject   = "missing_object"
	LifecycleOrphanRecovered = "orphan_recovered"
)

type FileAsset struct {
	ID             string
	TenantID       string
	SchoolID       string
	ExamID         string
	SubmissionID   string
	OwnerType      string
	OwnerID        string
	OriginalName   string
	ContentType    string
	SizeBytes      int64
	HashSHA256     string
	StorageBucket  string
	StorageKey     string
	Visibility     string
	UploadedBy     string
	StudentID      string
	Lifecycle      string
	Revision       int64
	LastError      string
	DeleteAttempts int
	LastErrorAt    *time.Time
	RetentionUntil *time.Time
	LegalHold      bool
	VerifiedAt     *time.Time
	CreatedAt      time.Time
	DeletedAt      *time.Time
}

type FileResponse struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	SchoolID       string     `json:"school_id,omitempty"`
	ExamID         string     `json:"exam_id,omitempty"`
	SubmissionID   string     `json:"submission_id,omitempty"`
	OwnerType      string     `json:"owner_type"`
	OwnerID        string     `json:"owner_id,omitempty"`
	OriginalName   string     `json:"original_name"`
	ContentType    string     `json:"content_type"`
	SizeBytes      int64      `json:"size_bytes"`
	HashSHA256     string     `json:"hash_sha256"`
	Visibility     string     `json:"visibility"`
	UploadedBy     string     `json:"uploaded_by"`
	Lifecycle      string     `json:"lifecycle_status"`
	Revision       int64      `json:"revision"`
	DeleteAttempts int        `json:"delete_attempts"`
	RetentionUntil *time.Time `json:"retention_until,omitempty"`
	LegalHold      bool       `json:"legal_hold"`
	VerifiedAt     *time.Time `json:"verified_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (a FileAsset) Response() FileResponse {
	return FileResponse{
		ID:             a.ID,
		TenantID:       a.TenantID,
		SchoolID:       a.SchoolID,
		ExamID:         a.ExamID,
		SubmissionID:   a.SubmissionID,
		OwnerType:      a.OwnerType,
		OwnerID:        a.OwnerID,
		OriginalName:   a.OriginalName,
		ContentType:    a.ContentType,
		SizeBytes:      a.SizeBytes,
		HashSHA256:     a.HashSHA256,
		Visibility:     a.Visibility,
		UploadedBy:     a.UploadedBy,
		Lifecycle:      a.Lifecycle,
		Revision:       a.Revision,
		DeleteAttempts: a.DeleteAttempts,
		RetentionUntil: a.RetentionUntil,
		LegalHold:      a.LegalHold,
		VerifiedAt:     a.VerifiedAt,
		CreatedAt:      a.CreatedAt,
	}
}

type CreateAssetInput struct {
	TenantID      string
	SchoolID      string
	ExamID        string
	SubmissionID  string
	OwnerType     string
	OwnerID       string
	OriginalName  string
	ContentType   string
	SizeBytes     int64
	HashSHA256    string
	StorageBucket string
	StorageKey    string
	Visibility    string
	UploadedBy    string
}

type Store interface {
	Create(ctx context.Context, input CreateAssetInput) (FileAsset, error)
	FindDuplicate(ctx context.Context, tenantID string, ownerType string, ownerID string, hashSHA256 string) (FileAsset, bool, error)
	Get(ctx context.Context, tenantID string, id string) (FileAsset, error)
	Delete(ctx context.Context, tenantID string, id string) (FileAsset, error)
}

// LifecycleStore is implemented by production stores. The base Store remains
// available to trusted internal fixtures that create already-persisted assets.
type LifecycleStore interface {
	Store
	ValidateCreateScope(ctx context.Context, scope auth.AccessScope, input CreateAssetInput) error
	CreatePending(ctx context.Context, input CreateAssetInput) (FileAsset, error)
	Activate(ctx context.Context, tenantID string, id string, expectedRevision int64) (FileAsset, error)
	MarkUploadFailed(ctx context.Context, tenantID string, id string, expectedRevision int64, detail string) (FileAsset, error)
	GetScoped(ctx context.Context, scope auth.AccessScope, id string) (FileAsset, error)
	BeginDelete(ctx context.Context, scope auth.AccessScope, id string, expectedRevision int64) (FileAsset, error)
	CompleteDelete(ctx context.Context, tenantID string, id string, expectedRevision int64) (FileAsset, error)
	MarkDeleteFailed(ctx context.Context, tenantID string, id string, expectedRevision int64, detail string) (FileAsset, error)
}

// MissingObjectRecovery restores an asset quarantined by reconciliation after
// its object has been re-uploaded and verified at the original storage key.
type MissingObjectRecovery interface {
	RecoverMissing(context.Context, string, string, int64) (FileAsset, error)
}

func AllowsAsset(scope auth.AccessScope, asset FileAsset) bool {
	if scope.TenantID == "" || asset.TenantID != scope.TenantID {
		return false
	}
	if scope.TenantWide || scope.AllowsFile(asset.ID) {
		return true
	}
	if asset.SchoolID != "" && scope.AllowsSchool(asset.SchoolID) {
		return true
	}
	if asset.ExamID != "" && scope.AllowsExam(asset.ExamID) {
		return true
	}
	if asset.SubmissionID != "" && scope.AllowsSubmission(asset.SubmissionID) {
		return true
	}
	if asset.StudentID != "" && scope.AllowsStudent(asset.StudentID) {
		return true
	}
	return asset.UploadedBy != "" && asset.UploadedBy == scope.ActorID
}

func AllowsCreate(scope auth.AccessScope, input CreateAssetInput) bool {
	if scope.TenantID == "" || input.TenantID != scope.TenantID {
		return false
	}
	if scope.TenantWide {
		return true
	}
	if input.SchoolID != "" && scope.AllowsSchool(input.SchoolID) {
		return true
	}
	if input.ExamID != "" && scope.AllowsExam(input.ExamID) {
		return true
	}
	if input.SubmissionID != "" && scope.AllowsSubmission(input.SubmissionID) {
		return true
	}
	return input.SchoolID == "" && input.ExamID == "" && input.SubmissionID == "" && input.UploadedBy == scope.ActorID
}

type ObjectStorage interface {
	Put(ctx context.Context, bucket string, key string, body io.Reader, size int64, contentType string) error
	Get(ctx context.Context, bucket string, key string) (io.ReadCloser, error)
	Remove(ctx context.Context, bucket string, key string) error
}

type ObjectInfo struct {
	Bucket       string
	Key          string
	SizeBytes    int64
	LastModified time.Time
}

type ReconciliationObjectStorage interface {
	ObjectStorage
	Stat(context.Context, string, string) (ObjectInfo, error)
	List(context.Context, string, string, int) ([]ObjectInfo, error)
}
