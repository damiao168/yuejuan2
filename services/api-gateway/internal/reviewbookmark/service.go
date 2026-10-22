package reviewbookmark

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func requireAccount(user auth.User) error {
	if user.ID == "" || user.TenantID == "" || user.Status != "active" {
		return ErrForbidden
	}
	return nil
}
func requireWork(user auth.User) error {
	if err := requireAccount(user); err != nil {
		return err
	}
	if !auth.HasPermission(user, "review:work") {
		return ErrForbidden
	}
	return nil
}

func normalizePut(input PutInput) (PutInput, error) {
	input.Reason = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(input.Reason, "\r\n", "\n"), "\r", "\n"))
	if input.ExpectedRevision < 0 || !utf8.ValidString(input.Reason) || utf8.RuneCountInString(input.Reason) > 200 {
		return input, ErrInvalid
	}
	return input, nil
}

func project(record Record, source Source) Bookmark {
	id := record.ID
	result := Bookmark{ID: &id, TaskID: record.TaskID, Revision: record.Revision, Marked: record.DeletedAt == nil, CanEditReason: !source.Archived}
	if result.Marked {
		result.Reason = record.Reason
		marked := record.MarkedAt
		result.MarkedAt = &marked
	}
	return result
}

func sourceUnavailable(err error) bool {
	return err == ErrNotFound || err == ErrForbidden || err == auth.ErrForbidden || err == auth.ErrUnauthenticated
}

type listCursor struct {
	At     time.Time `json:"at"`
	ID     string    `json:"id"`
	Filter string    `json:"filter"`
}

func filterDigest(user auth.User, filter ListFilter) string {
	raw, _ := json.Marshal([]string{user.TenantID, user.ID, filter.ExamID, filter.QuestionID, filter.Subject, filter.State, filter.Q})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func normalizeFilter(filter ListFilter) (ListFilter, error) {
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	filter.Q = strings.TrimSpace(filter.Q)
	filter.Subject = strings.TrimSpace(filter.Subject)
	if filter.State == "" {
		filter.State = "all"
	}
	if filter.Limit < 1 || filter.Limit > 200 || (filter.State != "all" && filter.State != "processable" && filter.State != "submitted") || len(filter.Cursor) > 2048 || utf8.RuneCountInString(filter.Q) > 200 {
		return filter, ErrInvalid
	}
	return filter, nil
}

func buildList(user auth.User, filter ListFilter, records []Record, sources map[string]Source) (ListResult, error) {
	result := ListResult{Items: []ListItem{}, Exams: []ExamOption{}, Questions: []QuestionOption{}}
	filter, err := normalizeFilter(filter)
	if err != nil {
		return result, err
	}
	digest := filterDigest(user, filter)
	var cursor listCursor
	if filter.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Filter != digest || cursor.ID == "" || cursor.At.IsZero() {
			return result, ErrInvalid
		}
	}
	exams := map[string]ExamOption{}
	questions := map[string]QuestionOption{}
	var matches []Record
	for _, record := range records {
		source, ok := sources[record.TaskID]
		if !ok || record.DeletedAt != nil {
			continue
		}
		exams[source.ExamID] = ExamOption{ID: source.ExamID, Name: source.ExamName}
		if filter.ExamID == "" || filter.ExamID == source.ExamID {
			questions[source.QuestionID] = QuestionOption{ID: source.QuestionID, QuestionNo: source.QuestionNo}
		}
		if filter.ExamID != "" && filter.ExamID != source.ExamID || filter.QuestionID != "" && filter.QuestionID != source.QuestionID || filter.Subject != "" && filter.Subject != source.Subject || filter.State != "all" && filter.State != source.State {
			continue
		}
		if filter.Q != "" && !strings.Contains(strings.ToLower(source.ExamName+" "+source.QuestionNo+" "+source.AnonymousCode), strings.ToLower(filter.Q)) {
			continue
		}
		matches = append(matches, record)
	}
	// 全部授权与筛选在分页前完成，避免失权对象进入计数、选项或占用当前页。
	result.TotalCount = len(matches)
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].MarkedAt.Equal(matches[j].MarkedAt) {
			return matches[i].ID > matches[j].ID
		}
		return matches[i].MarkedAt.After(matches[j].MarkedAt)
	})
	for _, record := range matches {
		if filter.Cursor != "" && (record.MarkedAt.After(cursor.At) || record.MarkedAt.Equal(cursor.At) && record.ID >= cursor.ID) {
			continue
		}
		if len(result.Items) == filter.Limit {
			last := result.Items[len(result.Items)-1]
			raw, _ := json.Marshal(listCursor{At: *last.MarkedAt, ID: *last.ID, Filter: digest})
			result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
			break
		}
		source := sources[record.TaskID]
		result.Items = append(result.Items, ListItem{Bookmark: project(record, source), ExamID: source.ExamID, ExamName: source.ExamName, QuestionID: source.QuestionID, QuestionNo: source.QuestionNo, Subject: source.Subject, AnonymousCode: source.AnonymousCode, State: source.State})
	}
	for _, option := range exams {
		result.Exams = append(result.Exams, option)
	}
	for _, option := range questions {
		result.Questions = append(result.Questions, option)
	}
	sort.Slice(result.Exams, func(i, j int) bool { return result.Exams[i].ID < result.Exams[j].ID })
	sort.Slice(result.Questions, func(i, j int) bool { return result.Questions[i].ID < result.Questions[j].ID })
	return result, nil
}
