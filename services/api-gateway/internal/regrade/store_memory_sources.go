package regrade

import (
	"context"
	"sort"
)

type memoryRelease struct {
	TenantID  string
	ExamID    string
	ID        string
	Version   int
	Published bool
	Current   bool
	Items     map[string][]SourceItem
}

// SeedPublishedRelease 提供不可变来源快照。
// 测试可预置非当前的历史已发布版本，Preview 同时报告当前发布，帮助操作人员理解影响。
func (s *MemoryStore) SeedPublishedRelease(tenantID, examID, releaseID string, version int, current bool, byQuestion map[string][]SourceItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current {
		for id, value := range s.sources {
			if value.TenantID == tenantID && value.ExamID == examID {
				value.Current = false
				s.sources[id] = value
			}
		}
	}
	copyByQuestion := map[string][]SourceItem{}
	for questionID, values := range byQuestion {
		copyByQuestion[questionID] = append([]SourceItem(nil), values...)
	}
	s.sources[releaseID] = memoryRelease{TenantID: tenantID, ExamID: examID, ID: releaseID, Version: version, Published: true, Current: current, Items: copyByQuestion}
}

func (s *MemoryStore) SourceItems(_ context.Context, tenantID, examID, questionID, releaseID string, selector Selector) ([]SourceItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sourceItemsLocked(tenantID, examID, questionID, releaseID, selector)
}

func (s *MemoryStore) sourceItemsLocked(tenantID, examID, questionID, releaseID string, selector Selector) ([]SourceItem, error) {
	release, ok := s.sources[releaseID]
	if !ok || !release.Published || release.TenantID != tenantID || release.ExamID != examID {
		return nil, ErrSourceRelease
	}
	items := []SourceItem{}
	for _, item := range release.Items[questionID] {
		if matches(item, selector) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].SubmissionID < items[j].SubmissionID })
	return items, nil
}

func (s *MemoryStore) Preview(ctx context.Context, tenantID, examID, questionID, releaseID string, selector Selector) (Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.sourceItemsLocked(tenantID, examID, questionID, releaseID, selector)
	if err != nil {
		return Preview{}, err
	}
	release := s.sources[releaseID]
	currentID, currentVersion := "", 0
	for _, candidate := range s.sources {
		if candidate.TenantID == tenantID && candidate.ExamID == examID && candidate.Current {
			currentID, currentVersion = candidate.ID, candidate.Version
			break
		}
	}
	return previewFromSources(examID, questionID, releaseID, release.Version, currentID, currentVersion, items), nil
}

func matches(item SourceItem, selector Selector) bool {
	if len(selector.SubmissionIDs) > 0 {
		found := false
		for _, id := range selector.SubmissionIDs {
			if item.SubmissionID == id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if selector.ScoreBand != nil {
		if selector.ScoreBand.Min != nil && item.OldScore < *selector.ScoreBand.Min {
			return false
		}
		if selector.ScoreBand.Max != nil && item.OldScore > *selector.ScoreBand.Max {
			return false
		}
	}
	return true
}

func previewFromSources(examID, questionID, releaseID string, version int, currentID string, currentVersion int, items []SourceItem) Preview {
	result := Preview{ExamID: examID, QuestionID: questionID, SourceReleaseID: releaseID, SourceReleaseVersion: version, CurrentReleaseID: currentID, CurrentReleaseVersion: currentVersion, ScoreBands: []ScoreBandCount{}}
	counts := map[float64]int{}
	var min, max float64
	for index, item := range items {
		result.AffectedCount++
		counts[item.OldScore]++
		low, high := -item.OldScore, item.MaxScore-item.OldScore
		// 这里只给出改成零分或满分时的差值范围，不预测实际会改多少分。
		if index == 0 || low < min {
			min = low
		}
		if index == 0 || high > max {
			max = high
		}
	}
	for score, count := range counts {
		result.ScoreBands = append(result.ScoreBands, ScoreBandCount{Score: score, Count: count})
	}
	sort.Slice(result.ScoreBands, func(i, j int) bool { return result.ScoreBands[i].Score < result.ScoreBands[j].Score })
	result.PotentialDelta = PotentialDelta{Min: min, Max: max}
	return result
}
