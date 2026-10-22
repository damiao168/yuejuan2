package paper

import "strings"

// defendTemplatePageSemantics 只撤回不可信的类别和题号，不删掉用户仍可核对的框。
// supported 代表通过自动使用校验，不表示已由 OCR 独立证实或人工确认；旧工作节点缺字段时按未核验处理。
func defendTemplatePageSemantics(result *TemplatePageAnalysis, labels templateAnalysisQuestionLabels) {
	all, objective, counts := map[string]bool{}, map[string]bool{}, map[string]int{}
	for _, label := range labels.All {
		all[label] = true
	}
	for _, label := range labels.Objective {
		objective[label] = true
	}
	for _, row := range result.Candidates {
		if row.Role == "answer" && row.PrintedQuestionLabel != nil && all[*row.PrintedQuestionLabel] {
			counts[*row.PrintedQuestionLabel]++
		}
	}
	objectiveCounts := templateObjectiveItemCounts(result.Candidates)
	objectiveConflicts := templateObjectiveConflictingGroups(result.Candidates)
	for index := range result.Candidates {
		row := &result.Candidates[index]
		problem := ""
		if row.SemanticCheck == "" {
			row.SemanticCheck = "not_evaluated"
		}
		label := ""
		if row.PrintedQuestionLabel != nil {
			label = *row.PrintedQuestionLabel
		}
		switch row.Role {
		case "objective_group":
			if len(objective) == 0 || label != "" && (!all[label] || !objective[label]) {
				problem = "SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT"
			} else if label != "" {
				// 大区通过子项题号对应正式题目，外层不关联单独一道题。
				problem = "SEMANTIC_ASSOCIATION_CONFLICT"
			} else if row.IdentityLabel != nil {
				problem = "SEMANTIC_ROLE_IDENTITY_CONFLICT"
			} else if row.AssociationStatus != "unassociated" {
				problem = "SEMANTIC_ASSOCIATION_CONFLICT"
			}
		case "identity":
			if label != "" {
				problem = "SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT"
			} else if row.IdentityLabel != nil && !templateStaticIdentityLabel(*row.IdentityLabel) {
				problem = "SEMANTIC_ROLE_IDENTITY_CONFLICT"
			} else if row.AssociationStatus != "unassociated" {
				problem = "SEMANTIC_ASSOCIATION_CONFLICT"
			}
		case "answer":
			if row.IdentityLabel != nil {
				problem = "SEMANTIC_ROLE_IDENTITY_CONFLICT"
			} else if label != "" && objective[label] {
				problem = "OBJECTIVE_QUESTION_REQUIRES_GROUP"
			} else if label != "" && !all[label] {
				problem = "SEMANTIC_ASSOCIATION_CONFLICT"
			} else if counts[label] > 1 {
				// 当前正式切题协议一题一个 ROI；多块不能静默只留第一块，更不能合成跨题外接框。
				problem = "SEMANTIC_MULTIPART_UNSUPPORTED"
			}
		}
		if problem == "" && (row.Role == "identity" || row.Role == "objective_group" || row.Role == "answer") {
			// 原因码可能仍保留清理前的反证；单独的 supported 不能让禁区或矛盾类别重新进入自动对应。
			problem = templateAutomaticRegionConflictReason(row.ReasonCodes)
		}
		if problem == "" && (row.Role == "identity" || row.Role == "objective_group" || row.Role == "answer") && (result.SemanticStatus != "completed" || row.SemanticCheck != "supported") {
			problem = "SEMANTIC_EVIDENCE_INSUFFICIENT"
		}
		if problem == "" && len(row.ObjectiveItems) > 0 && (row.Role != "objective_group" || objectiveConflicts[index] || !templateObjectiveItemsValid(*row, objective, objectiveCounts)) {
			problem = "SEMANTIC_OBJECTIVE_ITEMS_CONFLICT"
		}
		if problem != "" {
			row.Role, row.AssociationStatus, row.SemanticCheck = "uncertain", "unassociated", "needs_review"
			// 不把冲突子框交给编辑器自动对应；候选原因码保留，原始响应按受控诊断配置留存。
			row.ObjectiveItems = nil
			// 只保留正式题号用于解释冲突；填写值和不在白名单内的文本不能进入模板建议。
			if !all[label] {
				row.PrintedQuestionLabel = nil
			}
			row.IdentityLabel = nil
			row.ReasonCodes = appendTemplateReason(row.ReasonCodes, problem)
		}
	}
}

// templateAutomaticRegionConflictReason 仅用于将成为正式布局的自动区域。
// 已核实的独立禁区在说明候选中是正常结果，但不能重新作为答题或身份区；OCR 不可用等诊断不属于反证。
func templateAutomaticRegionConflictReason(codes []string) string {
	for _, code := range codes {
		switch code {
		case "CONTENT_FORBIDDEN_WRITING", "SEMANTIC_FORBIDDEN_AREA_DETECTED", "LOCAL_FORBIDDEN_LABEL_VERIFIED", "MULTIMODAL_FORBIDDEN_REGION_EXCLUDED":
			return "SEMANTIC_FORBIDDEN_AREA_DETECTED"
		case "REGION_CONFLICT", "SEMANTIC_MULTIPART_UNSUPPORTED", "MODEL_OBS_MULTIPLE_QUESTION_LABELS", "OBJECTIVE_QUESTION_REQUIRES_GROUP":
			return code
		}
		if (strings.HasPrefix(code, "SEMANTIC_") || strings.HasPrefix(code, "MODEL_OBS_")) && strings.HasSuffix(code, "CONFLICT") {
			return code
		}
	}
	return ""
}

func appendTemplateReason(codes []string, code string) []string {
	for _, existing := range codes {
		if existing == code {
			return codes
		}
	}
	return append(codes, code)
}

func templateStaticIdentityLabel(label string) bool {
	switch label {
	case "姓名", "学号", "准考证号", "考号", "班级", "学校", "条码", "二维码", "身份信息":
		return true
	}
	return false
}
