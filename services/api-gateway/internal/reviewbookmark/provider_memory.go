package reviewbookmark

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

type UserReader interface {
	FindUserByID(context.Context, string, string) (auth.UserWithPassword, error)
}
type ExamReader interface {
	GetExam(context.Context, auth.AccessScope, string) (exam.Exam, error)
}
type SchoolReader interface {
	ListSchools(context.Context, string) ([]org.School, error)
}
type SegmentReader interface {
	GetEvidence(context.Context, string, string) (segment.SegmentEvidence, error)
}
type reviewMaterialReader interface {
	GetBookmarkMaterial(context.Context, string, string, string) (review.BookmarkMaterial, error)
	GetBookmarkQuestion(context.Context, string, string, string, string) (review.BookmarkMaterial, error)
}
type seedOpinionReader interface {
	GetBookmarkOpinion(context.Context, string, string, string) (seedquality.BookmarkOpinion, error)
}
type MemorySourceProvider struct {
	tasks    review.TaskStore
	seeds    seedquality.Store
	users    UserReader
	exams    ExamReader
	schools  SchoolReader
	segments SegmentReader
}

func NewMemorySourceProvider(tasks review.TaskStore, seeds seedquality.Store, users UserReader, exams ExamReader, schools SchoolReader, segments SegmentReader) *MemorySourceProvider {
	return &MemorySourceProvider{tasks: tasks, seeds: seeds, users: users, exams: exams, schools: schools, segments: segments}
}
func (p *MemorySourceProvider) authorize(ctx context.Context, user auth.User, examID string) (exam.Exam, error) {
	if err := requireWork(user); err != nil {
		return exam.Exam{}, err
	}
	if p.users == nil || p.exams == nil || p.schools == nil {
		return exam.Exam{}, ErrNotFound
	}
	current, err := p.users.FindUserByID(ctx, user.TenantID, user.ID)
	if err != nil || current.Status != "active" || !auth.HasPermission(current.User, "review:work") || current.SchoolID == "" {
		return exam.Exam{}, ErrNotFound
	}
	e, err := p.exams.GetExam(ctx, auth.AccessScope{TenantID: user.TenantID, TenantWide: true}, examID)
	if err != nil || e.SchoolID == "" || e.SchoolID != current.SchoolID {
		return exam.Exam{}, ErrNotFound
	}
	schools, err := p.schools.ListSchools(ctx, user.TenantID)
	if err != nil {
		return exam.Exam{}, err
	}
	for _, school := range schools {
		if school.ID == e.SchoolID && school.TenantID == user.TenantID && school.Status == "active" {
			return e, nil
		}
	}
	return exam.Exam{}, ErrNotFound
}
func (p *MemorySourceProvider) Resolve(ctx context.Context, user auth.User, taskID string) (Source, error) {
	source, _, err := p.resolve(ctx, user, taskID, false)
	return source, err
}
func (p *MemorySourceProvider) ResolveEvidence(ctx context.Context, user auth.User, taskID string) (Source, EvidenceMaterial, error) {
	return p.resolve(ctx, user, taskID, true)
}
func (p *MemorySourceProvider) resolve(ctx context.Context, user auth.User, taskID string, withMaterial bool) (Source, EvidenceMaterial, error) {
	if err := requireWork(user); err != nil {
		return Source{}, EvidenceMaterial{}, err
	}
	var source Source
	var material EvidenceMaterial
	var frozen review.BookmarkMaterial
	var submissionID string
	reader, _ := p.tasks.(reviewMaterialReader)
	var err error
	// 两种来源不共享身份空间；即使 UUID 意外相同也不猜测应打开哪一份材料。
	if p.tasks != nil && p.seeds != nil {
		_, a := p.tasks.GetTask(ctx, user.TenantID, taskID)
		_, b := p.seeds.GetTask(ctx, user.TenantID, taskID)
		if a == nil && b == nil {
			return source, material, ErrNotFound
		}
	}
	if p.tasks != nil {
		task, e := p.tasks.GetTask(ctx, user.TenantID, taskID)
		if e == nil {
			if task.AssignedTo != user.ID {
				return source, material, ErrNotFound
			}
			source = Source{TaskID: task.ID, ReviewTaskID: task.ID, AnswerSegmentID: task.AnswerSegmentID, ExamID: task.ExamID, QuestionID: task.QuestionID, QuestionNo: task.QuestionNo, AnonymousCode: task.AnonymousCode}
			submissionID = task.SubmissionID
			if reader == nil {
				return source, material, ErrNotFound
			}
			frozen, err = reader.GetBookmarkMaterial(ctx, user.TenantID, taskID, user.ID)
			if err != nil {
				return source, material, ErrNotFound
			}
			switch task.Status {
			case "assigned", "in_progress", "returned":
				source.State = "processable"
			case "submitted", "completed":
				if frozen.OwnGrade == nil {
					return source, material, ErrNotFound
				}
				source.State = "submitted"
			default:
				return source, material, ErrNotFound
			}
		} else if !errors.Is(e, review.ErrNotFound) {
			return source, material, e
		}
	}
	if source.TaskID == "" {
		if p.seeds == nil {
			return source, material, ErrNotFound
		}
		task, e := p.seeds.GetTask(ctx, user.TenantID, taskID)
		if e != nil {
			return source, material, ErrNotFound
		}
		if task.AssignedTo != user.ID {
			return source, material, ErrNotFound
		}
		source = Source{TaskID: task.ID, SeedTaskID: task.ID, AnswerSegmentID: segmentFromURL(task.SourceImageURL), ExamID: task.ExamID, QuestionID: task.QuestionID, QuestionNo: task.QuestionNo, AnonymousCode: task.AnonymousCode, MaxScore: task.MaxScore, ObservedSnapshotID: task.SnapshotID}
		switch task.Status {
		case "in_progress":
			source.State = "processable"
		case "completed":
			opinionReader, ok := p.seeds.(seedOpinionReader)
			if !ok {
				return source, material, ErrNotFound
			}
			op, e := opinionReader.GetBookmarkOpinion(ctx, user.TenantID, task.ID, user.ID)
			if e != nil {
				return source, material, ErrNotFound
			}
			source.State = "submitted"
			material.OwnSubmission = &OwnSubmission{Score: op.Score, MaxScore: task.MaxScore, RubricSelections: seedSelections(op.RubricSelections), SubmittedAt: op.SubmittedAt}
		default:
			return source, material, ErrNotFound
		}
		if withMaterial {
			if reader == nil {
				return source, material, ErrEvidenceUnavailable
			}
			frozen, err = reader.GetBookmarkQuestion(ctx, user.TenantID, task.ExamID, task.QuestionID, task.SnapshotID)
			if err != nil {
				return source, material, ErrEvidenceUnavailable
			}
			frozen.Text = ""
		}
	}
	e, err := p.authorize(ctx, user, source.ExamID)
	if err != nil {
		return source, material, err
	}
	if withMaterial && (e.Status == "draft" || e.Status == "configured") {
		return source, material, ErrEvidenceUnavailable
	}
	// 窄读取仅在真实快照的租户、考试和题目均匹配时返回 SnapshotID。
	// 此要求只限制回看，缺少冻结依据仍允许维护个人标记；暗样本还必须命中任务固定的快照。
	if withMaterial && (frozen.SnapshotID == "" || frozen.Question.ID != source.QuestionID || frozen.Question.ExamID != source.ExamID || (source.SeedTaskID != "" && frozen.SnapshotID != source.ObservedSnapshotID)) {
		return source, material, ErrEvidenceUnavailable
	}
	source.ExamName, source.Subject, source.Archived = e.Name, e.Subject, e.Status == "archived"
	if archive, ok := p.exams.(interface {
		BookmarkExamArchived(context.Context, string, string) (bool, error)
	}); ok {
		source.Archived, err = archive.BookmarkExamArchived(ctx, user.TenantID, source.ExamID)
		if err != nil {
			return source, material, ErrNotFound
		}
	}
	if source.ReviewTaskID != "" {
		source.MaxScore = frozen.Question.Score
		source.ObservedSnapshotID = frozen.SnapshotID
		if frozen.OwnGrade != nil {
			g := frozen.OwnGrade
			material.OwnSubmission = &OwnSubmission{Score: g.Score, MaxScore: g.MaxScore, RubricSelections: append([]review.RubricSelection{}, g.RubricSelections...), SubmittedAt: g.CreatedAt}
		}
	}
	material.SnapshotID = source.ObservedSnapshotID
	evidence, err := p.sourceSegment(ctx, user.TenantID, source, submissionID)
	if err != nil {
		return source, material, err
	}
	material.Version = segment.CropImageVersion{AssetID: evidence.CropFileAssetID, SHA256: evidence.CropSHA256, RegistrationID: evidence.RegistrationRunID, CorrectionID: evidence.CorrectionID}
	source.ObservedCropSHA256 = evidence.CropSHA256
	material.ImageAvailable = evidence.ProcessingStatus == "completed" && evidence.RegistrationStatus == "completed" && evidence.CropFileAssetID != "" && evidence.CropSHA256 != ""
	material.Question = questionProjection(frozen)
	// 与 PostgreSQL 保持同一公开形状；暗样本没有可公开且绑定裁剪版本的文字。
	material.Text = ""
	return source, material, nil
}
func segmentFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host != "" || parsed.Scheme != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
		return ""
	}
	parts := strings.Split(parsed.Path, "/")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "api" || parts[2] != "v1" || parts[3] != "answer-segments" || parts[4] == "" || parts[5] != "image" {
		return ""
	}
	return parts[4]
}
func questionProjection(frozen review.BookmarkMaterial) EvidenceQuestion {
	q := EvidenceQuestion{Stem: frozen.Question.Stem, Options: append([]string{}, frozen.Question.Options...), Rubric: EvidenceRubric{MaxScore: frozen.Rubric.MaxScore, Points: []EvidencePoint{}}}
	for _, point := range frozen.Rubric.Points {
		q.Rubric.Points = append(q.Rubric.Points, EvidencePoint{ID: point.ID, Description: point.Description, Score: point.Score})
	}
	return q
}
func seedSelections(raw map[string]any) []review.RubricSelection {
	out := []review.RubricSelection{}
	for id, value := range raw {
		score, ok := value.(float64)
		if id != "" && ok {
			out = append(out, review.RubricSelection{PointID: id, Score: score})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PointID < out[j].PointID })
	return out
}
