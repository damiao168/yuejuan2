package paper

import (
	"errors"
	"strings"
)

const (
	// OMRProfileModeManualOnly deliberately permits extraction as an operator
	// suggestion while preventing it from becoming an automatic grade. The raw
	// fill-ratio extractor cannot distinguish every printed answer-sheet design
	// from a handwritten mark.
	// 此模式始终需要人工确认；差分模式另由已批准的校准记录决定自动确认资格。
	OMRProfileModeManualOnly = "manual_only"
	// OMRProfileModeTemplateDifference uses the immutable blank page that was
	// used to configure and register the answer sheet. It removes printing
	// already present on the form before measuring a mark. This is still an
	// extraction mode, not permission to auto-confirm a grade.
	OMRProfileModeTemplateDifference = "template_difference"

	// OMRProfileVersionOpenCVFillV1 is the conservative raw-fill suggestion
	// profile. It remains manual-only because it does not subtract the printed
	// content of a concrete answer-sheet design.
	OMRProfileVersionOpenCVFillV1 = "opencv-fill-v1"

	// OMRProfileVersionTemplateDifferenceBubbleV1 is intentionally narrow: it
	// only supports explicit bubble regions on a page that has been registered
	// against the exact reference asset frozen in the locked template.
	OMRProfileVersionTemplateDifferenceBubbleV1 = "opencv-template-difference-bubble-v1"

	OMRReferenceSourceExamPaper = "exam_paper"

	OMRAutoConfirmReasonTemplateNotFound            = "template_not_found"
	OMRAutoConfirmReasonTemplateNotLocked           = "template_not_locked"
	OMRAutoConfirmReasonTemplateContentHashMismatch = "template_content_hash_mismatch"
	OMRAutoConfirmReasonTemplateProfileUnverified   = "template_profile_unverified"
	OMRAutoConfirmReasonTemplateReferenceUnverified = "template_reference_unverified"
	OMRAutoConfirmReasonTemplateReferenceMismatch   = "template_reference_mismatch"
	OMRAutoConfirmReasonTemplateReferenceRegion     = "template_reference_region_missing"
	OMRAutoConfirmReasonManualOnlyProfile           = "manual_only_profile"
	OMRAutoConfirmReasonCalibrationUnapproved       = "template_difference_calibration_unapproved"
	OMRAutoConfirmReasonCalibrationScopeMismatch    = "template_difference_calibration_scope_mismatch"
	OMRAutoConfirmReasonCalibrationApproved         = "template_difference_calibration_approved"
	OMRAutoConfirmReasonCalibrationRevoked          = "template_difference_calibration_revoked"
	OMRAutoConfirmReasonUnsupportedProfile          = "unsupported_omr_profile"

	// OMRCalibrationMinimumSampleCount is deliberately conservative. It is a
	// deployment gate, not a claim that one hundred clean examples prove a
	// model is universally correct.
	OMRCalibrationMinimumSampleCount = 100
	// OMRCalibrationMinimumSamplesPerOption prevents a skewed sample from
	// approving a question whose other bubble regions were never examined.
	OMRCalibrationMinimumSamplesPerOption = 10
	// OMRCalibrationMinimumSamplesPerStratum forces evidence to cover the
	// worker's high-confidence, low-confidence, blank, and ambiguous branches.
	OMRCalibrationMinimumSamplesPerStratum = 10
	// OMRCalibrationMinimumConfidence is stricter than the historical generic
	// OMR threshold. A calibrated permission only covers this high-confidence
	// branch; all other outcomes continue through human review.
	OMRCalibrationMinimumConfidence = 0.98
)

// TemplateOMRReference is the immutable source document used both for page
// registration and for background subtraction. It is server-derived from the
// selected exam paper; a client-supplied value is overwritten before storage.
type TemplateOMRReference struct {
	Source      string `json:"source"`
	FileAssetID string `json:"file_asset_id"`
	HashSHA256  string `json:"hash_sha256"`
	ContentType string `json:"content_type"`
}

// TemplateOMRProfile is part of the immutable answer-sheet layout. It
// describes which extraction contract generated a suggestion; it is not an
// operator-controlled permission to write automatic grades.
type TemplateOMRProfile struct {
	Mode      string                `json:"mode,omitempty"`
	Version   string                `json:"version,omitempty"`
	Reference *TemplateOMRReference `json:"reference,omitempty"`
}

// OMRReferenceCrop is the exact reference page and normalized crop that a
// worker must use for a template-difference extraction. It is derived from
// the locked layout, not accepted from a worker or client callback.
type OMRReferenceCrop struct {
	FileAssetID string  `json:"file_asset_id"`
	HashSHA256  string  `json:"hash_sha256"`
	ContentType string  `json:"content_type"`
	PageNo      int     `json:"page_no"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Width       float64 `json:"width"`
	Height      float64 `json:"height"`
}

// OMRCalibrationApproval is the immutable scope a grading store must load
// from a separately approved calibration session. It is intentionally a
// value object: the template layout, worker payload, and browser cannot grant
// automatic-confirmation permission by constructing one themselves.
type OMRCalibrationApproval struct {
	ID                   string  `json:"id"`
	TemplateID           string  `json:"template_id"`
	TemplateContentHash  string  `json:"template_content_hash"`
	ScopeType            string  `json:"scope_type"`
	QuestionID           string  `json:"question_id,omitempty"`
	ProfileVersion       string  `json:"profile_version"`
	ProfileHash          string  `json:"profile_hash"`
	ReferenceFileAssetID string  `json:"reference_file_asset_id"`
	ReferenceSHA256      string  `json:"reference_sha256"`
	MinimumConfidence    float64 `json:"minimum_confidence"`
	EvidenceHash         string  `json:"evidence_hash"`
	Status               string  `json:"status"`
}

// OMRRuntimeProfile is the exact, serializable worker configuration. Its hash
// is snapshotted with every OMR run and must be echoed by the worker result.
type OMRRuntimeProfile struct {
	Mode               string  `json:"mode"`
	Version            string  `json:"version"`
	MarkedThreshold    float64 `json:"marked_threshold"`
	AmbiguousThreshold float64 `json:"ambiguous_threshold"`
	MinimumMargin      float64 `json:"minimum_margin"`
	BorderFraction     float64 `json:"border_fraction"`
	// ReferenceMaskDilationPixels allows the worker to suppress a small
	// registration anti-aliasing halo around printed template ink. It is zero
	// for the raw profile and therefore omitted from its historical hash.
	ReferenceMaskDilationPixels int `json:"reference_mask_dilation_pixels,omitempty"`
}

// OMRAutoConfirmPolicy is the server-side decision snapshot associated with a
// queued OMR run. Workers receive the runtime profile but cannot grant the
// eligibility flag themselves.
type OMRAutoConfirmPolicy struct {
	RuntimeProfile      OMRRuntimeProfile
	ProfileHash         string
	AutoConfirmEligible bool
	Reason              string
	Reference           *OMRReferenceCrop
	MinimumConfidence   float64
	Calibration         *OMRCalibrationApproval
}

func DefaultTemplateOMRProfile() TemplateOMRProfile {
	return TemplateOMRProfile{Mode: OMRProfileModeManualOnly, Version: OMRProfileVersionOpenCVFillV1}
}

func defaultOMRRuntimeProfile() OMRRuntimeProfile {
	return OMRRuntimeProfile{
		Mode:               OMRProfileModeManualOnly,
		Version:            OMRProfileVersionOpenCVFillV1,
		MarkedThreshold:    0.18,
		AmbiguousThreshold: 0.10,
		MinimumMargin:      0.06,
		BorderFraction:     0.12,
	}
}

func DefaultOMRRuntimeProfileHash() string {
	return OMRRuntimeProfileHash(defaultOMRRuntimeProfile())
}

func OMRRuntimeProfileHash(profile OMRRuntimeProfile) string {
	return "sha256:" + stableContentHash(profile)
}

// NormalizeTemplateLayout turns an omitted profile into an explicit immutable
// manual-only contract before the template is hashed and stored. Existing
// locked templates are not rewritten here; they remain safely unverified when
// read by OMRAutoConfirmPolicyForTemplate.
func NormalizeTemplateLayout(layout TemplateLayout) TemplateLayout {
	if isEmptyTemplateOMRProfile(layout.OMRProfile) {
		layout.OMRProfile = DefaultTemplateOMRProfile()
	}
	return layout
}

// BindTemplateOMRReference replaces every client-supplied reference with the
// asset metadata resolved by the server. Manual-only extraction must not carry
// a dormant reference that could later be mistaken for an approved contract.
func BindTemplateOMRReference(layout TemplateLayout, reference TemplateOMRReference) TemplateLayout {
	if layout.OMRProfile.Mode == OMRProfileModeTemplateDifference {
		referenceCopy := reference
		layout.OMRProfile.Reference = &referenceCopy
		return layout
	}
	layout.OMRProfile.Reference = nil
	return layout
}

func ValidateTemplateOMRProfile(profile TemplateOMRProfile) error {
	if isEmptyTemplateOMRProfile(profile) {
		return nil // Legacy input is normalized before storage.
	}
	mode := strings.TrimSpace(profile.Mode)
	version := strings.TrimSpace(profile.Version)
	switch mode {
	case OMRProfileModeManualOnly:
		if version != OMRProfileVersionOpenCVFillV1 {
			return errors.New("OMR profile version is not supported")
		}
		if profile.Reference != nil {
			return errors.New("manual-only OMR profile must not include a reference asset")
		}
	case OMRProfileModeTemplateDifference:
		if version != OMRProfileVersionTemplateDifferenceBubbleV1 {
			return errors.New("OMR profile version is not supported")
		}
		// A draft may request this mode before the store has materialized the
		// trusted reference. Locking and task creation require it below.
		if profile.Reference != nil {
			if err := ValidateTemplateOMRReference(*profile.Reference); err != nil {
				return err
			}
		}
	default:
		return errors.New("OMR profile mode is not supported")
	}
	return nil
}

func ValidateTemplateOMRReference(reference TemplateOMRReference) error {
	if strings.TrimSpace(reference.Source) != OMRReferenceSourceExamPaper {
		return errors.New("OMR reference source is not supported")
	}
	if strings.TrimSpace(reference.FileAssetID) == "" || strings.TrimSpace(reference.HashSHA256) == "" {
		return errors.New("OMR reference asset and hash are required")
	}
	switch strings.ToLower(strings.TrimSpace(strings.Split(reference.ContentType, ";")[0])) {
	case "application/pdf", "image/png", "image/jpeg", "image/tiff":
		return nil
	default:
		return errors.New("OMR reference content type is not supported")
	}
}

// OMRAutoConfirmPolicyForTemplate resolves the only authority that may allow
// automatic confirmation. The result is intentionally fail-closed: the raw
// OpenCV profile is an auditable suggestion path, never an auto-grade path.
// 此兼容入口没有校准记录，因此不授权自动确认；生产调用需使用带 Calibration 的入口。
func OMRAutoConfirmPolicyForTemplate(layout TemplateLayout, templateStatus, templateContentHash, segmentContentHash string) OMRAutoConfirmPolicy {
	return OMRAutoConfirmPolicyForTemplateReference(layout, templateStatus, templateContentHash, segmentContentHash, TemplateOMRReference{}, "")
}

// OMRAutoConfirmPolicyForTemplateReference resolves the worker extraction
// contract and its server-side grade permission together. It takes the
// current paper asset metadata separately so a stale or forged layout
// reference cannot trick a worker into subtracting a different document.
func OMRAutoConfirmPolicyForTemplateReference(layout TemplateLayout, templateStatus, templateContentHash, segmentContentHash string, currentReference TemplateOMRReference, questionID string) OMRAutoConfirmPolicy {
	// This compatibility helper deliberately has no template ID, so it can only
	// ever return an uncalibrated policy. Production callers must use the
	// calibration-aware function with the persisted template ID.
	return OMRAutoConfirmPolicyForTemplateReferenceAndCalibration(layout, templateStatus, templateContentHash, segmentContentHash, currentReference, questionID, "", nil)
}

// OMRAutoConfirmPolicyForTemplateReferenceAndCalibration resolves template
// extraction and a separately persisted calibration approval in one
// fail-closed decision. The approval must match every immutable input that can
// change mark recognition.
// 调用方必须从数据库加载已批准记录；此函数校验匹配范围，不鉴别对象是否来自可信存储。
func OMRAutoConfirmPolicyForTemplateReferenceAndCalibration(layout TemplateLayout, templateStatus, templateContentHash, segmentContentHash string, currentReference TemplateOMRReference, questionID, templateID string, calibration *OMRCalibrationApproval) OMRAutoConfirmPolicy {
	runtimeProfile := defaultOMRRuntimeProfile()
	policy := OMRAutoConfirmPolicy{
		RuntimeProfile:    runtimeProfile,
		ProfileHash:       DefaultOMRRuntimeProfileHash(),
		Reason:            OMRAutoConfirmReasonTemplateProfileUnverified,
		MinimumConfidence: 0.9,
	}

	if strings.TrimSpace(templateStatus) == "" {
		policy.Reason = OMRAutoConfirmReasonTemplateNotFound
		return policy
	}
	if templateStatus != "locked" {
		policy.Reason = OMRAutoConfirmReasonTemplateNotLocked
		return policy
	}
	if strings.TrimSpace(templateContentHash) == "" || templateContentHash != segmentContentHash {
		policy.Reason = OMRAutoConfirmReasonTemplateContentHashMismatch
		return policy
	}
	if isEmptyTemplateOMRProfile(layout.OMRProfile) {
		return policy
	}
	if err := ValidateTemplateOMRProfile(layout.OMRProfile); err != nil {
		policy.Reason = OMRAutoConfirmReasonUnsupportedProfile
		return policy
	}

	if layout.OMRProfile.Mode == OMRProfileModeManualOnly {
		// An explicit manual-only profile is more informative than an omitted
		// legacy profile, but is still deliberately ineligible for auto-confirm.
		policy.Reason = OMRAutoConfirmReasonManualOnlyProfile
		return policy
	}

	if layout.OMRProfile.Reference == nil {
		policy.Reason = OMRAutoConfirmReasonTemplateReferenceUnverified
		return policy
	}
	if !sameTemplateOMRReference(*layout.OMRProfile.Reference, currentReference) {
		policy.Reason = OMRAutoConfirmReasonTemplateReferenceMismatch
		return policy
	}
	reference, ok := referenceCropForQuestion(layout, questionID)
	if !ok {
		policy.Reason = OMRAutoConfirmReasonTemplateReferenceRegion
		return policy
	}
	policy.RuntimeProfile = templateDifferenceRuntimeProfile()
	policy.ProfileHash = OMRRuntimeProfileHash(policy.RuntimeProfile)
	policy.Reference = &OMRReferenceCrop{
		FileAssetID: currentReference.FileAssetID,
		HashSHA256:  currentReference.HashSHA256,
		ContentType: currentReference.ContentType,
		PageNo:      reference.PageNo,
		X:           reference.X,
		Y:           reference.Y,
		Width:       reference.Width,
		Height:      reference.Height,
	}
	if calibration == nil {
		// Difference extraction is useful immediately as an auditable suggestion,
		// but calibration approval is a separate production control. Do not let an
		// operator-selected profile bypass that control.
		policy.Reason = OMRAutoConfirmReasonCalibrationUnapproved
		return policy
	}
	if !calibrationMatchesTemplateDifference(*calibration, templateID, templateContentHash, questionID, policy.ProfileHash, currentReference) {
		policy.Reason = OMRAutoConfirmReasonCalibrationScopeMismatch
		return policy
	}
	calibrationCopy := *calibration
	policy.Calibration = &calibrationCopy
	policy.MinimumConfidence = calibration.MinimumConfidence
	policy.AutoConfirmEligible = true
	policy.Reason = OMRAutoConfirmReasonCalibrationApproved
	return policy
}

func calibrationMatchesTemplateDifference(calibration OMRCalibrationApproval, templateID, templateContentHash, questionID, profileHash string, reference TemplateOMRReference) bool {
	return calibration.Status == "approved" &&
		strings.TrimSpace(calibration.ID) != "" &&
		strings.TrimSpace(templateID) != "" && calibration.TemplateID == templateID &&
		strings.TrimSpace(calibration.TemplateContentHash) != "" && calibration.TemplateContentHash == templateContentHash &&
		(calibration.ScopeType == "template" || ((calibration.ScopeType == "" || calibration.ScopeType == "question") && strings.TrimSpace(calibration.QuestionID) != "" && calibration.QuestionID == questionID)) &&
		calibration.ProfileVersion == OMRProfileVersionTemplateDifferenceBubbleV1 &&
		strings.TrimSpace(calibration.ProfileHash) != "" && calibration.ProfileHash == profileHash &&
		strings.TrimSpace(calibration.ReferenceFileAssetID) != "" && calibration.ReferenceFileAssetID == reference.FileAssetID &&
		strings.TrimSpace(calibration.ReferenceSHA256) != "" && calibration.ReferenceSHA256 == reference.HashSHA256 &&
		calibration.MinimumConfidence >= OMRCalibrationMinimumConfidence && calibration.MinimumConfidence <= 1 &&
		strings.TrimSpace(calibration.EvidenceHash) != ""
}

func isEmptyTemplateOMRProfile(profile TemplateOMRProfile) bool {
	return strings.TrimSpace(profile.Mode) == "" && strings.TrimSpace(profile.Version) == "" && profile.Reference == nil
}

func templateDifferenceRuntimeProfile() OMRRuntimeProfile {
	return OMRRuntimeProfile{
		Mode:                        OMRProfileModeTemplateDifference,
		Version:                     OMRProfileVersionTemplateDifferenceBubbleV1,
		MarkedThreshold:             0.12,
		AmbiguousThreshold:          0.04,
		MinimumMargin:               0.04,
		BorderFraction:              0.12,
		ReferenceMaskDilationPixels: 1,
	}
}

type templateReferenceRegion struct {
	PageNo int
	X      float64
	Y      float64
	Width  float64
	Height float64
}

func referenceCropForQuestion(layout TemplateLayout, questionID string) (templateReferenceRegion, bool) {
	if strings.TrimSpace(questionID) == "" {
		return templateReferenceRegion{}, false
	}
	for _, page := range layout.Pages {
		for _, region := range page.QuestionRegions {
			if region.QuestionID == questionID {
				return templateReferenceRegion{PageNo: page.PageNo, X: region.X, Y: region.Y, Width: region.Width, Height: region.Height}, true
			}
		}
	}
	return templateReferenceRegion{}, false
}

func sameTemplateOMRReference(left, right TemplateOMRReference) bool {
	return strings.TrimSpace(left.Source) == OMRReferenceSourceExamPaper &&
		strings.TrimSpace(right.Source) == OMRReferenceSourceExamPaper &&
		strings.TrimSpace(left.FileAssetID) != "" && left.FileAssetID == right.FileAssetID &&
		strings.TrimSpace(left.HashSHA256) != "" && left.HashSHA256 == right.HashSHA256 &&
		strings.EqualFold(strings.TrimSpace(left.ContentType), strings.TrimSpace(right.ContentType))
}
