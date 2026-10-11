package paper

import (
	"errors"
	"strings"
)

const (
	// OMRProfileModeManualOnly 允许提取结果作为操作人员的建议，但阻止其成为自动成绩。
	// 原始填涂比例提取器无法区分所有印刷答题卡设计与手写标记。
	// 此模式始终需要人工确认；差分模式另由已批准的校准记录决定自动确认资格。
	OMRProfileModeManualOnly = "manual_only"
	// OMRProfileModeTemplateDifference 使用配置和配准答题卡时的不可变空白页。
	// 它先移除表单已有印刷内容，再测量标记；这仍是提取模式，不代表有权自动确认成绩。
	OMRProfileModeTemplateDifference = "template_difference"

	// OMRProfileVersionOpenCVFillV1 是保守的原始填涂建议配置。
	// 它不扣除具体答题卡设计的印刷内容，因此仍只允许人工确认。
	OMRProfileVersionOpenCVFillV1 = "opencv-fill-v1"

	// OMRProfileVersionTemplateDifferenceBubbleV1 的范围刻意受限：
	// 只支持已按锁定模板中冻结的准确参考资源完成配准的页面上的显式涂点区域。
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

	// OMRCalibrationMinimumSampleCount 采用保守值。
	// 这是部署门禁，并不表示一百个清晰样本就能证明模型普遍正确。
	OMRCalibrationMinimumSampleCount = 100
	// OMRCalibrationMinimumSamplesPerOption 防止偏斜样本批准其他涂点区域从未被检查过的题目。
	OMRCalibrationMinimumSamplesPerOption = 10
	// OMRCalibrationMinimumSamplesPerStratum 要求证据覆盖 worker 的高置信度、低置信度、空白和歧义分支。
	OMRCalibrationMinimumSamplesPerStratum = 10
	// OMRCalibrationMinimumConfidence 比历史通用 OMR 阈值更严格。
	// 校准授权只覆盖这个高置信度分支；其他结果仍交由人工复核。
	OMRCalibrationMinimumConfidence = 0.98
)

// TemplateOMRReference 是页面配准和背景扣除共同使用的不可变来源文档。
// 服务器根据选定试卷生成该值；存储前覆盖客户端提供的值。
type TemplateOMRReference struct {
	Source      string `json:"source"`
	FileAssetID string `json:"file_asset_id"`
	HashSHA256  string `json:"hash_sha256"`
	ContentType string `json:"content_type"`
}

// TemplateOMRProfile 是不可变答题卡布局的一部分。
// 它描述生成建议所用的提取契约，不是操作人员可控制的自动成绩写入授权。
type TemplateOMRProfile struct {
	Mode      string                `json:"mode,omitempty"`
	Version   string                `json:"version,omitempty"`
	Reference *TemplateOMRReference `json:"reference,omitempty"`
}

// OMRReferenceCrop 指定 worker 执行模板差分提取必须使用的准确参考页和归一化裁剪区域。
// 它由锁定布局生成，不接受 worker 或客户端回调提供的值。
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

// OMRCalibrationApproval 是评分存储必须从单独批准的校准会话中加载的不可变作用域。
// 它只是值对象：模板布局、worker 载荷和浏览器不能自行构造它来取得自动确认权限。
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

// OMRRuntimeProfile 是准确、可序列化的 worker 配置。
// 每次 OMR 运行都保存其哈希快照，worker 结果必须原样返回该哈希。
type OMRRuntimeProfile struct {
	Mode               string  `json:"mode"`
	Version            string  `json:"version"`
	MarkedThreshold    float64 `json:"marked_threshold"`
	AmbiguousThreshold float64 `json:"ambiguous_threshold"`
	MinimumMargin      float64 `json:"minimum_margin"`
	BorderFraction     float64 `json:"border_fraction"`
	// ReferenceMaskDilationPixels 允许 worker 去除印刷模板墨迹周围因配准产生的少量抗锯齿晕边。
	// 原始配置中该值为零，因此不计入其历史哈希。
	ReferenceMaskDilationPixels int `json:"reference_mask_dilation_pixels,omitempty"`
}

// OMRAutoConfirmPolicy 是与已入队 OMR 运行关联的服务器端决策快照。
// worker 接收运行时配置，但不能自行授予资格标记。
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

// NormalizeTemplateLayout 在模板计算哈希和存储前，将省略的配置变为显式、不可变的仅人工确认契约。
// 此处不改写已有锁定模板；OMRAutoConfirmPolicyForTemplate 读取它们时仍保守地视为未核验。
func NormalizeTemplateLayout(layout TemplateLayout) TemplateLayout {
	if isEmptyTemplateOMRProfile(layout.OMRProfile) {
		layout.OMRProfile = DefaultTemplateOMRProfile()
	}
	return layout
}

// BindTemplateOMRReference 将全部客户端提供的参考替换为服务器解析出的资源元数据。
// 仅人工提取不能保留潜藏的参考，以免以后被误认为已批准契约。
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
		return nil // 旧版输入在存储前先规范化。
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
		// 草稿可在存储生成可信参考之前请求此模式。下方的锁定和任务创建流程则要求已有该参考。
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

// OMRAutoConfirmPolicyForTemplate 解析唯一可允许自动确认的授权来源。
// 结果采取保守拒绝：原始 OpenCV 配置只提供可审计建议，绝不作为自动评分路径。
// 此兼容入口没有校准记录，因此不授权自动确认；生产调用需使用带 Calibration 的入口。
func OMRAutoConfirmPolicyForTemplate(layout TemplateLayout, templateStatus, templateContentHash, segmentContentHash string) OMRAutoConfirmPolicy {
	return OMRAutoConfirmPolicyForTemplateReference(layout, templateStatus, templateContentHash, segmentContentHash, TemplateOMRReference{}, "")
}

// OMRAutoConfirmPolicyForTemplateReference 同时解析 worker 提取契约和服务器端成绩权限。
// 当前试卷资源元数据单独传入，避免过时或伪造的布局参考诱使 worker 扣除其他文档。
func OMRAutoConfirmPolicyForTemplateReference(layout TemplateLayout, templateStatus, templateContentHash, segmentContentHash string, currentReference TemplateOMRReference, questionID string) OMRAutoConfirmPolicy {
	// 这个兼容辅助函数没有模板 ID，因此只能返回未经校准的策略。
	// 生产调用方必须使用模板带持久化 ID 且支持校准的函数。
	template := AnswerSheetTemplate{Layout: layout, Status: templateStatus, ContentHash: templateContentHash}
	return OMRAutoConfirmPolicyForTemplateReferenceAndCalibration(template, segmentContentHash, currentReference, questionID, nil)
}

// OMRAutoConfirmPolicyForTemplateReferenceAndCalibration 在一次保守拒绝决策中解析模板提取配置与单独持久化的校准批准。
// 批准必须匹配所有可能改变标记识别结果的不可变输入。
// 调用方必须从数据库加载已批准记录；此函数校验匹配范围，不鉴别对象是否来自可信存储。
func OMRAutoConfirmPolicyForTemplateReferenceAndCalibration(template AnswerSheetTemplate, segmentContentHash string, currentReference TemplateOMRReference, questionID string, calibration *OMRCalibrationApproval) OMRAutoConfirmPolicy {
	runtimeProfile := defaultOMRRuntimeProfile()
	policy := OMRAutoConfirmPolicy{
		RuntimeProfile:    runtimeProfile,
		ProfileHash:       DefaultOMRRuntimeProfileHash(),
		Reason:            OMRAutoConfirmReasonTemplateProfileUnverified,
		MinimumConfidence: 0.9,
	}

	if strings.TrimSpace(template.Status) == "" {
		policy.Reason = OMRAutoConfirmReasonTemplateNotFound
		return policy
	}
	if template.Status != "locked" {
		policy.Reason = OMRAutoConfirmReasonTemplateNotLocked
		return policy
	}
	if strings.TrimSpace(template.ContentHash) == "" || template.ContentHash != segmentContentHash {
		policy.Reason = OMRAutoConfirmReasonTemplateContentHashMismatch
		return policy
	}
	if isEmptyTemplateOMRProfile(template.Layout.OMRProfile) {
		return policy
	}
	if err := ValidateTemplateOMRProfile(template.Layout.OMRProfile); err != nil {
		policy.Reason = OMRAutoConfirmReasonUnsupportedProfile
		return policy
	}

	if template.Layout.OMRProfile.Mode == OMRProfileModeManualOnly {
		// 显式的仅人工确认配置比省略的旧配置提供更多信息，但仍不具备自动确认资格。
		policy.Reason = OMRAutoConfirmReasonManualOnlyProfile
		return policy
	}

	if template.Layout.OMRProfile.Reference == nil {
		policy.Reason = OMRAutoConfirmReasonTemplateReferenceUnverified
		return policy
	}
	if !sameTemplateOMRReference(*template.Layout.OMRProfile.Reference, currentReference) {
		policy.Reason = OMRAutoConfirmReasonTemplateReferenceMismatch
		return policy
	}
	reference, ok := referenceCropForQuestion(template.Layout, questionID)
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
		// 差分提取可立即用于提供可审计建议，但校准批准属于单独的生产控制。
		// 不能让操作人员选择的配置绕过这项控制。
		policy.Reason = OMRAutoConfirmReasonCalibrationUnapproved
		return policy
	}
	if !calibrationMatchesTemplateDifference(*calibration, template.ID, template.ContentHash, questionID, policy.ProfileHash, currentReference) {
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
