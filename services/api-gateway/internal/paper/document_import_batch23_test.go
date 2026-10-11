package paper

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// 合成依赖只记录读取失败后的分派，不模拟数据库或执行器的重试保证。
type batch23SourceFailureStore struct {
	PaperImportRepository
	calls                  int
	tenant, importID, code string
	details                []string
	context                context.Context
	failure                error
}

func (s *batch23SourceFailureStore) FailPaperImport(ctx context.Context, tenantID, importID, code string, details []string) (PaperImportJob, error) {
	s.calls++
	s.context, s.tenant, s.importID, s.code, s.details = ctx, tenantID, importID, code, details
	return PaperImportJob{ID: "failed-job"}, s.failure
}

type batch23DurableSourceStore struct {
	*batch23SourceFailureStore
	paperImportDispatchStore
}

type batch23UnavailableSource struct {
	DocumentSourceLoader
	err error
}

func (s batch23UnavailableSource) Load(context.Context, string, string) (string, error) {
	return "", s.err
}

func TestBatch23SourceReadFailureKeepsStorageRouting(t *testing.T) {
	readErr := errors.New("synthetic source unavailable")
	failErr := errors.New("synthetic failure write rejected")
	for _, test := range []paperSourceReadFailureCase{
		{name: "内存存储返回记录的失败任务", wantID: "failed-job"},
		{name: "失败状态写入错误优先返回", failure: failErr, wantErr: failErr},
		{name: "持久化存储交回原读取错误", durable: true, wantErr: readErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertPaperSourceReadStorageRouting(t, test, readErr)
		})
	}
}

func TestBatch23IncrementalInputKeepsSelectedOrderAndGlobalIssues(t *testing.T) {
	input := PaperImportParseRequest{
		Documents: []PaperImportParseDocument{{SourceID: "old"}, {SourceID: "new-b"}, {SourceID: "new-a"}},
		Pages:     []PaperImportDecodedPage{{SourceID: "old", PageNo: 1}, {SourceID: "new-a", PageNo: 2}},
		ExtraIssues: []PaperImportIssue{
			{Code: "global"},
			{Code: "old-only", SourceRefs: []PaperImportSourceRef{{SourceID: "old"}}},
			{Code: "multiple-matches", SourceRefs: []PaperImportSourceRef{{SourceID: "old"}, {SourceID: "new-a"}, {SourceID: "new-b"}}},
			{Code: "unknown", SourceRefs: []PaperImportSourceRef{{SourceID: "unknown"}}},
		},
	}
	for _, test := range []struct {
		name     string
		command  string
		ids      []string
		filtered bool
	}{
		{name: "追加资料只筛选新增来源且每条问题保留一次", command: "add_sources", ids: []string{"new-a", "new-b", "new-a"}, filtered: true},
		{name: "无匹配文档时沿用完整输入", command: "add_sources", ids: []string{"unknown"}},
		{name: "无新增来源时沿用完整输入", command: "add_sources"},
		{name: "替换命令不能使用追加筛选", command: "replace_sources", ids: []string{"new-a"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			documents, pages, issues := incrementalPaperImportParseInput(PaperImportRunBinding{CommandType: test.command, NewSourceIDs: test.ids, Input: input})
			want := input
			if test.filtered {
				want.Documents = input.Documents[1:]
				want.Pages = input.Pages[1:]
				want.ExtraIssues = []PaperImportIssue{input.ExtraIssues[0], input.ExtraIssues[2]}
			}
			if !reflect.DeepEqual(documents, want.Documents) || !reflect.DeepEqual(pages, want.Pages) || !reflect.DeepEqual(issues, want.ExtraIssues) {
				t.Fatalf("documents=%#v pages=%#v issues=%#v, want %#v", documents, pages, issues, want)
			}
		})
	}
}

func TestBatch23MissingPagesIgnoreNonpositiveAndUnknownSourceBlocks(t *testing.T) {
	sources := []PaperImportSource{{ID: "a", FileAssetID: "file-a", DocumentIndex: 2}, {ID: "b", FileAssetID: "file-b", DocumentIndex: 1}, {ID: "empty"}}
	blocks := []PaperImportOCRBlock{
		{SourceID: "a", PageNo: 0}, {SourceID: "a", PageNo: -1},
		{SourceID: "a", PageNo: 1}, {SourceID: "a", PageNo: 4}, {SourceID: "a", PageNo: 4},
		{SourceID: "b", PageNo: 2}, {SourceID: "unknown", PageNo: 9},
	}
	issues := possiblePageMissingIssues(sources, blocks)
	want := []PaperImportSourceRef{
		{SourceID: "a", FileAssetID: "file-a", DocumentIndex: 2, PageNo: 2},
		{SourceID: "a", FileAssetID: "file-a", DocumentIndex: 2, PageNo: 3},
		{SourceID: "b", FileAssetID: "file-b", DocumentIndex: 1, PageNo: 1},
	}
	if len(issues) != len(want) {
		t.Fatalf("missing page issues=%#v", issues)
	}
	for index, issue := range issues {
		if issue.Code != "POSSIBLE_PAGE_MISSING" || issue.Certainty != "suspected" || !reflect.DeepEqual(issue.SourceRefs, []PaperImportSourceRef{want[index]}) {
			t.Fatalf("issue %d=%#v, want source %#v", index, issue, want[index])
		}
	}
}

type paperSourceReadFailureCase struct {
	name    string
	durable bool
	failure error
	wantErr error
	wantID  string
}

func assertPaperSourceReadStorageRouting(t *testing.T, test paperSourceReadFailureCase, readErr error) {
	t.Helper()
	ctx := context.Background()
	store := &batch23SourceFailureStore{failure: test.failure}
	var repository PaperImportRepository = store
	if test.durable {
		repository = &batch23DurableSourceStore{batch23SourceFailureStore: store}
	}
	service := &DocumentImportService{store: repository, sourceLoader: batch23UnavailableSource{err: readErr}}
	job := PaperImportJob{ID: "import", Sources: []PaperImportSource{{ID: "source", FileAssetID: "file"}}}
	got, err := service.processSources(ctx, "tenant", "user", job)
	if !errors.Is(err, test.wantErr) || got.ID != test.wantID {
		t.Fatalf("job=%#v error=%v, want id=%q error=%v", got, err, test.wantID, test.wantErr)
	}
	if test.durable {
		if store.calls != 0 {
			t.Fatalf("durable source failure wrote status %d times", store.calls)
		}
		return
	}
	if store.calls != 1 || store.context != ctx || store.tenant != "tenant" || store.importID != "import" || store.code != "source_text_unavailable" || !reflect.DeepEqual(store.details, []string{readErr.Error()}) {
		t.Fatalf("unexpected failure write: %#v", store)
	}
}
