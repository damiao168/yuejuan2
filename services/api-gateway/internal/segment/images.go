package segment

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/binaryresource"
	"edugrade-enterprise/services/api-gateway/internal/files"
	submissionpkg "edugrade-enterprise/services/api-gateway/internal/submission"
)

// CropImageReader 是 review、regrade、backmark 和 appeal 在完成自身资源授权后使用的受限应用能力。
type CropImageReader interface {
	ReadCropImage(context.Context, string, string) (binaryresource.Resource, error)
}

// PageImageReader 提供与已校验分段关联的页面。
type PageImageReader interface {
	ReadPageImage(context.Context, string, string) (binaryresource.Resource, error)
}

type ImageService struct {
	segments    Store
	submissions submissionpkg.Store
	files       files.Store
	objects     files.ObjectStorage
}

func NewImageService(segments Store, submissions submissionpkg.Store, fileStore files.Store, objects files.ObjectStorage) *ImageService {
	return &ImageService{segments: segments, submissions: submissions, files: fileStore, objects: objects}
}

func WriteImageError(w http.ResponseWriter, r *http.Request, err error) {
	writeStoreError(w, r, err)
}

func (s *ImageService) ReadCropImage(ctx context.Context, tenantID, segmentID string) (binaryresource.Resource, error) {
	evidence, err := s.segments.GetEvidence(ctx, tenantID, segmentID)
	if err != nil {
		return binaryresource.Resource{}, err
	}
	// 整区来源没有精确单题图；必须通过共区入口读取，不能借单题端点改变图片含义。
	if AnswerScope(evidence.NormalizedBBox) != "question" {
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	if evidence.ProcessingStatus != "completed" || evidence.RegistrationStatus != "completed" || evidence.CropFileAssetID == "" || evidence.CropSHA256 == "" {
		// 切题和配准都完成，且裁剪图已有资产 ID 与哈希，才能继续读取评分图片。
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	asset, err := s.files.Get(ctx, tenantID, evidence.CropFileAssetID)
	if err != nil || asset.ExamID != evidence.ExamID || asset.HashSHA256 != evidence.CropSHA256 || asset.DeletedAt != nil || !strings.HasPrefix(asset.ContentType, "image/") || asset.SizeBytes <= 0 {
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	validOwner := (asset.OwnerType == "answer_segment_crop" && asset.OwnerID == evidence.RegistrationRunID) || (asset.OwnerType == "page_registration_correction_preview" && evidence.CorrectionID != "" && asset.OwnerID == evidence.CorrectionID)
	if !validOwner {
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	return binaryresource.Resource{
		ContentType: asset.ContentType, Size: asset.SizeBytes,
		Disposition: mime.FormatMediaType("inline", map[string]string{"filename": "segment-" + evidence.SegmentID + ".png"}),
		ETag:        `"sha256:` + evidence.CropSHA256 + `"`, CacheControl: "private, no-cache", AllowHead: true,
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			return s.objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
		},
		OpenErrorMessage: "failed to read segment image",
		AuditAction:      "segment.image_viewed", AuditTargetType: "answer_segment", AuditTargetID: evidence.SegmentID,
		AuditReason: "view active answer segment image",
	}, nil
}

func (s *ImageService) ReadPageImage(ctx context.Context, tenantID, segmentID string) (binaryresource.Resource, error) {
	evidence, err := s.segments.GetEvidence(ctx, tenantID, segmentID)
	if err != nil {
		return binaryresource.Resource{}, err
	}
	pages, err := s.submissions.ListPages(ctx, tenantID, evidence.SubmissionID)
	if err != nil {
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	assetID := ""
	for _, page := range pages {
		if page.ID == evidence.SubmissionPageID {
			// 优先使用质检后的规范化文件；没有规范化版本时才回退到原始上传文件。
			assetID = page.NormalizedFileAssetID
			if assetID == "" {
				assetID = page.FileAssetID
			}
			break
		}
	}
	if assetID == "" {
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	asset, err := s.files.Get(ctx, tenantID, assetID)
	if err != nil || asset.DeletedAt != nil || asset.ExamID != evidence.ExamID || !strings.HasPrefix(asset.ContentType, "image/") || asset.SizeBytes <= 0 {
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	return binaryresource.Resource{
		ContentType: asset.ContentType, Size: asset.SizeBytes,
		Disposition: mime.FormatMediaType("inline", map[string]string{"filename": "paper-page-" + evidence.SubmissionPageID}),
		ETag:        `"sha256:` + asset.HashSHA256 + `"`, CacheControl: "private, no-cache", AllowHead: true,
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			return s.objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
		},
		OpenErrorMessage: "failed to read paper page image",
		AuditAction:      "student.paper_page_viewed", AuditTargetType: "submission_page", AuditTargetID: evidence.SubmissionPageID,
		AuditReason: "view published paper page",
	}, nil
}
