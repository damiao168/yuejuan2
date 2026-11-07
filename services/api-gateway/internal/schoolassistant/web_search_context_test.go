package schoolassistant

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func contextReadSource(id, text string) Source {
	return Source{ID: id, Kind: "web", Label: "公开资料", URL: "https://public.example/" + id, Note: "已读取网页正文。", Excerpt: text}
}

func assertContextPiecesFromOriginal(t *testing.T, excerpt, original string) {
	t.Helper()
	last := 0
	for _, piece := range strings.Split(excerpt, publicContextSeparator) {
		if piece == "" {
			t.Fatal("empty context piece")
		}
		index := strings.Index(original[last:], piece)
		if index < 0 {
			t.Fatal("context piece is not a continuous original substring in source order")
		}
		last += index + len(piece)
	}
}

func TestPublicContextFindsRelatedChineseTailAndPreservesHeader(t *testing.T) {
	text := "公开通知\n发布日期：2026年11月2日。\n" + strings.Repeat("导航及无关内容。", 900) + "深圳专项实施办法：本次项目于十一月正式开始。" + strings.Repeat("其他说明。", 70)
	sources := []Source{contextReadSource("candidate-7", text)}
	view := compactPublicSearchCandidates("深圳专项实施办法何时正式开始？", "核实实施日期", sources)
	if !strings.Contains(view[0].Excerpt, "深圳专项实施办法") || !strings.Contains(view[0].Excerpt, "2026年11月2日") {
		t.Fatal("context lost relevant tail or header date")
	}
	if utf8.RuneCountInString(view[0].Excerpt) > publicContextPageRunes {
		t.Fatal("context exceeds per-page budget")
	}
	assertContextPiecesFromOriginal(t, view[0].Excerpt, text)
}

func TestPublicContextCoversDistinctCitiesAndEnglishTerms(t *testing.T) {
	for _, test := range []struct{ name, question, first, second string }{
		{"Chinese entities", "对比北京与上海的公共交通方案", "北京公共交通方案：增加轨道交通班次。", "上海公共交通方案：扩大公共交通线路。"},
		{"English words and date", "Compare Austin and Seattle public transport changes in 2026", "AUSTIN transit update 2026-11-02: new routes begin.", "Seattle transport plan: additional rapid transit service."},
	} {
		t.Run(test.name, func(t *testing.T) {
			text := "资料目录与发布日期\n" + strings.Repeat("说明材料。", 350) + test.first + strings.Repeat("其他段落。", 950) + test.second + strings.Repeat("附录内容。", 120)
			view := compactPublicSearchCandidates(test.question, "核实两个地区的安排", []Source{contextReadSource("candidate-1", text)})
			if !strings.Contains(view[0].Excerpt, test.first) || !strings.Contains(view[0].Excerpt, test.second) {
				t.Fatal("context did not retain separate related entities")
			}
			assertContextPiecesFromOriginal(t, view[0].Excerpt, text)
		})
	}
}

func TestPublicContextFallbackAndShortSources(t *testing.T) {
	text := "页首资料与日期\n" + strings.Repeat("完全无关的长段落。", 900) + "页末附录与联系方式"
	for _, question := range []string{"不存在的检索主题", ""} {
		view := compactPublicSearchCandidates(question, "", []Source{contextReadSource("candidate-1", text)})
		if !strings.HasPrefix(view[0].Excerpt, "页首资料") || !strings.HasSuffix(view[0].Excerpt, "页末附录与联系方式") {
			t.Fatal("fallback lost beginning or end")
		}
		assertContextPiecesFromOriginal(t, view[0].Excerpt, text)
	}
	short := []Source{contextReadSource("candidate-1", "无需删减的短正文。"), {ID: "candidate-2", Excerpt: "短搜索摘要。"}}
	if view := compactPublicSearchCandidates("问题", "", short); !reflect.DeepEqual(view, short) {
		t.Fatal("short sources changed")
	}
	if view := compactPublicSearchCandidates("", "", nil); view != nil {
		t.Fatal("nil sources changed")
	}
}

func TestPublicContextFairTotalBudgetStableIDsAndIndependentCopy(t *testing.T) {
	version := 3
	sources := make([]Source, 10)
	for i := range sources {
		sources[i] = contextReadSource(string(rune('a'+i)), "资料开头。"+strings.Repeat("长正文材料。", 3000)+"相关主题位于末尾。")
		sources[i].ReleaseVersion = &version
	}
	before := append([]Source(nil), sources...)
	view := compactPublicSearchCandidates("相关主题", "", sources)
	total := 0
	for i := range view {
		if view[i].ID != sources[i].ID {
			t.Fatal("source order or stable ID changed")
		}
		count := utf8.RuneCountInString(view[i].Excerpt)
		if count < 500 {
			t.Fatal("a late candidate was starved of its context share")
		}
		total += count
		assertContextPiecesFromOriginal(t, view[i].Excerpt, sources[i].Excerpt)
	}
	if total > publicContextTotalRunes {
		t.Fatal("context exceeds global budget")
	}
	if !reflect.DeepEqual(sources, before) {
		t.Fatal("original source changed")
	}
	view[0].Excerpt = "changed"
	*view[0].ReleaseVersion = 8
	if sources[0].Excerpt == "changed" || *sources[0].ReleaseVersion != 3 {
		t.Fatal("view aliases original mutable state")
	}
}

func TestPublicContextUnReadSummaryAndHugeInputRemainBounded(t *testing.T) {
	text := "标题和日期\n" + strings.Repeat("无关信息。", 1_000_000) + "目标资料位于文末，实施时间为十一月。"
	view := compactPublicSearchCandidates(strings.Repeat("很长的查询。", 10_000), "目标资料实施时间", []Source{contextReadSource("candidate-1", text), {ID: "candidate-2", Excerpt: text}})
	if utf8.RuneCountInString(view[0].Excerpt) > publicContextPageRunes || utf8.RuneCountInString(view[1].Excerpt) > publicContextSummaryRunes {
		t.Fatal("huge input escaped context bounds")
	}
	for i := range view {
		assertContextPiecesFromOriginal(t, view[i].Excerpt, text)
	}
	if regions := publicContextRegions(text); len(regions) != 2 || regions[0].end-regions[0].start+regions[1].end-regions[1].start > publicContextScanBytes {
		t.Fatal("huge input scan is not bounded")
	}
	// 问题过长可能占满词表；正常公开问题仍应能定位异常长正文尾部，而不需要扫描全部内容。
	if tail := compactPublicSearchCandidates("目标资料实施时间", "", []Source{contextReadSource("candidate-1", text)}); !strings.Contains(tail[0].Excerpt, "目标资料位于文末") {
		t.Fatal("bounded scan lost related tail")
	}
}

func TestPublicContextSpliceCannotBecomeOriginalEvidence(t *testing.T) {
	original := "真实开头。" + strings.Repeat("中间正文。", 900) + "真实结尾。"
	view := compactPublicSearchCandidates("无匹配词", "", []Source{contextReadSource("candidate-1", original)})
	parts := strings.Split(view[0].Excerpt, publicContextSeparator)
	if len(parts) != 2 {
		t.Fatal("expected separated fallback windows")
	}
	quote := parts[0][len(parts[0])-len("。"):] + publicContextSeparator + parts[1][:len("正")]
	if !strings.Contains(view[0].Excerpt, quote) || validWebSearchQuotes(webSearchEvidence{SourceID: "candidate-1", Quote: quote}, original) {
		t.Fatal("cross-window splice passed original evidence validation")
	}
	if !validWebSearchQuotes(webSearchEvidence{SourceID: "candidate-1", Quote: "真实结尾。"}, original) {
		t.Fatal("continuous original evidence rejected")
	}
}
