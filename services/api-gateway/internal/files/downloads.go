package files

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresource"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

var ErrNotActive = errors.New("file is not active")

// DownloadReader is the narrow private-file capability used after a review
// task has been authorized. It still enforces the file's own data scope.
type DownloadReader interface {
	ReadDownload(context.Context, auth.User, string) (binaryresource.Resource, error)
}

type DownloadService struct {
	store   Store
	objects ObjectStorage
}

func NewDownloadService(store Store, objects ObjectStorage) *DownloadService {
	return &DownloadService{store: store, objects: objects}
}

func (s *DownloadService) ReadDownload(ctx context.Context, user auth.User, id string) (binaryresource.Resource, error) {
	var asset FileAsset
	var err error
	if lifecycle, ok := s.store.(LifecycleStore); ok {
		scope, exists := auth.AccessScopeFromContext(ctx)
		if !exists {
			return binaryresource.Resource{}, ErrForbidden
		}
		asset, err = lifecycle.GetScoped(ctx, scope, id)
	} else {
		asset, err = s.store.Get(ctx, user.TenantID, id)
	}
	if err != nil {
		return binaryresource.Resource{}, err
	}
	if asset.Lifecycle != "" && asset.Lifecycle != LifecycleActive {
		return binaryresource.Resource{}, ErrNotActive
	}
	return binaryresource.Resource{
		ContentType: asset.ContentType, Size: asset.SizeBytes,
		Disposition: mime.FormatMediaType("attachment", map[string]string{"filename": asset.OriginalName}),
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			// 使用资产记录中的桶，不能用当前默认桶替换历史文件的实际位置。
			return s.objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
		},
		OpenErrorMessage: "failed to read object storage",
		AuditAction:      "file.downloaded", AuditTargetType: "file_asset", AuditTargetID: asset.ID,
		AuditReason: "download private file",
	}, nil
}

func WriteDownloadError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotActive) {
		httpx.Error(w, r, http.StatusConflict, "file_not_active", "file is not available for download")
		return
	}
	writeStoreError(w, r, err)
}
