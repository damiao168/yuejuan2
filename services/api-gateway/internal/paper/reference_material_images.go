package paper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"image"
	"image/png"
	"io"
	"strings"
)

func (s *ReferenceMaterialsService) Sources(ctx context.Context, scope auth.AccessScope, exam string) ([]ReferenceMaterialSource, error) {
	// 当前导入代次的已持久化页图，或直接导入图片。旧代次页图不进入候选。
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (
 SELECT f.id::text source_id,f.id::text page_id,'import_image' kind,1 page_no,j.id::text||':'||j.current_generation::text||':'||j.source_revision identity
 FROM paper_import_job j JOIN paper_import_source src ON src.tenant_id=j.tenant_id AND src.paper_import_id=j.id
 JOIN file_asset f ON f.tenant_id=src.tenant_id AND f.id=src.file_asset_id
 WHERE j.tenant_id=$1 AND j.exam_id=$2 AND j.deleted_at IS NULL AND src.deleted_at IS NULL AND f.content_type IN ('image/png','image/jpeg')
 UNION ALL
 SELECT src.file_asset_id::text,p->>'file_asset_id','decoded_page',(p->>'page_no')::int,i.run_id::text||':'||i.generation::text||':'||i.source_revision
 FROM paper_import_parse_input i JOIN paper_import_job j ON j.tenant_id=i.tenant_id AND j.id=i.paper_import_id AND j.current_generation=i.generation AND j.source_revision=i.source_revision
 CROSS JOIN LATERAL jsonb_array_elements(i.pages) p
 JOIN paper_import_source src ON src.tenant_id=j.tenant_id AND src.paper_import_id=j.id AND src.id::text=p->>'source_id'
 WHERE j.tenant_id=$1 AND j.exam_id=$2 AND j.deleted_at IS NULL AND src.deleted_at IS NULL
 UNION ALL
 SELECT a->>'file_asset_id',a->>'file_asset_id','question_bank',1,q.id::text||':'||q.source_content_hash
 FROM question q CROSS JOIN LATERAL jsonb_array_elements(COALESCE(q.bank_content->'assets','[]')) a
 WHERE q.tenant_id=$1 AND q.exam_id=$2 AND q.deleted_at IS NULL AND q.source_type='question_bank'
 ) SELECT DISTINCT c.source_id,c.page_id,c.kind,c.page_no,c.identity FROM candidates c ORDER BY c.identity,c.page_no`, scope.TenantID, exam)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []ReferenceMaterialSource
	for rows.Next() {
		var item ReferenceMaterialSource
		if err = rows.Scan(&item.SourceAssetID, &item.PageAssetID, &item.Kind, &item.PageNo, &item.Identity); err != nil {
			return nil, err
		}
		candidates = append(candidates, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	out := []ReferenceMaterialSource{}
	for _, item := range candidates {
		source, e := s.scopedSource(ctx, scope, item.SourceAssetID)
		if e != nil {
			continue
		}
		asset, e := s.assets.Get(ctx, scope.TenantID, item.PageAssetID)
		if e != nil || asset.Lifecycle != files.LifecycleActive || asset.DeletedAt != nil || asset.SubmissionID != "" {
			continue
		}
		if asset.ContentType != "image/png" && asset.ContentType != "image/jpeg" {
			continue
		}
		data, e := s.readImage(ctx, asset)
		if e != nil {
			continue
		}
		config, _, e := image.DecodeConfig(bytes.NewReader(data))
		if e != nil {
			continue
		}
		item.Name = source.OriginalName
		item.SHA256 = asset.HashSHA256
		item.SourceHash = source.HashSHA256
		orientation, e := referenceImageOrientation(data)
		if e != nil {
			continue
		}
		item.Width, item.Height = referenceOrientedDimensions(config.Width, config.Height, orientation)
		item.PageRef = referenceHash("reference-page-v1", exam, item.SourceAssetID, item.PageAssetID, item.Identity, item.SHA256, item.SourceHash)
		out = append(out, item)
	}
	return out, nil
}

func (s *ReferenceMaterialsService) scopedSource(ctx context.Context, scope auth.AccessScope, id string) (files.FileAsset, error) {
	var a files.FileAsset
	var e error
	if store, ok := s.assets.(files.LifecycleStore); ok {
		a, e = store.GetScoped(ctx, scope, id)
	} else {
		a, e = s.assets.Get(ctx, scope.TenantID, id)
		if e == nil && !files.AllowsAsset(scope, a) {
			e = files.ErrForbidden
		}
	}
	if e != nil {
		return a, e
	}
	if a.Lifecycle != files.LifecycleActive || a.DeletedAt != nil || a.SubmissionID != "" || a.OwnerType == "submission" || a.OwnerType == "answer_segment_crop" {
		return a, files.ErrForbidden
	}
	return a, nil
}

func (s *ReferenceMaterialsService) readImage(ctx context.Context, a files.FileAsset) ([]byte, error) {
	if a.SizeBytes <= 0 || a.SizeBytes > 8<<20 || a.Lifecycle != files.LifecycleActive || a.DeletedAt != nil {
		return nil, ErrInvalidInput
	}
	r, e := s.objects.Get(ctx, a.StorageBucket, a.StorageKey)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	b, e := io.ReadAll(io.LimitReader(r, 8<<20+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) != a.SizeBytes || fmt.Sprintf("%x", sha256.Sum256(b)) != a.HashSHA256 {
		return nil, ErrConflict
	}
	config, _, e := image.DecodeConfig(bytes.NewReader(b))
	if e != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return nil, ErrInvalidInput
	}
	if _, e := referenceImageOrientation(b); e != nil {
		return nil, ErrInvalidInput
	}
	return b, nil
}

func (s *ReferenceMaterialsService) SourceImage(ctx context.Context, scope auth.AccessScope, exam, ref string) ([]byte, string, error) {
	list, e := s.Sources(ctx, scope, exam)
	if e != nil {
		return nil, "", e
	}
	for _, p := range list {
		if p.PageRef == ref {
			a, e := s.assets.Get(ctx, scope.TenantID, p.PageAssetID)
			if e != nil {
				return nil, "", e
			}
			b, e := s.readImage(ctx, a)
			if e != nil {
				return nil, "", e
			}
			img, e := decodeOrientedReferenceImage(b)
			if e != nil {
				return nil, "", ErrInvalidInput
			}
			// 来源摘要仍校验原文件；预览统一输出无方向元数据的 PNG，与裁图使用同一像素空间。
			var out bytes.Buffer
			if e = png.Encode(&out, img); e != nil {
				return nil, "", e
			}
			return out.Bytes(), "image/png", nil
		}
	}
	return nil, "", ErrNotFound
}

func cropReferenceImage(data []byte, input CreateReferenceCropInput) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalidInput
	}
	orientation, err := referenceImageOrientation(data)
	if err != nil {
		return nil, ErrInvalidInput
	}
	width, height := referenceOrientedDimensions(config.Width, config.Height, orientation)
	b := input.Box
	// 整数框以规范页左上为原点，先做有界加法检查，再完整解码，避免越界和解码炸弹。
	if input.Width != width || input.Height != height || b.X < 0 || b.Y < 0 || b.Width < 1 || b.Height < 1 || b.Width > width-b.X || b.Height > height-b.Y || int64(width)*int64(height) > 40_000_000 || int64(b.Width)*int64(b.Height) > 4_000_000 {
		return nil, ErrInvalidInput
	}
	img, err := decodeOrientedReferenceImage(data)
	if err != nil {
		return nil, ErrInvalidInput
	}
	crop := image.NewNRGBA(image.Rect(0, 0, b.Width, b.Height))
	for y := 0; y < b.Height; y++ {
		for x := 0; x < b.Width; x++ {
			crop.Set(x, y, img.At(b.X+x, b.Y+y))
		}
	}
	var out bytes.Buffer
	if err = png.Encode(&out, crop); err != nil {
		return nil, err
	}
	if out.Len() > 1<<20 {
		return nil, ErrInvalidInput
	}
	return out.Bytes(), nil
}

func (s *ReferenceMaterialsService) CreateCrop(ctx context.Context, scope auth.AccessScope, exam, question string, input CreateReferenceCropInput) (ReferenceMaterialItem, error) {
	if !referenceCommandValid(input.CommandID) {
		return ReferenceMaterialItem{}, ErrInvalidInput
	}
	ctx = commandreceipt.WithID(ctx, input.CommandID)
	list, e := s.Sources(ctx, scope, exam)
	if e != nil {
		return ReferenceMaterialItem{}, e
	}
	var page *ReferenceMaterialSource
	for i := range list {
		if list[i].PageRef == input.PageRef {
			page = &list[i]
			break
		}
	}
	if page == nil || page.SHA256 != input.PageSHA256 {
		return ReferenceMaterialItem{}, ErrConflict
	}
	asset, e := s.assets.Get(ctx, scope.TenantID, page.PageAssetID)
	if e != nil {
		return ReferenceMaterialItem{}, e
	}
	data, e := s.readImage(ctx, asset)
	if e != nil {
		return ReferenceMaterialItem{}, e
	}
	cropped, e := cropReferenceImage(data, input)
	if e != nil {
		return ReferenceMaterialItem{}, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return ReferenceMaterialItem{}, e
	}
	defer tx.Rollback()
	identity := commandreceipt.Identity{TenantID: scope.TenantID, ActorID: scope.ActorID, Operation: "reference.crop", TargetID: exam + "/" + question}
	var result ReferenceMaterialItem
	if found, e := commandreceipt.Load(ctx, tx, identity, input, &result); e != nil || found {
		if e != nil {
			return result, e
		}
		_ = tx.Rollback()
		return result, s.finishCrop(ctx, scope.TenantID, result, cropped)
	}
	if e = ensureExamPaperMutableTx(ctx, tx, scope.TenantID, exam); e != nil {
		return result, e
	}
	if e = referenceSourceCurrentTx(ctx, tx, scope.TenantID, exam, *page); e != nil {
		return result, e
	}
	var school string
	if e = tx.QueryRowContext(ctx, `SELECT e.school_id::text FROM exam e JOIN question q ON q.tenant_id=e.tenant_id AND q.exam_id=e.id WHERE e.tenant_id=$1 AND e.id=$2 AND q.id=$3 AND q.deleted_at IS NULL`, scope.TenantID, exam, question).Scan(&school); e != nil {
		return result, e
	}
	id := uuid.NewString()
	key := "tenant/" + scope.TenantID + "/reference/" + id + ".png"
	digest := fmt.Sprintf("%x", sha256.Sum256(cropped))
	result = ReferenceMaterialItem{ID: id, CropRef: id, SHA256: digest, Width: input.Box.Width, Height: input.Box.Height, SizeBytes: int64(len(cropped)), Box: input.Box, Source: *page, FileAssetID: id, Required: true}
	// 对象 I/O 前不持考试锁。此事务已锁考试，因此仅登记 pending；对象写入在提交后执行。
	metadata, _ := json.Marshal(struct {
		Item       ReferenceMaterialItem `json:"item"`
		SourceID   string                `json:"source_id"`
		PageID     string                `json:"page_id"`
		SourceHash string                `json:"source_hash"`
		QuestionID string                `json:"question_id"`
	}{result, page.SourceAssetID, page.PageAssetID, page.SourceHash, question})
	_, e = tx.ExecContext(ctx, `INSERT INTO file_asset(id,tenant_id,school_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by,lifecycle_status,reference_crop_json) VALUES($1,$2,$3,$4,'question_reference_crop',$5,'reference.png','image/png',$6,$7,$8,$9,'private',$10,'pending_upload',$11)`, id, scope.TenantID, school, exam, id, len(cropped), digest, s.bucket, key, scope.ActorID, metadata)
	if e != nil {
		return result, e
	}
	if e = commandreceipt.Save(ctx, tx, identity, input, result); e != nil {
		return result, e
	}
	if e = tx.Commit(); e != nil {
		return result, e
	}
	return result, s.finishCrop(ctx, scope.TenantID, result, cropped)
}

func (s *ReferenceMaterialsService) finishCrop(ctx context.Context, tenant string, item ReferenceMaterialItem, data []byte) error {
	// files.Get 只返回 active；上传恢复须读取自己的 pending 记录，不能放宽通用读取。
	a := files.FileAsset{ID: item.CropRef, TenantID: tenant}
	err := s.db.QueryRowContext(ctx, `SELECT hash_sha256,lifecycle_status,revision,storage_bucket,storage_key FROM file_asset WHERE tenant_id=$1 AND id=$2 AND owner_type='question_reference_crop' AND deleted_at IS NULL`, tenant, item.CropRef).Scan(&a.HashSHA256, &a.Lifecycle, &a.Revision, &a.StorageBucket, &a.StorageKey)
	if err != nil {
		return err
	}
	if a.HashSHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
		return ErrConflict
	}
	if a.Lifecycle == files.LifecycleActive {
		return nil
	}
	if a.Lifecycle != files.LifecyclePendingUpload && a.Lifecycle != files.LifecycleUploadFailed {
		return ErrConflict
	}
	if err = s.objects.Put(ctx, a.StorageBucket, a.StorageKey, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		return err
	}
	store, ok := s.assets.(files.LifecycleStore)
	if !ok {
		return ErrInvalidInput
	}
	_, err = store.Activate(ctx, tenant, a.ID, a.Revision)
	if errors.Is(err, files.ErrConflict) {
		current, e := s.assets.Get(ctx, tenant, a.ID)
		if e == nil && current.Lifecycle == files.LifecycleActive {
			return nil
		}
	}
	return err
}

func referenceCommandValid(id string) bool {
	return len(id) >= 8 && len(id) <= 128 && strings.TrimSpace(id) == id
}

// 来源版本在取得考试锁后再核验，防止使用已替换解析代次的裁图保存新清单。
func referenceSourceCurrentTx(ctx context.Context, tx *sql.Tx, tenant, exam string, source ReferenceMaterialSource) error {
	var current bool
	var err error
	switch source.Kind {
	case "import_image":
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM paper_import_job j JOIN paper_import_source src ON src.tenant_id=j.tenant_id AND src.paper_import_id=j.id WHERE j.tenant_id=$1 AND j.exam_id=$2 AND j.deleted_at IS NULL AND src.deleted_at IS NULL AND src.file_asset_id::text=$3 AND j.id::text||':'||j.current_generation::text||':'||j.source_revision=$4)`, tenant, exam, source.SourceAssetID, source.Identity).Scan(&current)
	case "decoded_page":
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM paper_import_parse_input i JOIN paper_import_job j ON j.tenant_id=i.tenant_id AND j.id=i.paper_import_id AND j.current_generation=i.generation AND j.source_revision=i.source_revision CROSS JOIN LATERAL jsonb_array_elements(i.pages) p JOIN paper_import_source src ON src.tenant_id=j.tenant_id AND src.paper_import_id=j.id AND src.id::text=p->>'source_id' WHERE j.tenant_id=$1 AND j.exam_id=$2 AND j.deleted_at IS NULL AND src.deleted_at IS NULL AND src.file_asset_id::text=$5 AND p->>'file_asset_id'=$3 AND i.run_id::text||':'||i.generation::text||':'||i.source_revision=$4)`, tenant, exam, source.PageAssetID, source.Identity, source.SourceAssetID).Scan(&current)
	case "question_bank":
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM question q CROSS JOIN LATERAL jsonb_array_elements(q.bank_content->'assets') a WHERE q.tenant_id=$1 AND q.exam_id=$2 AND q.deleted_at IS NULL AND a->>'file_asset_id'=$3 AND q.id::text||':'||q.source_content_hash=$4)`, tenant, exam, source.SourceAssetID, source.Identity).Scan(&current)
	default:
		return ErrInvalidInput
	}
	if err != nil {
		return err
	}
	if !current {
		return ErrConflict
	}
	return nil
}

func (s *ReferenceMaterialsService) CropImage(ctx context.Context, scope auth.AccessScope, exam, question, id string) ([]byte, error) {
	var belongs bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM file_asset WHERE tenant_id=$1 AND id=$2 AND exam_id=$3 AND owner_type='question_reference_crop' AND reference_crop_json->>'question_id'=$4)`, scope.TenantID, id, exam, question).Scan(&belongs); err != nil {
		return nil, err
	}
	if !belongs {
		// 从题库复制的裁图仍属于原考试，必须通过目标题目的准确授权清单读取。
		m, err := s.Get(ctx, scope.TenantID, exam, question)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrNotFound) {
			// 题目不存在或不属于当前考试时沿用跨题拒绝边界，不能把清单回退查询变成数据库错误。
			return nil, files.ErrForbidden
		}
		if err != nil {
			return nil, err
		}
		for _, item := range m.Items {
			if item.CropRef == id {
				_, images, err := s.Resolve(ctx, scope.TenantID, exam, question, referenceMaterialBinding(m))
				if err != nil {
					return nil, err
				}
				for _, image := range images {
					if image.Item.CropRef == id {
						return image.Data, nil
					}
				}
			}
		}
		return nil, files.ErrForbidden
	}
	a, e := s.assets.Get(ctx, scope.TenantID, id)
	if e != nil {
		return nil, e
	}
	if a.OwnerType != "question_reference_crop" || a.ExamID != exam {
		return nil, files.ErrForbidden
	}
	return s.readImage(ctx, a)
}
