package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
)

type archiveWorkspaceFixture struct {
	fakeExamReader
	management exam.ManagementExam
	err        error
}

func (f archiveWorkspaceFixture) GetManagementExam(context.Context, auth.AccessScope, string, string) (exam.ManagementExam, error) {
	return f.management, f.err
}

func TestArchivedWorkspacePreservesSubjectStageAndRemovesWriteActions(t *testing.T) {
	now := time.Now().UTC()
	for _, scenario := range []struct {
		name     string
		metadata exam.ManagementArchiveMetadata
		legacy   bool
		subjects []string
		err      error
		readOnly bool
	}{
		{"whole exam archive", exam.ManagementArchiveMetadata{ArchivedAt: &now}, false, nil, nil, true},
		{"restored whole exam", exam.ManagementArchiveMetadata{}, false, nil, nil, false},
		{"legacy whole exam archive", exam.ManagementArchiveMetadata{}, true, nil, nil, true},
		{"separately archived current subject", exam.ManagementArchiveMetadata{}, false, []string{"math"}, nil, true},
		{"archived sibling keeps current subject available", exam.ManagementArchiveMetadata{}, false, []string{"english"}, nil, false},
		{"archive source unavailable", exam.ManagementArchiveMetadata{}, false, nil, errors.New("source unavailable"), true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			reader := archiveWorkspaceFixture{fakeExamReader: fakeExamReader{item: exam.Exam{ID: "math", TenantID: "tenant-1", SessionID: "session-1", Status: "draft"}}, management: exam.ManagementExam{ManagementArchiveMetadata: scenario.metadata, LegacyArchived: scenario.legacy, ArchivedSubjectIDs: scenario.subjects}, err: scenario.err}
			service := NewService(Dependencies{Exams: reader, Papers: fakePaperReader{}, Submissions: fakeSubmissionReader{}, Reviews: fakeReviewReader{}})
			result, err := service.Get(context.Background(), auth.AccessScope{TenantID: "tenant-1", TenantWide: true}, "math")
			if err != nil {
				t.Fatal(err)
			}
			if result.ReadOnly != scenario.readOnly || result.ExamStatus != "draft" {
				t.Fatalf("archive changed subject stage or readonly projection: %#v", result)
			}
			if scenario.readOnly && len(result.NextActions) != 0 {
				t.Fatalf("readonly workspace recommends write workflow: %#v", result.NextActions)
			}
		})
	}
}
