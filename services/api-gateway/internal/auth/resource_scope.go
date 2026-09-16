package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"github.com/google/uuid"
)

var ErrResourceBoundaryNotFound = errors.New("resource boundary not found")

type ResourceBoundary struct {
	ResourceType string
	ResourceID   string
	TenantID     string
	SchoolID     string
	GradeID      string
	ClassIDs     []string
	ExamID       string
	StudentID    string
	SubmissionID string
	AssignedTo   string
}

type ResourceBoundaryResolver interface {
	ResolveResourceBoundary(ctx context.Context, scope AccessScope, resourceType, resourceID string) (ResourceBoundary, error)
}

func AllowsResourceBoundary(scope AccessScope, boundary ResourceBoundary) bool {
	if strings.TrimSpace(boundary.TenantID) == "" || boundary.TenantID != scope.TenantID {
		return false
	}
	if scope.syntheticUnbounded {
		return true
	}
	// Hidden tasks use the workbench URLs, but their assignment is held in
	// seed_task rather than the ordinary review-task scope list.
	if boundary.ResourceType == "seed_task" {
		return scope.ActorID != "" && boundary.AssignedTo == scope.ActorID
	}
	if scope.IsPlatform || scope.TenantWide {
		return true
	}
	if boundary.ResourceType == "review_task" && scope.AllowsReviewTask(boundary.ResourceID) {
		return boundary.AssignedTo == scope.ActorID
	}
	if boundary.ResourceType == "arbitration_task" && scope.AllowsArbitrationTask(boundary.ResourceID) {
		return boundary.AssignedTo == scope.ActorID
	}
	if (boundary.ResourceType == "review_task" || boundary.ResourceType == "arbitration_task") && !scope.schoolWide {
		return false
	}
	if boundary.SubmissionID != "" && scope.AllowsSubmission(boundary.SubmissionID) {
		return true
	}
	if boundary.ResourceType == "file_asset" && scope.AllowsFile(boundary.ResourceID) {
		return true
	}
	if boundary.ResourceType == "student" && scope.AllowsStudent(boundary.ResourceID) {
		return true
	}
	if boundary.ResourceType == "review_annotation" && boundary.AssignedTo == scope.ActorID && scope.AllowsSubmission(boundary.SubmissionID) {
		return true
	}
	if (boundary.ResourceType == "backmark_item" || boundary.ResourceType == "regrade_item") && boundary.AssignedTo == scope.ActorID {
		return true
	}
	if (boundary.ResourceType == "review_annotation" || boundary.ResourceType == "backmark_item" || boundary.ResourceType == "regrade_item") && !scope.schoolWide {
		return false
	}
	if boundary.StudentID != "" && scope.StudentID != "" && boundary.StudentID == scope.StudentID {
		return true
	}
	if scope.AssignedOnly && !scope.schoolWide && isSubmissionDescendant(boundary.ResourceType) {
		return false
	}
	if boundary.ExamID != "" && scope.AllowsExam(boundary.ExamID) {
		return true
	}
	// Class-scoped roles inherit parent school/grade IDs so the organization
	// tree can be rendered. Those navigation IDs must not become descendant
	// object access; only a real school-scoped role has that authority.
	if boundary.SchoolID != "" && scope.AllowsSchool(boundary.SchoolID) && (scope.schoolWide || boundary.ResourceType == "" || boundary.ResourceType == "school") {
		return true
	}
	if boundary.GradeID != "" && scope.AllowsGrade(boundary.GradeID) && (scope.schoolWide || boundary.ResourceType == "" || boundary.ResourceType == "grade") {
		return true
	}
	for _, classID := range boundary.ClassIDs {
		if scope.AllowsClass(classID) {
			return true
		}
	}
	return false
}

func isSubmissionDescendant(resourceType string) bool {
	switch resourceType {
	case "submission", "submission_page", "answer_segment", "file_asset", "capture_page", "page_registration_run", "page_registration_correction", "review_annotation", "ai_grade", "ocr_task", "math_understanding", "ai_human_disagreement", "backmark_item", "regrade_item":
		return true
	default:
		return false
	}
}

var resourceSegmentTypes = map[string]string{
	"exams": "exam", "questions": "question", "papers": "exam_paper", "paper-imports": "paper_import",
	"answer-sheet-templates": "answer_sheet_template", "answer-sheet-print-batches": "answer_sheet_print_batch",
	"answer-sheet-print-sheets": "answer_sheet_print_sheet", "files": "file_asset", "submissions": "submission",
	"submission-pages": "submission_page", "capture-batches": "capture_batch", "capture-pages": "capture_page",
	"page-registration-runs": "page_registration_run", "page-registration-corrections": "page_registration_correction",
	"answer-segments": "answer_segment", "review-tasks": "review_task", "arbitration-tasks": "arbitration_task",
	"double-mark-sessions": "double_mark_session", "scoring-rules": "scoring_rule", "scoring-runs": "scoring_run",
	"students": "student", "appeals": "appeal", "question-appeals": "question_appeal", "score-releases": "score_release",
	"classes": "school_class", "grades": "grade", "ocr-tasks": "ocr_task", "omr-calibrations": "omr_calibration",
	"ai-grades": "ai_grade", "subjective-grading-batches": "subjective_grading_batch",
	"annotations": "review_annotation", "orchestrations": "orchestration_run", "agent-tasks": "agent_task",
	"answer-groups": "answer_group", "backmark-batches": "backmark_batch", "backmark-items": "backmark_item",
	"calibration-sessions": "grader_calibration_session", "gold-papers": "gold_paper",
	"grading-quality-incidents": "grading_quality_incident", "exceptions": "processing_exception",
	"regrade-jobs": "regrade_job", "regrade-items": "regrade_item", "release-gate-evidence": "release_gate_evidence",
	"release-gate-waivers": "release_gate_waiver", "uploads": "capture_upload", "math-answer-segments": "answer_segment",
	"math-understanding": "math_understanding", "ai-human-disagreements": "ai_human_disagreement",
}

// RequireRequestResourceBoundary applies one uniform object-boundary check to
// every recognized path variable registered by ServeMux. Collection routes and
// worker-internal routes are intentionally not interpreted as object IDs.
func RequireRequestResourceBoundary(resolver ResourceBoundaryResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if resolver == nil || strings.HasPrefix(r.URL.Path, "/api/v1/internal/") {
				next.ServeHTTP(w, r)
				return
			}
			scope, ok := AccessScopeFromContext(r.Context())
			if !ok {
				httpx.Error(w, r, http.StatusForbidden, "access_scope_forbidden", "resource is outside the current data access scope")
				return
			}
			for _, target := range requestBoundaryTargets(r) {
				boundary, err := resolver.ResolveResourceBoundary(r.Context(), scope, target.resourceType, target.resourceID)
				if errors.Is(err, ErrResourceBoundaryNotFound) {
					httpx.Error(w, r, http.StatusNotFound, "resource_not_found", "resource not found")
					return
				}
				if err != nil {
					httpx.Error(w, r, http.StatusInternalServerError, "resource_scope_lookup_failed", "failed to resolve resource data scope")
					return
				}
				if !AllowsResourceBoundary(scope, boundary) {
					httpx.Error(w, r, http.StatusForbidden, "access_scope_forbidden", "resource is outside the current data access scope")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

type requestBoundaryTarget struct{ resourceType, resourceID string }

func requestBoundaryTargets(r *http.Request) []requestBoundaryTarget {
	pattern := r.Pattern
	if fields := strings.Fields(pattern); len(fields) > 1 {
		pattern = fields[len(fields)-1]
	}
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	out := []requestBoundaryTarget{}
	for index, part := range patternParts {
		if index == 0 || !strings.HasPrefix(part, "{") || !strings.HasSuffix(part, "}") {
			continue
		}
		resourceType, recognized := resourceSegmentTypes[patternParts[index-1]]
		if !recognized {
			continue
		}
		param := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
		param = strings.TrimSuffix(param, "...")
		if id := strings.TrimSpace(r.PathValue(param)); id != "" {
			out = append(out, requestBoundaryTarget{resourceType: resourceType, resourceID: id})
		}
	}
	return out
}

func (s *PostgresStore) ResolveResourceBoundary(ctx context.Context, scope AccessScope, resourceType, resourceID string) (ResourceBoundary, error) {
	if _, err := uuid.Parse(strings.TrimSpace(resourceID)); err != nil {
		return ResourceBoundary{}, ErrResourceBoundaryNotFound
	}
	query := resourceBoundaryQuery(resourceType)
	if query == "" {
		return ResourceBoundary{}, ErrResourceBoundaryNotFound
	}
	boundary := ResourceBoundary{ResourceType: resourceType, ResourceID: resourceID}
	args := []any{scope.TenantID, resourceID}
	if strings.Contains(query, "$3") {
		args = append(args, scope.StudentID)
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&boundary.TenantID, &boundary.SchoolID, &boundary.GradeID, pqArray(&boundary.ClassIDs),
		&boundary.ExamID, &boundary.StudentID, &boundary.SubmissionID, &boundary.AssignedTo,
	)
	if errors.Is(err, sql.ErrNoRows) && resourceType == "review_task" {
		boundary.ResourceType = "seed_task"
		err = s.db.QueryRowContext(ctx, `SELECT st.tenant_id::text,e.school_id::text,''::text,
COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),
st.exam_id::text,''::text,''::text,st.grader_id::text
FROM seed_task st JOIN exam e ON e.tenant_id=st.tenant_id AND e.id=st.exam_id AND e.deleted_at IS NULL
LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL
WHERE st.tenant_id=$1::uuid AND st.id=$2::uuid GROUP BY st.id,e.id`, scope.TenantID, resourceID).Scan(
			&boundary.TenantID, &boundary.SchoolID, &boundary.GradeID, pqArray(&boundary.ClassIDs),
			&boundary.ExamID, &boundary.StudentID, &boundary.SubmissionID, &boundary.AssignedTo,
		)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ResourceBoundary{}, ErrResourceBoundaryNotFound
	}
	return boundary, err
}

func resourceBoundaryQuery(resourceType string) string {
	const examProjection = `e.tenant_id::text,COALESCE(e.school_id::text,''),''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),e.id::text,COALESCE((SELECT $3 WHERE $3<>'' AND EXISTS(SELECT 1 FROM student st LEFT JOIN exam_class x ON x.tenant_id=st.tenant_id AND x.class_id=st.class_id AND x.exam_id=e.id WHERE st.tenant_id=e.tenant_id AND st.id::text=$3 AND (x.id IS NOT NULL OR EXISTS(SELECT 1 FROM submission sx WHERE sx.tenant_id=e.tenant_id AND sx.exam_id=e.id AND sx.student_id=st.id AND sx.deleted_at IS NULL)) AND st.deleted_at IS NULL)),''),''::text,''::text`
	switch resourceType {
	case "exam":
		return `SELECT ` + examProjection + ` FROM exam e LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE e.tenant_id=$1::uuid AND e.id=$2::uuid AND e.deleted_at IS NULL GROUP BY e.id`
	case "question":
		return `SELECT ` + examProjection + ` FROM question q JOIN exam e ON e.tenant_id=q.tenant_id AND e.id=q.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE q.tenant_id=$1::uuid AND q.id=$2::uuid AND q.deleted_at IS NULL GROUP BY e.id`
	case "exam_paper":
		return `SELECT ` + examProjection + ` FROM exam_paper p JOIN exam e ON e.tenant_id=p.tenant_id AND e.id=p.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE p.tenant_id=$1::uuid AND p.id=$2::uuid AND p.deleted_at IS NULL GROUP BY e.id`
	case "paper_import":
		return `SELECT ` + examProjection + ` FROM paper_import_job p JOIN exam e ON e.tenant_id=p.tenant_id AND e.id=p.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE p.tenant_id=$1::uuid AND p.id=$2::uuid AND p.deleted_at IS NULL GROUP BY e.id`
	case "answer_sheet_template":
		return `SELECT ` + examProjection + ` FROM answer_sheet_template t JOIN exam e ON e.tenant_id=t.tenant_id AND e.id=t.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE t.tenant_id=$1::uuid AND t.id=$2::uuid AND t.deleted_at IS NULL GROUP BY e.id`
	case "answer_sheet_print_batch":
		return `SELECT ` + examProjection + ` FROM answer_sheet_print_batch b JOIN exam e ON e.tenant_id=b.tenant_id AND e.id=b.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE b.tenant_id=$1::uuid AND b.id=$2::uuid GROUP BY e.id`
	case "answer_sheet_print_sheet":
		return `SELECT e.tenant_id::text,COALESCE(e.school_id::text,''),''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),e.id::text,s.student_id::text,''::text,''::text FROM answer_sheet_print_sheet s JOIN exam e ON e.tenant_id=s.tenant_id AND e.id=s.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE s.tenant_id=$1::uuid AND s.id=$2::uuid GROUP BY e.id,s.student_id`
	case "file_asset":
		return `SELECT f.tenant_id::text,COALESCE(f.school_id::text,e.school_id::text,''),''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),COALESCE(e.id::text,''),COALESCE(sub.student_id::text,''),COALESCE(sub.id::text,''),''::text FROM file_asset f LEFT JOIN submission sub ON sub.tenant_id=f.tenant_id AND sub.id=f.submission_id LEFT JOIN exam e ON e.tenant_id=f.tenant_id AND e.id=COALESCE(f.exam_id,sub.exam_id) LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE f.tenant_id=$1::uuid AND f.id=$2::uuid AND f.deleted_at IS NULL GROUP BY f.id,e.id,sub.id`
	case "submission":
		return submissionBoundaryQuery(`s.id=$2::uuid`)
	case "submission_page":
		return submissionBoundaryQuery(`EXISTS(SELECT 1 FROM submission_page sp WHERE sp.tenant_id=s.tenant_id AND sp.submission_id=s.id AND sp.id=$2::uuid AND sp.deleted_at IS NULL)`)
	case "answer_segment":
		return submissionBoundaryQuery(`EXISTS(SELECT 1 FROM answer_segment seg WHERE seg.tenant_id=s.tenant_id AND seg.submission_id=s.id AND seg.id=$2::uuid AND seg.deleted_at IS NULL)`)
	case "capture_batch":
		return `SELECT ` + examProjection + ` FROM capture_batch b JOIN exam e ON e.tenant_id=b.tenant_id AND e.id=b.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE b.tenant_id=$1::uuid AND b.id=$2::uuid AND b.deleted_at IS NULL GROUP BY e.id`
	case "capture_page":
		return capturePageBoundaryQuery(`cp.id=$2::uuid`)
	case "page_registration_run":
		return capturePageBoundaryQuery(`EXISTS(SELECT 1 FROM page_registration_run pr WHERE pr.tenant_id=cp.tenant_id AND pr.capture_page_id=cp.id AND pr.id=$2::uuid AND pr.deleted_at IS NULL)`)
	case "page_registration_correction":
		return capturePageBoundaryQuery(`EXISTS(SELECT 1 FROM page_registration_correction pc WHERE pc.tenant_id=cp.tenant_id AND pc.capture_page_id=cp.id AND pc.id=$2::uuid AND pc.deleted_at IS NULL)`)
	case "review_task":
		return taskBoundaryQuery("review_task")
	case "arbitration_task":
		return taskBoundaryQuery("arbitration_task")
	case "double_mark_session":
		return examOwnedBoundaryQuery("double_mark_session")
	case "scoring_rule":
		return examOwnedBoundaryQuery("scoring_rule")
	case "scoring_run":
		return examOwnedBoundaryQuery("scoring_run")
	case "student":
		return `SELECT st.tenant_id::text,st.school_id::text,''::text,ARRAY[COALESCE(st.class_id::text,'')],''::text,st.id::text,''::text,''::text FROM student st WHERE st.tenant_id=$1::uuid AND st.id=$2::uuid AND st.deleted_at IS NULL`
	case "school_class":
		return `SELECT c.tenant_id::text,c.school_id::text,c.grade_id::text,ARRAY[c.id::text],''::text,''::text,''::text,''::text FROM school_class c WHERE c.tenant_id=$1::uuid AND c.id=$2::uuid AND c.deleted_at IS NULL`
	case "grade":
		return `SELECT g.tenant_id::text,g.school_id::text,g.id::text,'{}'::text[],''::text,''::text,''::text,''::text FROM grade g WHERE g.tenant_id=$1::uuid AND g.id=$2::uuid AND g.deleted_at IS NULL`
	case "appeal":
		return `SELECT a.tenant_id::text,e.school_id::text,''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),a.exam_id::text,a.student_id::text,a.submission_id::text,COALESCE(a.assigned_to::text,'') FROM appeal a JOIN exam e ON e.tenant_id=a.tenant_id AND e.id=a.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE a.tenant_id=$1::uuid AND a.id=$2::uuid AND a.deleted_at IS NULL GROUP BY a.id,e.id`
	case "question_appeal":
		return `SELECT qa.tenant_id::text,e.school_id::text,''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),qa.exam_id::text,qa.student_id::text,qa.submission_id::text,''::text FROM question_appeal qa JOIN exam e ON e.tenant_id=qa.tenant_id AND e.id=qa.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE qa.tenant_id=$1::uuid AND qa.id=$2::uuid AND qa.deleted_at IS NULL GROUP BY qa.id,e.id`
	case "score_release":
		return examOwnedBoundaryQueryWithoutSoftDelete("score_release")
	case "ocr_task":
		return submissionBoundaryQuery(`EXISTS(SELECT 1 FROM ocr_task ot WHERE ot.tenant_id=s.tenant_id AND ot.submission_id=s.id AND ot.id=$2::uuid AND ot.deleted_at IS NULL)`)
	case "omr_calibration":
		return `SELECT ` + examProjection + ` FROM omr_calibration_session c JOIN answer_sheet_template t ON t.tenant_id=c.tenant_id AND t.id=c.template_id JOIN exam e ON e.tenant_id=t.tenant_id AND e.id=t.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE c.tenant_id=$1::uuid AND c.id=$2::uuid AND c.deleted_at IS NULL GROUP BY e.id`
	case "ai_grade":
		return submissionBoundaryQuery(`EXISTS(SELECT 1 FROM ai_grade ag JOIN answer_segment aseg ON aseg.tenant_id=ag.tenant_id AND aseg.id=ag.answer_segment_id AND aseg.deleted_at IS NULL WHERE ag.tenant_id=s.tenant_id AND aseg.submission_id=s.id AND ag.id=$2::uuid AND ag.deleted_at IS NULL)`)
	case "subjective_grading_batch":
		return `SELECT MIN(b.tenant_id::text),MIN(e.school_id::text),''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),MIN(e.id::text),''::text,''::text,''::text FROM subjective_grading_batch b JOIN LATERAL jsonb_array_elements_text(b.segment_ids) ids(id) ON true JOIN answer_segment seg ON seg.tenant_id=b.tenant_id AND seg.id::text=ids.id AND seg.deleted_at IS NULL JOIN submission s ON s.tenant_id=seg.tenant_id AND s.id=seg.submission_id AND s.deleted_at IS NULL JOIN exam e ON e.tenant_id=s.tenant_id AND e.id=s.exam_id AND e.deleted_at IS NULL LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE b.tenant_id=$1::uuid AND b.id=$2::uuid AND b.deleted_at IS NULL GROUP BY b.id HAVING COUNT(DISTINCT e.id)=1`
	case "review_annotation":
		return `SELECT t.tenant_id::text,e.school_id::text,''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),t.exam_id::text,COALESCE(s.student_id::text,''),t.submission_id::text,COALESCE(t.assigned_to::text,'') FROM review_annotation a JOIN review_task t ON t.tenant_id=a.tenant_id AND t.id=a.review_task_id JOIN exam e ON e.tenant_id=t.tenant_id AND e.id=t.exam_id LEFT JOIN submission s ON s.tenant_id=t.tenant_id AND s.id=t.submission_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE a.tenant_id=$1::uuid AND a.id=$2::uuid AND a.deleted_at IS NULL AND t.deleted_at IS NULL GROUP BY a.id,t.id,e.id,s.id`
	case "orchestration_run":
		return orchestrationBoundaryQuery(`o.id=$2::uuid`)
	case "agent_task":
		return orchestrationBoundaryQuery(`EXISTS(SELECT 1 FROM agent_task at WHERE at.tenant_id=o.tenant_id AND at.orchestration_run_id=o.id AND at.id=$2::uuid AND at.deleted_at IS NULL)`)
	case "answer_group":
		return examOwnedBoundaryQueryWithoutSoftDelete("answer_group")
	case "backmark_batch":
		return examOwnedBoundaryQueryWithoutSoftDelete("backmark_batch")
	case "backmark_item":
		return `SELECT i.tenant_id::text,e.school_id::text,''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),b.exam_id::text,COALESCE(s.student_id::text,''),COALESCE(t.submission_id::text,''),i.reassigned_to::text FROM backmark_item i JOIN backmark_batch b ON b.tenant_id=i.tenant_id AND b.id=i.batch_id JOIN review_task t ON t.tenant_id=i.tenant_id AND t.id=i.review_task_id LEFT JOIN submission s ON s.tenant_id=t.tenant_id AND s.id=t.submission_id JOIN exam e ON e.tenant_id=b.tenant_id AND e.id=b.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE i.tenant_id=$1::uuid AND i.id=$2::uuid GROUP BY i.id,b.id,t.id,s.id,e.id`
	case "grader_calibration_session":
		return examOwnedBoundaryQueryWithoutSoftDelete("grader_calibration_session")
	case "gold_paper":
		return examOwnedBoundaryQueryWithoutSoftDelete("grading_gold_paper")
	case "grading_quality_incident":
		return examOwnedBoundaryQueryWithoutSoftDelete("grading_quality_incident")
	case "processing_exception":
		return examOwnedBoundaryQueryWithoutSoftDelete("operational_exception")
	case "regrade_job":
		return examOwnedBoundaryQueryWithoutSoftDelete("regrade_job")
	case "regrade_item":
		return `SELECT i.tenant_id::text,e.school_id::text,''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),j.exam_id::text,COALESCE(s.student_id::text,''),i.submission_id::text,COALESCE(i.assigned_to::text,'') FROM regrade_item i JOIN regrade_job j ON j.tenant_id=i.tenant_id AND j.id=i.job_id JOIN submission s ON s.tenant_id=i.tenant_id AND s.id=i.submission_id JOIN exam e ON e.tenant_id=j.tenant_id AND e.id=j.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE i.tenant_id=$1::uuid AND i.id=$2::uuid GROUP BY i.id,j.id,s.id,e.id`
	case "release_gate_evidence":
		return examOwnedBoundaryQueryWithoutSoftDelete("release_gate_evidence")
	case "release_gate_waiver":
		return examOwnedBoundaryQueryWithoutSoftDelete("release_gate_waiver_request")
	case "capture_upload":
		return examOwnedBoundaryQueryWithoutSoftDelete("capture_upload_session")
	case "math_understanding":
		return submissionBoundaryQuery(`EXISTS(SELECT 1 FROM math_understanding_artifact ma JOIN answer_segment aseg ON aseg.tenant_id=ma.tenant_id AND aseg.id=ma.answer_segment_id AND aseg.deleted_at IS NULL WHERE ma.tenant_id=s.tenant_id AND aseg.submission_id=s.id AND ma.id=$2::uuid)`)
	case "ai_human_disagreement":
		return submissionBoundaryQuery(`EXISTS(SELECT 1 FROM ai_human_disagreement d WHERE d.tenant_id=s.tenant_id AND d.submission_id=s.id AND d.id=$2::uuid)`)
	default:
		return ""
	}
}

func orchestrationBoundaryQuery(predicate string) string {
	return `WITH target AS (
SELECT o.id,o.tenant_id,
  CASE WHEN o.target_type='exam' THEN o.target_id ELSE s.exam_id END AS exam_id,
  s.id AS submission_id,s.student_id
FROM orchestration_run o
LEFT JOIN ocr_task ot ON o.target_type='ocr_task' AND ot.tenant_id=o.tenant_id AND ot.id=o.target_id AND ot.deleted_at IS NULL
LEFT JOIN answer_segment seg ON o.target_type='answer_segment' AND seg.tenant_id=o.tenant_id AND seg.id=o.target_id AND seg.deleted_at IS NULL
LEFT JOIN submission s ON s.tenant_id=o.tenant_id AND s.id=CASE
  WHEN o.target_type='submission' THEN o.target_id
  WHEN o.target_type='answer_segment' THEN seg.submission_id
  WHEN o.target_type='ocr_task' THEN ot.submission_id END AND s.deleted_at IS NULL
WHERE o.tenant_id=$1::uuid AND ` + predicate + ` AND o.deleted_at IS NULL
)
SELECT target.tenant_id::text,e.school_id::text,''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),e.id::text,COALESCE(target.student_id::text,''),COALESCE(target.submission_id::text,''),''::text
FROM target JOIN exam e ON e.tenant_id=target.tenant_id AND e.id=target.exam_id AND e.deleted_at IS NULL
LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL
GROUP BY target.id,target.tenant_id,target.student_id,target.submission_id,e.id`
}

func submissionBoundaryQuery(predicate string) string {
	return `SELECT s.tenant_id::text,e.school_id::text,''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),s.exam_id::text,COALESCE(s.student_id::text,''),s.id::text,''::text FROM submission s JOIN exam e ON e.tenant_id=s.tenant_id AND e.id=s.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE s.tenant_id=$1::uuid AND ` + predicate + ` AND s.deleted_at IS NULL GROUP BY s.id,e.id`
}

func capturePageBoundaryQuery(predicate string) string {
	return `SELECT cp.tenant_id::text,e.school_id::text,''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),e.id::text,COALESCE(s.student_id::text,''),COALESCE(s.id::text,''),''::text FROM capture_page cp JOIN capture_batch b ON b.tenant_id=cp.tenant_id AND b.id=cp.capture_batch_id JOIN exam e ON e.tenant_id=b.tenant_id AND e.id=b.exam_id LEFT JOIN submission s ON s.tenant_id=cp.tenant_id AND s.id=cp.submission_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE cp.tenant_id=$1::uuid AND ` + predicate + ` AND cp.deleted_at IS NULL GROUP BY cp.id,e.id,s.id`
}

func taskBoundaryQuery(table string) string {
	return `SELECT t.tenant_id::text,e.school_id::text,''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),t.exam_id::text,COALESCE(s.student_id::text,''),t.submission_id::text,COALESCE(t.assigned_to::text,'') FROM ` + table + ` t JOIN exam e ON e.tenant_id=t.tenant_id AND e.id=t.exam_id LEFT JOIN submission s ON s.tenant_id=t.tenant_id AND s.id=t.submission_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE t.tenant_id=$1::uuid AND t.id=$2::uuid AND t.deleted_at IS NULL GROUP BY t.id,e.id,s.id`
}

func examOwnedBoundaryQuery(table string) string {
	return `SELECT t.tenant_id::text,e.school_id::text,''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),t.exam_id::text,''::text,''::text,''::text FROM ` + table + ` t JOIN exam e ON e.tenant_id=t.tenant_id AND e.id=t.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE t.tenant_id=$1::uuid AND t.id=$2::uuid AND t.deleted_at IS NULL GROUP BY t.id,e.id`
}

func examOwnedBoundaryQueryWithoutSoftDelete(table string) string {
	return `SELECT t.tenant_id::text,e.school_id::text,''::text,COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER(WHERE ec.class_id IS NOT NULL),'{}'),t.exam_id::text,''::text,''::text,''::text FROM ` + table + ` t JOIN exam e ON e.tenant_id=t.tenant_id AND e.id=t.exam_id LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL WHERE t.tenant_id=$1::uuid AND t.id=$2::uuid GROUP BY t.id,e.id`
}
