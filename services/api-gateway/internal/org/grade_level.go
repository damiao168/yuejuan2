package org

import (
	"strconv"
	"strings"
	"time"
)

func validGradeLevel(stage string, level int) bool {
	return stage == "junior" && level >= 7 && level <= 9 || stage == "senior" && level >= 10 && level <= 12
}

func validateGradeInput(input Grade) error {
	if input.EntryYear != 0 && (input.EntryYear < 1900 || input.EntryYear > 2100) {
		return ErrInvalidEntryYear
	}
	// 旧客户端允许不填级数；显式级数必须属于所选学段，不能用改级数跨学段迁移届别。
	stage := input.EducationStage
	if stage == "" {
		stage = educationStageForLevel(input.LevelNo)
	}
	if input.LevelNo != 0 && !validGradeLevel(stage, input.LevelNo) {
		return ErrInvalidGradeLevel
	}
	return nil
}

func legacyGradeEntryYear(input Grade) int {
	if input.EntryYear != 0 {
		return input.EntryYear
	}
	start := currentSchoolYearStart(time.Now())
	parts := strings.Split(input.AcademicYear, "-")
	if len(parts) == 2 && len(parts[0]) == 4 && len(parts[1]) == 4 {
		if value, err := strconv.Atoi(parts[0]); err == nil {
			start = value
		}
	}
	base := 7
	if input.EducationStage == "senior" {
		base = 10
	}
	if input.LevelNo > base {
		start -= input.LevelNo - base
	}
	return start
}

func currentSchoolYearStart(now time.Time) int {
	// 学年按中国学校九月开学划分；一月至八月仍属于上一年开始的学年。
	now = now.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	year := now.Year()
	if now.Month() < time.September {
		year--
	}
	return year
}

func applyGradePatch(item Grade, input GradePatch) (Grade, error) {
	if input.LevelNo != nil {
		if !validGradeLevel(item.EducationStage, *input.LevelNo) {
			return Grade{}, ErrInvalidGradeLevel
		}
		// 只同步明确的标准级数名称；“2026级”等固定届名和学校自定义名称保留。
		if input.Name == nil {
			old, next := standardGradeName(item.LevelNo), standardGradeName(*input.LevelNo)
			if old != "" && (item.Name == old || item.Name == old+"年级") {
				item.Name = next + strings.TrimPrefix(item.Name, old)
			}
		}
		item.LevelNo = *input.LevelNo
	}
	if input.Name != nil {
		item.Name = *input.Name
	}
	return item, nil
}

func standardGradeName(level int) string {
	return map[int]string{7: "初一", 8: "初二", 9: "初三", 10: "高一", 11: "高二", 12: "高三"}[level]
}
