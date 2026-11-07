package scorerelease

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const teacherScoreTemplate = "school-teacher-v1"

var errTeacherExportTooLarge = errors.New("teacher workbook exceeds synchronous export limit")

type teacherScoreData struct {
	Name, School string
	Subjects     []formalExportSubject
	Students     []*formalExportStudent
}

func teacherSubjects(input []formalExportSubject) []formalExportSubject {
	order := map[string]int{"chinese": 1, "mathematics": 2, "math": 2, "english": 3, "physics": 4, "chemistry": 5, "biology": 6, "ethics_politics": 7, "history": 8, "geography": 9}
	result := append([]formalExportSubject(nil), input...)
	sort.SliceStable(result, func(i, j int) bool {
		a, b := order[result[i].Subject], order[result[j].Subject]
		if a == 0 {
			a = 100
		}
		if b == 0 {
			b = 100
		}
		if a != b {
			return a < b
		}
		return result[i].Subject < result[j].Subject
	})
	return result
}

func classSheetName(label string, used map[string]bool) string {
	label = strings.Trim(strings.Map(func(char rune) rune {
		if strings.ContainsRune(`:/\?*[]`, char) || char < 32 {
			return '-'
		}
		return char
	}, label), " '")
	if label == "" {
		label = "班级成绩"
	}
	for number := 1; ; number++ {
		suffix := ""
		if number > 1 {
			suffix = "（" + strconv.Itoa(number) + "）"
		}
		name := []rune(label)
		limit := 31 - len([]rune(suffix))
		if len(name) > limit {
			name = name[:limit]
		}
		candidate := string(name) + suffix
		if !used[strings.ToLower(candidate)] {
			used[strings.ToLower(candidate)] = true
			return candidate
		}
	}
}

// 分数以百分之一分累计；同科满分不一致时不能合并均分，真实零分仍计入有效人数。
type classSubjectMean struct {
	sum        int64
	count      int
	maximum    int64
	comparable bool
}

func (mean *classSubjectMean) add(score, maximum int64) {
	if mean.count == 0 {
		mean.maximum, mean.comparable = maximum, true
	} else if mean.maximum != maximum {
		mean.comparable = false
	}
	mean.sum += score
	mean.count++
}

func (mean classSubjectMean) cell() any {
	if mean.count == 0 {
		return nil
	}
	if !mean.comparable {
		return "满分不同，不计算"
	}
	return float64(mean.sum) / float64(mean.count) / 100
}

// teacherScoreSheets 每个冻结考试班级一张表，考试和班级只写在顶部；每科只有一列，班均留在最后一行。
// 教师内部班均只计有效已发布分数。它不生成名次，不改变学生端既有的公开统计或人数门槛。
func teacherScoreSheets(data *teacherScoreData) ([]scoreSheet, error) {
	if data == nil {
		return nil, errFormalExportIncomplete
	}
	if len(data.Students) > 10000 {
		return nil, errTeacherExportTooLarge
	}
	subjects := teacherSubjects(data.Subjects)
	groups, keys := map[string][]*formalExportStudent{}, []string{}
	for _, student := range data.Students {
		key := student.GradeID + "\x00" + student.ClassID
		if _, found := groups[key]; !found {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], student)
	}
	// 工作表顺序沿用已排序的冻结班级名册，而不是当前的班级列表。
	if len(keys) > 200 {
		return nil, errTeacherExportTooLarge
	}
	usedNames := map[string]bool{}
	sheets := make([]scoreSheet, 0, len(keys))
	for _, key := range keys {
		students := groups[key]
		first := students[0]
		classSubjects := []formalExportSubject{}
		for _, subject := range subjects {
			for _, student := range students {
				if _, selected := student.Subjects[subject.ExamID]; selected {
					classSubjects = append(classSubjects, subject)
					break
				}
			}
		}
		label := first.ClassName
		if !strings.Contains(label, first.GradeName) {
			label = first.GradeName + " · " + label
		}
		sheet := scoreSheet{Name: classSheetName(label, usedNames), Title: data.Name, Subtitle: data.School + " · " + label + " · 仅计已发布有效成绩，缺失不补零", Header: []string{"学号", "姓名"}, Widths: []float64{22, 18}, FreezeColumns: 2, Footer: true}
		means := make([]classSubjectMean, len(classSubjects))
		totalMean := classSubjectMean{}
		usedLabels := map[string]int{}
		for _, subject := range classSubjects {
			name := formalLabel(subject.Subject)
			usedLabels[name]++
			if usedLabels[name] > 1 {
				name += fmt.Sprintf("（%d）", usedLabels[name])
			}
			sheet.Header = append(sheet.Header, name)
			sheet.Widths = append(sheet.Widths, 16)
		}
		sheet.Header = append(sheet.Header, "总分", "成绩状态")
		sheet.Widths = append(sheet.Widths, 16, 34)
		combo := first.Combo
		sameCombo := true
		for _, student := range students {
			if student.Combo != combo {
				sameCombo = false
			}
			row := []any{student.StudentNo, student.Name}
			for index, subject := range classSubjects {
				entry, selected := student.Subjects[subject.ExamID]
				value, maximum, valid := formalNumeric(entry)
				switch {
				case !selected:
					row = append(row, "未选考")
				case !valid:
					row = append(row, "本版本无成绩")
				default:
					row = append(row, float64(value)/100)
					means[index].add(value, maximum)
				}
			}
			if student.Complete {
				row = append(row, float64(student.Total)/100, "已正式发布")
				totalMean.add(student.Total, student.Maximum)
			} else {
				row = append(row, nil, "应考科目成绩不完整")
			}
			sheet.Rows = append(sheet.Rows, row)
		}
		footer := []any{"班级平均分", nil}
		for _, mean := range means {
			footer = append(footer, mean.cell())
		}
		var total any
		note := "按各科有效成绩计算，缺失不补零"
		if sameCombo {
			total = totalMean.cell()
		} else {
			note = "选科不同，不计算总分平均分"
		}
		sheet.Rows = append(sheet.Rows, append(footer, total, note))
		sheets = append(sheets, sheet)
	}
	if len(sheets) == 0 {
		return nil, errFormalExportIncomplete
	}
	return sheets, nil
}
