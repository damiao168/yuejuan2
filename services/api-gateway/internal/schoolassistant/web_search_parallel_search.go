package schoolassistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

type publicSearchBatchResult struct {
	index   int
	sources []Source
	err     error
}

// searchWebBatch 只执行模型已选定且调用方已预留预算的最多两条查询。
// 返回顺序跟随输入，事件只由请求协程写出；取消或写入失败时收齐已启动请求再返回。
func (s *ConversationService) searchWebBatch(ctx context.Context, queries []string, firstAttempt int, send func(ConversationEvent) error) ([][]Source, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(queries) < 1 || len(queries) > webSearchMaxAttempts || firstAttempt < 0 {
		return nil, reject(400, "invalid_web_search_query", "本轮只能执行一至两条公开查询。")
	}
	for _, query := range queries {
		if strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > webSearchMaxQueryRunes {
			return nil, reject(400, "invalid_web_search_query", "请输入不超过 500 字的公开搜索问题。")
		}
	}
	if s.deps.Search == nil {
		return nil, reject(503, "web_search_unavailable", "联网搜索暂未配置，请关闭联网搜索后提问。")
	}
	batchCtx, cancelBatch := context.WithCancel(ctx)
	results := make(chan publicSearchBatchResult, len(queries))
	launched, collected := 0, 0
	defer func() {
		cancelBatch()
		// 搜索实现须遵守context取消；此处回收所有在途调用，避免函数退出后继续占用连接。
		for collected < launched {
			<-results
			collected++
		}
	}()
	for index, query := range queries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		callID := fmt.Sprintf("web-search-%d", firstAttempt+index+1)
		if err := send(ConversationEvent{Type: "tool_started", CallID: callID, ToolName: "web_search", Label: "搜索公开资料"}); err != nil {
			return nil, err
		}
		launched++
		go func(index int, query string) {
			searchCtx, cancel := context.WithTimeout(batchCtx, webSearchRequestTimeout)
			defer cancel()
			var sources []Source
			err := searchCtx.Err()
			if err == nil {
				sources, err = s.deps.Search.Search(searchCtx, query)
			}
			if searchCtx.Err() != nil {
				err = searchCtx.Err()
			}
			results <- publicSearchBatchResult{index: index, sources: sources, err: err}
		}(index, strings.TrimSpace(query))
	}
	groups, failures := make([][]Source, len(queries)), make([]error, len(queries))
	succeeded := 0
	for collected < launched {
		var result publicSearchBatchResult
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result = <-results:
			collected++
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		status, message := "completed", fmt.Sprintf("取得 %d 条候选资料，正在核实是否适用。", len(result.sources))
		if result.err != nil {
			failure := publicSearchBatchFailure(result.err)
			failures[result.index] = failure
			status, message = "failed", failure.message
		} else {
			groups[result.index] = result.sources
			succeeded++
		}
		callID := fmt.Sprintf("web-search-%d", firstAttempt+result.index+1)
		if err := send(ConversationEvent{Type: "tool_finished", CallID: callID, ToolName: "web_search", Label: "搜索公开资料", Status: status, Message: message}); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if succeeded == 0 {
		// 全部失败时按输入顺序选第一个稳定错误，不能由网络返回先后改变对外结果。
		for _, err := range failures {
			if err != nil {
				return nil, err
			}
		}
	}
	return groups, nil
}

func publicSearchBatchFailure(err error) *queryError {
	var failure *queryError
	if errors.As(err, &failure) {
		return failure
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &queryError{status: 503, code: "web_search_timeout", message: "联网搜索超时，请稍后重试。"}
	}
	return &queryError{status: 503, code: "web_search_failed", message: webSearchFailed}
}
