package review

import (
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"fmt"
	"sync"
)

type MemoryStore struct {
	autoSubmitBookmarks AutoSubmitBookmarkGuard
	receipts            commandreceipt.Memory
	mu                  sync.RWMutex
	next                int
	contexts            map[string]Context
	tasks               map[string]ReviewTask
	claims              map[string]memoryTaskClaim
	drafts              map[string]ReviewDraft
	grades              map[string][]HumanGrade
	policies            map[string]DoubleMarkPolicy
	sessions            map[string]DoubleMarkSession
	sessionByTask       map[string]string
	arbitrations        map[string]ArbitrationTask
	finalGrades         map[string]FinalGrade
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		next:          1,
		contexts:      map[string]Context{},
		tasks:         map[string]ReviewTask{},
		claims:        map[string]memoryTaskClaim{},
		drafts:        map[string]ReviewDraft{},
		grades:        map[string][]HumanGrade{},
		policies:      map[string]DoubleMarkPolicy{},
		sessions:      map[string]DoubleMarkSession{},
		sessionByTask: map[string]string{},
		arbitrations:  map[string]ArbitrationTask{},
		finalGrades:   map[string]FinalGrade{},
	}
}

func (s *MemoryStore) AddContext(tenantID string, segmentID string, ctx Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx.AnswerSegmentID = segmentID
	ctx.AISuggestion = cloneMap(ctx.AISuggestion)
	s.contexts[key(tenantID, segmentID)] = ctx
}

func key(tenantID string, id string) string {
	return tenantID + "|" + id
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}
