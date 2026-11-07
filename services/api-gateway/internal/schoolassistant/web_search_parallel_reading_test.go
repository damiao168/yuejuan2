package schoolassistant

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type parallelResearchReader func(context.Context, string) (WebPageContent, error)

func (read parallelResearchReader) Read(ctx context.Context, address string) (WebPageContent, error) {
	return read(ctx, address)
}

func selectedParallelReadService(reader WebPageReader) (*ConversationService, []Source) {
	candidates := make([]Source, 3)
	for index := range candidates {
		candidates[index] = researchSource(fmt.Sprint(index+1), "搜索摘要")
		candidates[index].ID = fmt.Sprintf("candidate-%d", index+1)
	}
	return NewConversationService(NewService(Dependencies{}), ConversationDependencies{Reader: reader}), candidates
}

func waitParallelRead(t *testing.T, ctx context.Context, ch <-chan string) string {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-ctx.Done():
		t.Fatal("selected pages did not start concurrently")
		return ""
	}
}

// 三个读取都等待测试放行；串行实现无法到达三者已启动的屏障。
// 反序完成时仍必须更新原候选，不能因完成顺序改变来源和证据的对应关系。
func TestWebResearchReadsSelectedPagesConcurrentlyAndKeepsSourceIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started, finished := make(chan string, 3), make(chan string, 3)
	releases := map[string]chan struct{}{}
	for index := 1; index <= 3; index++ {
		releases[researchSource(fmt.Sprint(index), "").URL] = make(chan struct{})
	}
	reader := parallelResearchReader(func(ctx context.Context, address string) (WebPageContent, error) {
		started <- address
		select {
		case <-ctx.Done():
			return WebPageContent{}, ctx.Err()
		case <-releases[address]:
			return WebPageContent{URL: address, Text: "通知正文 " + address}, nil
		}
	})
	service, candidates := selectedParallelReadService(reader)
	attempted := map[string]bool{}
	var events []ConversationEvent
	result := make(chan error, 1)
	go func() {
		_, err := service.readWebPageSelection(ctx, webSearchPageSelection{SourceIDs: []string{"candidate-1", "candidate-2", "candidate-3"}, InformationNeeded: "通知时间"}, candidates, attempted, func(event ConversationEvent) error {
			events = append(events, event)
			if event.Type == "tool_finished" {
				finished <- event.CallID
			}
			return nil
		})
		result <- err
	}()
	seen := map[string]bool{}
	for range 3 {
		seen[waitParallelRead(t, ctx, started)] = true
	}
	if len(seen) != 3 {
		t.Fatalf("unexpected reads: %v", seen)
	}
	for index := 2; index >= 0; index-- {
		close(releases[candidates[index].URL])
		if callID := waitParallelRead(t, ctx, finished); callID != "web-fetch-"+candidates[index].ID {
			t.Fatalf("finished source mismatch: %s", callID)
		}
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if len(attempted) != webSearchMaxPageReads || len(events) != 6 {
		t.Fatalf("read budget or events changed: attempted=%v events=%d", attempted, len(events))
	}
	for index, source := range candidates {
		if source.ID != fmt.Sprintf("candidate-%d", index+1) || source.Note != "已读取网页正文。" || source.Excerpt != "通知正文 "+source.URL {
			t.Fatalf("concurrent completion changed source identity: %+v", source)
		}
	}
}

// 用户取消时先通知所有读取退出，再收齐结果；函数返回后不能仍有读取占用网络连接。
func TestWebResearchCancelledParallelReadsAreJoinedBeforeReturning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guardCtx, stopGuard := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopGuard()
	started, cancelled := make(chan string, 3), make(chan string, 3)
	allowExit := make(chan struct{})
	defer close(allowExit)
	reader := parallelResearchReader(func(ctx context.Context, address string) (WebPageContent, error) {
		started <- address
		<-ctx.Done()
		cancelled <- address
		select {
		case <-guardCtx.Done():
		case <-allowExit:
		}
		return WebPageContent{}, ctx.Err()
	})
	service, candidates := selectedParallelReadService(reader)
	finishedEvents := 0
	result := make(chan error, 1)
	go func() {
		_, err := service.readWebPageSelection(ctx, webSearchPageSelection{SourceIDs: []string{"candidate-1", "candidate-2", "candidate-3"}, InformationNeeded: "通知时间"}, candidates, map[string]bool{}, func(event ConversationEvent) error {
			if event.Type == "tool_finished" {
				finishedEvents++
			}
			return nil
		})
		result <- err
	}()
	for range 3 {
		waitParallelRead(t, guardCtx, started)
	}
	cancel()
	for range 3 {
		waitParallelRead(t, guardCtx, cancelled)
	}
	select {
	case err := <-result:
		t.Fatalf("returned before selected readers exited: %v", err)
	default:
	}
	// Guard 的取消只放行测试读取器，不影响被测请求已经取消的事实。
	stopGuard()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || finishedEvents != 0 {
			t.Fatalf("cancelled reads published completion: err=%v events=%d", err, finishedEvents)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled readers were not collected")
	}
}

func TestWebResearchSSEFailureCancelsAlreadyStartedParallelReads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started, exited := make(chan string, 3), make(chan string, 3)
	reader := parallelResearchReader(func(ctx context.Context, address string) (WebPageContent, error) {
		started <- address
		<-ctx.Done()
		exited <- address
		return WebPageContent{}, ctx.Err()
	})
	service, candidates := selectedParallelReadService(reader)
	writeFailure := errors.New("client stream closed")
	startedEvents := 0
	_, err := service.readWebPageSelection(ctx, webSearchPageSelection{SourceIDs: []string{"candidate-1", "candidate-2", "candidate-3"}, InformationNeeded: "通知时间"}, candidates, map[string]bool{}, func(event ConversationEvent) error {
		if event.Type == "tool_finished" {
			t.Fatal("failed stream emitted a page completion")
		}
		startedEvents++
		if startedEvents == 3 {
			// 前两个读取确实已开始，第三个事件写入失败后应取消并收齐它们。
			for range 2 {
				waitParallelRead(t, ctx, started)
			}
			return writeFailure
		}
		return nil
	})
	if !errors.Is(err, writeFailure) || len(exited) != 2 {
		t.Fatalf("stream failure left background reads: err=%v exited=%d", err, len(exited))
	}
	for _, source := range candidates {
		if source.Excerpt != "搜索摘要" || source.Note != "" {
			t.Fatalf("failed stream changed candidates: %+v", source)
		}
	}
}
