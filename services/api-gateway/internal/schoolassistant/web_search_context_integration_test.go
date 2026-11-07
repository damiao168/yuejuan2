package schoolassistant

import (
	"strings"
	"testing"
)

func TestPublicSearchRejectsEvidenceOmittedFromModelView(t *testing.T) {
	question := "核对公开通知的结束时间"
	hidden := "仅在未提供段落中的隐藏事实471。"
	text := strings.Repeat("背景材料与无关信息。", 230) + hidden + strings.Repeat("其他背景材料。", 650) + "公开通知的结束时间是下午三点。"
	source := researchSource("notice", "需要查看通知正文。")
	view := compactPublicSearchCandidates(question, "结束时间", []Source{{ID: "candidate-1", Excerpt: text, Note: "已读取网页正文。"}})
	if len(view) != 1 || strings.Contains(view[0].Excerpt, hidden) {
		t.Fatal("fixture must place the hidden fact outside the provided excerpts")
	}
	reader := &researchPageReader{pages: map[string]WebPageContent{source.URL: {URL: source.URL, Text: text}}}
	model := &publicLoopModel{replies: []string{
		publicLoopSearch(question, question),
		`{"action":"read","source_ids":["candidate-1"],"information_needed":"结束时间"}`,
		publicLoopAnswer("隐藏事实已核实。[1]", webSearchEvidence{SourceID: "candidate-1", Quote: hidden}),
	}}
	events, _, err := runPublicLoopFixture(t, model, &researchSearchProvider{results: [][]Source{{source}}}, reader)
	code, _ := conversationError(err)
	if code != "invalid_web_search_action" || len(model.requests) != 3 {
		t.Fatalf("unseen source text was accepted: code=%s calls=%d", code, len(model.requests))
	}
	for _, event := range events {
		if event.Type == "sources" || event.Type == "done" {
			t.Fatal("unseen source text produced a completed answer")
		}
	}
}

func TestPublicSearchSourcesRejectsQuoteAcrossExtractedWindows(t *testing.T) {
	state := publicSearchLoopState{Candidates: []Source{{ID: "candidate-1", Excerpt: "第一段原文。中间有未提供的材料。第二段原文。"}}}
	action := webSearchAction{Action: "answer", Answer: "说明。[1]", Evidence: []webSearchEvidence{{SourceID: "candidate-1", Quote: "第一段原文。\n…\n第二段原文。"}}}
	if _, err := publicSearchActionSources(state, action); err == nil {
		t.Fatal("a synthetic excerpt boundary was accepted as a continuous source quote")
	}
}
