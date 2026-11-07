package paper

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"encoding/json"
	"errors"
	"strings"
)

func (s *ReferenceMaterialsService) Withdraw(ctx context.Context, scope auth.AccessScope, exam, question, id string, input WithdrawReferenceMaterialsInput) (ReferenceMaterials, error) {
	m := ReferenceMaterials{}
	if !referenceCommandValid(input.CommandID) || len(strings.TrimSpace(input.Reason)) < 1 || len(input.Reason) > 1000 {
		return m, ErrInvalidInput
	}
	ctx = commandreceipt.WithID(ctx, input.CommandID)
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return m, e
	}
	defer tx.Rollback()
	identity := commandreceipt.Identity{TenantID: scope.TenantID, ActorID: scope.ActorID, Operation: "reference.withdraw", TargetID: exam + "/" + question + "/" + id}
	if found, e := commandreceipt.Load(ctx, tx, identity, input, &m); e != nil || found {
		return m, e
	}
	// 归档触发器也会取得考试锁；先锁考试再更新清单，与确认/开考保持相同顺序。
	// 这里只确认考试存在，冻结场次仍可撤回，不使用草稿可编辑检查。
	var examStatus string
	if e = tx.QueryRowContext(ctx, `SELECT status FROM exam WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL FOR SHARE`, scope.TenantID, exam).Scan(&examStatus); e != nil {
		return m, e
	}
	result, e := tx.ExecContext(ctx, `UPDATE question_reference_manifest SET use_state='withdrawn',authorization_revision=authorization_revision+1,withdrawn_at=now(),withdrawal_reason=$6 WHERE tenant_id=$1 AND exam_id=$2 AND question_id=$3 AND id=$4 AND authorization_revision=$5 AND use_state='active'`, scope.TenantID, exam, question, id, input.ExpectedAuthorizationRevision, input.Reason)
	if e != nil {
		return m, e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return m, ErrConflict
	}
	m, e = loadReferenceMaterials(ctx, tx, scope.TenantID, exam, question, id)
	if e != nil {
		return m, e
	}
	if e = commandreceipt.Save(ctx, tx, identity, input, m); e != nil {
		return m, e
	}
	return m, tx.Commit()
}

func (s *ReferenceMaterialsService) Recover(ctx context.Context, scope auth.AccessScope, exam, id string) (commandreceipt.Receipt, error) {
	receipt, e := commandreceipt.Recover(ctx, s.db, scope.TenantID, scope.ActorID, id, "reference.")
	if e != nil {
		return receipt, e
	}
	if receipt.TargetID != exam && !strings.HasPrefix(receipt.TargetID, exam+"/") {
		return commandreceipt.Receipt{CommandID: id, Status: "not_accepted"}, nil
	}
	if receipt.Operation == "reference.closed" {
		receipt.Status = "rejected"
		receipt.Result = nil
		return receipt, nil
	}
	if receipt.Operation == "reference.crop" && receipt.Status == "succeeded" {
		var item ReferenceMaterialItem
		if json.Unmarshal(receipt.Result, &item) != nil {
			return receipt, ErrInvalidInput
		}
		a, err := s.assets.Get(ctx, scope.TenantID, item.CropRef)
		if err != nil || a.Lifecycle != files.LifecycleActive {
			receipt.Status = "processing"
		}
	}
	return receipt, nil
}

// ResumeCommand 只恢复服务器已登记的原裁图；没有业务回执时，在同一命令锁下封闭旧命令。
// 封闭记录使迟到的原请求也不能提交，刷新后无需把教师输入保存到浏览器便可重新编辑。
func (s *ReferenceMaterialsService) ResumeCommand(ctx context.Context, scope auth.AccessScope, exam, question, id string) (commandreceipt.Receipt, error) {
	if !referenceCommandValid(id) {
		return commandreceipt.Receipt{}, ErrInvalidInput
	}
	ctx = commandreceipt.WithID(ctx, id)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return commandreceipt.Receipt{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scope.TenantID+":"+scope.ActorID+":"+id+":business-command"); err != nil {
		return commandreceipt.Receipt{}, err
	}
	receipt := commandreceipt.Receipt{CommandID: id, Status: "succeeded"}
	err = tx.QueryRowContext(ctx, `SELECT operation,target_id,result FROM business_command_receipt WHERE tenant_id=$1 AND actor_id=$2 AND command_id=$3`, scope.TenantID, scope.ActorID, id).Scan(&receipt.Operation, &receipt.TargetID, &receipt.Result)
	if errors.Is(err, sql.ErrNoRows) {
		identity := commandreceipt.Identity{TenantID: scope.TenantID, ActorID: scope.ActorID, Operation: "reference.closed", TargetID: exam + "/" + question}
		if err = commandreceipt.Save(ctx, tx, identity, nil, map[string]bool{"closed": true}); err != nil {
			return receipt, err
		}
		receipt.Operation, receipt.TargetID, receipt.Status = identity.Operation, identity.TargetID, "rejected"
		return receipt, tx.Commit()
	}
	if err != nil {
		return receipt, err
	}
	target := exam + "/" + question
	if receipt.TargetID != target && !(receipt.Operation == "reference.withdraw" && strings.HasPrefix(receipt.TargetID, target+"/")) {
		return commandreceipt.Receipt{}, ErrConflict
	}
	if !strings.HasPrefix(receipt.Operation, "reference.") {
		return commandreceipt.Receipt{}, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return receipt, err
	}
	if receipt.Operation == "reference.closed" {
		receipt.Status = "rejected"
		receipt.Result = nil
		return receipt, nil
	}
	if receipt.Operation != "reference.crop" {
		return receipt, nil
	}
	var item ReferenceMaterialItem
	if json.Unmarshal(receipt.Result, &item) != nil {
		return receipt, ErrInvalidInput
	}
	if _, err = s.assets.Get(ctx, scope.TenantID, item.CropRef); err == nil {
		return receipt, nil
	}
	// 隐藏的来源 ID 只从该裁图的服务端元数据恢复；客户端不能提供对象位置。
	var raw []byte
	err = s.db.QueryRowContext(ctx, `SELECT reference_crop_json FROM file_asset WHERE tenant_id=$1 AND id=$2 AND exam_id=$3 AND reference_crop_json->>'question_id'=$4 AND owner_type='question_reference_crop' AND deleted_at IS NULL`, scope.TenantID, item.CropRef, exam, question).Scan(&raw)
	if err != nil {
		return receipt, err
	}
	var metadata struct {
		SourceID   string `json:"source_id"`
		PageID     string `json:"page_id"`
		SourceHash string `json:"source_hash"`
	}
	if json.Unmarshal(raw, &metadata) != nil {
		return receipt, ErrInvalidInput
	}
	source, err := s.scopedSource(ctx, scope, metadata.SourceID)
	if err != nil || source.HashSHA256 != metadata.SourceHash {
		return receipt, ErrConflict
	}
	page, err := s.assets.Get(ctx, scope.TenantID, metadata.PageID)
	if err != nil || page.HashSHA256 != item.Source.SHA256 {
		return receipt, ErrConflict
	}
	data, err := s.readImage(ctx, page)
	if err != nil {
		return receipt, err
	}
	data, err = cropReferenceImage(data, CreateReferenceCropInput{Width: item.Source.Width, Height: item.Source.Height, Box: item.Box})
	if err != nil {
		return receipt, err
	}
	if err = s.finishCrop(ctx, scope.TenantID, item, data); err != nil {
		return receipt, err
	}
	return receipt, nil
}
