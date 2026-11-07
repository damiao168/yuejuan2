package org

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

const studentImportMaxRows = 10000
const studentImportMaxBytes = 4 << 20

// StudentImportRow 的 Row 是标准 CSV 中的原行号；界面负责映射回上传工作表。
type StudentImportRow struct {
	Row     int
	Student Student
}

// AtomicStudentImporter 必须先检查整批，再一次提交学生和初始学籍。
// 未实现此能力的仓储不能退回逐行创建，否则同一接口会出现不同的失败语义。
type AtomicStudentImporter interface {
	ImportStudentsAtomic(context.Context, string, []StudentImportRow) (CSVImportResult, error)
}

func emptyStudentImportResult() CSVImportResult {
	return CSVImportResult{Errors: []CSVImportError{}}
}

// ImportStudentsCSV 保留原有 CSV 请求和错误行号；任何一行校验失败都不会写入。
// 重复上传只跳过身份、班级和在籍状态完全一致的学生，不借导入修改既有学籍或考试快照。
func (h *Handler) ImportStudentsCSV(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, studentImportMaxBytes)
	reader := csv.NewReader(r.Body)
	reader.FieldsPerRecord = -1
	input := []StudentImportRow{}
	result := emptyStudentImportResult()
	scope, _ := auth.AccessScopeFromContext(r.Context())
	authorizedClasses := map[string]bool{}
	recordNumber := 0
	dataRows := 0
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				httpx.Error(w, r, http.StatusRequestEntityTooLarge, "student_import_too_large", "名单内容超过4MB，请分批导入")
			} else {
				httpx.Error(w, r, http.StatusBadRequest, "invalid_csv", "CSV格式有误，本次没有导入任何学生")
			}
			return
		}
		recordNumber++
		if recordNumber == 1 && len(record) >= 4 && strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(record[0]), "\ufeff"), "student_no") {
			continue
		}
		dataRows++
		if dataRows > studentImportMaxRows {
			httpx.Error(w, r, http.StatusRequestEntityTooLarge, "student_import_too_many_rows", "每批最多导入10000名学生，请分批处理")
			return
		}
		if len(record) < 4 {
			result.Errors = append(result.Errors, CSVImportError{Row: recordNumber, Message: "列不完整，请使用学生导入模板重新填写"})
			continue
		}
		student := Student{StudentNo: strings.TrimSpace(record[0]), Name: strings.TrimSpace(record[1]), SchoolID: strings.TrimSpace(record[2]), ClassID: strings.TrimSpace(record[3]), Status: "active"}
		if message := studentImportFieldError(student); message != "" {
			result.Errors = append(result.Errors, CSVImportError{Row: recordNumber, Message: message})
			continue
		}
		allowed, known := authorizedClasses[student.ClassID]
		if !known {
			allowed = h.allowsResource(r, scope, "school_class", student.ClassID)
			authorizedClasses[student.ClassID] = allowed
		}
		if !scope.ManagesSchool(student.SchoolID) || !allowed {
			result.Errors = append(result.Errors, CSVImportError{Row: recordNumber, Message: "所选学校或班级不在你的管理范围内"})
			continue
		}
		input = append(input, StudentImportRow{Row: recordNumber, Student: student})
	}
	if len(input) == 0 && len(result.Errors) == 0 {
		result.Errors = append(result.Errors, CSVImportError{Row: 1, Message: "没有可导入的学生记录"})
	}
	if len(result.Errors) != 0 {
		httpx.JSON(w, http.StatusOK, map[string]any{"result": result})
		return
	}
	importer, ok := h.store.(AtomicStudentImporter)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "student_import_atomic_unavailable", "当前名单存储不支持整批导入，请联系管理员")
		return
	}
	out, err := importer.ImportStudentsAtomic(r.Context(), mustUser(r).TenantID, input)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "student_import_failed", "暂时无法确认导入结果，请刷新学生列表后重试；相同记录会自动跳过")
		return
	}
	if out.Committed {
		h.auditAction(r, "org.students_imported", "student", "", "atomic students csv import")
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"result": out})
}

func studentImportFieldError(student Student) string {
	if student.StudentNo == "" || student.Name == "" || student.SchoolID == "" || student.ClassID == "" {
		return "学号、姓名、学校和班级不能为空"
	}
	if !utf8.ValidString(student.StudentNo) || !utf8.ValidString(student.Name) || utf8.RuneCountInString(student.StudentNo) > 128 || utf8.RuneCountInString(student.Name) > 128 {
		return "学号和姓名必须是有效文字，且各自不超过128个字符"
	}
	if strings.ContainsRune(student.StudentNo, '\x00') || strings.ContainsRune(student.Name, '\x00') || strings.ContainsRune(student.SchoolID, '\x00') || strings.ContainsRune(student.ClassID, '\x00') {
		return "学生字段含有无效字符，请清理后重新导入"
	}
	return ""
}

func studentImportKey(student Student) string { return student.SchoolID + "\x00" + student.StudentNo }

func sameImportedStudent(existing, input Student) bool {
	return existing.Name == input.Name && existing.ClassID == input.ClassID && existing.Status == "active"
}

func preflightStudentImport(input []StudentImportRow, classes map[string]Class, existing map[string]Student) (CSVImportResult, []StudentImportRow) {
	result := emptyStudentImportResult()
	newRows := []StudentImportRow{}
	seen := map[string]StudentImportRow{}
	for _, row := range input {
		student := row.Student
		if message := studentImportFieldError(student); message != "" {
			result.Errors = append(result.Errors, CSVImportError{Row: row.Row, Message: message})
			continue
		}
		class, ok := classes[student.ClassID]
		if !ok || class.SchoolID != student.SchoolID {
			result.Errors = append(result.Errors, CSVImportError{Row: row.Row, Message: "所选班级不属于该学校或已不可用，请重新选择"})
			continue
		}
		key := studentImportKey(student)
		if first, ok := seen[key]; ok {
			if !sameImportedStudent(first.Student, student) {
				result.Errors = append(result.Errors, CSVImportError{Row: row.Row, Message: "文件中同一学校的学号重复，姓名或班级不一致，请核对"})
			} else {
				result.Skipped++
			}
			continue
		}
		seen[key] = row
		if current, ok := existing[key]; ok {
			if !sameImportedStudent(current, student) {
				result.Errors = append(result.Errors, CSVImportError{Row: row.Row, Message: "该学号已存在，但姓名、班级或在籍状态不一致；请到学生管理中核对，导入不会覆盖"})
			} else {
				result.Skipped++
			}
			continue
		}
		newRows = append(newRows, row)
	}
	if len(result.Errors) > 0 {
		result.Skipped = 0
	}
	return result, newRows
}

// ImportStudentsAtomic 的内存实现把检查和全部写入放在同一把锁内，模拟数据库批次边界。
func (s *MemoryStore) ImportStudentsAtomic(_ context.Context, tenantID string, input []StudentImportRow) (CSVImportResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	classes := map[string]Class{}
	for id, class := range s.classes {
		if school, ok := s.schools[class.SchoolID]; ok && school.TenantID == tenantID && class.TenantID == tenantID && !s.deletedClasses[id] {
			classes[id] = class
		}
	}
	existing := map[string]Student{}
	for _, student := range s.students {
		if student.TenantID == tenantID {
			existing[studentImportKey(student)] = student
		}
	}
	result, pending := preflightStudentImport(input, classes, existing)
	if len(result.Errors) > 0 {
		return result, nil
	}
	for _, row := range pending {
		student := row.Student
		student.ID, student.TenantID, student.Status = s.id("student"), tenantID, "active"
		student.AccountStatus = "not_provisioned"
		class := classes[student.ClassID]
		student.AcademicYearID, student.GradeCohortID = class.AcademicYearID, class.GradeCohortID
		student.AdmissionYear = s.grades[class.GradeID].EntryYear
		s.students[student.ID] = student
	}
	result.Created, result.Committed = len(pending), true
	return result, nil
}
