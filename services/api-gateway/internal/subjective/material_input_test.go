package subjective

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestBuildGradingAgentV2RequestIncludesFrozenReferenceMedia(t *testing.T) {
	input := validMathV2AdapterInput()
	input.RequestID = "subjective-material-0001"
	student := validResolvedActiveCrop(t)
	var reference bytes.Buffer
	if err := png.Encode(&reference, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	ref := ReferenceMedia{MediaID: "media-diagram-1", Role: "question_diagram", Ordinal: 1, Encoding: "base64", MediaType: "image/png", SHA256: fmt.Sprintf("%x", sha256.Sum256(reference.Bytes())), ByteSize: int64(reference.Len()), WidthPixels: 2, HeightPixels: 2, Data: reference.Bytes()}
	input.ReferenceManifest = &ReferenceManifest{Mode: "reference_images", ManifestHash: strings.Repeat("a", 64), AssessmentSnapshotID: "snapshot-1", AssessmentSnapshotHash: strings.Repeat("b", 64), RequiredMediaIDs: []string{ref.MediaID}}
	input.ReferenceManifest.AssessmentSnapshotID = input.AssessmentSnapshot.ID
	input.AssessmentSnapshot.ContentHash = input.ReferenceManifest.AssessmentSnapshotHash
	input.ReferenceMedia = []ReferenceMedia{ref}
	built, err := BuildGradingAgentV2Request(input.RequestID, input, student, ModelPolicy{ModelVersion: "fixture-v1", PromptVersion: "prompt-v2", MinConfidence: .8})
	if err != nil {
		t.Fatal(err)
	}
	defer built.Clear()
	var body map[string]any
	if err := json.Unmarshal(built.Body, &body); err != nil {
		t.Fatal(err)
	}
	features, ok := body["required_features"].([]any)
	if !ok || len(features) != 1 || features[0] != materialInputFeature {
		t.Fatalf("features=%#v", body["required_features"])
	}
	manifest, ok := body["reference_manifest"].(map[string]any)
	if !ok || manifest["manifest_hash"] != input.ReferenceManifest.ManifestHash {
		t.Fatalf("manifest=%#v", body["reference_manifest"])
	}
	media, ok := body["reference_media"].([]any)
	if !ok || len(media) != 1 {
		t.Fatalf("media=%#v", body["reference_media"])
	}
	item := media[0].(map[string]any)
	if item["media_id"] != ref.MediaID || item["role"] != ref.Role || item["ordinal"] != float64(1) || item["sha256"] != ref.SHA256 {
		t.Fatalf("item=%#v", item)
	}
	if body["request_binding_hash"] == "" {
		t.Fatal("request binding hash missing")
	}
}

func TestBuildGradingAgentV2RequestRejectsUnboundReferenceMedia(t *testing.T) {
	input := validMathV2AdapterInput()
	crop := validResolvedActiveCrop(t)
	input.ReferenceManifest = &ReferenceManifest{Mode: "reference_images", ManifestHash: strings.Repeat("a", 64), AssessmentSnapshotID: "snapshot-1", AssessmentSnapshotHash: strings.Repeat("b", 64), RequiredMediaIDs: []string{"missing"}}
	input.ReferenceMedia = []ReferenceMedia{{MediaID: "other", Role: "question_diagram", Ordinal: 1, Encoding: "base64", MediaType: "image/png", SHA256: crop.SHA256, ByteSize: crop.ByteSize, WidthPixels: crop.WidthPixels, HeightPixels: crop.HeightPixels, Data: crop.Data}}
	_, err := BuildGradingAgentV2Request(input.RequestID, input, crop, ModelPolicy{ModelVersion: "fixture-v1", PromptVersion: "prompt-v2", MinConfidence: .8})
	assertGradingAgentV2ErrorCode(t, err, "material_manifest_invalid")
}

// Python 消费端使用同一固定向量，防止字段顺序或长度编码变化让已冻结请求失去绑定。
func TestMaterialRequestBindingCrossLanguageVector(t *testing.T) {
	manifest := &ReferenceManifest{ManifestHash: strings.Repeat("a", 64), AssessmentSnapshotID: "snapshot-1", AssessmentSnapshotHash: strings.Repeat("b", 64)}
	media := ReferenceMedia{MediaID: "ref-diagram", Role: "question_diagram", Ordinal: 1, SHA256: strings.Repeat("d", 64)}
	if got := computeReferenceBindingHash("subjective-material-vector-0001", "question:1", "segment.1", manifest, media); got != "6f6810a9db1fdea1a7bb98adb92a53b4b4e5677ff93edfd97b6bd800609c2bfe" {
		t.Fatalf("reference binding=%s", got)
	}
	if got := computeMaterialRequestBindingHash("subjective-material-vector-0001", "question:1", "segment.1", manifest, ResolvedActiveCrop{SHA256: strings.Repeat("c", 64)}, []ReferenceMedia{media}); got != "0140eb7f168a21455b2a41bb8445992822d6dc694c5c8188e31c83c4995174ea" {
		t.Fatalf("request binding=%s", got)
	}
}
