// Generated from services/api-gateway/openapi/edugrade-api.openapi.json. DO NOT EDIT.

import type { ApiTransport } from "../runtime";
import { appendQuery, fillPath } from "../runtime";
import type {
  OnboardingReadiness,
  MFAStatusResponse,
  TOTPEnrollmentRequest,
  TOTPEnrollmentResponse,
  TOTPConfirmationRequest,
  MFARecoveryCodesResponse,
  MFAChallengeStartRequest,
  MFAChallengeStartResponse,
  MFAChallengeVerifyRequest,
  MFAChallengeVerifyResponse,
  MFACommandRequest,
  MFADisableResponse,
  AuthLoginRequest,
  AuthLoginResponse,
  AuthTokenResponse,
  ReauthenticateRequest,
  SessionLockResponse,
  ReauthenticateResponse,
  OneTimeTokenRequest,
  CompleteOneTimePasswordRequest,
  ActivationPreviewResponse,
  RecoveryPreviewResponse,
  ActivationCompleteResponse,
  RecoveryCompleteResponse,
  ManagedUserRecoveryResponse,
  ManagedUserActivationDeliveryResponse,
  SecurityEventListResponse,
  CaptureUploadRecoveryResponse,
  CaptureBatchCommandResponse,
  CaptureBatchResponse,
  CaptureBatchCreateRequest,
  MathRubricScoreResponse,
  FailMathVerificationRuntimeRequest,
  MathVerificationFailureResponse,
  MathVerificationRuntimeInputResponse,
  CompleteMathVerificationRuntimeRequest,
  MathVerificationRuntimeResponse,
  MathUnderstandingRuntimeInputResponse,
  CompleteMathUnderstandingRuntimeRequest,
  MathUnderstandingRuntimeResponse,
  CreateMathCorrectionRequest,
  MathUnderstandingResponse,
  MathCorrectionListResponse,
  MathCorrectionResponse,
  MathTrainingExportResponse,
  EvaluateMathPilotGateRequest,
  MathPilotGateResponse,
  MathPilotGateListResponse,
  ExamWorkspaceResponse,
  CandidateRefreshResponse,
  ExamPage,
  SubmissionPage,
  ReviewTaskPage,
  ReviewTaskContextResponse,
  StudentQuestionReviewAnnotationListResponse,
  CreateReviewAnnotationRequest,
  UpdateReviewAnnotationRequest,
  DeleteRevisionRequest,
  ReviewAnnotationResponse,
  ReviewAnnotationListResponse,
  CreateReviewCommentTemplateRequest,
  UpdateReviewCommentTemplateRequest,
  ReviewCommentTemplateResponse,
  ReviewCommentTemplateListResponse,
  AppealPage,
  BatchEnqueueResponse,
  EducationStage,
  SubjectCode,
  SubjectProfileListResponse,
  QuestionArchetypeListResponse,
  PutQuestionAssessmentProfileRequest,
  QuestionAssessmentProfileResponse,
  ExamQuestionAssessmentSnapshotResponse,
  GoldPaperStatus,
  GoldPaperResponse,
  GoldPaperListResponse,
  CreateGoldPaperVersionRequest,
  NominateGoldPaperRequest,
  RetireGoldPaperRequest,
  GoldCoverageResponse,
  PutCalibrationPolicyRequest,
  CalibrationPolicyResponse,
  CreateCalibrationSessionRequest,
  CalibrationSessionResponse,
  SubmitCalibrationAttemptRequest,
  GraderQualificationResponse,
  SubmitCalibrationAttemptResponse,
  BuildAnswerGroupsRequest,
  ReviewAnswerGroupSampleRequest,
  PutAnswerGroupDecisionRequest,
  ConfirmAnswerGroupRequest,
  RollbackAnswerGroupRequest,
  BuildAnswerGroupsResponse,
  AnswerGroupListResponse,
  AnswerGroupResponse,
  AnswerGroupMutationResponse,
  AnswerGroupMetricsResponse,
  ConfirmAnswerGroupResponse,
  RollbackAnswerGroupResponse,
  QualityDashboardResponse,
  PutAIEligibilityPolicyRequest,
  AIEligibilityPolicyResponse,
  AIEligibilityDecisionResponse,
  CreateGradingEvaluationRequest,
  AddGradingEvaluationObservationRequest,
  GradingEvaluationRunResponse,
  GradingEvaluationRunListResponse,
  GradingEvaluationObservationResponse,
  GradingEvaluationSliceMetricsResponse,
  GradingEvaluationResponseDifficultyResponse,
  GradingEvaluationQualitySummaryResponse,
  InvalidationReasonRequest,
  CreateModelCalibrationRequest,
  AddModelCalibrationEvidenceRequest,
  RecordModelScoreCandidateRequest,
  ModelCalibrationResponse,
  ModelCalibrationListResponse,
  ModelCalibrationEvidenceResponse,
  ModelCalibrationEvidenceListResponse,
  ModelScoreCandidateResponse,
  AIHumanDisagreementSeverity,
  AIHumanDisagreementStatus,
  ClassifyAIHumanDisagreementRequest,
  RouteAIHumanDisagreementRequest,
  AIHumanDisagreementResponse,
  AIHumanDisagreementListResponse,
  AIHumanDisagreementDatasetResponse,
  ScoreReleaseDetail,
  CreateScoreReleaseRequest,
  CreateScoreReleaseRollbackRequest,
  ScoreReleaseResponse,
  ScoreReleaseListResponse,
  ScoreReleaseGateResponse,
  ScoreReleaseDiffResponse,
  StudentPublishedResultResponse,
  StudentPublishedQuestionResponse,
  RegradeGraderContextResponse,
  CreateRegradePreviewRequest,
  CreateRegradeJobRequest,
  RecordRegradeCandidateRequest,
  ReviewRegradeItemRequest,
  CreateRegradeScoreReleaseRequest,
  RegradePreviewResponse,
  RegradeSummaryResponse,
  RegradeJobResponse,
  RegradeJobListResponse,
  RegradeItemResponse,
  RegradeWorkItemResponse,
  RegradeWorkItemListResponse,
  CreateReleaseGatePolicyRequest,
  PreviewReleaseGateRequest,
  RequestReleaseGateWaiverRequest,
  DecideReleaseGateWaiverRequest,
  ReleaseGatePolicyResponse,
  ReleaseGateEvidenceResponse,
  ReleaseGateWaiverResponse,
  StudentPublishedExamListResponse,
  CreatePublishedQuestionAppealRequest,
  StartQuestionAppealReviewRequest,
  DecideQuestionAppealRequest,
  ResolveQuestionAppealRequest,
  PublishedQuestionAppealResponse,
  StudentPublishedQuestionAppealResponse,
  PublishedQuestionAppealListResponse,
  StudentPublishedQuestionAppealListResponse,
  PublishedQuestionAppealEventsResponse,
  PublishedQuestionAppealContextResponse,
  CaptureUploadInitRequest,
  CaptureUploadCompleteRequest,
  CaptureUploadSession,
  CaptureUploadInitResponse,
  CaptureUploadChunkResponse,
  GraderQualityWindowListResponse,
  RecomputeGraderDriftRequest,
  RecomputeGraderDriftResponse,
  GradingQualityIncidentListResponse,
  GradingQualityIncidentResponse,
  BackmarkPreviewRequest,
  CreateBackmarkBatchRequest,
  BackmarkPreviewResponse,
  BackmarkSummaryResponse,
  BackmarkRegradeInput,
  BackmarkBatchListResponse,
  BackmarkGraderItemResponse,
  BackmarkGraderItemListResponse,
  BackmarkGraderContextResponse,
  SubmitBackmarkItemRequest,
  SubmitBackmarkItemResponse,
  ProcessingStage,
  ProcessingExceptionSeverity,
  ProcessingExceptionStatus,
  ProcessingSummaryResponse,
  ProcessingExceptionListResponse,
  AssignProcessingExceptionRequest,
  ResolveProcessingExceptionRequest,
  ProcessingExceptionResponse,
  CreateExamSessionRequest,
  ExamSessionResponse,
  ExamSessionCommandResponse,
  CreatePaperImportRequest,
  AddPaperImportSourcesRequest,
  ReplacePaperImportSourcesRequest,
  ReviewPaperImportRequest,
  PaperImportResponse,
  PaperImportListResponse,
  ProcessingRetryResponse,
  StartScoringRunRequest,
  ScoringRunResponse,
  ScoringCommandRecovery,
  SubjectiveAIGradeRequest,
  SubjectiveAIGradeResponse,
  SubjectiveBatchResponse,
  SubjectiveBatchCreateRequest,
  SubjectiveBatchCommandRecovery,
  SubjectiveEnqueueCommandRecovery,
  ReviewCommandSubmitResult,
  ReviewCommandSubmitGradeInput,
  ReviewCommandArbitrationSubmitResult,
  ReviewCommandSubmitArbitrationInput,
  ScoreCommandSubmissionGrade,
  ScoreCommandConfirmInput,
  ScoreCommandPublishResult,
  ScoreCommandPublishInput,
  BusinessCommandReceipt,
  CreateQuestionBankRequest,
  UpdateQuestionBankRequest,
  CreateQuestionBankItemRequest,
  CreateQuestionBankVersionRequest,
  UpdateQuestionBankVersionRequest,
  QuestionBankResponse,
  QuestionBankItemResponse,
  QuestionBankItemCreateResponse,
  QuestionBankVersionResponse,
  QuestionBankPage,
  QuestionBankItemPage,
  QuestionBankVersionPage,
  QuestionBankReviewRequest,
  UpdateQuestionBankScoringRequest,
  QuestionBankReviewerBindingRequest,
  QuestionBankReviewsResponse,
  MaterializeQuestionBankRequest,
  MaterializeQuestionBankResponse,
  CreateQuestionBankRubricTemplateRequest,
  QuestionBankMetadataSchemaResponse,
  UpdateQuestionBankMetadataSchemaRequest,
  ValidateQuestionBankMetadataRequest,
  QuestionBankMetadataValidationResult,
  QuestionBankACLResponse,
  UpdateQuestionBankACLRequest,
  RetireQuestionBankItemRequest,
  QuestionBankSearchPage,
  QuestionBankImportPreviewRequest,
  QuestionBankImportPreviewBatch,
  QuestionBankImportRequest,
  QuestionBankImportResult,
  QuestionBankBatchImportRequest,
  QuestionBankBatchImportResult
} from "./types";

export interface operations {
  "createSubjectiveAIGrade": { args: { path: { "id": string; }; body: SubjectiveAIGradeRequest; signal?: AbortSignal; }; response: SubjectiveAIGradeResponse; };
  "getMathRubricScore": { args: { path: { "segmentId": string; }; signal?: AbortSignal; }; response: MathRubricScoreResponse; };
  "getMathUnderstandingRuntimeInput": { args: { path: { "taskId": string; }; signal?: AbortSignal; }; response: MathUnderstandingRuntimeInputResponse; };
  "completeMathUnderstandingRuntimeTask": { args: { path: { "taskId": string; }; body: CompleteMathUnderstandingRuntimeRequest; signal?: AbortSignal; }; response: MathUnderstandingRuntimeResponse; };
  "getMathVerificationRuntimeInput": { args: { path: { "taskId": string; }; signal?: AbortSignal; }; response: MathVerificationRuntimeInputResponse; };
  "completeMathVerificationRuntimeTask": { args: { path: { "taskId": string; }; body: CompleteMathVerificationRuntimeRequest; signal?: AbortSignal; }; response: MathVerificationRuntimeResponse; };
  "failMathVerificationRuntimeTask": { args: { path: { "taskId": string; }; body: FailMathVerificationRuntimeRequest; signal?: AbortSignal; }; response: MathVerificationFailureResponse; };
  "login": { args: { body: AuthLoginRequest; signal?: AbortSignal; }; response: AuthLoginResponse; };
  "createAccessToken": { args: { body: AuthLoginRequest; signal?: AbortSignal; }; response: AuthTokenResponse; };
  "lockPublicComputerSession": { args: { signal?: AbortSignal; }; response: SessionLockResponse; };
  "reauthenticateSession": { args: { body: ReauthenticateRequest; signal?: AbortSignal; }; response: ReauthenticateResponse; };
  "getMFAStatus": { args: { signal?: AbortSignal; }; response: MFAStatusResponse; };
  "enrollTOTP": { args: { body: TOTPEnrollmentRequest; signal?: AbortSignal; }; response: TOTPEnrollmentResponse; };
  "confirmTOTP": { args: { body: TOTPConfirmationRequest; signal?: AbortSignal; }; response: MFARecoveryCodesResponse; };
  "startMFAChallenge": { args: { body: MFAChallengeStartRequest; signal?: AbortSignal; }; response: MFAChallengeStartResponse; };
  "verifyMFAChallenge": { args: { body: MFAChallengeVerifyRequest; signal?: AbortSignal; }; response: MFAChallengeVerifyResponse; };
  "disableTOTP": { args: { body: MFACommandRequest; signal?: AbortSignal; }; response: MFADisableResponse; };
  "rotateMFARecoveryCodes": { args: { body: MFACommandRequest; signal?: AbortSignal; }; response: MFARecoveryCodesResponse; };
  "listPersonalSecurityEvents": { args: { signal?: AbortSignal; }; response: SecurityEventListResponse; };
  "verifyAccountActivation": { args: { body: OneTimeTokenRequest; signal?: AbortSignal; }; response: ActivationPreviewResponse; };
  "completeAccountActivation": { args: { body: CompleteOneTimePasswordRequest; signal?: AbortSignal; }; response: ActivationCompleteResponse; };
  "verifyCredentialRecovery": { args: { body: OneTimeTokenRequest; signal?: AbortSignal; }; response: RecoveryPreviewResponse; };
  "completeCredentialRecovery": { args: { body: CompleteOneTimePasswordRequest; signal?: AbortSignal; }; response: RecoveryCompleteResponse; };
  "createManagedUserCredentialRecovery": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: ManagedUserRecoveryResponse; };
  "reissueManagedUserActivation": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: ManagedUserActivationDeliveryResponse; };
  "updateManagedUserStatus": { args: { path: { "id": string; }; body: { "status": "active" | "disabled"; }; signal?: AbortSignal; }; response: { "user": { "id": string; "username": string; "display_name": string; "status": "active" | "disabled"; "roles": Array<string>; "school_id"?: string; "phone_masked"?: string; "employee_no"?: string; "activated_at"?: string; "last_login_at"?: string; "created_at"?: string; }; }; };
  "listSubmissionPageQualityRuns": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: { "runs": Array<Record<string, unknown>>; }; };
  "recoverCaptureUpload": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: CaptureUploadRecoveryResponse; };
  "recoverCaptureBatchCommand": { args: { path: { "examId": string; "commandId": string; }; signal?: AbortSignal; }; response: CaptureBatchCommandResponse; };
  "createCaptureBatch": { args: { path: { "examId": string; }; headers: { "Idempotency-Key": string; }; body: CaptureBatchCreateRequest; signal?: AbortSignal; }; response: CaptureBatchResponse; };
  "listExams": { args: { query?: { "limit"?: number; "cursor"?: string; "status"?: string; "school_id"?: string; }; signal?: AbortSignal; }; response: ExamPage; };
  "refreshExamCandidates": { args: { path: { "examId": string; }; signal?: AbortSignal; }; response: CandidateRefreshResponse; };
  "getExamWorkspace": { args: { path: { "examId": string; }; signal?: AbortSignal; }; response: ExamWorkspaceResponse; };
  "listExamSubmissions": { args: { path: { "examId": string; }; query?: { "limit"?: number; "cursor"?: string; }; signal?: AbortSignal; }; response: SubmissionPage; };
  "listReviewTasks": { args: { query?: { "limit"?: number; "cursor"?: string; "status"?: string; "assigned_to"?: string; "exam_id"?: string; }; signal?: AbortSignal; }; response: ReviewTaskPage; };
  "getReviewTaskContext": { args: { path: { "taskId": string; }; signal?: AbortSignal; }; response: ReviewTaskContextResponse; };
  "listReviewAnnotations": { args: { path: { "taskId": string; }; signal?: AbortSignal; }; response: ReviewAnnotationListResponse; };
  "createReviewAnnotation": { args: { path: { "taskId": string; }; body: CreateReviewAnnotationRequest; signal?: AbortSignal; }; response: ReviewAnnotationResponse; };
  "getReviewAnnotation": { args: { path: { "annotationId": string; }; signal?: AbortSignal; }; response: ReviewAnnotationResponse; };
  "updateReviewAnnotation": { args: { path: { "annotationId": string; }; body: UpdateReviewAnnotationRequest; signal?: AbortSignal; }; response: ReviewAnnotationResponse; };
  "deleteReviewAnnotation": { args: { path: { "annotationId": string; }; body: DeleteRevisionRequest; signal?: AbortSignal; }; response: void; };
  "listReviewCommentTemplates": { args: { signal?: AbortSignal; }; response: ReviewCommentTemplateListResponse; };
  "createReviewCommentTemplate": { args: { body: CreateReviewCommentTemplateRequest; signal?: AbortSignal; }; response: ReviewCommentTemplateResponse; };
  "getReviewCommentTemplate": { args: { path: { "templateId": string; }; signal?: AbortSignal; }; response: ReviewCommentTemplateResponse; };
  "updateReviewCommentTemplate": { args: { path: { "templateId": string; }; body: UpdateReviewCommentTemplateRequest; signal?: AbortSignal; }; response: ReviewCommentTemplateResponse; };
  "deleteReviewCommentTemplate": { args: { path: { "templateId": string; }; body: DeleteRevisionRequest; signal?: AbortSignal; }; response: void; };
  "useReviewCommentTemplate": { args: { path: { "shortcut": string; }; signal?: AbortSignal; }; response: ReviewCommentTemplateResponse; };
  "nominateGoldPaper": { args: { path: { "examId": string; "questionId": string; }; body: NominateGoldPaperRequest; signal?: AbortSignal; }; response: GoldPaperResponse; };
  "getGoldCoverage": { args: { path: { "examId": string; "questionId": string; }; signal?: AbortSignal; }; response: GoldCoverageResponse; };
  "listGoldPapers": { args: { query?: { "exam_id"?: string; "question_id"?: string; "status"?: GoldPaperStatus; }; signal?: AbortSignal; }; response: GoldPaperListResponse; };
  "getGoldPaper": { args: { path: { "goldPaperId": string; }; signal?: AbortSignal; }; response: GoldPaperResponse; };
  "createGoldPaperVersion": { args: { path: { "goldPaperId": string; }; body: CreateGoldPaperVersionRequest; signal?: AbortSignal; }; response: GoldPaperResponse; };
  "approveGoldPaperVersion": { args: { path: { "goldPaperId": string; "version": number; }; signal?: AbortSignal; }; response: GoldPaperResponse; };
  "retireGoldPaper": { args: { path: { "goldPaperId": string; }; body: RetireGoldPaperRequest; signal?: AbortSignal; }; response: GoldPaperResponse; };
  "getCalibrationPolicy": { args: { path: { "examId": string; "questionId": string; }; signal?: AbortSignal; }; response: CalibrationPolicyResponse; };
  "putCalibrationPolicy": { args: { path: { "examId": string; "questionId": string; }; body: PutCalibrationPolicyRequest; signal?: AbortSignal; }; response: CalibrationPolicyResponse; };
  "createCalibrationSession": { args: { path: { "examId": string; "questionId": string; }; body: CreateCalibrationSessionRequest; signal?: AbortSignal; }; response: CalibrationSessionResponse; };
  "getCalibrationSession": { args: { path: { "calibrationSessionId": string; }; signal?: AbortSignal; }; response: CalibrationSessionResponse; };
  "submitCalibrationAttempt": { args: { path: { "calibrationSessionId": string; }; body: SubmitCalibrationAttemptRequest; signal?: AbortSignal; }; response: SubmitCalibrationAttemptResponse; };
  "getGraderQualification": { args: { path: { "examId": string; "questionId": string; }; query?: { "grader_id"?: string; }; signal?: AbortSignal; }; response: GraderQualificationResponse; };
  "buildAnswerGroups": { args: { path: { "examId": string; "questionId": string; }; body: BuildAnswerGroupsRequest; signal?: AbortSignal; }; response: BuildAnswerGroupsResponse; };
  "listAnswerGroups": { args: { path: { "examId": string; "questionId": string; }; signal?: AbortSignal; }; response: AnswerGroupListResponse; };
  "getAnswerGroupMetrics": { args: { path: { "examId": string; "questionId": string; }; signal?: AbortSignal; }; response: AnswerGroupMetricsResponse; };
  "getAnswerGroup": { args: { path: { "groupId": string; }; signal?: AbortSignal; }; response: AnswerGroupResponse; };
  "reviewAnswerGroupSample": { args: { path: { "groupId": string; "segmentId": string; }; body: ReviewAnswerGroupSampleRequest; signal?: AbortSignal; }; response: AnswerGroupMutationResponse; };
  "putAnswerGroupDecision": { args: { path: { "groupId": string; }; body: PutAnswerGroupDecisionRequest; signal?: AbortSignal; }; response: AnswerGroupMutationResponse; };
  "confirmAnswerGroup": { args: { path: { "groupId": string; }; body: ConfirmAnswerGroupRequest; signal?: AbortSignal; }; response: ConfirmAnswerGroupResponse; };
  "rollbackAnswerGroup": { args: { path: { "groupId": string; }; body: RollbackAnswerGroupRequest; signal?: AbortSignal; }; response: RollbackAnswerGroupResponse; };
  "listAppeals": { args: { query?: { "limit"?: number; "cursor"?: string; "exam_id"?: string; "student_id"?: string; "status"?: string; }; signal?: AbortSignal; }; response: AppealPage; };
  "enqueueSubjectiveGradingBatch": { args: { path: { "batchId": string; }; signal?: AbortSignal; }; response: BatchEnqueueResponse; };
  "listAssessmentSubjectProfiles": { args: { query?: { "stage"?: EducationStage; "subject"?: SubjectCode; }; signal?: AbortSignal; }; response: SubjectProfileListResponse; };
  "listAssessmentQuestionArchetypes": { args: { signal?: AbortSignal; }; response: QuestionArchetypeListResponse; };
  "getExamQuestionAssessmentProfile": { args: { path: { "examId": string; "questionId": string; }; signal?: AbortSignal; }; response: QuestionAssessmentProfileResponse; };
  "putExamQuestionAssessmentProfile": { args: { path: { "examId": string; "questionId": string; }; body: PutQuestionAssessmentProfileRequest; signal?: AbortSignal; }; response: QuestionAssessmentProfileResponse; };
  "getExamQuestionAssessmentSnapshot": { args: { path: { "examId": string; "questionId": string; }; signal?: AbortSignal; }; response: ExamQuestionAssessmentSnapshotResponse; };
  "getExamQualityDashboard": { args: { path: { "examId": string; }; signal?: AbortSignal; }; response: QualityDashboardResponse; };
  "getAIEligibilityPolicy": { args: { query?: { "subject_code"?: string; "education_stage"?: string; "archetype_code"?: string; "risk_tier"?: string; }; signal?: AbortSignal; }; response: AIEligibilityPolicyResponse; };
  "putAIEligibilityPolicy": { args: { body: PutAIEligibilityPolicyRequest; signal?: AbortSignal; }; response: AIEligibilityPolicyResponse; };
  "getAIEligibilityDecision": { args: { path: { "runItemId": string; }; signal?: AbortSignal; }; response: AIEligibilityDecisionResponse; };
  "listGradingEvaluations": { args: { query?: { "limit"?: number; }; signal?: AbortSignal; }; response: GradingEvaluationRunListResponse; };
  "createGradingEvaluation": { args: { body: CreateGradingEvaluationRequest; signal?: AbortSignal; }; response: GradingEvaluationRunResponse; };
  "getGradingEvaluation": { args: { path: { "runId": string; }; signal?: AbortSignal; }; response: GradingEvaluationRunResponse; };
  "addGradingEvaluationObservation": { args: { path: { "runId": string; }; body: AddGradingEvaluationObservationRequest; signal?: AbortSignal; }; response: GradingEvaluationObservationResponse; };
  "completeGradingEvaluation": { args: { path: { "runId": string; }; signal?: AbortSignal; }; response: GradingEvaluationRunResponse; };
  "invalidateGradingEvaluation": { args: { path: { "runId": string; }; body: InvalidationReasonRequest; signal?: AbortSignal; }; response: GradingEvaluationRunResponse; };
  "listGradingEvaluationSliceMetrics": { args: { path: { "runId": string; }; signal?: AbortSignal; }; response: GradingEvaluationSliceMetricsResponse; };
  "listGradingEvaluationResponseDifficulty": { args: { path: { "runId": string; }; signal?: AbortSignal; }; response: GradingEvaluationResponseDifficultyResponse; };
  "getGradingEvaluationQualitySummary": { args: { path: { "runId": string; }; signal?: AbortSignal; }; response: GradingEvaluationQualitySummaryResponse; };
  "listModelCalibrations": { args: { query?: { "model_reference"?: string; "prompt_version"?: string; "rubric_version"?: string; "subject"?: string; "archetype"?: string; "slice_key"?: string; "limit"?: number; }; signal?: AbortSignal; }; response: ModelCalibrationListResponse; };
  "createModelCalibration": { args: { body: CreateModelCalibrationRequest; signal?: AbortSignal; }; response: ModelCalibrationResponse; };
  "getModelCalibration": { args: { path: { "calibrationId": string; }; signal?: AbortSignal; }; response: ModelCalibrationResponse; };
  "listModelCalibrationEvidence": { args: { path: { "calibrationId": string; }; signal?: AbortSignal; }; response: ModelCalibrationEvidenceListResponse; };
  "addModelCalibrationEvidence": { args: { path: { "calibrationId": string; }; body: AddModelCalibrationEvidenceRequest; signal?: AbortSignal; }; response: ModelCalibrationEvidenceResponse; };
  "completeModelCalibration": { args: { path: { "calibrationId": string; }; signal?: AbortSignal; }; response: ModelCalibrationResponse; };
  "approveModelCalibration": { args: { path: { "calibrationId": string; }; signal?: AbortSignal; }; response: ModelCalibrationResponse; };
  "invalidateModelCalibration": { args: { path: { "calibrationId": string; }; body: InvalidationReasonRequest; signal?: AbortSignal; }; response: ModelCalibrationResponse; };
  "recordModelScoreCandidate": { args: { body: RecordModelScoreCandidateRequest; signal?: AbortSignal; }; response: ModelScoreCandidateResponse; };
  "listAIHumanDisagreements": { args: { query?: { "exam_id"?: string; "question_id"?: string; "status"?: AIHumanDisagreementStatus; "severity"?: AIHumanDisagreementSeverity; "limit"?: number; }; signal?: AbortSignal; }; response: AIHumanDisagreementListResponse; };
  "getAIHumanDisagreementDataset": { args: { query?: { "exam_id"?: string; "question_id"?: string; "status"?: AIHumanDisagreementStatus; "severity"?: AIHumanDisagreementSeverity; "limit"?: number; }; signal?: AbortSignal; }; response: AIHumanDisagreementDatasetResponse; };
  "getAIHumanDisagreement": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: AIHumanDisagreementResponse; };
  "classifyAIHumanDisagreement": { args: { path: { "id": string; }; body: ClassifyAIHumanDisagreementRequest; signal?: AbortSignal; }; response: AIHumanDisagreementResponse; };
  "routeAIHumanDisagreement": { args: { path: { "id": string; }; body: RouteAIHumanDisagreementRequest; signal?: AbortSignal; }; response: AIHumanDisagreementResponse; };
  "listScoreReleases": { args: { path: { "examId": string; }; signal?: AbortSignal; }; response: ScoreReleaseListResponse; };
  "createScoreRelease": { args: { path: { "examId": string; }; body: CreateScoreReleaseRequest; signal?: AbortSignal; }; response: ScoreReleaseResponse; };
  "getScoreReleaseGate": { args: { path: { "examId": string; }; signal?: AbortSignal; }; response: ScoreReleaseGateResponse; };
  "getCurrentPublishedScoreRelease": { args: { path: { "examId": string; }; signal?: AbortSignal; }; response: ScoreReleaseDetail; };
  "getScoreRelease": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: ScoreReleaseDetail; };
  "getScoreReleaseDiff": { args: { path: { "id": string; }; query?: { "base"?: string; }; signal?: AbortSignal; }; response: ScoreReleaseDiffResponse; };
  "publishScoreRelease": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: ScoreReleaseResponse; };
  "createScoreReleaseRollback": { args: { path: { "examId": string; }; body: CreateScoreReleaseRollbackRequest; signal?: AbortSignal; }; response: ScoreReleaseResponse; };
  "listStudentPublishedExams": { args: { signal?: AbortSignal; }; response: StudentPublishedExamListResponse; };
  "getStudentPublishedResult": { args: { path: { "examId": string; }; signal?: AbortSignal; }; response: StudentPublishedResultResponse; };
  "getStudentPublishedQuestion": { args: { path: { "examId": string; "questionId": string; }; signal?: AbortSignal; }; response: StudentPublishedQuestionResponse; };
  "listStudentQuestionReviewAnnotations": { args: { path: { "examId": string; "questionId": string; }; signal?: AbortSignal; }; response: StudentQuestionReviewAnnotationListResponse; };
  "getStudentPublishedQuestionAnswerImage": { args: { path: { "examId": string; "questionId": string; }; signal?: AbortSignal; }; response: unknown; };
  "previewRegrade": { args: { path: { "examId": string; "questionId": string; }; body: CreateRegradePreviewRequest; signal?: AbortSignal; }; response: RegradePreviewResponse; };
  "createRegradeJob": { args: { path: { "examId": string; "questionId": string; }; body: CreateRegradeJobRequest; signal?: AbortSignal; }; response: RegradeSummaryResponse; };
  "listRegradeJobs": { args: { query?: { "exam_id"?: string; "question_id"?: string; }; signal?: AbortSignal; }; response: RegradeJobListResponse; };
  "getRegradeJob": { args: { path: { "jobId": string; }; signal?: AbortSignal; }; response: RegradeSummaryResponse; };
  "approveRegradeJob": { args: { path: { "jobId": string; }; signal?: AbortSignal; }; response: RegradeJobResponse; };
  "startRegradeJob": { args: { path: { "jobId": string; }; signal?: AbortSignal; }; response: RegradeJobResponse; };
  "pauseRegradeJob": { args: { path: { "jobId": string; }; signal?: AbortSignal; }; response: RegradeJobResponse; };
  "resumeRegradeJob": { args: { path: { "jobId": string; }; signal?: AbortSignal; }; response: RegradeJobResponse; };
  "finalizeRegradeJob": { args: { path: { "jobId": string; }; signal?: AbortSignal; }; response: RegradeJobResponse; };
  "createRegradeScoreRelease": { args: { path: { "jobId": string; }; body: CreateRegradeScoreReleaseRequest; signal?: AbortSignal; }; response: ScoreReleaseResponse; };
  "listMyRegradeItems": { args: { signal?: AbortSignal; }; response: RegradeWorkItemListResponse; };
  "claimRegradeItem": { args: { path: { "itemId": string; }; signal?: AbortSignal; }; response: RegradeWorkItemResponse; };
  "getRegradeItemContext": { args: { path: { "itemId": string; }; signal?: AbortSignal; }; response: RegradeGraderContextResponse; };
  "downloadRegradeItemSegmentImage": { args: { path: { "itemId": string; }; signal?: AbortSignal; }; response: unknown; };
  "recordRegradeCandidate": { args: { path: { "itemId": string; }; body: RecordRegradeCandidateRequest; signal?: AbortSignal; }; response: RegradeWorkItemResponse; };
  "reviewRegradeItem": { args: { path: { "itemId": string; }; body: ReviewRegradeItemRequest; signal?: AbortSignal; }; response: RegradeItemResponse; };
  "createReleaseGatePolicy": { args: { path: { "examId": string; }; body: CreateReleaseGatePolicyRequest; signal?: AbortSignal; }; response: ReleaseGatePolicyResponse; };
  "previewReleaseGate": { args: { path: { "examId": string; }; body: PreviewReleaseGateRequest; signal?: AbortSignal; }; response: ReleaseGateEvidenceResponse; };
  "getReleaseGateEvidence": { args: { path: { "evidenceId": string; }; signal?: AbortSignal; }; response: ReleaseGateEvidenceResponse; };
  "requestReleaseGateWaiver": { args: { path: { "examId": string; }; body: RequestReleaseGateWaiverRequest; signal?: AbortSignal; }; response: ReleaseGateWaiverResponse; };
  "decideReleaseGateWaiver": { args: { path: { "waiverId": string; }; body: DecideReleaseGateWaiverRequest; signal?: AbortSignal; }; response: ReleaseGateWaiverResponse; };
  "createStudentQuestionAppeal": { args: { path: { "examId": string; }; body: CreatePublishedQuestionAppealRequest; signal?: AbortSignal; }; response: StudentPublishedQuestionAppealResponse; };
  "listStudentQuestionAppeals": { args: { query?: { "exam_id"?: string; "status"?: string; }; signal?: AbortSignal; }; response: StudentPublishedQuestionAppealListResponse; };
  "listQuestionAppeals": { args: { query?: { "exam_id"?: string; "status"?: string; }; signal?: AbortSignal; }; response: PublishedQuestionAppealListResponse; };
  "getQuestionAppeal": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: PublishedQuestionAppealResponse; };
  "getQuestionAppealContext": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: PublishedQuestionAppealContextResponse; };
  "getQuestionAppealAnswerImage": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: unknown; };
  "startQuestionAppealReview": { args: { path: { "id": string; }; body: StartQuestionAppealReviewRequest; signal?: AbortSignal; }; response: PublishedQuestionAppealResponse; };
  "decideQuestionAppeal": { args: { path: { "id": string; }; body: DecideQuestionAppealRequest; signal?: AbortSignal; }; response: PublishedQuestionAppealResponse; };
  "resolveQuestionAppeal": { args: { path: { "id": string; }; body: ResolveQuestionAppealRequest; signal?: AbortSignal; }; response: PublishedQuestionAppealResponse; };
  "listQuestionAppealEvents": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: PublishedQuestionAppealEventsResponse; };
  "initCaptureUpload": { args: { body: CaptureUploadInitRequest; signal?: AbortSignal; }; response: CaptureUploadInitResponse; };
  "putCaptureUploadChunk": { args: { path: { "id": string; }; headers: { "Upload-Offset": number; "X-Chunk-SHA256": string; }; body: Blob | ArrayBuffer | Uint8Array; signal?: AbortSignal; }; response: CaptureUploadChunkResponse; };
  "completeCaptureUpload": { args: { path: { "id": string; }; body: CaptureUploadCompleteRequest; signal?: AbortSignal; }; response: CaptureUploadSession; };
  "getExamProcessingSummary": { args: { path: { "examId": string; }; signal?: AbortSignal; }; response: ProcessingSummaryResponse; };
  "listProcessingExceptions": { args: { query?: { "exam_id"?: string; "severity"?: ProcessingExceptionSeverity; "stage"?: ProcessingStage; "subject"?: string; "status"?: ProcessingExceptionStatus; "limit"?: number; "cursor"?: string; }; signal?: AbortSignal; }; response: ProcessingExceptionListResponse; };
  "retryProcessingException": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: ProcessingRetryResponse; };
  "assignProcessingException": { args: { path: { "id": string; }; body: AssignProcessingExceptionRequest; signal?: AbortSignal; }; response: ProcessingExceptionResponse; };
  "resolveProcessingException": { args: { path: { "id": string; }; body: ResolveProcessingExceptionRequest; signal?: AbortSignal; }; response: ProcessingExceptionResponse; };
  "previewBackmarkBatch": { args: { path: { "examId": string; "questionId": string; }; body: BackmarkPreviewRequest; signal?: AbortSignal; }; response: BackmarkPreviewResponse; };
  "createBackmarkBatch": { args: { path: { "examId": string; "questionId": string; }; body: CreateBackmarkBatchRequest; signal?: AbortSignal; }; response: BackmarkSummaryResponse; };
  "listBackmarkBatches": { args: { query?: { "exam_id"?: string; "question_id"?: string; "limit"?: number; "cursor"?: string; }; signal?: AbortSignal; }; response: BackmarkBatchListResponse; };
  "getBackmarkBatch": { args: { path: { "batchId": string; }; query?: { "limit"?: number; "cursor"?: string; "status"?: "pending" | "in_progress" | "diff_ready" | "arbitration_required" | "regrade_required" | "cancelled"; }; signal?: AbortSignal; }; response: BackmarkSummaryResponse; };
  "previewBackmarkBatchRegrade": { args: { path: { "batchId": string; }; body: BackmarkRegradeInput; signal?: AbortSignal; }; response: RegradePreviewResponse; };
  "createBackmarkBatchRegradeJob": { args: { path: { "batchId": string; }; body: BackmarkRegradeInput; signal?: AbortSignal; }; response: RegradeSummaryResponse; };
  "listMyBackmarkItems": { args: { query?: { "limit"?: number; "cursor"?: string; }; signal?: AbortSignal; }; response: BackmarkGraderItemListResponse; };
  "claimBackmarkItem": { args: { path: { "itemId": string; }; signal?: AbortSignal; }; response: BackmarkGraderItemResponse; };
  "getBackmarkItemContext": { args: { path: { "itemId": string; }; signal?: AbortSignal; }; response: BackmarkGraderContextResponse; };
  "getBackmarkItemSegmentImage": { args: { path: { "itemId": string; }; signal?: AbortSignal; }; response: unknown; };
  "submitBackmarkItem": { args: { path: { "itemId": string; }; body: SubmitBackmarkItemRequest; signal?: AbortSignal; }; response: SubmitBackmarkItemResponse; };
  "listGraderQualityWindows": { args: { path: { "examId": string; "questionId": string; }; query?: { "grader_id"?: string; "limit"?: number; }; signal?: AbortSignal; }; response: GraderQualityWindowListResponse; };
  "recomputeGraderDrift": { args: { path: { "examId": string; "questionId": string; }; body?: RecomputeGraderDriftRequest; signal?: AbortSignal; }; response: RecomputeGraderDriftResponse; };
  "listGradingQualityIncidents": { args: { query?: { "exam_id"?: string; "question_id"?: string; "grader_id"?: string; "status"?: "open" | "acknowledged" | "resolved"; "limit"?: number; }; signal?: AbortSignal; }; response: GradingQualityIncidentListResponse; };
  "resolveGradingQualityIncident": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: GradingQualityIncidentResponse; };
  "getMathUnderstanding": { args: { path: { "segmentId": string; }; signal?: AbortSignal; }; response: MathUnderstandingResponse; };
  "listMathUnderstandingCorrections": { args: { path: { "artifactId": string; }; signal?: AbortSignal; }; response: MathCorrectionListResponse; };
  "createMathUnderstandingCorrection": { args: { path: { "artifactId": string; }; body: CreateMathCorrectionRequest; signal?: AbortSignal; }; response: MathCorrectionResponse; };
  "exportMathUnderstandingCorrections": { args: { query: { "subject": "mathematics" | "physics" | "chemistry"; "limit"?: number; }; signal?: AbortSignal; }; response: MathTrainingExportResponse; };
  "listMathPilotGates": { args: { query?: { "subject"?: "mathematics" | "physics" | "chemistry"; "limit"?: number; }; signal?: AbortSignal; }; response: MathPilotGateListResponse; };
  "evaluateMathPilotGate": { args: { body: EvaluateMathPilotGateRequest; signal?: AbortSignal; }; response: MathPilotGateResponse; };
  "createExamSession": { args: { headers: { "Idempotency-Key": string; }; body: CreateExamSessionRequest; signal?: AbortSignal; }; response: ExamSessionResponse; };
  "recoverExamSessionCommand": { args: { path: { "commandId": string; }; signal?: AbortSignal; }; response: ExamSessionCommandResponse; };
  "listPaperImports": { args: { path: { "examId": string; }; signal?: AbortSignal; }; response: PaperImportListResponse; };
  "createPaperImport": { args: { path: { "examId": string; }; headers: { "Idempotency-Key": string; }; body: CreatePaperImportRequest; signal?: AbortSignal; }; response: PaperImportResponse; };
  "getPaperImport": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: PaperImportResponse; };
  "addPaperImportSources": { args: { path: { "id": string; }; headers: { "Idempotency-Key": string; }; body: AddPaperImportSourcesRequest; signal?: AbortSignal; }; response: PaperImportResponse; };
  "replacePaperImportSources": { args: { path: { "id": string; }; headers: { "Idempotency-Key": string; }; body: ReplacePaperImportSourcesRequest; signal?: AbortSignal; }; response: PaperImportResponse; };
  "savePaperImportReview": { args: { path: { "id": string; }; body: ReviewPaperImportRequest; signal?: AbortSignal; }; response: PaperImportResponse; };
  "applyPaperImport": { args: { path: { "id": string; }; signal?: AbortSignal; }; response: PaperImportResponse; };
  "retryPaperImportParse": { args: { path: { "id": string; }; query: { "expected_generation": number; }; signal?: AbortSignal; }; response: PaperImportResponse; };
  "cancelPaperImport": { args: { path: { "id": string; }; query: { "expected_generation": number; }; headers: { "Idempotency-Key": string; }; signal?: AbortSignal; }; response: PaperImportResponse; };
  "startScoringRun": { args: { path: { "examId": string; }; headers: { "Idempotency-Key": string; }; body: StartScoringRunRequest; signal?: AbortSignal; }; response: ScoringRunResponse; };
  "recoverScoringCommand": { args: { path: { "examId": string; "commandId": string; }; signal?: AbortSignal; }; response: ScoringCommandRecovery; };
  "createSubjectiveGradingBatch": { args: { headers: { "Idempotency-Key": string; }; body: SubjectiveBatchCreateRequest; signal?: AbortSignal; }; response: SubjectiveBatchResponse; };
  "recoverSubjectiveBatchCommand": { args: { path: { "commandId": string; }; signal?: AbortSignal; }; response: SubjectiveBatchCommandRecovery; };
  "getSubjectiveGradingBatch": { args: { path: { "batchId": string; }; signal?: AbortSignal; }; response: SubjectiveBatchResponse; };
  "recoverSubjectiveEnqueueCommand": { args: { path: { "batchId": string; }; signal?: AbortSignal; }; response: SubjectiveEnqueueCommandRecovery; };
  "submitHumanGrade": { args: { path: { "id": string; }; headers: { "Idempotency-Key": string; }; body: ReviewCommandSubmitGradeInput; signal?: AbortSignal; }; response: ReviewCommandSubmitResult; };
  "submitArbitration": { args: { path: { "id": string; }; headers: { "Idempotency-Key": string; }; body: ReviewCommandSubmitArbitrationInput; signal?: AbortSignal; }; response: ReviewCommandArbitrationSubmitResult; };
  "confirmExamGrades": { args: { path: { "examId": string; }; headers: { "Idempotency-Key": string; }; body: ScoreCommandConfirmInput; signal?: AbortSignal; }; response: { "grades": Array<ScoreCommandSubmissionGrade>; }; };
  "publishExamGrades": { args: { path: { "examId": string; }; headers: { "Idempotency-Key": string; }; body: ScoreCommandPublishInput; signal?: AbortSignal; }; response: ScoreCommandPublishResult; };
  "recoverReviewCommand": { args: { path: { "commandId": string; }; signal?: AbortSignal; }; response: BusinessCommandReceipt; };
  "recoverScoreCommand": { args: { path: { "commandId": string; }; signal?: AbortSignal; }; response: BusinessCommandReceipt; };
  "recoverReportCommand": { args: { path: { "commandId": string; }; signal?: AbortSignal; }; response: BusinessCommandReceipt; };
  "exportLearningReport": { args: { path: { "examId": string; }; headers: { "Idempotency-Key": string; }; signal?: AbortSignal; }; response: unknown; };
  "listQuestionBanks": { args: { query?: { "limit"?: number; "offset"?: number; "q"?: string; }; signal?: AbortSignal; }; response: QuestionBankPage; };
  "createQuestionBank": { args: { headers: { "Idempotency-Key": string; }; body: CreateQuestionBankRequest; signal?: AbortSignal; }; response: QuestionBankResponse; };
  "getQuestionBank": { args: { path: { "bankId": string; }; signal?: AbortSignal; }; response: QuestionBankResponse; };
  "updateQuestionBank": { args: { path: { "bankId": string; }; headers: { "Idempotency-Key": string; }; body: UpdateQuestionBankRequest; signal?: AbortSignal; }; response: QuestionBankResponse; };
  "listQuestionBankItems": { args: { path: { "bankId": string; }; query?: { "limit"?: number; "offset"?: number; "q"?: string; }; signal?: AbortSignal; }; response: QuestionBankItemPage; };
  "createQuestionBankItem": { args: { path: { "bankId": string; }; headers: { "Idempotency-Key": string; }; body: CreateQuestionBankItemRequest; signal?: AbortSignal; }; response: QuestionBankItemCreateResponse; };
  "getQuestionBankItem": { args: { path: { "itemId": string; }; signal?: AbortSignal; }; response: QuestionBankItemResponse; };
  "listQuestionBankVersions": { args: { path: { "itemId": string; }; query?: { "limit"?: number; "offset"?: number; }; signal?: AbortSignal; }; response: QuestionBankVersionPage; };
  "createQuestionBankVersion": { args: { path: { "itemId": string; }; headers: { "Idempotency-Key": string; }; body: CreateQuestionBankVersionRequest; signal?: AbortSignal; }; response: QuestionBankVersionResponse; };
  "getQuestionBankVersion": { args: { path: { "versionId": string; }; signal?: AbortSignal; }; response: QuestionBankVersionResponse; };
  "updateQuestionBankVersion": { args: { path: { "versionId": string; }; headers: { "Idempotency-Key": string; }; body: UpdateQuestionBankVersionRequest; signal?: AbortSignal; }; response: QuestionBankVersionResponse; };
  "updateQuestionBankScoring": { args: { path: { "versionId": string; }; headers: { "Idempotency-Key": string; }; body: UpdateQuestionBankScoringRequest; signal?: AbortSignal; }; response: QuestionBankVersionResponse; };
  "listQuestionBankReviews": { args: { path: { "versionId": string; }; signal?: AbortSignal; }; response: QuestionBankReviewsResponse; };
  "bindQuestionBankReviewers": { args: { path: { "bankId": string; }; headers: { "Idempotency-Key": string; }; body: QuestionBankReviewerBindingRequest; signal?: AbortSignal; }; response: QuestionBankResponse; };
  "listQuestionBankRubricTemplates": { args: { query: { "bank_id": string; "limit"?: number; "offset"?: number; "q"?: string; }; signal?: AbortSignal; }; response: QuestionBankItemPage; };
  "createQuestionBankRubricTemplate": { args: { headers: { "Idempotency-Key": string; }; body: CreateQuestionBankRubricTemplateRequest; signal?: AbortSignal; }; response: QuestionBankItemCreateResponse; };
  "materializeQuestionBankVersions": { args: { path: { "examId": string; }; headers: { "Idempotency-Key": string; }; body: MaterializeQuestionBankRequest; signal?: AbortSignal; }; response: MaterializeQuestionBankResponse; };
  "submitQuestionBankReview": { args: { path: { "versionId": string; }; headers: { "Idempotency-Key": string; }; body: QuestionBankReviewRequest; signal?: AbortSignal; }; response: QuestionBankVersionResponse; };
  "approveQuestionBankVersion": { args: { path: { "versionId": string; }; headers: { "Idempotency-Key": string; }; body: QuestionBankReviewRequest; signal?: AbortSignal; }; response: QuestionBankVersionResponse; };
  "returnQuestionBankVersionToDraft": { args: { path: { "versionId": string; }; headers: { "Idempotency-Key": string; }; body: QuestionBankReviewRequest; signal?: AbortSignal; }; response: QuestionBankVersionResponse; };
  "publishQuestionBankVersion": { args: { path: { "versionId": string; }; headers: { "Idempotency-Key": string; }; body: QuestionBankReviewRequest; signal?: AbortSignal; }; response: QuestionBankVersionResponse; };
  "getQuestionBankMetadataSchema": { args: { path: { "bankId": string; }; query?: { "version"?: number; }; signal?: AbortSignal; }; response: QuestionBankMetadataSchemaResponse; };
  "updateQuestionBankMetadataSchema": { args: { path: { "bankId": string; }; headers: { "Idempotency-Key": string; }; body: UpdateQuestionBankMetadataSchemaRequest; signal?: AbortSignal; }; response: QuestionBankMetadataSchemaResponse; };
  "validateQuestionBankMetadata": { args: { path: { "bankId": string; }; body: ValidateQuestionBankMetadataRequest; signal?: AbortSignal; }; response: QuestionBankMetadataValidationResult; };
  "getQuestionBankACL": { args: { path: { "bankId": string; }; signal?: AbortSignal; }; response: QuestionBankACLResponse; };
  "updateQuestionBankACL": { args: { path: { "bankId": string; }; headers: { "Idempotency-Key": string; }; body: UpdateQuestionBankACLRequest; signal?: AbortSignal; }; response: QuestionBankACLResponse; };
  "searchQuestionBankItems": { args: { query?: { "q"?: string; "bank_id"?: string; "subject_code"?: SubjectCode; "knowledge_point"?: string; "question_type"?: string; "archetype"?: string; "workflow_status"?: "draft" | "reviewing" | "approved" | "published"; "difficulty_band"?: string; "cognitive_level"?: string; "copyright"?: string; "intended_use"?: string; "use_policy"?: "practice_only" | "exam_allowed"; "metadata_key"?: string; "metadata_value"?: string; "statistics_available"?: boolean; "mode"?: "default" | "published" | "my_drafts" | "all"; "sort"?: "updated_desc" | "created_desc" | "item_code_asc"; "limit"?: number; "offset"?: number; }; signal?: AbortSignal; }; response: QuestionBankSearchPage; };
  "retireQuestionBankItem": { args: { path: { "itemId": string; }; headers: { "Idempotency-Key": string; }; body: RetireQuestionBankItemRequest; signal?: AbortSignal; }; response: QuestionBankItemResponse; };
  "previewQuestionBankImports": { args: { body: QuestionBankImportPreviewRequest; signal?: AbortSignal; }; response: QuestionBankImportPreviewBatch; };
  "confirmQuestionBankImportBatch": { args: { body: QuestionBankBatchImportRequest; signal?: AbortSignal; }; response: QuestionBankBatchImportResult; };
  "importQuestionBankItemFromExamQuestion": { args: { path: { "questionId": string; }; headers: { "Idempotency-Key": string; }; body: QuestionBankImportRequest; signal?: AbortSignal; }; response: QuestionBankImportResult; };
  "getOnboardingReadiness": { args: { signal?: AbortSignal; }; response: OnboardingReadiness; };
}

export class EduGradeApi {
  constructor(private readonly transport: ApiTransport) {}

  createSubjectiveAIGrade(args: operations["createSubjectiveAIGrade"]["args"]): Promise<operations["createSubjectiveAIGrade"]["response"]> {
    const requestPath = fillPath("/api/v1/answer-segments/{id}/subjective-ai-grade", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getMathRubricScore(args: operations["getMathRubricScore"]["args"]): Promise<operations["getMathRubricScore"]["response"]> {
    const requestPath = fillPath("/api/v1/math-answer-segments/{segmentId}/rubric-score", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getMathUnderstandingRuntimeInput(args: operations["getMathUnderstandingRuntimeInput"]["args"]): Promise<operations["getMathUnderstandingRuntimeInput"]["response"]> {
    const requestPath = fillPath("/api/v1/internal/math-understanding/tasks/{taskId}/input", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  completeMathUnderstandingRuntimeTask(args: operations["completeMathUnderstandingRuntimeTask"]["args"]): Promise<operations["completeMathUnderstandingRuntimeTask"]["response"]> {
    const requestPath = fillPath("/api/v1/internal/math-understanding/tasks/{taskId}/complete", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getMathVerificationRuntimeInput(args: operations["getMathVerificationRuntimeInput"]["args"]): Promise<operations["getMathVerificationRuntimeInput"]["response"]> {
    const requestPath = fillPath("/api/v1/internal/math-verification/tasks/{taskId}/input", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  completeMathVerificationRuntimeTask(args: operations["completeMathVerificationRuntimeTask"]["args"]): Promise<operations["completeMathVerificationRuntimeTask"]["response"]> {
    const requestPath = fillPath("/api/v1/internal/math-verification/tasks/{taskId}/complete", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  failMathVerificationRuntimeTask(args: operations["failMathVerificationRuntimeTask"]["args"]): Promise<operations["failMathVerificationRuntimeTask"]["response"]> {
    const requestPath = fillPath("/api/v1/internal/math-verification/tasks/{taskId}/fail", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  login(args: operations["login"]["args"]): Promise<operations["login"]["response"]> {
    const requestPath = "/api/v1/auth/login";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  createAccessToken(args: operations["createAccessToken"]["args"]): Promise<operations["createAccessToken"]["response"]> {
    const requestPath = "/api/v1/auth/token";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  lockPublicComputerSession(args: operations["lockPublicComputerSession"]["args"] = {}): Promise<operations["lockPublicComputerSession"]["response"]> {
    const requestPath = "/api/v1/auth/lock";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  reauthenticateSession(args: operations["reauthenticateSession"]["args"]): Promise<operations["reauthenticateSession"]["response"]> {
    const requestPath = "/api/v1/auth/reauthenticate";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getMFAStatus(args: operations["getMFAStatus"]["args"] = {}): Promise<operations["getMFAStatus"]["response"]> {
    const requestPath = "/api/v1/auth/mfa";
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  enrollTOTP(args: operations["enrollTOTP"]["args"]): Promise<operations["enrollTOTP"]["response"]> {
    const requestPath = "/api/v1/auth/mfa/totp/enroll";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  confirmTOTP(args: operations["confirmTOTP"]["args"]): Promise<operations["confirmTOTP"]["response"]> {
    const requestPath = "/api/v1/auth/mfa/totp/confirm";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  startMFAChallenge(args: operations["startMFAChallenge"]["args"]): Promise<operations["startMFAChallenge"]["response"]> {
    const requestPath = "/api/v1/auth/step-up/start";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  verifyMFAChallenge(args: operations["verifyMFAChallenge"]["args"]): Promise<operations["verifyMFAChallenge"]["response"]> {
    const requestPath = "/api/v1/auth/step-up/verify";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  disableTOTP(args: operations["disableTOTP"]["args"]): Promise<operations["disableTOTP"]["response"]> {
    const requestPath = "/api/v1/auth/mfa/totp/disable";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  rotateMFARecoveryCodes(args: operations["rotateMFARecoveryCodes"]["args"]): Promise<operations["rotateMFARecoveryCodes"]["response"]> {
    const requestPath = "/api/v1/auth/mfa/recovery-codes/rotate";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listPersonalSecurityEvents(args: operations["listPersonalSecurityEvents"]["args"] = {}): Promise<operations["listPersonalSecurityEvents"]["response"]> {
    const requestPath = "/api/v1/auth/security-events";
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  verifyAccountActivation(args: operations["verifyAccountActivation"]["args"]): Promise<operations["verifyAccountActivation"]["response"]> {
    const requestPath = "/api/v1/auth/activation/verify";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  completeAccountActivation(args: operations["completeAccountActivation"]["args"]): Promise<operations["completeAccountActivation"]["response"]> {
    const requestPath = "/api/v1/auth/activation/complete";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  verifyCredentialRecovery(args: operations["verifyCredentialRecovery"]["args"]): Promise<operations["verifyCredentialRecovery"]["response"]> {
    const requestPath = "/api/v1/auth/recovery/verify";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  completeCredentialRecovery(args: operations["completeCredentialRecovery"]["args"]): Promise<operations["completeCredentialRecovery"]["response"]> {
    const requestPath = "/api/v1/auth/recovery/complete";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  createManagedUserCredentialRecovery(args: operations["createManagedUserCredentialRecovery"]["args"]): Promise<operations["createManagedUserCredentialRecovery"]["response"]> {
    const requestPath = fillPath("/api/v1/users/{id}/credential-reset", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  reissueManagedUserActivation(args: operations["reissueManagedUserActivation"]["args"]): Promise<operations["reissueManagedUserActivation"]["response"]> {
    const requestPath = fillPath("/api/v1/users/{id}/activation", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  updateManagedUserStatus(args: operations["updateManagedUserStatus"]["args"]): Promise<operations["updateManagedUserStatus"]["response"]> {
    const requestPath = fillPath("/api/v1/users/{id}/status", args.path);
    return this.transport.request(requestPath, { method: "PATCH", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listSubmissionPageQualityRuns(args: operations["listSubmissionPageQualityRuns"]["args"]): Promise<operations["listSubmissionPageQualityRuns"]["response"]> {
    const requestPath = fillPath("/api/v1/submission-pages/{id}/quality-runs", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  recoverCaptureUpload(args: operations["recoverCaptureUpload"]["args"]): Promise<operations["recoverCaptureUpload"]["response"]> {
    const requestPath = fillPath("/api/v1/capture/uploads/{id}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  recoverCaptureBatchCommand(args: operations["recoverCaptureBatchCommand"]["args"]): Promise<operations["recoverCaptureBatchCommand"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/capture-batches/commands/{commandId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createCaptureBatch(args: operations["createCaptureBatch"]["args"]): Promise<operations["createCaptureBatch"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/capture-batches", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  listExams(args: operations["listExams"]["args"] = {}): Promise<operations["listExams"]["response"]> {
    const requestPath = appendQuery("/api/v1/exams", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  refreshExamCandidates(args: operations["refreshExamCandidates"]["args"]): Promise<operations["refreshExamCandidates"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/candidates/refresh", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  getExamWorkspace(args: operations["getExamWorkspace"]["args"]): Promise<operations["getExamWorkspace"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/workspace", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listExamSubmissions(args: operations["listExamSubmissions"]["args"]): Promise<operations["listExamSubmissions"]["response"]> {
    const requestPath = appendQuery(fillPath("/api/v1/exams/{examId}/submissions", args.path), args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listReviewTasks(args: operations["listReviewTasks"]["args"] = {}): Promise<operations["listReviewTasks"]["response"]> {
    const requestPath = appendQuery("/api/v1/review-tasks", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getReviewTaskContext(args: operations["getReviewTaskContext"]["args"]): Promise<operations["getReviewTaskContext"]["response"]> {
    const requestPath = fillPath("/api/v1/review-tasks/{taskId}/context", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listReviewAnnotations(args: operations["listReviewAnnotations"]["args"]): Promise<operations["listReviewAnnotations"]["response"]> {
    const requestPath = fillPath("/api/v1/review-tasks/{taskId}/annotations", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createReviewAnnotation(args: operations["createReviewAnnotation"]["args"]): Promise<operations["createReviewAnnotation"]["response"]> {
    const requestPath = fillPath("/api/v1/review-tasks/{taskId}/annotations", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getReviewAnnotation(args: operations["getReviewAnnotation"]["args"]): Promise<operations["getReviewAnnotation"]["response"]> {
    const requestPath = fillPath("/api/v1/review/annotations/{annotationId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  updateReviewAnnotation(args: operations["updateReviewAnnotation"]["args"]): Promise<operations["updateReviewAnnotation"]["response"]> {
    const requestPath = fillPath("/api/v1/review/annotations/{annotationId}", args.path);
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  deleteReviewAnnotation(args: operations["deleteReviewAnnotation"]["args"]): Promise<operations["deleteReviewAnnotation"]["response"]> {
    const requestPath = fillPath("/api/v1/review/annotations/{annotationId}", args.path);
    return this.transport.request(requestPath, { method: "DELETE", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listReviewCommentTemplates(args: operations["listReviewCommentTemplates"]["args"] = {}): Promise<operations["listReviewCommentTemplates"]["response"]> {
    const requestPath = "/api/v1/review/comment-templates";
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createReviewCommentTemplate(args: operations["createReviewCommentTemplate"]["args"]): Promise<operations["createReviewCommentTemplate"]["response"]> {
    const requestPath = "/api/v1/review/comment-templates";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getReviewCommentTemplate(args: operations["getReviewCommentTemplate"]["args"]): Promise<operations["getReviewCommentTemplate"]["response"]> {
    const requestPath = fillPath("/api/v1/review/comment-templates/{templateId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  updateReviewCommentTemplate(args: operations["updateReviewCommentTemplate"]["args"]): Promise<operations["updateReviewCommentTemplate"]["response"]> {
    const requestPath = fillPath("/api/v1/review/comment-templates/{templateId}", args.path);
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  deleteReviewCommentTemplate(args: operations["deleteReviewCommentTemplate"]["args"]): Promise<operations["deleteReviewCommentTemplate"]["response"]> {
    const requestPath = fillPath("/api/v1/review/comment-templates/{templateId}", args.path);
    return this.transport.request(requestPath, { method: "DELETE", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  useReviewCommentTemplate(args: operations["useReviewCommentTemplate"]["args"]): Promise<operations["useReviewCommentTemplate"]["response"]> {
    const requestPath = fillPath("/api/v1/review/comment-templates/{shortcut}/use", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  nominateGoldPaper(args: operations["nominateGoldPaper"]["args"]): Promise<operations["nominateGoldPaper"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/gold-papers", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getGoldCoverage(args: operations["getGoldCoverage"]["args"]): Promise<operations["getGoldCoverage"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/gold-coverage", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listGoldPapers(args: operations["listGoldPapers"]["args"] = {}): Promise<operations["listGoldPapers"]["response"]> {
    const requestPath = appendQuery("/api/v1/gold-papers", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getGoldPaper(args: operations["getGoldPaper"]["args"]): Promise<operations["getGoldPaper"]["response"]> {
    const requestPath = fillPath("/api/v1/gold-papers/{goldPaperId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createGoldPaperVersion(args: operations["createGoldPaperVersion"]["args"]): Promise<operations["createGoldPaperVersion"]["response"]> {
    const requestPath = fillPath("/api/v1/gold-papers/{goldPaperId}/versions", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  approveGoldPaperVersion(args: operations["approveGoldPaperVersion"]["args"]): Promise<operations["approveGoldPaperVersion"]["response"]> {
    const requestPath = fillPath("/api/v1/gold-papers/{goldPaperId}/versions/{version}/approve", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  retireGoldPaper(args: operations["retireGoldPaper"]["args"]): Promise<operations["retireGoldPaper"]["response"]> {
    const requestPath = fillPath("/api/v1/gold-papers/{goldPaperId}/retire", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getCalibrationPolicy(args: operations["getCalibrationPolicy"]["args"]): Promise<operations["getCalibrationPolicy"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/calibration-policy", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  putCalibrationPolicy(args: operations["putCalibrationPolicy"]["args"]): Promise<operations["putCalibrationPolicy"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/calibration-policy", args.path);
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  createCalibrationSession(args: operations["createCalibrationSession"]["args"]): Promise<operations["createCalibrationSession"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/calibration-sessions", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getCalibrationSession(args: operations["getCalibrationSession"]["args"]): Promise<operations["getCalibrationSession"]["response"]> {
    const requestPath = fillPath("/api/v1/calibration-sessions/{calibrationSessionId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  submitCalibrationAttempt(args: operations["submitCalibrationAttempt"]["args"]): Promise<operations["submitCalibrationAttempt"]["response"]> {
    const requestPath = fillPath("/api/v1/calibration-sessions/{calibrationSessionId}/attempts", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getGraderQualification(args: operations["getGraderQualification"]["args"]): Promise<operations["getGraderQualification"]["response"]> {
    const requestPath = appendQuery(fillPath("/api/v1/exams/{examId}/questions/{questionId}/grader-qualification", args.path), args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  buildAnswerGroups(args: operations["buildAnswerGroups"]["args"]): Promise<operations["buildAnswerGroups"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/answer-groups/build", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listAnswerGroups(args: operations["listAnswerGroups"]["args"]): Promise<operations["listAnswerGroups"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/answer-groups", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getAnswerGroupMetrics(args: operations["getAnswerGroupMetrics"]["args"]): Promise<operations["getAnswerGroupMetrics"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/answer-group-metrics", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getAnswerGroup(args: operations["getAnswerGroup"]["args"]): Promise<operations["getAnswerGroup"]["response"]> {
    const requestPath = fillPath("/api/v1/answer-groups/{groupId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  reviewAnswerGroupSample(args: operations["reviewAnswerGroupSample"]["args"]): Promise<operations["reviewAnswerGroupSample"]["response"]> {
    const requestPath = fillPath("/api/v1/answer-groups/{groupId}/samples/{segmentId}", args.path);
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  putAnswerGroupDecision(args: operations["putAnswerGroupDecision"]["args"]): Promise<operations["putAnswerGroupDecision"]["response"]> {
    const requestPath = fillPath("/api/v1/answer-groups/{groupId}/decision", args.path);
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  confirmAnswerGroup(args: operations["confirmAnswerGroup"]["args"]): Promise<operations["confirmAnswerGroup"]["response"]> {
    const requestPath = fillPath("/api/v1/answer-groups/{groupId}/confirm", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  rollbackAnswerGroup(args: operations["rollbackAnswerGroup"]["args"]): Promise<operations["rollbackAnswerGroup"]["response"]> {
    const requestPath = fillPath("/api/v1/answer-groups/{groupId}/rollback", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listAppeals(args: operations["listAppeals"]["args"] = {}): Promise<operations["listAppeals"]["response"]> {
    const requestPath = appendQuery("/api/v1/appeals", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  enqueueSubjectiveGradingBatch(args: operations["enqueueSubjectiveGradingBatch"]["args"]): Promise<operations["enqueueSubjectiveGradingBatch"]["response"]> {
    const requestPath = fillPath("/api/v1/subjective-grading-batches/{batchId}/enqueue", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  listAssessmentSubjectProfiles(args: operations["listAssessmentSubjectProfiles"]["args"] = {}): Promise<operations["listAssessmentSubjectProfiles"]["response"]> {
    const requestPath = appendQuery("/api/v1/assessment/subject-profiles", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listAssessmentQuestionArchetypes(args: operations["listAssessmentQuestionArchetypes"]["args"] = {}): Promise<operations["listAssessmentQuestionArchetypes"]["response"]> {
    const requestPath = "/api/v1/assessment/question-archetypes";
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getExamQuestionAssessmentProfile(args: operations["getExamQuestionAssessmentProfile"]["args"]): Promise<operations["getExamQuestionAssessmentProfile"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/assessment-profile", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  putExamQuestionAssessmentProfile(args: operations["putExamQuestionAssessmentProfile"]["args"]): Promise<operations["putExamQuestionAssessmentProfile"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/assessment-profile", args.path);
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getExamQuestionAssessmentSnapshot(args: operations["getExamQuestionAssessmentSnapshot"]["args"]): Promise<operations["getExamQuestionAssessmentSnapshot"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/assessment-snapshot", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getExamQualityDashboard(args: operations["getExamQualityDashboard"]["args"]): Promise<operations["getExamQualityDashboard"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/quality-dashboard", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getAIEligibilityPolicy(args: operations["getAIEligibilityPolicy"]["args"] = {}): Promise<operations["getAIEligibilityPolicy"]["response"]> {
    const requestPath = appendQuery("/api/v1/ai-eligibility/policy", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  putAIEligibilityPolicy(args: operations["putAIEligibilityPolicy"]["args"]): Promise<operations["putAIEligibilityPolicy"]["response"]> {
    const requestPath = "/api/v1/ai-eligibility/policy";
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getAIEligibilityDecision(args: operations["getAIEligibilityDecision"]["args"]): Promise<operations["getAIEligibilityDecision"]["response"]> {
    const requestPath = fillPath("/api/v1/ai-eligibility/decisions/{runItemId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listGradingEvaluations(args: operations["listGradingEvaluations"]["args"] = {}): Promise<operations["listGradingEvaluations"]["response"]> {
    const requestPath = appendQuery("/api/v1/grading-evaluations", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createGradingEvaluation(args: operations["createGradingEvaluation"]["args"]): Promise<operations["createGradingEvaluation"]["response"]> {
    const requestPath = "/api/v1/grading-evaluations";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getGradingEvaluation(args: operations["getGradingEvaluation"]["args"]): Promise<operations["getGradingEvaluation"]["response"]> {
    const requestPath = fillPath("/api/v1/grading-evaluations/{runId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  addGradingEvaluationObservation(args: operations["addGradingEvaluationObservation"]["args"]): Promise<operations["addGradingEvaluationObservation"]["response"]> {
    const requestPath = fillPath("/api/v1/grading-evaluations/{runId}/observations", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  completeGradingEvaluation(args: operations["completeGradingEvaluation"]["args"]): Promise<operations["completeGradingEvaluation"]["response"]> {
    const requestPath = fillPath("/api/v1/grading-evaluations/{runId}/complete", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  invalidateGradingEvaluation(args: operations["invalidateGradingEvaluation"]["args"]): Promise<operations["invalidateGradingEvaluation"]["response"]> {
    const requestPath = fillPath("/api/v1/grading-evaluations/{runId}/invalidate", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listGradingEvaluationSliceMetrics(args: operations["listGradingEvaluationSliceMetrics"]["args"]): Promise<operations["listGradingEvaluationSliceMetrics"]["response"]> {
    const requestPath = fillPath("/api/v1/grading-evaluations/{runId}/slice-metrics", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listGradingEvaluationResponseDifficulty(args: operations["listGradingEvaluationResponseDifficulty"]["args"]): Promise<operations["listGradingEvaluationResponseDifficulty"]["response"]> {
    const requestPath = fillPath("/api/v1/grading-evaluations/{runId}/response-difficulty", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getGradingEvaluationQualitySummary(args: operations["getGradingEvaluationQualitySummary"]["args"]): Promise<operations["getGradingEvaluationQualitySummary"]["response"]> {
    const requestPath = fillPath("/api/v1/grading-evaluations/{runId}/quality-summary", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listModelCalibrations(args: operations["listModelCalibrations"]["args"] = {}): Promise<operations["listModelCalibrations"]["response"]> {
    const requestPath = appendQuery("/api/v1/model-calibrations", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createModelCalibration(args: operations["createModelCalibration"]["args"]): Promise<operations["createModelCalibration"]["response"]> {
    const requestPath = "/api/v1/model-calibrations";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getModelCalibration(args: operations["getModelCalibration"]["args"]): Promise<operations["getModelCalibration"]["response"]> {
    const requestPath = fillPath("/api/v1/model-calibrations/{calibrationId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listModelCalibrationEvidence(args: operations["listModelCalibrationEvidence"]["args"]): Promise<operations["listModelCalibrationEvidence"]["response"]> {
    const requestPath = fillPath("/api/v1/model-calibrations/{calibrationId}/evidence", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  addModelCalibrationEvidence(args: operations["addModelCalibrationEvidence"]["args"]): Promise<operations["addModelCalibrationEvidence"]["response"]> {
    const requestPath = fillPath("/api/v1/model-calibrations/{calibrationId}/evidence", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  completeModelCalibration(args: operations["completeModelCalibration"]["args"]): Promise<operations["completeModelCalibration"]["response"]> {
    const requestPath = fillPath("/api/v1/model-calibrations/{calibrationId}/complete", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  approveModelCalibration(args: operations["approveModelCalibration"]["args"]): Promise<operations["approveModelCalibration"]["response"]> {
    const requestPath = fillPath("/api/v1/model-calibrations/{calibrationId}/approve", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  invalidateModelCalibration(args: operations["invalidateModelCalibration"]["args"]): Promise<operations["invalidateModelCalibration"]["response"]> {
    const requestPath = fillPath("/api/v1/model-calibrations/{calibrationId}/invalidate", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  recordModelScoreCandidate(args: operations["recordModelScoreCandidate"]["args"]): Promise<operations["recordModelScoreCandidate"]["response"]> {
    const requestPath = "/api/v1/model-score-candidates";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listAIHumanDisagreements(args: operations["listAIHumanDisagreements"]["args"] = {}): Promise<operations["listAIHumanDisagreements"]["response"]> {
    const requestPath = appendQuery("/api/v1/ai-human-disagreements", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getAIHumanDisagreementDataset(args: operations["getAIHumanDisagreementDataset"]["args"] = {}): Promise<operations["getAIHumanDisagreementDataset"]["response"]> {
    const requestPath = appendQuery("/api/v1/ai-human-disagreements/dataset", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getAIHumanDisagreement(args: operations["getAIHumanDisagreement"]["args"]): Promise<operations["getAIHumanDisagreement"]["response"]> {
    const requestPath = fillPath("/api/v1/ai-human-disagreements/{id}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  classifyAIHumanDisagreement(args: operations["classifyAIHumanDisagreement"]["args"]): Promise<operations["classifyAIHumanDisagreement"]["response"]> {
    const requestPath = fillPath("/api/v1/ai-human-disagreements/{id}/classification", args.path);
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  routeAIHumanDisagreement(args: operations["routeAIHumanDisagreement"]["args"]): Promise<operations["routeAIHumanDisagreement"]["response"]> {
    const requestPath = fillPath("/api/v1/ai-human-disagreements/{id}/route", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listScoreReleases(args: operations["listScoreReleases"]["args"]): Promise<operations["listScoreReleases"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/score-releases", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createScoreRelease(args: operations["createScoreRelease"]["args"]): Promise<operations["createScoreRelease"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/score-releases", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getScoreReleaseGate(args: operations["getScoreReleaseGate"]["args"]): Promise<operations["getScoreReleaseGate"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/release-gate", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getCurrentPublishedScoreRelease(args: operations["getCurrentPublishedScoreRelease"]["args"]): Promise<operations["getCurrentPublishedScoreRelease"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/score-releases/current-published", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getScoreRelease(args: operations["getScoreRelease"]["args"]): Promise<operations["getScoreRelease"]["response"]> {
    const requestPath = fillPath("/api/v1/score-releases/{id}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getScoreReleaseDiff(args: operations["getScoreReleaseDiff"]["args"]): Promise<operations["getScoreReleaseDiff"]["response"]> {
    const requestPath = appendQuery(fillPath("/api/v1/score-releases/{id}/diff", args.path), args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  publishScoreRelease(args: operations["publishScoreRelease"]["args"]): Promise<operations["publishScoreRelease"]["response"]> {
    const requestPath = fillPath("/api/v1/score-releases/{id}/publish", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  createScoreReleaseRollback(args: operations["createScoreReleaseRollback"]["args"]): Promise<operations["createScoreReleaseRollback"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/score-releases/rollback", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listStudentPublishedExams(args: operations["listStudentPublishedExams"]["args"] = {}): Promise<operations["listStudentPublishedExams"]["response"]> {
    const requestPath = "/api/v1/student/exams";
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getStudentPublishedResult(args: operations["getStudentPublishedResult"]["args"]): Promise<operations["getStudentPublishedResult"]["response"]> {
    const requestPath = fillPath("/api/v1/student/exams/{examId}/result", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getStudentPublishedQuestion(args: operations["getStudentPublishedQuestion"]["args"]): Promise<operations["getStudentPublishedQuestion"]["response"]> {
    const requestPath = fillPath("/api/v1/student/exams/{examId}/questions/{questionId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listStudentQuestionReviewAnnotations(args: operations["listStudentQuestionReviewAnnotations"]["args"]): Promise<operations["listStudentQuestionReviewAnnotations"]["response"]> {
    const requestPath = fillPath("/api/v1/student/exams/{examId}/questions/{questionId}/annotations", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getStudentPublishedQuestionAnswerImage(args: operations["getStudentPublishedQuestionAnswerImage"]["args"]): Promise<operations["getStudentPublishedQuestionAnswerImage"]["response"]> {
    const requestPath = fillPath("/api/v1/student/exams/{examId}/questions/{questionId}/answer-image", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  previewRegrade(args: operations["previewRegrade"]["args"]): Promise<operations["previewRegrade"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/regrade-preview", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  createRegradeJob(args: operations["createRegradeJob"]["args"]): Promise<operations["createRegradeJob"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/regrade-jobs", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listRegradeJobs(args: operations["listRegradeJobs"]["args"] = {}): Promise<operations["listRegradeJobs"]["response"]> {
    const requestPath = appendQuery("/api/v1/regrade-jobs", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getRegradeJob(args: operations["getRegradeJob"]["args"]): Promise<operations["getRegradeJob"]["response"]> {
    const requestPath = fillPath("/api/v1/regrade-jobs/{jobId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  approveRegradeJob(args: operations["approveRegradeJob"]["args"]): Promise<operations["approveRegradeJob"]["response"]> {
    const requestPath = fillPath("/api/v1/regrade-jobs/{jobId}/approve", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  startRegradeJob(args: operations["startRegradeJob"]["args"]): Promise<operations["startRegradeJob"]["response"]> {
    const requestPath = fillPath("/api/v1/regrade-jobs/{jobId}/start", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  pauseRegradeJob(args: operations["pauseRegradeJob"]["args"]): Promise<operations["pauseRegradeJob"]["response"]> {
    const requestPath = fillPath("/api/v1/regrade-jobs/{jobId}/pause", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  resumeRegradeJob(args: operations["resumeRegradeJob"]["args"]): Promise<operations["resumeRegradeJob"]["response"]> {
    const requestPath = fillPath("/api/v1/regrade-jobs/{jobId}/resume", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  finalizeRegradeJob(args: operations["finalizeRegradeJob"]["args"]): Promise<operations["finalizeRegradeJob"]["response"]> {
    const requestPath = fillPath("/api/v1/regrade-jobs/{jobId}/finalize", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  createRegradeScoreRelease(args: operations["createRegradeScoreRelease"]["args"]): Promise<operations["createRegradeScoreRelease"]["response"]> {
    const requestPath = fillPath("/api/v1/regrade-jobs/{jobId}/score-release", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listMyRegradeItems(args: operations["listMyRegradeItems"]["args"] = {}): Promise<operations["listMyRegradeItems"]["response"]> {
    const requestPath = "/api/v1/regrade-items/mine";
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  claimRegradeItem(args: operations["claimRegradeItem"]["args"]): Promise<operations["claimRegradeItem"]["response"]> {
    const requestPath = fillPath("/api/v1/regrade-items/{itemId}/claim", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  getRegradeItemContext(args: operations["getRegradeItemContext"]["args"]): Promise<operations["getRegradeItemContext"]["response"]> {
    const requestPath = fillPath("/api/v1/regrade-items/{itemId}/context", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  downloadRegradeItemSegmentImage(args: operations["downloadRegradeItemSegmentImage"]["args"]): Promise<operations["downloadRegradeItemSegmentImage"]["response"]> {
    const requestPath = fillPath("/api/v1/regrade-items/{itemId}/segment-image", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  recordRegradeCandidate(args: operations["recordRegradeCandidate"]["args"]): Promise<operations["recordRegradeCandidate"]["response"]> {
    const requestPath = fillPath("/api/v1/regrade-items/{itemId}/candidate", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  reviewRegradeItem(args: operations["reviewRegradeItem"]["args"]): Promise<operations["reviewRegradeItem"]["response"]> {
    const requestPath = fillPath("/api/v1/regrade-items/{itemId}/review", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  createReleaseGatePolicy(args: operations["createReleaseGatePolicy"]["args"]): Promise<operations["createReleaseGatePolicy"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/release-gate/policies", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  previewReleaseGate(args: operations["previewReleaseGate"]["args"]): Promise<operations["previewReleaseGate"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/release-gate/preview", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getReleaseGateEvidence(args: operations["getReleaseGateEvidence"]["args"]): Promise<operations["getReleaseGateEvidence"]["response"]> {
    const requestPath = fillPath("/api/v1/release-gate-evidence/{evidenceId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  requestReleaseGateWaiver(args: operations["requestReleaseGateWaiver"]["args"]): Promise<operations["requestReleaseGateWaiver"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/release-gate/waivers", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  decideReleaseGateWaiver(args: operations["decideReleaseGateWaiver"]["args"]): Promise<operations["decideReleaseGateWaiver"]["response"]> {
    const requestPath = fillPath("/api/v1/release-gate-waivers/{waiverId}/decision", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  createStudentQuestionAppeal(args: operations["createStudentQuestionAppeal"]["args"]): Promise<operations["createStudentQuestionAppeal"]["response"]> {
    const requestPath = fillPath("/api/v1/student/exams/{examId}/question-appeals", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listStudentQuestionAppeals(args: operations["listStudentQuestionAppeals"]["args"] = {}): Promise<operations["listStudentQuestionAppeals"]["response"]> {
    const requestPath = appendQuery("/api/v1/student/question-appeals", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listQuestionAppeals(args: operations["listQuestionAppeals"]["args"] = {}): Promise<operations["listQuestionAppeals"]["response"]> {
    const requestPath = appendQuery("/api/v1/question-appeals", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getQuestionAppeal(args: operations["getQuestionAppeal"]["args"]): Promise<operations["getQuestionAppeal"]["response"]> {
    const requestPath = fillPath("/api/v1/question-appeals/{id}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getQuestionAppealContext(args: operations["getQuestionAppealContext"]["args"]): Promise<operations["getQuestionAppealContext"]["response"]> {
    const requestPath = fillPath("/api/v1/question-appeals/{id}/context", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getQuestionAppealAnswerImage(args: operations["getQuestionAppealAnswerImage"]["args"]): Promise<operations["getQuestionAppealAnswerImage"]["response"]> {
    const requestPath = fillPath("/api/v1/question-appeals/{id}/answer-image", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  startQuestionAppealReview(args: operations["startQuestionAppealReview"]["args"]): Promise<operations["startQuestionAppealReview"]["response"]> {
    const requestPath = fillPath("/api/v1/question-appeals/{id}/start-review", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  decideQuestionAppeal(args: operations["decideQuestionAppeal"]["args"]): Promise<operations["decideQuestionAppeal"]["response"]> {
    const requestPath = fillPath("/api/v1/question-appeals/{id}/decide", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  resolveQuestionAppeal(args: operations["resolveQuestionAppeal"]["args"]): Promise<operations["resolveQuestionAppeal"]["response"]> {
    const requestPath = fillPath("/api/v1/question-appeals/{id}/resolve", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listQuestionAppealEvents(args: operations["listQuestionAppealEvents"]["args"]): Promise<operations["listQuestionAppealEvents"]["response"]> {
    const requestPath = fillPath("/api/v1/question-appeals/{id}/events", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  initCaptureUpload(args: operations["initCaptureUpload"]["args"]): Promise<operations["initCaptureUpload"]["response"]> {
    const requestPath = "/api/v1/capture/uploads:init";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  putCaptureUploadChunk(args: operations["putCaptureUploadChunk"]["args"]): Promise<operations["putCaptureUploadChunk"]["response"]> {
    const requestPath = fillPath("/api/v1/capture/uploads/{id}/chunks", args.path);
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body as BodyInit, headers: { "Content-Type": "application/octet-stream", ...Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) } });
  }

  completeCaptureUpload(args: operations["completeCaptureUpload"]["args"]): Promise<operations["completeCaptureUpload"]["response"]> {
    const requestPath = fillPath("/api/v1/capture/uploads/{id}/complete", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getExamProcessingSummary(args: operations["getExamProcessingSummary"]["args"]): Promise<operations["getExamProcessingSummary"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/processing/summary", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listProcessingExceptions(args: operations["listProcessingExceptions"]["args"] = {}): Promise<operations["listProcessingExceptions"]["response"]> {
    const requestPath = appendQuery("/api/v1/processing/exceptions", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  retryProcessingException(args: operations["retryProcessingException"]["args"]): Promise<operations["retryProcessingException"]["response"]> {
    const requestPath = fillPath("/api/v1/processing/exceptions/{id}/retry", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  assignProcessingException(args: operations["assignProcessingException"]["args"]): Promise<operations["assignProcessingException"]["response"]> {
    const requestPath = fillPath("/api/v1/processing/exceptions/{id}/assign", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  resolveProcessingException(args: operations["resolveProcessingException"]["args"]): Promise<operations["resolveProcessingException"]["response"]> {
    const requestPath = fillPath("/api/v1/processing/exceptions/{id}/resolve", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  previewBackmarkBatch(args: operations["previewBackmarkBatch"]["args"]): Promise<operations["previewBackmarkBatch"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/backmark-preview", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  createBackmarkBatch(args: operations["createBackmarkBatch"]["args"]): Promise<operations["createBackmarkBatch"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/backmark-batches", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listBackmarkBatches(args: operations["listBackmarkBatches"]["args"] = {}): Promise<operations["listBackmarkBatches"]["response"]> {
    const requestPath = appendQuery("/api/v1/backmark-batches", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getBackmarkBatch(args: operations["getBackmarkBatch"]["args"]): Promise<operations["getBackmarkBatch"]["response"]> {
    const requestPath = appendQuery(fillPath("/api/v1/backmark-batches/{batchId}", args.path), args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  previewBackmarkBatchRegrade(args: operations["previewBackmarkBatchRegrade"]["args"]): Promise<operations["previewBackmarkBatchRegrade"]["response"]> {
    const requestPath = fillPath("/api/v1/backmark-batches/{batchId}/regrade-preview", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  createBackmarkBatchRegradeJob(args: operations["createBackmarkBatchRegradeJob"]["args"]): Promise<operations["createBackmarkBatchRegradeJob"]["response"]> {
    const requestPath = fillPath("/api/v1/backmark-batches/{batchId}/regrade-jobs", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listMyBackmarkItems(args: operations["listMyBackmarkItems"]["args"] = {}): Promise<operations["listMyBackmarkItems"]["response"]> {
    const requestPath = appendQuery("/api/v1/backmark-items/mine", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  claimBackmarkItem(args: operations["claimBackmarkItem"]["args"]): Promise<operations["claimBackmarkItem"]["response"]> {
    const requestPath = fillPath("/api/v1/backmark-items/{itemId}/claim", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  getBackmarkItemContext(args: operations["getBackmarkItemContext"]["args"]): Promise<operations["getBackmarkItemContext"]["response"]> {
    const requestPath = fillPath("/api/v1/backmark-items/{itemId}/context", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getBackmarkItemSegmentImage(args: operations["getBackmarkItemSegmentImage"]["args"]): Promise<operations["getBackmarkItemSegmentImage"]["response"]> {
    const requestPath = fillPath("/api/v1/backmark-items/{itemId}/segment-image", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  submitBackmarkItem(args: operations["submitBackmarkItem"]["args"]): Promise<operations["submitBackmarkItem"]["response"]> {
    const requestPath = fillPath("/api/v1/backmark-items/{itemId}/submit", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listGraderQualityWindows(args: operations["listGraderQualityWindows"]["args"]): Promise<operations["listGraderQualityWindows"]["response"]> {
    const requestPath = appendQuery(fillPath("/api/v1/exams/{examId}/questions/{questionId}/grader-quality-windows", args.path), args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  recomputeGraderDrift(args: operations["recomputeGraderDrift"]["args"]): Promise<operations["recomputeGraderDrift"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/{questionId}/grader-drift/recompute", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  listGradingQualityIncidents(args: operations["listGradingQualityIncidents"]["args"] = {}): Promise<operations["listGradingQualityIncidents"]["response"]> {
    const requestPath = appendQuery("/api/v1/grading-quality-incidents", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  resolveGradingQualityIncident(args: operations["resolveGradingQualityIncident"]["args"]): Promise<operations["resolveGradingQualityIncident"]["response"]> {
    const requestPath = fillPath("/api/v1/grading-quality-incidents/{id}/resolve", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  getMathUnderstanding(args: operations["getMathUnderstanding"]["args"]): Promise<operations["getMathUnderstanding"]["response"]> {
    const requestPath = fillPath("/api/v1/math-answer-segments/{segmentId}/understanding", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listMathUnderstandingCorrections(args: operations["listMathUnderstandingCorrections"]["args"]): Promise<operations["listMathUnderstandingCorrections"]["response"]> {
    const requestPath = fillPath("/api/v1/math-understanding/{artifactId}/corrections", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createMathUnderstandingCorrection(args: operations["createMathUnderstandingCorrection"]["args"]): Promise<operations["createMathUnderstandingCorrection"]["response"]> {
    const requestPath = fillPath("/api/v1/math-understanding/{artifactId}/corrections", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  exportMathUnderstandingCorrections(args: operations["exportMathUnderstandingCorrections"]["args"]): Promise<operations["exportMathUnderstandingCorrections"]["response"]> {
    const requestPath = appendQuery("/api/v1/math-understanding/training-export", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listMathPilotGates(args: operations["listMathPilotGates"]["args"] = {}): Promise<operations["listMathPilotGates"]["response"]> {
    const requestPath = appendQuery("/api/v1/math-pilot-gates", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  evaluateMathPilotGate(args: operations["evaluateMathPilotGate"]["args"]): Promise<operations["evaluateMathPilotGate"]["response"]> {
    const requestPath = "/api/v1/math-pilot-gates/evaluate";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  createExamSession(args: operations["createExamSession"]["args"]): Promise<operations["createExamSession"]["response"]> {
    const requestPath = "/api/v1/exam-sessions";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  recoverExamSessionCommand(args: operations["recoverExamSessionCommand"]["args"]): Promise<operations["recoverExamSessionCommand"]["response"]> {
    const requestPath = fillPath("/api/v1/exam-sessions/commands/{commandId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listPaperImports(args: operations["listPaperImports"]["args"]): Promise<operations["listPaperImports"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/paper-imports", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createPaperImport(args: operations["createPaperImport"]["args"]): Promise<operations["createPaperImport"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/paper-imports", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  getPaperImport(args: operations["getPaperImport"]["args"]): Promise<operations["getPaperImport"]["response"]> {
    const requestPath = fillPath("/api/v1/paper-imports/{id}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  addPaperImportSources(args: operations["addPaperImportSources"]["args"]): Promise<operations["addPaperImportSources"]["response"]> {
    const requestPath = fillPath("/api/v1/paper-imports/{id}/sources", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  replacePaperImportSources(args: operations["replacePaperImportSources"]["args"]): Promise<operations["replacePaperImportSources"]["response"]> {
    const requestPath = fillPath("/api/v1/paper-imports/{id}/sources", args.path);
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  savePaperImportReview(args: operations["savePaperImportReview"]["args"]): Promise<operations["savePaperImportReview"]["response"]> {
    const requestPath = fillPath("/api/v1/paper-imports/{id}/review", args.path);
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  applyPaperImport(args: operations["applyPaperImport"]["args"]): Promise<operations["applyPaperImport"]["response"]> {
    const requestPath = fillPath("/api/v1/paper-imports/{id}/apply", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  retryPaperImportParse(args: operations["retryPaperImportParse"]["args"]): Promise<operations["retryPaperImportParse"]["response"]> {
    const requestPath = appendQuery(fillPath("/api/v1/paper-imports/{id}/retry-parse", args.path), args.query);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal });
  }

  cancelPaperImport(args: operations["cancelPaperImport"]["args"]): Promise<operations["cancelPaperImport"]["response"]> {
    const requestPath = appendQuery(fillPath("/api/v1/paper-imports/{id}/cancel", args.path), args.query);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  startScoringRun(args: operations["startScoringRun"]["args"]): Promise<operations["startScoringRun"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/scoring-runs", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  recoverScoringCommand(args: operations["recoverScoringCommand"]["args"]): Promise<operations["recoverScoringCommand"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/scoring-runs/commands/{commandId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createSubjectiveGradingBatch(args: operations["createSubjectiveGradingBatch"]["args"]): Promise<operations["createSubjectiveGradingBatch"]["response"]> {
    const requestPath = "/api/v1/subjective-grading-batches";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  recoverSubjectiveBatchCommand(args: operations["recoverSubjectiveBatchCommand"]["args"]): Promise<operations["recoverSubjectiveBatchCommand"]["response"]> {
    const requestPath = fillPath("/api/v1/subjective-grading-batch-commands/{commandId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  getSubjectiveGradingBatch(args: operations["getSubjectiveGradingBatch"]["args"]): Promise<operations["getSubjectiveGradingBatch"]["response"]> {
    const requestPath = fillPath("/api/v1/subjective-grading-batches/{batchId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  recoverSubjectiveEnqueueCommand(args: operations["recoverSubjectiveEnqueueCommand"]["args"]): Promise<operations["recoverSubjectiveEnqueueCommand"]["response"]> {
    const requestPath = fillPath("/api/v1/subjective-grading-batches/{batchId}/enqueue-command", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  submitHumanGrade(args: operations["submitHumanGrade"]["args"]): Promise<operations["submitHumanGrade"]["response"]> {
    const requestPath = fillPath("/api/v1/review-tasks/{id}/submit", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  submitArbitration(args: operations["submitArbitration"]["args"]): Promise<operations["submitArbitration"]["response"]> {
    const requestPath = fillPath("/api/v1/arbitration-tasks/{id}/submit", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  confirmExamGrades(args: operations["confirmExamGrades"]["args"]): Promise<operations["confirmExamGrades"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/confirm-grades", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  publishExamGrades(args: operations["publishExamGrades"]["args"]): Promise<operations["publishExamGrades"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/publish", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  recoverReviewCommand(args: operations["recoverReviewCommand"]["args"]): Promise<operations["recoverReviewCommand"]["response"]> {
    const requestPath = fillPath("/api/v1/review-commands/{commandId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  recoverScoreCommand(args: operations["recoverScoreCommand"]["args"]): Promise<operations["recoverScoreCommand"]["response"]> {
    const requestPath = fillPath("/api/v1/score-commands/{commandId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  recoverReportCommand(args: operations["recoverReportCommand"]["args"]): Promise<operations["recoverReportCommand"]["response"]> {
    const requestPath = fillPath("/api/v1/report-commands/{commandId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  exportLearningReport(args: operations["exportLearningReport"]["args"]): Promise<operations["exportLearningReport"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/reports/export", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  listQuestionBanks(args: operations["listQuestionBanks"]["args"] = {}): Promise<operations["listQuestionBanks"]["response"]> {
    const requestPath = appendQuery("/api/v1/question-banks", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createQuestionBank(args: operations["createQuestionBank"]["args"]): Promise<operations["createQuestionBank"]["response"]> {
    const requestPath = "/api/v1/question-banks";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  getQuestionBank(args: operations["getQuestionBank"]["args"]): Promise<operations["getQuestionBank"]["response"]> {
    const requestPath = fillPath("/api/v1/question-banks/{bankId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  updateQuestionBank(args: operations["updateQuestionBank"]["args"]): Promise<operations["updateQuestionBank"]["response"]> {
    const requestPath = fillPath("/api/v1/question-banks/{bankId}", args.path);
    return this.transport.request(requestPath, { method: "PATCH", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  listQuestionBankItems(args: operations["listQuestionBankItems"]["args"]): Promise<operations["listQuestionBankItems"]["response"]> {
    const requestPath = appendQuery(fillPath("/api/v1/question-banks/{bankId}/items", args.path), args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createQuestionBankItem(args: operations["createQuestionBankItem"]["args"]): Promise<operations["createQuestionBankItem"]["response"]> {
    const requestPath = fillPath("/api/v1/question-banks/{bankId}/items", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  getQuestionBankItem(args: operations["getQuestionBankItem"]["args"]): Promise<operations["getQuestionBankItem"]["response"]> {
    const requestPath = fillPath("/api/v1/question-bank/items/{itemId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  listQuestionBankVersions(args: operations["listQuestionBankVersions"]["args"]): Promise<operations["listQuestionBankVersions"]["response"]> {
    const requestPath = appendQuery(fillPath("/api/v1/question-bank/items/{itemId}/versions", args.path), args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createQuestionBankVersion(args: operations["createQuestionBankVersion"]["args"]): Promise<operations["createQuestionBankVersion"]["response"]> {
    const requestPath = fillPath("/api/v1/question-bank/items/{itemId}/versions", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  getQuestionBankVersion(args: operations["getQuestionBankVersion"]["args"]): Promise<operations["getQuestionBankVersion"]["response"]> {
    const requestPath = fillPath("/api/v1/question-bank/versions/{versionId}", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  updateQuestionBankVersion(args: operations["updateQuestionBankVersion"]["args"]): Promise<operations["updateQuestionBankVersion"]["response"]> {
    const requestPath = fillPath("/api/v1/question-bank/versions/{versionId}", args.path);
    return this.transport.request(requestPath, { method: "PATCH", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  updateQuestionBankScoring(args: operations["updateQuestionBankScoring"]["args"]): Promise<operations["updateQuestionBankScoring"]["response"]> {
    const requestPath = fillPath("/api/v1/question-bank/versions/{versionId}/scoring", args.path);
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  listQuestionBankReviews(args: operations["listQuestionBankReviews"]["args"]): Promise<operations["listQuestionBankReviews"]["response"]> {
    const requestPath = fillPath("/api/v1/question-bank/versions/{versionId}/reviews", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  bindQuestionBankReviewers(args: operations["bindQuestionBankReviewers"]["args"]): Promise<operations["bindQuestionBankReviewers"]["response"]> {
    const requestPath = fillPath("/api/v1/question-banks/{bankId}/reviewers", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  listQuestionBankRubricTemplates(args: operations["listQuestionBankRubricTemplates"]["args"]): Promise<operations["listQuestionBankRubricTemplates"]["response"]> {
    const requestPath = appendQuery("/api/v1/question-bank/rubric-templates", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  createQuestionBankRubricTemplate(args: operations["createQuestionBankRubricTemplate"]["args"]): Promise<operations["createQuestionBankRubricTemplate"]["response"]> {
    const requestPath = "/api/v1/question-bank/rubric-templates";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  materializeQuestionBankVersions(args: operations["materializeQuestionBankVersions"]["args"]): Promise<operations["materializeQuestionBankVersions"]["response"]> {
    const requestPath = fillPath("/api/v1/exams/{examId}/questions/materialize-from-bank", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  submitQuestionBankReview(args: operations["submitQuestionBankReview"]["args"]): Promise<operations["submitQuestionBankReview"]["response"]> {
    const requestPath = fillPath("/api/v1/question-bank/versions/{versionId}/submit-review", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  approveQuestionBankVersion(args: operations["approveQuestionBankVersion"]["args"]): Promise<operations["approveQuestionBankVersion"]["response"]> {
    const requestPath = fillPath("/api/v1/question-bank/versions/{versionId}/approve", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  returnQuestionBankVersionToDraft(args: operations["returnQuestionBankVersionToDraft"]["args"]): Promise<operations["returnQuestionBankVersionToDraft"]["response"]> {
    const requestPath = fillPath("/api/v1/question-bank/versions/{versionId}/return-to-draft", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  publishQuestionBankVersion(args: operations["publishQuestionBankVersion"]["args"]): Promise<operations["publishQuestionBankVersion"]["response"]> {
    const requestPath = fillPath("/api/v1/question-bank/versions/{versionId}/publish", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  getQuestionBankMetadataSchema(args: operations["getQuestionBankMetadataSchema"]["args"]): Promise<operations["getQuestionBankMetadataSchema"]["response"]> {
    const requestPath = appendQuery(fillPath("/api/v1/question-banks/{bankId}/metadata-schema", args.path), args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  updateQuestionBankMetadataSchema(args: operations["updateQuestionBankMetadataSchema"]["args"]): Promise<operations["updateQuestionBankMetadataSchema"]["response"]> {
    const requestPath = fillPath("/api/v1/question-banks/{bankId}/metadata-schema", args.path);
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  validateQuestionBankMetadata(args: operations["validateQuestionBankMetadata"]["args"]): Promise<operations["validateQuestionBankMetadata"]["response"]> {
    const requestPath = fillPath("/api/v1/question-banks/{bankId}/metadata-schema/validate", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  getQuestionBankACL(args: operations["getQuestionBankACL"]["args"]): Promise<operations["getQuestionBankACL"]["response"]> {
    const requestPath = fillPath("/api/v1/question-banks/{bankId}/acl", args.path);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  updateQuestionBankACL(args: operations["updateQuestionBankACL"]["args"]): Promise<operations["updateQuestionBankACL"]["response"]> {
    const requestPath = fillPath("/api/v1/question-banks/{bankId}/acl", args.path);
    return this.transport.request(requestPath, { method: "PUT", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  searchQuestionBankItems(args: operations["searchQuestionBankItems"]["args"] = {}): Promise<operations["searchQuestionBankItems"]["response"]> {
    const requestPath = appendQuery("/api/v1/question-bank/items", args.query);
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }

  retireQuestionBankItem(args: operations["retireQuestionBankItem"]["args"]): Promise<operations["retireQuestionBankItem"]["response"]> {
    const requestPath = fillPath("/api/v1/question-bank/items/{itemId}/retire", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  previewQuestionBankImports(args: operations["previewQuestionBankImports"]["args"]): Promise<operations["previewQuestionBankImports"]["response"]> {
    const requestPath = "/api/v1/question-bank/imports/preview";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  confirmQuestionBankImportBatch(args: operations["confirmQuestionBankImportBatch"]["args"]): Promise<operations["confirmQuestionBankImportBatch"]["response"]> {
    const requestPath = "/api/v1/question-bank/imports/confirm";
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body) });
  }

  importQuestionBankItemFromExamQuestion(args: operations["importQuestionBankItemFromExamQuestion"]["args"]): Promise<operations["importQuestionBankItemFromExamQuestion"]["response"]> {
    const requestPath = fillPath("/api/v1/question-bank/items/import-from-question/{questionId}", args.path);
    return this.transport.request(requestPath, { method: "POST", signal: args.signal, body: args.body === undefined ? undefined : JSON.stringify(args.body), headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) });
  }

  getOnboardingReadiness(args: operations["getOnboardingReadiness"]["args"] = {}): Promise<operations["getOnboardingReadiness"]["response"]> {
    const requestPath = "/api/v1/onboarding/readiness";
    return this.transport.request(requestPath, { method: "GET", signal: args.signal });
  }
}
