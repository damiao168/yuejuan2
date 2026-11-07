package scorerelease

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/csvsafe"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/subjectcode"
	"github.com/google/uuid"
)

const formalExportTemplate = "school-standard-v1"

var errFormalExportIncomplete = errors.New("all subjects require a published release and frozen roster")

type formalExportPin struct {
	ExamID    string `json:"exam_id"`
	Subject   string `json:"subject"`
	ReleaseID string `json:"release_id"`
	Version   int    `json:"version"`
}

type formalExportResult struct {
	Content  []byte
	RowCount int
	Pins     []formalExportPin
	Data     *teacherScoreData
}

type publishedCSVExporter interface {
	ExportCurrentPublishedCSV(context.Context, auth.AccessScope, string, time.Time) (formalExportResult, error)
}

// ExportCurrentPublished 只导出当前正式发布的不可变成绩。复评期间的可变题分、总分不会进入此文件。
// 审计失败时还未发送文件，可安全重试；每次成功导出都留下实际使用的版本组合和文件摘要。
func (h *Handler) ExportCurrentPublished(w http.ResponseWriter, r *http.Request) {
	user, ok := releaseUser(w, r)
	if !ok {
		return
	}
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok || scope.TenantID != user.TenantID || scope.ActorID != user.ID || !auth.HasPermission(user, "score:manage") {
		httpx.Error(w, r, http.StatusForbidden, "formal_export_scope_required", "需要本校完整成绩管理权限。")
		return
	}
	if _, err := uuid.Parse(r.PathValue("examId")); err != nil {
		writeError(w, r, ErrInvalidInput)
		return
	}
	format := r.URL.Query().Get("format")
	if format != "" && format != "csv" && format != "xlsx" {
		writeError(w, r, ErrInvalidInput)
		return
	}
	if format == "" {
		format = "csv"
	}
	exporter, ok := h.service.store.(publishedCSVExporter)
	if !ok || h.audit == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "formal_export_unavailable", "正式成绩导出或审计暂不可用，请稍后重试。")
		return
	}
	now := h.service.now().UTC()
	result, err := exporter.ExportCurrentPublishedCSV(r.Context(), scope, r.PathValue("examId"), now)
	if errors.Is(err, errFormalExportIncomplete) {
		httpx.Error(w, r, http.StatusConflict, "formal_export_incomplete", "本场考试尚有学科未正式发布，或缺少完整冻结名册，暂不能导出正式成绩。")
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	contentType, filename, templateVersion := "text/csv; charset=utf-8", "exam-"+r.PathValue("examId")+"-published-scores.csv", formalExportTemplate
	if format == "xlsx" {
		sheets, buildErr := teacherScoreSheets(result.Data)
		if errors.Is(buildErr, errTeacherExportTooLarge) {
			httpx.Error(w, r, http.StatusRequestEntityTooLarge, "teacher_export_too_large", "教师成绩表最多支持10000名学生、200个班级，请缩小考试范围。")
			return
		}
		if buildErr != nil {
			writeError(w, r, buildErr)
			return
		}
		pins, _ := json.Marshal(result.Pins)
		result.Content, err = buildScoreWorkbook(sheets, map[string]string{"发布版本": string(pins), "模板版本": teacherScoreTemplate, "导出时间": now.Format(time.RFC3339), "导出人": user.ID})
		if err != nil {
			writeError(w, r, err)
			return
		}
		contentType, templateVersion = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", teacherScoreTemplate
		filename = result.Data.Name + "-教师成绩表.xlsx"
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(result.Content))
	err = h.audit.Audit(r.Context(), auth.AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: "score_release.formal_exported", TargetType: "exam", TargetID: r.PathValue("examId"),
		AfterValue: map[string]any{"exam_id": r.PathValue("examId"), "releases": result.Pins, "template_version": templateVersion, "format": format, "sha256": digest, "row_count": result.RowCount},
		Reason:     "导出已发布的正式成绩", IPAddress: httpx.ClientIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	if err != nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "formal_export_audit_unavailable", "无法记录本次导出，请稍后重试。")
		return
	}
	w.Header().Set("Content-Type", contentType)
	disposition := `attachment; filename="` + filename + `"`
	if format == "xlsx" {
		disposition = `attachment; filename="teacher-scores.xlsx"; filename*=UTF-8''` + url.PathEscape(filename)
	}
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-EduGrade-Content-SHA256", digest)
	w.Header().Set("X-EduGrade-Watermark", "EduGrade published sha256="+digest)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Content)
}

type formalExportSubject struct {
	formalExportPin
	Policy  VisibilityPolicy
	Entries []formalExportEntry
}

type formalExportEntry struct {
	StudentID, StudentNo, Name, GradeID, GradeName, ClassID, ClassName string
	Score, Max                                                         *float64
}

type formalExportStudent struct {
	formalExportEntry
	Subjects       map[string]formalExportEntry
	Total, Maximum int64
	Complete       bool
	Combo          string
}

type formalExportStats struct {
	Values []int64
	Sum    int64
}

func formalGroup(entry formalExportEntry, subject, combo string, maximum int64, class bool) string {
	key := subject + "\x00" + entry.GradeID + "\x00" + combo + "\x00" + strconv.FormatInt(maximum, 10)
	if class {
		key += "\x00" + entry.ClassID
	}
	return key
}

func formalNumeric(entry formalExportEntry) (int64, int64, bool) {
	if entry.Score == nil || entry.Max == nil || math.IsNaN(*entry.Score) || math.IsInf(*entry.Score, 0) || math.IsNaN(*entry.Max) || math.IsInf(*entry.Max, 0) || *entry.Max <= 0 || *entry.Score < 0 || *entry.Score > *entry.Max {
		return 0, 0, false
	}
	return int64(math.Round(*entry.Score * 100)), int64(math.Round(*entry.Max * 100)), true
}

func formalLabel(code string) string {
	if canonical, ok := subjectcode.Normalize(code); ok {
		code = canonical
	}
	if label := map[string]string{"chinese": "语文", "mathematics": "数学", "english": "英语", "physics": "物理", "chemistry": "化学", "biology": "生物", "history": "历史", "geography": "地理", "ethics_politics": "政治", "other": "其他学科"}[code]; label != "" {
		return label
	}
	return code
}

func formalStatsCells(stats map[string]*formalExportStats, key string, value int64, policy VisibilityPolicy) []string {
	cells := []string{"", "", ""}
	group := stats[key]
	// 沿用发布结果的可见性和小群体门槛；导出不能成为绕过隐藏排名、均分的另一条入口。
	if group == nil || len(group.Values) < privacyMinCohortSize {
		return cells
	}
	if policy.ShowExactRank {
		cells[0] = strconv.Itoa(1 + sort.Search(len(group.Values), func(i int) bool { return group.Values[i] <= value }))
	}
	if policy.ShowCohortStatistics {
		cells[1] = fmt.Sprintf("%.2f", float64(group.Sum)/float64(len(group.Values))/100)
	}
	if policy.ShowExactRank || policy.ShowCohortStatistics {
		cells[2] = strconv.Itoa(len(group.Values))
	}
	return cells
}

// 同一学生跨科的冻结身份不一致时整份文件拒绝生成，不能靠当前学籍替换历史事实。
// 未出现在某科发布条目中的应考生只显示“该发布版本无成绩”，不推断缺考，更不会补零。
func buildFormalCSV(name, school, actor string, subjects []formalExportSubject, now time.Time) (formalExportResult, error) {
	if len(subjects) == 0 {
		return formalExportResult{}, errFormalExportIncomplete
	}
	students := map[string]*formalExportStudent{}
	labels, used := map[string]string{}, map[string]int{}
	stats := map[string]*formalExportStats{}
	totalPolicy := VisibilityPolicy{ShowExactRank: true, ShowCohortStatistics: true}
	add := func(key string, value int64) {
		if stats[key] == nil {
			stats[key] = &formalExportStats{}
		}
		stats[key].Values = append(stats[key].Values, value)
		stats[key].Sum += value
	}
	result := formalExportResult{Pins: []formalExportPin{}}
	for _, subject := range subjects {
		used[formalLabel(subject.Subject)]++
	}
	for _, subject := range subjects {
		label := formalLabel(subject.Subject)
		if used[label] > 1 {
			label += "（" + subject.ExamID + "）"
		}
		labels[subject.ExamID] = label
		result.Pins = append(result.Pins, subject.formalExportPin)
		totalPolicy.ShowExactRank = totalPolicy.ShowExactRank && subject.Policy.ShowExactRank
		totalPolicy.ShowCohortStatistics = totalPolicy.ShowCohortStatistics && subject.Policy.ShowCohortStatistics
		for _, entry := range subject.Entries {
			student := students[entry.StudentID]
			if student == nil {
				student = &formalExportStudent{formalExportEntry: entry, Subjects: map[string]formalExportEntry{}, Complete: true}
				students[entry.StudentID] = student
			}
			if entry.StudentID == "" || student.StudentNo != entry.StudentNo || student.Name != entry.Name || student.GradeID != entry.GradeID || student.GradeName != entry.GradeName || student.ClassID != entry.ClassID || student.ClassName != entry.ClassName {
				return formalExportResult{}, errFormalExportIncomplete
			}
			if _, exists := student.Subjects[subject.ExamID]; exists {
				return formalExportResult{}, errFormalExportIncomplete
			}
			student.Subjects[subject.ExamID] = entry
			value, maximum, ok := formalNumeric(entry)
			if !ok {
				student.Complete = false
				continue
			}
			student.Total += value
			student.Maximum += maximum
			for _, class := range []bool{true, false} {
				add(formalGroup(entry, subject.ExamID, "", maximum, class), value)
			}
		}
	}
	ordered := make([]*formalExportStudent, 0, len(students))
	for _, student := range students {
		combo := []string{}
		for _, subject := range subjects {
			if _, selected := student.Subjects[subject.ExamID]; selected {
				combo = append(combo, subject.ExamID)
			}
		}
		sort.Strings(combo)
		student.Combo = strings.Join(combo, ",")
		if student.Complete {
			for _, class := range []bool{true, false} {
				add(formalGroup(student.formalExportEntry, "total", student.Combo, student.Maximum, class), student.Total)
			}
		}
		ordered = append(ordered, student)
	}
	for _, group := range stats {
		sort.Slice(group.Values, func(i, j int) bool { return group.Values[i] > group.Values[j] })
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
	var buffer bytes.Buffer
	buffer.WriteString("\ufeff")
	writer := csv.NewWriter(&buffer)
	writer.UseCRLF = true
	header := []string{"考试名称", "学校", "学号", "姓名", "考试年级", "考试班级", "选考学科"}
	for _, subject := range subjects {
		for _, suffix := range []string{"成绩", "满分", "状态", "班级排名", "年级排名", "班级平均分", "年级平均分", "班级统计人数", "年级统计人数", "发布版本ID", "发布版本"} {
			header = append(header, labels[subject.ExamID]+"·"+suffix)
		}
	}
	header = append(header, "总分", "总分满分", "总分班级排名", "总分年级排名", "班级总分平均分", "年级总分平均分", "总分班级统计人数", "总分年级统计人数", "成绩状态", "导出人", "导出时间", "模板版本", "统计口径")
	if err := writer.Write(csvsafe.Row(header)); err != nil {
		return formalExportResult{}, err
	}
	for _, student := range ordered {
		combo := []string{}
		for _, subject := range subjects {
			if _, selected := student.Subjects[subject.ExamID]; selected {
				combo = append(combo, labels[subject.ExamID])
			}
		}
		row := []string{name, school, student.StudentNo, student.Name, student.GradeName, student.ClassName, strings.Join(combo, "、")}
		for _, subject := range subjects {
			entry, selected := student.Subjects[subject.ExamID]
			status := "未选考"
			if selected {
				status = "该发布版本无成绩"
			}
			cells := []string{"", "", status, "", "", "", "", "", "", subject.ReleaseID, strconv.Itoa(subject.Version)}
			if value, maximum, ok := formalNumeric(entry); selected && ok {
				class := formalStatsCells(stats, formalGroup(entry, subject.ExamID, "", maximum, true), value, subject.Policy)
				grade := formalStatsCells(stats, formalGroup(entry, subject.ExamID, "", maximum, false), value, subject.Policy)
				cells = []string{fmt.Sprintf("%.2f", float64(value)/100), fmt.Sprintf("%.2f", float64(maximum)/100), "已正式发布", class[0], grade[0], class[1], grade[1], class[2], grade[2], subject.ReleaseID, strconv.Itoa(subject.Version)}
			}
			row = append(row, cells...)
		}
		cells := []string{"", "", "", "", "", "", "", "", "部分应考科目在发布版本中无成绩"}
		if student.Complete {
			class := formalStatsCells(stats, formalGroup(student.formalExportEntry, "total", student.Combo, student.Maximum, true), student.Total, totalPolicy)
			grade := formalStatsCells(stats, formalGroup(student.formalExportEntry, "total", student.Combo, student.Maximum, false), student.Total, totalPolicy)
			cells = []string{fmt.Sprintf("%.2f", float64(student.Total)/100), fmt.Sprintf("%.2f", float64(student.Maximum)/100), class[0], grade[0], class[1], grade[1], class[2], grade[2], "已正式发布"}
		}
		row = append(row, cells...)
		row = append(row, actor, now.UTC().Format(time.RFC3339), formalExportTemplate, "已发布快照；冻结考试班级；原始分相加；同分并列1、1、3；总分按同年级同选科同满分；排名均分受发布策略及10人门槛限制；学号按文本导入")
		if err := writer.Write(csvsafe.Row(row)); err != nil {
			return formalExportResult{}, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return formalExportResult{}, err
	}
	result.Content = buffer.Bytes()
	result.RowCount = len(ordered)
	result.Data = &teacherScoreData{Name: name, School: school, Subjects: subjects, Students: ordered}
	return result, nil
}
