package exam

import (
	"context"
	"fmt"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func (s *MemoryStore) CreateExamSession(ctx context.Context, scope auth.AccessScope, createdBy string, input CreateSessionInput) (ExamSession, error) {
	if !scopeAllowsRequestedClasses(scope, input.SchoolID, input.ClassIDs) || !scopeAllowsRequestedGrade(scope, input.GradeID) {
		return ExamSession{}, ErrScopeForbidden
	}
	commandKey := ""
	requestHash, hashErr := examSessionCommandHash(input)
	if hashErr != nil {
		return ExamSession{}, ErrInvalidInput
	}
	if input.CommandID != "" {
		// 锁覆盖查回执和创建全过程；同租户、操作者、命令键只能复用同一份请求。
		commandKey = scope.TenantID + "\x00" + createdBy + "\x00" + input.CommandID
		s.sessionMu.Lock()
		defer s.sessionMu.Unlock()
		if existing, ok := s.sessionsByCommand[commandKey]; ok {
			if s.sessionCommandHash[commandKey] != requestHash {
				return ExamSession{}, ErrCommandConflict
			}
			return cloneExamSession(existing), nil
		}
	}
	now := time.Now().UTC()
	appealEnabled := true
	if input.AppealEnabled != nil {
		appealEnabled = *input.AppealEnabled
	}
	templateVersion := 0
	if input.TemplateID != "" {
		template, err := s.GetExamTemplate(ctx, scope, input.TemplateID)
		if err != nil {
			return ExamSession{}, ErrInvalidInput
		}
		templateVersion = template.Version
	}
	session := ExamSession{
		ID: fmt.Sprintf("exam-session-%d", now.UnixNano()), TenantID: scope.TenantID,
		SchoolID: input.SchoolID, GradeID: input.GradeID, TemplateID: input.TemplateID, TemplateVersion: templateVersion, Name: input.Name,
		ExamType: input.ExamType, Status: "draft", GradingMode: input.GradingMode,
		AppealEnabled: appealEnabled, PublishPolicy: input.PublishPolicy, CreatedBy: createdBy,
		CommandID: input.CommandID,
		Revision:  1, CreatedAt: now, UpdatedAt: now,
	}
	for _, subject := range input.Subjects {
		classes := subject.ClassIDs
		if len(classes) == 0 {
			classes = input.ClassIDs
		}
		child, err := s.CreateExam(ctx, scope, createdBy, CreateInput{
			SchoolID: input.SchoolID, Name: fmt.Sprintf("%s · %s", input.Name, subjectDisplayName(subject.Subject)),
			Subject: subject.Subject, ExamType: input.ExamType, TotalScore: subject.TotalScore,
			GradingMode: input.GradingMode, AppealEnabled: input.AppealEnabled,
			PublishPolicy: input.PublishPolicy, ClassIDs: classes,
		})
		if err != nil {
			return ExamSession{}, err
		}
		child.SessionID = session.ID
		child.SessionName = session.Name
		child.SessionGradeID = session.GradeID
		s.mu.Lock()
		s.items[child.ID] = child
		s.mu.Unlock()
		session.Exams = append(session.Exams, child)
	}
	if commandKey != "" {
		s.sessionsByCommand[commandKey] = cloneExamSession(session)
		s.sessionCommandHash[commandKey] = requestHash
	}
	return session, nil
}

func (s *MemoryStore) RecoverExamSessionCommand(_ context.Context, scope auth.AccessScope, createdBy, commandID string) (ExamSessionCommandResult, error) {
	key := scope.TenantID + "\x00" + createdBy + "\x00" + commandID
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	session, ok := s.sessionsByCommand[key]
	if !ok {
		return ExamSessionCommandResult{CommandID: commandID, Status: "not_accepted"}, nil
	}
	if !scopeAllowsRequestedGrade(scope, session.GradeID) || (!scope.TenantWide && len(scope.SchoolIDs) > 0 && !scope.AllowsSchool(session.SchoolID)) {
		return ExamSessionCommandResult{}, ErrScopeForbidden
	}
	cloned := cloneExamSession(session)
	return ExamSessionCommandResult{CommandID: commandID, Status: "succeeded", Session: &cloned}, nil
}

func cloneExamSession(input ExamSession) ExamSession {
	out := input
	out.Exams = append([]Exam(nil), input.Exams...)
	for index := range out.Exams {
		out.Exams[index].ClassIDs = cloneStrings(input.Exams[index].ClassIDs)
	}
	return out
}
