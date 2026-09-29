package paper

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

const (
	maxTemplatePages   = 100
	maxTemplateRegions = 2000
)

func ValidateTemplateInput(name string, pageCount int, layout TemplateLayout) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("template name is required")
	}
	if err := ValidateTemplateOMRProfile(layout.OMRProfile); err != nil {
		return err
	}
	if pageCount < 1 || pageCount > maxTemplatePages || len(layout.Pages) != pageCount {
		return fmt.Errorf("page_count must match 1-%d layout pages", maxTemplatePages)
	}
	pageNos := map[int]bool{}
	questionIDs := map[string]bool{}
	regions := 0
	for _, page := range layout.Pages {
		if page.PageNo < 1 || page.PageNo > pageCount || pageNos[page.PageNo] {
			return errors.New("layout contains an invalid or duplicate page_no")
		}
		pageNos[page.PageNo] = true
		if page.Width < 1 || page.Height < 1 || page.Width > 20000 || page.Height > 20000 {
			return errors.New("page dimensions must be between 1 and 20000 pixels")
		}
		groups := [][]LayoutRegion{page.RegistrationMarks, page.IdentityRegions, page.QuestionRegions}
		for groupIndex, group := range groups {
			regions += len(group)
			for _, region := range group {
				if err := validateRegion(region); err != nil {
					return err
				}
				if groupIndex == 2 {
					if strings.TrimSpace(region.QuestionID) == "" {
						return errors.New("question region must reference question_id")
					}
					if questionIDs[region.QuestionID] {
						return errors.New("question_id may only have one template region")
					}
					questionIDs[region.QuestionID] = true
					labels := map[string]bool{}
					regions += len(region.OptionRegions)
					for _, option := range region.OptionRegions {
						label := strings.ToUpper(strings.TrimSpace(option.Label))
						if label == "" || len(label) > 16 || labels[label] {
							return errors.New("option regions require unique labels up to 16 characters")
						}
						labels[label] = true
						if err := validateOptionRegion(option); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	if regions > maxTemplateRegions {
		return fmt.Errorf("layout exceeds %d regions", maxTemplateRegions)
	}
	return nil
}

func validateOptionRegion(region OptionRegion) error {
	return validateRegion(LayoutRegion{X: region.X, Y: region.Y, Width: region.Width, Height: region.Height})
}

// 坐标和宽高均已归一化；右、下边界的微小余量用于容纳浮点计算误差。
func validateRegion(region LayoutRegion) error {
	values := []float64{region.X, region.Y, region.Width, region.Height}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("region coordinates must be finite numbers")
		}
	}
	if region.X < 0 || region.Y < 0 || region.Width <= 0 || region.Height <= 0 || region.X+region.Width > 1.000001 || region.Y+region.Height > 1.000001 {
		return errors.New("region coordinates must stay inside the normalized page")
	}
	return nil
}

func stableContentHash(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func buildReadiness(total float64, classCount int, studentCount int, papers []Paper, questions []Question, templates []AnswerSheetTemplate) ReadinessResult {
	classIDs := make([]string, classCount)
	for index := range classIDs {
		classIDs[index] = fmt.Sprintf("counted-class-%d", index)
	}
	candidateIDs := make([]string, studentCount)
	for index := range candidateIDs {
		candidateIDs[index] = fmt.Sprintf("counted-candidate-%d", index)
	}
	return buildReadinessForScope(total, classIDs, candidateIDs, papers, questions, templates)
}

func buildReadinessForScope(total float64, classIDs, candidateIDs []string, papers []Paper, questions []Question, templates []AnswerSheetTemplate) ReadinessResult {
	checks := []ReadinessCheck{}
	add := func(code string, label string, passed bool, message string, section string) {
		checks = append(checks, ReadinessCheck{Code: code, Label: label, Passed: passed, Severity: "blocker", Message: message, Section: section})
	}
	classCount, studentCount := len(classIDs), len(candidateIDs)
	add("student_scope", "学生范围", classCount > 0 && studentCount > 0, fmt.Sprintf("已关联 %d 个班级、%d 名在读学生", classCount, studentCount), "students")
	add("paper_file", "试卷文件", len(papers) > 0, fmt.Sprintf("已登记 %d 个试卷版本", len(papers)), "paper")
	add("questions", "题目结构", len(questions) > 0, fmt.Sprintf("已配置 %d 道题", len(questions)), "questions")

	scoreTotal := 0.0
	answersOK := len(questions) > 0
	rubricsOK := len(questions) > 0
	requiredAnswers, configuredAnswers := 0, 0
	requiredRubrics, configuredRubrics, lockedRubrics := 0, 0, 0
	missingAnswers, missingRubrics, unlockedRubrics, mismatchRubrics := []string{}, []string{}, []string{}, []string{}
	for index := range questions {
		question := &questions[index]
		scoreTotal += question.Score
		archetype := readinessAssessmentArchetype(*question)
		if questionRequiresStandardAnswerForArchetype(archetype) {
			requiredAnswers++
			if question.AnswerKey == nil || emptyAnswer(question.AnswerKey.StandardAnswer) {
				answersOK = false
				missingAnswers = append(missingAnswers, question.QuestionNo)
			} else {
				configuredAnswers++
			}
		}
		if questionRequiresRubricForArchetype(archetype) {
			requiredRubrics++
			if question.Rubric == nil {
				rubricsOK = false
				missingRubrics = append(missingRubrics, question.QuestionNo)
			} else {
				configuredRubrics++
				if question.Rubric.Status == "locked" {
					lockedRubrics++
				} else {
					rubricsOK = false
					unlockedRubrics = append(unlockedRubrics, question.QuestionNo)
				}
				if !scoreEqual(question.Rubric.MaxScore, question.Score) || !scoreEqual(SumRubricPoints(question.Rubric.Points), question.Score) {
					rubricsOK = false
					mismatchRubrics = append(mismatchRubrics, question.QuestionNo)
				}
			}
		}
	}
	add("total_score", "题目总分", len(questions) > 0 && scoreEqual(scoreTotal, total), fmt.Sprintf("题目合计 %.2f 分，考试总分 %.2f 分", scoreTotal, total), "questions")
	answerMessage := fmt.Sprintf("需要标准答案 %d 题，已配置 %d 题", requiredAnswers, configuredAnswers)
	if len(missingAnswers) > 0 {
		answerMessage += "；缺少 " + compactQuestionNumbers(missingAnswers)
	}
	rubricMessage := fmt.Sprintf("主观题共 %d 题：已配置 %d 题，已锁定 %d 题", requiredRubrics, configuredRubrics, lockedRubrics)
	if len(missingRubrics) > 0 {
		rubricMessage += "；缺少评分标准 " + compactQuestionNumbers(missingRubrics)
	}
	if len(unlockedRubrics) > 0 {
		rubricMessage += "；待锁定 " + compactQuestionNumbers(unlockedRubrics)
	}
	if len(mismatchRubrics) > 0 {
		rubricMessage += "；分值不一致 " + compactQuestionNumbers(mismatchRubrics)
	}
	add("answer_keys", "标准答案", answersOK, answerMessage, "questions")
	add("rubrics", "主观题评分标准", rubricsOK, rubricMessage, "questions")

	var locked *AnswerSheetTemplate
	for index := range templates {
		if templates[index].Status == "locked" && (locked == nil || templates[index].VersionNo > locked.VersionNo) {
			locked = &templates[index]
		}
	}
	add("locked_template", "答卷模板", locked != nil, "需要一个已锁定的答卷模板", "template")
	coverageOK := locked != nil && len(questions) > 0
	if locked != nil {
		covered := map[string]bool{}
		for _, page := range locked.Layout.Pages {
			for _, region := range page.QuestionRegions {
				covered[region.QuestionID] = true
			}
		}
		for _, question := range questions {
			if !covered[question.ID] {
				coverageOK = false
			}
		}
	}
	add("template_coverage", "题目区域", coverageOK, "模板必须为每道题配置一个合法答题区域", "template")

	ready := true
	for _, check := range checks {
		if !check.Passed {
			ready = false
		}
	}
	configuration := newReadinessConfigurationSnapshot(total, classIDs, candidateIDs, papers, questions, locked)
	return ReadinessResult{Ready: ready, ConfigurationHash: stableContentHash(configuration), Checks: checks, Advisories: buildReadinessAdvisories(questions)}
}

// 自动评分能力不足只生成维护提示；能否开始考试由上方就绪检查决定。
func buildReadinessAdvisories(questions []Question) []ReadinessAdvisory {
	advisories := []ReadinessAdvisory{}
	manualOnly := []ReadinessAdvisoryQuestion{}
	manualOnlyScore := 0.0
	for _, question := range questions {
		if HasAutomatedScoringPath(question.QuestionType) {
			continue
		}
		manualOnly = append(manualOnly, ReadinessAdvisoryQuestion{QuestionID: question.ID, QuestionNo: question.QuestionNo, QuestionType: question.QuestionType, Score: question.Score})
		manualOnlyScore += question.Score
	}
	if len(manualOnly) == 0 {
		return advisories
	}
	return append(advisories, ReadinessAdvisory{
		Code:      "manual_only_question_types",
		Label:     "全人工评阅题目",
		Severity:  "warning",
		Section:   "questions",
		Message:   fmt.Sprintf("%d 道题（%s）的题型没有自动评分能力，合计 %.2f 分将 100%% 由人工评阅，阅卷开始后每份答卷都会生成人工任务。", len(manualOnly), summarizeAdvisoryQuestions(manualOnly), manualOnlyScore),
		Score:     manualOnlyScore,
		Questions: manualOnly,
	})
}

func summarizeAdvisoryQuestions(questions []ReadinessAdvisoryQuestion) string {
	const maxListed = 8
	labels := []string{}
	for index, question := range questions {
		if index == maxListed {
			labels = append(labels, fmt.Sprintf("等 %d 道", len(questions)))
			break
		}
		labels = append(labels, fmt.Sprintf("%s·%s", question.QuestionNo, question.QuestionType))
	}
	return strings.Join(labels, "、")
}

func compactQuestionNumbers(values []string) string {
	limit := len(values)
	if limit > 5 {
		limit = 5
	}
	message := strings.Join(values[:limit], "、")
	if len(values) > limit {
		message += fmt.Sprintf(" 等 %d 题", len(values))
	} else {
		message += fmt.Sprintf("（%d 题）", len(values))
	}
	return message
}

// readinessAssessmentArchetype mirrors the default persisted by
// assessment_freeze_exam_on_ready. Canonicalizing the effective value before
// hashing keeps a readiness confirmation valid when that trigger materializes
// an otherwise implicit assessment configuration.
func readinessAssessmentArchetype(question Question) string {
	if archetype := strings.TrimSpace(question.AssessmentArchetype); archetype != "" {
		return archetype
	}
	switch question.QuestionType {
	case "single_choice", "multiple_choice", "true_false":
		return "selected_response"
	case "fill_blank":
		return "exact_text"
	case "numeric", "formula":
		return "numeric_expression"
	case "calculation":
		return "structured_steps"
	case "short_answer":
		return "short_constructed"
	case "essay", "discussion":
		return "extended_response"
	default:
		return "structured_steps"
	}
}

func emptyAnswer(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	return false
}
