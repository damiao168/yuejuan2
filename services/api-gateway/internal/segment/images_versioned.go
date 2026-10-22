package segment

import (
	"context"
	"errors"
	"io"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/binaryresource"
)

var ErrEvidenceVersionChanged = errors.New("answer segment evidence version changed")

// CropImageVersion 由已授权材料查询取得，不能接受浏览器传入的资产标识。
type CropImageVersion struct{ AssetID, SHA256, RegistrationID, CorrectionID string }

type VersionedCropImageReader interface {
	ReadVersionedCropImage(context.Context, string, string, CropImageVersion) (binaryresource.Resource, error)
}

// ReadVersionedCropImage 将校验和实际打开绑定到固定资产；校验后不再按题块查询当前图片。
func (s *ImageService) ReadVersionedCropImage(ctx context.Context, tenantID, segmentID string, expected CropImageVersion) (binaryresource.Resource, error) {
	e, err := s.segments.GetEvidence(ctx, tenantID, segmentID)
	if err != nil {
		return binaryresource.Resource{}, err
	}
	if e.CropFileAssetID != expected.AssetID || e.CropSHA256 != expected.SHA256 || e.RegistrationRunID != expected.RegistrationID || e.CorrectionID != expected.CorrectionID {
		return binaryresource.Resource{}, ErrEvidenceVersionChanged
	}
	if e.ProcessingStatus != "completed" || e.RegistrationStatus != "completed" || expected.AssetID == "" || expected.SHA256 == "" {
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	asset, err := s.files.Get(ctx, tenantID, expected.AssetID)
	if err != nil || asset.DeletedAt != nil || asset.ExamID != e.ExamID || asset.HashSHA256 != expected.SHA256 || !strings.HasPrefix(asset.ContentType, "image/") || asset.SizeBytes <= 0 {
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	if !((asset.OwnerType == "answer_segment_crop" && asset.OwnerID == expected.RegistrationID) || (asset.OwnerType == "page_registration_correction_preview" && expected.CorrectionID != "" && asset.OwnerID == expected.CorrectionID)) {
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	return binaryresource.Resource{ContentType: asset.ContentType, Size: asset.SizeBytes, Disposition: `inline; filename="answer.png"`, CacheControl: "private, no-store", AllowHead: true, Open: func(ctx context.Context) (io.ReadCloser, error) {
		return s.objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
	}, OpenErrorMessage: "failed to read answer image"}, nil
}
