package report

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/csvsafe"
)

func buildStudentReport(data dataset, studentID string) StudentReport {
	report := StudentReport{ExamID: data.ExamID, StudentID: studentID, Questions: []StudentQuestionReport{}, KnowledgeMastery: []KnowledgeMastery{}, TeacherFeedback: []FeedbackItem{}, AIFeedback: []FeedbackItem{}, ErrorClues: []ErrorClue{}}
	var submission submissionRecord
	found := false
	for _, item := range data.Submissions {
		if item.StudentID == studentID {
			submission = item
			found = true
			break
		}
	}
	if !found {
		report.Empty = &EmptyState{Empty: true, Reason: "no_published_grade"}
		return report
	}
	report.SubmissionID = submission.ID
	report.AnonymousCode = submission.AnonymousCode
	report.TotalScore = round2(submission.TotalScore)
	report.MaxScore = round2(submission.MaxScore)
	report.ScoreRate = ratio(submission.TotalScore, submission.MaxScore)
	records := []gradeRecord{}
	for _, grade := range data.Grades {
		if grade.SubmissionID == submission.ID {
			records = append(records, grade)
			report.TeacherFeedback = append(report.TeacherFeedback, grade.TeacherFeedback...)
			report.AIFeedback = append(report.AIFeedback, grade.AIFeedback...)
			report.ErrorClues = append(report.ErrorClues, grade.ErrorClues...)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].QuestionNo < records[j].QuestionNo })
	for _, grade := range records {
		report.Questions = append(report.Questions, StudentQuestionReport{
			QuestionID:      grade.QuestionID,
			QuestionNo:      grade.QuestionNo,
			QuestionType:    grade.QuestionType,
			Score:           round2(grade.Score),
			MaxScore:        round2(grade.MaxScore),
			ScoreRate:       ratio(grade.Score, grade.MaxScore),
			KnowledgePoints: append([]string(nil), grade.KnowledgePoints...),
			TeacherFeedback: grade.TeacherFeedback,
			AIFeedback:      grade.AIFeedback,
			ErrorClues:      grade.ErrorClues,
		})
	}
	report.KnowledgeMastery = knowledgeMastery(records)
	return report
}

func buildOverview(data dataset) OverviewReport {
	out := OverviewReport{ExamID: data.ExamID, StudentCount: len(data.Submissions), PublishedCount: len(data.Submissions)}
	if len(data.Submissions) == 0 {
		out.Empty = &EmptyState{Empty: true, Reason: "no_published_grades"}
		return out
	}
	out.Stats = scoreStats(data.Submissions)
	classes := buildClassReports(data)
	for _, class := range classes {
		out.ClassComparisons = append(out.ClassComparisons, ClassComparison{
			ClassID:       class.ClassID,
			ClassName:     class.ClassName,
			StudentCount:  class.StudentCount,
			Average:       class.Stats.Average,
			Median:        class.Stats.Median,
			PassRate:      class.Stats.PassRate,
			ExcellentRate: class.Stats.ExcellentRate,
		})
	}
	out.QuestionScoreRates = buildQuestionAnalysis(data)
	return out
}

func buildClassReports(data dataset) []ClassReport {
	byClass := map[string][]submissionRecord{}
	classNames := map[string]string{}
	for _, sub := range data.Submissions {
		classID := sub.ClassID
		if classID == "" {
			classID = "unassigned"
		}
		byClass[classID] = append(byClass[classID], sub)
		classNames[classID] = sub.ClassName
	}
	out := []ClassReport{}
	for classID, submissions := range byClass {
		classData := dataset{ExamID: data.ExamID, Submissions: submissions}
		for _, grade := range data.Grades {
			if grade.ClassID == classID || (classID == "unassigned" && grade.ClassID == "") {
				classData.Grades = append(classData.Grades, grade)
			}
		}
		out = append(out, ClassReport{
			ClassID:                classID,
			ClassName:              classNames[classID],
			StudentCount:           len(submissions),
			Stats:                  scoreStats(submissions),
			FrequentWrongQuestions: frequentWrongQuestions(classData.Grades),
			WeakKnowledgePoints:    weakKnowledgePoints(classData.Grades),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClassName < out[j].ClassName })
	return out
}

func buildQuestionAnalysis(data dataset) []QuestionAnalysis {
	byQuestion := map[string][]gradeRecord{}
	for _, grade := range data.Grades {
		byQuestion[grade.QuestionID] = append(byQuestion[grade.QuestionID], grade)
	}
	top, bottom := topBottomSubmissions(data.Submissions)
	out := []QuestionAnalysis{}
	for questionID, records := range byQuestion {
		if len(records) == 0 {
			continue
		}
		sort.Slice(records, func(i, j int) bool { return records[i].QuestionNo < records[j].QuestionNo })
		first := records[0]
		var scoreSum, maxSum float64
		correct := 0
		for _, record := range records {
			scoreSum += record.Score
			maxSum += record.MaxScore
			if record.MaxScore > 0 && record.Score >= record.MaxScore {
				correct++
			}
		}
		options := optionDistribution(records)
		// 难度按得分率表示，值越大越容易；正确率只计拿到该题满分的记录。
		item := QuestionAnalysis{
			QuestionID:      questionID,
			QuestionNo:      first.QuestionNo,
			QuestionType:    first.QuestionType,
			MaxScore:        round2(first.MaxScore),
			AverageScore:    round2(scoreSum / float64(len(records))),
			ScoreRate:       ratio(scoreSum, maxSum),
			CorrectRate:     round2(float64(correct) / float64(len(records))),
			Difficulty:      ratio(scoreSum, maxSum),
			Discrimination:  discrimination(records, top, bottom),
			KnowledgePoints: append([]string(nil), first.KnowledgePoints...),
			FrequentErrors:  frequentErrors(records),
		}
		if len(options) == 0 {
			item.OptionEmpty = &EmptyState{Empty: true, Reason: "no_option_payload"}
		} else {
			item.OptionDistribution = options
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QuestionNo < out[j].QuestionNo })
	return out
}

func scoreStats(submissions []submissionRecord) ScoreStats {
	out := ScoreStats{Count: len(submissions), Distribution: defaultDistribution()}
	if len(submissions) == 0 {
		return out
	}
	scores := make([]float64, len(submissions))
	var sum float64
	pass := 0
	excellent := 0
	for i, sub := range submissions {
		scores[i] = sub.TotalScore
		sum += sub.TotalScore
		// 阈值和分段沿用保留两位小数的得分率，调整精度会改变边界分数的归类。
		rate := ratio(sub.TotalScore, sub.MaxScore)
		if rate >= 0.6 {
			pass++
		}
		if rate >= 0.85 {
			excellent++
		}
		addDistribution(&out, rate)
	}
	sort.Float64s(scores)
	out.Lowest = round2(scores[0])
	out.Highest = round2(scores[len(scores)-1])
	out.Average = round2(sum / float64(len(scores)))
	out.Median = round2(median(scores))
	out.Stddev = round2(stddev(scores, sum/float64(len(scores))))
	out.PassRate = round2(float64(pass) / float64(len(scores)))
	out.ExcellentRate = round2(float64(excellent) / float64(len(scores)))
	return out
}

// 同一题完整计入它的每个知识点，知识点之间不分摊分数，因此不能相加还原总分。
func knowledgeMastery(records []gradeRecord) []KnowledgeMastery {
	byPoint := map[string]*KnowledgeMastery{}
	for _, record := range records {
		for _, point := range record.KnowledgePoints {
			if strings.TrimSpace(point) == "" {
				continue
			}
			item := byPoint[point]
			if item == nil {
				item = &KnowledgeMastery{KnowledgePoint: point}
				byPoint[point] = item
			}
			item.Score += record.Score
			item.MaxScore += record.MaxScore
			item.QuestionCount++
		}
	}
	out := make([]KnowledgeMastery, 0, len(byPoint))
	for _, item := range byPoint {
		item.Score = round2(item.Score)
		item.MaxScore = round2(item.MaxScore)
		item.MasteryRate = ratio(item.Score, item.MaxScore)
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MasteryRate < out[j].MasteryRate })
	return out
}

func weakKnowledgePoints(records []gradeRecord) []KnowledgeMastery {
	out := knowledgeMastery(records)
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func frequentWrongQuestions(records []gradeRecord) []QuestionWeakness {
	type agg struct {
		id    string
		no    string
		wrong int
		score float64
		max   float64
	}
	byQuestion := map[string]*agg{}
	for _, record := range records {
		item := byQuestion[record.QuestionID]
		if item == nil {
			item = &agg{id: record.QuestionID, no: record.QuestionNo}
			byQuestion[record.QuestionID] = item
		}
		if record.Score < record.MaxScore {
			item.wrong++
		}
		item.score += record.Score
		item.max += record.MaxScore
	}
	out := []QuestionWeakness{}
	for _, item := range byQuestion {
		out = append(out, QuestionWeakness{QuestionID: item.id, QuestionNo: item.no, WrongCount: item.wrong, ScoreRate: ratio(item.score, item.max)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WrongCount == out[j].WrongCount {
			return out[i].ScoreRate < out[j].ScoreRate
		}
		return out[i].WrongCount > out[j].WrongCount
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func frequentErrors(records []gradeRecord) []ErrorClue {
	counts := map[string]ErrorClue{}
	for _, record := range records {
		for _, clue := range record.ErrorClues {
			key := clue.Source + "|" + clue.Text
			item := counts[key]
			item.QuestionID = record.QuestionID
			item.QuestionNo = record.QuestionNo
			item.Source = clue.Source
			item.Text = clue.Text
			item.Count++
			counts[key] = item
		}
	}
	out := make([]ErrorClue, 0, len(counts))
	for _, item := range counts {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func optionDistribution(records []gradeRecord) []OptionCount {
	counts := map[string]int{}
	for _, record := range records {
		for _, option := range extractOptions(record.AnswerPayload) {
			counts[option]++
		}
	}
	out := make([]OptionCount, 0, len(counts))
	for option, count := range counts {
		out = append(out, OptionCount{Option: option, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Option < out[j].Option })
	return out
}

func extractOptions(payload map[string]any) []string {
	if len(payload) == 0 {
		return nil
	}
	keys := []string{"selected_option", "option", "answer"}
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return []string{strings.TrimSpace(value)}
		}
	}
	if raw, ok := payload["selected_options"].([]any); ok {
		out := []string{}
		for _, item := range raw {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				out = append(out, strings.TrimSpace(value))
			}
		}
		return out
	}
	return nil
}

func topBottomSubmissions(submissions []submissionRecord) (map[string]bool, map[string]bool) {
	sorted := append([]submissionRecord(nil), submissions...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].TotalScore > sorted[j].TotalScore })
	if len(sorted) == 0 {
		return map[string]bool{}, map[string]bool{}
	}
	// 高低组各取总分排名两端的 27%，向上取整；极小样本的两组可能重叠。
	size := int(math.Ceil(float64(len(sorted)) * 0.27))
	if size < 1 {
		size = 1
	}
	top := map[string]bool{}
	bottom := map[string]bool{}
	for i := 0; i < size && i < len(sorted); i++ {
		top[sorted[i].ID] = true
		bottom[sorted[len(sorted)-1-i].ID] = true
	}
	return top, bottom
}

func discrimination(records []gradeRecord, top map[string]bool, bottom map[string]bool) float64 {
	var topScore, topMax, bottomScore, bottomMax float64
	for _, record := range records {
		if top[record.SubmissionID] {
			topScore += record.Score
			topMax += record.MaxScore
		}
		if bottom[record.SubmissionID] {
			bottomScore += record.Score
			bottomMax += record.MaxScore
		}
	}
	return round2(ratio(topScore, topMax) - ratio(bottomScore, bottomMax))
}

func defaultDistribution() []ScoreDistributionBucket {
	return []ScoreDistributionBucket{
		{Label: "0-59%", Min: 0, Max: 59},
		{Label: "60-69%", Min: 60, Max: 69},
		{Label: "70-79%", Min: 70, Max: 79},
		{Label: "80-89%", Min: 80, Max: 89},
		{Label: "90-100%", Min: 90, Max: 100},
	}
}

func addDistribution(stats *ScoreStats, rate float64) {
	percent := int(math.Round(rate * 100))
	if percent < 60 {
		stats.Distribution[0].Count++
		return
	}
	if percent < 70 {
		stats.Distribution[1].Count++
		return
	}
	if percent < 80 {
		stats.Distribution[2].Count++
		return
	}
	if percent < 90 {
		stats.Distribution[3].Count++
		return
	}
	stats.Distribution[4].Count++
}

func buildExportCSV(tenantID string, examID string, actorID string, overview OverviewReport, classes []ClassReport, questions []QuestionAnalysis, quality GradingQualityReport) ExportResult {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	exportedAt := time.Now().UTC().Format(time.RFC3339)
	watermark := fmt.Sprintf("EduGrade report export tenant=%s exam=%s actor=%s at=%s", tenantID, examID, actorID, exportedAt)
	rows := 0
	_ = writer.Write([]string{"section", "key", "value", "watermark"})
	write := func(section string, key string, value string) {
		_ = writer.Write(csvsafe.Row([]string{section, key, value, watermark}))
		rows++
	}
	write("overview", "student_count", fmt.Sprintf("%d", overview.StudentCount))
	write("overview", "average", fmt.Sprintf("%.2f", overview.Stats.Average))
	write("overview", "median", fmt.Sprintf("%.2f", overview.Stats.Median))
	for _, class := range classes {
		write("class", class.ClassName+"_average", fmt.Sprintf("%.2f", class.Stats.Average))
	}
	for _, question := range questions {
		write("question", question.QuestionNo+"_score_rate", fmt.Sprintf("%.2f", question.ScoreRate))
	}
	write("grading_quality", "ai_adoption_rate", fmt.Sprintf("%.2f", quality.AIAdoptionRate.Value))
	write("grading_quality", "ocr_failure_rate", fmt.Sprintf("%.2f", quality.OCRFailureRate.Value))
	writer.Flush()
	return ExportResult{
		Filename:    fmt.Sprintf("exam-%s-report.csv", examID),
		ContentType: "text/csv; charset=utf-8",
		Content:     buf.Bytes(),
		RowCount:    rows,
		Watermark:   watermark,
	}
}

func parseStringList(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var stringsOnly []string
	if err := json.Unmarshal(raw, &stringsOnly); err == nil {
		return cleanStrings(stringsOnly)
	}
	var items []any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	out := []string{}
	for _, item := range items {
		switch value := item.(type) {
		case string:
			out = append(out, value)
		case map[string]any:
			for _, key := range []string{"name", "label", "description", "id"} {
				if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
					out = append(out, text)
					break
				}
			}
		}
	}
	return cleanStrings(out)
}

func cleanStrings(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func parseJSONMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func ratio(value float64, max float64) float64 {
	if max <= 0 {
		return 0
	}
	return round2(value / max)
}

func median(sorted []float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// 这里描述已发布成绩的总体离散程度，分母用总人数，不作样本方差校正。
func stddev(values []float64, mean float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		diff := value - mean
		sum += diff * diff
	}
	return math.Sqrt(sum / float64(len(values)))
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}
