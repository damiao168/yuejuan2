package goldpaper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresource"
	"edugrade-enterprise/services/api-gateway/internal/binaryresourcehttp"
	"edugrade-enterprise/services/api-gateway/internal/files"
)

type VersionImageReader interface {
	ReadVersionImage(context.Context, string, string, int) (binaryresource.Resource, error)
}

type ImageService struct {
	gold    Store
	files   files.Store
	objects files.ObjectStorage
}

// 完整校验发生在写出字节之前；限制单张冻结图大小，避免对象被覆盖后把无限数据读入内存。
const maxFrozenImageBytes int64 = 64 << 20

func NewImageService(gold Store, fileStore files.Store, objects files.ObjectStorage) *ImageService {
	return &ImageService{gold: gold, files: fileStore, objects: objects}
}

func VersionImageURL(goldID string, version int) string {
	return fmt.Sprintf("/api/v1/gold-papers/%s/versions/%d/image", goldID, version)
}

// ParseVersionImageURL 仅解析服务器保存的 Gold 来源链接，供暗样本委托读取；客户端不能借此选择任意文件。
func ParseVersionImageURL(value string) (string, int, bool) {
	parts := strings.Split(strings.Trim(value, "/"), "/")
	if len(parts) != 7 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "gold-papers" || parts[4] != "versions" || parts[6] != "image" {
		return "", 0, false
	}
	version, err := strconv.Atoi(parts[5])
	return parts[3], version, err == nil && version > 0
}

// ReadVersionImage 的调用方先完成 Gold、校准会话或暗样本任务授权；此处只读取该版本冻结的资产。
func (s *ImageService) ReadVersionImage(ctx context.Context, tenantID, goldID string, number int) (binaryresource.Resource, error) {
	item, err := s.gold.Get(ctx, tenantID, goldID)
	if err != nil {
		return binaryresource.Resource{}, err
	}
	var binding ImageBinding
	for _, version := range item.Versions {
		if version.Version == number && version.ImageInvalidatedAt == nil {
			binding = version.ImageBinding
		}
	}
	if !binding.Valid() || s.files == nil || s.objects == nil {
		return binaryresource.Resource{}, ErrImageUnavailable
	}
	asset, err := s.files.Get(ctx, tenantID, binding.AssetID)
	if err != nil || asset.DeletedAt != nil || asset.ExamID != item.ExamID || asset.HashSHA256 != binding.SHA256 ||
		asset.OwnerType != binding.OwnerType || asset.OwnerID != binding.OwnerID || !strings.HasPrefix(asset.ContentType, "image/") ||
		asset.SizeBytes <= 0 || asset.SizeBytes > maxFrozenImageBytes || asset.Lifecycle != files.LifecycleActive {
		return binaryresource.Resource{}, ErrImageUnavailable
	}
	return binaryresource.Resource{
		ContentType: asset.ContentType, Size: asset.SizeBytes, Disposition: `inline; filename="gold-answer.png"`,
		ETag: `"sha256:` + binding.SHA256 + `"`, CacheControl: "private, no-store", AllowHead: true,
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			body, err := s.objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
			if err != nil {
				return nil, err
			}
			defer body.Close()
			// 对象键若被意外覆盖，必须在写出任何字节前拒绝，不能只相信资产表中的旧哈希。
			data, err := io.ReadAll(io.LimitReader(body, asset.SizeBytes+1))
			digest := sha256.Sum256(data)
			if err != nil || int64(len(data)) != asset.SizeBytes || hex.EncodeToString(digest[:]) != binding.SHA256 {
				return nil, ErrImageUnavailable
			}
			return io.NopCloser(bytes.NewReader(data)), nil
		},
		OpenErrorMessage: "frozen Gold image could not be verified", AuditAction: "gold_paper.image_viewed",
		AuditTargetType: "gold_paper", AuditTargetID: goldID, AuditReason: "view frozen Gold answer image",
	}, nil
}

func (h *Handler) WithImageReader(reader VersionImageReader) *Handler {
	h.images = reader
	return h
}

func (h *Handler) GetVersionImage(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, r, ErrNotFound)
		return
	}
	number, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || number <= 0 || h.images == nil {
		writeError(w, r, ErrImageUnavailable)
		return
	}
	resource, err := h.images.ReadVersionImage(r.Context(), user.TenantID, r.PathValue("id"), number)
	if err != nil {
		writeError(w, r, err)
		return
	}
	binaryresourcehttp.Serve(w, r, resource, h.audit)
}
