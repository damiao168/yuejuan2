package reviewannotation

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu                         sync.RWMutex
	next                       int
	annotations                map[string]Annotation
	templates                  map[string]CommentTemplate
	taskRefs                   map[string]memoryTaskReference
	published                  map[string]bool
	studentQuestionAnnotations map[string][]StudentAnnotation
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		next: 1, annotations: map[string]Annotation{}, templates: map[string]CommentTemplate{},
		taskRefs: map[string]memoryTaskReference{}, published: map[string]bool{},
		studentQuestionAnnotations: map[string][]StudentAnnotation{},
	}
}

type memoryTaskReference struct {
	submissionID, answerSegmentID, submissionPageID string
}

// 集成内存服务或测试通过这两个方法注入其他领域维护的任务关联和发布状态；
// 生产数据源仍由对应领域的存储实现提供。
func (s *MemoryStore) SetTaskReference(tenantID, taskID, submissionID, answerSegmentID, submissionPageID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskRefs[memoryKey(tenantID, taskID)] = memoryTaskReference{submissionID, answerSegmentID, submissionPageID}
}

func (s *MemoryStore) SetSubmissionPublished(tenantID, submissionID string, published bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.published[memoryKey(tenantID, submissionID)] = published
}

// 测试夹具只保存学生安全 DTO，模拟 PostgreSQL 在发布投影上的查询结果；
// 即使传入私有批注，也不会被学生查询重新暴露。
func (s *MemoryStore) SetStudentQuestionAnnotations(tenantID, examID, studentID, questionID string, annotations []Annotation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.studentQuestionAnnotations[studentQuestionKey(tenantID, examID, studentID, questionID)] = cloneStudentAnnotations(StudentAnnotations(annotations))
}

func (s *MemoryStore) CreateAnnotation(_ context.Context, tenantID, reviewTaskID, actorID string, input CreateAnnotationInput) (Annotation, error) {
	input = normalizeAnnotationInput(input)
	if tenantID == "" || reviewTaskID == "" || actorID == "" || validateAnnotationInput(input) != nil {
		return Annotation{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	// 集成夹具未注入任务关联时，用任务 ID 占位，保持内存实现可独立测试；
	// PostgreSQL 实现则要求真实任务和答案段存在，不会采用此回退。
	reference, ok := s.taskRefs[memoryKey(tenantID, reviewTaskID)]
	if !ok {
		reference = memoryTaskReference{reviewTaskID, reviewTaskID, reviewTaskID}
	}
	item := Annotation{
		ID: s.id("review-annotation"), TenantID: tenantID, ReviewTaskID: reviewTaskID,
		AnswerSegmentID: reference.answerSegmentID, SubmissionPageID: reference.submissionPageID,
		Type: input.Type, Geometry: input.Geometry, Payload: clonePayload(input.Payload),
		Content: input.Content, Visibility: input.Visibility, Revision: 1,
		CreatedBy: actorID, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now,
	}
	s.annotations[memoryKey(tenantID, item.ID)] = item
	return cloneAnnotation(item), nil
}

// 学生只能看到已发布提交中的公开批注；未发布时返回空集合而不是内部错误。
func (s *MemoryStore) ListStudentAnnotations(_ context.Context, tenantID, submissionID string) ([]StudentAnnotation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.published[memoryKey(tenantID, submissionID)] {
		return []StudentAnnotation{}, nil
	}
	items := []Annotation{}
	for _, item := range s.annotations {
		reference := s.taskRefs[memoryKey(tenantID, item.ReviewTaskID)]
		if item.TenantID == tenantID && reference.submissionID == submissionID {
			items = append(items, cloneAnnotation(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return StudentAnnotations(items), nil
}

func (s *MemoryStore) ListStudentQuestionAnnotations(_ context.Context, tenantID, examID, studentID, questionID string) ([]StudentAnnotation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneStudentAnnotations(s.studentQuestionAnnotations[studentQuestionKey(tenantID, examID, studentID, questionID)]), nil
}

func (s *MemoryStore) ListAnnotations(_ context.Context, tenantID, reviewTaskID string) ([]Annotation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Annotation{}
	for _, item := range s.annotations {
		if item.TenantID == tenantID && item.ReviewTaskID == reviewTaskID {
			out = append(out, cloneAnnotation(item))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *MemoryStore) GetAnnotation(_ context.Context, tenantID, id string) (Annotation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.annotations[memoryKey(tenantID, id)]
	if !ok {
		return Annotation{}, ErrNotFound
	}
	return cloneAnnotation(item), nil
}

// 更新必须携带读取时的 revision；并发保存使用旧版本会冲突，避免后写入覆盖先写入。
func (s *MemoryStore) UpdateAnnotation(_ context.Context, tenantID, id, actorID string, input UpdateAnnotationInput) (Annotation, error) {
	normalized := normalizeAnnotationInput(CreateAnnotationInput{
		Type: input.Type, Geometry: input.Geometry, Payload: input.Payload,
		Content: input.Content, Visibility: input.Visibility,
	})
	if tenantID == "" || id == "" || actorID == "" || input.ExpectedRevision <= 0 || validateAnnotationInput(normalized) != nil {
		return Annotation{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryKey(tenantID, id)
	item, ok := s.annotations[key]
	if !ok {
		return Annotation{}, ErrNotFound
	}
	if item.Revision != input.ExpectedRevision {
		return Annotation{}, ErrRevisionConflict
	}
	item.Type, item.Geometry = normalized.Type, normalized.Geometry
	item.Payload, item.Content, item.Visibility = clonePayload(normalized.Payload), normalized.Content, normalized.Visibility
	item.Revision++
	item.UpdatedBy, item.UpdatedAt = actorID, time.Now().UTC()
	s.annotations[key] = item
	return cloneAnnotation(item), nil
}

// 删除也使用 expected_revision 做并发校验，避免把别人刚更新的批注误删。
func (s *MemoryStore) DeleteAnnotation(_ context.Context, tenantID, id, _ string, expectedRevision int64) error {
	if tenantID == "" || id == "" || expectedRevision <= 0 {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryKey(tenantID, id)
	item, ok := s.annotations[key]
	if !ok {
		return ErrNotFound
	}
	if item.Revision != expectedRevision {
		return ErrRevisionConflict
	}
	delete(s.annotations, key)
	return nil
}

func (s *MemoryStore) CreateCommentTemplate(_ context.Context, tenantID, actorID string, input CreateCommentTemplateInput) (CommentTemplate, error) {
	input.Title, input.Content, input.Shortcut = normalizeTemplate(input.Title, input.Content, input.Shortcut)
	if tenantID == "" || actorID == "" || validateTemplate(input.Title, input.Content, input.Shortcut) != nil {
		return CommentTemplate{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shortcutExists(tenantID, actorID, input.Shortcut, "") {
		return CommentTemplate{}, ErrShortcutConflict
	}
	now := time.Now().UTC()
	item := CommentTemplate{
		ID: s.id("comment-template"), TenantID: tenantID, OwnerID: actorID,
		Title: input.Title, Content: input.Content, Shortcut: input.Shortcut,
		UsageCount: 0, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	s.templates[memoryKey(tenantID, item.ID)] = item
	return item, nil
}

// 模板按租户和拥有者隔离，并按使用次数降序返回，便于前端优先展示常用快捷语句。
func (s *MemoryStore) ListCommentTemplates(_ context.Context, tenantID, ownerID string) ([]CommentTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []CommentTemplate{}
	for _, item := range s.templates {
		if item.TenantID == tenantID && item.OwnerID == ownerID {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UsageCount == out[j].UsageCount {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].UsageCount > out[j].UsageCount
	})
	return out, nil
}

func (s *MemoryStore) GetCommentTemplate(_ context.Context, tenantID, ownerID, id string) (CommentTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.templates[memoryKey(tenantID, id)]
	if !ok || item.OwnerID != ownerID {
		return CommentTemplate{}, ErrNotFound
	}
	return item, nil
}

func (s *MemoryStore) UpdateCommentTemplate(_ context.Context, tenantID, ownerID, id string, input UpdateCommentTemplateInput) (CommentTemplate, error) {
	input.Title, input.Content, input.Shortcut = normalizeTemplate(input.Title, input.Content, input.Shortcut)
	if tenantID == "" || ownerID == "" || id == "" || input.ExpectedRevision <= 0 || validateTemplate(input.Title, input.Content, input.Shortcut) != nil {
		return CommentTemplate{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryKey(tenantID, id)
	item, ok := s.templates[key]
	if !ok || item.OwnerID != ownerID {
		return CommentTemplate{}, ErrNotFound
	}
	if item.Revision != input.ExpectedRevision {
		return CommentTemplate{}, ErrRevisionConflict
	}
	if s.shortcutExists(tenantID, ownerID, input.Shortcut, id) {
		return CommentTemplate{}, ErrShortcutConflict
	}
	item.Title, item.Content, item.Shortcut = input.Title, input.Content, input.Shortcut
	item.Revision++
	item.UpdatedAt = time.Now().UTC()
	s.templates[key] = item
	return item, nil
}

func (s *MemoryStore) DeleteCommentTemplate(_ context.Context, tenantID, ownerID, id string, expectedRevision int64) error {
	if tenantID == "" || ownerID == "" || id == "" || expectedRevision <= 0 {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryKey(tenantID, id)
	item, ok := s.templates[key]
	if !ok || item.OwnerID != ownerID {
		return ErrNotFound
	}
	if item.Revision != expectedRevision {
		return ErrRevisionConflict
	}
	delete(s.templates, key)
	return nil
}

// 使用快捷语句和 usage_count 更新放在同一把锁内，列表排序不会看到半更新状态。
func (s *MemoryStore) UseCommentTemplate(_ context.Context, tenantID, ownerID, shortcut string) (CommentTemplate, error) {
	_, _, shortcut = normalizeTemplate("", "", shortcut)
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, item := range s.templates {
		if item.TenantID == tenantID && item.OwnerID == ownerID && item.Shortcut == shortcut {
			item.UsageCount++
			item.Revision++
			item.UpdatedAt = time.Now().UTC()
			s.templates[key] = item
			return item, nil
		}
	}
	return CommentTemplate{}, ErrNotFound
}

func (s *MemoryStore) shortcutExists(tenantID, ownerID, shortcut, exceptID string) bool {
	for _, item := range s.templates {
		if item.TenantID == tenantID && item.OwnerID == ownerID && item.Shortcut == shortcut && item.ID != exceptID {
			return true
		}
	}
	return false
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

func memoryKey(tenantID, id string) string { return tenantID + "\x00" + id }

func studentQuestionKey(tenantID, examID, studentID, questionID string) string {
	return tenantID + "\x00" + examID + "\x00" + studentID + "\x00" + questionID
}

func cloneAnnotation(item Annotation) Annotation {
	item.Payload = clonePayload(item.Payload)
	return item
}

func cloneStudentAnnotations(items []StudentAnnotation) []StudentAnnotation {
	return append([]StudentAnnotation(nil), items...)
}
