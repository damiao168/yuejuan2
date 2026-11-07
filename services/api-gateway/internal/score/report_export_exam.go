package score

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/csvsafe"
	"edugrade-enterprise/services/api-gateway/internal/subjectcode"
)

type examReport struct {
	Name, SchoolName string
	Subjects         []reportSubject
}

type reportSubject struct {
	ID, Code, SchoolID string
	Entries            []reportEntry
}

type reportEntry struct {
	StudentID, StudentNo, Name, Cohort, GradeID, GradeName, ClassID, ClassName string
	Status                                                                     string
	Score, Max                                                                 *float64
}

type reportStudent struct {
	reportEntry
	bySubject map[string]reportEntry
	combo     string
	conflict  bool
	total     *reportValue
}

type reportValue struct{ score, max int64 }
type reportStats struct {
	values []int64
	sum    int64
}

func (s *reportStats) add(value int64) { s.values = append(s.values, value); s.sum += value }
func (s reportStats) cells(value int64) []string {
	if len(s.values) == 0 {
		return []string{"", "", "0"}
	}
	// 分组在写 CSV 前降序排列；二分查找严格更高的分数数量，避免全年级导出逐行扫描全部学生。
	rank := 1 + sort.Search(len(s.values), func(index int) bool { return s.values[index] <= value })
	return []string{strconv.Itoa(rank), fmt.Sprintf("%.2f", float64(s.sum)/float64(len(s.values))/100), strconv.Itoa(len(s.values))}
}

func reportNumeric(entry reportEntry) *reportValue {
	if entry.Status != "待确认" && entry.Status != "已确认" && entry.Status != "待发布" && entry.Status != "已发布" && entry.Status != "已锁定" && entry.Status != "已评分" {
		return nil
	}
	if entry.Score == nil || entry.Max == nil || *entry.Max <= 0 || *entry.Score < 0 || *entry.Score > *entry.Max || math.IsNaN(*entry.Score) || math.IsInf(*entry.Score, 0) || math.IsNaN(*entry.Max) || math.IsInf(*entry.Max, 0) {
		return nil
	}
	return &reportValue{int64(math.Round(*entry.Score * 100)), int64(math.Round(*entry.Max * 100))}
}

func reportGroup(entry reportEntry, subject, combo string, maximum int64, class bool) string {
	key := subject + "\x00" + entry.GradeID + "\x00" + combo + "\x00" + strconv.FormatInt(maximum, 10)
	if class {
		key += "\x00" + entry.ClassID
	}
	return key
}

func reportSubjectLabel(code string) string {
	if canonical, ok := subjectcode.Normalize(code); ok {
		code = canonical
	}
	labels := map[string]string{"chinese": "语文", "mathematics": "数学", "math": "数学", "english": "英语", "physics": "物理", "chemistry": "化学", "biology": "生物", "history": "历史", "geography": "地理", "ethics_politics": "政治", "politics": "政治", "morality": "道德与法治", "morality_and_law": "道德与法治", "science": "科学", "information_technology": "信息技术", "physical_education": "体育", "other": "其他学科"}
	if label := labels[code]; label != "" {
		return label
	}
	return code
}

func reportStatsCells(groups map[string]*reportStats, key string, value int64) []string {
	if stat := groups[key]; stat != nil {
		return stat.cells(value)
	}
	return []string{"", "", "0"}
}

// buildExamReportCSV 供内部核验，包含待确认等未发布成绩；不能用于正式教务交付。
// 以冻结名册合并学生，同一场次按每科的最终成绩计算；未选考、缺考和未完成均不能补零。
// 总分只在该生选考学科都有效时生成，按同年级、同选科组合及同满分比较，避免选科学生被放入不可比的排名。
func buildExamReportCSV(data examReport, tenantID, examID, actorID string, now time.Time) (ExportResult, error) {
	students := map[string]*reportStudent{}
	labels, codes := map[string]string{}, map[string]int{}
	for _, subject := range data.Subjects {
		codes[reportSubjectLabel(subject.Code)]++
	}
	for _, subject := range data.Subjects {
		label := reportSubjectLabel(subject.Code)
		if codes[label] > 1 {
			label += "（" + subject.ID + "）"
		}
		labels[subject.ID] = label
		for _, entry := range subject.Entries {
			if entry.StudentID == "" {
				continue
			}
			student := students[entry.StudentID]
			if student == nil {
				student = &reportStudent{reportEntry: entry, bySubject: map[string]reportEntry{}}
				students[entry.StudentID] = student
			}
			if student.StudentNo != entry.StudentNo || student.Name != entry.Name || student.GradeID != entry.GradeID || student.ClassID != entry.ClassID {
				student.conflict = true
			}
			if _, duplicate := student.bySubject[subject.ID]; duplicate {
				student.conflict = true
			}
			student.bySubject[subject.ID] = entry
		}
	}
	groups := map[string]*reportStats{}
	add := func(key string, score int64) {
		if groups[key] == nil {
			groups[key] = &reportStats{}
		}
		groups[key].add(score)
	}
	ordered := make([]*reportStudent, 0, len(students))
	for _, student := range students {
		comboIDs := []string{}
		var score, maximum int64
		complete := !student.conflict
		for _, subject := range data.Subjects {
			entry, selected := student.bySubject[subject.ID]
			if !selected {
				continue
			}
			comboIDs = append(comboIDs, subject.ID)
			value := reportNumeric(entry)
			if value == nil {
				complete = false
				continue
			}
			score += value.score
			maximum += value.max
			for _, class := range []bool{false, true} {
				add(reportGroup(entry, subject.ID, "", value.max, class), value.score)
			}
		}
		sort.Strings(comboIDs)
		student.combo = strings.Join(comboIDs, ",")
		if complete && len(comboIDs) > 0 {
			student.total = &reportValue{score, maximum}
			for _, class := range []bool{false, true} {
				add(reportGroup(student.reportEntry, "total", student.combo, maximum, class), score)
			}
		}
		ordered = append(ordered, student)
	}
	for _, group := range groups {
		sort.Slice(group.values, func(i, j int) bool { return group.values[i] > group.values[j] })
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.GradeID != b.GradeID {
			return a.GradeID < b.GradeID
		}
		if a.ClassName != b.ClassName {
			return a.ClassName < b.ClassName
		}
		if a.StudentNo != b.StudentNo {
			return a.StudentNo < b.StudentNo
		}
		return a.StudentID < b.StudentID
	})
	var buf bytes.Buffer
	buf.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(&buf)
	writer.UseCRLF = true
	header := []string{"考试名称", "学校", "学号", "姓名", "入学届别", "考试年级", "考试班级", "选考学科"}
	for _, subject := range data.Subjects {
		for _, suffix := range []string{"成绩", "满分", "状态", "班级排名", "年级排名", "班级平均分", "年级平均分", "班级统计人数", "年级统计人数"} {
			header = append(header, labels[subject.ID]+"·"+suffix)
		}
	}
	header = append(header, "总分", "总分满分", "总分班级排名", "总分年级排名", "班级总分平均分", "年级总分平均分", "总分班级统计人数", "总分年级统计人数", "成绩状态", "导出人", "导出时间", "统计口径", "水印")
	if err := writer.Write(csvsafe.Row(header)); err != nil {
		return ExportResult{}, err
	}
	exported := now.UTC().Format(time.RFC3339)
	watermark := fmt.Sprintf("EduGrade internal-draft report tenant=%s exam=%s actor=%s at=%s", tenantID, examID, actorID, exported)
	for _, student := range ordered {
		comboLabels := []string{}
		for _, subject := range data.Subjects {
			if _, ok := student.bySubject[subject.ID]; ok {
				comboLabels = append(comboLabels, labels[subject.ID])
			}
		}
		row := []string{data.Name, data.SchoolName, student.StudentNo, student.Name, student.Cohort, student.GradeName, student.ClassName, strings.Join(comboLabels, "、")}
		for _, subject := range data.Subjects {
			entry, selected := student.bySubject[subject.ID]
			status := entry.Status
			if !selected {
				status = "未选考"
			}
			cells := []string{"", "", status, "", "", "", "", "", ""}
			if value := reportNumeric(entry); selected && value != nil {
				class := reportStatsCells(groups, reportGroup(entry, subject.ID, "", value.max, true), value.score)
				grade := reportStatsCells(groups, reportGroup(entry, subject.ID, "", value.max, false), value.score)
				cells = []string{fmt.Sprintf("%.2f", float64(value.score)/100), fmt.Sprintf("%.2f", float64(value.max)/100), status, class[0], grade[0], class[1], grade[1], class[2], grade[2]}
			}
			row = append(row, cells...)
		}
		totalCells := []string{"", "", "", "", "", "", "", "", "部分科目未完成"}
		if student.conflict {
			totalCells[8] = "跨科名册资料不一致，请核对"
		}
		if value := student.total; value != nil {
			class := reportStatsCells(groups, reportGroup(student.reportEntry, "total", student.combo, value.max, true), value.score)
			grade := reportStatsCells(groups, reportGroup(student.reportEntry, "total", student.combo, value.max, false), value.score)
			totalCells = []string{fmt.Sprintf("%.2f", float64(value.score)/100), fmt.Sprintf("%.2f", float64(value.max)/100), class[0], grade[0], class[1], grade[1], class[2], grade[2], "各科总成绩已生成（发布状态见各科）"}
		}
		row = append(row, totalCells...)
		row = append(row, actorID, exported, "内部核验，非正式成绩，包含未发布成绩；原始分相加；同分并列1、1、3；均分只计有效成绩；总分按同年级同选科组合及满分比较；学号请以文本导入", watermark)
		if err := writer.Write(csvsafe.Row(row)); err != nil {
			return ExportResult{}, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{Filename: fmt.Sprintf("exam-%s-internal-score-report.csv", examID), ContentType: "text/csv; charset=utf-8", Content: buf.Bytes(), RowCount: len(ordered), Watermark: watermark}, nil
}
