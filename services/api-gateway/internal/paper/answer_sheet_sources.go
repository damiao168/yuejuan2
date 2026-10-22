package paper

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func (h *Handler) DeleteAnswerSheetSource(w http.ResponseWriter, r *http.Request) {
	repository, ok := h.papers.(AnswerSheetSourceRepository)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "answer_sheet_source_delete_unavailable", "answer sheet source deletion is unavailable")
		return
	}
	user := mustUser(r)
	result, err := repository.DeleteAnswerSheetSource(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("id"))
	if errors.Is(err, ErrAnswerSheetSourceInUse) {
		httpx.Error(w, r, http.StatusConflict, "answer_sheet_source_in_use", "答题卡已锁定、收录或用于正式处理，不能删除")
		return
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.answer_sheet_source_deleted", "exam_paper", result.PaperID, "remove unused answer sheet source and its draft templates")
	httpx.JSON(w, http.StatusOK, result)
}

func (s *MemoryStore) DeleteAnswerSheetSource(_ context.Context, tenantID, examID, id string) (AnswerSheetSourceDeletion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, found := s.papers[id]
	if !found || item.TenantID != tenantID || item.ExamID != examID || item.Purpose != PaperPurposeAnswerSheet {
		return AnswerSheetSourceDeletion{}, ErrNotFound
	}
	if err := s.ensureExamPaperMutableLocked(examID); err != nil {
		return AnswerSheetSourceDeletion{}, err
	}
	ids := []string{}
	for _, template := range s.templates {
		if template.TenantID != tenantID || template.ExamPaperID != id {
			continue
		}
		if template.Status != "draft" || template.LockedAt != nil {
			return AnswerSheetSourceDeletion{}, ErrAnswerSheetSourceInUse
		}
		for _, binding := range s.bindings {
			if binding.TenantID == tenantID && binding.TemplateID == template.ID {
				return AnswerSheetSourceDeletion{}, ErrAnswerSheetSourceInUse
			}
		}
		for _, source := range s.librarySources {
			if source.TemplateID == template.ID {
				return AnswerSheetSourceDeletion{}, ErrAnswerSheetSourceInUse
			}
		}
		for _, templateID := range s.libraryInstances {
			if templateID == template.ID {
				return AnswerSheetSourceDeletion{}, ErrAnswerSheetSourceInUse
			}
		}
		ids = append(ids, template.ID)
	}
	for _, question := range s.questions {
		if question.TenantID == tenantID && question.ExamPaperID == id {
			return AnswerSheetSourceDeletion{}, ErrAnswerSheetSourceInUse
		}
	}
	for _, job := range s.imports {
		if job.TenantID == tenantID && (job.ExamPaperID == id || job.PaperFileAssetID == item.FileAssetID || job.AnswerFileAssetID == item.FileAssetID) {
			return AnswerSheetSourceDeletion{}, ErrAnswerSheetSourceInUse
		}
		if job.TenantID == tenantID {
			for _, source := range job.Sources {
				if source.FileAssetID == item.FileAssetID {
					return AnswerSheetSourceDeletion{}, ErrAnswerSheetSourceInUse
				}
			}
		}
	}
	// 所有引用检查先完成再一起移除，任一受保护引用不能造成草稿只删了一半。
	for _, templateID := range ids {
		delete(s.templates, templateID)
	}
	delete(s.papers, id)
	sort.Strings(ids)
	return AnswerSheetSourceDeletion{Deleted: true, PaperID: id, DeletedTemplateIDs: ids}, nil
}

func (s *PostgresStore) DeleteAnswerSheetSource(ctx context.Context, tenantID, examID, id string) (AnswerSheetSourceDeletion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AnswerSheetSourceDeletion{}, err
	}
	defer tx.Rollback()
	// 与创建模板共用考试级锁；先固定模板再锁考试行，沿用改框/锁定模板的顺序，避免交错删除新草稿。
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, examID); err != nil {
		return AnswerSheetSourceDeletion{}, err
	}
	var assetID string
	err = tx.QueryRowContext(ctx, `SELECT file_asset_id::text FROM exam_paper WHERE tenant_id=$1 AND exam_id=$2::uuid AND id=$3::uuid AND purpose='answer_sheet' AND deleted_at IS NULL`, tenantID, examID, id).Scan(&assetID)
	if errors.Is(err, sql.ErrNoRows) {
		return AnswerSheetSourceDeletion{}, ErrNotFound
	}
	if err != nil {
		return AnswerSheetSourceDeletion{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text,status,locked_at IS NOT NULL FROM answer_sheet_template WHERE tenant_id=$1 AND exam_paper_id=$2::uuid AND deleted_at IS NULL ORDER BY id FOR UPDATE`, tenantID, id)
	if err != nil {
		return AnswerSheetSourceDeletion{}, err
	}
	ids := []string{}
	for rows.Next() {
		var templateID, status string
		var locked bool
		if err = rows.Scan(&templateID, &status, &locked); err != nil {
			rows.Close()
			return AnswerSheetSourceDeletion{}, err
		}
		if status != "draft" || locked {
			rows.Close()
			return AnswerSheetSourceDeletion{}, ErrAnswerSheetSourceInUse
		}
		ids = append(ids, templateID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return AnswerSheetSourceDeletion{}, err
	}
	if err = ensureExamPaperMutableTx(ctx, tx, tenantID, examID); err != nil {
		return AnswerSheetSourceDeletion{}, err
	}
	// 锁定模板可能继续读取来源文件，必须等模板和考试锁固定后再锁来源行，避免形成反向等待。
	var latestAssetID string
	err = tx.QueryRowContext(ctx, `SELECT file_asset_id::text FROM exam_paper WHERE tenant_id=$1 AND exam_id=$2::uuid AND id=$3::uuid AND purpose='answer_sheet' AND deleted_at IS NULL FOR UPDATE`, tenantID, examID, id).Scan(&latestAssetID)
	if errors.Is(err, sql.ErrNoRows) {
		return AnswerSheetSourceDeletion{}, ErrNotFound
	}
	if err != nil {
		return AnswerSheetSourceDeletion{}, err
	}
	if latestAssetID != assetID {
		return AnswerSheetSourceDeletion{}, ErrConflict
	}
	var protected bool
	err = tx.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM question WHERE tenant_id=$1 AND exam_paper_id=$2::uuid AND deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM paper_import_job WHERE tenant_id=$1 AND (exam_paper_id=$2::uuid OR paper_file_asset_id=$3::uuid OR answer_file_asset_id=$3::uuid) AND deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM paper_import_source s JOIN paper_import_job j ON j.tenant_id=s.tenant_id AND j.id=s.paper_import_id WHERE s.tenant_id=$1 AND s.file_asset_id=$3::uuid AND s.deleted_at IS NULL AND j.deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM answer_sheet_template t WHERE t.tenant_id=$1 AND t.exam_paper_id=$2::uuid AND (
   EXISTS(SELECT 1 FROM answer_sheet_template_library l WHERE l.tenant_id=t.tenant_id AND l.source_template_id=t.id)
   OR EXISTS(SELECT 1 FROM answer_sheet_template_library_instance i WHERE i.tenant_id=t.tenant_id AND i.template_id=t.id)
   OR EXISTS(SELECT 1 FROM exam_answer_sheet_template_binding b WHERE b.tenant_id=t.tenant_id AND b.template_id=t.id)
   OR EXISTS(SELECT 1 FROM page_registration_run p WHERE p.tenant_id=t.tenant_id AND p.template_id=t.id)
   OR EXISTS(SELECT 1 FROM page_registration_correction c WHERE c.tenant_id=t.tenant_id AND c.template_id=t.id)
   OR EXISTS(SELECT 1 FROM answer_segment a WHERE a.tenant_id=t.tenant_id AND a.template_id=t.id)
   OR EXISTS(SELECT 1 FROM omr_run o WHERE o.tenant_id=t.tenant_id AND o.template_id=t.id)
   OR EXISTS(SELECT 1 FROM omr_calibration_session c WHERE c.tenant_id=t.tenant_id AND c.template_id=t.id)
   OR EXISTS(SELECT 1 FROM answer_sheet_print_batch b WHERE b.tenant_id=t.tenant_id AND b.template_id=t.id)
   OR EXISTS(SELECT 1 FROM page_template_match_run m WHERE m.tenant_id=t.tenant_id AND m.selected_template_id=t.id)
   OR EXISTS(SELECT 1 FROM score_release_anonymous_page a WHERE a.tenant_id=t.tenant_id AND a.template_id=t.id)
 ))`, tenantID, id, assetID).Scan(&protected)
	if err != nil {
		return AnswerSheetSourceDeletion{}, err
	}
	if protected {
		return AnswerSheetSourceDeletion{}, ErrAnswerSheetSourceInUse
	}
	// 仅软删当前考试登记和未锁定草稿；对象及文件资产可能由学校库或其他记录共享，不能在这里删除。
	if _, err = tx.ExecContext(ctx, `UPDATE answer_sheet_template SET deleted_at=now(),updated_at=now(),revision=revision+1 WHERE tenant_id=$1 AND exam_paper_id=$2::uuid AND status='draft' AND deleted_at IS NULL`, tenantID, id); err != nil {
		return AnswerSheetSourceDeletion{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE exam_paper SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND exam_id=$2::uuid AND id=$3::uuid AND deleted_at IS NULL`, tenantID, examID, id); err != nil {
		return AnswerSheetSourceDeletion{}, err
	}
	if err = tx.Commit(); err != nil {
		return AnswerSheetSourceDeletion{}, err
	}
	return AnswerSheetSourceDeletion{Deleted: true, PaperID: id, DeletedTemplateIDs: ids}, nil
}
