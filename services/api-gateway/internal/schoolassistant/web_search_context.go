package schoolassistant

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	publicContextTotalRunes   = 10_000
	publicContextPageRunes    = 2_400
	publicContextSummaryRunes = 500
	publicContextScanBytes    = 256 << 10
	publicContextSeparator    = "\n\n[……省略的原文……]\n\n"
)

// compactPublicSearchCandidates 只构造本轮模型的阅读视图，不改原始来源、编号或顺序。
// 分词和窗口评分只是有界抽取，不保证语义完整；调用方还须同时检查引用在视图和原文中存在。
func compactPublicSearchCandidates(question, informationNeeded string, sources []Source) []Source {
	view := append([]Source(nil), sources...)
	budgets := make([]int, len(sources))
	desired := make([]int, len(sources))
	active := 0
	for i, source := range sources {
		if source.ReleaseVersion != nil {
			version := *source.ReleaseVersion
			view[i].ReleaseVersion = &version
		}
		limit := publicContextSummaryRunes
		if source.Note == "已读取网页正文。" {
			limit = publicContextPageRunes
		}
		desired[i] = contextRuneLength(source.Excerpt, limit)
		if desired[i] > 0 {
			active++
		}
	}
	if active == 0 {
		return view
	}
	remaining := publicContextTotalRunes
	share := remaining / active
	for i := range budgets {
		budgets[i] = min(desired[i], share)
		remaining -= budgets[i]
	}
	// 先公平预留，再分配短摘要没有用完的额度，不能让第一篇长正文吃掉后续来源。
	for remaining > 0 {
		needy := 0
		for i := range budgets {
			if budgets[i] < desired[i] {
				needy++
			}
		}
		if needy == 0 {
			break
		}
		share = max(1, remaining/needy)
		for i := range budgets {
			add := min(desired[i]-budgets[i], share, remaining)
			budgets[i] += add
			remaining -= add
		}
	}
	terms := contextQueryTerms(question, informationNeeded)
	for i := range view {
		view[i].Excerpt = compactPublicContextExcerpt(sources[i].Excerpt, budgets[i], terms)
	}
	return view
}

type publicContextSpan struct{ start, end int }
type publicContextWindow struct {
	span  publicContextSpan
	terms []int
}

func contextRuneLength(text string, limit int) int {
	count := 0
	for range text {
		count++
		if count >= limit {
			break
		}
	}
	return count
}

func contextPrefixEnd(text string, start, end, runes int) int {
	for start < end && runes > 0 {
		_, size := utf8.DecodeRuneInString(text[start:end])
		start += size
		runes--
	}
	return start
}

func contextSuffixStart(text string, start, end, runes int) int {
	for end > start && runes > 0 {
		_, size := utf8.DecodeLastRuneInString(text[start:end])
		end -= size
		runes--
	}
	return end
}

// 正常正文已在读取层限制到12000字；异常大输入只扫描头尾各128KiB，避免全文分词和分配。
func publicContextRegions(text string) []publicContextSpan {
	if len(text) <= publicContextScanBytes {
		return []publicContextSpan{{0, len(text)}}
	}
	left, right := publicContextScanBytes/2, len(text)-publicContextScanBytes/2
	for left > 0 && !utf8.RuneStart(text[left]) {
		left--
	}
	for right < len(text) && !utf8.RuneStart(text[right]) {
		right++
	}
	return []publicContextSpan{{0, left}, {right, len(text)}}
}

func contextQueryTerms(question, informationNeeded string) map[string]int {
	terms := map[string]int{}
	for _, part := range []string{question, informationNeeded} {
		part = part[:contextPrefixEnd(part, 0, len(part), 1_024)]
		partLimit := min(128, len(terms)+64)
		walkContextTerms(part, func(term string) {
			if _, exists := terms[term]; !exists && len(terms) < partLimit {
				terms[term] = len(terms)
			}
		})
	}
	return terms
}

// 汉字按相邻二字匹配，其他文字按完整词和数字匹配；不维护天气或其他业务的特殊词表。
func walkContextTerms(text string, consume func(string)) {
	var previousHan rune
	var word strings.Builder
	flush := func() {
		if word.Len() > 1 {
			consume(word.String())
		}
		word.Reset()
	}
	for _, char := range text {
		if unicode.Is(unicode.Han, char) {
			flush()
			if previousHan != 0 {
				consume(string([]rune{previousHan, char}))
			}
			previousHan = char
			continue
		}
		previousHan = 0
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			if word.Len() < 256 {
				word.WriteRune(unicode.ToLower(char))
			}
		} else {
			flush()
		}
	}
	flush()
}

func compactPublicContextExcerpt(text string, budget int, terms map[string]int) string {
	if budget <= 0 {
		return ""
	}
	end := contextPrefixEnd(text, 0, len(text), budget)
	if end == len(text) {
		return text
	}
	separatorRunes := utf8.RuneCountInString(publicContextSeparator)
	if budget < 400 || len(terms) == 0 {
		return contextFallbackExcerpt(text, budget)
	}
	// 首段保留标题、作者和日期等可能的线索；其余额度由相关词覆盖决定，而非只截网页开头。
	selected := []publicContextSpan{{0, contextPrefixEnd(text, 0, len(text), min(180, budget/3))}}
	windowRunes := min(360, budget-utf8.RuneCountInString(text[:selected[0].end])-separatorRunes)
	var windows []publicContextWindow
	for _, region := range publicContextRegions(text) {
		for start := region.start; start < region.end; {
			windowEnd := contextPrefixEnd(text, start, region.end, windowRunes)
			if windowEnd == region.end {
				start = contextSuffixStart(text, region.start, region.end, windowRunes)
			}
			matched := map[int]bool{}
			walkContextTerms(text[start:windowEnd], func(term string) {
				if id, exists := terms[term]; exists {
					matched[id] = true
				}
			})
			if len(matched) > 0 {
				ids := make([]int, 0, len(matched))
				for id := range matched {
					ids = append(ids, id)
				}
				windows = append(windows, publicContextWindow{publicContextSpan{start, windowEnd}, ids})
			}
			if windowEnd == region.end {
				break
			}
			start = contextPrefixEnd(text, start, region.end, max(40, windowRunes*2/3))
		}
	}
	if len(windows) == 0 {
		return contextFallbackExcerpt(text, budget)
	}
	covered := map[int]bool{}
	walkContextTerms(text[:selected[0].end], func(term string) {
		if id, exists := terms[term]; exists {
			covered[id] = true
		}
	})
	used := make([]bool, len(windows))
	// 最多挑八个窗口；优先覆盖此前没出现的查询词，让多城市、多实体需求各有机会入选。
	for pass := 0; pass < 8; pass++ {
		best, bestScore := -1, 0
		var bestSpans []publicContextSpan
		for i, window := range windows {
			if used[i] {
				continue
			}
			score := len(window.terms)
			for _, id := range window.terms {
				if !covered[id] {
					score += 4
				}
			}
			if score <= bestScore {
				continue
			}
			spans := mergeContextSpans(append(append([]publicContextSpan(nil), selected...), window.span))
			if contextSpansRunes(text, spans, separatorRunes) > budget || sameContextSpans(spans, selected) {
				continue
			}
			best, bestScore, bestSpans = i, score, spans
		}
		if best < 0 {
			break
		}
		used[best] = true
		selected = bestSpans
		for _, id := range windows[best].terms {
			covered[id] = true
		}
	}
	return renderContextSpans(text, selected)
}

func contextFallbackExcerpt(text string, budget int) string {
	separatorRunes := utf8.RuneCountInString(publicContextSeparator)
	if budget <= separatorRunes+20 {
		return text[:contextPrefixEnd(text, 0, len(text), budget)]
	}
	contentRunes := budget - separatorRunes
	left := contextPrefixEnd(text, 0, len(text), (contentRunes+1)/2)
	right := contextSuffixStart(text, left, len(text), contentRunes/2)
	return renderContextSpans(text, []publicContextSpan{{0, left}, {right, len(text)}})
}

func mergeContextSpans(spans []publicContextSpan) []publicContextSpan {
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	result := make([]publicContextSpan, 0, len(spans))
	for _, span := range spans {
		if len(result) > 0 && span.start <= result[len(result)-1].end {
			result[len(result)-1].end = max(result[len(result)-1].end, span.end)
		} else {
			result = append(result, span)
		}
	}
	return result
}

func sameContextSpans(a, b []publicContextSpan) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contextSpansRunes(text string, spans []publicContextSpan, separatorRunes int) int {
	count := max(0, len(spans)-1) * separatorRunes
	for _, span := range spans {
		count += utf8.RuneCountInString(text[span.start:span.end])
	}
	return count
}

func renderContextSpans(text string, spans []publicContextSpan) string {
	parts := make([]string, 0, len(spans))
	for _, span := range mergeContextSpans(spans) {
		if span.end > span.start {
			parts = append(parts, text[span.start:span.end])
		}
	}
	return strings.Join(parts, publicContextSeparator)
}
