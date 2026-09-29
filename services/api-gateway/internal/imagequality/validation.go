package imagequality

import "strings"

func normalizeProfile(profile Profile) Profile {
	if strings.TrimSpace(profile.Name) == "" {
		return DefaultProfile()
	}
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Version = strings.TrimSpace(profile.Version)
	profile.ConfigHash = strings.TrimSpace(profile.ConfigHash)
	profile.MetricSchemaVersion = strings.TrimSpace(profile.MetricSchemaVersion)
	profile.ReportSchemaVersion = strings.TrimSpace(profile.ReportSchemaVersion)
	if profile.Version == "" {
		profile.Version = "v2"
	}
	if profile.ConfigHash == "" {
		profile.ConfigHash = "sha256:" + profile.Name + "-" + profile.Version
	}
	if profile.MetricSchemaVersion == "" {
		profile.MetricSchemaVersion = "image-quality-metrics-v2"
	}
	if profile.ReportSchemaVersion == "" {
		profile.ReportSchemaVersion = "image-quality-report-v2"
	}
	return profile
}

func validateCreateRunsInput(input CreateRunsInput) error {
	if strings.TrimSpace(input.SubmissionID) == "" || len(input.Pages) == 0 {
		return ErrInvalidInput
	}
	for _, page := range input.Pages {
		if strings.TrimSpace(page.SubmissionPageID) == "" ||
			strings.TrimSpace(page.SourceFileAssetID) == "" ||
			strings.TrimSpace(page.SourceSHA256) == "" ||
			page.PageNo <= 0 {
			return ErrInvalidInput
		}
	}
	return nil
}

func normalizeClaimInput(input ClaimInput) ClaimInput {
	input.WorkerInstanceID = strings.TrimSpace(input.WorkerInstanceID)
	if input.Limit <= 0 || input.Limit > 100 {
		input.Limit = 10
	}
	if input.LeaseSeconds <= 0 {
		input.LeaseSeconds = 300
	}
	return input
}

// 处理完成且质量通过时必须带标准化文件；待复核和质量不通过的结果不作此要求。
func validateResultInput(input ResultInput) error {
	if strings.TrimSpace(input.LeaseToken) == "" || input.AttemptNo <= 0 || strings.TrimSpace(input.ResultVersion) == "" {
		return ErrInvalidInput
	}
	switch input.ProcessingStatus {
	case ProcessingCompleted, ProcessingRetryableError, ProcessingTerminalError:
	default:
		return ErrInvalidInput
	}
	if input.ProcessingStatus == ProcessingCompleted {
		switch input.QualityStatus {
		case QualityPassed, QualityReview, QualityFailed:
		default:
			return ErrInvalidInput
		}
		if input.QualityStatus == QualityPassed && strings.TrimSpace(input.NormalizedFileAssetID) == "" {
			return ErrInvalidInput
		}
	}
	return nil
}
