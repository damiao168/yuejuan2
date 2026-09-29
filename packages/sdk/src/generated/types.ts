// Generated from services/api-gateway/openapi/edugrade-api.openapi.json. DO NOT EDIT.

export type RecordSegmentAnswerInput = { "answer_text"?: string; "answer_payload"?: Record<string, unknown>; "source"?: "manual_entry" | "ocr_text" | "imported_answer"; "confidence"?: number; };

export type SegmentAnswer = { "id": string; "tenant_id": string; "answer_segment_id": string; "answer_text": string; "answer_payload": Record<string, unknown>; "source": string; "confidence"?: number; "recorded_by": string; "created_at": string; };

export type ObjectiveGrade = { "id": string; "tenant_id": string; "answer_segment_id": string; "question_id": string; "question_no": string; "question_type": string; "answer_version": string; "grader_type": string; "rule_version": string; "suggested_score": number; "max_score": number; "confidence": number; "matched_points": Array<ObjectiveGradePoint>; "missing_points": Array<ObjectiveGradePoint>; "evidence": Array<ObjectiveGradeEvidence>; "risk_flags": Array<string>; "needs_human_review": boolean; "auto_pass": boolean; "mock": boolean; "raw_output": Record<string, unknown>; "created_by": string; "created_at": string; };

export type ObjectiveGradePoint = { "code": string; "label": string; "score": number; "evidence_ids"?: Array<string>; "reason"?: string; };

export type ObjectiveGradeEvidence = { "type": string; "evidence_id"?: string; "rubric_point_id"?: string; "answer_segment_id"?: string; "answer_text"?: string; "standard_answer"?: string; "rule"?: string; "location"?: string; "confidence"?: number; "bbox"?: Array<number>; };

export type ScoringRule = { "id": string; "tenant_id": string; "exam_id": string; "question_id": string; "version": number; "rule_type": string; "config": Record<string, unknown>; "status": "draft" | "published" | "retired"; "revision": number; "content_hash": string; "created_by": string; "published_by"?: string; "published_at"?: string; "created_at": string; "updated_at": string; };

export type PlatformSchoolAdministrator = { "id"?: string; "display_name"?: string; "username"?: string; "admin_count": number; "last_login_at"?: string; };

export type PlatformSchoolMemberCounts = { "accounts": number; "active_accounts": number; "administrators": number; "teachers": number; "graders": number; "students": number; "classes": number; };

export type PlatformSchoolUsageSummary = { "input_tokens": number; "output_tokens": number; "cached_input_tokens": number; "reasoning_tokens": number; "total_tokens": number; "requests": number; "arbitration_requests": number; "estimated_cost_microusd": number; "window_days": number; };

export type PlatformSchoolModelHealth = { "status": "healthy" | "warning" | "unconfigured"; "config_id"?: string; "config_status"?: string; "display_name"?: string; "provider_key"?: string; "model_name"?: string; "credential_hint"?: string; "connection_status"?: string; "connection_message"?: string; "capability_status"?: string; "capability_version"?: string; "capability_message"?: string; "latency_ms"?: number; "last_tested_at"?: string; "last_capability_tested_at"?: string; };

export type PlatformSchoolSummary = { "tenant_id": string; "school_id": string; "name": string; "code": string; "status": "active" | "disabled"; "created_at": string; "last_activity_at"?: string; "administrator": PlatformSchoolAdministrator; "members": PlatformSchoolMemberCounts; "usage": PlatformSchoolUsageSummary; "model_health": PlatformSchoolModelHealth; "exam_count": number; "attention_reasons": Array<string>; };

export type PlatformSchoolListResponse = { "schools": Array<PlatformSchoolSummary>; "summary": { "total": number; "active": number; "disabled": number; }; "next_cursor": string; "has_more": boolean; };

export type PlatformSchoolResponse = { "school": PlatformSchoolSummary; };

export type PlatformSchoolMember = { "id": string; "username": string; "display_name": string; "phone_masked"?: string; "employee_no"?: string; "status": string; "roles": Array<string>; "school_id"?: string; "created_at": string; "activated_at"?: string; "last_login_at"?: string; };

export type PlatformSchoolMembersResponse = { "members": Array<PlatformSchoolMember>; "summary": { "total": number; "active": number; "disabled": number; "admins": number; "teachers": number; "graders": number; }; };

export type PlatformSchoolUsageResponse = { "summary": PlatformSchoolUsageSummary; "trend": Array<{ "date": string; "total_tokens": number; "input_tokens": number; "output_tokens": number; "requests": number; }>; "by_feature": Array<PlatformSchoolUsageBreakdown>; "by_model": Array<PlatformSchoolUsageBreakdown>; "start_date": string; "end_date": string; };

export type PlatformSchoolUsageBreakdown = { "key": string; "label": string; "total_tokens": number; "requests": number; "share": number; "estimated_cost_microusd": number; };

export type PlatformSchoolModelHealthResponse = { "default_model": PlatformSchoolModelHealth; "roles": Array<{ "agent_role": "primary_a" | "primary_b" | "arbiter"; "model_name": string; "provider_key": string; "status": string; }>; };

export type PlatformSchoolActivityResponse = { "activities": Array<{ "id": string; "event_type": string; "severity": string; "title": string; "summary": string; "actor_name"?: string; "happened_at": string; }>; "security": { "active_admins": number; "mfa_enabled": number; "active_sessions": number; "last_login_at"?: string; }; };

export type PanelModelRoleBindingInput = { "tenant_id": string; "education_stage": "junior" | "senior"; "subject_code": "chinese" | "mathematics" | "english" | "physics" | "chemistry" | "biology" | "history" | "geography" | "ethics_politics"; "archetype_code": string; "agent_role": "primary_a" | "primary_b" | "arbiter"; "managed_model_api_config_id": string; "prompt_version": string; "strength_rank": number; "status": "active" | "disabled"; };

export type PanelModelRoleBinding = { "id": string; "tenant_id": string; "education_stage": "junior" | "senior"; "subject_code": "chinese" | "mathematics" | "english" | "physics" | "chemistry" | "biology" | "history" | "geography" | "ethics_politics"; "archetype_code": string; "agent_role": "primary_a" | "primary_b" | "arbiter"; "managed_model_api_config_id": string; "prompt_version": string; "strength_rank": number; "status": "active" | "disabled"; "created_by": string; "created_at": string; "updated_at": string; };

export type PanelModelRoleBindingList = { "bindings": Array<PanelModelRoleBinding>; };

export type PanelModelRoleBindingResponse = { "binding": PanelModelRoleBinding; };

export type ManagedChatAttachment = { "name": string; "media_type": string; "content": string; "size": number; };

export type ManagedChatMessage = { "role": "user" | "assistant"; "content": string; "reasoning_content"?: string; "attachments"?: Array<ManagedChatAttachment>; };

export type ManagedChatRequest = { "messages": Array<ManagedChatMessage>; };

export type ManagedChatModelStatus = { "available": boolean; "display_name"?: string; "model_name"?: string; "provider_key"?: string; "message": string; };

export type ManagedChatModelResponse = { "model": ManagedChatModelStatus; };

export type ManagedChatUsage = { "input_tokens": number; "cached_input_tokens": number; "output_tokens": number; "reasoning_tokens": number; "total_tokens": number; };

export type ManagedChatCompletion = { "message": ManagedChatMessage; "model_name": string; "display_name": string; "provider_key": string; "finish_reason"?: string; "usage": ManagedChatUsage; };

export type ManagedChatCompletionResponse = { "completion": ManagedChatCompletion; };

export type OnboardingNextAction = { "code": string; "path": string; };

export type OnboardingCheck = { "key": string; "state": "ready" | "action_required" | "warning" | "optional" | "unavailable"; "severity": "blocking" | "recommended" | "optional"; "title": string; "description"?: string; "action_code"?: string; "action_path"?: string; "metadata"?: Record<string, unknown>; };

export type OnboardingReadiness = { "scope": "platform" | "school"; "ready_for_use": boolean; "completed_count": number; "total_required": number; "checks": Array<OnboardingCheck>; "next_action"?: OnboardingNextAction; };

export type MFAStatusResponse = { "available": boolean; "enabled": boolean; "recovery_codes_remaining": number; };

export type TOTPEnrollmentRequest = { "password": string; };

export type TOTPEnrollmentResponse = { "enrollment_id": string; "secret": string; "qr_code_data_url": string; "expires_at": string; };

export type TOTPConfirmationRequest = { "enrollment_id": string; "code": string; };

export type MFARecoveryCodesResponse = { "status": "enabled" | "rotated"; "recovery_codes": Array<string>; };

export type MFAChallengeStartRequest = { "operation": "mfa.disable" | "mfa.recovery.rotate"; "password": string; };

export type MFAChallengeStartResponse = { "challenge_id": string; "operation": "mfa.disable" | "mfa.recovery.rotate"; "methods": Array<"totp" | "recovery_code">; "expires_at": string; };

export type MFAChallengeVerifyRequest = { "challenge_id": string; "method": "totp" | "recovery_code"; "code": string; };

export type MFAChallengeVerifyResponse = { "status": "verified"; "operation": "mfa.disable" | "mfa.recovery.rotate"; "expires_at": string; };

export type MFACommandRequest = { "challenge_id": string; };

export type MFADisableResponse = { "status": "disabled"; };

export type OrganizationScope = { "tenant_wide": boolean; "school_ids": Array<string>; "grade_ids": Array<string>; "class_ids": Array<string>; };

export type AuthUser = { "id": string; "tenant_id": string; "tenant_code": string; "username": string; "display_name": string; "status": string; "roles": Array<string>; "permissions": Array<string>; "data_scope": Record<string, unknown>; "organization_scope"?: OrganizationScope; "current_session_type"?: "standard" | "remembered_device" | "public_device" | "desktop_device" | "service"; };

export type AuthLoginRequest = { "tenant_code"?: string; "tenant_hint"?: string; "username"?: string; "identifier"?: string; "password": string; "remember_device"?: boolean; "public_device"?: boolean; "device_name"?: string; "client_type"?: "desktop" | "service"; };

export type AuthLoginResponse = { "expires_at": string; "user": AuthUser; };

export type WechatLoginStartRequest = { "tenant_code"?: string; "tenant_hint"?: string; "remember_device"?: boolean; "public_device"?: boolean; };

export type WechatLoginChallenge = { "challenge_id": string; "poll_token": string; "qr_code_data_url": string; "expires_at": string; };

export type WechatLoginPollRequest = { "challenge_id": string; "poll_token": string; };

export type WechatLoginStatus = { "status": "pending" | "failed" | "expired" | "consumed" | "authenticated"; "error_code"?: string; "expires_at": string; "user"?: AuthUser; };

export type AuthTokenResponse = { "token_type": "Bearer"; "access_token": string; "expires_at": string; "user": AuthUser; };

export type ReauthenticateRequest = { "password": string; };

export type SessionLockResponse = { "status": "locked"; "locked_at": string; };

export type ReauthenticateResponse = { "status": "reauthenticated"; "reauthenticated_at": string; };

export type OneTimeTokenRequest = { "token": string; };

export type CompleteOneTimePasswordRequest = { "token": string; "password": string; };

export type ActivationPreview = { "display_name": string; "tenant_code": string; "school_id"?: string; "phone_masked"?: string; "expires_at": string; };

export type RecoveryPreview = { "display_name": string; "tenant_code": string; "phone_masked"?: string; "expires_at": string; };

export type ActivationPreviewResponse = { "activation": ActivationPreview; };

export type RecoveryPreviewResponse = { "recovery": RecoveryPreview; };

export type ActivationCompleteResponse = { "status": "activated"; };

export type RecoveryCompleteResponse = { "status": "password_reset"; };

export type ManagedUserRecovery = { "token": string; "path": string; "expires_at": string; "display_name": string; "phone_masked"?: string; };

export type ManagedUserRecoveryResponse = { "recovery": ManagedUserRecovery; };

export type ManagedUserActivationDelivery = { "token": string; "path": string; "expires_at": string; "display_name": string; "phone_masked"?: string; };

export type ManagedUserActivationDeliveryResponse = { "activation": ManagedUserActivationDelivery; };

export type SecurityEvent = { "id": string; "event_type": "auth.login_succeeded" | "auth.login_failed" | "auth.login_rate_limited" | "auth.logout" | "auth.password_changed" | "auth.session_revoked" | "auth.sessions_revoked_all" | "auth.account_activated" | "auth.credential_recovery_completed" | "auth.reauthentication_failed" | "auth.session_reauthenticated" | "auth.session_locked" | "auth.mfa_enabled" | "auth.mfa_disabled" | "auth.mfa_recovery_used" | "auth.mfa_recovery_rotated"; "risk_level": "low" | "medium" | "high"; "device_summary": string; "occurred_at": string; };

export type SecurityEventListResponse = { "events": Array<SecurityEvent>; };

export type CaptureUploadRecoveryResponse = { "command_id": string; "status": "processing" | "succeeded" | "failed"; "upload": CaptureUploadSession; };

export type CaptureBatchCommandResponse = { "command": { "command_id": string; "status": "not_accepted" | "succeeded"; "batch"?: CaptureBatch; }; };

export type CaptureBatchResponse = { "batch": CaptureBatch; };

export type CaptureBatchCreateRequest = { "name": string; "source_type": "web_upload" | "scanner_upload" | "folder_import" | "desktop_sync"; "scanner_device"?: string; "idempotency_key": string; };

export type CaptureBatch = { "id": string; "tenant_id": string; "exam_id": string; "name": string; "operator_id": string; "scanner_device"?: string; "source_type": "web_upload" | "scanner_upload" | "folder_import" | "desktop_sync"; "status": "draft" | "uploading" | "matching" | "processing" | "needs_review" | "ready" | "completed" | "cancelled"; "revision": number; "file_count"?: number; "page_count"?: number; "submission_count"?: number; "normal_count"?: number; "review_count"?: number; "failed_count"?: number; "started_at"?: string; "completed_at"?: string; "created_at": string; };

export type FormulaCandidate = { "latex": string; "engine": string; "version": string; "confidence": number; "syntax_valid": boolean; "render_score"?: number; };

export type MathRubricEvidence = { "id": string; "rubric_criterion_key": string; "evidence_type": string; "source_artifact_ids": Array<string>; "verification_ids"?: Array<string>; "status": "supported" | "contradicted" | "uncertain" | "not_applicable" | "unsupported"; "explanation"?: string; "confidence": number; };

export type MathCriterionDecision = { "rubric_point_id": string; "status": "supported" | "contradicted" | "uncertain"; "max_score": number; "awarded_score": number | null; "evidence_ids": Array<string>; "verification_ids": Array<string>; "decision_source": "symbolic" | "rule" | "model_candidate" | "human"; "requires_human_review": boolean; "reason_code": string; };

export type MathCriterionCandidate = { "rubric_point_id": string; "status": "supported" | "contradicted" | "uncertain" | "not_applicable" | "unsupported"; "evidence_ids": Array<string>; };

export type MathRubricLegacyPoint = { "code": string; "label": string; "score": number; "evidence_ids"?: Array<string>; "reason"?: string; };

export type MathRubricScoreResponse = { "schema_version": "math-rubric-score-v1"; "scope": "teacher_suggestion_only"; "artifact_id": string; "artifact_version": number; "correction_revision": number; "verified_correction_revision": number; "exam_question_snapshot_id": string; "rubric_id": string; "rubric_version": string; "rubric_snapshot_hash": string; "max_score": number; "verified_score": number; "unresolved_score": number; "suggested_score": number | null; "score_range": { "min": number; "max": number; }; "requires_human_review": true; "criterion_decisions": Array<MathCriterionDecision>; "rubric_evidence": Array<MathRubricEvidence>; "matched_points": Array<MathRubricLegacyPoint>; "missing_points": Array<MathRubricLegacyPoint>; };

export type MathVerification = { "id": string; "step_id"?: string; "formula_id"?: string; "kind": "syntax" | "equivalence" | "substitution" | "unit" | "constraint" | "arithmetic"; "status": "verified" | "contradicted" | "uncertain" | "not_applicable"; "reason_code": string; "domain": string; "constraints"?: Array<string>; "engine": string; "engine_version": string; "ruleset_version": string; "details"?: Record<string, unknown>; "confidence": number; };

export type FailMathVerificationRuntimeRequest = { "lease_token": string; "retryable": boolean; "error_code": string; "error_detail": Record<string, unknown>; "duration_ms": number; };

export type MathVerificationFailureResponse = { "task": ProcessingWorkerTask; };

export type MathVerificationRuntimeInputResponse = { "task": { "id": string; "artifact_id": string; "artifact_version": number; "input_hash": string; "correction_revision": number; }; "contract": MathUnderstandingContract; };

export type CompleteMathVerificationRuntimeRequest = { "lease_token": string; "duration_ms": number; "artifact_id": string; "artifact_version": number; "correction_revision": number; "verifications": Array<MathVerification>; };

export type MathVerificationRuntimeResponse = { "artifact"?: MathUnderstandingArtifact; "superseded": boolean; };

export type MathUnderstandingRuntimeInputResponse = { "task": { "id": string; "answer_segment_id": string; "exam_question_snapshot_id": string; "subject_code": string; "region_kind": "text" | "formula" | "mixed" | "diagram" | "unknown"; "input_hash": string; }; "image_url": string; };

export type CompleteMathUnderstandingRuntimeRequest = { "lease_token": string; "duration_ms": number; "artifact": MathUnderstandingContract; };

export type MathUnderstandingRuntimeResponse = { "artifact": MathUnderstandingArtifact; };

export type MathFormulaArtifact = ({ "id": string; "block_id": string; "bbox": { "x": number; "y": number; "width": number; "height": number; }; "raw_latex"?: string; "canonical_latex"?: string; "recognition_engine": string; "recognition_version": string; "parser_version": string; "parse_status": "parsed" | "ambiguous" | "unsupported" | "failed"; "confidence": number; "ast"?: Record<string, unknown>; "symbols"?: Array<Record<string, unknown>>; "relations"?: Array<Record<string, unknown>>; "warnings"?: Array<string>; "candidates"?: Array<FormulaCandidate>; "selected_candidate"?: number; } & Record<string, unknown>);

export type MathSolutionStep = { "id": string; "order_hint": number; "block_ids": Array<string>; "formula_ids"?: Array<string>; "normalized_text"?: string; "bbox"?: { "x": number; "y": number; "width": number; "height": number; }; "kind"?: "setup" | "transformation" | "calculation" | "conclusion" | "explanation" | "branch"; "recognition_confidence"?: number; "structure_confidence"?: number; "confidence": number; };

export type MathSolutionEdge = { "from_step_id": string; "to_step_id": string; "kind": "next" | "derives" | "supports" | "corrects" | "branches"; };

export type MathSolutionGraph = { "id": string; "answer_segment_id": string; "builder_version": string; "formula_model_version": string; "overall_confidence": number; "requires_human_review": boolean; "steps": Array<MathSolutionStep>; "edges": Array<MathSolutionEdge>; };

export type MathUnderstandingArtifact = ({ "id": string; "subject_code": "mathematics" | "physics" | "chemistry"; "answer_segment_id": string; "exam_question_snapshot_id": string; "version": number; "input_hash"?: string; "engine_version": string; "blocks": Array<Record<string, unknown>>; "formulas": Array<MathFormulaArtifact>; "relations"?: Array<Record<string, unknown>>; "solution_graph": MathSolutionGraph; "verifications": Array<MathVerification>; "rubric_evidence": Array<Record<string, unknown>>; "created_at": string; "stage"?: "recognition" | "verified"; "parent_artifact_id"?: string; "correction_revision"?: number; "quality_summary"?: Record<string, unknown>; } & Record<string, unknown>);

export type MathUnderstandingContract = { "subject_code": "mathematics" | "physics" | "chemistry"; "answer_segment_id": string; "exam_question_snapshot_id": string; "input_hash": string; "engine_version": string; "blocks": Array<Record<string, unknown>>; "formulas": Array<MathFormulaArtifact> | null; "relations": Array<Record<string, unknown>> | null; "solution_graph": MathSolutionGraph; "verifications": Array<MathVerification> | null; "rubric_evidence": Array<Record<string, unknown>> | null; };

export type MathCorrectionOperation = { "type": "move_step" | "connect_edge" | "delete_edge" | "restore_block" | "correct_formula" | "merge_blocks" | "split_step"; "target_id": string; "payload": Record<string, unknown>; };

export type CreateMathCorrectionRequest = { "expected_artifact_version": number; "expected_correction_revision"?: number; "operations": Array<MathCorrectionOperation>; "corrected_contract": Record<string, unknown>; "reason": string; };

export type MathCorrection = ({ "id": string; "artifact_id": string; "answer_segment_id": string; "revision": number; "operations": Array<MathCorrectionOperation>; "corrected_contract": Record<string, unknown>; "reason": string; "created_at": string; } & Record<string, unknown>);

export type MathUnderstandingResponse = { "artifact": MathUnderstandingArtifact; "effective_artifact"?: MathUnderstandingContract; "correction_revision"?: number; "corrected"?: boolean; "corrections": Array<MathCorrection>; };

export type MathCorrectionListResponse = { "corrections": Array<MathCorrection>; };

export type MathCorrectionResponse = { "correction": MathCorrection; "verification_status"?: "queued" | "not_required" | "unavailable" | "failed"; "verification_task_id"?: string; };

export type MathTrainingExportResponse = { "samples": Array<Record<string, unknown>>; };

export type MathPilotMetrics = { "formula_exact_rate": number; "ast_exact_rate": number; "spatial_relation_f1": number; "solution_graph_edge_f1": number; "equivalence_precision": number; "rubric_evidence_precision": number; "unsafe_suggestion_rate": number; "risky_case_recall": number; "sample_count": number; };

export type MathPilotPolicy = { "minimum_samples": number; "minimum_formula_exact": number; "minimum_ast_exact": number; "minimum_spatial_f1": number; "minimum_graph_f1": number; "minimum_equivalence_precision": number; "minimum_rubric_precision": number; "maximum_unsafe_suggestion_rate": number; "minimum_risky_case_recall": number; };

export type MathPilotGateDecision = { "passed": boolean; "blockers": Array<string>; "scope": "teacher_suggestion_only"; };

export type MathPilotGateEvaluation = { "id": string; "tenant_id": string; "subject_code": "mathematics" | "physics" | "chemistry"; "benchmark_ref": string; "metrics": MathPilotMetrics; "policy": MathPilotPolicy; "decision": MathPilotGateDecision; "evaluated_by": string; "created_at": string; };

export type EvaluateMathPilotGateRequest = { "subject_code": "mathematics" | "physics" | "chemistry"; "benchmark_ref": string; "metrics": MathPilotMetrics; "policy": MathPilotPolicy; };

export type MathPilotGateResponse = { "evaluation": MathPilotGateEvaluation; };

export type MathPilotGateListResponse = { "evaluations": Array<MathPilotGateEvaluation>; };

export type ErrorResponse = { "request_id"?: string; "trace_id"?: string; "field_errors"?: (Record<string, never> & Record<string, Array<string>>); "conflict_revision"?: number; "error": { "code": string; "message": string; }; };

export type CursorPageMeta = { "next_cursor": string; "has_more": boolean; };

export type ExamWorkspaceStage = { "key": string; "label": string; "state": string; "action_route": string; };

export type ExamWorkspaceStageProgress = { "stage": string; "status": string; "completed"?: number; "total"?: number; "unit"?: string; "summary": string; };

export type ExamWorkspaceNotice = { "code": string; "title": string; "message": string; "severity": string; "action_label"?: string; "action_route"?: string; };

export type ExamWorkspaceCounts = { "paper_count": number; "question_count": number; "submission_count": number; "failed_submission_count": number; "quality_issue_submission_count": number; "unmatched_submission_count": number; "pending_review_count": number; "pending_arbitration_count": number; };

export type ExamWorkspaceNextAction = { "code": string; "label": string; "description": string; "route": string; "priority": string; };

export type ExamWorkspaceSubjectSummary = { "code": string; "label": string; "total_score": number; "question_count": number; "configured_question_count": number; "frozen_question_count": number; "risk_tier_source"?: string; "question_types": (Record<string, never> & Record<string, number>); };

export type ExamWorkspaceSubjectExam = { "exam_id": string; "subject": string; "total_score": number; };

export type ExamWorkspaceProjection = { "exam_id": string; "exam_name": string; "exam_session_name"?: string; "subject_exams"?: Array<ExamWorkspaceSubjectExam>; "exam_status": string; "revision": number; "stage": string; "stages": Array<ExamWorkspaceStage>; "stage_progress": Array<ExamWorkspaceStageProgress>; "blockers": Array<ExamWorkspaceNotice>; "warnings": Array<ExamWorkspaceNotice>; "counts": ExamWorkspaceCounts; "next_actions": Array<ExamWorkspaceNextAction>; "risk_tier": "R1" | "R2" | "R3" | "unknown"; "subject_summary": ExamWorkspaceSubjectSummary; "updated_at": string; };

export type ExamWorkspaceResponse = { "workspace": ExamWorkspaceProjection; };

export type CandidateRefreshResult = { "before_count": number; "after_count": number; "added_count": number; "removed_count": number; };

export type CandidateRefreshResponse = { "candidate_refresh": CandidateRefreshResult; };

export type ExamPage = (unknown) & (CursorPageMeta) & ({ "exams": Array<({ "id": string; "name": string; "status": string; } & Record<string, unknown>)>; });

export type SubmissionPage = (unknown) & (CursorPageMeta) & ({ "submissions": Array<({ "id": string; "exam_id": string; "status": string; } & Record<string, unknown>)>; });

export type ReviewTask = { "id": string; "tenant_id": string; "exam_id": string; "question_id": string; "question_no": string; "answer_segment_id": string; "submission_id": string; "anonymous_code": string; "source": string; "status": string; "priority": number; "assigned_to"?: string; "return_reason"?: string; "grade_round": string; "due_at"?: string; "revision": number; "created_by": string; "created_at": string; "updated_at": string; };

export type ReviewTaskAggregate = { "total_count": number; "completed_count": number; "remaining_count": number; "status_counts": (Record<string, never> & Record<string, number>); "reviewers": Array<ReviewerTaskAggregate>; };

export type ReviewerTaskAggregate = { "reviewer_id": string; "total_count": number; "completed_count": number; "remaining_count": number; };

export type ReviewTaskPage = (unknown) & (CursorPageMeta) & ({ "tasks": Array<ReviewTask>; "aggregate"?: ReviewTaskAggregate; });

export type ReviewQuestion = ({ "id": string; "exam_id": string; "question_no": string; "question_type": string; "score": number; "stem"?: string; "knowledge_points"?: Array<string>; } & Record<string, unknown>);

export type FrozenReviewRubric = ({ "id": string; "question_id": string; "version": string; "status": string; "max_score": number; "points": Array<Record<string, unknown>>; "deductions"?: Array<Record<string, unknown>>; "examples"?: Array<Record<string, unknown>>; } & Record<string, unknown>);

export type ReviewAnswerArtifact = { "answer_segment_id": string; "source"?: string; "raw_answer"?: string; "ocr_text"?: string; "status": string; "confidence"?: number; "segment_image_url": string; "original_image_url"?: string; };

export type ReviewAnswerCandidate = { "id": string; "answer_segment_id": string; "scoring_run_id"?: string; "exam_question_snapshot_id"?: string; "source": string; "payload": Record<string, unknown>; "display_text": string; "confidence"?: number; "decision": string; "evidence": Record<string, unknown>; "engine_version": string; "profile_version": string; "is_current": boolean; "created_at": string; };

export type ScoringEvidence = { "id": string; "tenant_id": string; "submission_id": string; "question_id": string; "exam_question_snapshot_id": string; "evidence_type": EvidenceType; "source_artifact_id": string; "rubric_criterion_key"?: string; "payload": Record<string, unknown>; "bbox"?: { "x": number; "y": number; "width": number; "height": number; }; "quality"?: number; "created_at": string; };

export type ReviewAISecondOpinion = { "available": boolean; "presentation": "explicit_second_opinion"; "score_prefill_allowed": boolean; "metadata": Record<string, unknown>; "history"?: Array<ReviewAISuggestionHistoryItem>; };

export type ReviewAISuggestionHistoryItem = ({ "id": string; "answer_segment_id": string; "math_artifact_id"?: string; "math_artifact_version"?: number; "math_correction_revision"?: number; "math_scoring_version"?: string; "rubric_version"?: string; "delivery_mode"?: string; "suggested_score"?: number; "max_score"?: number; "confidence"?: number; "status"?: string; "mock"?: boolean; "needs_human_review"?: boolean; "created_at"?: string; } & Record<string, unknown>);

export type ReviewTaskClaim = { "owner_id"?: string; "state": "unclaimed" | "assigned" | "claimed" | "expired"; "claimed_at"?: string; "expires_at"?: string; "can_renew": boolean; };

export type ReviewDraft = { "id": string; "review_task_id": string; "reviewer_id": string; "score"?: number; "rubric_selections": Array<{ "point_id": string; "score": number; }>; "comments": string; "private_note": string; "student_feedback": string; "viewer_state": Record<string, unknown>; "revision": number; "client_updated_at"?: string; "updated_at": string; };

export type ReviewSubjectToolHints = { "subject_code": SubjectCode; "archetype_code": QuestionArchetypeCode; "allowed_evidence_types": Array<EvidenceType>; "parser_policy": Record<string, unknown>; "evidence_policy": Record<string, unknown>; "response_schema": Record<string, unknown>; };

export type ReviewAutomationResult = { "source"?: string; "recognized_answer"?: string; "confidence"?: number; "decision"?: string; "standard_answer"?: unknown; "rule_type"?: string; "score"?: number; "max_score"?: number; "grade_source"?: string; };

export type ReviewTaskContext = { "task": ReviewTask; "expected_revision": number; "question_snapshot": ExamQuestionAssessmentSnapshot; "question": ReviewQuestion; "answer_artifact": ReviewAnswerArtifact; "frozen_rubric": FrozenReviewRubric; "ai_candidates": Array<ReviewAnswerCandidate>; "scoring_evidence": Array<ScoringEvidence>; "ai_second_opinion"?: ReviewAISecondOpinion; "claim": ReviewTaskClaim; "draft": ReviewDraft; "subject_tool_hints": ReviewSubjectToolHints; "automation_result"?: ReviewAutomationResult; };

export type ReviewTaskContextResponse = { "context": ReviewTaskContext; };

export type ReviewAnnotationVisibility = "private" | "student_after_publish";

export type ReviewAnnotationType = "note" | "highlight" | "rectangle" | "freehand";

export type CanonicalImageGeometry = { "coordinate_space": "canonical_image_normalized"; "x": number; "y": number; "width": number; "height": number; };

export type ReviewAnnotation = { "id": string; "tenant_id": string; "review_task_id": string; "answer_segment_id": string; "submission_page_id": string; "type": ReviewAnnotationType; "geometry": CanonicalImageGeometry; "payload": Record<string, unknown>; "content": string; "visibility": ReviewAnnotationVisibility; "revision": number; "created_by": string; "updated_by": string; "created_at": string; "updated_at": string; };

export type StudentReviewAnnotation = { "id": string; "answer_segment_id": string; "submission_page_id": string; "type": ReviewAnnotationType; "geometry": CanonicalImageGeometry; "content": string; "created_at": string; "updated_at": string; };

export type StudentQuestionReviewAnnotationListResponse = { "annotations": Array<StudentReviewAnnotation>; };

export type CreateReviewAnnotationRequest = { "type": ReviewAnnotationType; "geometry": CanonicalImageGeometry; "payload"?: Record<string, unknown>; "content": string; "visibility"?: ReviewAnnotationVisibility; };

export type UpdateReviewAnnotationRequest = { "type": ReviewAnnotationType; "geometry": CanonicalImageGeometry; "payload"?: Record<string, unknown>; "content": string; "visibility": ReviewAnnotationVisibility; "expected_revision": number; };

export type DeleteRevisionRequest = { "expected_revision": number; };

export type ReviewAnnotationResponse = { "annotation": ReviewAnnotation; };

export type ReviewAnnotationListResponse = { "annotations": Array<ReviewAnnotation>; };

export type ReviewCommentTemplate = { "id": string; "tenant_id": string; "owner_id": string; "title": string; "content": string; "shortcut": string; "usage_count": number; "revision": number; "created_at": string; "updated_at": string; };

export type CreateReviewCommentTemplateRequest = { "title": string; "content": string; "shortcut": string; };

export type UpdateReviewCommentTemplateRequest = { "title": string; "content": string; "shortcut": string; "expected_revision": number; };

export type ReviewCommentTemplateResponse = { "comment_template": ReviewCommentTemplate; };

export type ReviewCommentTemplateListResponse = { "comment_templates": Array<ReviewCommentTemplate>; };

export type AppealPage = (unknown) & (CursorPageMeta) & ({ "appeals": Array<Record<string, unknown>>; });

export type BatchEnqueueResponse = { "batch": SubjectiveGradingBatch; "tasks": Array<Record<string, unknown>>; "enqueue_result": { "requested_count": number; "accepted_count": number; "task_count": number; "failed_count": number; "partial_success": boolean; "failures": Array<{ "segment_id": string; "code": string; }>; }; };

export type EducationStage = "junior" | "senior";

export type SubjectCode = "chinese" | "mathematics" | "english" | "physics" | "chemistry" | "biology" | "history" | "geography" | "ethics_politics";

export type SubjectProfileStatus = "active" | "retired";

export type QuestionArchetypeCode = "selected_response" | "exact_text" | "numeric_expression" | "structured_steps" | "short_constructed" | "extended_response" | "diagram_graph" | "table_experiment";

export type EvidenceType = "selected_option" | "exact_text" | "text_span" | "numeric_value" | "math_expression" | "math_step" | "unit_value" | "chemical_equation" | "concept" | "relation" | "diagram_feature" | "table_cell";

export type ScoringMode = "RULE_AUTO" | "AI_ASSIST" | "AI_FAST_CONFIRM" | "HUMAN_PRIMARY" | "DUAL_HUMAN" | "MANUAL_ONLY";

export type ExamRiskTier = "R1" | "R2" | "R3";

export type SubjectProfile = { "id": string; "tenant_id": string; "code": string; "education_stage": EducationStage; "subject_code": SubjectCode; "version": number; "status": SubjectProfileStatus; "parser_policy": Record<string, unknown>; "evidence_policy": Record<string, unknown>; "scoring_default": QuestionScoringPolicy; "created_at": string; "updated_at": string; };

export type SubjectProfileListResponse = { "subject_profiles": Array<SubjectProfile>; };

export type QuestionArchetype = { "code": QuestionArchetypeCode; "response_schema": Record<string, unknown>; "evidence_types": Array<EvidenceType>; "default_scoring_mode": ScoringMode; };

export type QuestionArchetypeListResponse = { "question_archetypes": Array<QuestionArchetype>; };

export type PutQuestionAssessmentProfileRequest = ({ "subject_profile_id": string; "archetype_code": QuestionArchetypeCode; "allowed_evidence_types": Array<EvidenceType>; "risk_tier": ExamRiskTier; "scoring_policy": QuestionScoringPolicy; "expected_revision": number; }) & (unknown);

export type QuestionAssessmentProfile = { "id": string; "tenant_id": string; "exam_id": string; "question_id": string; "subject_profile_id": string; "subject_profile_code": string; "subject_profile_version": number; "education_stage": EducationStage; "subject_code": SubjectCode; "archetype_code": QuestionArchetypeCode; "allowed_evidence_types": Array<EvidenceType>; "risk_tier": ExamRiskTier; "scoring_policy": QuestionScoringPolicy; "revision": number; "created_at": string; "updated_at": string; };

export type QuestionAssessmentProfileResponse = { "assessment_profile": QuestionAssessmentProfile; };

export type ExamQuestionAssessmentSnapshot = { "id": string; "tenant_id": string; "exam_id": string; "question_id": string; "snapshot_version": number; "subject_profile_id": string; "subject_profile_code": string; "subject_profile_version": number; "education_stage": EducationStage; "subject_code": SubjectCode; "archetype_code": QuestionArchetypeCode; "allowed_evidence_types": Array<EvidenceType>; "risk_tier": ExamRiskTier; "profile_snapshot": Record<string, unknown>; "archetype_snapshot": Record<string, unknown>; "rubric_snapshot": Record<string, unknown>; "scoring_policy_snapshot": QuestionScoringPolicy; "content_hash": string; "created_at": string; "source_snapshot"?: Record<string, unknown>; };

export type ExamQuestionAssessmentSnapshotResponse = { "assessment_snapshot": ExamQuestionAssessmentSnapshot; };

export type GoldPaperStatus = "pending_approval" | "active" | "retired";

export type GoldPaperVersion = { "id": string; "version": number; "exam_question_snapshot_id": string; "reference_score": number; "max_score": number; "rubric_snapshot": Record<string, unknown>; "explanation": string; "trait_scores": Record<string, unknown>; "error_tags": Array<string>; "source_grade_ids": Array<string>; "nominated_by": string; "approved_by"?: string; "approved_at"?: string; "created_at": string; };

export type GoldPaper = { "id": string; "exam_id": string; "question_id": string; "submission_id": string; "answer_image_url"?: string; "active_version"?: number; "status": GoldPaperStatus; "subject_code": string; "archetype_code": string; "risk_tier": string; "nominated_by": string; "retired_at"?: string; "retirement_reason"?: string; "created_at": string; "updated_at": string; "versions": Array<GoldPaperVersion>; };

export type GoldPaperResponse = { "gold_paper": GoldPaper; };

export type GoldPaperListResponse = { "gold_papers": Array<GoldPaper>; };

export type CreateGoldPaperVersionRequest = { "reference_score": number; "explanation": string; "trait_scores"?: Record<string, unknown>; "error_tags"?: Array<string>; "source_grade_ids": Array<string>; };

export type NominateGoldPaperRequest = { "submission_id": string; "reference_score": number; "explanation": string; "trait_scores"?: Record<string, unknown>; "error_tags"?: Array<string>; "source_grade_ids": Array<string>; };

export type RetireGoldPaperRequest = { "reason": string; };

export type GoldScoreBandCoverage = { "band": "zero" | "middle" | "full"; "count": number; };

export type GoldCoverage = { "exam_id": string; "question_id": string; "subject_code": string; "archetype_code": string; "risk_tier": string; "active_approved_count": number; "score_bands": Array<GoldScoreBandCoverage>; "trait_patterns": Array<string>; "error_tags": Array<string>; "gaps": Array<string>; "ready": boolean; };

export type GoldCoverageResponse = { "coverage": GoldCoverage; };

export type CalibrationPolicy = { "id": string; "exam_id": string; "question_id": string; "archetype_code": string; "max_score": number; "minimum_samples": number; "maximum_mae": number; "minimum_exact_agreement": number; "minimum_within_one_agreement": number; "minimum_criterion_agreement"?: number; "maximum_severe_rate": number; "severe_error_threshold": number; "qualification_validity_days": number; "revision": number; "created_at": string; "updated_at": string; };

export type PutCalibrationPolicyRequest = { "archetype_code": string; "max_score": number; "minimum_samples": number; "maximum_mae": number; "minimum_exact_agreement": number; "minimum_within_one_agreement": number; "minimum_criterion_agreement"?: number; "maximum_severe_rate": number; "severe_error_threshold": number; "qualification_validity_days": number; "expected_revision": number; };

export type CalibrationPolicyResponse = { "policy": CalibrationPolicy; };

export type CalibrationGoldSample = { "gold_paper_id": string; "gold_version": number; "submission_id": string; "answer_image_url"?: string; "max_score": number; "rubric_snapshot": Record<string, unknown>; };

export type CalibrationMetrics = { "sample_count": number; "criterion_sample_count": number; "mae": number; "exact_agreement": number; "within_one_agreement": number; "criterion_agreement"?: number; "severe_disagreement_rate": number; };

export type CalibrationSession = { "id": string; "exam_id": string; "question_id": string; "grader_id": string; "exam_question_snapshot_id": string; "archetype_code": string; "risk_tier": string; "gold_set_hash": string; "status": "in_progress" | "passed" | "failed" | "invalidated"; "samples": Array<CalibrationGoldSample>; "submitted_count": number; "metrics"?: CalibrationMetrics; "started_at": string; "completed_at"?: string; "invalidated_at"?: string; "invalidation_reason"?: string; };

export type CreateCalibrationSessionRequest = { "grader_id"?: string; };

export type CalibrationSessionResponse = { "session": CalibrationSession; };

export type SubmitCalibrationAttemptRequest = { "gold_paper_id": string; "submitted_score": number; "rubric_selections": Record<string, unknown>; };

export type CalibrationCriterionDifference = { "criterion": string; "expected"?: unknown; "submitted"?: unknown; };

export type CalibrationAttempt = { "id": string; "session_id": string; "gold_paper_id": string; "gold_version": number; "submitted_score": number; "reference_score": number; "rubric_selections": Record<string, unknown>; "absolute_error": number; "exact_match": boolean; "within_one": boolean; "severe_disagreement": boolean; "criterion_correct": number; "criterion_count": number; "criterion_differences": Array<CalibrationCriterionDifference>; "created_at": string; };

export type GraderQualification = { "id": string; "exam_id": string; "question_id": string; "grader_id": string; "status": "qualified" | "expired" | "revoked"; "gold_set_hash": string; "calibration_session_id": string; "valid_until": string; "metrics": CalibrationMetrics; "created_at": string; "updated_at": string; };

export type GraderQualificationResponse = { "qualification": GraderQualification; };

export type SubmitCalibrationAttemptResponse = { "attempt": CalibrationAttempt; "session": CalibrationSession; "qualification"?: GraderQualification; };

export type AnswerGroupStatus = "sampling" | "ready_for_confirmation" | "confirmed" | "rolled_back";

export type AnswerGroupSampleOutcome = "accepted" | "rejected";

export type AnswerGroupMember = { "submission_id": string; "segment_id": string; "similarity": number; "outlier_score": number; "representative": boolean; "boundary": boolean; "outlier": boolean; "sample_status"?: AnswerGroupSampleOutcome; "sampled_by"?: string; "sampled_at"?: string; "representation_hash": string; };

export type AnswerGroupDecision = { "id": string; "score_candidate": Record<string, unknown>; "rubric_selection": Record<string, unknown>; "sample_size": number; "minimum_sample": number; "revision": number; "confirmed_by"?: string; "confirmed_at"?: string; "rollback_reference"?: string; "rolled_back_by"?: string; "rolled_back_at"?: string; "rollback_reason"?: string; };

export type AnswerGroup = { "id": string; "tenant_id": string; "exam_id": string; "question_id": string; "exam_question_snapshot_id": string; "algorithm_version": string; "representation_version": string; "member_count": number; "representative_submission_id": string; "homogeneity": number; "status": AnswerGroupStatus; "minimum_sample": number; "reviewed_sample_count": number; "can_confirm": boolean; "members": Array<AnswerGroupMember>; "decision"?: AnswerGroupDecision; "created_at": string; "updated_at": string; };

export type TeacherReferenceCase = { "gold_paper_id": string; "submission_id": string; "version": number; "reference_score": number; "max_score": number; "explanation": string; "trait_scores": Record<string, unknown>; "error_tags": Array<string>; };

export type AnswerAutomationCandidate = { "id": string; "group_id": string; "submission_id": string; "segment_id": string; "decision_revision": number; "kind": "group_score" | "individual_review"; "score_candidate": Record<string, unknown>; "rubric_selection": Record<string, unknown>; "status": "active" | "manual_required" | "rolled_back"; "algorithm_version": string; "rollback_reference"?: string; "created_at": string; };

export type AnswerGroupMetrics = { "group_count": number; "member_count": number; "group_homogeneity": number; "batch_override_rate": number; "human_actions_saved": number; "post_audit_error_rate"?: number; "post_audit_evidence_status": string; };

export type BuildAnswerGroupsRequest = { "algorithm_version"?: string; };

export type ReviewAnswerGroupSampleRequest = { "outcome": AnswerGroupSampleOutcome; "notes"?: string; };

export type PutAnswerGroupDecisionRequest = { "score_candidate": Record<string, unknown>; "rubric_selection": Record<string, unknown>; "expected_revision": number; };

export type ConfirmAnswerGroupRequest = { "expected_revision": number; };

export type RollbackAnswerGroupRequest = { "rollback_reference": string; "reason": string; };

export type BuildAnswerGroupsResponse = { "answer_groups": Array<AnswerGroup>; };

export type AnswerGroupListResponse = { "answer_groups": Array<AnswerGroup>; "teacher_reference_cases": Array<TeacherReferenceCase>; };

export type AnswerGroupResponse = { "answer_group": AnswerGroup; "teacher_reference_cases": Array<TeacherReferenceCase>; };

export type AnswerGroupMutationResponse = { "answer_group": AnswerGroup; };

export type AnswerGroupMetricsResponse = { "metrics": AnswerGroupMetrics; };

export type ConfirmAnswerGroupResponse = { "answer_group": AnswerGroup; "automation_candidates": Array<AnswerAutomationCandidate>; };

export type RollbackAnswerGroupResponse = { "answer_group": AnswerGroup; "automation_candidates": Array<AnswerAutomationCandidate>; };

export type QuestionScoringPolicy = { "mode": ScoringMode; "confidence_threshold"?: number; "require_evidence": boolean; "human_review_below_confidence"?: boolean; };

export type QualityRatio = { "numerator": number; "denominator": number; "sample_size": number; "rate"?: number; };

export type QualityFinding = { "code": string; "status": "ready" | "warning" | "blocked" | "insufficient_data"; "reason": string; "owner": string; "resolution": string; "question_id": string; "question_no": string; "grader_ref"?: string; "incident_ref"?: string; };

export type QualityQuestion = { "id": string; "question_no": string; "archetype_code": string; "risk_tier": string; "max_score": number; };

export type QualityQuestionDashboard = { "question": QualityQuestion; "gate": "ready" | "warning" | "blocked" | "insufficient_data"; "gold": { "active_approved": number; "ready": boolean; "gaps": Array<string>; "score_bands": Array<{ "band": string; "count": number; }>; }; "calibration": ({ "configured": boolean; "completed": number; "passed": number; "pass_rate": QualityRatio; "graders": Array<{ "grader_ref": string; "status": string; "sample_size": number; }>; } & Record<string, unknown>); "seed": ({ "policy_status": string; "sample_size": number; "exact_agreement": QualityRatio; "within_one_agreement": QualityRatio; "criterion_agreement": QualityRatio; "graders": Array<Record<string, unknown>>; } & Record<string, unknown>); "human_human_agreement": { "sample_size": number; "within_rule_agreement": QualityRatio; "open_cases": number; }; "answer_groups": ({ "group_count": number; "member_count": number; "open_sample_groups": number; } & Record<string, unknown>); "drift": ({ "open_warnings": number; "open_critical": number; "incidents": Array<Record<string, unknown>>; } & Record<string, unknown>); "backmark": ({ "open_batches": number; "pending_items": number; "completed_items": number; "correction_rate": QualityRatio; } & Record<string, unknown>); "findings": Array<QualityFinding>; };

export type QualityDashboard = { "exam_id": string; "gate": "ready" | "warning" | "blocked" | "insufficient_data"; "blocking": Array<QualityFinding>; "warnings": Array<QualityFinding>; "questions": Array<QualityQuestionDashboard>; "generated_at": string; };

export type QualityDashboardResponse = { "dashboard": QualityDashboard; };

export type AIEligibilityPolicyStatus = "active" | "disabled";

export type AIEligibilityReason = { "code": string; "message": string; "blocking": boolean; };

export type AIEligibilityEvaluationEvidence = { "approved": boolean; "sample_count": number; "severe_error_rate": number; "evaluation_ref"?: string; };

export type AIEligibilityCalibrationEvidence = { "available": boolean; "calibration_ref"?: string; };

export type AIEligibilityOutputConstraint = { "criteria_evidence_only": boolean; "allow_model_final_score": boolean; "final_score_authority": string; "max_severe_error_risk": number; };

export type AIEligibilityPolicy = { "id": string; "tenant_id": string; "subject_code": SubjectCode; "education_stage": EducationStage; "archetype_code": string; "risk_tier": ExamRiskTier; "min_ocr_quality": number; "min_parser_quality": number; "min_eval_n": number; "max_severe_error_rate": number; "allowed_modes": Array<ScoringMode>; "version": number; "status": AIEligibilityPolicyStatus; "created_at": string; };

export type PutAIEligibilityPolicyRequest = { "subject_code": SubjectCode; "education_stage": EducationStage; "archetype_code": string; "risk_tier": ExamRiskTier; "min_ocr_quality": number; "min_parser_quality": number; "min_eval_n": number; "max_severe_error_rate": number; "allowed_modes": Array<ScoringMode>; "status": AIEligibilityPolicyStatus; "expected_version": number; };

export type AIEligibilityDecision = { "id": string; "tenant_id": string; "run_item_id": string; "policy_id"?: string; "policy_version": number; "decision": ScoringMode; "external_ai_allowed": boolean; "input_snapshot": Record<string, unknown>; "reasons": Array<AIEligibilityReason>; "output_constraint": AIEligibilityOutputConstraint; "created_at": string; };

export type AIEligibilityPolicyResponse = { "policy": AIEligibilityPolicy; };

export type AIEligibilityDecisionResponse = { "decision": AIEligibilityDecision; };

export type GradingEvaluationRunStatus = "draft" | "completed" | "invalidated";

export type GradingEvaluationReferenceKind = "gold" | "human_adjudicated";

export type GradingEvaluationRun = { "id": string; "tenant_id"?: string; "key": string; "display_name": string; "model_reference": string; "prompt_version": string; "rubric_version": string; "dataset_reference": string; "dataset_sha256": string; "status": GradingEvaluationRunStatus; "observation_count": number; "completed_at"?: string | null; "invalidated_at"?: string | null; "invalidation_reason"?: string; "created_by"?: string; "created_at": string; };

export type CreateGradingEvaluationRequest = { "key": string; "display_name": string; "model_reference": string; "prompt_version": string; "rubric_version": string; "dataset_reference": string; "dataset_sha256": string; };

export type GradingEvaluationErrorSource = "none" | "image_quality" | "page_matching" | "answer_crop" | "handwriting_ocr" | "formula_recognition" | "answer_structuring" | "rubric" | "model_scoring" | "score_calculation" | "system" | "unattributed";

export type AddGradingEvaluationObservationRequest = { "response_key": string; "response_fingerprint": string; "reference_kind": GradingEvaluationReferenceKind; "subject": string; "archetype": string; "ocr_quality": string; "answer_length": string; "rubric_complexity": string; "reference_score": number; "model_score": number; "max_score": number; "page_match_correct"?: boolean; "crop_iou"?: number; "transcription_cer"?: number; "formula_exact"?: boolean; "rubric_criterion_agreement"?: number; "error_source"?: GradingEvaluationErrorSource; "needs_human_review"?: boolean; "reference_reviewer_count"?: number; "reference_adjudicated"?: boolean; };

export type GradingEvaluationObservation = { "id": string; "run_id": string; "response_key": string; "response_fingerprint": string; "reference_kind": GradingEvaluationReferenceKind; "subject": string; "archetype": string; "ocr_quality": string; "answer_length": string; "rubric_complexity": string; "reference_score": number; "model_score": number; "max_score": number; "reference_score_band": string; "page_match_correct"?: boolean; "crop_iou"?: number; "transcription_cer"?: number; "formula_exact"?: boolean; "rubric_criterion_agreement"?: number; "error_source": GradingEvaluationErrorSource; "needs_human_review": boolean; "reference_reviewer_count": number; "reference_adjudicated": boolean; "observed_at": string; };

export type GradingEvaluationMetrics = { "sample_count": number; "mae": number; "exact_rate": number; "within_one_rate": number; "severe_error_rate": number; "false_zero_rate": number; "false_full_rate": number; "qwk"?: number | null; "qwk_available": boolean; "qwk_unavailable_reason"?: string; };

export type GradingEvaluationSliceMetric = { "id": string; "run_id": string; "dimension": string; "value": string; "metrics": GradingEvaluationMetrics; "computed_at": string; };

export type GradingEvaluationResponseDifficulty = { "id": string; "run_id": string; "response_key": string; "response_fingerprint": string; "difficulty_score": number; "difficulty_band": string; "normalized_error": number; "severe_error": boolean; "ocr_quality": string; "answer_length": string; "rubric_complexity": string; "evidence_note": string; "computed_at": string; };

export type GradingEvaluationRunResponse = { "evaluation_run": GradingEvaluationRun; };

export type GradingEvaluationRunListResponse = { "evaluation_runs": Array<GradingEvaluationRun>; };

export type GradingEvaluationObservationResponse = { "observation": GradingEvaluationObservation; };

export type GradingEvaluationSliceMetricsResponse = { "slice_metrics": Array<GradingEvaluationSliceMetric>; };

export type GradingEvaluationResponseDifficultyResponse = { "response_difficulty": Array<GradingEvaluationResponseDifficulty>; };

export type GradingEvaluationCoveredRate = { "observed_count": number; "rate"?: number; };

export type GradingEvaluationCoveredMean = { "observed_count": number; "mean"?: number; };

export type GradingEvaluationErrorAttribution = { "source": GradingEvaluationErrorSource; "count": number; "rate": number; "severe_error_count": number; "human_review_count": number; "human_routing_recall": number; };

export type GradingEvaluationQualitySummary = { "sample_count": number; "page_match_accuracy": GradingEvaluationCoveredRate; "mean_crop_iou": GradingEvaluationCoveredMean; "mean_transcription_cer": GradingEvaluationCoveredMean; "formula_exact_rate": GradingEvaluationCoveredRate; "mean_rubric_criterion_agreement": GradingEvaluationCoveredMean; "human_review_rate": number; "risky_error_routing_recall": number; "error_attribution": Array<GradingEvaluationErrorAttribution>; };

export type GradingEvaluationQualitySummaryResponse = { "quality_summary": GradingEvaluationQualitySummary; };

export type InvalidationReasonRequest = { "reason": string; };

export type ModelCalibrationMethod = "auto" | "isotonic" | "logistic" | "conformal";

export type ModelCalibrationStatus = "draft" | "completed" | "approved" | "invalidated";

export type ModelCalibrationAxis = { "model_reference": string; "prompt_version": string; "rubric_version": string; "subject": string; "archetype": string; "slice_key": string; };

export type ModelCalibrationBin = { "min_raw_confidence": number; "max_raw_confidence": number; "calibrated_confidence": number; "sample_count": number; "correct_count": number; };

export type ModelCalibrationRiskCoveragePoint = { "threshold": number; "coverage": number; "sample_count": number; "empirical_risk": number; "severe_error_rate": number; };

export type ModelCalibrationMetrics = { "sample_count": number; "brier_score": number; "expected_calibration_error": number; "middle_score_sample_count": number; "middle_score_brier_score"?: number | null; };

export type ModelCalibrationArtifact = { "schema_version": number; "method": ModelCalibrationMethod; "bins": Array<ModelCalibrationBin>; "metrics": ModelCalibrationMetrics; "risk_coverage_curve": Array<ModelCalibrationRiskCoveragePoint>; };

export type ModelCalibration = { "id": string; "tenant_id"?: string; "key": string; "evaluation_run_id": string; "axis": ModelCalibrationAxis; "method": ModelCalibrationMethod; "status": ModelCalibrationStatus; "calibration_n": number; "artifact_uri"?: string; "artifact_sha256"?: string; "artifact"?: ModelCalibrationArtifact; "created_by"?: string; "created_at": string; "completed_at"?: string | null; "approved_at"?: string | null; "approved_by"?: string; "invalidated_at"?: string | null; "invalidated_by"?: string; "invalidation_reason"?: string; };

export type CreateModelCalibrationRequest = { "key": string; "evaluation_run_id": string; "axis": ModelCalibrationAxis; "method": ModelCalibrationMethod; };

export type AddModelCalibrationEvidenceRequest = { "response_key": string; "raw_confidence": number; };

export type ModelCalibrationEvidence = { "id": string; "calibration_id": string; "evaluation_run_id": string; "response_key": string; "raw_confidence": number; "correct": boolean; "severe_error": boolean; "score_band": string; "ocr_quality": string; "observed_at": string; };

export type RecordModelScoreCandidateRequest = { "candidate_key": string; "axis": ModelCalibrationAxis; "raw_confidence": number; "target_risk"?: number | null; };

export type ModelScoreCandidate = { "id": string; "tenant_id"?: string; "candidate_key": string; "axis": ModelCalibrationAxis; "raw_confidence": number; "calibrated_confidence"?: number | null; "calibration_id"?: string; "target_risk"?: number | null; "abstain_reason"?: string; "created_at": string; };

export type ModelCalibrationResponse = { "calibration": ModelCalibration; };

export type ModelCalibrationListResponse = { "calibrations": Array<ModelCalibration>; };

export type ModelCalibrationEvidenceResponse = { "evidence": ModelCalibrationEvidence; };

export type ModelCalibrationEvidenceListResponse = { "evidence": Array<ModelCalibrationEvidence>; };

export type ModelScoreCandidateResponse = { "candidate": ModelScoreCandidate; };

export type AIHumanDisagreementSeverity = "warning" | "severe";

export type AIHumanDisagreementStatus = "needs_review" | "classified" | "routed";

export type AIHumanDisagreementTaxonomy = "ai_scoring_error" | "human_scoring_error" | "ocr_error" | "parser_error" | "rubric_ambiguity" | "reference_answer_issue" | "question_issue" | "insufficient_evidence" | "acceptable_variation";

export type AIHumanDisagreementDifferenceType = "score" | "criterion" | "evidence" | "score_and_criterion" | "score_and_evidence" | "combined";

export type AIHumanEvidenceSummary = { "ai_evidence_count": number; "ai_matched_criterion_count": number; "human_criterion_count": number; };

export type AIHumanDisagreement = { "id": string; "tenant_id"?: string; "exam_id": string; "question_id": string; "submission_id": string; "answer_segment_id": string; "ai_candidate_id": string; "human_grade_id": string; "ai_candidate_score": number; "human_score": number; "max_score": number; "delta": number; "absolute_delta": number; "difference_type": AIHumanDisagreementDifferenceType; "severity": AIHumanDisagreementSeverity; "risk_tier": string; "trigger_rules": Array<string>; "evidence_summary": AIHumanEvidenceSummary; "status": AIHumanDisagreementStatus; "taxonomy"?: AIHumanDisagreementTaxonomy; "reviewer_id"?: string; "reviewed_at"?: string | null; "notes"?: string; "routed_review_task_id"?: string; "routed_by"?: string; "revision": number; "created_at": string; "updated_at": string; };

export type ClassifyAIHumanDisagreementRequest = { "taxonomy": AIHumanDisagreementTaxonomy; "notes"?: string; "expected_revision": number; };

export type RouteAIHumanDisagreementRequest = { "review_task_id": string; "expected_revision": number; };

export type AIHumanDisagreementResponse = { "disagreement": AIHumanDisagreement; "recommended_follow_ups"?: Array<string>; };

export type AIHumanDisagreementListResponse = { "disagreements": Array<AIHumanDisagreement>; };

export type AIHumanDisagreementDatasetEntry = { "lineage_ref": string; "question_id": string; "risk_tier": string; "ai_candidate_score": number; "human_score": number; "max_score": number; "delta": number; "severity": AIHumanDisagreementSeverity; "difference_type": AIHumanDisagreementDifferenceType; "taxonomy": AIHumanDisagreementTaxonomy; "trigger_rules": Array<string>; };

export type AIHumanDisagreementDatasetResponse = { "dataset": Array<AIHumanDisagreementDatasetEntry>; "anonymized": boolean; };

export type ScoreReleaseSource = "initial" | "regrade" | "appeal" | "rollback" | "migration";

export type ScoreReleaseStatus = "draft" | "published";

export type ScoreReleaseAppealWindow = { "enabled": boolean; "opens_at"?: string | null; "closes_at"?: string | null; "allowed_reason_codes"?: Array<string>; };

export type ScoreReleaseVisibilityPolicy = { "show_question_scores": boolean; "show_feedback": boolean; "show_rubric_summary": boolean; "show_cohort_statistics"?: boolean; "show_score_distribution"?: boolean; "show_percentile"?: boolean; "show_exact_rank"?: boolean; "show_question_statistics"?: boolean; "show_knowledge_analysis"?: boolean; "show_question_stem"?: boolean; "show_answers"?: boolean; "show_high_score_paper"?: boolean; };

export type ScoreReleaseGateIssue = { "code": string; "message": string; "blocking": boolean; "count": number; "action_route"?: string; };

export type ScoreReleaseGate = { "version": string; "passed": boolean; "blocking": Array<ScoreReleaseGateIssue>; "warnings": Array<ScoreReleaseGateIssue>; "counts": (Record<string, never> & Record<string, number>); "generated_at": string; };

export type ScoreReleaseStudentExplanation = { "feedback"?: string; "rubric_summary"?: Array<string>; };

export type ScoreReleaseQuestionFact = { "question_id": string; "question_no": string; "final_grade_id": string; "score": number; "max_score": number; "source_type": string; "source_id"?: string; "explanation": ScoreReleaseStudentExplanation; };

export type ScoreRelease = { "id": string; "tenant_id": string; "exam_id": string; "version": number; "source": ScoreReleaseSource; "reason": string; "status": ScoreReleaseStatus; "visibility_policy": ScoreReleaseVisibilityPolicy; "appeal_window": ScoreReleaseAppealWindow; "gate_snapshot": ScoreReleaseGate; "source_release_id"?: string; "supersedes_release_id"?: string; "created_by": string; "created_at": string; "published_by"?: string; "published_at"?: string | null; };

export type ScoreReleaseItem = { "release_id": string; "student_id"?: string; "submission_id": string; "total_score": number; "max_score": number; "status": string; "snapshot_hash": string; };

export type ScoreReleaseQuestion = { "release_id": string; "submission_id": string; "question_id": string; "question_no": string; "final_grade_id": string; "score": number; "max_score": number; "source_type": string; "source_id"?: string; "explanation": ScoreReleaseStudentExplanation; };

export type ScoreReleaseDetail = { "release": ScoreRelease; "items": Array<ScoreReleaseItem>; "questions": Array<ScoreReleaseQuestion>; };

export type ScoreReleaseDiffItem = { "submission_id": string; "old_total": number; "new_total": number; };

export type ScoreReleaseDiffQuestion = { "submission_id": string; "question_id": string; "old_score": number; "new_score": number; };

export type ScoreReleaseDiff = { "base_release_id": string; "release_id": string; "affected_count": number; "items": Array<ScoreReleaseDiffItem>; "questions": Array<ScoreReleaseDiffQuestion>; };

export type CreateScoreReleaseRequest = { "source"?: ScoreReleaseSource; "reason": string; "idempotency_key": string; "visibility_policy"?: ScoreReleaseVisibilityPolicy; "appeal_window"?: ScoreReleaseAppealWindow; };

export type CreateScoreReleaseRollbackRequest = { "source_release_id": string; "reason": string; "idempotency_key": string; };

export type ScoreReleaseResponse = { "score_release": ScoreRelease; };

export type ScoreReleaseListResponse = { "score_releases": Array<ScoreRelease>; };

export type ScoreReleaseGateResponse = { "release_gate": ScoreReleaseGate; };

export type ScoreReleaseDiffResponse = { "diff": ScoreReleaseDiff; };

export type StudentPublishedResult = { "exam_id": string; "release_id": string; "release_version": number; "total_score": number; "max_score": number; "questions": Array<StudentPublishedQuestion>; "appeal_window": StudentScoreAppealWindow; };

export type StudentPublishedQuestion = { "question_id": string; "question_no": string; "score": number; "max_score": number; "feedback"?: string; "rubric_summary"?: Array<string>; };

export type StudentScoreAppealWindow = { "open": boolean; "closes_at"?: string | null; "allowed_reason_codes"?: Array<string>; };

export type StudentPublishedResultResponse = { "result": StudentPublishedResult; };

export type StudentPublishedQuestionResponse = { "question": StudentPublishedQuestion; };

export type RegradeScoreBand = { "min"?: number | null; "max"?: number | null; };

export type RegradeSelector = { "submission_ids"?: Array<string>; "score_band"?: RegradeScoreBand; };

export type RegradeScoreBandCount = { "score": number; "count": number; };

export type RegradePotentialDelta = { "min": number; "max": number; };

export type RegradePreview = { "exam_id": string; "question_id": string; "source_release_id": string; "source_release_version": number; "current_release_id"?: string; "current_release_version"?: number; "affected_count": number; "old_score_bands": Array<RegradeScoreBandCount>; "possible_delta": RegradePotentialDelta; };

export type RegradeJob = { "id": string; "tenant_id": string; "exam_id": string; "question_id": string; "source_release_id": string; "reason_code": string; "reason_text": string; "strategy": string; "selector": RegradeSelector; "new_rubric_snapshot_id"?: string; "new_policy_version"?: string; "severity_delta": number; "status": string; "affected_count": number; "created_by": string; "approved_by"?: string; "approved_at"?: string | null; "finalized_by"?: string; "finalized_at"?: string | null; "created_at": string; "updated_at": string; };

export type RegradeItem = { "id": string; "job_id": string; "submission_id": string; "old_final_grade_id": string; "old_score": number; "max_score": number; "candidate_grade_id"?: string; "candidate_score"?: number | null; "candidate_rubric_selections"?: Array<RegradeRubricSelection>; "candidate_comment"?: string; "reviewed_grade_id"?: string; "reviewed_score"?: number | null; "delta"?: number | null; "status": string; "assigned_to"?: string; "claimed_by"?: string; "reviewed_by"?: string; "review_note"?: string; "revision": number; "created_at": string; "updated_at": string; };

export type RegradeWorkItem = { "id": string; "job_id": string; "status": string; "max_score": number; "revision": number; "created_at": string; };

export type RegradeGraderQuestion = { "id": string; "question_no": string; "question_type": string; "score": number; "stem"?: string; "knowledge_points"?: Array<string>; };

export type RegradeGraderAnswer = { "raw_answer"?: string; "ocr_text"?: string; "segment_status": string; "segment_image_url": string; };

export type RegradeGraderContext = { "item": RegradeWorkItem; "expected_revision": number; "question": RegradeGraderQuestion; "frozen_rubric": FrozenReviewRubric; "answer": RegradeGraderAnswer; };

export type RegradeGraderContextResponse = { "regrade_context": RegradeGraderContext; };

export type RegradeEvent = { "id": string; "job_id": string; "item_id"?: string; "type": string; "actor_id"?: string; "payload"?: Record<string, unknown>; "created_at": string; };

export type RegradeHistogram = { "delta": number; "count": number; };

export type RegradeSevereChange = { "submission_id": string; "old_score": number; "new_score": number; "delta": number; };

export type RegradeReleaseChange = { "submission_id": string; "old_final_grade_id": string; "source_score": number; "regraded_score": number; "max_score": number; "reviewed_grade_id"?: string; };

export type RegradeReleasePlan = { "job_id": string; "exam_id": string; "question_id": string; "source_release_id": string; "affected_count": number; "resolved_items": Array<RegradeReleaseChange>; };

export type RegradeSummary = { "job": RegradeJob; "items": Array<RegradeItem>; "events": Array<RegradeEvent>; "diff_histogram": Array<RegradeHistogram>; "severe_changes": Array<RegradeSevereChange>; "release_plan"?: RegradeReleasePlan; };

export type CreateRegradePreviewRequest = { "source_release_id": string; "selector": RegradeSelector; };

export type CreateRegradeJobRequest = { "source_release_id": string; "reason_code": string; "reason_text": string; "strategy": string; "selector": RegradeSelector; "new_rubric_snapshot_id"?: string; "new_policy_version"?: string; "severity_delta"?: number | null; "idempotency_key": string; "assignee_id"?: string; };

export type RecordRegradeCandidateRequest = { "score": number; "candidate_grade_id"?: string; "rubric_selections": Array<RegradeRubricSelection>; "comment"?: string; "expected_revision": number; "require_manual_review": boolean; };

export type RegradeRubricSelection = { "point_id": string; "score": number; };

export type ReviewRegradeItemRequest = { "decision": string; "reviewed_score"?: number | null; "reviewed_grade_id"?: string; "note"?: string; "expected_revision": number; };

export type CreateRegradeScoreReleaseRequest = { "reason": string; "idempotency_key"?: string; };

export type RegradePreviewResponse = { "preview": RegradePreview; };

export type RegradeSummaryResponse = { "regrade": RegradeSummary; };

export type RegradeJobResponse = { "regrade_job": RegradeJob; };

export type RegradeJobListResponse = { "regrade_jobs": Array<RegradeJob>; };

export type RegradeItemResponse = { "regrade_item": RegradeItem; };

export type RegradeWorkItemResponse = { "regrade_item": RegradeWorkItem; };

export type RegradeWorkItemListResponse = { "regrade_items": Array<RegradeWorkItem>; };

export type ReleaseGatePolicy = { "id": string; "tenant_id": string; "exam_id": string; "version": string; "status": string; "require_warning_acknowledgement": boolean; "waivable_warning_codes": Array<string>; "created_by": string; "created_at": string; };

export type ReleaseGateEvaluation = { "policy": ReleaseGatePolicy; "base_gate": ScoreReleaseGate; "passed": boolean; "pending_warning_codes": Array<string>; "applied_waiver_ids": Array<string>; "generated_at": string; };

export type ReleaseGateEvidence = { "id": string; "tenant_id": string; "exam_id": string; "release_id"?: string; "phase": "preview" | "publish"; "evaluation": ReleaseGateEvaluation; "hash": string; "created_by": string; "created_at": string; };

export type ReleaseGateWaiver = { "id": string; "tenant_id": string; "exam_id": string; "policy_id"?: string; "evidence_id": string; "issue_code": string; "reason": string; "status": "requested" | "approved" | "rejected"; "requested_by": string; "requested_at": string; "decided_by"?: string; "decided_at"?: string | null; "decision_reason"?: string; };

export type CreateReleaseGatePolicyRequest = { "version": string; "require_warning_acknowledgement": boolean; "waivable_warning_codes": Array<string>; };

export type PreviewReleaseGateRequest = { "release_id": string; };

export type RequestReleaseGateWaiverRequest = { "evidence_id": string; "issue_code": string; "reason": string; };

export type DecideReleaseGateWaiverRequest = { "approve": boolean; "reason": string; };

export type ReleaseGatePolicyResponse = { "release_gate_policy": ReleaseGatePolicy; };

export type ReleaseGateEvidenceResponse = { "release_gate_evidence": ReleaseGateEvidence; };

export type ReleaseGateWaiverResponse = { "release_gate_waiver": ReleaseGateWaiver; };

export type StudentPublishedExam = { "exam_id": string; "name": string; "subject": string; "release_version": number; "published_at": string; };

export type StudentPublishedExamListResponse = { "exams": Array<StudentPublishedExam>; };

export type PublishedQuestionAppeal = { "id": string; "tenant_id": string; "exam_id": string; "student_id"?: string; "submission_id": string; "source_release_id": string; "source_release_version": number; "question_id": string; "question_no": string; "source_score": number; "source_max_score": number; "reason_code": string; "reason": string; "selected_region"?: Record<string, unknown>; "status": string; "assigned_to"?: string; "decision"?: string; "public_response"?: string; "regrade_job_id"?: string; "new_release_id"?: string; "created_by"?: string; "decided_by"?: string; "decided_at"?: string | null; "created_at": string; "updated_at": string; "revision": number; };

export type StudentPublishedQuestionAppeal = { "id": string; "exam_id": string; "source_release_id": string; "source_release_version": number; "question_id": string; "question_no": string; "source_score": number; "source_max_score": number; "reason_code": string; "reason": string; "selected_region"?: Record<string, unknown>; "status": string; "decision"?: string; "public_response"?: string; "new_release_id"?: string; "created_at": string; "updated_at": string; };

export type PublishedQuestionAppealEvent = { "id": string; "appeal_id": string; "type": string; "actor_id"?: string; "payload"?: Record<string, unknown>; "created_at": string; };

export type CreatePublishedQuestionAppealRequest = { "exam_id"?: string; "source_release_id": string; "question_id": string; "reason_code": string; "reason": string; "selected_region"?: Record<string, unknown>; };

export type StartQuestionAppealReviewRequest = { "assigned_to": string; "expected_revision": number; };

export type DecideQuestionAppealRequest = { "decision": "reject" | "refer_regrade"; "public_response": string; "private_note"?: string; "regrade_job_id"?: string; "expected_revision": number; };

export type ResolveQuestionAppealRequest = { "new_release_id": string; "public_response"?: string; "private_note"?: string; "expected_revision": number; };

export type PublishedQuestionAppealResponse = { "appeal": PublishedQuestionAppeal; };

export type StudentPublishedQuestionAppealResponse = { "appeal": StudentPublishedQuestionAppeal; };

export type PublishedQuestionAppealListResponse = { "appeals": Array<PublishedQuestionAppeal>; };

export type StudentPublishedQuestionAppealListResponse = { "appeals": Array<StudentPublishedQuestionAppeal>; };

export type PublishedQuestionAppealEventsResponse = { "events": Array<PublishedQuestionAppealEvent>; };

export type QuestionAppealReleaseVersion = { "id": string; "version": number; "source": string; "status": string; "reason": string; "published_at"?: string | null; };

export type PublishedQuestionAppealContext = { "appeal": PublishedQuestionAppeal; "source_type": string; "rubric_snapshot"?: Record<string, unknown>; "release_history": Array<QuestionAppealReleaseVersion>; "answer_image_ready": boolean; };

export type PublishedQuestionAppealContextResponse = { "context": PublishedQuestionAppealContext; };

export type CaptureUploadInitRequest = { "sha256": string; "size": number; "mime": string; "exam": string; "batch": string; "idempotency_key": string; "filename"?: string; };

export type CaptureUploadCompleteRequest = { "sha256"?: string; };

export type CaptureUploadSession = { "remote_upload_id": string; "exam": string; "batch": string; "mime": string; "sha256": string; "size": number; "chunk_size": number; "confirmed_offset": number; "status": "uploading" | "finalizing" | "completed" | "failed"; "file_asset_id"?: string; "capture_file_id"?: string; "error_code"?: string; "created_at": string; "completed_at"?: string | null; };

export type CaptureUploadInitResponse = { "remote_upload_id": string; "chunk_size": number; "confirmed_offset": number; "already_exists": boolean; "status": "uploading" | "finalizing" | "completed" | "failed"; "file_asset_id"?: string; "capture_file_id"?: string; "error_code"?: string; };

export type CaptureUploadChunkResponse = { "remote_upload_id": string; "confirmed_offset": number; "status": "uploading" | "finalizing" | "completed" | "failed"; };

export type BackmarkTimeRange = { "from"?: string | null; "to"?: string | null; };

export type GraderQualityWindow = { "id": string; "exam_id": string; "question_id": string; "grader_id": string; "window_size": number; "window_start": string; "window_end": string; "sample_count": number; "mean_error": number; "mae": number; "exact_agreement": number; "rubric_agreement"?: number; "severe_rate": number; "middle_score_sample_count": number; "middle_score_mae"?: number; "middle_score_exact_agreement"?: number; "ewma_bias"?: number; "status": "insufficient_data" | "stable" | "warning" | "critical"; "computed_at": string; };

export type GraderQualityWindowListResponse = { "windows": Array<GraderQualityWindow>; };

export type RecomputeGraderDriftRequest = { "grader_id"?: string; };

export type GradingQualityIncident = { "id": string; "exam_id": string; "question_id": string; "grader_id": string; "source_window_id": string; "type": "grader_bias_high" | "grader_bias_low" | "high_inconsistency" | "severe_seed_failure" | "rubric_disagreement_spike" | "suspicious_speed"; "severity": "warning" | "critical"; "metric_snapshot": Record<string, unknown>; "affected_range": Record<string, unknown>; "status": "open" | "acknowledged" | "resolved"; "created_at": string; "resolved_at"?: string | null; };

export type RecomputeGraderDriftResponse = { "windows": Array<GraderQualityWindow>; "created_incidents": Array<GradingQualityIncident>; };

export type GradingQualityIncidentListResponse = { "incidents": Array<GradingQualityIncident>; };

export type GradingQualityIncidentResponse = { "incident": GradingQualityIncident; };

export type BackmarkScoreBand = { "min"?: number | null; "max"?: number | null; };

export type BackmarkSelector = { "time_range"?: BackmarkTimeRange; "task_ids"?: Array<string>; "grader_id"?: string; "score_band"?: BackmarkScoreBand; };

export type BackmarkPolicy = { "disposition": "confirm" | "arbitrate" | "regrade"; "arbitration_delta"?: number; };

export type BackmarkPreviewRequest = { "selector": BackmarkSelector; };

export type CreateBackmarkBatchRequest = { "source_incident_id": string; "selector": BackmarkSelector; "policy": BackmarkPolicy; "reassigned_to": string; "selector_hash"?: string; };

export type BackmarkBand = { "score": number; "count": number; };

export type BackmarkPreview = { "affected_count": number; "score_bands": Array<BackmarkBand>; "time_range": BackmarkTimeRange; "selector_hash"?: string; };

export type BackmarkPreviewResponse = { "preview": BackmarkPreview; };

export type BackmarkBatch = { "id": string; "exam_id": string; "question_id": string; "source_incident_id": string; "selector": BackmarkSelector; "policy": BackmarkPolicy; "affected_count": number; "status": "open" | "in_progress" | "ready_for_confirmation" | "completed" | "cancelled"; "created_by": string; "created_at": string; "updated_at": string; };

export type BackmarkItem = { "id": string; "batch_id": string; "review_task_id": string; "original_grade_id": string; "reassigned_task_id"?: string; "new_grade_id"?: string; "original_reviewer_id": string; "reassigned_to": string; "original_score": number; "max_score": number; "new_score"?: number; "diff"?: number; "status": string; "revision": number; "created_at": string; "updated_at": string; };

export type BackmarkHistogram = { "delta": number; "count": number; };

export type BackmarkSummary = { "batch": BackmarkBatch; "items": Array<BackmarkItem>; "diff_histogram": Array<BackmarkHistogram>; "regrade_required_count"?: number; "pending_count"?: number; "completed_count"?: number; };

export type BackmarkSummaryResponse = { "backmark": BackmarkSummary; "next_cursor": string; "has_more": boolean; };

export type BackmarkRegradeInput = { "source_release_id": string; "assignee_id": string; };

export type BackmarkBatchListResponse = { "backmark_batches": Array<BackmarkBatch>; "next_cursor": string; "has_more": boolean; };

export type BackmarkGraderItem = { "id": string; "status": "pending" | "in_progress"; "max_score": number; "revision": number; "created_at": string; };

export type BackmarkGraderItemResponse = { "backmark_item": BackmarkGraderItem; };

export type BackmarkGraderItemListResponse = { "backmark_items": Array<BackmarkGraderItem>; "next_cursor": string; "has_more": boolean; };

export type BackmarkQuestion = { "id": string; "question_no": string; "question_type": string; "score": number; "stem"?: string; "knowledge_points"?: Array<string>; };

export type BackmarkAnswer = { "raw_answer"?: string; "ocr_text"?: string; "segment_status": string; "segment_image_url": string; };

export type BackmarkGraderContext = { "item": BackmarkGraderItem; "expected_revision": number; "question": BackmarkQuestion; "frozen_rubric": FrozenReviewRubric; "answer": BackmarkAnswer; };

export type BackmarkGraderContextResponse = { "backmark_context": BackmarkGraderContext; };

export type BackmarkRubricSelection = { "point_id": string; "score": number; };

export type SubmitBackmarkItemRequest = { "score": number; "rubric_selections": Array<BackmarkRubricSelection>; "comments"?: string; "expected_revision": number; };

export type BackmarkGrade = { "id": string; "backmark_item_id": string; "reviewer_id": string; "score": number; "max_score": number; "rubric_selections": Array<BackmarkRubricSelection>; "comments"?: string; "created_at": string; };

export type SubmitBackmarkItemResponse = { "backmark_item": BackmarkGraderItem; "backmark_grade": BackmarkGrade; };

export type ProcessingStage = "RECEIVED" | "VALIDATED" | "QUALITY_CHECKED" | "REGISTERED" | "IDENTIFIED" | "PARSED" | "SEGMENTED" | "READY";

export type ProcessingIssueCode = "BLOCKED_MISSING_IDENTITY" | "BLOCKED_MISSING_PAGE" | "BLOCKED_BAD_ALIGNMENT" | "BLOCKED_LOW_IMAGE_QUALITY" | "OCR_LOW_CONFIDENCE" | "MATH_PARSE_FAILED" | "CHEMISTRY_PARSE_FAILED" | "TABLE_PARSE_FAILED" | "DIAGRAM_PARSE_FAILED" | "SEGMENTATION_FAILED";

export type ProcessingExceptionSeverity = "P0" | "P1" | "P2" | "P3";

export type ProcessingExceptionStatus = "open" | "assigned" | "resolved";

export type ProcessingStageCount = { "stage": ProcessingStage; "count": number; };

export type ProcessingIssueCount = { "code": ProcessingIssueCode; "count": number; };

export type ProcessingSummary = { "exam_id": string; "total_pages": number; "ready_pages": number; "blocked_pages": number; "pending_pages": number; "by_stage": Array<ProcessingStageCount>; "issues": Array<ProcessingIssueCount>; "generated_at": string; };

export type ProcessingSummaryResponse = { "summary": ProcessingSummary; };

export type ProcessingException = { "id": string; "exam_id": string; "page_id": string; "source_type": string; "source_id": string; "code": ProcessingIssueCode; "severity": ProcessingExceptionSeverity; "blocking": boolean; "status": ProcessingExceptionStatus; "assigned_to"?: string; "details": Record<string, unknown>; "created_at": string; "updated_at": string; "resolved_at"?: string | null; "resolution"?: string; "retry_source_type"?: string; "retry_source_id"?: string; };

export type ProcessingExceptionListResponse = { "exceptions": Array<ProcessingException>; "next_cursor"?: string; "has_more": boolean; "projected_at"?: string; };

export type AssignProcessingExceptionRequest = { "assignee_id": string; };

export type ResolveProcessingExceptionRequest = { "resolution": string; };

export type ProcessingExceptionResponse = { "exception": ProcessingException; };

export type ProcessingWorkerAttempt = { "id": string; "tenant_id": string; "task_id": string; "attempt_no": number; "worker_service": string; "worker_instance_id": string; "status": string; "started_at": string; "heartbeat_at"?: string | null; "completed_at"?: string | null; "duration_ms"?: number; "error_code"?: string; "error_detail"?: Record<string, unknown>; };

export type ProcessingWorkerTask = { "id": string; "tenant_id": string; "task_type": string; "queue_name": string; "source_type": string; "source_id": string; "status": string; "priority": number; "payload": Record<string, unknown>; "payload_schema_version": string; "result"?: Record<string, unknown>; "result_schema_version"?: string; "idempotency_key": string; "dedupe_key"?: string; "max_attempts": number; "attempt_count": number; "retry_backoff_seconds": number; "not_before"?: string | null; "lease_expires_at"?: string | null; "leased_by"?: string; "worker_service"?: string; "worker_instance_id"?: string; "started_at"?: string | null; "completed_at"?: string | null; "cancelled_at"?: string | null; "duration_ms"?: number; "error_code"?: string; "error_detail"?: Record<string, unknown>; "revision": number; "created_by"?: string; "created_at": string; "updated_at": string; "attempts"?: Array<ProcessingWorkerAttempt>; };

export type ExamSessionBlueprintSectionRequest = { "title": string; "question_type": string; "question_count": number; "score_per_question": number; };

export type ExamSessionSubjectRequest = { "subject": string; "total_score": number; "duration_minutes": number; "candidate_rule"?: string; "class_ids"?: Array<string>; "sections": Array<ExamSessionBlueprintSectionRequest>; };

export type CreateExamSessionRequest = { "school_id": string; "grade_id": string; "template_id"?: string; "name": string; "exam_type": string; "grading_mode": string; "appeal_enabled"?: boolean; "publish_policy": string; "class_ids"?: Array<string>; "subjects": Array<ExamSessionSubjectRequest>; "command_id": string; };

export type ExamSessionChild = { "id": string; "tenant_id": string; "school_id": string; "exam_session_id"?: string; "exam_session_name"?: string; "exam_session_grade_id"?: string; "name": string; "subject": string; "exam_type": string; "total_score": number; "status": string; "grading_mode": string; "appeal_enabled": boolean; "publish_policy": string; "created_by": string; "class_ids": Array<string>; "revision": number; "created_at": string; "updated_at": string; };

export type ExamSession = { "id": string; "tenant_id": string; "school_id": string; "grade_id": string; "template_id"?: string; "template_version"?: number; "name": string; "exam_type": string; "status": string; "grading_mode": string; "appeal_enabled": boolean; "publish_policy": string; "created_by": string; "revision": number; "exams": Array<ExamSessionChild>; "created_at": string; "updated_at": string; };

export type ExamSessionResponse = { "exam_session": ExamSession; };

export type ExamSessionCommand = { "command_id": string; "status": "not_accepted" | "processing" | "succeeded" | "rejected" | "unknown"; "http_status"?: number; "error_code"?: string; "exam_session"?: ExamSession; };

export type ExamSessionCommandResponse = { "command": ExamSessionCommand; };

export type PaperImportRole = "auto" | "question" | "answer" | "solution" | "rubric" | "mixed" | "unknown";

export type CreatePaperImportSource = { "file_asset_id": string; "document_index": number; "role_hint"?: PaperImportRole; };

export type ReplacePaperImportSource = { "id": string; "document_index": number; "role_hint": PaperImportRole; };

export type CreatePaperImportRequest = unknown | unknown | unknown;

export type AddPaperImportSourcesRequest = { "sources": Array<CreatePaperImportSource>; "expected_generation": number; };

export type ReplacePaperImportSourcesRequest = { "sources": Array<ReplacePaperImportSource>; "expected_generation": number; };

export type PaperImportSourceRef = { "source_id": string; "file_asset_id": string; "document_index": number; "page_no"?: number; "block_id"?: string; "bbox"?: unknown; "text_start"?: number; "text_end"?: number; "ocr_confidence"?: number; };

export type PaperImportSource = { "id": string; "file_asset_id": string; "document_index": number; "role_hint": PaperImportRole; "detected_role": "question" | "answer" | "solution" | "rubric" | "mixed" | "unknown"; "role_confidence": number; "processing_status": "pending" | "processing" | "processed" | "failed"; "original_name"?: string; "content_type"?: string; "created_at": string; };

export type PaperImportQuestionCandidate = { "candidate_id": string; "question_no_raw"?: string; "question_no_normalized"?: string; "parent_question_no"?: string; "subquestion_no"?: string; "section_hint"?: string; "stem"?: string; "options": Array<string>; "question_type"?: string; "score"?: number; "knowledge_point_hints": Array<string>; "confidence": number; "source_refs": Array<PaperImportSourceRef>; "issues": Array<string>; };

export type PaperImportAnswerCandidate = { "candidate_id": string; "question_no_hint"?: string; "question_no_normalized"?: string; "subquestion_no_hint"?: string; "standard_answer"?: unknown; "equivalent_answers": Array<unknown>; "tolerance"?: unknown; "confidence": number; "source_refs": Array<PaperImportSourceRef>; "issues": Array<string>; };

export type PaperImportSolutionStep = { "step_no": number; "content": string; };

export type PaperImportSolutionCandidate = { "candidate_id": string; "question_no_hint"?: string; "question_no_normalized"?: string; "subquestion_no_hint"?: string; "raw_text": string; "steps": Array<PaperImportSolutionStep>; "confidence": number; "source_refs": Array<PaperImportSourceRef>; "issues": Array<string>; };

export type PaperImportRubricEvidenceRequirement = { "type": "all_of" | "any_of" | "at_least" | "valid_transformation" | "final_result" | "concept" | "unit" | "domain"; "target"?: string; "minimum"?: number; "children"?: Array<PaperImportRubricEvidenceRequirement>; };

export type PaperImportRubricCandidatePoint = { "id": string; "description": string; "score"?: number | null; "required"?: boolean | null; "evidence_requirements"?: Array<PaperImportRubricEvidenceRequirement>; };

export type PaperImportRubricCandidate = { "candidate_id": string; "question_no_hint"?: string; "question_no_normalized"?: string; "max_score"?: number | null; "points": Array<PaperImportRubricCandidatePoint>; "deductions": Array<unknown>; "examples": Array<unknown>; "confidence": number; "source_refs": Array<PaperImportSourceRef>; "issues": Array<string>; };

export type PaperImportIssue = { "code": string; "severity": "info" | "warning" | "error"; "certainty": "confirmed" | "suspected" | "unknown"; "question_no"?: string; "section"?: string; "message": string; "confidence"?: number; "source_refs": Array<PaperImportSourceRef>; "resolution_hint"?: string; };

export type PaperImportAnswerKeyInput = { "standard_answer": unknown; "equivalent_answers": Array<unknown>; "tolerance": unknown; };

export type PaperImportRubricPoint = { "id": string; "description": string; "score": number; "required": boolean; };

export type PaperImportRubricInput = { "status": string; "max_score": number; "points": Array<PaperImportRubricPoint>; "deductions": Array<unknown>; "examples": Array<unknown>; };

export type PaperImportSolutionInput = { "raw_text": string; "steps": Array<PaperImportSolutionStep>; "source_refs": Array<PaperImportSourceRef>; };

export type MathRubricDraftRequest = { "candidate_id": string; "expected_generation": number; "expected_updated_at": string; };

export type MathRubricDraftPoint = { "id": string; "description": string; "suggested_score": unknown; "evidence_step_ids": Array<string>; "source_refs": Array<PaperImportSourceRef>; "review_note": string; };

export type MathRubricDraftSuggestion = { "candidate_id": string; "question_no_normalized": string; "max_score": unknown; "points": Array<MathRubricDraftPoint>; "origin": "ai_suggestion_from_solution"; "status": "review_required"; "provenance": Record<string, unknown>; "issues": Array<string>; };

export type MathRubricDraftResponse = { "suggested_rubric_candidates": Array<MathRubricDraftSuggestion>; };

export type PaperImportDraftQuestion = { "candidate_id"?: string; "answer_candidate_id"?: string; "solution_candidate_id"?: string; "rubric_candidate_id"?: string; "source_refs": Array<PaperImportSourceRef>; "question_no": string; "question_type": string; "assessment_archetype"?: "selected_response" | "exact_text" | "numeric_expression" | "structured_steps" | "short_constructed" | "extended_response" | "diagram_graph" | "table_experiment"; "score": number; "stem": string; "knowledge_points": Array<string>; "answer_key"?: PaperImportAnswerKeyInput; "solution"?: PaperImportSolutionInput; "rubric"?: PaperImportRubricInput; "confidence": number; "issues": Array<string>; "matched_question_id"?: string; "match_status"?: "create" | "matched" | "matched_by_order" | "mismatch" | "extra" | "ambiguous"; "completeness_status"?: "complete" | "needs_review"; "human_confirmed_fields"?: Array<string>; "parent_question_no"?: string; "subquestion_no"?: string; "options"?: Array<string>; "score_source"?: "material" | "missing" | "blueprint" | "human"; "score_resolution"?: "use_blueprint" | "use_material"; "question_type_resolution"?: "use_blueprint" | "use_material"; };

export type ReviewPaperImportRequest = { "expected_generation": number; "questions": Array<PaperImportDraftQuestion>; };

export type PaperImportRuntimeProgress = { "task_type": string; "task_status": string; "stage"?: string; "phase"?: string; "completed"?: number; "total"?: number; "unit"?: string; "page_no"?: number; "page_total"?: number; "batch_no"?: number; "batch_total"?: number; "batch_size"?: number; "event_seq"?: number; "cold_start"?: boolean; "model"?: string; "runtime_mode"?: "resident" | "per_job"; "runtime_plan_source"?: "explicit" | "measured_profile" | "compatibility_default"; "runtime_batch_size"?: number; "parse_route"?: "pending" | "unrelated_guard" | "anchored" | "full_model" | "compact_model"; "message"?: string; "progress_changed_at"?: string; "started_at"?: string; "updated_at": string; };

export type PaperImportJob = { "id": string; "tenant_id": string; "exam_id": string; "exam_paper_id": string; "paper_file_asset_id": string; "answer_file_asset_id": string; "status": "processing" | "review_required" | "failed" | "cancelled" | "applied"; "generation": number; "run_id": string; "source_revision": string; "result_generation"?: number; "subject": string; "authoritative_subject_code"?: string; "recognition_policy_version"?: string; "recognition_policy_hash"?: string; "formula_status"?: "pending" | "running" | "succeeded" | "review_required" | "failed"; "formula_region_count"?: number; "formula_review_count"?: number; "runtime_progress"?: PaperImportRuntimeProgress; "model_usage"?: { "input_tokens"?: number; "cached_input_tokens"?: number; "output_tokens"?: number; "reasoning_tokens"?: number; "total_tokens"?: number; }; "sources": Array<PaperImportSource>; "question_candidates": Array<PaperImportQuestionCandidate>; "answer_candidates": Array<PaperImportAnswerCandidate>; "solution_candidates": Array<PaperImportSolutionCandidate>; "rubric_candidates": Array<PaperImportRubricCandidate>; "structured_issues": Array<PaperImportIssue>; "questions": Array<PaperImportDraftQuestion>; "issues": Array<string>; "error_code"?: string; "created_by": string; "created_at": string; "updated_at": string; "applied_at"?: string; };

export type PaperImportResponse = { "import": PaperImportJob; };

export type PaperImportListResponse = { "imports": Array<PaperImportJob>; };

export type ProcessingRetryResponse = { "task": ProcessingWorkerTask; };

export type ScoringQuestionSummary = { "question_id": string; "question_no": string; "question_type": string; "total": number; "queued": number; "confirmed": number; "review": number; "failed": number; };

export type ScoringSummary = { "run"?: ScoringRun; "questions": Array<ScoringQuestionSummary>; };

export type ScoringSummaryResponse = { "scoring_summary": ScoringSummary; };

export type ScoringReadinessCheck = { "code": string; "label": string; "passed": boolean; "severity": "blocker" | "warning"; "message": string; "count"?: number; };

export type ScoringReadiness = { "ready": boolean; "exam_status": string; "total_questions": number; "total_segments": number; "ready_segments": number; "automatic_candidates": number; "manual_review_candidates": number; "active_run"?: ScoringRun; "checks": Array<ScoringReadinessCheck>; };

export type ScoringReadinessResponse = { "scoring_readiness": ScoringReadiness; };

export type ScoringRun = { "id": string; "tenant_id": string; "exam_id": string; "idempotency_key": string; "status": "queued" | "processing" | "needs_review" | "failed" | "cancelling" | "cancelled" | "completed"; "started_by": string; "created_at": string; "updated_at": string; "total_count": number; "queued_count": number; "auto_confirmed_count": number; "human_confirmed_count": number; "review_count": number; "failed_count": number; "started_at"?: string; "completed_at"?: string; };

export type StartScoringRunRequest = { "idempotency_key": string; };

export type ScoringRunResponse = { "scoring_run": ScoringRun; };

export type ScoringRunItem = { "answer_segment_id": string; "submission_id": string; "submission_page_id": string; "page_no": number; "page_file_asset_id": string; "question_id": string; "question_no": string; "question_type": string; "assessment_snapshot_id": string; "anonymous_code": string; "normalized_bbox": (Record<string, never> & Record<string, number>); "state": string; "recognition_source"?: string; "recognized_answer"?: string; "recognition_decision"?: string; "recognition_confidence"?: number; "standard_answer"?: unknown; "rule_type"?: string; "score"?: number; "max_score"?: number; "grade_source"?: string; "omr_run_id"?: string; "runtime_task_id"?: string; "runtime_status"?: string; "review_task_id"?: string; "review_status"?: string; "reason_code"?: string; "error_code"?: string; };

export type OMRCalibrationSummary = { "total_count": number; "labeled_count": number; "pending_count": number; "match_count": number; "mismatch_count": number; "eligible_count": number; "eligible_match_count": number; "eligible_mismatch_count": number; "option_coverage": (Record<string, never> & Record<string, number>); "question_coverage": (Record<string, never> & Record<string, number>); "stratum_coverage": (Record<string, never> & Record<string, number>); "ready_to_approve": boolean; "blockers": Array<string>; };

export type OMRCalibrationSession = { "id": string; "tenant_id": string; "template_id": string; "template_content_hash": string; "scope_type": "question" | "template"; "question_id"?: string; "question_ids": Array<string>; "question_type": "single_choice" | "true_false" | "template"; "profile_version": string; "profile_hash": string; "reference_file_asset_id": string; "reference_sha256": string; "option_labels": Array<string>; "sample_seed": string; "sample_count": number; "minimum_samples": number; "minimum_samples_per_option": number; "minimum_samples_per_stratum": number; "minimum_confidence": number; "inherited_from_session_id"?: string; "status": "draft" | "approved" | "revoked" | "discarded"; "created_by": string; "created_at": string; "approved_by"?: string; "approved_at"?: string; "approval_note"?: string; "evidence_hash"?: string; "revoked_by"?: string; "revoked_at"?: string; "revoke_reason"?: string; "discarded_by"?: string; "discarded_at"?: string; "discard_reason"?: string; "updated_at": string; "summary": OMRCalibrationSummary; };

export type OMRCalibrationCase = { "id": string; "calibration_id": string; "omr_run_id": string; "answer_segment_id": string; "question_id": string; "question_no": string; "question_type": "single_choice" | "multiple_choice" | "true_false"; "option_labels": Array<string>; "sample_stratum": "selected_high" | "selected_low" | "blank" | "ambiguous" | ""; "crop_sha256": string; "observed_decision": string; "observed_options": Array<string> | null; "observed_confidence": number; "measurements": Array<Record<string, unknown>> | null; "expected_options"?: Array<string>; "matches"?: boolean; "labeled_by"?: string; "labeled_at"?: string; "created_at": string; "overlay_file_asset_id"?: string; "segment_image_url": string; };

export type OMRCalibrationDetail = { "session": OMRCalibrationSession; "cases": Array<OMRCalibrationCase>; };

export type CreateOMRCalibrationRequest = { "question_id"?: string; };

export type OMRCalibrationResponse = { "calibration": OMRCalibrationDetail; };

export type ScoringRunDetailResponse = { "scoring_run": ScoringRun; "items": Array<ScoringRunItem>; };

export type ScoringRunRetryResponse = { "scoring_run": ScoringRun; "requeued": number; "skipped": number; };

export type ScoringCommandRecovery = { "command_id": string; "status": "not_accepted" | "succeeded"; "scoring_run"?: ScoringRun; };

export type SubjectiveAIModelPolicy = { "model_version"?: string; "prompt_version"?: string; "min_confidence"?: number; };

export type SubjectiveAIGradeRequest = { "model_policy"?: SubjectiveAIModelPolicy; "idempotency_key"?: string; };

export type SubjectiveGradePointResult = { "code": string; "label": string; "score": number; "evidence_ids"?: Array<string>; "reason"?: string; };

export type SubjectiveGradeEvidence = { "type": string; "evidence_id"?: string; "rubric_point_id"?: string; "answer_segment_id"?: string; "answer_text"?: string; "standard_answer"?: string; "rule"?: string; "location"?: string; "confidence"?: number; "bbox"?: Array<number>; };

export type SubjectiveAIGrade = { "id": string; "tenant_id": string; "answer_segment_id": string; "question_id": string; "question_no": string; "question_type": string; "answer_version": string; "grader_type": "llm_subjective" | "mock_llm_subjective"; "model_version": string; "prompt_version": string; "rubric_version": string; "delivery_mode": string; "capability_profile": string; "adapter_request_id": string; "subjective_grading_run_id"?: string; "adapter_name": string; "provider_key": string; "deployment_key": string; "deployment_region": string; "adapter_attempts": number; "adapter_latency_ms": number; "adapter_repair_attempted": boolean; "suggested_score": number; "max_score": number; "confidence": number; "matched_points": Array<SubjectiveGradePointResult>; "missing_points": Array<SubjectiveGradePointResult>; "evidence": Array<SubjectiveGradeEvidence>; "risk_flags": Array<string>; "needs_human_review": true; "student_feedback": string; "teacher_note": string; "mock": boolean; "status": "succeeded" | "failed"; "failure_reason"?: string; "raw_output": Record<string, unknown>; "math_artifact_id"?: string; "math_artifact_version"?: number; "math_correction_revision"?: number; "math_scoring_version"?: string; "created_by": string; "created_at": string; };

export type SubjectiveAIGradeResponse = { "grade": SubjectiveAIGrade; "idempotent_replay"?: boolean; "eligibility_decision"?: AIEligibilityDecision; };

export type MathEvidenceQuality = { "recognition": number; "formula": number; "structure": number; "verification": number; "rubric_mapping": number; "critical": number; };

export type MathSemanticCandidate = { "rubric_point_id": string; "status": "supported" | "contradicted" | "uncertain"; "evidence_ids": Array<string>; "confidence": number; "reason_code": string; };

export type MathGradingReviewResponse = { "review_required": true; "code": "math_human_review_required"; "message": string; "criterion_candidates": Array<MathSemanticCandidate>; "math_rubric_score"?: MathRubricScoreResponse; "math_evidence_quality"?: MathEvidenceQuality; };

export type SubjectiveGradingRun = { "id": string; "tenant_id": string; "answer_segment_id": string; "batch_id"?: string; "answer_version": string; "question_id": string; "rubric_version": string; "model_version": string; "prompt_version": string; "min_confidence": number; "request_id": string; "math_artifact_id"?: string; "math_artifact_version"?: number; "math_correction_revision"?: number; "math_scoring_version"?: string; "status": "queued" | "processing" | "succeeded" | "failed" | "conflict"; "attempt_count": number; "grade_id"?: string; "error_code"?: string; "started_at"?: string; "completed_at"?: string; "created_at": string; "updated_at": string; };

export type SubjectiveGradingBatch = { "id": string; "tenant_id": string; "idempotency_key": string; "scoring_run_id"?: string; "status": "planned" | "processing" | "completed" | "failed" | "cancelled"; "created_by": string; "created_at": string; "updated_at": string; "segment_ids": Array<string>; "total_count": number; "queued_count": number; "processing_count": number; "succeeded_count": number; "failed_count": number; };

export type SubjectiveBatchResponse = { "batch": SubjectiveGradingBatch; };

export type SubjectiveBatchCreateRequest = { "idempotency_key": string; "scoring_run_id"?: string; "segment_ids": Array<string>; };

export type SubjectiveBatchCommandRecovery = { "command_id": string; "status": "not_accepted" | "succeeded"; "batch"?: SubjectiveGradingBatch; };

export type SubjectiveEnqueueCommandRecovery = { "command_id": string; "batch_id": string; "status": "not_accepted" | "processing" | "succeeded"; "run_request_ids": Array<string>; };

export type ReviewCommandSubmitResult = { "task": { "id": string; "status": string; "revision": number; }; "human_grade"?: ReviewCommandHumanGrade; "question_grade_id"?: string; "double_mark_session"?: ReviewCommandDoubleMarkSession; "final_grade"?: ReviewCommandFinalGrade; "arbitration_task"?: ReviewCommandArbitrationTask; };

export type ReviewCommandReviewTask = { "id": string; "tenant_id": string; "exam_id": string; "question_id": string; "question_no": string; "answer_segment_id": string; "submission_id": string; "anonymous_code": string; "source": string; "status": string; "priority": number; "assigned_to"?: string; "return_reason"?: string; "grade_round": string; "due_at"?: string | null; "revision": number; "created_by": string; "created_at": string; "updated_at": string; };

export type ReviewCommandHumanGrade = { "id": string; "tenant_id": string; "review_task_id": string; "answer_segment_id": string; "reviewer_id": string; "score": number; "max_score": number; "rubric_selections": Array<ReviewCommandRubricSelection> | null; "comments"?: string; "private_note"?: string; "student_feedback"?: string; "reason"?: string; "grade_round": string; "ai_grade_id"?: string; "created_at": string; };

export type ReviewCommandRubricSelection = { "point_id": string; "score": number; };

export type ReviewCommandDoubleMarkSession = { "id": string; "tenant_id": string; "exam_id": string; "question_id": string; "question_no": string; "answer_segment_id": string; "submission_id": string; "anonymous_code": string; "first_review_task_id": string; "second_review_task_id": string; "first_reviewer_id": string; "second_reviewer_id": string; "threshold": number; "resolution_strategy": string; "status": string; "score_difference"?: number | null; "final_grade_id"?: string; "arbitration_task_id"?: string; "created_by": string; "created_at": string; "updated_at": string; };

export type ReviewCommandFinalGrade = { "id": string; "tenant_id": string; "exam_id": string; "question_id": string; "question_no": string; "answer_segment_id": string; "submission_id": string; "anonymous_code": string; "score": number; "max_score": number; "source": string; "double_mark_session_id"?: string; "arbitration_task_id"?: string; "resolution_strategy"?: string; "locked": boolean; "created_by": string; "created_at": string; "updated_at": string; };

export type ReviewCommandArbitrationTask = { "id": string; "tenant_id": string; "double_mark_session_id": string; "exam_id": string; "question_id": string; "question_no": string; "answer_segment_id": string; "submission_id": string; "anonymous_code": string; "first_reviewer_id": string; "second_reviewer_id": string; "first_score": number; "second_score": number; "score_difference": number; "difference_reason": string; "status": string; "assigned_to"?: string; "final_score"?: number; "reason"?: string; "student_feedback"?: string; "allow_same_arbitrator": boolean; "context": ReviewCommandReviewContext; "revision": number; "created_by": string; "created_at": string; "updated_at": string; };

export type ReviewCommandReviewContext = { "raw_answer"?: string; "ocr_text"?: string; "ai_suggestion"?: Record<string, unknown>; };

export type ReviewCommandSubmitGradeInput = { "score": number; "rubric_selections": Array<ReviewCommandRubricSelection> | null; "comments": string; "private_note": string; "student_feedback": string; "reason": string; "expected_revision": number; };

export type ReviewCommandArbitrationSubmitResult = { "arbitration_task": ReviewCommandArbitrationTask; "final_grade": ReviewCommandFinalGrade; };

export type ReviewCommandCreateArbitrationTaskInput = { "double_mark_session_id": string; "assigned_to"?: string; "difference_reason"?: string; };

export type ReviewCommandAssignArbitrationInput = { "assigned_to": string; "expected_revision": number; };

export type ArbitrationTaskResponse = { "arbitration_task": ReviewCommandArbitrationTask; };

export type ReviewCommandSubmitArbitrationInput = { "final_score": number; "reason": string; "student_feedback": string; "expected_revision": number; };

export type ScoreCommandSubmissionGrade = { "id": string; "tenant_id": string; "exam_id": string; "submission_id": string; "student_id"?: string; "anonymous_code": string; "total_score": number; "max_score": number; "status": string; "locked": boolean; "confirmed_by"?: string; "confirmed_at"?: string | null; "published_by"?: string; "published_at"?: string | null; "revision": number; "created_by": string; "created_at": string; "updated_at": string; "items"?: Array<ScoreCommandFinalGrade>; };

export type ScoreCommandFinalGrade = { "id": string; "tenant_id": string; "exam_id": string; "question_id": string; "question_no": string; "answer_segment_id": string; "submission_id": string; "anonymous_code": string; "score": number; "max_score": number; "source": string; "status": string; "locked": boolean; "created_by": string; "created_at": string; "updated_at": string; };

export type ScoreCommandConfirmInput = { "reason": string; };

export type ScoreCommandPublishResult = { "status": string; "submission_grades": Array<ScoreCommandSubmissionGrade> | null; "quality": ScoreCommandQualityReport; "published_at": string; };

export type ScoreCommandQualityReport = { "passed": boolean; "issues": Array<ScoreCommandQualityIssue> | null; };

export type ScoreCommandQualityIssue = { "code": string; "message": string; "blocking": boolean; "count": number; };

export type ScoreCommandPublishInput = { "reason": string; };

export type BusinessCommandReceipt = { "command_id": string; "status": "not_accepted" | "processing" | "takeover_ready" | "succeeded" | "rejected" | "unknown"; "http_status"?: number; "error_code"?: string; "operation"?: "review.submit" | "review.arbitrate" | "review.seed-submit" | "score.confirm" | "score.publish" | "report.export"; "target_id"?: string; "payload"?: unknown; "result"?: unknown; };

export type QuestionBankMetadata = { "subject_code": SubjectCode; "education_stage": EducationStage; "grade_scope": string; "difficulty_band": "unclassified" | "easy" | "medium" | "hard"; "cognitive_level": "unclassified" | "remember" | "understand" | "apply" | "analyze" | "evaluate" | "create"; "copyright": "unknown" | "owned" | "licensed"; "language": "zh-CN" | "en"; "suggested_time_minutes"?: number; "source_year"?: number; "intended_use"?: "practice" | "homework" | "quiz" | "exam" | "mock_exam"; };

export type QuestionBankMetadataInput = { "subject_code": SubjectCode; "education_stage": EducationStage; "grade_scope": string; "difficulty_band"?: "unclassified" | "easy" | "medium" | "hard"; "cognitive_level"?: "unclassified" | "remember" | "understand" | "apply" | "analyze" | "evaluate" | "create"; "copyright"?: "unknown" | "owned" | "licensed"; "language"?: "zh-CN" | "en"; "suggested_time_minutes"?: number; "source_year"?: number; "intended_use"?: "practice" | "homework" | "quiz" | "exam" | "mock_exam"; };

export type QuestionBank = { "id": string; "tenant_id": string; "school_id": string; "name": string; "description": string; "status": "active" | "archived"; "revision": number; "created_by": string; "created_at": string; "updated_at": string; "metadata_schema_version": number; };

export type QuestionBankItem = { "id": string; "tenant_id": string; "bank_id": string; "item_code": string; "subject_code": SubjectCode; "grade_scope": string; "current_published_version_id": string | null; "status": "active" | "retired" | "archived"; "created_by": string; "created_at": string; "kind": "question" | "rubric_template"; "revision": number; };

export type QuestionBankVersion = { "id": string; "tenant_id": string; "item_id": string; "version_no": number; "schema_version": number; "revision": number; "workflow_status": "draft" | "reviewing" | "approved" | "published"; "source_version_id": string | null; "author_id": string; "content_hash": string; "hash_scope": "draft_content_v1"; "question_type": "single_choice" | "multiple_choice" | "true_false" | "fill_blank" | "numeric" | "formula" | "short_answer" | "calculation" | "essay" | "discussion" | "coding"; "assessment_archetype": "selected_response" | "exact_text" | "numeric_expression" | "structured_steps" | "short_constructed" | "extended_response" | "diagram_graph" | "table_experiment"; "stem": string; "options": Array<string>; "default_score": number; "knowledge_points": Array<string>; "metadata": QuestionBankMetadata; "created_at": string; "updated_at": string; "scoring": QuestionBankScoring; "bundle_hash": string; "answer_version_id": string | null; "rubric_version_id": string | null; "bundle_schema_version": 2; "custom_metadata"?: Record<string, unknown>; "import_provenance"?: QuestionBankImportProvenance; };

export type CreateQuestionBankRequest = { "school_id": string; "name": string; "description"?: string; };

export type UpdateQuestionBankRequest = { "expected_revision": number; "name": string; "description"?: string; "status": "active" | "archived"; };

export type CreateQuestionBankItemRequest = { "item_code": string; "question_type": "single_choice" | "multiple_choice" | "true_false" | "fill_blank" | "numeric" | "formula" | "short_answer" | "calculation" | "essay" | "discussion" | "coding"; "assessment_archetype": "selected_response" | "exact_text" | "numeric_expression" | "structured_steps" | "short_constructed" | "extended_response" | "diagram_graph" | "table_experiment"; "stem": string; "options"?: Array<string>; "default_score": number; "knowledge_points"?: Array<string>; "metadata": QuestionBankMetadataInput; "custom_metadata"?: Record<string, unknown>; };

export type CreateQuestionBankVersionRequest = { "source_version_id": string; };

export type UpdateQuestionBankVersionRequest = { "expected_revision": number; "question_type": "single_choice" | "multiple_choice" | "true_false" | "fill_blank" | "numeric" | "formula" | "short_answer" | "calculation" | "essay" | "discussion" | "coding"; "assessment_archetype": "selected_response" | "exact_text" | "numeric_expression" | "structured_steps" | "short_constructed" | "extended_response" | "diagram_graph" | "table_experiment"; "stem": string; "options"?: Array<string>; "default_score": number; "knowledge_points"?: Array<string>; "metadata": QuestionBankMetadataInput; "custom_metadata"?: Record<string, unknown>; };

export type QuestionBankResponse = { "bank": QuestionBank; };

export type QuestionBankItemResponse = { "item": QuestionBankItem; };

export type QuestionBankItemCreateResponse = { "item": QuestionBankItem; "version": QuestionBankVersion; };

export type QuestionBankVersionResponse = { "version": QuestionBankVersion; };

export type QuestionBankPage = { "banks": Array<QuestionBank>; "total": number; "limit": number; "offset": number; };

export type QuestionBankItemPage = { "items": Array<QuestionBankItem>; "total": number; "limit": number; "offset": number; };

export type QuestionBankVersionPage = { "versions": Array<QuestionBankVersion>; "total": number; "limit": number; "offset": number; };

export type QuestionBankAsset = { "file_asset_id": string; "sha256": string; "name": string; "content_type": string; };

export type QuestionBankScoring = { "answer": AnswerKeyInput | null; "solution": QuestionBankSolution | null; "rubric": RubricInput | null; "template_version_id": string | null; "assets": Array<QuestionBankAsset>; "use_policy": "practice_only" | "exam_allowed"; };

export type AnswerKeyInput = { "standard_answer": unknown; "equivalent_answers": Array<unknown>; "tolerance": { "absolute"?: number; "relative"?: number; }; };

export type QuestionBankSolution = { "raw_text": string; "steps": Array<{ "step_no": number; "content": string; }>; "source_refs": Array<unknown>; };

export type RubricInput = { "status": "approved"; "max_score": number; "points": Array<QuestionBankRubricPoint>; "deductions": Array<unknown>; "examples": Array<unknown>; };

export type QuestionBankReviewRequest = { "expected_revision": number; "bundle_hash": string; "comment": string; };

export type UpdateQuestionBankScoringRequest = { "expected_revision": number; "scoring": QuestionBankScoring; };

export type QuestionBankReviewerBindingRequest = { "expected_revision": number; "user_id": string; "read": boolean; "review": boolean; "publish": boolean; };

export type QuestionBankReview = { "id": string; "version_id": string; "reviewer_id": string; "decision": "submit-review" | "approve" | "return-to-draft" | "publish"; "comment": string; "content_revision": number; "bundle_hash": string; "created_at": string; };

export type QuestionBankReviewsResponse = { "reviews": Array<QuestionBankReview>; };

export type MaterializeQuestionBankRequest = { "expected_revision": number; "selections": Array<{ "version_id": string; "question_no": string; "sort_order": number; }>; };

export type MaterializeQuestionBankResponse = { "exam_id": string; "revision": number; "questions": Array<MaterializedBankQuestion>; "source_version_ids": Array<string>; };

export type CreateQuestionBankRubricTemplateRequest = { "item_code": string; "question_type": "single_choice" | "multiple_choice" | "true_false" | "fill_blank" | "numeric" | "formula" | "short_answer" | "calculation" | "essay" | "discussion" | "coding"; "assessment_archetype": "selected_response" | "exact_text" | "numeric_expression" | "structured_steps" | "short_constructed" | "extended_response" | "diagram_graph" | "table_experiment"; "stem": string; "options"?: Array<string>; "default_score": number; "knowledge_points"?: Array<string>; "metadata": QuestionBankMetadataInput; "bank_id": string; "custom_metadata"?: Record<string, unknown>; };

export type QuestionBankRubricPoint = { "id": string; "description": string; "score": number; "required": boolean; "evidence_requirements"?: Array<PaperImportRubricEvidenceRequirement>; };

export type MaterializedBankQuestion = ({ "id": string; "exam_id": string; "question_no": string; "question_type": string; "score": number; "stem"?: string; "sort_order": number; "status": string; "knowledge_points"?: Array<string>; "source_type": string; "source_bank_item_id": string; "source_bank_item_version_id": string; "source_content_hash": string; "bank_content"?: Record<string, unknown>; } & Record<string, unknown>);

export type QuestionBankMetadataOption = { "value": string; "label": string; "active": boolean; };

export type QuestionBankTaxonomyTerm = { "id": string; "label": string; "active": boolean; };

export type QuestionBankTaxonomyDefinition = { "id": string; "label": string; "terms": Array<QuestionBankTaxonomyTerm>; };

export type QuestionBankMetadataFieldDefinition = { "key": string; "label": string; "type": "enum" | "string" | "number" | "boolean" | "taxonomy"; "required": boolean; "min"?: number | null; "max"?: number | null; "max_length"?: number | null; "options"?: Array<QuestionBankMetadataOption>; "taxonomy_id"?: string; };

export type QuestionBankMetadataSchema = { "bank_id": string; "version": number; "fields": Array<QuestionBankMetadataFieldDefinition>; "taxonomies": Array<QuestionBankTaxonomyDefinition>; "created_by": string; "created_at": string; };

export type QuestionBankMetadataSchemaResponse = { "schema": QuestionBankMetadataSchema; };

export type UpdateQuestionBankMetadataSchemaRequest = { "expected_revision": number; "fields": Array<QuestionBankMetadataFieldDefinition>; "taxonomies": Array<QuestionBankTaxonomyDefinition>; };

export type ValidateQuestionBankMetadataRequest = { "schema_version": number; "values": Record<string, unknown>; };

export type QuestionBankMetadataValidationResult = { "valid": boolean; "field_errors": (Record<string, never> & Record<string, Array<string>>); };

export type QuestionBankACLBinding = { "user_id": string; "preset": "Viewer" | "Author" | "Reviewer" | "Publisher" | "Manager" | "Custom"; "actions": Array<"read" | "create" | "edit" | "review" | "publish" | "retire" | "statistics" | "manage">; };

export type QuestionBankACLGroup = { "id": string; "name": string; "preset": "Viewer" | "Author" | "Reviewer" | "Publisher" | "Manager"; "actions": Array<"read" | "create" | "edit" | "review" | "publish" | "retire" | "statistics" | "manage">; "member_user_ids": Array<string>; };

export type UpdateQuestionBankACLBinding = { "user_id": string; "preset": "Viewer" | "Author" | "Reviewer" | "Publisher" | "Manager"; };

export type UpdateQuestionBankACLGroup = { "id"?: string; "name": string; "preset": "Viewer" | "Author" | "Reviewer" | "Publisher" | "Manager"; "member_user_ids": Array<string>; };

export type QuestionBankACL = { "bank_id": string; "revision": number; "bindings": Array<QuestionBankACLBinding>; "groups": Array<QuestionBankACLGroup>; };

export type QuestionBankACLResponse = { "acl": QuestionBankACL; };

export type UpdateQuestionBankACLRequest = { "expected_revision": number; "bindings": Array<UpdateQuestionBankACLBinding>; "groups": Array<UpdateQuestionBankACLGroup>; };

export type RetireQuestionBankItemRequest = { "expected_revision": number; };

export type QuestionBankSearchItem = { "item": QuestionBankItem; "version": QuestionBankVersion; "statistics_available": boolean; };

export type QuestionBankSearchPage = { "items": Array<QuestionBankSearchItem>; "total": number; "limit": number; "offset": number; };

export type QuestionBankImportMapping = { "item_code"?: string; "grade_scope"?: string; "difficulty_band"?: "unclassified" | "easy" | "medium" | "hard"; "cognitive_level"?: "unclassified" | "remember" | "understand" | "apply" | "analyze" | "evaluate" | "create"; "copyright"?: "unknown" | "owned" | "licensed"; "language"?: "zh-CN" | "en"; "intended_use"?: "practice" | "homework" | "quiz" | "exam" | "mock_exam"; "options"?: Array<string>; "knowledge_points"?: Array<string>; "custom_metadata"?: Record<string, unknown>; };

export type QuestionBankImportPreviewSelection = { "question_id": string; "target_bank_id": string; "source_snapshot_id": string; "mapping": QuestionBankImportMapping; };

export type QuestionBankImportPreviewRequest = { "selections": Array<QuestionBankImportPreviewSelection>; };

export type QuestionBankImportIssue = { "code": string; "field"?: string; "message": string; "blocking": boolean; };

export type QuestionBankImportDuplicateHint = { "kind": "exact_bundle" | "same_content_different_scoring" | "similar_content"; "item_id": string; "item_code": string; "version_id": string; "version_no": number; "stem_summary": string; "content_hash": string; "bundle_hash": string; };

export type QuestionBankImportSource = { "exam_id": string; "question_id": string; "question_no": string; "readiness_snapshot_id": string; "configuration_hash": string; "snapshot_hash": string; "assessment_snapshot_id": string; "assessment_snapshot_hash": string; "assessment_snapshot_version": number; "profile_snapshot": Record<string, unknown>; "archetype_snapshot": Record<string, unknown>; "scoring_policy_snapshot": Record<string, unknown>; "source_bank_item_id"?: string; "source_bank_item_version_id"?: string; "source_content_hash"?: string; };

export type QuestionBankImportContent = { "question_type": "single_choice" | "multiple_choice" | "true_false" | "fill_blank" | "numeric" | "formula" | "short_answer" | "calculation" | "essay" | "discussion" | "coding"; "assessment_archetype": "selected_response" | "exact_text" | "numeric_expression" | "structured_steps" | "short_constructed" | "extended_response" | "diagram_graph" | "table_experiment"; "stem": string; "options": Array<string>; "default_score": number; "knowledge_points": Array<string>; "metadata": QuestionBankMetadata; "custom_metadata"?: Record<string, unknown>; };

export type QuestionBankImportPreview = { "question_id": string; "available": boolean; "content": QuestionBankImportContent; "scoring": QuestionBankScoring; "content_hash": string; "bundle_hash": string; "source": QuestionBankImportSource; "issues": Array<QuestionBankImportIssue>; "duplicate_hints": Array<QuestionBankImportDuplicateHint>; "suggested_item_code": string; };

export type QuestionBankImportPreviewItem = { "question_id": string; "status": "ready" | "failed"; "preview"?: QuestionBankImportPreview; "error_code"?: string; "retryable": boolean; };

export type QuestionBankImportPreviewBatch = { "items": Array<QuestionBankImportPreviewItem>; };

export type QuestionBankImportRequest = { "target_bank_id": string; "source_snapshot_id": string; "assessment_snapshot_id": string; "scoring_source": "original_exam"; "dedup_decision": "new_item" | "new_version"; "existing_item_id"?: string; "expected_target_revision": number; "mapping": QuestionBankImportMapping; "command_id": string; "expected_target_schema_version": number; };

export type QuestionBankImportProvenance = { "id": string; "target_bank_id": string; "target_schema_version": number; "source": QuestionBankImportSource; "scoring_source": "original_exam"; "dedup_decision": "new_item" | "new_version"; "linked_item_id"?: string; "mapping": QuestionBankImportMapping; "command_id": string; "created_at": string; };

export type QuestionBankImportResult = { "item": QuestionBankItem; "version": QuestionBankVersion; "duplicate_hints": Array<QuestionBankImportDuplicateHint>; "issues": Array<QuestionBankImportIssue>; "provenance": QuestionBankImportProvenance; };

export type QuestionBankBatchImportItem = { "target_bank_id": string; "source_snapshot_id": string; "assessment_snapshot_id": string; "scoring_source": "original_exam"; "dedup_decision": "new_item" | "new_version"; "existing_item_id"?: string; "expected_target_revision": number; "mapping": QuestionBankImportMapping; "command_id": string; "expected_target_schema_version": number; "question_id": string; };

export type QuestionBankBatchImportRequest = { "items": Array<QuestionBankBatchImportItem>; };

export type QuestionBankBatchImportResultItem = { "question_id": string; "command_id": string; "status": "succeeded" | "failed"; "result"?: QuestionBankImportResult; "error_code"?: string; "retryable": boolean; };

export type QuestionBankBatchImportResult = { "items": Array<QuestionBankBatchImportResultItem>; };

export interface components {
  schemas: {
    "RecordSegmentAnswerInput": RecordSegmentAnswerInput;
    "SegmentAnswer": SegmentAnswer;
    "ObjectiveGrade": ObjectiveGrade;
    "ObjectiveGradePoint": ObjectiveGradePoint;
    "ObjectiveGradeEvidence": ObjectiveGradeEvidence;
    "ScoringRule": ScoringRule;
    "PlatformSchoolAdministrator": PlatformSchoolAdministrator;
    "PlatformSchoolMemberCounts": PlatformSchoolMemberCounts;
    "PlatformSchoolUsageSummary": PlatformSchoolUsageSummary;
    "PlatformSchoolModelHealth": PlatformSchoolModelHealth;
    "PlatformSchoolSummary": PlatformSchoolSummary;
    "PlatformSchoolListResponse": PlatformSchoolListResponse;
    "PlatformSchoolResponse": PlatformSchoolResponse;
    "PlatformSchoolMember": PlatformSchoolMember;
    "PlatformSchoolMembersResponse": PlatformSchoolMembersResponse;
    "PlatformSchoolUsageResponse": PlatformSchoolUsageResponse;
    "PlatformSchoolUsageBreakdown": PlatformSchoolUsageBreakdown;
    "PlatformSchoolModelHealthResponse": PlatformSchoolModelHealthResponse;
    "PlatformSchoolActivityResponse": PlatformSchoolActivityResponse;
    "PanelModelRoleBindingInput": PanelModelRoleBindingInput;
    "PanelModelRoleBinding": PanelModelRoleBinding;
    "PanelModelRoleBindingList": PanelModelRoleBindingList;
    "PanelModelRoleBindingResponse": PanelModelRoleBindingResponse;
    "ManagedChatAttachment": ManagedChatAttachment;
    "ManagedChatMessage": ManagedChatMessage;
    "ManagedChatRequest": ManagedChatRequest;
    "ManagedChatModelStatus": ManagedChatModelStatus;
    "ManagedChatModelResponse": ManagedChatModelResponse;
    "ManagedChatUsage": ManagedChatUsage;
    "ManagedChatCompletion": ManagedChatCompletion;
    "ManagedChatCompletionResponse": ManagedChatCompletionResponse;
    "OnboardingNextAction": OnboardingNextAction;
    "OnboardingCheck": OnboardingCheck;
    "OnboardingReadiness": OnboardingReadiness;
    "MFAStatusResponse": MFAStatusResponse;
    "TOTPEnrollmentRequest": TOTPEnrollmentRequest;
    "TOTPEnrollmentResponse": TOTPEnrollmentResponse;
    "TOTPConfirmationRequest": TOTPConfirmationRequest;
    "MFARecoveryCodesResponse": MFARecoveryCodesResponse;
    "MFAChallengeStartRequest": MFAChallengeStartRequest;
    "MFAChallengeStartResponse": MFAChallengeStartResponse;
    "MFAChallengeVerifyRequest": MFAChallengeVerifyRequest;
    "MFAChallengeVerifyResponse": MFAChallengeVerifyResponse;
    "MFACommandRequest": MFACommandRequest;
    "MFADisableResponse": MFADisableResponse;
    "OrganizationScope": OrganizationScope;
    "AuthUser": AuthUser;
    "AuthLoginRequest": AuthLoginRequest;
    "AuthLoginResponse": AuthLoginResponse;
    "WechatLoginStartRequest": WechatLoginStartRequest;
    "WechatLoginChallenge": WechatLoginChallenge;
    "WechatLoginPollRequest": WechatLoginPollRequest;
    "WechatLoginStatus": WechatLoginStatus;
    "AuthTokenResponse": AuthTokenResponse;
    "ReauthenticateRequest": ReauthenticateRequest;
    "SessionLockResponse": SessionLockResponse;
    "ReauthenticateResponse": ReauthenticateResponse;
    "OneTimeTokenRequest": OneTimeTokenRequest;
    "CompleteOneTimePasswordRequest": CompleteOneTimePasswordRequest;
    "ActivationPreview": ActivationPreview;
    "RecoveryPreview": RecoveryPreview;
    "ActivationPreviewResponse": ActivationPreviewResponse;
    "RecoveryPreviewResponse": RecoveryPreviewResponse;
    "ActivationCompleteResponse": ActivationCompleteResponse;
    "RecoveryCompleteResponse": RecoveryCompleteResponse;
    "ManagedUserRecovery": ManagedUserRecovery;
    "ManagedUserRecoveryResponse": ManagedUserRecoveryResponse;
    "ManagedUserActivationDelivery": ManagedUserActivationDelivery;
    "ManagedUserActivationDeliveryResponse": ManagedUserActivationDeliveryResponse;
    "SecurityEvent": SecurityEvent;
    "SecurityEventListResponse": SecurityEventListResponse;
    "CaptureUploadRecoveryResponse": CaptureUploadRecoveryResponse;
    "CaptureBatchCommandResponse": CaptureBatchCommandResponse;
    "CaptureBatchResponse": CaptureBatchResponse;
    "CaptureBatchCreateRequest": CaptureBatchCreateRequest;
    "CaptureBatch": CaptureBatch;
    "FormulaCandidate": FormulaCandidate;
    "MathRubricEvidence": MathRubricEvidence;
    "MathCriterionDecision": MathCriterionDecision;
    "MathCriterionCandidate": MathCriterionCandidate;
    "MathRubricLegacyPoint": MathRubricLegacyPoint;
    "MathRubricScoreResponse": MathRubricScoreResponse;
    "MathVerification": MathVerification;
    "FailMathVerificationRuntimeRequest": FailMathVerificationRuntimeRequest;
    "MathVerificationFailureResponse": MathVerificationFailureResponse;
    "MathVerificationRuntimeInputResponse": MathVerificationRuntimeInputResponse;
    "CompleteMathVerificationRuntimeRequest": CompleteMathVerificationRuntimeRequest;
    "MathVerificationRuntimeResponse": MathVerificationRuntimeResponse;
    "MathUnderstandingRuntimeInputResponse": MathUnderstandingRuntimeInputResponse;
    "CompleteMathUnderstandingRuntimeRequest": CompleteMathUnderstandingRuntimeRequest;
    "MathUnderstandingRuntimeResponse": MathUnderstandingRuntimeResponse;
    "MathFormulaArtifact": MathFormulaArtifact;
    "MathSolutionStep": MathSolutionStep;
    "MathSolutionEdge": MathSolutionEdge;
    "MathSolutionGraph": MathSolutionGraph;
    "MathUnderstandingArtifact": MathUnderstandingArtifact;
    "MathUnderstandingContract": MathUnderstandingContract;
    "MathCorrectionOperation": MathCorrectionOperation;
    "CreateMathCorrectionRequest": CreateMathCorrectionRequest;
    "MathCorrection": MathCorrection;
    "MathUnderstandingResponse": MathUnderstandingResponse;
    "MathCorrectionListResponse": MathCorrectionListResponse;
    "MathCorrectionResponse": MathCorrectionResponse;
    "MathTrainingExportResponse": MathTrainingExportResponse;
    "MathPilotMetrics": MathPilotMetrics;
    "MathPilotPolicy": MathPilotPolicy;
    "MathPilotGateDecision": MathPilotGateDecision;
    "MathPilotGateEvaluation": MathPilotGateEvaluation;
    "EvaluateMathPilotGateRequest": EvaluateMathPilotGateRequest;
    "MathPilotGateResponse": MathPilotGateResponse;
    "MathPilotGateListResponse": MathPilotGateListResponse;
    "ErrorResponse": ErrorResponse;
    "CursorPageMeta": CursorPageMeta;
    "ExamWorkspaceStage": ExamWorkspaceStage;
    "ExamWorkspaceStageProgress": ExamWorkspaceStageProgress;
    "ExamWorkspaceNotice": ExamWorkspaceNotice;
    "ExamWorkspaceCounts": ExamWorkspaceCounts;
    "ExamWorkspaceNextAction": ExamWorkspaceNextAction;
    "ExamWorkspaceSubjectSummary": ExamWorkspaceSubjectSummary;
    "ExamWorkspaceSubjectExam": ExamWorkspaceSubjectExam;
    "ExamWorkspaceProjection": ExamWorkspaceProjection;
    "ExamWorkspaceResponse": ExamWorkspaceResponse;
    "CandidateRefreshResult": CandidateRefreshResult;
    "CandidateRefreshResponse": CandidateRefreshResponse;
    "ExamPage": ExamPage;
    "SubmissionPage": SubmissionPage;
    "ReviewTask": ReviewTask;
    "ReviewTaskAggregate": ReviewTaskAggregate;
    "ReviewerTaskAggregate": ReviewerTaskAggregate;
    "ReviewTaskPage": ReviewTaskPage;
    "ReviewQuestion": ReviewQuestion;
    "FrozenReviewRubric": FrozenReviewRubric;
    "ReviewAnswerArtifact": ReviewAnswerArtifact;
    "ReviewAnswerCandidate": ReviewAnswerCandidate;
    "ScoringEvidence": ScoringEvidence;
    "ReviewAISecondOpinion": ReviewAISecondOpinion;
    "ReviewAISuggestionHistoryItem": ReviewAISuggestionHistoryItem;
    "ReviewTaskClaim": ReviewTaskClaim;
    "ReviewDraft": ReviewDraft;
    "ReviewSubjectToolHints": ReviewSubjectToolHints;
    "ReviewAutomationResult": ReviewAutomationResult;
    "ReviewTaskContext": ReviewTaskContext;
    "ReviewTaskContextResponse": ReviewTaskContextResponse;
    "ReviewAnnotationVisibility": ReviewAnnotationVisibility;
    "ReviewAnnotationType": ReviewAnnotationType;
    "CanonicalImageGeometry": CanonicalImageGeometry;
    "ReviewAnnotation": ReviewAnnotation;
    "StudentReviewAnnotation": StudentReviewAnnotation;
    "StudentQuestionReviewAnnotationListResponse": StudentQuestionReviewAnnotationListResponse;
    "CreateReviewAnnotationRequest": CreateReviewAnnotationRequest;
    "UpdateReviewAnnotationRequest": UpdateReviewAnnotationRequest;
    "DeleteRevisionRequest": DeleteRevisionRequest;
    "ReviewAnnotationResponse": ReviewAnnotationResponse;
    "ReviewAnnotationListResponse": ReviewAnnotationListResponse;
    "ReviewCommentTemplate": ReviewCommentTemplate;
    "CreateReviewCommentTemplateRequest": CreateReviewCommentTemplateRequest;
    "UpdateReviewCommentTemplateRequest": UpdateReviewCommentTemplateRequest;
    "ReviewCommentTemplateResponse": ReviewCommentTemplateResponse;
    "ReviewCommentTemplateListResponse": ReviewCommentTemplateListResponse;
    "AppealPage": AppealPage;
    "BatchEnqueueResponse": BatchEnqueueResponse;
    "EducationStage": EducationStage;
    "SubjectCode": SubjectCode;
    "SubjectProfileStatus": SubjectProfileStatus;
    "QuestionArchetypeCode": QuestionArchetypeCode;
    "EvidenceType": EvidenceType;
    "ScoringMode": ScoringMode;
    "ExamRiskTier": ExamRiskTier;
    "SubjectProfile": SubjectProfile;
    "SubjectProfileListResponse": SubjectProfileListResponse;
    "QuestionArchetype": QuestionArchetype;
    "QuestionArchetypeListResponse": QuestionArchetypeListResponse;
    "PutQuestionAssessmentProfileRequest": PutQuestionAssessmentProfileRequest;
    "QuestionAssessmentProfile": QuestionAssessmentProfile;
    "QuestionAssessmentProfileResponse": QuestionAssessmentProfileResponse;
    "ExamQuestionAssessmentSnapshot": ExamQuestionAssessmentSnapshot;
    "ExamQuestionAssessmentSnapshotResponse": ExamQuestionAssessmentSnapshotResponse;
    "GoldPaperStatus": GoldPaperStatus;
    "GoldPaperVersion": GoldPaperVersion;
    "GoldPaper": GoldPaper;
    "GoldPaperResponse": GoldPaperResponse;
    "GoldPaperListResponse": GoldPaperListResponse;
    "CreateGoldPaperVersionRequest": CreateGoldPaperVersionRequest;
    "NominateGoldPaperRequest": NominateGoldPaperRequest;
    "RetireGoldPaperRequest": RetireGoldPaperRequest;
    "GoldScoreBandCoverage": GoldScoreBandCoverage;
    "GoldCoverage": GoldCoverage;
    "GoldCoverageResponse": GoldCoverageResponse;
    "CalibrationPolicy": CalibrationPolicy;
    "PutCalibrationPolicyRequest": PutCalibrationPolicyRequest;
    "CalibrationPolicyResponse": CalibrationPolicyResponse;
    "CalibrationGoldSample": CalibrationGoldSample;
    "CalibrationMetrics": CalibrationMetrics;
    "CalibrationSession": CalibrationSession;
    "CreateCalibrationSessionRequest": CreateCalibrationSessionRequest;
    "CalibrationSessionResponse": CalibrationSessionResponse;
    "SubmitCalibrationAttemptRequest": SubmitCalibrationAttemptRequest;
    "CalibrationCriterionDifference": CalibrationCriterionDifference;
    "CalibrationAttempt": CalibrationAttempt;
    "GraderQualification": GraderQualification;
    "GraderQualificationResponse": GraderQualificationResponse;
    "SubmitCalibrationAttemptResponse": SubmitCalibrationAttemptResponse;
    "AnswerGroupStatus": AnswerGroupStatus;
    "AnswerGroupSampleOutcome": AnswerGroupSampleOutcome;
    "AnswerGroupMember": AnswerGroupMember;
    "AnswerGroupDecision": AnswerGroupDecision;
    "AnswerGroup": AnswerGroup;
    "TeacherReferenceCase": TeacherReferenceCase;
    "AnswerAutomationCandidate": AnswerAutomationCandidate;
    "AnswerGroupMetrics": AnswerGroupMetrics;
    "BuildAnswerGroupsRequest": BuildAnswerGroupsRequest;
    "ReviewAnswerGroupSampleRequest": ReviewAnswerGroupSampleRequest;
    "PutAnswerGroupDecisionRequest": PutAnswerGroupDecisionRequest;
    "ConfirmAnswerGroupRequest": ConfirmAnswerGroupRequest;
    "RollbackAnswerGroupRequest": RollbackAnswerGroupRequest;
    "BuildAnswerGroupsResponse": BuildAnswerGroupsResponse;
    "AnswerGroupListResponse": AnswerGroupListResponse;
    "AnswerGroupResponse": AnswerGroupResponse;
    "AnswerGroupMutationResponse": AnswerGroupMutationResponse;
    "AnswerGroupMetricsResponse": AnswerGroupMetricsResponse;
    "ConfirmAnswerGroupResponse": ConfirmAnswerGroupResponse;
    "RollbackAnswerGroupResponse": RollbackAnswerGroupResponse;
    "QuestionScoringPolicy": QuestionScoringPolicy;
    "QualityRatio": QualityRatio;
    "QualityFinding": QualityFinding;
    "QualityQuestion": QualityQuestion;
    "QualityQuestionDashboard": QualityQuestionDashboard;
    "QualityDashboard": QualityDashboard;
    "QualityDashboardResponse": QualityDashboardResponse;
    "AIEligibilityPolicyStatus": AIEligibilityPolicyStatus;
    "AIEligibilityReason": AIEligibilityReason;
    "AIEligibilityEvaluationEvidence": AIEligibilityEvaluationEvidence;
    "AIEligibilityCalibrationEvidence": AIEligibilityCalibrationEvidence;
    "AIEligibilityOutputConstraint": AIEligibilityOutputConstraint;
    "AIEligibilityPolicy": AIEligibilityPolicy;
    "PutAIEligibilityPolicyRequest": PutAIEligibilityPolicyRequest;
    "AIEligibilityDecision": AIEligibilityDecision;
    "AIEligibilityPolicyResponse": AIEligibilityPolicyResponse;
    "AIEligibilityDecisionResponse": AIEligibilityDecisionResponse;
    "GradingEvaluationRunStatus": GradingEvaluationRunStatus;
    "GradingEvaluationReferenceKind": GradingEvaluationReferenceKind;
    "GradingEvaluationRun": GradingEvaluationRun;
    "CreateGradingEvaluationRequest": CreateGradingEvaluationRequest;
    "GradingEvaluationErrorSource": GradingEvaluationErrorSource;
    "AddGradingEvaluationObservationRequest": AddGradingEvaluationObservationRequest;
    "GradingEvaluationObservation": GradingEvaluationObservation;
    "GradingEvaluationMetrics": GradingEvaluationMetrics;
    "GradingEvaluationSliceMetric": GradingEvaluationSliceMetric;
    "GradingEvaluationResponseDifficulty": GradingEvaluationResponseDifficulty;
    "GradingEvaluationRunResponse": GradingEvaluationRunResponse;
    "GradingEvaluationRunListResponse": GradingEvaluationRunListResponse;
    "GradingEvaluationObservationResponse": GradingEvaluationObservationResponse;
    "GradingEvaluationSliceMetricsResponse": GradingEvaluationSliceMetricsResponse;
    "GradingEvaluationResponseDifficultyResponse": GradingEvaluationResponseDifficultyResponse;
    "GradingEvaluationCoveredRate": GradingEvaluationCoveredRate;
    "GradingEvaluationCoveredMean": GradingEvaluationCoveredMean;
    "GradingEvaluationErrorAttribution": GradingEvaluationErrorAttribution;
    "GradingEvaluationQualitySummary": GradingEvaluationQualitySummary;
    "GradingEvaluationQualitySummaryResponse": GradingEvaluationQualitySummaryResponse;
    "InvalidationReasonRequest": InvalidationReasonRequest;
    "ModelCalibrationMethod": ModelCalibrationMethod;
    "ModelCalibrationStatus": ModelCalibrationStatus;
    "ModelCalibrationAxis": ModelCalibrationAxis;
    "ModelCalibrationBin": ModelCalibrationBin;
    "ModelCalibrationRiskCoveragePoint": ModelCalibrationRiskCoveragePoint;
    "ModelCalibrationMetrics": ModelCalibrationMetrics;
    "ModelCalibrationArtifact": ModelCalibrationArtifact;
    "ModelCalibration": ModelCalibration;
    "CreateModelCalibrationRequest": CreateModelCalibrationRequest;
    "AddModelCalibrationEvidenceRequest": AddModelCalibrationEvidenceRequest;
    "ModelCalibrationEvidence": ModelCalibrationEvidence;
    "RecordModelScoreCandidateRequest": RecordModelScoreCandidateRequest;
    "ModelScoreCandidate": ModelScoreCandidate;
    "ModelCalibrationResponse": ModelCalibrationResponse;
    "ModelCalibrationListResponse": ModelCalibrationListResponse;
    "ModelCalibrationEvidenceResponse": ModelCalibrationEvidenceResponse;
    "ModelCalibrationEvidenceListResponse": ModelCalibrationEvidenceListResponse;
    "ModelScoreCandidateResponse": ModelScoreCandidateResponse;
    "AIHumanDisagreementSeverity": AIHumanDisagreementSeverity;
    "AIHumanDisagreementStatus": AIHumanDisagreementStatus;
    "AIHumanDisagreementTaxonomy": AIHumanDisagreementTaxonomy;
    "AIHumanDisagreementDifferenceType": AIHumanDisagreementDifferenceType;
    "AIHumanEvidenceSummary": AIHumanEvidenceSummary;
    "AIHumanDisagreement": AIHumanDisagreement;
    "ClassifyAIHumanDisagreementRequest": ClassifyAIHumanDisagreementRequest;
    "RouteAIHumanDisagreementRequest": RouteAIHumanDisagreementRequest;
    "AIHumanDisagreementResponse": AIHumanDisagreementResponse;
    "AIHumanDisagreementListResponse": AIHumanDisagreementListResponse;
    "AIHumanDisagreementDatasetEntry": AIHumanDisagreementDatasetEntry;
    "AIHumanDisagreementDatasetResponse": AIHumanDisagreementDatasetResponse;
    "ScoreReleaseSource": ScoreReleaseSource;
    "ScoreReleaseStatus": ScoreReleaseStatus;
    "ScoreReleaseAppealWindow": ScoreReleaseAppealWindow;
    "ScoreReleaseVisibilityPolicy": ScoreReleaseVisibilityPolicy;
    "ScoreReleaseGateIssue": ScoreReleaseGateIssue;
    "ScoreReleaseGate": ScoreReleaseGate;
    "ScoreReleaseStudentExplanation": ScoreReleaseStudentExplanation;
    "ScoreReleaseQuestionFact": ScoreReleaseQuestionFact;
    "ScoreRelease": ScoreRelease;
    "ScoreReleaseItem": ScoreReleaseItem;
    "ScoreReleaseQuestion": ScoreReleaseQuestion;
    "ScoreReleaseDetail": ScoreReleaseDetail;
    "ScoreReleaseDiffItem": ScoreReleaseDiffItem;
    "ScoreReleaseDiffQuestion": ScoreReleaseDiffQuestion;
    "ScoreReleaseDiff": ScoreReleaseDiff;
    "CreateScoreReleaseRequest": CreateScoreReleaseRequest;
    "CreateScoreReleaseRollbackRequest": CreateScoreReleaseRollbackRequest;
    "ScoreReleaseResponse": ScoreReleaseResponse;
    "ScoreReleaseListResponse": ScoreReleaseListResponse;
    "ScoreReleaseGateResponse": ScoreReleaseGateResponse;
    "ScoreReleaseDiffResponse": ScoreReleaseDiffResponse;
    "StudentPublishedResult": StudentPublishedResult;
    "StudentPublishedQuestion": StudentPublishedQuestion;
    "StudentScoreAppealWindow": StudentScoreAppealWindow;
    "StudentPublishedResultResponse": StudentPublishedResultResponse;
    "StudentPublishedQuestionResponse": StudentPublishedQuestionResponse;
    "RegradeScoreBand": RegradeScoreBand;
    "RegradeSelector": RegradeSelector;
    "RegradeScoreBandCount": RegradeScoreBandCount;
    "RegradePotentialDelta": RegradePotentialDelta;
    "RegradePreview": RegradePreview;
    "RegradeJob": RegradeJob;
    "RegradeItem": RegradeItem;
    "RegradeWorkItem": RegradeWorkItem;
    "RegradeGraderQuestion": RegradeGraderQuestion;
    "RegradeGraderAnswer": RegradeGraderAnswer;
    "RegradeGraderContext": RegradeGraderContext;
    "RegradeGraderContextResponse": RegradeGraderContextResponse;
    "RegradeEvent": RegradeEvent;
    "RegradeHistogram": RegradeHistogram;
    "RegradeSevereChange": RegradeSevereChange;
    "RegradeReleaseChange": RegradeReleaseChange;
    "RegradeReleasePlan": RegradeReleasePlan;
    "RegradeSummary": RegradeSummary;
    "CreateRegradePreviewRequest": CreateRegradePreviewRequest;
    "CreateRegradeJobRequest": CreateRegradeJobRequest;
    "RecordRegradeCandidateRequest": RecordRegradeCandidateRequest;
    "RegradeRubricSelection": RegradeRubricSelection;
    "ReviewRegradeItemRequest": ReviewRegradeItemRequest;
    "CreateRegradeScoreReleaseRequest": CreateRegradeScoreReleaseRequest;
    "RegradePreviewResponse": RegradePreviewResponse;
    "RegradeSummaryResponse": RegradeSummaryResponse;
    "RegradeJobResponse": RegradeJobResponse;
    "RegradeJobListResponse": RegradeJobListResponse;
    "RegradeItemResponse": RegradeItemResponse;
    "RegradeWorkItemResponse": RegradeWorkItemResponse;
    "RegradeWorkItemListResponse": RegradeWorkItemListResponse;
    "ReleaseGatePolicy": ReleaseGatePolicy;
    "ReleaseGateEvaluation": ReleaseGateEvaluation;
    "ReleaseGateEvidence": ReleaseGateEvidence;
    "ReleaseGateWaiver": ReleaseGateWaiver;
    "CreateReleaseGatePolicyRequest": CreateReleaseGatePolicyRequest;
    "PreviewReleaseGateRequest": PreviewReleaseGateRequest;
    "RequestReleaseGateWaiverRequest": RequestReleaseGateWaiverRequest;
    "DecideReleaseGateWaiverRequest": DecideReleaseGateWaiverRequest;
    "ReleaseGatePolicyResponse": ReleaseGatePolicyResponse;
    "ReleaseGateEvidenceResponse": ReleaseGateEvidenceResponse;
    "ReleaseGateWaiverResponse": ReleaseGateWaiverResponse;
    "StudentPublishedExam": StudentPublishedExam;
    "StudentPublishedExamListResponse": StudentPublishedExamListResponse;
    "PublishedQuestionAppeal": PublishedQuestionAppeal;
    "StudentPublishedQuestionAppeal": StudentPublishedQuestionAppeal;
    "PublishedQuestionAppealEvent": PublishedQuestionAppealEvent;
    "CreatePublishedQuestionAppealRequest": CreatePublishedQuestionAppealRequest;
    "StartQuestionAppealReviewRequest": StartQuestionAppealReviewRequest;
    "DecideQuestionAppealRequest": DecideQuestionAppealRequest;
    "ResolveQuestionAppealRequest": ResolveQuestionAppealRequest;
    "PublishedQuestionAppealResponse": PublishedQuestionAppealResponse;
    "StudentPublishedQuestionAppealResponse": StudentPublishedQuestionAppealResponse;
    "PublishedQuestionAppealListResponse": PublishedQuestionAppealListResponse;
    "StudentPublishedQuestionAppealListResponse": StudentPublishedQuestionAppealListResponse;
    "PublishedQuestionAppealEventsResponse": PublishedQuestionAppealEventsResponse;
    "QuestionAppealReleaseVersion": QuestionAppealReleaseVersion;
    "PublishedQuestionAppealContext": PublishedQuestionAppealContext;
    "PublishedQuestionAppealContextResponse": PublishedQuestionAppealContextResponse;
    "CaptureUploadInitRequest": CaptureUploadInitRequest;
    "CaptureUploadCompleteRequest": CaptureUploadCompleteRequest;
    "CaptureUploadSession": CaptureUploadSession;
    "CaptureUploadInitResponse": CaptureUploadInitResponse;
    "CaptureUploadChunkResponse": CaptureUploadChunkResponse;
    "BackmarkTimeRange": BackmarkTimeRange;
    "GraderQualityWindow": GraderQualityWindow;
    "GraderQualityWindowListResponse": GraderQualityWindowListResponse;
    "RecomputeGraderDriftRequest": RecomputeGraderDriftRequest;
    "GradingQualityIncident": GradingQualityIncident;
    "RecomputeGraderDriftResponse": RecomputeGraderDriftResponse;
    "GradingQualityIncidentListResponse": GradingQualityIncidentListResponse;
    "GradingQualityIncidentResponse": GradingQualityIncidentResponse;
    "BackmarkScoreBand": BackmarkScoreBand;
    "BackmarkSelector": BackmarkSelector;
    "BackmarkPolicy": BackmarkPolicy;
    "BackmarkPreviewRequest": BackmarkPreviewRequest;
    "CreateBackmarkBatchRequest": CreateBackmarkBatchRequest;
    "BackmarkBand": BackmarkBand;
    "BackmarkPreview": BackmarkPreview;
    "BackmarkPreviewResponse": BackmarkPreviewResponse;
    "BackmarkBatch": BackmarkBatch;
    "BackmarkItem": BackmarkItem;
    "BackmarkHistogram": BackmarkHistogram;
    "BackmarkSummary": BackmarkSummary;
    "BackmarkSummaryResponse": BackmarkSummaryResponse;
    "BackmarkRegradeInput": BackmarkRegradeInput;
    "BackmarkBatchListResponse": BackmarkBatchListResponse;
    "BackmarkGraderItem": BackmarkGraderItem;
    "BackmarkGraderItemResponse": BackmarkGraderItemResponse;
    "BackmarkGraderItemListResponse": BackmarkGraderItemListResponse;
    "BackmarkQuestion": BackmarkQuestion;
    "BackmarkAnswer": BackmarkAnswer;
    "BackmarkGraderContext": BackmarkGraderContext;
    "BackmarkGraderContextResponse": BackmarkGraderContextResponse;
    "BackmarkRubricSelection": BackmarkRubricSelection;
    "SubmitBackmarkItemRequest": SubmitBackmarkItemRequest;
    "BackmarkGrade": BackmarkGrade;
    "SubmitBackmarkItemResponse": SubmitBackmarkItemResponse;
    "ProcessingStage": ProcessingStage;
    "ProcessingIssueCode": ProcessingIssueCode;
    "ProcessingExceptionSeverity": ProcessingExceptionSeverity;
    "ProcessingExceptionStatus": ProcessingExceptionStatus;
    "ProcessingStageCount": ProcessingStageCount;
    "ProcessingIssueCount": ProcessingIssueCount;
    "ProcessingSummary": ProcessingSummary;
    "ProcessingSummaryResponse": ProcessingSummaryResponse;
    "ProcessingException": ProcessingException;
    "ProcessingExceptionListResponse": ProcessingExceptionListResponse;
    "AssignProcessingExceptionRequest": AssignProcessingExceptionRequest;
    "ResolveProcessingExceptionRequest": ResolveProcessingExceptionRequest;
    "ProcessingExceptionResponse": ProcessingExceptionResponse;
    "ProcessingWorkerAttempt": ProcessingWorkerAttempt;
    "ProcessingWorkerTask": ProcessingWorkerTask;
    "ExamSessionBlueprintSectionRequest": ExamSessionBlueprintSectionRequest;
    "ExamSessionSubjectRequest": ExamSessionSubjectRequest;
    "CreateExamSessionRequest": CreateExamSessionRequest;
    "ExamSessionChild": ExamSessionChild;
    "ExamSession": ExamSession;
    "ExamSessionResponse": ExamSessionResponse;
    "ExamSessionCommand": ExamSessionCommand;
    "ExamSessionCommandResponse": ExamSessionCommandResponse;
    "PaperImportRole": PaperImportRole;
    "CreatePaperImportSource": CreatePaperImportSource;
    "ReplacePaperImportSource": ReplacePaperImportSource;
    "CreatePaperImportRequest": CreatePaperImportRequest;
    "AddPaperImportSourcesRequest": AddPaperImportSourcesRequest;
    "ReplacePaperImportSourcesRequest": ReplacePaperImportSourcesRequest;
    "PaperImportSourceRef": PaperImportSourceRef;
    "PaperImportSource": PaperImportSource;
    "PaperImportQuestionCandidate": PaperImportQuestionCandidate;
    "PaperImportAnswerCandidate": PaperImportAnswerCandidate;
    "PaperImportSolutionStep": PaperImportSolutionStep;
    "PaperImportSolutionCandidate": PaperImportSolutionCandidate;
    "PaperImportRubricEvidenceRequirement": PaperImportRubricEvidenceRequirement;
    "PaperImportRubricCandidatePoint": PaperImportRubricCandidatePoint;
    "PaperImportRubricCandidate": PaperImportRubricCandidate;
    "PaperImportIssue": PaperImportIssue;
    "PaperImportAnswerKeyInput": PaperImportAnswerKeyInput;
    "PaperImportRubricPoint": PaperImportRubricPoint;
    "PaperImportRubricInput": PaperImportRubricInput;
    "PaperImportSolutionInput": PaperImportSolutionInput;
    "MathRubricDraftRequest": MathRubricDraftRequest;
    "MathRubricDraftPoint": MathRubricDraftPoint;
    "MathRubricDraftSuggestion": MathRubricDraftSuggestion;
    "MathRubricDraftResponse": MathRubricDraftResponse;
    "PaperImportDraftQuestion": PaperImportDraftQuestion;
    "ReviewPaperImportRequest": ReviewPaperImportRequest;
    "PaperImportRuntimeProgress": PaperImportRuntimeProgress;
    "PaperImportJob": PaperImportJob;
    "PaperImportResponse": PaperImportResponse;
    "PaperImportListResponse": PaperImportListResponse;
    "ProcessingRetryResponse": ProcessingRetryResponse;
    "ScoringQuestionSummary": ScoringQuestionSummary;
    "ScoringSummary": ScoringSummary;
    "ScoringSummaryResponse": ScoringSummaryResponse;
    "ScoringReadinessCheck": ScoringReadinessCheck;
    "ScoringReadiness": ScoringReadiness;
    "ScoringReadinessResponse": ScoringReadinessResponse;
    "ScoringRun": ScoringRun;
    "StartScoringRunRequest": StartScoringRunRequest;
    "ScoringRunResponse": ScoringRunResponse;
    "ScoringRunItem": ScoringRunItem;
    "OMRCalibrationSummary": OMRCalibrationSummary;
    "OMRCalibrationSession": OMRCalibrationSession;
    "OMRCalibrationCase": OMRCalibrationCase;
    "OMRCalibrationDetail": OMRCalibrationDetail;
    "CreateOMRCalibrationRequest": CreateOMRCalibrationRequest;
    "OMRCalibrationResponse": OMRCalibrationResponse;
    "ScoringRunDetailResponse": ScoringRunDetailResponse;
    "ScoringRunRetryResponse": ScoringRunRetryResponse;
    "ScoringCommandRecovery": ScoringCommandRecovery;
    "SubjectiveAIModelPolicy": SubjectiveAIModelPolicy;
    "SubjectiveAIGradeRequest": SubjectiveAIGradeRequest;
    "SubjectiveGradePointResult": SubjectiveGradePointResult;
    "SubjectiveGradeEvidence": SubjectiveGradeEvidence;
    "SubjectiveAIGrade": SubjectiveAIGrade;
    "SubjectiveAIGradeResponse": SubjectiveAIGradeResponse;
    "MathEvidenceQuality": MathEvidenceQuality;
    "MathSemanticCandidate": MathSemanticCandidate;
    "MathGradingReviewResponse": MathGradingReviewResponse;
    "SubjectiveGradingRun": SubjectiveGradingRun;
    "SubjectiveGradingBatch": SubjectiveGradingBatch;
    "SubjectiveBatchResponse": SubjectiveBatchResponse;
    "SubjectiveBatchCreateRequest": SubjectiveBatchCreateRequest;
    "SubjectiveBatchCommandRecovery": SubjectiveBatchCommandRecovery;
    "SubjectiveEnqueueCommandRecovery": SubjectiveEnqueueCommandRecovery;
    "ReviewCommandSubmitResult": ReviewCommandSubmitResult;
    "ReviewCommandReviewTask": ReviewCommandReviewTask;
    "ReviewCommandHumanGrade": ReviewCommandHumanGrade;
    "ReviewCommandRubricSelection": ReviewCommandRubricSelection;
    "ReviewCommandDoubleMarkSession": ReviewCommandDoubleMarkSession;
    "ReviewCommandFinalGrade": ReviewCommandFinalGrade;
    "ReviewCommandArbitrationTask": ReviewCommandArbitrationTask;
    "ReviewCommandReviewContext": ReviewCommandReviewContext;
    "ReviewCommandSubmitGradeInput": ReviewCommandSubmitGradeInput;
    "ReviewCommandArbitrationSubmitResult": ReviewCommandArbitrationSubmitResult;
    "ReviewCommandCreateArbitrationTaskInput": ReviewCommandCreateArbitrationTaskInput;
    "ReviewCommandAssignArbitrationInput": ReviewCommandAssignArbitrationInput;
    "ArbitrationTaskResponse": ArbitrationTaskResponse;
    "ReviewCommandSubmitArbitrationInput": ReviewCommandSubmitArbitrationInput;
    "ScoreCommandSubmissionGrade": ScoreCommandSubmissionGrade;
    "ScoreCommandFinalGrade": ScoreCommandFinalGrade;
    "ScoreCommandConfirmInput": ScoreCommandConfirmInput;
    "ScoreCommandPublishResult": ScoreCommandPublishResult;
    "ScoreCommandQualityReport": ScoreCommandQualityReport;
    "ScoreCommandQualityIssue": ScoreCommandQualityIssue;
    "ScoreCommandPublishInput": ScoreCommandPublishInput;
    "BusinessCommandReceipt": BusinessCommandReceipt;
    "QuestionBankMetadata": QuestionBankMetadata;
    "QuestionBankMetadataInput": QuestionBankMetadataInput;
    "QuestionBank": QuestionBank;
    "QuestionBankItem": QuestionBankItem;
    "QuestionBankVersion": QuestionBankVersion;
    "CreateQuestionBankRequest": CreateQuestionBankRequest;
    "UpdateQuestionBankRequest": UpdateQuestionBankRequest;
    "CreateQuestionBankItemRequest": CreateQuestionBankItemRequest;
    "CreateQuestionBankVersionRequest": CreateQuestionBankVersionRequest;
    "UpdateQuestionBankVersionRequest": UpdateQuestionBankVersionRequest;
    "QuestionBankResponse": QuestionBankResponse;
    "QuestionBankItemResponse": QuestionBankItemResponse;
    "QuestionBankItemCreateResponse": QuestionBankItemCreateResponse;
    "QuestionBankVersionResponse": QuestionBankVersionResponse;
    "QuestionBankPage": QuestionBankPage;
    "QuestionBankItemPage": QuestionBankItemPage;
    "QuestionBankVersionPage": QuestionBankVersionPage;
    "QuestionBankAsset": QuestionBankAsset;
    "QuestionBankScoring": QuestionBankScoring;
    "AnswerKeyInput": AnswerKeyInput;
    "QuestionBankSolution": QuestionBankSolution;
    "RubricInput": RubricInput;
    "QuestionBankReviewRequest": QuestionBankReviewRequest;
    "UpdateQuestionBankScoringRequest": UpdateQuestionBankScoringRequest;
    "QuestionBankReviewerBindingRequest": QuestionBankReviewerBindingRequest;
    "QuestionBankReview": QuestionBankReview;
    "QuestionBankReviewsResponse": QuestionBankReviewsResponse;
    "MaterializeQuestionBankRequest": MaterializeQuestionBankRequest;
    "MaterializeQuestionBankResponse": MaterializeQuestionBankResponse;
    "CreateQuestionBankRubricTemplateRequest": CreateQuestionBankRubricTemplateRequest;
    "QuestionBankRubricPoint": QuestionBankRubricPoint;
    "MaterializedBankQuestion": MaterializedBankQuestion;
    "QuestionBankMetadataOption": QuestionBankMetadataOption;
    "QuestionBankTaxonomyTerm": QuestionBankTaxonomyTerm;
    "QuestionBankTaxonomyDefinition": QuestionBankTaxonomyDefinition;
    "QuestionBankMetadataFieldDefinition": QuestionBankMetadataFieldDefinition;
    "QuestionBankMetadataSchema": QuestionBankMetadataSchema;
    "QuestionBankMetadataSchemaResponse": QuestionBankMetadataSchemaResponse;
    "UpdateQuestionBankMetadataSchemaRequest": UpdateQuestionBankMetadataSchemaRequest;
    "ValidateQuestionBankMetadataRequest": ValidateQuestionBankMetadataRequest;
    "QuestionBankMetadataValidationResult": QuestionBankMetadataValidationResult;
    "QuestionBankACLBinding": QuestionBankACLBinding;
    "QuestionBankACLGroup": QuestionBankACLGroup;
    "UpdateQuestionBankACLBinding": UpdateQuestionBankACLBinding;
    "UpdateQuestionBankACLGroup": UpdateQuestionBankACLGroup;
    "QuestionBankACL": QuestionBankACL;
    "QuestionBankACLResponse": QuestionBankACLResponse;
    "UpdateQuestionBankACLRequest": UpdateQuestionBankACLRequest;
    "RetireQuestionBankItemRequest": RetireQuestionBankItemRequest;
    "QuestionBankSearchItem": QuestionBankSearchItem;
    "QuestionBankSearchPage": QuestionBankSearchPage;
    "QuestionBankImportMapping": QuestionBankImportMapping;
    "QuestionBankImportPreviewSelection": QuestionBankImportPreviewSelection;
    "QuestionBankImportPreviewRequest": QuestionBankImportPreviewRequest;
    "QuestionBankImportIssue": QuestionBankImportIssue;
    "QuestionBankImportDuplicateHint": QuestionBankImportDuplicateHint;
    "QuestionBankImportSource": QuestionBankImportSource;
    "QuestionBankImportContent": QuestionBankImportContent;
    "QuestionBankImportPreview": QuestionBankImportPreview;
    "QuestionBankImportPreviewItem": QuestionBankImportPreviewItem;
    "QuestionBankImportPreviewBatch": QuestionBankImportPreviewBatch;
    "QuestionBankImportRequest": QuestionBankImportRequest;
    "QuestionBankImportProvenance": QuestionBankImportProvenance;
    "QuestionBankImportResult": QuestionBankImportResult;
    "QuestionBankBatchImportItem": QuestionBankBatchImportItem;
    "QuestionBankBatchImportRequest": QuestionBankBatchImportRequest;
    "QuestionBankBatchImportResultItem": QuestionBankBatchImportResultItem;
    "QuestionBankBatchImportResult": QuestionBankBatchImportResult;
  };
}
