package paper

import (
	"context"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func (s *MemoryStore) librarySchool(ctx context.Context, scope auth.AccessScope, examID string) (string, error) {
	if s.libraryExams == nil {
		return "", ErrNotFound
	}
	e, err := s.libraryExams.GetExam(ctx, scope, examID)
	if err != nil || e.TenantID != scope.TenantID || e.SchoolID == "" {
		return "", ErrNotFound
	}
	return e.SchoolID, nil
}

func (s *MemoryStore) ListTemplateLibrary(ctx context.Context, scope auth.AccessScope, examID string) ([]AnswerSheetTemplateLibraryVersion, error) {
	school, err := s.librarySchool(ctx, scope, examID)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []AnswerSheetTemplateLibraryVersion{}
	for _, item := range s.templateLibrary {
		if item.TenantID == scope.TenantID && item.SchoolID == school {
			out = append(out, copyLibraryValue(item))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ReviewedAt.After(out[j].ReviewedAt) })
	return out, nil
}

func (s *MemoryStore) PublishTemplateToLibrary(ctx context.Context, scope auth.AccessScope, id string, input PublishTemplateToLibraryInput) (AnswerSheetTemplateLibraryVersion, error) {
	if !libraryPublishInputValid(input) {
		return AnswerSheetTemplateLibraryVersion{}, ErrInvalidInput
	}
	s.mu.RLock()
	source, ok := s.templates[id]
	s.mu.RUnlock()
	if !ok || source.TenantID != scope.TenantID {
		return AnswerSheetTemplateLibraryVersion{}, ErrNotFound
	}
	school, err := s.librarySchool(ctx, scope, source.ExamID)
	if err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source = s.templates[id]
	if source.Revision != input.ExpectedRevision {
		return AnswerSheetTemplateLibraryVersion{}, ErrConflict
	}
	if source.Status != "locked" {
		return AnswerSheetTemplateLibraryVersion{}, ErrTemplateNotLocked
	}
	for libraryID, ref := range s.librarySources {
		if ref.TemplateID == id && ref.Revision == source.Revision {
			if s.templateLibrary[libraryID].SchoolID != school {
				return AnswerSheetTemplateLibraryVersion{}, ErrConflict
			}
			return copyLibraryValue(s.templateLibrary[libraryID]), nil
		}
	}
	paper, ok := s.papers[source.ExamPaperID]
	if !ok || paper.TenantID != scope.TenantID || paper.ExamID != source.ExamID || paper.Purpose != PaperPurposeAnswerSheet || !validLibraryReference(paper.File) {
		return AnswerSheetTemplateLibraryVersion{}, ErrInvalidInput
	}
	questions := s.libraryQuestionsLocked(scope.TenantID, source.ExamID)
	layout, err := newLibraryLayout(source, questions)
	if err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	for _, item := range s.templateLibrary {
		if item.TenantID == scope.TenantID && item.SchoolID == school && item.SourceSHA256 == paper.File.HashSHA256 && item.ContentHash == stableContentHash(layout) {
			return copyLibraryValue(item), nil
		}
	}
	now := time.Now().UTC()
	item := AnswerSheetTemplateLibraryVersion{ID: s.id("template-library"), TenantID: scope.TenantID, SchoolID: school, Name: strings.TrimSpace(input.Name), VersionNo: 1, PageCount: source.PageCount, Layout: layout, SourceSHA256: paper.File.HashSHA256, ContentHash: stableContentHash(layout), ReviewedBy: scope.ActorID, ReviewedAt: now}
	s.templateLibrary[item.ID] = copyLibraryValue(item)
	s.librarySources[item.ID] = templateLibrarySource{TemplateID: id, Revision: source.Revision, File: paper.File}
	return copyLibraryValue(item), nil
}

func validLibraryReference(file FileAssetInput) bool {
	_, err := hex.DecodeString(file.HashSHA256)
	return err == nil && len(file.HashSHA256) == 64 && file.SizeBytes > 0 && file.StorageKey != "" && (file.ContentType == "image/jpeg" || file.ContentType == "image/png" || file.ContentType == "application/pdf")
}

func (s *MemoryStore) libraryQuestionsLocked(tenantID, examID string) []Question {
	out := []Question{}
	for _, q := range s.questions {
		if q.TenantID == tenantID && q.ExamID == examID && q.Status != "deleted" {
			out = append(out, q)
		}
	}
	return out
}

func (s *MemoryStore) InstantiateTemplateLibrary(ctx context.Context, scope auth.AccessScope, examID, libraryID string) (TemplateLibraryInstance, error) {
	school, err := s.librarySchool(ctx, scope, examID)
	if err != nil {
		return TemplateLibraryInstance{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.instantiateLibraryLocked(scope, school, examID, libraryID, "")
}

func (s *MemoryStore) instantiateLibraryLocked(scope auth.AccessScope, school, examID, libraryID, paperID string) (TemplateLibraryInstance, error) {
	item, ok := s.templateLibrary[libraryID]
	if !ok || item.TenantID != scope.TenantID || item.SchoolID != school {
		return TemplateLibraryInstance{}, ErrNotFound
	}
	key := examID + "/" + libraryID
	if id := s.libraryInstances[key]; id != "" {
		template, exists := s.templates[id]
		if !exists {
			return TemplateLibraryInstance{}, ErrConflict
		}
		return TemplateLibraryInstance{Template: copyLibraryValue(template), Paper: s.papers[template.ExamPaperID], Reused: true}, nil
	}
	if err := s.ensureExamPaperMutableLocked(examID); err != nil {
		return TemplateLibraryInstance{}, err
	}
	layout, err := instantiateLibraryLayout(item, s.libraryQuestionsLocked(scope.TenantID, examID))
	if err != nil {
		return TemplateLibraryInstance{}, err
	}
	paper, exists := s.papers[paperID]
	if paperID != "" && (!exists || paper.TenantID != scope.TenantID || paper.ExamID != examID || paper.Purpose != PaperPurposeAnswerSheet || paper.File.HashSHA256 != item.SourceSHA256) {
		return TemplateLibraryInstance{}, ErrInvalidInput
	}
	if paperID == "" {
		version := 1
		for _, p := range s.papers {
			if p.TenantID == scope.TenantID && p.ExamID == examID && p.VersionNo >= version {
				version = p.VersionNo + 1
			}
		}
		paper = Paper{ID: s.id("paper"), TenantID: scope.TenantID, ExamID: examID, FileAssetID: s.id("file"), VersionNo: version, Status: "uploaded", Purpose: PaperPurposeAnswerSheet, File: s.librarySources[libraryID].File}
	}
	version := 1
	for _, t := range s.templates {
		if t.TenantID == scope.TenantID && t.ExamID == examID && t.VersionNo >= version {
			version = t.VersionNo + 1
		}
	}
	now := time.Now().UTC()
	template := AnswerSheetTemplate{ID: s.id("template"), TenantID: scope.TenantID, ExamID: examID, ExamPaperID: paper.ID, VersionNo: version, Revision: 1, Name: item.Name, Status: "draft", PageCount: item.PageCount, Layout: layout, ContentHash: stableContentHash(layout), CreatedBy: scope.ActorID, CreatedAt: now, UpdatedAt: now}
	// 映射失败前不写任何对象；绑定仍需沿用独立的锁定和 revision 门禁。
	s.papers[paper.ID] = paper
	s.templates[template.ID] = template
	s.libraryInstances[key] = template.ID
	s.invalidateReadiness(examID)
	return TemplateLibraryInstance{Template: copyLibraryValue(template), Paper: paper}, nil
}

func (s *MemoryStore) ResolveTemplateLibrary(ctx context.Context, scope auth.AccessScope, examID string, input ResolveTemplateLibraryInput) (TemplateLibraryResolution, error) {
	school, err := s.librarySchool(ctx, scope, examID)
	if err != nil {
		return TemplateLibraryResolution{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.papers[input.ExamPaperID]
	if !ok || p.TenantID != scope.TenantID || p.ExamID != examID || p.Purpose != PaperPurposeAnswerSheet {
		return TemplateLibraryResolution{}, ErrInvalidInput
	}
	if !validLibraryReference(p.File) {
		return TemplateLibraryResolution{}, ErrInvalidInput
	}
	out := TemplateLibraryResolution{Status: "unknown", Candidates: []AnswerSheetTemplateLibraryVersion{}}
	for _, item := range s.templateLibrary {
		if item.TenantID == scope.TenantID && item.SchoolID == school && item.SourceSHA256 == p.File.HashSHA256 {
			out.Candidates = append(out.Candidates, copyLibraryValue(item))
		}
	}
	sort.Slice(out.Candidates, func(i, j int) bool { return out.Candidates[i].ID < out.Candidates[j].ID })
	if len(out.Candidates) > 1 {
		out.Status = "ambiguous"
		return out, nil
	}
	if len(out.Candidates) == 1 {
		out.Status = "matched"
		instance, err := s.instantiateLibraryLocked(scope, school, examID, out.Candidates[0].ID, p.ID)
		if err != nil {
			return TemplateLibraryResolution{}, err
		}
		out.Instance = &instance
	}
	return out, nil
}

func (s *MemoryStore) TemplateLibraryMatchSources(ctx context.Context, scope auth.AccessScope, examID string, input ResolveTemplateLibraryInput) (TemplateLibraryMatchSources, error) {
	school, err := s.librarySchool(ctx, scope, examID)
	if err != nil {
		return TemplateLibraryMatchSources{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.papers[input.ExamPaperID]
	if !ok || p.TenantID != scope.TenantID || p.ExamID != examID || p.Purpose != PaperPurposeAnswerSheet || !validLibraryReference(p.File) {
		return TemplateLibraryMatchSources{}, ErrInvalidInput
	}
	out := TemplateLibraryMatchSources{Source: TemplateLibraryMatchDocument{SourceSHA256: p.File.HashSHA256, File: p.File}, Candidates: []TemplateLibraryMatchDocument{}, Complete: true}
	items := []AnswerSheetTemplateLibraryVersion{}
	for _, item := range s.templateLibrary {
		if item.TenantID == scope.TenantID && item.SchoolID == school {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	questions := s.libraryQuestionsLocked(scope.TenantID, examID)
	for _, item := range items {
		if _, err := instantiateLibraryLayout(item, questions); err != nil {
			continue
		}
		if len(out.Candidates) == TemplateLibraryMatchCandidateLimit {
			out.Complete = false
			break
		}
		ref, exists := s.librarySources[item.ID]
		if !exists || !validLibraryReference(ref.File) || ref.File.HashSHA256 != item.SourceSHA256 {
			return TemplateLibraryMatchSources{}, ErrConflict
		}
		out.Candidates = append(out.Candidates, TemplateLibraryMatchDocument{LibraryID: item.ID, PageCount: item.PageCount, SourceSHA256: item.SourceSHA256, Layout: copyLibraryValue(item.Layout), File: ref.File})
	}
	return out, nil
}
