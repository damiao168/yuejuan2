package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/regraderelease"
	"edugrade-enterprise/services/api-gateway/internal/releasegate"
	"edugrade-enterprise/services/api-gateway/internal/report"
	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"edugrade-enterprise/services/api-gateway/internal/studentportal"
)

type ReleaseStores struct {
	Score                   score.Store
	ScoreRelease            scorerelease.Store
	ReleaseGate             releasegate.Store
	StudentPortal           studentportal.Store
	Appeal                  appeal.Store
	PublishedQuestionAppeal appeal.PublishedQuestionAppealStore
	Report                  report.Store
}

type ReleaseModule struct {
	ScoreReleaseService            *scorerelease.Service
	ReleaseGateService             *releasegate.Service
	ScoreHandler                   *score.Handler
	ScoreReleaseHandler            *scorerelease.Handler
	ReleaseGateHandler             *releasegate.Handler
	StudentPortalHandler           *studentportal.Handler
	RegradeReleaseHandler          *regraderelease.Handler
	AppealHandler                  *appeal.Handler
	PublishedQuestionAppealHandler *appeal.PublishedQuestionAppealHandler
	ReportHandler                  *report.Handler
}

type ReleaseDependencies struct {
	Auth                  auth.AuditRecorder
	Exams                 exam.Store
	Regrade               *regrade.Service
	StudentQuestionImage  segment.CropImageReader
	StudentPaperPageImage segment.PageImageReader
	DB                    *sql.DB
	FileStore             files.Store
	ObjectStore           files.ObjectStorage
	FileBucket            string
}

// 发布协调器同时接入成绩发布与 regrade 阻断器；高分试卷能力只有数据库、文件、对象存储和 bucket 全部可用时才启用。
func NewReleaseModule(stores ReleaseStores, dependencies ReleaseDependencies) *ReleaseModule {
	scoreReleaseService := scorerelease.NewService(stores.ScoreRelease)
	var highScorePaper scorerelease.HighScorePaperManager
	if dependencies.DB != nil && dependencies.FileStore != nil && dependencies.ObjectStore != nil && dependencies.FileBucket != "" {
		highScorePaper = scorerelease.NewPostgresHighScorePaperManager(dependencies.DB, dependencies.FileStore, dependencies.ObjectStore, dependencies.FileBucket)
		scoreReleaseService.WithHighScorePaper(highScorePaper)
	}
	releaseGateService := releasegate.NewService(stores.ReleaseGate, scoreReleaseService).
		WithRegradeBlockerReader(regradeBlocker{service: dependencies.Regrade})
	return &ReleaseModule{
		ScoreReleaseService: scoreReleaseService,
		ReleaseGateService:  releaseGateService,
		ScoreHandler:        score.NewHandler(stores.Score, dependencies.Auth),
		ScoreReleaseHandler: scorerelease.NewHandler(scoreReleaseService, dependencies.Auth).
			WithPublicationPublisher(releaseGatePublisher{
				coordinator: releasegate.NewPublicationCoordinator(releaseGateService, scoreReleaseService),
			}).
			WithStudentQuestionImage(dependencies.StudentQuestionImage).
			WithStudentPaperPageImage(dependencies.StudentPaperPageImage).
			WithHighScorePaper(highScorePaper),
		ReleaseGateHandler:   releasegate.NewHandler(releaseGateService, dependencies.Auth),
		StudentPortalHandler: studentportal.NewHandler(studentportal.NewService(stores.StudentPortal)),
		RegradeReleaseHandler: regraderelease.NewHandler(
			regraderelease.NewService(dependencies.Regrade, scoreReleaseService),
		),
		AppealHandler: appeal.NewHandler(stores.Appeal, dependencies.Auth),
		PublishedQuestionAppealHandler: appeal.NewPublishedQuestionAppealHandler(
			appeal.NewPublishedQuestionAppealService(stores.PublishedQuestionAppeal), dependencies.Auth,
		).WithSegmentImage(dependencies.StudentQuestionImage).
			WithExamSummaryReader(publishedQuestionAppealExamReader{store: dependencies.Exams}),
		ReportHandler: report.NewHandler(stores.Report, dependencies.Auth),
	}
}

type publishedQuestionAppealExamReader struct{ store exam.Store }

func (reader publishedQuestionAppealExamReader) ReadQuestionAppealExamSummary(ctx context.Context, tenantID, examID string) (appeal.QuestionAppealExamSummary, error) {
	if reader.store == nil {
		return appeal.QuestionAppealExamSummary{}, exam.ErrNotFound
	}
	// 调用前已验证申请权限；用同租户单个考试边界读取，不能扩成全租户目录或为教师授予 exam:manage。
	item, err := reader.store.GetExam(ctx, auth.AccessScope{TenantID: tenantID, ExamIDs: []string{examID}}, examID)
	if err != nil || item.ID != examID || item.TenantID != tenantID {
		if err == nil {
			err = exam.ErrNotFound
		}
		return appeal.QuestionAppealExamSummary{}, err
	}
	return appeal.QuestionAppealExamSummary{Name: item.Name, Subject: item.Subject}, nil
}
