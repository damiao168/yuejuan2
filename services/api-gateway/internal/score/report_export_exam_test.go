package score

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func reportTestEntry(id, grade, class string, score *float64, status string) reportEntry {
	max := 100.0
	return reportEntry{StudentID: id, StudentNo: "00" + id, Name: "学生" + id, Cohort: "2026级", GradeID: grade, GradeName: "高一", ClassID: class, ClassName: class + "班", Score: score, Max: &max, Status: status}
}

func reportTestCSV(t *testing.T, data examReport) (map[string]map[string]string, []string) {
	t.Helper()
	result, err := buildExamReportCSV(data, "tenant", "exam", "actor", time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(result.Content), "\ufeff") {
		t.Fatal("missing UTF-8 BOM")
	}
	if !strings.Contains(result.Filename, "internal") || !strings.Contains(string(result.Content), "内部核验，非正式成绩") {
		t.Fatal("internal verification report must identify its non-official source")
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(result.Content), "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]map[string]string{}
	for _, row := range rows[1:] {
		item := map[string]string{}
		for i, header := range rows[0] {
			item[header] = row[i]
		}
		values[item["学号"]] = item
	}
	return values, rows[0]
}

func TestExamReportCombinesSubjectsAndRanksOnlyComparableCompleteStudents(t *testing.T) {
	if reportSubjectLabel("ethics_politics") != "政治" || reportSubjectLabel("civics") != "政治" {
		t.Fatal("canonical and legacy subject codes must use the same Chinese export labels")
	}
	v90, v80, v70, v100 := 90.0, 80.0, 70.0, 100.0
	data := examReport{Name: "期中考试", SchoolName: "本校", Subjects: []reportSubject{
		{ID: "math", Code: "mathematics", SchoolID: "school", Entries: []reportEntry{
			reportTestEntry("1", "g1", "A", &v90, "待确认"), reportTestEntry("2", "g1", "A", &v90, "已确认"), reportTestEntry("3", "g1", "A", &v80, "已确认"), reportTestEntry("4", "g1", "A", &v100, "已确认"), reportTestEntry("5", "g1", "B", &v70, "已确认"), reportTestEntry("6", "g2", "C", &v90, "已确认"),
		}},
		{ID: "english", Code: "english", SchoolID: "school", Entries: []reportEntry{
			reportTestEntry("1", "g1", "A", &v80, "已确认"), reportTestEntry("2", "g1", "A", &v80, "已确认"), reportTestEntry("3", "g1", "A", &v70, "已确认"), reportTestEntry("4", "g1", "A", nil, "缺考"), reportTestEntry("6", "g2", "C", &v80, "已确认"),
		}},
	}}
	rows, _ := reportTestCSV(t, data)
	want := map[string]map[string]string{
		"001": {"总分": "170.00", "总分年级排名": "1", "总分班级排名": "1", "数学·班级排名": "2", "数学·年级排名": "2", "数学·班级平均分": "90.00", "数学·年级平均分": "86.00", "英语·年级统计人数": "3", "年级总分平均分": "163.33", "总分年级统计人数": "3"},
		"002": {"总分年级排名": "1"}, "003": {"总分年级排名": "3"},
		"004": {"总分": "", "总分年级排名": "", "英语·成绩": "", "英语·状态": "缺考"},
		"005": {"总分": "70.00", "总分年级排名": "1", "英语·状态": "未选考", "总分年级统计人数": "1"},
		"006": {"总分年级排名": "1", "总分年级统计人数": "1", "数学·年级统计人数": "1"},
	}
	for student, fields := range want {
		for key, value := range fields {
			if rows[student][key] != value {
				t.Errorf("%s %s=%q want %q", student, key, rows[student][key], value)
			}
		}
	}
	if len(rows) != 6 {
		t.Fatalf("one row per student: %d", len(rows))
	}
}

func TestExamReportKeepsZeroAndExcludesInvalidOrConflictingSnapshots(t *testing.T) {
	zero, ninety := 0.0, 90.0
	first := reportTestEntry("1", "grade", "A", &zero, "已确认")
	second := reportTestEntry("2", "grade", "A", &ninety, "缺页")
	conflict := reportTestEntry("3", "grade", "A", &ninety, "已确认")
	other := conflict
	other.ClassID = "B"
	other.ClassName = "B班"
	data := examReport{Name: "=untrusted", Subjects: []reportSubject{{ID: "math", Code: "mathematics", Entries: []reportEntry{first, second, conflict}}, {ID: "english", Code: "english", Entries: []reportEntry{other}}}}
	rows, _ := reportTestCSV(t, data)
	if rows["001"]["数学·成绩"] != "0.00" || rows["001"]["总分"] != "0.00" {
		t.Fatal("real zero was discarded")
	}
	if rows["002"]["数学·成绩"] != "" || rows["002"]["总分"] != "" {
		t.Fatal("missing page was converted to a statistical score")
	}
	if rows["003"]["总分"] != "" || !strings.Contains(rows["003"]["成绩状态"], "不一致") {
		t.Fatal("inconsistent historical class placements were combined")
	}
	if rows["001"]["考试名称"] == "=untrusted" {
		t.Fatal("formula injection was not neutralized")
	}
}

func TestExamReportRequiresWholeSchoolAccessForEverySubject(t *testing.T) {
	subjects := []reportSubject{{ID: "m", SchoolID: "school"}, {ID: "e", SchoolID: "school"}}
	if err := validateReportSubjects(auth.AccessScope{TenantID: "tenant", SchoolIDs: []string{"school"}, ExamIDs: []string{"m"}}, subjects); err == nil {
		t.Fatal("navigation or single-exam access expanded to whole-school report")
	}
	if err := validateReportSubjects(auth.AccessScope{TenantID: "tenant", TenantWide: true}, subjects); err != nil {
		t.Fatal(err)
	}
	subjects[1].SchoolID = "other"
	if err := validateReportSubjects(auth.AccessScope{TenantID: "tenant", TenantWide: true}, subjects); err == nil {
		t.Fatal("a malformed multi-school session must not be mislabeled as one school")
	}
}

func TestExamReportHandlerRejectsUnscopedActor(t *testing.T) {
	actor := auth.User{ID: "actor", TenantID: "tenant"}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/exams/exam/grades/export/report?tenant=other", nil)
	r.SetPathValue("examId", "exam")
	r = r.WithContext(auth.WithUser(context.Background(), actor))
	w := httptest.NewRecorder()
	NewHandler(NewMemoryStore(), nil).ExportGradesReport(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "score_report_scope_required") {
		t.Fatalf("unscoped export: %d %s", w.Code, w.Body.String())
	}
}
