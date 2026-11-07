package org

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func gradeLevelFromName(name string) int {
	for level := 7; level <= 12; level++ {
		standard := standardGradeName(level)
		if strings.TrimSpace(name) == standard || strings.TrimSpace(name) == standard+"年级" {
			return level
		}
	}
	return 0
}

func expectedGradeLevel(stage string, entry, schoolYear int) (int, error) {
	if entry > schoolYear {
		return 0, ErrGradeEntryYearFuture
	}
	if schoolYear-entry >= 3 {
		return 0, ErrGradeCohortGraduated
	}
	base := 7
	if stage == "senior" {
		base = 10
	}
	return base + schoolYear - entry, nil
}

func normalizeCurrentGrade(input Grade, schoolYear int) (Grade, error) {
	if err := validateGradeInput(input); err != nil {
		return Grade{}, err
	}
	if input.LevelNo == 0 {
		input.LevelNo = gradeLevelFromName(input.Name)
	}
	if input.EducationStage == "" && input.LevelNo != 0 {
		input.EducationStage = educationStageForLevel(input.LevelNo)
	}
	if input.EducationStage != "junior" && input.EducationStage != "senior" {
		return Grade{}, ErrInvalidGradeLevel
	}
	yearName := fmt.Sprintf("%d-%d", schoolYear, schoolYear+1)
	// 新建入口创建当前届别，不是历史学年导入；不能由请求中的学年绕过级数对应规则。
	if input.AcademicYear != "" && input.AcademicYear != yearName {
		return Grade{}, ErrGradeAcademicYearMismatch
	}
	input.AcademicYear = yearName
	if input.EntryYear == 0 {
		if !validGradeLevel(input.EducationStage, input.LevelNo) {
			return Grade{}, ErrInvalidGradeLevel
		}
		base := 7
		if input.EducationStage == "senior" {
			base = 10
		}
		input.EntryYear = schoolYear - (input.LevelNo - base)
	}
	expected, err := expectedGradeLevel(input.EducationStage, input.EntryYear, schoolYear)
	if err != nil {
		return Grade{}, err
	}
	if input.LevelNo == 0 {
		input.LevelNo = expected
	}
	if !validGradeLevel(input.EducationStage, input.LevelNo) {
		return Grade{}, ErrInvalidGradeLevel
	}
	return input, nil
}

func currentGradeRepresentatives(rows []Grade) map[string]Grade {
	out := map[string]Grade{}
	for _, row := range rows {
		key := row.GradeCohortID
		if key == "" {
			key = fmt.Sprintf("%s:%s:%d", row.SchoolID, row.EducationStage, row.EntryYear)
		}
		prior, ok := out[key]
		if !ok || row.AcademicYear > prior.AcademicYear || row.AcademicYear == prior.AcademicYear && row.ID < prior.ID {
			out[key] = row
		}
	}
	return out
}

func validateCurrentGradeConflict(input Grade, rows []Grade, schoolYear int, creating bool) error {
	for _, row := range rows {
		if creating && row.EducationStage == input.EducationStage && row.EntryYear == input.EntryYear {
			return ErrGradeCohortExists
		}
	}
	expected, err := expectedGradeLevel(input.EducationStage, input.EntryYear, schoolYear)
	if err != nil {
		return err
	}
	if input.LevelNo != expected {
		return ErrGradeLevelYearMismatch
	}
	for _, representative := range currentGradeRepresentatives(rows) {
		if representative.EducationStage != input.EducationStage {
			continue
		}
		if representative.GradeCohortID == input.GradeCohortID && !creating {
			if representative.ID != input.ID {
				return ErrGradeHistoricalRecordImmutable
			}
			continue
		}
		otherExpected, otherErr := expectedGradeLevel(representative.EducationStage, representative.EntryYear, schoolYear)
		// 毕业和尚未开学的历史届别不占本学年在读年级；保留它们的班级、学生和快照。
		if otherErr != nil {
			continue
		}
		level := representative.LevelNo
		if level == 0 {
			level = gradeLevelFromName(representative.Name)
		}
		if representative.EntryYear != input.EntryYear && level == input.LevelNo {
			// 校正旧错误时不能被另一个错误占位锁住；新建仍要求先修正现有配置。
			if !creating && otherExpected != level {
				continue
			}
			return ErrGradeLevelConflict
		}
	}
	return nil
}

// lockGradeSchool 将同校年级的检查和写入串行化，跨学校、租户仍独立。
// 创建、调级和删除统一按 school→cohort→grade 加锁；学校键不变，用NO KEY UPDATE允许子对象正常取得外键KEY SHARE。
func lockGradeSchool(ctx context.Context, tx *sql.Tx, tenantID, schoolID string) (int, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id::text FROM school WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL FOR NO KEY UPDATE`, tenantID, schoolID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrInvalidParent
	}
	if err != nil {
		return 0, err
	}
	var start int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(start_year) FILTER (WHERE is_current AND deleted_at IS NULL),$3::int) FROM academic_year WHERE tenant_id=$1 AND school_id::text=$2`, tenantID, schoolID, currentSchoolYearStart(time.Now())).Scan(&start)
	return start, err
}

func schoolGradesTx(ctx context.Context, tx *sql.Tx, tenantID, schoolID string) ([]Grade, error) {
	rows, err := tx.QueryContext(ctx, `SELECT g.id::text,g.school_id::text,g.name,COALESCE(g.level_no,0),COALESCE(g.academic_year,''),c.education_stage,g.grade_cohort_id::text,c.entry_year FROM grade g JOIN grade_cohort c ON c.tenant_id=g.tenant_id AND c.id=g.grade_cohort_id WHERE g.tenant_id=$1 AND g.school_id::text=$2 AND g.deleted_at IS NULL`, tenantID, schoolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grade{}
	for rows.Next() {
		var grade Grade
		if err = rows.Scan(&grade.ID, &grade.SchoolID, &grade.Name, &grade.LevelNo, &grade.AcademicYear, &grade.EducationStage, &grade.GradeCohortID, &grade.EntryYear); err != nil {
			return nil, err
		}
		out = append(out, grade)
	}
	return out, rows.Err()
}

func writeGradeConflictError(w http.ResponseWriter, r *http.Request, err error) bool {
	for _, item := range []struct {
		err           error
		code, message string
		status        int
	}{
		{ErrGradeCohortExists, "grade_cohort_exists", "该学校的同学段入学届别已存在，请直接编辑现有年级，不要重复新增", http.StatusConflict},
		{ErrGradeLevelYearMismatch, "grade_level_year_mismatch", "当前年级与入学届别、学校当前学年不一致，请按入学年份重新核对", http.StatusBadRequest},
		{ErrGradeLevelConflict, "grade_level_conflict", "该学校同学段已有其他届别使用这个当前年级，请先校正现有年级配置", http.StatusConflict},
		{ErrGradeEntryYearFuture, "grade_entry_year_future", "该入学届别尚未开始，不能创建为当前在读年级", http.StatusBadRequest},
		{ErrGradeCohortGraduated, "grade_cohort_graduated", "该届别已超出三年在读范围，不能再创建或调整为当前在读年级", http.StatusBadRequest},
		{ErrGradeAcademicYearMismatch, "grade_academic_year_mismatch", "新建年级必须使用学校当前学年，不能通过历史或未来学年绕过届别检查", http.StatusBadRequest},
		{ErrGradeHistoricalRecordImmutable, "grade_historical_record_immutable", "历史学年年级不能调整当前级数，请编辑该届别的最新年级记录", http.StatusConflict},
	} {
		if errors.Is(err, item.err) {
			httpx.Error(w, r, item.status, item.code, item.message)
			return true
		}
	}
	return false
}
