package grading

import (
	"encoding/json"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestLegacyDefaultOMRReprocessOnlyRefreshesSameUncalibratedExtractionContract(t *testing.T) {
	policy := paper.OMRAutoConfirmPolicyForTemplate(paper.TemplateLayout{OMRProfile: paper.DefaultTemplateOMRProfile()}, "locked", "template", "template")
	old := OMRRun{ProfileVersion: paper.OMRProfileVersionOpenCVFillV1, ProfileHash: policy.ProfileHash,
		AutoConfirmReason: paper.OMRAutoConfirmReasonManualOnlyProfile, AutoConfirmMinConfidence: .9}
	profile, _ := json.Marshal(policy.RuntimeProfile)
	profileHash, _ := json.Marshal(policy.ProfileHash)
	for _, test := range []struct {
		name   string
		change func(*OMRRun, map[string]json.RawMessage)
		want   bool
	}{
		{"same historical default", func(*OMRRun, map[string]json.RawMessage) {}, true},
		{"other reason", func(old *OMRRun, _ map[string]json.RawMessage) { old.AutoConfirmReason = "other_reason" }, false},
		{"already eligible", func(old *OMRRun, _ map[string]json.RawMessage) { old.AutoConfirmEligible = true }, false},
		{"other version", func(old *OMRRun, _ map[string]json.RawMessage) { old.ProfileVersion = "other-version" }, false},
		{"other profile hash", func(old *OMRRun, _ map[string]json.RawMessage) { old.ProfileHash = "other-hash" }, false},
		{"reference asset", func(old *OMRRun, _ map[string]json.RawMessage) { old.ReferenceFileAssetID = "reference" }, false},
		{"reference hash", func(old *OMRRun, _ map[string]json.RawMessage) { old.ReferenceSHA256 = "reference-hash" }, false},
		{"calibration", func(old *OMRRun, _ map[string]json.RawMessage) { old.CalibrationSessionID = "calibration" }, false},
		{"calibration evidence", func(old *OMRRun, _ map[string]json.RawMessage) { old.CalibrationEvidenceHash = "evidence" }, false},
		{"worker profile hash changed", func(_ *OMRRun, payload map[string]json.RawMessage) {
			payload["profile_hash"] = json.RawMessage(`"other-hash"`)
		}, false},
		{"worker extraction changed", func(_ *OMRRun, payload map[string]json.RawMessage) {
			payload["profile"] = json.RawMessage(`{"mode":"manual_only","version":"opencv-fill-v1","marked_threshold":0.4}`)
		}, false},
		{"worker reference", func(_ *OMRRun, payload map[string]json.RawMessage) { payload["reference"] = json.RawMessage(`{}`) }, false},
		{"worker profile missing", func(_ *OMRRun, payload map[string]json.RawMessage) { delete(payload, "profile") }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := old
			payload := map[string]json.RawMessage{"profile": profile, "profile_hash": profileHash}
			test.change(&changed, payload)
			if got := legacyDefaultOMRReprocessMatches(changed, payload); got != test.want {
				t.Fatalf("refresh eligibility=%v want=%v", got, test.want)
			}
		})
	}
}
