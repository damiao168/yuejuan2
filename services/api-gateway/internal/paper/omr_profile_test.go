package paper

import "testing"

func TestNormalizeTemplateLayoutMakesDefaultOMRPolicyExplicit(t *testing.T) {
	layout := NormalizeTemplateLayout(TemplateLayout{Pages: []TemplatePage{{PageNo: 1, Width: 100, Height: 100}}})
	if layout.OMRProfile != DefaultTemplateOMRProfile() {
		t.Fatalf("omitted profile must become the explicit default extraction profile: %#v", layout.OMRProfile)
	}
	if err := ValidateTemplateOMRProfile(layout.OMRProfile); err != nil {
		t.Fatalf("normalized default profile must be valid: %v", err)
	}
}

func TestLockedDefaultOMRProfilePermitsQualityGatedAutoConfirmationWithoutChangingWorkerContract(t *testing.T) {
	layout := TemplateLayout{OMRProfile: DefaultTemplateOMRProfile()}
	policy := OMRAutoConfirmPolicyForTemplate(layout, "locked", "template-hash", "template-hash")
	if !policy.AutoConfirmEligible || policy.Reason != OMRAutoConfirmReasonLockedTemplateOMR {
		t.Fatalf("locked matching default profile must permit per-question quality checks: %#v", policy)
	}
	if policy.MinimumConfidence != 0.8 {
		t.Fatalf("default choice rule-strength threshold must be 0.8: %#v", policy)
	}
	// 资格调整只在服务端生效；旧模板的提取模式、版本和 Worker 配置哈希不能被重新编码。
	if policy.RuntimeProfile.Mode != "manual_only" || policy.RuntimeProfile.Version != "opencv-fill-v1" ||
		policy.ProfileHash != "sha256:83097fe80682c49894f52f1de95ec5e901d656a88f8144884cd9f6a160ca465c" {
		t.Fatalf("historical extraction contract changed: %#v", policy)
	}
	if policy.Calibration != nil || policy.Reference != nil {
		t.Fatalf("default extraction must not require or attach difference calibration: %#v", policy)
	}
}

func TestTemplateOMRAutoConfirmPolicyFailsClosed(t *testing.T) {
	explicit := TemplateLayout{OMRProfile: DefaultTemplateOMRProfile()}
	tests := []struct {
		name         string
		layout       TemplateLayout
		status       string
		templateHash string
		segmentHash  string
		wantReason   string
	}{
		{name: "legacy omitted profile", layout: TemplateLayout{}, status: "locked", templateHash: "template-hash", segmentHash: "template-hash", wantReason: OMRAutoConfirmReasonTemplateProfileUnverified},
		{name: "unknown mode", layout: TemplateLayout{OMRProfile: TemplateOMRProfile{Mode: "unknown", Version: OMRProfileVersionOpenCVFillV1}}, status: "locked", templateHash: "template-hash", segmentHash: "template-hash", wantReason: OMRAutoConfirmReasonUnsupportedProfile},
		{name: "invalid version", layout: TemplateLayout{OMRProfile: TemplateOMRProfile{Mode: OMRProfileModeManualOnly, Version: "unknown"}}, status: "locked", templateHash: "template-hash", segmentHash: "template-hash", wantReason: OMRAutoConfirmReasonUnsupportedProfile},
		{name: "default profile with reference", layout: TemplateLayout{OMRProfile: TemplateOMRProfile{Mode: OMRProfileModeManualOnly, Version: OMRProfileVersionOpenCVFillV1, Reference: &TemplateOMRReference{}}}, status: "locked", templateHash: "template-hash", segmentHash: "template-hash", wantReason: OMRAutoConfirmReasonUnsupportedProfile},
		{name: "template snapshot mismatch", layout: explicit, status: "locked", templateHash: "new-template-hash", segmentHash: "old-template-hash", wantReason: OMRAutoConfirmReasonTemplateContentHashMismatch},
		{name: "empty template hash", layout: explicit, status: "locked", wantReason: OMRAutoConfirmReasonTemplateContentHashMismatch},
		{name: "unlocked template", layout: explicit, status: "draft", templateHash: "template-hash", segmentHash: "template-hash", wantReason: OMRAutoConfirmReasonTemplateNotLocked},
		{name: "unknown template", layout: explicit, status: "", templateHash: "", segmentHash: "template-hash", wantReason: OMRAutoConfirmReasonTemplateNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := OMRAutoConfirmPolicyForTemplate(test.layout, test.status, test.templateHash, test.segmentHash)
			if policy.AutoConfirmEligible {
				t.Fatal("missing or invalid template evidence must block automatic eligibility")
			}
			if policy.Reason != test.wantReason {
				t.Fatalf("reason=%q want=%q", policy.Reason, test.wantReason)
			}
			if policy.RuntimeProfile.Version != OMRProfileVersionOpenCVFillV1 || policy.ProfileHash != DefaultOMRRuntimeProfileHash() {
				t.Fatalf("policy must snapshot the supported runtime profile: %#v", policy)
			}
		})
	}
}

func TestTemplateDifferenceProfileUsesFrozenReferenceButStaysManual(t *testing.T) {
	reference := TemplateOMRReference{Source: OMRReferenceSourceExamPaper, FileAssetID: "asset-1", HashSHA256: "reference-hash", ContentType: "application/pdf"}
	layout := TemplateLayout{
		OMRProfile: TemplateOMRProfile{Mode: OMRProfileModeTemplateDifference, Version: OMRProfileVersionTemplateDifferenceBubbleV1, Reference: &reference},
		Pages:      []TemplatePage{{PageNo: 1, Width: 100, Height: 100, QuestionRegions: []LayoutRegion{{QuestionID: "question-1", X: 0.1, Y: 0.2, Width: 0.3, Height: 0.4}}}},
	}
	policy := OMRAutoConfirmPolicyForTemplateReference(layout, "locked", "template-hash", "template-hash", reference, "question-1")
	if policy.AutoConfirmEligible {
		t.Fatal("an uncalibrated template-difference profile must stay manual")
	}
	if policy.Reason != OMRAutoConfirmReasonCalibrationUnapproved {
		t.Fatalf("unexpected policy reason: %q", policy.Reason)
	}
	if policy.MinimumConfidence != 0.9 {
		t.Fatalf("default choice threshold must not change uncalibrated difference policy: %#v", policy)
	}
	if policy.RuntimeProfile.Version != OMRProfileVersionTemplateDifferenceBubbleV1 || policy.Reference == nil {
		t.Fatalf("difference policy must include a reference-bound runtime contract: %#v", policy)
	}
	if policy.Reference.PageNo != 1 || policy.Reference.X != 0.1 || policy.Reference.FileAssetID != reference.FileAssetID {
		t.Fatalf("unexpected reference crop: %#v", policy.Reference)
	}

	wrong := reference
	wrong.HashSHA256 = "other-hash"
	fallback := OMRAutoConfirmPolicyForTemplateReference(layout, "locked", "template-hash", "template-hash", wrong, "question-1")
	if fallback.RuntimeProfile.Version != OMRProfileVersionOpenCVFillV1 || fallback.Reason != OMRAutoConfirmReasonTemplateReferenceMismatch {
		t.Fatalf("reference mismatch must retain blocked default extraction: %#v", fallback)
	}
}

func TestTemplateDifferenceApprovedCalibrationRequiresExactScope(t *testing.T) {
	reference := TemplateOMRReference{Source: OMRReferenceSourceExamPaper, FileAssetID: "asset-1", HashSHA256: "reference-hash", ContentType: "application/pdf"}
	layout := TemplateLayout{
		OMRProfile: TemplateOMRProfile{Mode: OMRProfileModeTemplateDifference, Version: OMRProfileVersionTemplateDifferenceBubbleV1, Reference: &reference},
		Pages:      []TemplatePage{{PageNo: 1, Width: 100, Height: 100, QuestionRegions: []LayoutRegion{{QuestionID: "question-1", X: 0.1, Y: 0.2, Width: 0.3, Height: 0.4}}}},
	}
	base := OMRAutoConfirmPolicyForTemplateReference(layout, "locked", "template-hash", "template-hash", reference, "question-1")
	approval := OMRCalibrationApproval{
		ID:                   "calibration-1",
		TemplateID:           "template-1",
		TemplateContentHash:  "template-hash",
		QuestionID:           "question-1",
		ProfileVersion:       OMRProfileVersionTemplateDifferenceBubbleV1,
		ProfileHash:          base.ProfileHash,
		ReferenceFileAssetID: reference.FileAssetID,
		ReferenceSHA256:      reference.HashSHA256,
		MinimumConfidence:    OMRCalibrationMinimumConfidence,
		EvidenceHash:         "sha256:evidence",
		Status:               "approved",
	}
	policy := OMRAutoConfirmPolicyForTemplateReferenceAndCalibration(AnswerSheetTemplate{Layout: layout, Status: "locked", ContentHash: "template-hash", ID: "template-1"}, "template-hash", reference, "question-1", &approval)
	if !policy.AutoConfirmEligible || policy.Reason != OMRAutoConfirmReasonCalibrationApproved {
		t.Fatalf("exact approved scope must permit auto-confirmation: %#v", policy)
	}
	if policy.MinimumConfidence != OMRCalibrationMinimumConfidence || policy.Calibration == nil || policy.Calibration.ID != approval.ID {
		t.Fatalf("policy must snapshot the approved calibration threshold and evidence: %#v", policy)
	}

	wrongTemplate := approval
	wrongTemplate.TemplateID = "template-2"
	wrongReference := approval
	wrongReference.ReferenceSHA256 = "other-hash"
	for name, mismatched := range map[string]OMRCalibrationApproval{
		"template":  wrongTemplate,
		"reference": wrongReference,
	} {
		t.Run(name, func(t *testing.T) {
			policy := OMRAutoConfirmPolicyForTemplateReferenceAndCalibration(AnswerSheetTemplate{Layout: layout, Status: "locked", ContentHash: "template-hash", ID: "template-1"}, "template-hash", reference, "question-1", &mismatched)
			if policy.AutoConfirmEligible || policy.Reason != OMRAutoConfirmReasonCalibrationScopeMismatch {
				t.Fatalf("mismatched approval must fail closed: %#v", policy)
			}
		})
	}
}

func TestTemplateOMRProfileRejectsUnknownModeAndInvalidReference(t *testing.T) {
	if err := ValidateTemplateOMRProfile(TemplateOMRProfile{Mode: "template_difference_bubble", Version: "template-diff-v1"}); err == nil {
		t.Fatal("unknown OMR mode must be rejected")
	}
	if err := ValidateTemplateOMRProfile(TemplateOMRProfile{Mode: OMRProfileModeManualOnly, Version: OMRProfileVersionOpenCVFillV1, Reference: &TemplateOMRReference{}}); err == nil {
		t.Fatal("manual-only profile must not carry a reference")
	}
	if err := ValidateTemplateOMRProfile(TemplateOMRProfile{Mode: OMRProfileModeTemplateDifference, Version: OMRProfileVersionTemplateDifferenceBubbleV1}); err != nil {
		t.Fatalf("draft difference profile should be accepted until the store binds the reference: %v", err)
	}
}
