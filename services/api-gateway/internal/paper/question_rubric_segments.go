package paper

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	maxQuestionRubricSegments = 500
	maxQuestionRubricSpans    = 1000
)

// QuestionRubricSolutionSpan 引用已保存解答步骤的原文，不接收浏览器改写后的内容。
// Start/End 是首尾去空白后文本的 UTF-16 单元偏移，左闭右开，与浏览器 String.slice 一致。
type QuestionRubricSolutionSpan struct {
	SourceID string `json:"source_id"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}

// QuestionRubricSolutionSegment 允许把相邻原文范围合成一个配分单元，跨度仍须依原文顺序完整覆盖。
type QuestionRubricSolutionSegment struct {
	Spans []QuestionRubricSolutionSpan `json:"spans"`
}

type questionRubricSegmentSource struct {
	id         string
	text       string
	units      int
	boundaries map[int]int
}

func questionRubricSegmentSteps(steps []SolutionStep, segments []QuestionRubricSolutionSegment) ([]SolutionStep, string, error) {
	if len(segments) == 0 || len(segments) > maxQuestionRubricSegments {
		return nil, "", ErrInvalidInput
	}
	sources := []questionRubricSegmentSource{}
	context := []string{}
	for index, step := range steps {
		text := trimQuestionRubricSource(step.Content)
		if text == "" {
			continue
		}
		source := questionRubricSegmentSource{id: fmt.Sprintf("step-%d", index+1), text: text, boundaries: map[int]int{}}
		for position, character := range text {
			source.boundaries[source.units] = position
			source.units++
			if character > 0xffff {
				source.units++
			}
		}
		source.boundaries[source.units] = len(text)
		sources = append(sources, source)
		context = append(context, text)
	}
	if len(sources) == 0 {
		return nil, "", ErrInvalidInput
	}

	result := make([]SolutionStep, 0, len(segments))
	sourceIndex, offset, spanCount := 0, 0, 0
	for _, segment := range segments {
		spanCount += len(segment.Spans)
		if len(segment.Spans) == 0 || spanCount > maxQuestionRubricSpans {
			return nil, "", ErrInvalidInput
		}
		var content strings.Builder
		for _, span := range segment.Spans {
			if sourceIndex >= len(sources) {
				return nil, "", ErrInvalidInput
			}
			source := sources[sourceIndex]
			startByte, validStart := source.boundaries[span.Start]
			endByte, validEnd := source.boundaries[span.End]
			// 顺序游标同时拒绝漏字、重复引用和倒序；UTF-16 边界表拒绝从 emoji 代理对中间切开。
			if span.SourceID != source.id || span.Start != offset || span.End <= span.Start || !validStart || !validEnd {
				return nil, "", ErrInvalidInput
			}
			if content.Len() > 0 && span.Start == 0 {
				content.WriteString("\n\n")
			}
			content.WriteString(source.text[startByte:endByte])
			offset = span.End
			if offset == source.units {
				sourceIndex++
				offset = 0
			}
		}
		if strings.TrimSpace(content.String()) == "" {
			return nil, "", ErrInvalidInput
		}
		result = append(result, SolutionStep{StepNo: len(result) + 1, Content: content.String()})
	}
	if sourceIndex != len(sources) || offset != 0 {
		return nil, "", ErrInvalidInput
	}
	// 拆分后的公式可能只是完整 aligned 环境的一部分；上下文从当前仓储步骤重建，不能沿用可能过期的 RawText。
	return result, strings.Join(context, "\n\n"), nil
}

func trimQuestionRubricSource(text string) string {
	// 偏移来自浏览器 trim 后的文本；Go 的 TrimSpace 对 BOM、NEL 的处理不同，不能据此改变来源长度。
	return strings.TrimFunc(text, func(character rune) bool {
		return character == '\ufeff' || character != '\u0085' && unicode.IsSpace(character)
	})
}
