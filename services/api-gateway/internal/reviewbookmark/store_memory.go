package reviewbookmark

import (
	"context"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"github.com/google/uuid"
)

type MemoryStore struct {
	mu       sync.Mutex
	provider SourceProvider
	records  map[string]Record
	targets  map[string]string
	receipts commandreceipt.Memory
}

func NewMemoryStore(provider SourceProvider) *MemoryStore {
	return &MemoryStore{provider: provider, records: map[string]Record{}, targets: map[string]string{}}
}

// WithProvider 供应用装配时重绑同一实例的数据来源，已保存书签不受影响。
func (s *MemoryStore) WithProvider(provider SourceProvider) *MemoryStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider = provider
	return s
}

// WithReviewTaskBookmarkLock 供正式自动提交核对个人标记；不调用来源提供器，任务归属由回调在任务锁内检查。
// 先持标记锁再进入任务锁，与 Put 的来源读取顺序相同，避免检查标记后又被并发新增。
func (s *MemoryStore) WithReviewTaskBookmarkLock(_ context.Context, tenantID, reviewerID, taskID string, operation func(marked bool) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.targets[targetKey(auth.User{TenantID: tenantID, ID: reviewerID}, taskID)]
	record, found := s.records[id]
	marked := found && record.TenantID == tenantID && record.OwnerID == reviewerID && (record.ReviewTaskID == taskID || record.SeedTaskID == taskID) && record.DeletedAt == nil
	return operation(marked)
}

func targetKey(user auth.User, task string) string { return user.TenantID + ":" + user.ID + ":" + task }

func (s *MemoryStore) resolve(ctx context.Context, user auth.User, task string) (Source, error) {
	if err := requireWork(user); err != nil {
		return Source{}, err
	}
	if s.provider == nil {
		return Source{}, ErrNotFound
	}
	return s.provider.Resolve(ctx, user, task)
}

func (s *MemoryStore) State(ctx context.Context, user auth.User, task string) (Bookmark, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := s.resolve(ctx, user, task)
	if err != nil {
		return Bookmark{}, err
	}
	id := s.targets[targetKey(user, task)]
	if id == "" {
		return Bookmark{TaskID: task, CanEditReason: !source.Archived}, nil
	}
	return project(s.records[id], source), nil
}

func (s *MemoryStore) GetByID(ctx context.Context, user auth.User, id string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireWork(user); err != nil {
		return Record{}, err
	}
	record, ok := s.records[id]
	if !ok || record.TenantID != user.TenantID || record.OwnerID != user.ID || record.DeletedAt != nil {
		return Record{}, ErrNotFound
	}
	if _, err := s.resolve(ctx, user, record.TaskID); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s *MemoryStore) Put(ctx context.Context, user auth.User, task string, input PutInput) (Acknowledgement, error) {
	original := input
	input, err := normalizePut(input)
	if err != nil {
		return Acknowledgement{}, err
	}
	ctx = commandreceipt.WithInput(ctx, original)
	s.mu.Lock()
	defer s.mu.Unlock()
	// 即使命中历史回执仍先检查当前源权限；回执不能成为永久材料访问凭证。
	source, err := s.resolve(ctx, user, task)
	if err != nil {
		return Acknowledgement{}, err
	}
	var ack Acknowledgement
	loaded, err := s.receipts.Load(ctx, user.TenantID, user.ID, "personal_review_bookmark.put", task, input, &ack)
	if err != nil || loaded {
		return ack, err
	}
	if source.Archived {
		return ack, ErrArchived
	}
	id := s.targets[targetKey(user, task)]
	record := s.records[id]
	if record.Revision != input.ExpectedRevision {
		return ack, &RevisionConflict{Revision: record.Revision}
	}
	now := time.Now().UTC()
	if id == "" {
		if (source.ReviewTaskID == "") == (source.SeedTaskID == "") {
			return ack, ErrNotFound
		}
		id = uuid.NewString()
		record = Record{ID: id, TenantID: user.TenantID, OwnerID: user.ID, TaskID: task, ReviewTaskID: source.ReviewTaskID, SeedTaskID: source.SeedTaskID, CreatedAt: now}
	}
	if record.Revision == 0 || record.DeletedAt != nil || record.Reason != input.Reason {
		if record.Revision == 0 || record.DeletedAt != nil {
			record.MarkedAt = now
			record.ObservedCropSHA256 = source.ObservedCropSHA256
			record.ObservedSnapshotID = source.ObservedSnapshotID
		}
		record.Reason = input.Reason
		record.DeletedAt = nil
		record.Revision++
		record.UpdatedAt = now
	}
	ack = Acknowledgement{BookmarkID: id, Revision: record.Revision, Marked: true}
	if err := s.receipts.Save(ctx, user.TenantID, user.ID, "personal_review_bookmark.put", task, input, ack); err != nil {
		return Acknowledgement{}, err
	}
	s.records[id] = record
	s.targets[targetKey(user, task)] = id
	return ack, nil
}

func (s *MemoryStore) Delete(ctx context.Context, user auth.User, id string, input DeleteInput) (Acknowledgement, error) {
	if err := requireAccount(user); err != nil {
		return Acknowledgement{}, err
	}
	if input.ExpectedRevision < 0 {
		return Acknowledgement{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok || record.TenantID != user.TenantID || record.OwnerID != user.ID {
		return Acknowledgement{}, ErrNotFound
	}
	var ack Acknowledgement
	loaded, err := s.receipts.Load(ctx, user.TenantID, user.ID, "personal_review_bookmark.delete", id, input, &ack)
	if err != nil || loaded {
		return ack, err
	}
	if record.Revision != input.ExpectedRevision {
		return ack, &RevisionConflict{Revision: record.Revision}
	}
	if record.DeletedAt == nil {
		now := time.Now().UTC()
		record.DeletedAt = &now
		record.UpdatedAt = now
		record.Reason = ""
		record.Revision++
	}
	ack = Acknowledgement{BookmarkID: id, Revision: record.Revision, Marked: false}
	if err := s.receipts.Save(ctx, user.TenantID, user.ID, "personal_review_bookmark.delete", id, input, ack); err != nil {
		return Acknowledgement{}, err
	}
	s.records[id] = record
	return ack, nil
}

func (s *MemoryStore) List(ctx context.Context, user auth.User, filter ListFilter) (ListResult, error) {
	if err := requireWork(user); err != nil {
		return ListResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records := []Record{}
	sources := map[string]Source{}
	for _, record := range s.records {
		if record.TenantID != user.TenantID || record.OwnerID != user.ID || record.DeletedAt != nil {
			continue
		}
		source, err := s.resolve(ctx, user, record.TaskID)
		if sourceUnavailable(err) {
			continue
		}
		if err != nil {
			return ListResult{}, err
		}
		records = append(records, record)
		sources[record.TaskID] = source
	}
	return buildList(user, filter, records, sources)
}

func (s *MemoryStore) Cleanup(ctx context.Context, user auth.User) ([]CleanupItem, error) {
	if err := requireAccount(user); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []CleanupItem{}
	for _, record := range s.records {
		if record.TenantID != user.TenantID || record.OwnerID != user.ID || record.DeletedAt != nil {
			continue
		}
		_, err := s.resolve(ctx, user, record.TaskID)
		if err == nil {
			continue
		}
		if !sourceUnavailable(err) {
			return nil, err
		}
		items = append(items, CleanupItem{BookmarkID: record.ID, Revision: record.Revision})
	}
	return items, nil
}
