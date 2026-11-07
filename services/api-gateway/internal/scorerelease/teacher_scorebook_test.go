package scorerelease

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

func teacherExportFixture(t *testing.T) formalExportResult {
	t.Helper()
	entry := func(id, class, name string, score *float64) formalExportEntry {
		return formalExportEntry{StudentID: id, StudentNo: "00" + id, Name: name, GradeID: "grade", GradeName: "高一", ClassID: class, ClassName: class + "班", Score: score, Max: formalTestScore(100)}
	}
	math := formalExportSubject{formalExportPin: formalExportPin{ExamID: "math", Subject: "mathematics", ReleaseID: "math-published-v1", Version: 1}, Entries: []formalExportEntry{entry("123", "1", "=1+1", formalTestScore(0)), entry("124", "1", "学生二", formalTestScore(100)), entry("125", "2", "学生三", formalTestScore(80))}}
	english := formalExportSubject{formalExportPin: formalExportPin{ExamID: "english", Subject: "english", ReleaseID: "english-published-v2", Version: 2}, Entries: []formalExportEntry{entry("123", "1", "=1+1", formalTestScore(90)), entry("124", "1", "学生二", formalTestScore(70)), entry("125", "2", "学生三", nil)}}
	result, err := buildFormalCSV("高一期中考试", "本校", "actor", []formalExportSubject{english, math}, time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func workbookFiles(t *testing.T, content []byte) map[string]string {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		result[file.Name] = string(raw)
		decoder := xml.NewDecoder(bytes.NewReader(raw))
		for {
			_, err = decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("invalid XML %s: %v", file.Name, err)
			}
		}
	}
	return result
}

func TestTeacherWorkbookUsesOneClassSheetAndBottomMean(t *testing.T) {
	result := teacherExportFixture(t)
	sheets, err := teacherScoreSheets(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(sheets) != 2 {
		t.Fatalf("class sheets=%d", len(sheets))
	}
	first := sheets[0]
	if fmt.Sprint(first.Header) != "[学号 姓名 数学 英语 总分 成绩状态]" {
		t.Fatalf("wide or repeated subject columns: %v", first.Header)
	}
	if first.Title != "高一期中考试" || strings.Count(first.Subtitle, "1班") != 1 {
		t.Fatalf("exam/class metadata missing: %+v", first)
	}
	if len(first.Rows) != 3 || first.Rows[0][0] != "00123" || first.Rows[0][2] != float64(0) {
		t.Fatalf("zero/identifier lost: %+v", first.Rows)
	}
	footer := first.Rows[len(first.Rows)-1]
	if footer[0] != "班级平均分" || footer[2] != float64(50) || footer[3] != float64(80) || footer[4] != float64(130) {
		t.Fatalf("incorrect bottom mean: %v", footer)
	}
	second := sheets[1]
	if second.Rows[0][3] != "本版本无成绩" || second.Rows[0][4] != nil || second.Rows[1][3] != nil || second.Rows[1][4] != nil {
		t.Fatalf("missing score became zero/absent or total: %v", second.Rows)
	}
	content, err := buildScoreWorkbook(sheets, map[string]string{"发布版本": "math-published-v1"})
	if err != nil {
		t.Fatal(err)
	}
	files := workbookFiles(t, content)
	// 手工核验时可显式保存合成工作簿；正常回归不向工作区写文件。
	if path := os.Getenv("EDUGRADE_TEACHER_WORKBOOK_TEST_FILE"); path != "" {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	main := files["xl/worksheets/sheet1.xml"]
	for _, expected := range []string{`xSplit="2" ySplit="3"`, `autoFilter ref="A3:F5"`, `<t xml:space="preserve">00123</t>`, `<t xml:space="preserve">=1+1</t>`, `班级平均分`} {
		if !strings.Contains(main, expected) {
			t.Fatalf("missing %s", expected)
		}
	}
	if strings.Contains(main, "<f>") || strings.Contains(main, "发布版本") || strings.Contains(main, "actor") || strings.Count(main, "高一期中考试") != 1 || strings.Count(main, "数学") != 1 {
		t.Fatal("technical fields, repeated metadata or formulas entered main sheet")
	}
	if !strings.Contains(files["docProps/custom.xml"], "math-published-v1") || !strings.Contains(files["xl/workbook.xml"], "_xlnm.Print_Titles") {
		t.Fatal("version trace or print header missing")
	}
}

func TestTeacherWorkbookDoesNotAverageDifferentSelectionsOrMaximums(t *testing.T) {
	result := teacherExportFixture(t)
	first, second := result.Data.Students[0], result.Data.Students[1]
	second.Combo = "different"
	second.Maximum = 20000
	entry := second.Subjects["math"]
	entry.Max = formalTestScore(150)
	second.Subjects["math"] = entry
	sheets, err := teacherScoreSheets(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	footer := sheets[0].Rows[2]
	if first.Combo == second.Combo || footer[2] != "满分不同，不计算" || footer[4] != nil || footer[5] != "选科不同，不计算总分平均分" {
		t.Fatalf("incomparable scores averaged: %v", footer)
	}
	used := map[string]bool{}
	label := strings.Repeat("高一/[班]", 10)
	a, b := classSheetName(label, used), classSheetName(label, used)
	if a == b || len([]rune(a)) > 31 || len([]rune(b)) > 31 || strings.ContainsAny(a, `:/\?*[]`) {
		t.Fatalf("unsafe sheet names: %q %q", a, b)
	}
	content, err := buildScoreWorkbook([]scoreSheet{{Name: "O'Brian班", Title: "考试", Header: []string{"学号", "姓名"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(workbookFiles(t, content)["xl/workbook.xml"], "O&#39;&#39;Brian班") {
		t.Fatal("print titles must escape apostrophes in class names")
	}
}

func TestTeacherWorkbookHandlerProducesXLSXAndAuditsActualFile(t *testing.T) {
	tenant, actor, exam, school := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	user := auth.User{ID: actor, TenantID: tenant, Permissions: []string{"score:manage"}, Roles: []string{"school_admin"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": school}}}
	scope, err := auth.ResolveDeclaredAccessScope(user)
	if err != nil {
		t.Fatal(err)
	}
	store := &formalHandlerStore{result: teacherExportFixture(t)}
	audit := &formalAuditSink{}
	handler := NewHandler(NewService(store), audit)
	request := httptest.NewRequest("GET", "/?format=xlsx", nil)
	request.SetPathValue("examId", exam)
	request = request.WithContext(auth.WithAccessScope(auth.WithUser(request.Context(), user), scope))
	response := httptest.NewRecorder()
	handler.ExportCurrentPublished(response, request)
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" || !strings.Contains(response.Header().Get("Content-Disposition"), "filename*=UTF-8''") || audit.event.AfterValue["template_version"] != teacherScoreTemplate || audit.event.AfterValue["format"] != "xlsx" {
		t.Fatalf("bad teacher export: %d %v %+v", response.Code, response.Header(), audit.event)
	}
	workbookFiles(t, response.Body.Bytes())
	request = httptest.NewRequest("GET", "/?format=pdf", nil)
	request.SetPathValue("examId", exam)
	request = request.WithContext(auth.WithAccessScope(auth.WithUser(request.Context(), user), scope))
	store.called = false
	response = httptest.NewRecorder()
	handler.ExportCurrentPublished(response, request)
	if response.Code != 400 || store.called {
		t.Fatalf("unsupported format executed exporter: %d", response.Code)
	}
}
