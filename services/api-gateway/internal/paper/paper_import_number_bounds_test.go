package paper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestPaperImportQuestionNumberBoundsPreserveAcceptedLabels(t *testing.T) {
	for input, want := range map[string]string{
		"第03题": "3", "（01）": "1", "01.02": "1(2)", "18（1）": "18(1)",
		"二十三": "23", "A-1": "a-1", "10001": "10001", "9223372036854775807": "9223372036854775807",
		strings.Repeat("a", 64): strings.Repeat("a", 64),
	} {
		if got := normalizePaperImportQuestionNumber(input); got != want {
			t.Errorf("normalize %q=%q want %q", input, got, want)
		}
	}
	for _, input := range []string{strings.Repeat("9", 32), "9223372036854775808", "1(" + strings.Repeat("9", 32) + ")", strings.Repeat("9", 32) + ".1", strings.Repeat("a", 65), strings.Repeat("9 ", 20)} {
		if got := normalizePaperImportQuestionNumber(input); got != "" {
			t.Errorf("out-of-range label produced %q", got)
		}
	}
}

func TestPaperImportNumberBoundsCoverAllCandidateKinds(t *testing.T) {
	tooLarge := strings.Repeat("9", 32)
	questions := []QuestionCandidate{{CandidateID: "q", QuestionNoRaw: "1", QuestionNoNormalized: "1", ParentQuestionNo: tooLarge}}
	answers := []AnswerCandidate{{CandidateID: "a", QuestionNoHint: tooLarge, QuestionNoNormalized: "1"}}
	solutions := []SolutionCandidate{{CandidateID: "s", QuestionNoHint: "1", SubquestionNoHint: tooLarge}}
	rubrics := []RubricCandidate{{CandidateID: "r", QuestionNoHint: "1", QuestionNoNormalized: tooLarge}}
	drafts, issues := reconcilePaperImportCandidates(questions, answers, solutions, rubrics, nil)
	if drafts[0].QuestionNo != "" || questions[0].QuestionNoNormalized != "" || answers[0].QuestionNoNormalized != "" || solutions[0].QuestionNoNormalized != "" || rubrics[0].QuestionNoNormalized != "" {
		t.Fatal("invalid candidate number was used for matching")
	}
	if len(issues) > 12 {
		t.Fatalf("invalid labels expanded diagnostics: %d", len(issues))
	}
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Message, "64字符") {
			found = true
		}
		if strings.Contains(issue.Message, tooLarge) {
			t.Fatal("diagnostic echoed unbounded input")
		}
	}
	if !found {
		t.Fatal("missing bounded number diagnostic")
	}
}

func TestPaperImportGapExpansionHasGlobalBudget(t *testing.T) {
	tests := []struct {
		name    string
		numbers []string
		count   int
		summary string
	}{
		{"normal consecutive", []string{"1", "2", "3"}, 0, ""},
		{"small gap and subquestion", []string{"1", "4", "1(2)", "A-1"}, 2, ""},
		{"exact budget", []string{"1", "102"}, 100, ""},
		{"above budget", []string{"1", "103"}, 1, "101个"},
		{"sparse", []string{"1", "10001"}, 1, "9999个"},
		{"maximum int64", []string{"1", "9223372036854775807"}, 1, "9223372036854775805个"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			byNo := map[string][]int{}
			for i, no := range test.numbers {
				byNo[no] = []int{i}
			}
			issues := []PaperImportIssue{}
			addQuestionNumberGapIssues(byNo, &issues)
			if len(issues) != test.count {
				t.Fatalf("gap issues=%d want=%d", len(issues), test.count)
			}
			if test.summary != "" && (issues[0].QuestionNo != "" || !strings.Contains(issues[0].Message, test.summary)) {
				t.Fatalf("wrong summary: %#v", issues)
			}
			for _, issue := range issues {
				if utf8.RuneCountInString(issue.Message) > 600 {
					t.Fatal("unbounded gap summary")
				}
			}
		})
	}
	// 多个小断档也共用预算；不能按区间各展开100条，重新制造输入放大。
	byNo := map[string][]int{}
	for i := 0; i < 30; i++ {
		byNo[strconv.Itoa(i*100)] = []int{i}
	}
	var issues []PaperImportIssue
	addQuestionNumberGapIssues(byNo, &issues)
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "另19处断档") || utf8.RuneCountInString(issues[0].Message) > 600 {
		t.Fatalf("multi-range output was not bounded: %#v", issues)
	}
}

// 走真实HTTP解析客户端、导入服务与MemoryStore完成入口，验证保存输出也保持有界。
// HTTP服务只替代模型transport；不声称本测试运行了真实模型或PostgreSQL事务。
func TestPaperImportTinyNumberResultPersistsBoundedIssues(t *testing.T) {
	for _, number := range []string{"4", "10001", "9223372036854775807", strings.Repeat("9", 32)} {
		t.Run(number, func(t *testing.T) {
			store, job := candidateImport(t)
			score := 1.0
			parsed := DocumentParseResponse{QuestionCandidates: []QuestionCandidate{
				{CandidateID: "q1", QuestionNoRaw: "1", QuestionNoNormalized: "1", QuestionType: "single_choice", Score: &score, Stem: "first"},
				{CandidateID: "q2", QuestionNoRaw: number, QuestionNoNormalized: number, QuestionType: "single_choice", Score: &score, Stem: "second"},
			}}
			payload, err := json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			service := NewDocumentImportService(store, nil, nil, server.URL, strings.Repeat("x", 32), time.Second)
			result, err := service.completeParsedDocuments(context.Background(), "tenant", job, []normalizedImportDocument{{SourceID: job.Sources[0].ID, FileAssetID: "asset-1", DocumentIndex: 0, Content: "第1题 first"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.QuestionCandidates) != 2 || len(result.StructuredIssues) > 16 || result.Status != "review_required" {
				t.Fatalf("unbounded completion: candidates=%d issues=%d status=%s", len(result.QuestionCandidates), len(result.StructuredIssues), result.Status)
			}
			gaps := 0
			for _, issue := range result.StructuredIssues {
				if issue.Code == "POSSIBLE_MISSING_QUESTION" {
					gaps++
				}
			}
			wantGaps := 1
			if number == "4" {
				wantGaps = 2
			}
			if number == strings.Repeat("9", 32) {
				wantGaps = 0
			}
			if gaps != wantGaps {
				t.Fatalf("completion gap diagnostics=%d want=%d", gaps, wantGaps)
			}
			stored, err := store.GetPaperImport(context.Background(), "tenant", job.ID)
			if err != nil || len(stored.StructuredIssues) != len(result.StructuredIssues) {
				t.Fatalf("bounded result not stored: %v", err)
			}
		})
	}
}
