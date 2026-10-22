package paper

import (
	"context"
	"sort"
	"time"
)

func (s *MemoryStore) ListTemplates(_ context.Context, tenantID string, examID string) ([]AnswerSheetTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []AnswerSheetTemplate{}
	for _, item := range s.templates {
		if item.TenantID == tenantID && item.ExamID == examID {
			out = append(out, cloneTemplateSnapshot(item))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VersionNo > out[j].VersionNo })
	return out, nil
}

func (s *MemoryStore) CreateTemplate(_ context.Context, tenantID string, examID string, userID string, input CreateTemplateInput) (AnswerSheetTemplate, error) {
	input.Layout = NormalizeTemplateLayout(cloneTemplateLayout(input.Layout))
	if err := ValidateTemplateInput(input.Name, input.PageCount, input.Layout); err != nil {
		return AnswerSheetTemplate{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureExamPaperMutableLocked(examID); err != nil {
		return AnswerSheetTemplate{}, err
	}
	if !s.paperBelongsToExam(tenantID, examID, input.ExamPaperID) {
		return AnswerSheetTemplate{}, ErrInvalidInput
	}
	var err error
	input.Layout, err = s.materializeTemplateOMRReferenceLocked(tenantID, examID, input.ExamPaperID, input.Layout)
	if err != nil {
		return AnswerSheetTemplate{}, err
	}
	if err := ValidateTemplateInput(input.Name, input.PageCount, input.Layout); err != nil {
		return AnswerSheetTemplate{}, ErrInvalidInput
	}
	version := 1
	for _, item := range s.templates {
		if item.TenantID == tenantID && item.ExamID == examID && item.VersionNo >= version {
			version = item.VersionNo + 1
		}
	}
	now := time.Now().UTC()
	item := AnswerSheetTemplate{ID: s.id("template"), TenantID: tenantID, ExamID: examID, ExamPaperID: input.ExamPaperID, VersionNo: version, Revision: 1, Name: input.Name, Status: "draft", PageCount: input.PageCount, Layout: input.Layout, ContentHash: stableContentHash(input.Layout), CreatedBy: userID, CreatedAt: now, UpdatedAt: now}
	s.templates[item.ID] = item
	s.invalidateReadiness(examID)
	return cloneTemplateSnapshot(item), nil
}

func (s *MemoryStore) UpdateTemplate(_ context.Context, tenantID string, id string, input UpdateTemplateInput) (AnswerSheetTemplate, error) {
	input.Layout = NormalizeTemplateLayout(cloneTemplateLayout(input.Layout))
	if err := ValidateTemplateInput(input.Name, input.PageCount, input.Layout); err != nil {
		return AnswerSheetTemplate{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.templates[id]
	if !ok || item.TenantID != tenantID {
		return AnswerSheetTemplate{}, ErrNotFound
	}
	if err := s.ensureExamPaperMutableLocked(item.ExamID); err != nil {
		return AnswerSheetTemplate{}, err
	}
	if item.Status != "draft" {
		return AnswerSheetTemplate{}, ErrTemplateLocked
	}
	if input.ExpectedRevision != item.Revision {
		return AnswerSheetTemplate{}, ErrConflict
	}
	var err error
	input.Layout, err = s.materializeTemplateOMRReferenceLocked(tenantID, item.ExamID, item.ExamPaperID, input.Layout)
	if err != nil {
		return AnswerSheetTemplate{}, err
	}
	if err := ValidateTemplateInput(input.Name, input.PageCount, input.Layout); err != nil {
		return AnswerSheetTemplate{}, ErrInvalidInput
	}
	item.Name, item.PageCount, item.Layout = input.Name, input.PageCount, input.Layout
	item.Revision++
	item.ContentHash = stableContentHash(input.Layout)
	item.UpdatedAt = time.Now().UTC()
	s.templates[id] = item
	s.invalidateReadiness(item.ExamID)
	return cloneTemplateSnapshot(item), nil
}

func (s *MemoryStore) LockTemplate(ctx context.Context, tenantID string, id string, userID string) (AnswerSheetTemplate, error) {
	return s.lockTemplateAtRevision(ctx, tenantID, id, userID, 0)
}

// LockTemplateAtRevision 在持有同一把写锁时核对用户已审阅的版本；旧空请求仍使用 LockTemplate。
func (s *MemoryStore) LockTemplateAtRevision(ctx context.Context, tenantID, id, userID string, expectedRevision int) (AnswerSheetTemplate, error) {
	if expectedRevision < 1 {
		return AnswerSheetTemplate{}, ErrInvalidInput
	}
	return s.lockTemplateAtRevision(ctx, tenantID, id, userID, expectedRevision)
}

func (s *MemoryStore) lockTemplateAtRevision(_ context.Context, tenantID, id, userID string, expectedRevision int) (AnswerSheetTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.templates[id]
	if !ok || item.TenantID != tenantID {
		return AnswerSheetTemplate{}, ErrNotFound
	}
	if err := s.ensureExamPaperMutableLocked(item.ExamID); err != nil {
		return AnswerSheetTemplate{}, err
	}
	if item.Status != "draft" {
		return AnswerSheetTemplate{}, ErrTemplateLocked
	}
	if expectedRevision > 0 && item.Revision != expectedRevision {
		return AnswerSheetTemplate{}, ErrConflict
	}
	item.Layout = NormalizeTemplateLayout(cloneTemplateLayout(item.Layout))
	var err error
	item.Layout, err = s.materializeTemplateOMRReferenceLocked(tenantID, item.ExamID, item.ExamPaperID, item.Layout)
	if err != nil {
		return AnswerSheetTemplate{}, err
	}
	if err := ValidateTemplateInput(item.Name, item.PageCount, item.Layout); err != nil {
		return AnswerSheetTemplate{}, ErrInvalidInput
	}
	if err := validateTemplateReadyToLock(item.Layout, s.readinessQuestionsLocked(tenantID, item.ExamID)); err != nil {
		return AnswerSheetTemplate{}, err
	}
	now := time.Now().UTC()
	item.Status, item.LockedBy, item.LockedAt, item.UpdatedAt = "locked", userID, &now, now
	item.Revision++
	item.ContentHash = stableContentHash(item.Layout)
	s.templates[id] = item
	s.invalidateReadiness(item.ExamID)
	return cloneTemplateSnapshot(item), nil
}

func (s *MemoryStore) CloneTemplate(_ context.Context, tenantID string, id string, userID string) (AnswerSheetTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.templates[id]
	if !ok || source.TenantID != tenantID {
		return AnswerSheetTemplate{}, ErrNotFound
	}
	if err := s.ensureExamPaperMutableLocked(source.ExamID); err != nil {
		return AnswerSheetTemplate{}, err
	}
	version := source.VersionNo + 1
	for _, item := range s.templates {
		if item.TenantID == tenantID && item.ExamID == source.ExamID && item.VersionNo >= version {
			version = item.VersionNo + 1
		}
	}
	now := time.Now().UTC()
	clone := cloneTemplateSnapshot(source)
	clone.ID, clone.VersionNo, clone.Revision = s.id("template"), version, 1
	clone.Name, clone.Status, clone.CreatedBy = source.Name+" 副本", "draft", userID
	clone.LockedBy, clone.LockedAt, clone.CreatedAt, clone.UpdatedAt = "", nil, now, now
	var err error
	clone.Layout, err = s.materializeTemplateOMRReferenceLocked(tenantID, clone.ExamID, clone.ExamPaperID, clone.Layout)
	if err != nil {
		return AnswerSheetTemplate{}, err
	}
	clone.ContentHash = stableContentHash(clone.Layout)
	s.templates[clone.ID] = clone
	s.invalidateReadiness(clone.ExamID)
	return cloneTemplateSnapshot(clone), nil
}

func (s *MemoryStore) Readiness(_ context.Context, tenantID string, examID string) (ReadinessResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readinessLocked(tenantID, examID), nil
}

func (s *MemoryStore) ConfirmReadiness(_ context.Context, tenantID string, examID string, userID string, input ConfirmReadinessInput) (ReadinessResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.readinessLocked(tenantID, examID)
	// 比较和状态变更在同一把锁内；核对后被修改的配置不能被旧确认冻结。
	if input.ExpectedConfigurationHash != "" && input.ExpectedConfigurationHash != result.ConfigurationHash {
		return result, ErrReadinessChanged
	}
	if !result.Ready {
		return result, ErrNotReady
	}
	now := time.Now().UTC()
	result.Confirmed, result.ConfirmedAt, result.ConfirmedBy = true, &now, userID
	s.readiness[examID] = result
	state := s.examState[examID]
	state.Status = "ready"
	s.examState[examID] = state
	return result, nil
}

func (s *MemoryStore) StartCollection(_ context.Context, tenantID string, examID string, _ string) (ReadinessResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.readinessLocked(tenantID, examID)
	confirmed, ok := s.readiness[examID]
	state := s.examState[examID]
	if !result.Ready || !ok || confirmed.ConfigurationHash != result.ConfigurationHash || state.Status != "ready" {
		return result, ErrNotReady
	}
	result.Confirmed, result.ConfirmedAt, result.ConfirmedBy = true, confirmed.ConfirmedAt, confirmed.ConfirmedBy
	state.Status = "collecting"
	s.examState[examID] = state
	return result, nil
}

func (s *MemoryStore) readinessLocked(tenantID string, examID string) ReadinessResult {
	state := s.examState[examID]
	papers := []Paper{}
	templates := []AnswerSheetTemplate{}
	for _, item := range s.papers {
		if item.TenantID == tenantID && item.ExamID == examID {
			papers = append(papers, item)
		}
	}
	questions := s.readinessQuestionsLocked(tenantID, examID)
	for _, item := range s.templates {
		if item.TenantID == tenantID && item.ExamID == examID {
			templates = append(templates, item)
		}
	}
	// 内存夹具只保存班级和学生数量；真实人员身份与冻结名册由 PostgreSQL 路径验证。
	result := buildReadiness(state.Total, state.ClassCount, state.StudentCount, papers, questions, templates)
	latest := s.latestReadinessImportLocked(tenantID, examID)
	if latest.ID != "" {
		result = withStructureReadiness(result, latest)
	}
	if confirmed, ok := s.readiness[examID]; ok && confirmed.ConfigurationHash == result.ConfigurationHash {
		result.Confirmed, result.ConfirmedAt, result.ConfirmedBy = true, confirmed.ConfirmedAt, confirmed.ConfirmedBy
	}
	return result
}

func (s *MemoryStore) readinessQuestionsLocked(tenantID, examID string) []Question {
	questions := []Question{}
	for _, item := range s.questions {
		if item.TenantID == tenantID && item.ExamID == examID && item.Status != "deleted" {
			if versions := s.rubrics[item.ID]; len(versions) > 0 {
				latest := versions[len(versions)-1]
				item.Rubric = &latest
			}
			questions = append(questions, item)
		}
	}
	return questions
}

func (s *MemoryStore) latestReadinessImportLocked(tenantID, examID string) PaperImportJob {
	var latest PaperImportJob
	for _, job := range s.imports {
		if job.TenantID == tenantID && job.ExamID == examID && (latest.ID == "" || job.CreatedAt.After(latest.CreatedAt)) {
			latest = job
		}
	}
	return latest
}

func (s *MemoryStore) paperBelongsToExam(tenantID string, examID string, paperID string) bool {
	item, ok := s.papers[paperID]
	return ok && item.TenantID == tenantID && item.ExamID == examID
}

func (s *MemoryStore) invalidateReadiness(examID string) {
	delete(s.readiness, examID)
	state := s.examState[examID]
	if state.Status == "ready" {
		state.Status = "configured"
		s.examState[examID] = state
	}
}

func (s *MemoryStore) materializeTemplateOMRReferenceLocked(tenantID, examID, examPaperID string, layout TemplateLayout) (TemplateLayout, error) {
	if layout.OMRProfile.Mode != OMRProfileModeTemplateDifference && layout.EditorState == nil {
		return BindTemplateOMRReference(layout, TemplateOMRReference{}), nil
	}
	paper, ok := s.papers[examPaperID]
	if !ok || paper.TenantID != tenantID || paper.ExamID != examID {
		return TemplateLayout{}, ErrInvalidInput
	}
	if err := validateTemplateEditorSource(layout, paper.File.HashSHA256); err != nil {
		return TemplateLayout{}, err
	}
	if layout.OMRProfile.Mode != OMRProfileModeTemplateDifference {
		return BindTemplateOMRReference(layout, TemplateOMRReference{}), nil
	}
	reference := TemplateOMRReference{
		Source:      OMRReferenceSourceExamPaper,
		FileAssetID: paper.FileAssetID,
		HashSHA256:  paper.File.HashSHA256,
		ContentType: paper.File.ContentType,
	}
	if err := ValidateTemplateOMRReference(reference); err != nil {
		return TemplateLayout{}, ErrInvalidInput
	}
	return BindTemplateOMRReference(layout, reference), nil
}
