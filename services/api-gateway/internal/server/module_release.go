package server

import (
	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/auth"
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
}

func NewReleaseModule(stores ReleaseStores, dependencies ReleaseDependencies) *ReleaseModule {
	scoreReleaseService := scorerelease.NewService(stores.ScoreRelease)
	releaseGateService := releasegate.NewService(stores.ReleaseGate, scoreReleaseService).
		WithRegradeBlockerReader(regradeBlocker{service: dependencies.Regrade})
	return &ReleaseModule{
		ScoreHandler: score.NewHandler(stores.Score, dependencies.Auth),
		ScoreReleaseHandler: scorerelease.NewHandler(scoreReleaseService, dependencies.Auth).
			WithPublicationPublisher(releaseGatePublisher{
				coordinator: releasegate.NewPublicationCoordinator(releaseGateService, scoreReleaseService),
			}).
			WithStudentQuestionImage(dependencies.StudentQuestionImage).
			WithStudentPaperPageImage(dependencies.StudentPaperPageImage),
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
