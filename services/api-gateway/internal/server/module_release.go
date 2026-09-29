package server

import (
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/auth"
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
		ScoreHandler: score.NewHandler(stores.Score, dependencies.Auth),
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
		).WithSegmentImage(dependencies.StudentQuestionImage),
		ReportHandler: report.NewHandler(stores.Report, dependencies.Auth),
	}
}
