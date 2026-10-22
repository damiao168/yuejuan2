package reviewbookmark

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresource"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

type EvidenceTask struct {
	ID            string  `json:"id"`
	ExamID        string  `json:"exam_id"`
	ExamName      string  `json:"exam_name"`
	QuestionID    string  `json:"question_id"`
	QuestionNo    string  `json:"question_no"`
	Subject       string  `json:"subject"`
	AnonymousCode string  `json:"anonymous_code"`
	State         string  `json:"state"`
	MaxScore      float64 `json:"max_score"`
}
type EvidencePoint struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Score       float64 `json:"score"`
}
type EvidenceRubric struct {
	MaxScore float64         `json:"max_score"`
	Points   []EvidencePoint `json:"points"`
}
type EvidenceQuestion struct {
	Stem    string         `json:"stem"`
	Options []string       `json:"options"`
	Rubric  EvidenceRubric `json:"rubric"`
}
type OwnSubmission struct {
	Score            float64                  `json:"score"`
	MaxScore         float64                  `json:"max_score"`
	RubricSelections []review.RubricSelection `json:"rubric_selections"`
	SubmittedAt      time.Time                `json:"submitted_at"`
}
type EvidenceAnswer struct {
	Text            string `json:"text"`
	TextBinding     string `json:"text_binding"`
	ImageURL        string `json:"image_url"`
	EvidenceVersion string `json:"evidence_version"`
	ImageAvailable  bool   `json:"image_available"`
	MaterialChange  string `json:"material_change"`
}
type EvidenceContext struct {
	Bookmark      Bookmark         `json:"bookmark"`
	Task          EvidenceTask     `json:"task"`
	Question      EvidenceQuestion `json:"question"`
	OwnSubmission *OwnSubmission   `json:"own_submission,omitempty"`
	Answer        EvidenceAnswer   `json:"answer"`
	Readonly      bool             `json:"readonly"`
}

// EvidenceMaterial 只在服务端传递真实资产身份，公开 DTO 不序列化这些字段。
type EvidenceMaterial struct {
	Question       EvidenceQuestion
	OwnSubmission  *OwnSubmission
	Text           string
	SnapshotID     string
	Version        segment.CropImageVersion
	ImageAvailable bool
}
type evidenceProvider interface {
	ResolveEvidence(context.Context, auth.User, string) (Source, EvidenceMaterial, error)
}

type EvidenceService struct {
	provider SourceProvider
	images   segment.VersionedCropImageReader
	secret   []byte
}

// NewEvidenceService 的密钥与模型密钥使用不同 HMAC 域；缺失密钥时只读内容失败关闭。
func NewEvidenceService(provider SourceProvider, images segment.VersionedCropImageReader, secret []byte) *EvidenceService {
	return &EvidenceService{provider: provider, images: images, secret: append([]byte(nil), secret...)}
}
func (s *EvidenceService) resolve(ctx context.Context, user auth.User, record Record) (Source, EvidenceMaterial, error) {
	if err := requireWork(user); err != nil {
		return Source{}, EvidenceMaterial{}, err
	}
	if record.TenantID != user.TenantID || record.OwnerID != user.ID || record.DeletedAt != nil {
		return Source{}, EvidenceMaterial{}, ErrNotFound
	}
	p, ok := s.provider.(evidenceProvider)
	if !ok || len(s.secret) == 0 {
		return Source{}, EvidenceMaterial{}, ErrEvidenceUnavailable
	}
	return p.ResolveEvidence(ctx, user, record.TaskID)
}

var ErrEvidenceUnavailable = segment.ErrEvidenceUnavailable

func (s *EvidenceService) version(user auth.User, source Source, material EvidenceMaterial) string {
	// 令牌仅输出 HMAC，不把真实题块、资产、哈希或文本编码回浏览器。
	raw, _ := json.Marshal([]any{"personal-review-bookmark-evidence-v1", user.TenantID, user.ID, source.TaskID, source.AnswerSegmentID, material.Version, material.SnapshotID, material.Question, material.Text})
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *EvidenceService) Context(ctx context.Context, user auth.User, record Record) (EvidenceContext, error) {
	source, material, err := s.resolve(ctx, user, record)
	if err != nil {
		return EvidenceContext{}, err
	}
	version := s.version(user, source, material)
	change := "unknown"
	if record.ObservedCropSHA256 != "" && material.Version.SHA256 != "" {
		change = "unchanged"
		if record.ObservedCropSHA256 != material.Version.SHA256 {
			change = "changed"
		}
	}
	if record.ObservedSnapshotID != "" && material.SnapshotID != "" && record.ObservedSnapshotID != material.SnapshotID {
		change = "changed"
	}
	binding := "unavailable"
	if material.Text != "" {
		binding = "unverified"
	}
	available := material.ImageAvailable && s.images != nil
	imageURL := ""
	if available {
		imageURL = "/api/v1/me/review-bookmarks/" + url.PathEscape(record.ID) + "/image?version=" + url.QueryEscape(version)
	}
	return EvidenceContext{Bookmark: project(record, source), Readonly: true, Task: EvidenceTask{ID: source.TaskID, ExamID: source.ExamID, ExamName: source.ExamName, QuestionID: source.QuestionID, QuestionNo: source.QuestionNo, Subject: source.Subject, AnonymousCode: source.AnonymousCode, State: source.State, MaxScore: source.MaxScore}, Question: material.Question, OwnSubmission: material.OwnSubmission, Answer: EvidenceAnswer{Text: material.Text, TextBinding: binding, ImageURL: imageURL, EvidenceVersion: version, ImageAvailable: available, MaterialChange: change}}, nil
}
func (s *EvidenceService) Image(ctx context.Context, user auth.User, record Record, version string) (binaryresource.Resource, error) {
	source, material, err := s.resolve(ctx, user, record)
	if err != nil {
		return binaryresource.Resource{}, err
	}
	if version == "" || !hmac.Equal([]byte(version), []byte(s.version(user, source, material))) {
		return binaryresource.Resource{}, ErrEvidenceChanged
	}
	if s.images == nil || !material.ImageAvailable {
		return binaryresource.Resource{}, ErrEvidenceUnavailable
	}
	resource, err := s.images.ReadVersionedCropImage(ctx, user.TenantID, source.AnswerSegmentID, material.Version)
	if err == segment.ErrEvidenceVersionChanged {
		err = ErrEvidenceChanged
	}
	if err != nil {
		return binaryresource.Resource{}, err
	}
	resource.ETag = ""
	resource.Disposition = `inline; filename="answer.png"`
	resource.CacheControl = "private, no-store"
	resource.AllowHead = true
	return resource, nil
}
