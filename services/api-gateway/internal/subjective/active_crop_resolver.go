package subjective

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

const (
	maxActiveCropBytes    int64 = 5 * 1024 * 1024
	maxActiveCropPixels         = 12_000_000
	maxActiveCropBBoxArea       = 0.9
	activeCropBBoxScale         = 1_000_000
)

var ErrActiveCropUnavailable = errors.New("active answer crop is unavailable")

type ActiveCropBBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type ResolvedActiveCrop struct {
	Kind           string         `json:"kind"`
	MediaType      string         `json:"media_type"`
	SHA256         string         `json:"sha256"`
	ByteSize       int64          `json:"byte_size"`
	WidthPixels    int            `json:"width_pixels"`
	HeightPixels   int            `json:"height_pixels"`
	NormalizedBBox ActiveCropBBox `json:"normalized_bbox"`
	Data           []byte         `json:"-"`
}

type ActiveCropEvidenceStore interface {
	GetEvidenceForQuestion(ctx context.Context, tenantID string, segmentID string, questionID string) (segment.SegmentEvidence, error)
}

type ActiveCropResolver struct {
	evidence ActiveCropEvidenceStore
	assets   files.Store
	objects  files.ObjectStorage
}

func NewActiveCropResolver(evidence ActiveCropEvidenceStore, assets files.Store, objects files.ObjectStorage) *ActiveCropResolver {
	return &ActiveCropResolver{evidence: evidence, assets: assets, objects: objects}
}

func (r *ActiveCropResolver) Resolve(ctx context.Context, tenantID string, segmentID string, questionID string) (ResolvedActiveCrop, error) {
	if r == nil || r.evidence == nil || r.assets == nil || r.objects == nil ||
		strings.TrimSpace(tenantID) == "" || strings.TrimSpace(segmentID) == "" || strings.TrimSpace(questionID) == "" {
		return ResolvedActiveCrop{}, ErrActiveCropUnavailable
	}
	// 先锁定租户范围内当前裁剪的元数据；对象读取前后都会复核它，避免修正或替换在读取期间改变。
	first, err := r.loadMetadata(ctx, tenantID, segmentID, questionID)
	if err != nil {
		return ResolvedActiveCrop{}, resolverError(ctx)
	}
	body, err := r.objects.Get(ctx, first.storageBucket, first.storageKey)
	if err != nil {
		return ResolvedActiveCrop{}, resolverError(ctx)
	}
	data, readErr := io.ReadAll(io.LimitReader(body, maxActiveCropBytes+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) > maxActiveCropBytes || int64(len(data)) != first.byteSize {
		return ResolvedActiveCrop{}, resolverError(ctx)
	}
	width, height, err := validateActiveCropPNG(data)
	if err != nil {
		return ResolvedActiveCrop{}, ErrActiveCropUnavailable
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != first.sha256 {
		return ResolvedActiveCrop{}, ErrActiveCropUnavailable
	}

	// Re-read tenant-scoped metadata after object I/O. An applied correction can
	// be undone, or a segment crop can be replaced, while the object is read.
	second, err := r.loadMetadata(ctx, tenantID, segmentID, questionID)
	if err != nil || first != second {
		return ResolvedActiveCrop{}, resolverError(ctx)
	}
	return ResolvedActiveCrop{
		Kind:           "answer_segment_crop",
		MediaType:      "image/png",
		SHA256:         first.sha256,
		ByteSize:       first.byteSize,
		WidthPixels:    width,
		HeightPixels:   height,
		NormalizedBBox: first.bbox,
		Data:           data,
	}, nil
}

type activeCropMetadata struct {
	segmentID      string
	submissionID   string
	examID         string
	questionID     string
	registrationID string
	correctionID   string
	assetID        string
	ownerType      string
	ownerID        string
	storageBucket  string
	storageKey     string
	sha256         string
	byteSize       int64
	bbox           ActiveCropBBox
}

func (r *ActiveCropResolver) loadMetadata(ctx context.Context, tenantID string, segmentID string, questionID string) (activeCropMetadata, error) {
	evidence, err := r.evidence.GetEvidenceForQuestion(ctx, tenantID, segmentID, questionID)
	if err != nil {
		return activeCropMetadata{}, ErrActiveCropUnavailable
	}
	// 只有已完成、未删除且归属关系完整的裁剪才能进入模型请求；任何状态不一致都按不可用处理。
	if evidence.SegmentID != segmentID ||
		evidence.QuestionID != questionID ||
		evidence.SubmissionID == "" ||
		evidence.ExamID == "" ||
		evidence.RegistrationRunID == "" ||
		evidence.ProcessingStatus != "completed" ||
		evidence.RegistrationStatus != "completed" ||
		evidence.CropFileAssetID == "" ||
		!validLowerSHA256(evidence.CropSHA256) {
		return activeCropMetadata{}, ErrActiveCropUnavailable
	}
	bbox, err := activeCropBBox(evidence.NormalizedBBox)
	if err != nil {
		return activeCropMetadata{}, ErrActiveCropUnavailable
	}
	asset, err := r.assets.Get(ctx, tenantID, evidence.CropFileAssetID)
	if err != nil ||
		asset.ID != evidence.CropFileAssetID ||
		asset.TenantID != tenantID ||
		asset.ExamID != evidence.ExamID ||
		asset.DeletedAt != nil ||
		(asset.SubmissionID != "" && asset.SubmissionID != evidence.SubmissionID) ||
		asset.Visibility != "private" ||
		strings.TrimSpace(strings.ToLower(asset.ContentType)) != "image/png" ||
		asset.SizeBytes <= 0 ||
		asset.SizeBytes > maxActiveCropBytes ||
		asset.HashSHA256 != evidence.CropSHA256 ||
		asset.StorageBucket == "" ||
		asset.StorageKey == "" {
		return activeCropMetadata{}, ErrActiveCropUnavailable
	}
	if evidence.CorrectionID == "" {
		if asset.OwnerType != "answer_segment_crop" || asset.OwnerID != evidence.RegistrationRunID {
			return activeCropMetadata{}, ErrActiveCropUnavailable
		}
	} else if asset.OwnerType != "page_registration_correction_preview" || asset.OwnerID != evidence.CorrectionID {
		return activeCropMetadata{}, ErrActiveCropUnavailable
	}
	return activeCropMetadata{
		segmentID:      evidence.SegmentID,
		submissionID:   evidence.SubmissionID,
		examID:         evidence.ExamID,
		questionID:     evidence.QuestionID,
		registrationID: evidence.RegistrationRunID,
		correctionID:   evidence.CorrectionID,
		assetID:        asset.ID,
		ownerType:      asset.OwnerType,
		ownerID:        asset.OwnerID,
		storageBucket:  asset.StorageBucket,
		storageKey:     asset.StorageKey,
		sha256:         evidence.CropSHA256,
		byteSize:       asset.SizeBytes,
		bbox:           bbox,
	}, nil
}

// 只解析 PNG 头部并先做像素上限检查，避免为验证一张答案图而进行无界解码。
func validateActiveCropPNG(data []byte) (int, int, error) {
	signature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if len(data) < 33 ||
		!bytes.Equal(data[:8], signature) ||
		binary.BigEndian.Uint32(data[8:12]) != 13 ||
		string(data[12:16]) != "IHDR" {
		return 0, 0, ErrActiveCropUnavailable
	}
	width := int(binary.BigEndian.Uint32(data[16:20]))
	height := int(binary.BigEndian.Uint32(data[20:24]))
	if width <= 0 || height <= 0 || width > maxActiveCropPixels/height {
		return 0, 0, ErrActiveCropUnavailable
	}
	return width, height, nil
}

func activeCropBBox(value map[string]any) (ActiveCropBBox, error) {
	if len(value) != 4 {
		return ActiveCropBBox{}, ErrActiveCropUnavailable
	}
	for _, field := range []string{"x", "y", "width", "height"} {
		if _, ok := value[field]; !ok {
			return ActiveCropBBox{}, ErrActiveCropUnavailable
		}
	}
	bbox := ActiveCropBBox{}
	targets := []*float64{&bbox.X, &bbox.Y, &bbox.Width, &bbox.Height}
	for index, field := range []string{"x", "y", "width", "height"} {
		number, ok := finiteFloat(value[field])
		if !ok || !sixDecimalBBox(number) {
			return ActiveCropBBox{}, ErrActiveCropUnavailable
		}
		*targets[index] = number
	}
	if bbox.X < 0 || bbox.Y < 0 || bbox.Width <= 0 || bbox.Height <= 0 ||
		bbox.X+bbox.Width > 1 || bbox.Y+bbox.Height > 1 ||
		bbox.Width*bbox.Height >= maxActiveCropBBoxArea {
		return ActiveCropBBox{}, ErrActiveCropUnavailable
	}
	return bbox, nil
}

func finiteFloat(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func sixDecimalBBox(value float64) bool {
	units := math.Round(value * activeCropBBoxScale)
	return math.Abs(value-units/activeCropBBoxScale) <= 1e-12
}

func validLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func resolverError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrActiveCropUnavailable
}
