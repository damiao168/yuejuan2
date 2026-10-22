package server

import (
	"context"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/deps"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/middleware"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"edugrade-enterprise/services/api-gateway/internal/observability"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/releasegate"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"edugrade-enterprise/services/api-gateway/internal/submission"
)

type Server struct {
	handler http.Handler
}

// releaseGatePublisher 将 A20 生成证据的协调器适配到 A18 的受限发布接口。
// 证据在 A20 中保持只追加，通过其受作用域限制的端点读取，不作为可变发布输入原样传回。
type releaseGatePublisher struct {
	coordinator *releasegate.PublicationCoordinator
}

func (p releaseGatePublisher) PublishPublication(ctx context.Context, tenantID, examID, releaseID, actorID string) (scorerelease.Release, error) {
	release, _, err := p.coordinator.Publish(ctx, tenantID, examID, releaseID, actorID)
	return release, err
}

// regradeBlocker 只投影与发布相关的状态。
// A20 无需重评分数、复核人员或答案材料，就能判断发布是否必须等待修正计划。
type regradeBlocker struct{ service *regrade.Service }

func (b regradeBlocker) BlockingRegradeCount(ctx context.Context, tenantID, examID string) (int, error) {
	jobs, err := b.service.List(ctx, tenantID, examID, "")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, job := range jobs {
		// ready 计划已冻结，可安全地实例化为独立的后续发布。
		// 更早状态的证据仍在变化，必须阻断公开发布。
		if job.Status != regrade.StatusCancelled && job.Status != regrade.StatusReadyForRelease {
			count++
		}
	}
	return count, nil
}

// 生产启动先完成基础设施和事务存储图，再创建路由；任一装配失败都会关闭已打开资源，避免半初始化服务继续运行。
func New(cfg config.Config, logg *logger.Logger) (*Server, func(), error) {
	infra, err := newInfrastructure(cfg, logg)
	if err != nil {
		return nil, nil, err
	}
	modules, err := NewPostgresApplicationModules(infra)
	if err != nil {
		infra.Close()
		return nil, nil, err
	}
	parseExecutor, err := paper.NewParseTaskExecutor(modules.Exam.PaperImportService, modules.Capture.WorkerRuntimeStore, cfg.AIService.Timeout)
	if err != nil {
		infra.Close()
		return nil, nil, err
	}
	infra.startPaperParseExecutor(parseExecutor)
	infra.startRubricDraftJobs(modules.Exam.RubricDraftJobs)
	infra.startScoringCoordinator(modules.Grading.ScoringCoordinator)
	router := NewRouterComplete(RouterDependencies{
		Config: cfg, Logger: logg, Checkers: infra.Checkers, Metrics: infra.Metrics,
		Modules: modules,
	})
	return &Server{handler: router}, infra.Close, nil
}

type RouterDependencies struct {
	Config   config.Config
	Logger   *logger.Logger
	Checkers []deps.Checker
	Metrics  *observability.Registry
	Modules  ApplicationModules
}

func NewRouter(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, authStore auth.Store, orgStore org.Store, examStore exam.Store, paperStore paper.Store) http.Handler {
	return NewRouterWithFiles(cfg, logg, checkers, IdentityStores{Auth: authStore, Org: orgStore}, ExamPreparationStores{Exam: examStore, Paper: paperStore, Files: files.NewMemoryStore()}, files.NewMemoryObjectStorage())
}

// NewRouterWithFiles 沿用文件路由的内存提交存储，不使用 preparation 中传入的 Submissions。
func NewRouterWithFiles(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, identity IdentityStores, preparation ExamPreparationStores, objectStore files.ObjectStorage) http.Handler {
	preparation.Submissions = submission.NewMemoryStore()
	return NewRouterFull(cfg, logg, checkers, identity, preparation, objectStore)
}

// NewRouterFull 只替换身份、考试、试卷、文件和提交依赖；其他模块继续使用完整内存图中的默认依赖。
func NewRouterFull(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, identity IdentityStores, preparation ExamPreparationStores, objectStore files.ObjectStorage) http.Handler {
	stores := NewMemoryApplicationStores()
	stores.Identity = identity
	stores.Exam.Exam = preparation.Exam
	stores.Exam.Paper = preparation.Paper
	stores.Exam.Files = preparation.Files
	stores.Exam.Submissions = preparation.Submissions
	return NewRouterWithApplicationStores(cfg, logg, checkers, objectStore, stores)
}

func NewRouterWithApplicationStores(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, objectStore files.ObjectStorage, stores ApplicationStores) http.Handler {
	modules := NewApplicationModules(ApplicationDependencies{
		Config: cfg, ObjectStore: objectStore,
	}, stores)
	return NewRouterComplete(RouterDependencies{
		Config: cfg, Logger: logg, Checkers: checkers, Modules: modules,
	})
}

func NewMemoryRouter(cfg config.Config, logg *logger.Logger, checkers []deps.Checker, objectStore files.ObjectStorage, configure func(*ApplicationStores)) http.Handler {
	stores := NewMemoryApplicationStores()
	if configure != nil {
		configure(&stores)
	}
	return NewRouterWithApplicationStores(cfg, logg, checkers, objectStore, stores)
}

// 路由先注册资源，再统一套恢复、请求 ID、审计、指标、CSRF 和请求体限制中间件；单独的导入 mux 仍经过同一条链。
func NewRouterComplete(dependencies RouterDependencies) http.Handler {
	ctx := buildRouterContext(dependencies)
	mux := http.NewServeMux()

	registerQuestionBankRoutes(mux, ctx)
	registerSystemRoutes(mux, ctx)
	registerAuthRoutes(mux, ctx)
	registerOnboardingRoutes(mux, ctx)
	registerGovernanceRoutes(mux, ctx)
	registerSchoolAssistantRoutes(mux, ctx)
	registerPlatformSchoolRoutes(mux, ctx)
	registerOrganizationRoutes(mux, ctx)
	registerExamRoutes(mux, ctx)
	registerPaperRoutes(mux, ctx)
	registerFileRoutes(mux, ctx)
	registerCaptureRoutes(mux, ctx)
	registerWorkerRoutes(mux, ctx)
	registerProcessingRoutes(mux, ctx)
	registerGradingRoutes(mux, ctx)
	registerReleaseRoutes(mux, ctx)
	registerReviewRoutes(mux, ctx)
	mux.HandleFunc("/", ctx.system.NotFound)

	rootHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/question-bank/items/import-from-question/") {
			ctx.questionBankImportMux.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return middleware.Chain(
		rootHandler,
		middleware.RequestID(),
		middleware.AccessLog(dependencies.Logger, ctx.cfg.Observability.SlowRequestThreshold),
		// RequestID 在最外层，AccessLog 包住 Recover，确保 panic 也带关联标识并记录访问结果。
		middleware.Recover(dependencies.Logger),
		ctx.metrics.Middleware(),
		middleware.SecurityHeaders(),
		middleware.CORS(ctx.cfg.Security.CORSAllowedOrigins, ctx.cfg.Security.CORSAllowedMethods, ctx.cfg.Security.CORSAllowedHeaders),
		middleware.BrowserCSRF(ctx.cfg.Auth.SessionCookieName),
		middleware.BodyLimit(ctx.cfg.Security.MaxRequestBodyBytes, skipGlobalBodyLimit),
		middleware.BodyLimit(modelgovernance.ChatRequestBodyLimit, skipNonImageChat),
		middleware.BodyLimit(fileRequestBodyLimit(ctx.cfg.Files.MaxUploadBytes), skipNonFileUpload),
	)
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func useRealAIService(cfg config.Config) bool {
	if strings.TrimSpace(cfg.AIService.URL) == "" {
		return false
	}
	environment := strings.ToLower(strings.TrimSpace(cfg.Service.Environment))
	if environment == "production" || environment == "demo" || environment == "staging" {
		return cfg.AIService.Enabled
	}
	return true
}

// mock 只允许开发/测试环境，demo 还必须显式开启且没有真实 AI 地址；生产等环境始终拒绝。
func allowMockAI(cfg config.Config) bool {
	environment := strings.ToLower(strings.TrimSpace(cfg.Service.Environment))
	switch environment {
	case "", "development", "dev", "test", "local":
		return strings.TrimSpace(cfg.AIService.URL) == ""
	case "demo":
		return cfg.AIService.Enabled && cfg.AIService.AllowMock && strings.TrimSpace(cfg.AIService.URL) == ""
	default:
		return false
	}
}

func localModelBaseline(cfg config.Config) modelgovernance.LocalBaseline {
	return modelgovernance.LocalBaseline{
		ProviderKey:       cfg.AIService.ProviderKey,
		ProviderName:      "Local grading runtime",
		DeploymentKey:     cfg.AIService.DeploymentKey,
		ModelName:         cfg.AIService.ModelVersion,
		ModelVersion:      cfg.AIService.ModelVersion,
		AdapterType:       cfg.AIService.AdapterType,
		Region:            cfg.AIService.DeploymentRegion,
		CapabilityProfile: cfg.AIService.CapabilityProfile,
	}
}

func skipGlobalBodyLimit(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if r.URL.Path == "/api/v1/files" {
		return true
	}
	if !skipNonImageChat(r) {
		return true
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/exams/"), "/")
	// 页序与图文对话各自限制请求大小；其余 JSON 请求仍沿用全局限制。
	return strings.HasPrefix(r.URL.Path, "/api/v1/exams/") && len(parts) == 2 && parts[0] != "" && parts[1] == "answer-sheet-page-order"
}

func skipNonImageChat(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return true
	}
	// 只为真正支持图片的三个对话入口放宽限制，不匹配相邻业务路径。
	switch r.URL.Path {
	case "/api/v1/school-assistant/conversations/stream", "/api/v1/ai-chat/completions", "/api/v1/ai-chat/completions/stream":
		return false
	default:
		return true
	}
}

func skipNonFileUpload(r *http.Request) bool {
	return r.Method != http.MethodPost || r.URL.Path != "/api/v1/files"
}

func fileRequestBodyLimit(fileLimit int64) int64 {
	if fileLimit <= 0 {
		fileLimit = 100 * 1024 * 1024
	}
	return fileLimit + 1024*1024
}
