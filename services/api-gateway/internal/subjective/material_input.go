package subjective

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image/png"
	"strings"
)

const (
	materialInputFeature = "material-input-v1"
	maxReferenceMedia    = 4
	maxReferencePerRole  = 2
	maxReferenceBytes    = 1 * 1024 * 1024
	maxReferencePixels   = 4_000_000
	maxAllMediaBytes     = 5 * 1024 * 1024
	maxAllMediaPixels    = 12_000_000
)

// ReferenceManifest 是确认考试时冻结的参考图清单投影。顺序和摘要属于评分输入，不能从当前题目配置重新读取。
type ReferenceManifest struct {
	Mode                   string   `json:"mode"`
	ManifestHash           string   `json:"manifest_hash"`
	AssessmentSnapshotID   string   `json:"assessment_snapshot_id"`
	AssessmentSnapshotHash string   `json:"assessment_snapshot_hash"`
	RequiredMediaIDs       []string `json:"required_media_ids"`
}

// ReferenceMedia 是服务端已解码、裁剪并授权的 PNG。Data 不会写入运行记录，只在构造实际请求时短暂编码。
type ReferenceMedia struct {
	MediaID      string `json:"media_id"`
	Role         string `json:"role"`
	Ordinal      int    `json:"ordinal"`
	BindingHash  string `json:"binding_hash"`
	Encoding     string `json:"encoding"`
	MediaType    string `json:"media_type"`
	SHA256       string `json:"sha256"`
	ByteSize     int64  `json:"byte_size"`
	WidthPixels  int    `json:"width_pixels"`
	HeightPixels int    `json:"height_pixels"`
	Data         []byte `json:"-"`
}

func validMaterialManifest(m *ReferenceManifest, media []ReferenceMedia) error {
	if m == nil {
		if len(media) != 0 {
			return &GradingAgentError{Code: "material_manifest_missing"}
		}
		return nil
	}
	if m.Mode != "reference_images" || !validLowerSHA256(m.ManifestHash) || !validLowerSHA256(m.AssessmentSnapshotHash) || strings.TrimSpace(m.AssessmentSnapshotID) == "" || len(media) < 1 || len(media) > maxReferenceMedia || len(m.RequiredMediaIDs) != len(media) {
		return &GradingAgentError{Code: "material_manifest_invalid"}
	}
	seen := map[string]bool{}
	for _, id := range m.RequiredMediaIDs {
		if strings.TrimSpace(id) == "" || seen[id] {
			return &GradingAgentError{Code: "material_manifest_invalid"}
		}
		seen[id] = true
	}
	if len(media) > 0 {
		for i, item := range media {
			if !seen[item.MediaID] || item.MediaID != m.RequiredMediaIDs[i] || item.Ordinal != i+1 {
				return &GradingAgentError{Code: "material_manifest_invalid"}
			}
			delete(seen, item.MediaID)
		}
	}
	roleCount := map[string]int{}
	var bytes, pixels int64
	for _, item := range media {
		if err := validateReferenceMedia(item); err != nil {
			return err
		}
		roleCount[item.Role]++
		if roleCount[item.Role] > maxReferencePerRole {
			return &GradingAgentError{Code: "material_media_invalid"}
		}
		bytes += item.ByteSize
		pixels += int64(item.WidthPixels) * int64(item.HeightPixels)
	}
	if bytes > maxAllMediaBytes || pixels > maxAllMediaPixels {
		return &GradingAgentError{Code: "material_media_budget_exceeded"}
	}
	return nil
}

func validateReferenceMedia(item ReferenceMedia) error {
	if !gradingAgentV2InternalID.MatchString(item.MediaID) || (item.Role != "question_diagram" && item.Role != "reference_solution") || item.Ordinal < 1 || item.Ordinal > maxReferenceMedia || item.Encoding != "base64" || item.MediaType != "image/png" || !validLowerSHA256(item.SHA256) || item.ByteSize < 1 || item.ByteSize > maxReferenceBytes || int64(len(item.Data)) != item.ByteSize || item.WidthPixels < 1 || item.HeightPixels < 1 || int64(item.WidthPixels)*int64(item.HeightPixels) > maxReferencePixels || len(item.Data) == 0 {
		return &GradingAgentError{Code: "material_media_invalid"}
	}
	if fmt.Sprintf("%x", sha256.Sum256(item.Data)) != item.SHA256 {
		return &GradingAgentError{Code: "material_media_invalid"}
	}
	// 先限尺寸再完整解码；仅检查 PNG 签名会把截断图片送进模型。
	config, err := png.DecodeConfig(bytes.NewReader(item.Data))
	if err != nil || config.Width != item.WidthPixels || config.Height != item.HeightPixels {
		return &GradingAgentError{Code: "material_media_invalid"}
	}
	if _, err := png.Decode(bytes.NewReader(item.Data)); err != nil {
		return &GradingAgentError{Code: "material_media_invalid"}
	}
	return nil
}

func computeReferenceBindingHash(requestID, questionID, segmentID string, manifest *ReferenceManifest, item ReferenceMedia) string {
	parts := []string{"grading-agent-v2", requestID, questionID, segmentID, manifest.ManifestHash, manifest.AssessmentSnapshotID, manifest.AssessmentSnapshotHash, item.MediaID, item.Role, fmt.Sprintf("%d", item.Ordinal), item.SHA256}
	h := sha256.New()
	for _, p := range parts {
		_, _ = fmt.Fprintf(h, "%d:%s", len([]byte(p)), p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func computeMaterialRequestBindingHash(requestID, questionID, segmentID string, manifest *ReferenceManifest, crop ResolvedActiveCrop, media []ReferenceMedia) string {
	parts := []string{"grading-agent-v2", requestID, questionID, segmentID, crop.SHA256}
	if manifest != nil {
		parts = append(parts, manifest.ManifestHash)
		for _, item := range media {
			parts = append(parts, computeReferenceBindingHash(requestID, questionID, segmentID, manifest, item))
		}
	}
	h := sha256.New()
	for _, p := range parts {
		_, _ = fmt.Fprintf(h, "%d:%s", len([]byte(p)), p)
	}
	return hex.EncodeToString(h.Sum(nil))
}
