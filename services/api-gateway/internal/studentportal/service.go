package studentportal

import (
	"context"
	"sort"
	"strings"
)

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) ListPublishedExams(ctx context.Context, tenantID, studentID string) ([]PublishedExam, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(studentID) == "" {
		return nil, ErrInvalidInput
	}
	items, err := s.store.ListPublishedExams(ctx, tenantID, studentID)
	if err != nil {
		return nil, err
	}
	// 学生在每场考试只能有一个当前发布版本。
	// 将异常存储响应视为不可用，避免为学生选择可能过时的版本。
	seen := make(map[string]struct{}, len(items))
	result := make([]PublishedExam, 0, len(items))
	for _, item := range items {
		item.ExamID, item.Name, item.Subject, item.ExamType = strings.TrimSpace(item.ExamID), strings.TrimSpace(item.Name), strings.TrimSpace(item.Subject), strings.TrimSpace(item.ExamType)
		if item.ExamID == "" || item.Name == "" || item.ReleaseVersion < 1 {
			return nil, ErrInvalidInput
		}
		if _, exists := seen[item.ExamID]; exists {
			return nil, ErrInvalidInput
		}
		seen[item.ExamID] = struct{}{}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].PublishedAt.Equal(result[j].PublishedAt) {
			return result[i].ExamID < result[j].ExamID
		}
		return result[i].PublishedAt.After(result[j].PublishedAt)
	})
	return result, nil
}
