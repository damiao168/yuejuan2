package scorerelease

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
)

type publicationPreviewMode string

const (
	previewDraft     publicationPreviewMode = "draft"
	previewCandidate publicationPreviewMode = "candidate"
)

type publicationConfirmation struct {
	Snapshot string `json:"publication_snapshot"`
}

type publicationConfirmationKey struct{}

func withPublicationConfirmation(ctx context.Context, input publicationConfirmation) context.Context {
	return context.WithValue(ctx, publicationConfirmationKey{}, input)
}

func publicationConfirmationFromContext(ctx context.Context) (publicationConfirmation, bool) {
	input, present := ctx.Value(publicationConfirmationKey{}).(publicationConfirmation)
	return input, present
}

func (in publicationConfirmation) valid() bool {
	if in.Snapshot == "" {
		return true
	}
	if len(in.Snapshot) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(in.Snapshot)
	return err == nil && hex.EncodeToString(decoded) == in.Snapshot
}

// 摘要绑定发布 ID、公开设置和可复核的完整成绩事实。已公开的基准版本也参与摘要；期间发生回滚或更正要重新确认。
func publicationSnapshot(release Release, currentID string, facts []SubmissionFact) (string, error) {
	canonical := slices.Clone(facts)
	for index := range canonical {
		canonical[index].Questions = slices.Clone(canonical[index].Questions)
		slices.SortFunc(canonical[index].Questions, func(a, b QuestionFact) int { return cmp.Compare(a.QuestionID, b.QuestionID) })
	}
	slices.SortFunc(canonical, func(a, b SubmissionFact) int { return cmp.Compare(a.SubmissionID, b.SubmissionID) })
	value, err := json.Marshal(struct {
		ReleaseID, CurrentID, SourceID string
		Policy                         VisibilityPolicy
		Appeal                         AppealWindow
		Facts                          []SubmissionFact
	}{release.ID, currentID, release.SourceReleaseID, release.VisibilityPolicy, release.AppealWindow, canonical})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:]), nil
}

func checkPublicationConfirmation(input publicationConfirmation, draftRelease, candidateRelease Release, currentID string, draft, candidate []SubmissionFact) error {
	finalHash, err := publicationSnapshot(candidateRelease, currentID, candidate)
	if err != nil {
		return err
	}
	want := input.Snapshot
	if want == "" {
		// 旧客户端仍能发布原草稿；只有实际候选内容变化时，才需要支持确认摘要的新客户端。
		want, err = publicationSnapshot(draftRelease, currentID, draft)
		if err != nil {
			return err
		}
	}
	if want != finalHash {
		return ErrPublicationSnapshotChanged
	}
	return nil
}

// 候选只读预览和正式发布使用同一份快照形状，原草稿和重评来源不因预览而被改写。
func publicationPreviewDetail(release Release, currentID string, facts []SubmissionFact) (Detail, error) {
	digest, err := publicationSnapshot(release, currentID, facts)
	if err != nil {
		return Detail{}, err
	}
	detail := Detail{Release: release, Items: make([]ReleaseItem, 0, len(facts)), Questions: []ReleaseQuestion{}, PublicationSnapshot: digest}
	for _, fact := range facts {
		fact.Questions = slices.Clone(fact.Questions)
		detail.Items = append(detail.Items, ReleaseItem{ReleaseID: release.ID, StudentID: fact.StudentID, SubmissionID: fact.SubmissionID, TotalScore: fact.TotalScore, MaxScore: fact.MaxScore, Status: fact.Status, SnapshotHash: snapshotHash(fact)})
		for _, question := range fact.Questions {
			detail.Questions = append(detail.Questions, ReleaseQuestion{ReleaseID: release.ID, SubmissionID: fact.SubmissionID, QuestionFact: question})
		}
	}
	slices.SortFunc(detail.Items, func(a, b ReleaseItem) int { return cmp.Compare(a.SubmissionID, b.SubmissionID) })
	slices.SortFunc(detail.Questions, func(a, b ReleaseQuestion) int {
		return cmp.Or(cmp.Compare(a.SubmissionID, b.SubmissionID), cmp.Compare(a.QuestionNo, b.QuestionNo), cmp.Compare(a.QuestionID, b.QuestionID))
	})
	return detail, nil
}

type publicationPreviewReader interface {
	publicationPreview(context.Context, string, string, publicationPreviewMode) (Detail, error)
}

func (s *Service) publicationPreview(ctx context.Context, tenantID, releaseID string, mode publicationPreviewMode) (Detail, error) {
	if mode == "" {
		return s.Get(ctx, tenantID, releaseID)
	}
	if !validIDs(tenantID, releaseID) || (mode != previewDraft && mode != previewCandidate) {
		return Detail{}, ErrInvalidInput
	}
	reader, ok := s.store.(publicationPreviewReader)
	if !ok {
		return Detail{}, ErrInvalidTransition
	}
	return reader.publicationPreview(ctx, tenantID, releaseID, mode)
}
