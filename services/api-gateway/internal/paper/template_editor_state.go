package paper

import (
	"encoding/hex"
	"errors"
	"strings"
)

var ErrTemplateReviewRequired = errors.New("template regions still require review")

func validateTemplateRegionMetadata(region LayoutRegion) error {
	if region.Origin != "" && region.Origin != "model" && region.Origin != "local_detector" && region.Origin != "library" && region.Origin != "human" {
		return errors.New("region origin is invalid")
	}
	if region.ReviewState != "" && region.ReviewState != "suggested" && region.ReviewState != "edited" && region.ReviewState != "confirmed" && region.ReviewState != "discarded" {
		return errors.New("region review_state is invalid")
	}
	if region.SemanticCheck != "" && region.SemanticCheck != "supported" && region.SemanticCheck != "needs_review" && region.SemanticCheck != "not_evaluated" {
		return errors.New("region semantic_check is invalid")
	}
	if len(region.AnalysisRunID) > 128 || len(region.ReasonCodes) > 40 {
		return errors.New("region audit metadata exceeds limit")
	}
	for _, code := range region.ReasonCodes {
		if len(code) < 1 || len(code) > 96 {
			return errors.New("region reason_codes must contain bounded reason codes")
		}
		for _, value := range code {
			if !(value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_') {
				return errors.New("region reason_codes cannot contain free text")
			}
		}
	}
	return nil
}

func validateTemplateEditorState(state *TemplateEditorState, pageCount int) error {
	if state == nil {
		return nil
	}
	if state.Version != 1 || len(state.SourceSHA256) != 64 || state.Candidates == nil || len(state.Candidates) > maxTemplateRegions {
		return errors.New("editor_state version, source hash or candidate count is invalid")
	}
	if _, err := hex.DecodeString(state.SourceSHA256); err != nil {
		return errors.New("editor_state source_sha256 is invalid")
	}
	seen := map[string]bool{}
	for _, candidate := range state.Candidates {
		if strings.TrimSpace(candidate.ID) == "" || len(candidate.ID) > 128 || seen[candidate.ID] || candidate.PageNo < 1 || candidate.PageNo > pageCount || len(candidate.QuestionNo) > 128 {
			return errors.New("editor_state contains an invalid candidate reference")
		}
		seen[candidate.ID] = true
		switch candidate.Role {
		case "answer", "objective_group", "identity", "uncertain", "instruction", "other":
		default:
			return errors.New("editor_state candidate role is invalid")
		}
		if err := validateRegion(candidate.Region); err != nil {
			return err
		}
		if err := validateTemplateOptions(candidate.Region.OptionRegions); err != nil {
			return err
		}
	}
	return nil
}

func validateTemplateEditorSource(layout TemplateLayout, sourceSHA256 string) error {
	if layout.EditorState != nil && !strings.EqualFold(layout.EditorState.SourceSHA256, sourceSHA256) {
		return ErrConflict
	}
	return nil
}

// validateTemplateReadyToLock 在事务内执行；草稿允许存疑候选，锁定不能把它们悄悄变成正式题区。
// 缺少新审核字段的历史区域仍保留原语义；客户端的 confirmed 不能跳过正式题目、覆盖和空间冲突检查。
func validateTemplateReadyToLock(layout TemplateLayout, questions []Question) error {
	if err := validateTemplateGradingGroups(layout, questions); err != nil {
		return err
	}
	if err := validateTemplateReviewResolved(layout); err != nil {
		return err
	}
	covered := map[string]bool{}
	for _, page := range layout.Pages {
		for _, region := range page.QuestionRegions {
			covered[region.QuestionID] = true
		}
		for _, group := range page.GradingGroups {
			for _, id := range group.QuestionIDs {
				covered[id] = true
			}
		}
	}
	if !libraryRegionsDoNotConflict(layout) {
		return ErrTemplateReviewRequired
	}
	allowed := map[string]bool{}
	for _, question := range questions {
		if question.Status == "deleted" {
			continue
		}
		allowed[question.ID] = true
		if !covered[question.ID] {
			return ErrTemplateReviewRequired
		}
	}
	for id := range covered {
		if !allowed[id] {
			return ErrInvalidInput
		}
	}
	return nil
}

// validateTemplateReviewResolved 也用于启用与入库，避免历史已锁定记录绕过新审核门禁。
// 已通过语义检查并对应到正式布局的识别结果直接可用；人工纠错也不必再逐框确认。
// 已收录库的几何可以继承，不伪造本场人工确认；显式冲突和未关联候选仍须解决。
// 旧版无审核元数据的区域保持原规则。
func validateTemplateReviewResolved(layout TemplateLayout) error {
	if layout.EditorState != nil {
		for _, candidate := range layout.EditorState.Candidates {
			if candidate.Region.ReviewState != "discarded" {
				return ErrTemplateReviewRequired
			}
		}
	}
	for _, page := range layout.Pages {
		for _, group := range [][]LayoutRegion{page.RegistrationMarks, page.IdentityRegions, page.QuestionRegions} {
			for _, region := range group {
				if region.ReviewState == "confirmed" || region.ReviewState == "edited" {
					continue
				}
				// 兼容旧草稿或工作节点返回的矛盾字段；自动区域的明确反证必须先解决，不能只改 supported 就启用。
				if templateAutomaticRegionConflictReason(region.ReasonCodes) != "" {
					return ErrTemplateReviewRequired
				}
				if region.ReviewState == "suggested" && region.SemanticCheck == "supported" {
					continue
				}
				// 库实例化已校验学校、题号题型和布局；not_evaluated 只表示本场未重新识别，不要求逐框重审。
				if region.Origin == "library" && region.ReviewState == "suggested" && region.SemanticCheck == "not_evaluated" {
					continue
				}
				if region.Origin != "" || region.ReviewState != "" || region.SemanticCheck != "" || region.AnalysisRunID != "" || len(region.ReasonCodes) > 0 {
					return ErrTemplateReviewRequired
				}
			}
		}
	}
	return nil
}
