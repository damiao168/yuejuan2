package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/aidisagreement"
	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/answergroup"
	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/backmark"
	"edugrade-enterprise/services/api-gateway/internal/calibration"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/captureupload"
	"edugrade-enterprise/services/api-gateway/internal/dashboard"
	"edugrade-enterprise/services/api-gateway/internal/evidence"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/graderdrift"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"edugrade-enterprise/services/api-gateway/internal/imagequality"
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"edugrade-enterprise/services/api-gateway/internal/modelcalibration"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
	"edugrade-enterprise/services/api-gateway/internal/orchestrator"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/platformschools"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/qualitydashboard"
	"edugrade-enterprise/services/api-gateway/internal/questionbank"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/releasegate"
	"edugrade-enterprise/services/api-gateway/internal/report"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/reviewannotation"
	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"edugrade-enterprise/services/api-gateway/internal/studentportal"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"fmt"
	"reflect"
	"strings"
)

type ApplicationStores struct {
	Identity        IdentityStores
	Exam            ExamPreparationStores
	Capture         CaptureProcessingStores
	AIFoundation    AIFoundationStores
	Grading         GradingQualityStores
	Release         ReleaseStores
	AIGovernance    AIGovernanceStores
	PlatformSchools platformschools.Store
	Idempotency     idempotency.Store
}

func NewMemoryApplicationStores() ApplicationStores {
	mathStore := mathunderstanding.NewMemoryStore()
	stores := ApplicationStores{
		Identity: IdentityStores{Auth: auth.NewMemoryStore(), Org: org.NewMemoryStore()},
		Exam: ExamPreparationStores{
			Exam: exam.NewMemoryStore(), Paper: paper.NewMemoryStore(), Files: files.NewMemoryStore(),
			Submissions: submission.NewMemoryStore(), Segments: segment.NewMemoryStore(), Assessments: assessment.NewMemoryStore(),
			QuestionBank: questionbank.NewMemoryStore(),
		},
		Capture: CaptureProcessingStores{
			ImageQuality: imagequality.NewMemoryStore(), WorkerRuntime: workerruntime.NewMemoryStore(),
			OCR: ocrpkg.NewMemoryStore(), OCRQueue: ocrpkg.NewMemoryQueue(), Orchestrator: orchestrator.NewMemoryStore(),
			Capture: capture.NewMemoryStore(), CaptureUpload: captureupload.NewMemoryStore(), Processing: processing.NewMemoryStore(),
		},
		AIFoundation: AIFoundationStores{
			GradingEvaluation: gradingevaluation.NewMemoryStore(), ModelCalibration: modelcalibration.NewMemoryStore(),
			Disagreement: aidisagreement.NewMemoryStore(),
		},
		Grading: GradingQualityStores{
			Grading: grading.NewMemoryStore(), Subjective: subjective.NewMemoryStore(), Evidence: evidence.NewMemoryStore(),
			Review: review.NewMemoryStore(), ReviewAnnotation: reviewannotation.NewMemoryStore(),
			GoldPaper: goldpaper.NewMemoryStore(), Calibration: calibration.NewMemoryStore(),
			AnswerGroup: answergroup.NewMemoryStore(nil, answergroup.DefaultPolicy()), Backmark: backmark.NewMemoryStore(),
			Regrade: regrade.NewMemoryStore(), GraderDrift: graderdrift.NewMemoryStore(), SeedQuality: seedquality.NewMemoryStore(),
		},
		Release: ReleaseStores{
			Score: score.NewMemoryStore(), ScoreRelease: scorerelease.NewMemoryStore(), ReleaseGate: releasegate.NewMemoryStore(),
			StudentPortal: studentportal.NewMemoryStore(), Appeal: appeal.NewMemoryStore(),
			PublishedQuestionAppeal: appeal.NewPublishedQuestionAppealMemoryStore(), Report: report.NewMemoryStore(),
		},
		AIGovernance: AIGovernanceStores{
			ModelGovernance: modelgovernance.NewMemoryStore(), MathUnderstanding: mathStore,
			MathCorrections: mathunderstanding.NewMemoryCorrectionStore(mathStore), MathPilotGates: mathunderstanding.NewMemoryPilotGateStore(),
		},
		PlatformSchools: platformschools.NewMemoryStore(),
		Idempotency:     idempotency.NewMemoryStore(),
	}
	return stores
}

// NewPostgresApplicationStores is the single production persistence graph.
// Integration tests use this factory too, so adding a new application store
// cannot silently leave the production-style suite backed by memory.
func NewPostgresApplicationStores(infra *Infrastructure) (ApplicationStores, error) {
	assessmentStore := assessment.NewPostgresStore(infra.DB)
	gradingStores := GradingQualityStores{
		Grading:          grading.NewPostgresStore(infra.DB),
		Subjective:       subjective.NewPostgresStore(infra.DB),
		Evidence:         evidence.NewPostgresStore(infra.DB),
		Review:           review.NewPostgresStore(infra.DB),
		ReviewAnnotation: reviewannotation.NewPostgresStore(infra.DB),
		GoldPaper:        goldpaper.NewPostgresStore(infra.DB),
		Calibration:      calibration.NewPostgresStore(infra.DB),
		AnswerGroup:      answergroup.NewPostgresStore(infra.DB, nil, answergroup.DefaultPolicy()),
		Backmark:         backmark.NewPostgresStore(infra.DB),
		Regrade:          regrade.NewPostgresStore(infra.DB),
		GraderDrift:      graderdrift.NewPostgresStore(infra.DB),
		SeedQuality:      seedquality.NewPostgresStore(infra.DB),
	}
	gradingStores.QualityDashboard = newPostgresQualityDashboard(infra.DB, gradingStores, assessmentStore)

	credentialCipher, err := modelgovernance.NewCredentialCipher(infra.Config.ModelSecrets.MasterKey)
	if err != nil {
		return ApplicationStores{}, err
	}
	governanceStore := modelgovernance.NewPostgresStore(infra.DB, credentialCipher)
	if strings.EqualFold(strings.TrimSpace(infra.Config.Service.Environment), "production") && infra.Config.AIService.Enabled {
		if err := governanceStore.ValidateManagedProductionReadiness(context.Background()); err != nil {
			return ApplicationStores{}, err
		}
	}
	mathStore := mathunderstanding.NewPostgresStore(infra.DB)

	stores := ApplicationStores{
		Identity: IdentityStores{Auth: auth.NewPostgresStore(infra.DB), Org: org.NewPostgresStore(infra.DB)},
		Exam: ExamPreparationStores{
			Exam: exam.NewPostgresStore(infra.DB), Paper: paper.NewPostgresStore(infra.DB), Files: files.NewPostgresStore(infra.DB),
			Submissions: submission.NewPostgresStore(infra.DB), Segments: segment.NewPostgresStore(infra.DB), Assessments: assessmentStore,
			QuestionBank:           questionbank.NewPostgresStore(infra.DB),
			DashboardOrganizations: dashboard.NewPostgresOrganizationSummaryStore(infra.DB), DashboardActivities: dashboard.NewPostgresActivityStore(infra.DB),
		},
		Capture: CaptureProcessingStores{
			ImageQuality: imagequality.NewPostgresStore(infra.DB), WorkerRuntime: workerruntime.NewPostgresStore(infra.DB),
			OCR: ocrpkg.NewPostgresStore(infra.DB), Orchestrator: orchestrator.NewPostgresStore(infra.DB),
			Capture: capture.NewPostgresStoreWithBarcodeKeyring(infra.DB, capture.BarcodeKeyring{
				ActiveKeyID: infra.Config.Barcode.ActiveKeyID, Keys: infra.Config.Barcode.HMACKeys,
			}),
			CaptureUpload: captureupload.NewPostgresStore(infra.DB), Processing: processing.NewPostgresStore(infra.DB),
		},
		AIFoundation: AIFoundationStores{
			Eligibility: aieligibility.NewPostgresStore(infra.DB), GradingEvaluation: gradingevaluation.NewPostgresStore(infra.DB),
			ModelCalibration: modelcalibration.NewPostgresStore(infra.DB), Disagreement: aidisagreement.NewPostgresStore(infra.DB),
		},
		Grading: gradingStores,
		Release: ReleaseStores{
			Score: score.NewPostgresStore(infra.DB), ScoreRelease: scorerelease.NewPostgresStore(infra.DB, gradingStores.QualityDashboard),
			ReleaseGate: releasegate.NewPostgresStore(infra.DB), StudentPortal: studentportal.NewPostgresStore(infra.DB),
			Appeal: appeal.NewPostgresStore(infra.DB), PublishedQuestionAppeal: appeal.NewPublishedQuestionAppealPostgresStore(infra.DB),
			Report: report.NewPostgresStore(infra.DB),
		},
		AIGovernance: AIGovernanceStores{
			ModelGovernance: governanceStore, MathUnderstanding: mathStore,
			MathCorrections: mathunderstanding.NewPostgresCorrectionStore(infra.DB, mathStore), MathPilotGates: mathunderstanding.NewPostgresPilotGateStore(infra.DB),
		},
		PlatformSchools: platformschools.NewPostgresStore(infra.DB),
		Idempotency:     idempotency.NewPostgresStore(infra.DB),
	}
	if err := validatePostgresStoreGraph(stores); err != nil {
		return ApplicationStores{}, err
	}
	return stores, nil
}

func validatePostgresStoreGraph(stores ApplicationStores) error {
	return validatePostgresStoreValue(reflect.ValueOf(stores), "stores")
}

func validatePostgresStoreValue(value reflect.Value, path string) error {
	typeOfValue := value.Type()
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		name := path + "." + typeOfValue.Field(index).Name
		if name == "stores.Capture.OCRQueue" {
			// PostgreSQL worker runtime is the durable OCR queue. This legacy
			// seam is used only by the memory router when no runtime is present.
			continue
		}
		switch field.Kind() {
		case reflect.Struct:
			if err := validatePostgresStoreValue(field, name); err != nil {
				return err
			}
		case reflect.Interface, reflect.Pointer:
			for field.Kind() == reflect.Interface && !field.IsNil() {
				field = field.Elem()
			}
			if field.Kind() != reflect.Pointer && field.Kind() != reflect.Interface {
				continue
			}
			if field.IsNil() {
				return fmt.Errorf("production application store %s is not configured", name)
			}
		}
	}
	return nil
}

func newPostgresQualityDashboard(db *sql.DB, stores GradingQualityStores, assessments assessment.Store) *qualitydashboard.Service {
	calibrationService := calibration.NewService(stores.Calibration, stores.GoldPaper)
	seedQualityService := seedquality.NewService(stores.SeedQuality, stores.GoldPaper, calibrationService, assessments)
	graderDriftService := graderdrift.NewService(stores.GraderDrift, seedQualityService, calibrationService)
	backmarkService := backmark.NewService(stores.Backmark)
	if contextStore, ok := stores.Review.(review.TaskContextStore); ok {
		backmarkService.WithContextSource(contextStore)
	}
	backmarkService.WithTaskSource(stores.Review)
	return newQualityDashboardService(db, stores, graderDriftService, backmarkService)
}
