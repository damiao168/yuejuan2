package server

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"
)

// 先形成投影并派发人工异常，重复相同投影不能覆盖人工派发。
func e2eCreateAssignedProjectionSource(t *testing.T, scenario reliabilityCommandScenario) (*processing.PostgresStore, string, string) {
	t.Helper()
	db, tenantID, userID, first, fileID := scenario.DB, scenario.TenantID, scenario.UserID, scenario.First, scenario.FileID
	var err error
	submissionID, pageID := uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(`INSERT INTO submission(id,tenant_id,exam_id,candidate_no,source_type,status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4,'pdf_upload','pages_uploaded',$5::uuid)`, submissionID, tenantID, first.Exams[0].ID, "projection-"+uuid.NewString(), userID); err != nil {
		t.Fatalf("seed projection submission: %v", err)
	}
	if _, err = db.Exec(`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, pageID, tenantID, submissionID, fileID); err != nil {
		t.Fatalf("seed projection page: %v", err)
	}
	projectionStore := processing.NewPostgresStore(db)
	if err = projectionStore.RefreshExam(context.Background(), tenantID, first.Exams[0].ID); err != nil {
		t.Fatalf("initial projection: %v", err)
	}
	summary, err := projectionStore.Summary(context.Background(), tenantID, first.Exams[0].ID)
	if err != nil || summary.TotalPages != 1 {
		t.Fatalf("initial summary=%#v err=%v", summary, err)
	}
	var exceptionID string
	if err = db.QueryRow(`SELECT id::text FROM operational_exception WHERE tenant_id=$1::uuid AND page_id=$2::uuid AND status='open'`, tenantID, pageID).Scan(&exceptionID); err != nil {
		t.Fatalf("load projected exception: %v", err)
	}
	if _, err = projectionStore.AssignException(context.Background(), tenantID, exceptionID, userID, processing.AssignInput{AssigneeID: userID}); err != nil {
		t.Fatalf("assign projected exception: %v", err)
	}
	if err = projectionStore.RefreshExam(context.Background(), tenantID, first.Exams[0].ID); err != nil {
		t.Fatalf("repeat unchanged projection: %v", err)
	}
	assigned, err := projectionStore.GetException(context.Background(), tenantID, exceptionID)
	if err != nil || assigned.Status != "assigned" || assigned.AssignedTo != userID {
		t.Fatalf("unchanged refresh lost manual assignment: %#v err=%v", assigned, err)
	}
	return projectionStore, pageID, exceptionID
}

// 页删除使有效页数为零，清理当前状态并将已派发异常以来源删除原因关闭。
func e2eDeleteAssignedProjectionSource(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, tenantID, first, projectionStore, pageID, exceptionID := scenario.DB, scenario.TenantID, scenario.First, scenario.Projection, scenario.PageID, scenario.ExceptionID
	var err error
	if _, err = db.Exec(`UPDATE submission_page SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, pageID); err != nil {
		t.Fatal(err)
	}
	if err = projectionStore.RefreshExam(context.Background(), tenantID, first.Exams[0].ID); err != nil {
		t.Fatalf("projection after delete: %v", err)
	}
	summary, err := projectionStore.Summary(context.Background(), tenantID, first.Exams[0].ID)
	if err != nil || summary.TotalPages != 0 {
		t.Fatalf("deleted page remains in current summary=%#v err=%v", summary, err)
	}
	var staleState, unresolvedException int
	if err = db.QueryRow(`SELECT count(*),(SELECT count(*) FROM operational_exception WHERE tenant_id=$1::uuid AND page_id=$2::uuid AND status IN ('open','assigned')) FROM submission_page_processing_state WHERE tenant_id=$1::uuid AND page_id=$2::uuid`, tenantID, pageID).Scan(&staleState, &unresolvedException); err != nil {
		t.Fatal(err)
	}
	if staleState != 0 || unresolvedException != 0 {
		t.Fatalf("invalid projection facts remain: states=%d exceptions=%d", staleState, unresolvedException)
	}
	closed, err := projectionStore.GetException(context.Background(), tenantID, exceptionID)
	if err != nil || closed.Status != "resolved" || closed.Resolution != "source_deleted" {
		t.Fatalf("deleted source did not close assigned exception: %#v err=%v", closed, err)
	}

}

// 重复时间戳变化仍保留明确人工处理，删除提交之后再更新投影。
func e2eAssertManualProjectionResolutionPreserved(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, tenantID, userID, first, fileID, projectionStore := scenario.DB, scenario.TenantID, scenario.UserID, scenario.First, scenario.FileID, scenario.Projection
	var err error
	resolvedSubmissionID, resolvedPageID := uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(`INSERT INTO submission(id,tenant_id,exam_id,candidate_no,source_type,status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4,'pdf_upload','pages_uploaded',$5::uuid)`, resolvedSubmissionID, tenantID, first.Exams[0].ID, "projection-resolved-"+uuid.NewString(), userID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, resolvedPageID, tenantID, resolvedSubmissionID, fileID); err != nil {
		t.Fatal(err)
	}
	if err = projectionStore.RefreshExam(context.Background(), tenantID, first.Exams[0].ID); err != nil {
		t.Fatal(err)
	}
	var resolvedExceptionID string
	if err = db.QueryRow(`SELECT id::text FROM operational_exception WHERE tenant_id=$1::uuid AND page_id=$2::uuid AND status='open'`, tenantID, resolvedPageID).Scan(&resolvedExceptionID); err != nil {
		t.Fatal(err)
	}
	if _, err = projectionStore.ResolveException(context.Background(), tenantID, resolvedExceptionID, userID, processing.ResolveInput{Resolution: "operator_confirmed"}); err != nil {
		t.Fatal(err)
	}
	e2eRefreshResolvedProjectionHistory(t, scenario, resolvedPageID, resolvedExceptionID)
	if _, err = db.Exec(`UPDATE submission SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, resolvedSubmissionID); err != nil {
		t.Fatal(err)
	}
	if err = projectionStore.RefreshExam(context.Background(), tenantID, first.Exams[0].ID); err != nil {
		t.Fatal(err)
	}

}

// 两次更新时间戳并刷新仍保留 operator_confirmed 终态，不把人工处理覆盖成开放异常。
func e2eRefreshResolvedProjectionHistory(t *testing.T, scenario reliabilityCommandScenario, resolvedPageID, resolvedExceptionID string) {
	t.Helper()
	db, tenantID, first, projectionStore := scenario.DB, scenario.TenantID, scenario.First, scenario.Projection
	var err error
	for range 2 {
		if _, err := db.Exec(`UPDATE submission_page SET updated_at=clock_timestamp() WHERE id=$1::uuid`, resolvedPageID); err != nil {
			t.Fatal(err)
		}
		if err = projectionStore.RefreshExam(context.Background(), tenantID, first.Exams[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	resolvedException, err := projectionStore.GetException(context.Background(), tenantID, resolvedExceptionID)
	if err != nil || resolvedException.Status != "resolved" || resolvedException.Resolution != "operator_confirmed" {
		t.Fatalf("unchanged refresh lost manual resolution: %#v err=%v", resolvedException, err)
	}
}

// 来源转移后先刷新目标考试再刷新原考试，不能把旧异常藏在新归属之后。
func e2eMoveProjectionSourceToDestination(t *testing.T, scenario reliabilityCommandScenario) string {
	t.Helper()
	db, tenantID, userID, first, separate, fileID, projectionStore := scenario.DB, scenario.TenantID, scenario.UserID, scenario.First, scenario.Separate, scenario.FileID, scenario.Projection
	var err error
	movedSubmissionID, movedPageID := uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(`INSERT INTO submission(id,tenant_id,exam_id,candidate_no,source_type,status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4,'pdf_upload','pages_uploaded',$5::uuid)`, movedSubmissionID, tenantID, first.Exams[0].ID, "projection-move-"+uuid.NewString(), userID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, movedPageID, tenantID, movedSubmissionID, fileID); err != nil {
		t.Fatal(err)
	}
	if err = projectionStore.RefreshExam(context.Background(), tenantID, first.Exams[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE submission SET exam_id=$3::uuid,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, movedSubmissionID, separate.Exams[0].ID); err != nil {
		t.Fatal(err)
	}
	// 先处理目标方不能把旧异常隐藏在页面状态的新考试归属之后。
	if err = projectionStore.RefreshExam(context.Background(), tenantID, separate.Exams[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = projectionStore.RefreshExam(context.Background(), tenantID, first.Exams[0].ID); err != nil {
		t.Fatal(err)
	}
	return movedPageID
}

// 转移保留原异常关闭历史，目标新异常及两侧有效页统计逐项核对。
func e2eAssertDestinationFirstProjectionFacts(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, tenantID, first, separate, projectionStore, movedPageID := scenario.DB, scenario.TenantID, scenario.First, scenario.Separate, scenario.Projection, scenario.MovedPageID
	var oldOpen, newOpen, oldClosed int
	if err := db.QueryRow(`SELECT count(*) FILTER(WHERE exam_id=$3::uuid AND status IN ('open','assigned')),count(*) FILTER(WHERE exam_id=$4::uuid AND status='open'),count(*) FILTER(WHERE exam_id=$3::uuid AND status='resolved' AND resolution='source_reassigned') FROM operational_exception WHERE tenant_id=$1::uuid AND page_id=$2::uuid`, tenantID, movedPageID, first.Exams[0].ID, separate.Exams[0].ID).Scan(&oldOpen, &newOpen, &oldClosed); err != nil {
		t.Fatal(err)
	}
	if oldOpen != 0 || newOpen != 1 || oldClosed != 1 {
		t.Fatalf("destination-first lifecycle: old open=%d new open=%d old history=%d", oldOpen, newOpen, oldClosed)
	}
	oldSummary, oldErr := projectionStore.Summary(context.Background(), tenantID, first.Exams[0].ID)
	newSummary, newErr := projectionStore.Summary(context.Background(), tenantID, separate.Exams[0].ID)
	if oldErr != nil || newErr != nil || oldSummary.TotalPages != 0 || newSummary.TotalPages != 1 {
		t.Fatalf("moved page projection old=%#v/%v new=%#v/%v", oldSummary, oldErr, newSummary, newErr)
	}
}

// 同考试内移动页面的提交归属只保留一个投影页。
func e2eRegroupProjectionSource(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, tenantID, userID, separate, projectionStore, movedPageID := scenario.DB, scenario.TenantID, scenario.UserID, scenario.Separate, scenario.Projection, scenario.MovedPageID
	var err error
	regroupedSubmissionID := uuid.NewString()
	if _, err = db.Exec(`INSERT INTO submission(id,tenant_id,exam_id,candidate_no,source_type,status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4,'pdf_upload','pages_uploaded',$5::uuid)`, regroupedSubmissionID, tenantID, separate.Exams[0].ID, "projection-regroup-"+uuid.NewString(), userID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE submission_page SET submission_id=$3::uuid,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, movedPageID, regroupedSubmissionID); err != nil {
		t.Fatal(err)
	}
	if err = projectionStore.RefreshExam(context.Background(), tenantID, separate.Exams[0].ID); err != nil {
		t.Fatal(err)
	}
	regroupedSummary, regroupedErr := projectionStore.Summary(context.Background(), tenantID, separate.Exams[0].ID)
	if regroupedErr != nil || regroupedSummary.TotalPages != 1 {
		t.Fatalf("page regroup duplicated projection: %#v err=%v", regroupedSummary, regroupedErr)
	}
}

// 整份提交删除后不能保留当前页面状态，另一有效页统计保持一份。
func e2eDeleteProjectionSubmission(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, tenantID, userID, separate, fileID, projectionStore := scenario.DB, scenario.TenantID, scenario.UserID, scenario.Separate, scenario.FileID, scenario.Projection
	var err error
	deletedSubmissionID, deletedSubmissionPageID := uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(`INSERT INTO submission(id,tenant_id,exam_id,candidate_no,source_type,status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4,'pdf_upload','pages_uploaded',$5::uuid)`, deletedSubmissionID, tenantID, separate.Exams[0].ID, "projection-submission-delete-"+uuid.NewString(), userID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, deletedSubmissionPageID, tenantID, deletedSubmissionID, fileID); err != nil {
		t.Fatal(err)
	}
	if err = projectionStore.RefreshExam(context.Background(), tenantID, separate.Exams[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE submission SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, deletedSubmissionID); err != nil {
		t.Fatal(err)
	}
	if err = projectionStore.RefreshExam(context.Background(), tenantID, separate.Exams[0].ID); err != nil {
		t.Fatal(err)
	}
	deletedSubmissionSummary, deletedSubmissionErr := projectionStore.Summary(context.Background(), tenantID, separate.Exams[0].ID)
	var deletedSubmissionState int
	if err = db.QueryRow(`SELECT count(*) FROM submission_page_processing_state WHERE tenant_id=$1::uuid AND page_id=$2::uuid`, tenantID, deletedSubmissionPageID).Scan(&deletedSubmissionState); err != nil {
		t.Fatal(err)
	}
	if deletedSubmissionErr != nil || deletedSubmissionSummary.TotalPages != 1 || deletedSubmissionState != 0 {
		t.Fatalf("deleted submission remains projected: summary=%#v state=%d err=%v", deletedSubmissionSummary, deletedSubmissionState, deletedSubmissionErr)
	}

}

// 投影器只确认所领取版本，新到达请求保留待处理，其他持有者不能发布。
func e2eAssertProjectionAcknowledgesClaimedVersion(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, tenantID, first, projectionStore := scenario.DB, scenario.TenantID, scenario.First, scenario.Projection
	var err error
	// 已领取的投影器只确认领取的版本。领取后到达的请求仍保持待处理，
	// 无关持有者不能发布或确认已租用工作。
	if _, err = db.Exec(`UPDATE processing_projection_cursor SET projected_version=requested_version,lease_owner=NULL,lease_expires_at=NULL,available_at=now()`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`SELECT request_processing_projection_refresh($1::uuid,$2::uuid)`, tenantID, first.Exams[0].ID); err != nil {
		t.Fatal(err)
	}
	claimedProjection, ok, err := projectionStore.ClaimProjection(context.Background(), "projector-a", 5*time.Minute)
	if err != nil || !ok || claimedProjection.ExamID != first.Exams[0].ID {
		t.Fatalf("claim projection=%#v ok=%v err=%v", claimedProjection, ok, err)
	}
	if _, err = db.Exec(`SELECT request_processing_projection_refresh($1::uuid,$2::uuid)`, tenantID, first.Exams[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = projectionStore.ApplyProjection(context.Background(), "projector-b", claimedProjection); !errors.Is(err, processing.ErrProjectionLeaseLost) {
		t.Fatalf("foreign projector lease error=%v", err)
	}
	if err = projectionStore.ApplyProjection(context.Background(), "projector-a", claimedProjection); err != nil {
		t.Fatalf("apply claimed projection: %v", err)
	}
	var requestedVersion, projectedVersion int64
	if err = db.QueryRow(`SELECT requested_version,projected_version FROM processing_projection_cursor WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, tenantID, first.Exams[0].ID).Scan(&requestedVersion, &projectedVersion); err != nil {
		t.Fatal(err)
	}
	if projectedVersion != claimedProjection.RequestedVersion || requestedVersion <= projectedVersion {
		t.Fatalf("new projection request was incorrectly acknowledged: requested=%d projected=%d claimed=%d", requestedVersion, projectedVersion, claimedProjection.RequestedVersion)
	}
	nextProjection, ok, err := projectionStore.ClaimProjection(context.Background(), "projector-b", 5*time.Minute)
	if err != nil || !ok || nextProjection.ExamID != first.Exams[0].ID {
		t.Fatalf("reclaim pending projection=%#v ok=%v err=%v", nextProjection, ok, err)
	}
	if err = projectionStore.ApplyProjection(context.Background(), "projector-b", nextProjection); err != nil {
		t.Fatalf("apply pending projection: %v", err)
	}
}
