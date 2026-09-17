package paper

import "testing"

func TestSortPaperImportOCRBlocksPreservesReadingOrderInsidePage(t *testing.T) {
	blocks := []PaperImportOCRBlock{
		{SourceID: "second", DocumentIndex: 1, PageNo: 1, BlockID: "b1"},
		{SourceID: "first", DocumentIndex: 0, PageNo: 2, BlockID: "b3"},
		{SourceID: "first", DocumentIndex: 0, PageNo: 1, BlockID: "b10"},
		{SourceID: "first", DocumentIndex: 0, PageNo: 1, BlockID: "b2"},
	}
	sortPaperImportOCRBlocks(blocks)
	got := []string{blocks[0].BlockID, blocks[1].BlockID, blocks[2].BlockID, blocks[3].BlockID}
	want := []string{"b10", "b2", "b3", "b1"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("block order = %#v, want %#v", got, want)
		}
	}
}

func TestPossiblePageMissingIssueIsSuspectedAndGrounded(t *testing.T) {
	sources := []PaperImportSource{{ID: "source", FileAssetID: "file", DocumentIndex: 2}}
	issues := possiblePageMissingIssues(sources, []PaperImportOCRBlock{{SourceID: "source", PageNo: 1}, {SourceID: "source", PageNo: 3}})
	if len(issues) != 1 || issues[0].Code != "POSSIBLE_PAGE_MISSING" || issues[0].Certainty != "suspected" || len(issues[0].SourceRefs) != 1 || issues[0].SourceRefs[0].PageNo != 2 {
		t.Fatalf("unexpected possible page issue: %#v", issues)
	}
}

func TestDirectVisualMediaTypesBypassPageDecoder(t *testing.T) {
	for _, contentType := range []string{"image/png", "image/jpeg; charset=binary", "IMAGE/WEBP"} {
		if !isDirectVisualMediaType(contentType) {
			t.Fatalf("%s should be sent directly to the visual model", contentType)
		}
	}
	for _, contentType := range []string{"image/tiff", "application/pdf", "image/gif"} {
		if isDirectVisualMediaType(contentType) {
			t.Fatalf("%s still requires page rendering", contentType)
		}
	}
}

func TestBuildDecodedVisualParseInputPreservesTextAndUsesRawPages(t *testing.T) {
	job := PaperImportJob{Sources: []PaperImportSource{
		{ID: "scan", FileAssetID: "scan-pdf", DocumentIndex: 0, RoleHint: "question"},
		{ID: "answer", FileAssetID: "answer-docx", DocumentIndex: 1, RoleHint: "answer"},
	}}
	baseDocuments := []PaperImportParseDocument{{
		SourceID: "answer", FileAssetID: "answer-docx", DocumentIndex: 1,
		RoleHint: "answer", Content: "第1题答案A", Blocks: []PaperImportOCRBlock{},
	}}
	pages := []PaperImportDecodedPage{{
		SourceID: "scan", DocumentIndex: 0, PageNo: 1, FileAssetID: "rendered-page",
	}}

	input, err := buildDecodedVisualParseInput(job, baseDocuments, pages)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Documents) != 2 || len(input.Pages) != 1 {
		t.Fatalf("unexpected parse input: %#v", input)
	}
	if input.Documents[0].Content != directVisualDocumentPlaceholder || input.Documents[1].Content != "第1题答案A" {
		t.Fatalf("decoded visual input lost a source: %#v", input.Documents)
	}
}
