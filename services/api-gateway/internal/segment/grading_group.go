package segment

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/binaryresource"
)

// GradingGroupCrop 和单题 bbox 一起保存，绑定同一次配准产物；不改写该题自己的裁图或分数。
type GradingGroupCrop struct {
	ID             string             `json:"id"`
	QuestionIDs    []string           `json:"question_ids"`
	NormalizedBBox map[string]float64 `json:"normalized_bbox"`
	PixelBBox      map[string]float64 `json:"pixel_bbox"`
	FileAssetID    string             `json:"file_asset_id"`
	SHA256         string             `json:"sha256"`
}

type GradingGroupReader interface {
	GetGradingGroup(context.Context, string, string) (*GradingGroupCrop, error)
	ReadGradingGroupImage(context.Context, string, string) (binaryresource.Resource, error)
}

type AnswerScopeReader interface {
	GetAnswerScope(context.Context, string, string) (string, error)
}

func (s *ImageService) GetAnswerScope(ctx context.Context, tenantID, segmentID string) (string, error) {
	evidence, err := s.segments.GetEvidence(ctx, tenantID, segmentID)
	if err != nil {
		return "", err
	}
	scope := AnswerScope(evidence.NormalizedBBox)
	if scope != "question" && scope != "grading_group" {
		return "", ErrInvalidInput
	}
	return scope, nil
}

// AnswerScope 区分精确单题图片与仅有整区来源的答案；历史记录缺省为 question。
func AnswerScope(bbox map[string]any) string {
	if raw, exists := bbox["answer_scope"]; exists {
		if value, ok := raw.(string); ok && value != "" {
			return value
		}
		return "invalid"
	}
	return "question"
}

// ParseGradingGroupCrop 只接收 worker 附在冻结子题 bbox 内的共享裁图记录；历史无分组记录返回 nil。
func ParseGradingGroupCrop(bbox map[string]any, questionID string) (*GradingGroupCrop, error) {
	scope := AnswerScope(bbox)
	if scope != "question" && scope != "grading_group" {
		return nil, ErrInvalidInput
	}
	raw, exists := bbox["grading_group"]
	if !exists {
		if scope == "grading_group" {
			return nil, ErrInvalidInput
		}
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, ErrInvalidInput
	}
	var group GradingGroupCrop
	if err := json.Unmarshal(encoded, &group); err != nil {
		return nil, ErrInvalidInput
	}
	if group.ID == "" || strings.TrimSpace(group.ID) != group.ID || len(group.ID) > 128 || group.FileAssetID == "" || len(group.QuestionIDs) < 2 || len(group.QuestionIDs) > 500 || len(group.SHA256) != 64 {
		return nil, ErrInvalidInput
	}
	if _, err := hex.DecodeString(group.SHA256); err != nil {
		return nil, ErrInvalidInput
	}
	seen, member := map[string]bool{}, false
	for _, id := range group.QuestionIDs {
		if strings.TrimSpace(id) == "" || seen[id] {
			return nil, ErrInvalidInput
		}
		seen[id], member = true, member || id == questionID
	}
	if !member || !validGroupBBox(group.NormalizedBBox, true) || !validGroupBBox(group.PixelBBox, false) {
		return nil, ErrInvalidInput
	}
	for _, key := range []string{"x", "y", "width", "height"} {
		value, ok := bbox[key].(float64)
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, ErrInvalidInput
		}
	}
	x, y, w, h := bbox["x"].(float64), bbox["y"].(float64), bbox["width"].(float64), bbox["height"].(float64)
	g := group.NormalizedBBox
	if g["x"] > x+1e-6 || g["y"] > y+1e-6 || g["x"]+g["width"] < x+w-1e-6 || g["y"]+g["height"] < y+h-1e-6 {
		return nil, ErrInvalidInput
	}
	return &group, nil
}

func validGroupBBox(box map[string]float64, normalized bool) bool {
	for _, key := range []string{"x", "y", "width", "height"} {
		value, ok := box[key]
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return false
		}
	}
	if box["width"] <= 0 || box["height"] <= 0 {
		return false
	}
	return !normalized || box["x"]+box["width"] <= 1.000001 && box["y"]+box["height"] <= 1.000001
}

func (s *ImageService) GetGradingGroup(ctx context.Context, tenantID, segmentID string) (*GradingGroupCrop, error) {
	evidence, err := s.segments.GetEvidence(ctx, tenantID, segmentID)
	if err != nil {
		return nil, err
	}
	return ParseGradingGroupCrop(evidence.NormalizedBBox, evidence.QuestionID)
}

func (s *ImageService) ReadGradingGroupImage(ctx context.Context, tenantID, segmentID string) (binaryresource.Resource, error) {
	evidence, err := s.segments.GetEvidence(ctx, tenantID, segmentID)
	if err != nil {
		return binaryresource.Resource{}, err
	}
	group, err := ParseGradingGroupCrop(evidence.NormalizedBBox, evidence.QuestionID)
	if err != nil || group == nil || evidence.ProcessingStatus != "completed" || evidence.RegistrationStatus != "completed" {
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	asset, err := s.files.Get(ctx, tenantID, group.FileAssetID)
	if err != nil || asset.ExamID != evidence.ExamID || asset.HashSHA256 != group.SHA256 || asset.DeletedAt != nil || !strings.HasPrefix(asset.ContentType, "image/") || asset.SizeBytes <= 0 {
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	// 共享图和单题图必须属于同一次配准或当前修正预览，不能凭组ID读取其他答卷的资产。
	if !((asset.OwnerType == "answer_segment_crop" && asset.OwnerID == evidence.RegistrationRunID) || (asset.OwnerType == "page_registration_correction_preview" && evidence.CorrectionID != "" && asset.OwnerID == evidence.CorrectionID)) {
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	return binaryresource.Resource{ContentType: asset.ContentType, Size: asset.SizeBytes, Disposition: `inline; filename="grading-group.png"`, ETag: `"sha256:` + group.SHA256 + `"`, CacheControl: "private, no-cache", AllowHead: true,
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			return s.objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
		}, OpenErrorMessage: "failed to read grading group image",
		AuditAction: "segment.grading_group_image_viewed", AuditTargetType: "answer_segment", AuditTargetID: evidence.SegmentID, AuditReason: "view configured shared grading region"}, nil
}
