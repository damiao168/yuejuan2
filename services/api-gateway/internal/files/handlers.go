package files

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresourcehttp"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Handler struct {
	store          Store
	objects        ObjectStorage
	audit          auth.Store
	cfg            config.FileConfig
	reconciliation ReconciliationReader
	downloads      *DownloadService
}

func (h *Handler) WithReconciliationReader(reader ReconciliationReader) *Handler {
	h.reconciliation = reader
	return h
}

func (h *Handler) ReconciliationStatus(w http.ResponseWriter, r *http.Request) {
	if h.reconciliation == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "file_reconciliation_not_configured", "file reconciliation is not configured")
		return
	}
	run, findings, err := h.reconciliation.LatestReconciliation(r.Context())
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "file_reconciliation_load_failed", "failed to load file reconciliation status")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"run": run, "findings": findings})
}

func NewHandler(store Store, objects ObjectStorage, audit auth.Store, cfg config.FileConfig) *Handler {
	cfg = normalizeFileConfig(cfg)
	return &Handler{store: store, objects: objects, audit: audit, cfg: cfg, downloads: NewDownloadService(store, objects)}
}

func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxUploadBytes+1024*1024)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.Error(w, r, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		httpx.Error(w, r, http.StatusBadRequest, "invalid_multipart", "multipart form is invalid or too large")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "file_missing", "multipart field file is required")
		return
	}
	defer file.Close()

	if header.Size <= 0 || header.Size > h.cfg.MaxUploadBytes {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_file_size", fmt.Sprintf("file size must be between 1 and %d bytes", h.cfg.MaxUploadBytes))
		return
	}

	originalName, err := CleanFilename(header.Filename)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_filename", "file name is invalid")
		return
	}

	sample := make([]byte, 512)
	n, readErr := file.Read(sample)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "file_read_failed", "failed to read uploaded file")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "file_seek_failed", "failed to inspect uploaded file")
		return
	}
	contentType, err := ValidateFileType(originalName, header.Header.Get("Content-Type"), SniffFileContentType(originalName, sample[:n]), h.cfg.AllowedExtensions)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "unsupported_file_type", "file type is not allowed")
		return
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "file_hash_failed", "failed to hash uploaded file")
		return
	}
	hashSHA256 := hex.EncodeToString(hash.Sum(nil))
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "file_seek_failed", "failed to prepare uploaded file")
		return
	}

	ownerType := strings.TrimSpace(r.FormValue("owner_type"))
	if ownerType == "" {
		ownerType = "generic"
	}
	if !ValidateOwnerType(ownerType) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_owner_type", "owner_type is not supported")
		return
	}
	ownerID := strings.TrimSpace(r.FormValue("owner_id"))
	if ownerID != "" && !IsUUIDLike(ownerID) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_owner_id", "owner_id must be a UUID")
		return
	}
	schoolID := strings.TrimSpace(r.FormValue("school_id"))
	examID := strings.TrimSpace(r.FormValue("exam_id"))
	submissionID := strings.TrimSpace(r.FormValue("submission_id"))
	for field, value := range map[string]string{"school_id": schoolID, "exam_id": examID, "submission_id": submissionID} {
		if value != "" && !IsUUIDLike(value) {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_"+field, field+" must be a UUID")
			return
		}
	}
	if !ValidateOwnerReferences(ownerType, ownerID, examID, submissionID) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_owner_reference", "file owner metadata is inconsistent")
		return
	}
	storageKey, err := BuildStorageKey(user.TenantID, hashSHA256, originalName)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "storage_key_failed", "failed to create storage key")
		return
	}
	input := CreateAssetInput{
		TenantID: user.TenantID, SchoolID: schoolID, ExamID: examID, SubmissionID: submissionID,
		OwnerType: ownerType, OwnerID: ownerID, OriginalName: originalName, ContentType: contentType,
		SizeBytes: header.Size, HashSHA256: hashSHA256, StorageBucket: h.cfg.Bucket, StorageKey: storageKey,
		Visibility: "private", UploadedBy: user.ID,
	}
	lifecycle, hasLifecycle := h.store.(LifecycleStore)
	var accessScope auth.AccessScope
	if hasLifecycle {
		scope, ok := auth.AccessScopeFromContext(r.Context())
		if !ok {
			httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
			return
		}
		if err := lifecycle.ValidateCreateScope(r.Context(), scope, input); err != nil {
			writeStoreError(w, r, err)
			return
		}
		accessScope = scope
	}

	existing, duplicate, err := h.store.FindDuplicate(r.Context(), user.TenantID, ownerType, ownerID, hashSHA256)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if duplicate && hasLifecycle {
		// Duplicate lookup is tenant-wide. Authorize the persisted asset itself
		// before returning it or writing to its storage key; request metadata is
		// not proof that an existing object belongs to the caller's data scope.
		existing, err = lifecycle.GetScoped(r.Context(), accessScope, existing.ID)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
	}
	if duplicate && (!hasLifecycle || existing.Lifecycle == LifecycleActive) {
		// Exam-material imports are retryable workflows. Re-uploading identical
		// material should reuse the active asset so a failed import can attach a
		// fresh source and run again without duplicating object storage.
		if ownerType == "import" {
			httpx.JSON(w, http.StatusOK, map[string]any{
				"file":      existing.Response(),
				"duplicate": true,
			})
			return
		}
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"request_id": logger.RequestID(r.Context()),
			"error": map[string]string{
				"code":    "duplicate_file",
				"message": "same file already exists for this owner",
			},
			"existing_file": existing.Response(),
		})
		return
	}
	if duplicate && hasLifecycle && existing.Lifecycle == LifecycleMissingObject {
		if _, ok := h.store.(MissingObjectRecovery); !ok {
			httpx.Error(w, r, http.StatusServiceUnavailable, "missing_object_recovery_unavailable", "missing object recovery is unavailable")
			return
		}
	}
	var asset FileAsset
	if hasLifecycle {
		if duplicate && (existing.Lifecycle == LifecyclePendingUpload || existing.Lifecycle == LifecycleUploadFailed || existing.Lifecycle == LifecycleMissingObject) {
			asset = existing
		} else {
			asset, err = lifecycle.CreatePending(r.Context(), input)
			if err != nil {
				writeStoreError(w, r, err)
				return
			}
		}
		// A retry must write to the path recorded by the pending asset. BuildStorageKey
		// contains a random suffix, so the key generated for this request is different.
		storageKey = asset.StorageKey
		input.StorageKey = asset.StorageKey
		input.StorageBucket = asset.StorageBucket
	}
	if err := h.objects.Put(r.Context(), input.StorageBucket, storageKey, file, header.Size, contentType); err != nil {
		if hasLifecycle && asset.Lifecycle == LifecyclePendingUpload {
			_, _ = lifecycle.MarkUploadFailed(r.Context(), user.TenantID, asset.ID, asset.Revision, "object_put_failed")
		}
		httpx.Error(w, r, http.StatusBadGateway, "object_storage_failed", "failed to write object storage")
		return
	}
	// 对象写入成功仍须回读验证大小和哈希，验证通过后才能把元数据标记为 active。
	if err := h.verifyStoredObject(r, input.StorageBucket, storageKey, header.Size, hashSHA256); err != nil {
		if hasLifecycle && asset.Lifecycle == LifecyclePendingUpload {
			_, _ = lifecycle.MarkUploadFailed(r.Context(), user.TenantID, asset.ID, asset.Revision, "object_verification_failed")
		}
		httpx.Error(w, r, http.StatusBadGateway, "object_verification_failed", "uploaded object could not be verified")
		return
	}
	if hasLifecycle {
		if asset.Lifecycle == LifecycleMissingObject {
			asset, err = h.store.(MissingObjectRecovery).RecoverMissing(r.Context(), user.TenantID, asset.ID, asset.Revision)
		} else {
			asset, err = lifecycle.Activate(r.Context(), user.TenantID, asset.ID, asset.Revision)
		}
		if err != nil {
			// Keep the object and pending metadata: a retry can safely reconcile
			// the deterministic storage key instead of creating an orphan.
			writeStoreError(w, r, err)
			return
		}
	} else {
		asset, err = h.store.Create(r.Context(), input)
		if err != nil {
			_ = h.objects.Remove(r.Context(), h.cfg.Bucket, storageKey)
			writeStoreError(w, r, err)
			return
		}
	}
	h.auditAction(r, "file.uploaded", "file_asset", asset.ID, "upload private file")
	httpx.JSON(w, http.StatusCreated, map[string]any{"file": asset.Response()})
}

func (h *Handler) verifyStoredObject(r *http.Request, bucket, key string, expectedSize int64, expectedHash string) error {
	stored, err := h.objects.Get(r.Context(), bucket, key)
	if err != nil {
		return err
	}
	defer stored.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, stored)
	if err != nil || size != expectedSize || hex.EncodeToString(hash.Sum(nil)) != expectedHash {
		return ErrStorageFailure
	}
	return nil
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	asset, err := h.getScoped(r, user, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"file": asset.Response()})
}

func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	resource, err := h.downloads.ReadDownload(r.Context(), user, r.PathValue("id"))
	if err != nil {
		WriteDownloadError(w, r, err)
		return
	}
	binaryresourcehttp.Serve(w, r, resource, h.audit)
}

func normalizeFileConfig(cfg config.FileConfig) config.FileConfig {
	if cfg.Bucket == "" {
		cfg.Bucket = "edugrade-files"
	}
	if cfg.MaxUploadBytes <= 0 {
		cfg.MaxUploadBytes = 100 * 1024 * 1024
	}
	if len(cfg.AllowedExtensions) == 0 {
		cfg.AllowedExtensions = []string{".pdf", ".png", ".jpg", ".jpeg", ".tif", ".tiff", ".csv", ".docx"}
	}
	return cfg
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	lifecycle, hasLifecycle := h.store.(LifecycleStore)
	if !hasLifecycle {
		asset, err := h.store.Delete(r.Context(), user.TenantID, r.PathValue("id"))
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		cleanup := "removed"
		if err := h.objects.Remove(r.Context(), asset.StorageBucket, asset.StorageKey); err != nil {
			cleanup = "pending"
		}
		h.auditAction(r, "file.deleted", "file_asset", asset.ID, "delete private file")
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "object_cleanup": cleanup})
		return
	}
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
		return
	}
	current, err := lifecycle.GetScoped(r.Context(), scope, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	// 数据库与对象存储不共用事务；先记录待删状态，清理失败后保留可重试的进度。
	asset, err := lifecycle.BeginDelete(r.Context(), scope, current.ID, current.Revision)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if err := h.objects.Remove(r.Context(), asset.StorageBucket, asset.StorageKey); err != nil {
		_, _ = lifecycle.MarkDeleteFailed(r.Context(), user.TenantID, asset.ID, asset.Revision, "object_remove_failed")
		h.auditAction(r, "file.delete_pending", "file_asset", asset.ID, "object cleanup requires retry")
		httpx.Error(w, r, http.StatusServiceUnavailable, "object_cleanup_pending", "file deletion is pending storage cleanup; retry later")
		return
	}
	asset, err = lifecycle.CompleteDelete(r.Context(), user.TenantID, asset.ID, asset.Revision)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "file.deleted", "file_asset", asset.ID, "delete private file")
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "object_cleanup": "removed"})
}

func (h *Handler) getScoped(r *http.Request, user auth.User, id string) (FileAsset, error) {
	if lifecycle, ok := h.store.(LifecycleStore); ok {
		scope, exists := auth.AccessScopeFromContext(r.Context())
		if !exists {
			return FileAsset{}, ErrForbidden
		}
		return lifecycle.GetScoped(r.Context(), scope, id)
	}
	return h.store.Get(r.Context(), user.TenantID, id)
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "file_not_found", "file asset not found")
	case errors.Is(err, ErrDuplicateFile):
		httpx.Error(w, r, http.StatusConflict, "duplicate_file", "same file already exists for this owner")
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, r, http.StatusForbidden, "file_access_forbidden", "file is outside the current data access scope")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "resource_version_conflict", "file state changed; refresh and retry")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "file_operation_failed", "file operation failed")
	}
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func (h *Handler) auditAction(r *http.Request, action string, targetType string, targetID string, reason string) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID:   user.TenantID,
		ActorID:    user.ID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Reason:     reason,
		IPAddress:  r.RemoteAddr,
		UserAgent:  r.UserAgent(),
		RequestID:  logger.RequestID(r.Context()),
	})
}
