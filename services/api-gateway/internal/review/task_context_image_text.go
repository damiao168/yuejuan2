package review

import (
	"encoding/json"
	"strings"
)

const imageObservationSource = "image_observation"

type taskImageTextObservation struct {
	SchemaVersion  string `json:"schema_version"`
	InputVersionID string `json:"input_version_id"`
	CropSHA256     string `json:"crop_sha256"`
	Members        []struct {
		QuestionID  string `json:"question_id"`
		Presence    string `json:"presence"`
		Association string `json:"association"`
		Answer      *struct {
			Value string   `json:"value"`
			Parts []string `json:"parts"`
		} `json:"answer"`
	} `json:"members"`
}

type taskImageTextOutput struct {
	SchemaVersion        string                    `json:"schema_version"`
	InputVersionID       string                    `json:"input_version_id"`
	AssessmentSnapshotID string                    `json:"assessment_snapshot_id"`
	Observation          *taskImageTextObservation `json:"observation"`
	PanelExecution       json.RawMessage           `json:"panel_execution"`
}

// 调用方先筛选当前答案、快照和量规；数据库查询还排除取消计划和已替换的图像输入。
// 此处再核对输入与裁图绑定，只投影本题作答，不从历史候选、评分理由或参考答案拼接文字。
func taskImageStudentText(suggestion map[string]any, value Context) string {
	var saved struct {
		Status       string `json:"status"`
		InputBinding struct {
			InputMode            string `json:"input_mode"`
			InputProtocol        string `json:"input_protocol"`
			InputVersionID       string `json:"input_version_id"`
			CropSHA256           string `json:"crop_sha256"`
			AssessmentSnapshotID string `json:"assessment_snapshot_id"`
			AnswerSegmentID      string `json:"answer_segment_id"`
			QuestionID           string `json:"question_id"`
		} `json:"input_binding"`
		RawOutput taskImageTextOutput `json:"raw_output"`
	}
	raw, err := json.Marshal(suggestion)
	if err != nil || json.Unmarshal(raw, &saved) != nil {
		return ""
	}
	binding, output := saved.InputBinding, saved.RawOutput
	if saved.Status != "succeeded" || binding.InputMode != "image" || binding.InputProtocol != "grading-image-v3" ||
		binding.InputVersionID != value.AnswerVersion || binding.CropSHA256 == "" ||
		binding.AssessmentSnapshotID != value.AssessmentSnapshot.ID || binding.AnswerSegmentID != value.AnswerSegmentID || binding.QuestionID != value.Question.ID ||
		output.SchemaVersion != "grading-image-v3" || output.InputVersionID != binding.InputVersionID || output.AssessmentSnapshotID != binding.AssessmentSnapshotID {
		return ""
	}
	if len(output.PanelExecution) == 0 {
		return taskImageObservationText(output.Observation, binding.InputVersionID, binding.CropSHA256, binding.QuestionID)
	}
	var panel struct {
		Roles []struct {
			Role      string              `json:"role"`
			Status    string              `json:"status"`
			RawOutput taskImageTextOutput `json:"raw_output"`
		} `json:"role_results"`
	}
	if json.Unmarshal(output.PanelExecution, &panel) != nil {
		return ""
	}
	labels := map[string]string{"single": "评分模型", "primary_a": "评分 A", "primary_b": "评分 B", "arbiter": "第三评分"}
	texts := []string{}
	seen := map[string]bool{}
	// 多模型分别保留成功角色的转写；部分失败或缺少汇总分不等于已有角色作答不可读。
	// 存在 panel 却没有有效角色时不退回顶层候选，避免把失败角色的旧文字重新展示。
	for _, role := range panel.Roles {
		label, known := labels[role.Role]
		if !known || role.Status != "succeeded" {
			continue
		}
		if seen[role.Role] {
			return ""
		}
		seen[role.Role] = true
		text := taskImageObservationText(role.RawOutput.Observation, binding.InputVersionID, binding.CropSHA256, binding.QuestionID)
		if text == "" {
			continue
		}
		if role.Role != "single" {
			text = label + "：\n" + text
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, "\n\n")
}

func taskImageObservationText(observation *taskImageTextObservation, inputVersionID, cropHash, questionID string) string {
	if observation == nil || observation.SchemaVersion != "grading-image-v3" || observation.InputVersionID != inputVersionID || observation.CropSHA256 != cropHash {
		return ""
	}
	text, matches := "", 0
	for _, member := range observation.Members {
		if member.QuestionID != questionID {
			continue
		}
		matches++
		if member.Presence != "present" || member.Association != "matched" || member.Answer == nil {
			return ""
		}
		text = strings.TrimSpace(member.Answer.Value)
		if text == "" {
			parts := []string{}
			for _, part := range member.Answer.Parts {
				if part = strings.TrimSpace(part); part != "" {
					parts = append(parts, part)
				}
			}
			text = strings.Join(parts, "\n")
		}
	}
	if matches != 1 {
		return ""
	}
	return text
}
