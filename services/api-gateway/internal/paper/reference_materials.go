package paper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"github.com/google/uuid"
)

const referenceCropTransform = "reference-crop-png-v2"

type ReferencePixelBox struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}
type ReferenceMaterialSource struct {
	PageRef       string `json:"page_ref"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	PageNo        int    `json:"page_no"`
	SHA256        string `json:"sha256"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	SourceAssetID string `json:"-"`
	PageAssetID   string `json:"-"`
	SourceHash    string `json:"source_sha256"`
	Identity      string `json:"source_version"`
}
type ReferenceMaterialItem struct {
	ID          string                  `json:"id"`
	CropRef     string                  `json:"crop_ref"`
	Role        string                  `json:"role"`
	Ordinal     int                     `json:"ordinal"`
	Required    bool                    `json:"required"`
	SHA256      string                  `json:"sha256"`
	Width       int                     `json:"width"`
	Height      int                     `json:"height"`
	SizeBytes   int64                   `json:"size_bytes"`
	Box         ReferencePixelBox       `json:"pixel_bbox"`
	Source      ReferenceMaterialSource `json:"source"`
	FileAssetID string                  `json:"-"`
}
type ReferenceMaterials struct {
	ID                    string                  `json:"manifest_id,omitempty"`
	Mode                  string                  `json:"mode"`
	Revision              int                     `json:"revision"`
	ContentHash           string                  `json:"content_hash,omitempty"`
	AuthorizationRevision int                     `json:"authorization_revision,omitempty"`
	UseState              string                  `json:"use_state,omitempty"`
	Frozen                bool                    `json:"frozen"`
	Items                 []ReferenceMaterialItem `json:"items"`
	CreatedBy             string                  `json:"created_by,omitempty"`
	CreatedAt             time.Time               `json:"created_at,omitempty"`
}

// ReferenceMaterialBinding 只含冻结内容身份；实时撤回状态不能改变历史快照摘要。
type ReferenceMaterialBinding struct {
	ID          string `json:"manifest_id"`
	Mode        string `json:"mode"`
	Revision    int    `json:"revision"`
	ContentHash string `json:"content_hash"`
	ImageCount  int    `json:"image_count"`
}

func referenceMaterialBinding(m ReferenceMaterials) *ReferenceMaterialBinding {
	if m.ID == "" {
		return nil
	}
	return &ReferenceMaterialBinding{m.ID, m.Mode, m.Revision, m.ContentHash, len(m.Items)}
}

type SaveReferenceMaterialsInput struct {
	CommandID        string `json:"command_id"`
	ExpectedRevision int    `json:"expected_revision"`
	Mode             string `json:"mode"`
	Items            []struct {
		CropRef string `json:"crop_ref"`
		Role    string `json:"role"`
		Ordinal int    `json:"ordinal"`
	} `json:"items"`
}
type CreateReferenceCropInput struct {
	CommandID  string            `json:"command_id"`
	PageRef    string            `json:"page_ref"`
	PageSHA256 string            `json:"page_sha256"`
	Width      int               `json:"page_width"`
	Height     int               `json:"page_height"`
	Box        ReferencePixelBox `json:"pixel_bbox"`
}
type WithdrawReferenceMaterialsInput struct {
	CommandID                     string `json:"command_id"`
	ExpectedAuthorizationRevision int    `json:"expected_authorization_revision"`
	Reason                        string `json:"reason"`
}

// ReferenceMaterialsService 只复用已登记的来源与文件存储；请求中的不透明句柄
// 每次都从数据库重新解析，不接受客户端指定对象地址或学生原卷。
type ReferenceMaterialsService struct {
	db      *sql.DB
	assets  files.Store
	objects files.ObjectStorage
	bucket  string
}

func NewReferenceMaterialsService(db *sql.DB, assets files.Store, objects files.ObjectStorage, bucket string) *ReferenceMaterialsService {
	if db == nil || assets == nil || objects == nil {
		return nil
	}
	return &ReferenceMaterialsService{db, assets, objects, bucket}
}
func referenceHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d:%s", len([]byte(p)), p)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

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

// Get 固定考试后通过确认快照找清单，草稿才读最新修订；历史 NULL 只读投影未记录。
func (s *ReferenceMaterialsService) Get(ctx context.Context, tenant, exam, question string) (ReferenceMaterials, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT e.status FROM exam e JOIN question q ON q.tenant_id=e.tenant_id AND q.exam_id=e.id WHERE e.tenant_id=$1 AND e.id=$2 AND q.id=$3 AND q.deleted_at IS NULL`, tenant, exam, question).Scan(&status)
	if err != nil {
		return ReferenceMaterials{}, err
	}
	if status == "draft" || status == "configured" {
		return loadReferenceMaterials(ctx, s.db, tenant, exam, question, "")
	}
	var id sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT q->'reference_materials'->>'manifest_id' FROM exam_readiness_snapshot r CROSS JOIN LATERAL jsonb_array_elements(r.import_snapshot_json->'questions') q WHERE r.tenant_id=$1 AND r.exam_id=$2 AND r.status='passed' AND q->>'id'=$3 ORDER BY r.confirmed_at DESC LIMIT 1`, tenant, exam, question).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !id.Valid {
		return ReferenceMaterials{Mode: "legacy_unrecorded", Frozen: true, Items: []ReferenceMaterialItem{}}, nil
	}
	if err != nil {
		return ReferenceMaterials{}, err
	}
	m, err := loadReferenceMaterials(ctx, s.db, tenant, exam, question, id.String)
	m.Frozen = true
	return m, err
}
func loadReferenceMaterials(ctx context.Context, q postgresQueryer, tenant, exam, question, id string) (ReferenceMaterials, error) {
	m := ReferenceMaterials{Mode: "legacy_unrecorded", Items: []ReferenceMaterialItem{}}
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT id::text,mode,revision,content_hash,authorization_revision,use_state,created_by::text,created_at FROM question_reference_manifest WHERE tenant_id=$1 AND exam_id=$2 AND question_id=$3 AND ($4='' OR id::text=$4) ORDER BY revision DESC LIMIT 1`, tenant, exam, question, id).Scan(&m.ID, &m.Mode, &m.Revision, &m.ContentHash, &m.AuthorizationRevision, &m.UseState, &m.CreatedBy, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if id != "" {
			return m, ErrNotFound
		}
		return m, nil
	}
	if err != nil {
		return m, err
	}
	rows, err := q.QueryContext(ctx, `SELECT payload,derived_file_asset_id::text,source_file_asset_id::text,page_file_asset_id::text FROM question_reference_manifest_item WHERE tenant_id=$1 AND manifest_id=$2 ORDER BY ordinal`, tenant, m.ID)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ReferenceMaterialItem
		var derived, source, page string
		if err = rows.Scan(&raw, &derived, &source, &page); err != nil {
			return m, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return m, err
		}
		item.FileAssetID = derived
		item.Source.SourceAssetID = source
		item.Source.PageAssetID = page
		m.Items = append(m.Items, item)
	}
	return m, rows.Err()
}

func (s *ReferenceMaterialsService) Save(ctx context.Context, scope auth.AccessScope, exam, question string, input SaveReferenceMaterialsInput) (ReferenceMaterials, error) {
	m := ReferenceMaterials{}
	if !referenceCommandValid(input.CommandID) || input.ExpectedRevision < 0 || !(input.Mode == "text_only" && len(input.Items) == 0 || input.Mode == "reference_images" && len(input.Items) > 0 && len(input.Items) <= 4) {
		return m, ErrInvalidInput
	}
	ctx = commandreceipt.WithID(ctx, input.CommandID)
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return m, e
	}
	defer tx.Rollback()
	identity := commandreceipt.Identity{TenantID: scope.TenantID, ActorID: scope.ActorID, Operation: "reference.save", TargetID: exam + "/" + question}
	if found, e := commandreceipt.Load(ctx, tx, identity, input, &m); e != nil || found {
		return m, e
	}
	if e = ensureExamPaperMutableTx(ctx, tx, scope.TenantID, exam); e != nil {
		return m, e
	}
	previous, e := loadReferenceMaterials(ctx, tx, scope.TenantID, exam, question, "")
	if e != nil {
		return m, e
	}
	if previous.Revision != input.ExpectedRevision {
		return m, ErrConflict
	}
	m = ReferenceMaterials{ID: uuid.NewString(), Mode: input.Mode, Revision: previous.Revision + 1, AuthorizationRevision: 1, UseState: "active", CreatedBy: scope.ActorID, CreatedAt: time.Now().UTC(), Items: []ReferenceMaterialItem{}}
	seen := map[string]bool{}
	roles := map[string]int{}
	parts := []string{"material-manifest-v1", input.Mode}
	grants := []map[string]any{}
	// 同一事务按资产身份加共享锁；保存只接受 active 的本题派生图及匹配的来源摘要。
	for i, entry := range input.Items {
		if seen[entry.CropRef] || entry.Ordinal != i+1 || (entry.Role != "question_diagram" && entry.Role != "reference_solution") {
			return m, ErrInvalidInput
		}
		seen[entry.CropRef] = true
		roles[entry.Role]++
		if roles[entry.Role] > 2 || entry.Role == "question_diagram" && roles["reference_solution"] > 0 {
			return m, ErrInvalidInput
		}
		var raw []byte
		var derived string
		e = tx.QueryRowContext(ctx, `SELECT reference_crop_json,id::text FROM file_asset WHERE tenant_id=$1 AND id::text=$2 AND exam_id=$3 AND reference_crop_json->>'question_id'=$4 AND owner_type='question_reference_crop' AND lifecycle_status='active' AND deleted_at IS NULL FOR SHARE`, scope.TenantID, entry.CropRef, exam, question).Scan(&raw, &derived)
		if e != nil {
			return m, e
		}
		var metadata struct {
			Item       ReferenceMaterialItem `json:"item"`
			SourceID   string                `json:"source_id"`
			PageID     string                `json:"page_id"`
			SourceHash string                `json:"source_hash"`
			QuestionID string                `json:"question_id"`
		}
		if e = json.Unmarshal(raw, &metadata); e != nil {
			return m, e
		}
		item := metadata.Item
		item.FileAssetID = derived
		item.ID = uuid.NewString()
		item.Role = entry.Role
		item.Ordinal = entry.Ordinal
		item.Required = true
		item.Source.SourceAssetID = metadata.SourceID
		item.Source.PageAssetID = metadata.PageID
		item.Source.SourceHash = metadata.SourceHash
		if e = referenceSourceCurrentTx(ctx, tx, scope.TenantID, exam, item.Source); e != nil {
			return m, e
		}
		if reader, ok := s.assets.(interface {
			GetScopedTx(context.Context, *sql.Tx, auth.AccessScope, string) (files.FileAsset, error)
		}); ok {
			if _, e = reader.GetScopedTx(ctx, tx, scope, metadata.SourceID); e != nil {
				return m, e
			}
		} else {
			return m, ErrInvalidInput
		}
		for _, ref := range []struct{ id, hash string }{{metadata.SourceID, metadata.SourceHash}, {metadata.PageID, item.Source.SHA256}} {
			var actual string
			e = tx.QueryRowContext(ctx, `SELECT hash_sha256 FROM file_asset WHERE tenant_id=$1 AND id=$2 AND lifecycle_status='active' AND deleted_at IS NULL FOR SHARE`, scope.TenantID, ref.id).Scan(&actual)
			if e != nil {
				return m, e
			}
			if actual != ref.hash {
				return m, ErrConflict
			}
		}
		m.Items = append(m.Items, item)
		parts = append(parts, item.Role, fmt.Sprint(item.Ordinal), item.Source.PageRef, item.Source.SourceHash, item.Source.SHA256, item.SHA256, fmt.Sprint(item.Box.X), fmt.Sprint(item.Box.Y), fmt.Sprint(item.Box.Width), fmt.Sprint(item.Box.Height), fmt.Sprint(item.SizeBytes), referenceCropTransform)
		grants = append(grants, map[string]any{"source_kind": item.Source.Kind, "source_file_asset_id": metadata.SourceID, "source_sha256": metadata.SourceHash, "source_version": item.Source.Identity, "exam_id": exam, "authorized_by": scope.ActorID, "purpose": "frozen_exam_reference"})
	}
	m.ContentHash = referenceHash(parts...)
	inputJSON, _ := json.Marshal(input)
	grantJSON, _ := json.Marshal(map[string]any{"schema_version": 1, "exam_id": exam, "sources": grants})
	inserted, e := tx.ExecContext(ctx, `INSERT INTO question_reference_manifest(id,tenant_id,exam_id,question_id,revision,previous_manifest_id,mode,content_hash,command_id,request_hash,created_by,created_at,exam_use_grant_json) SELECT $1,$2,$3,q.id,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11,$12,$13 FROM question q WHERE q.tenant_id=$2 AND q.exam_id=$3 AND q.id=$4 AND q.deleted_at IS NULL`, m.ID, scope.TenantID, exam, question, m.Revision, previous.ID, m.Mode, m.ContentHash, input.CommandID, referenceHash(string(inputJSON)), scope.ActorID, m.CreatedAt, grantJSON)
	if e != nil {
		return m, e
	}
	// INSERT SELECT 没有匹配题目时不会报 SQL 错误，不能因此提交一个不存在清单的成功回执。
	if count, err := inserted.RowsAffected(); err != nil {
		return m, err
	} else if count != 1 {
		return m, ErrNotFound
	}
	for _, item := range m.Items {
		raw, _ := json.Marshal(item)
		_, e = tx.ExecContext(ctx, `INSERT INTO question_reference_manifest_item(id,tenant_id,manifest_id,ordinal,role,source_file_asset_id,page_file_asset_id,derived_file_asset_id,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, item.ID, scope.TenantID, m.ID, item.Ordinal, item.Role, item.Source.SourceAssetID, item.Source.PageAssetID, item.FileAssetID, raw)
		if e != nil {
			return m, e
		}
	}
	if e = commandreceipt.Save(ctx, tx, identity, input, m); e != nil {
		return m, e
	}
	return m, tx.Commit()
}

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

type ResolvedReferenceImage struct {
	Item ReferenceMaterialItem
	Data []byte
}

// Resolve 只接收已冻结的准确绑定；考试使用权不随原题库个人浏览角色变化，
// 显式撤回或源/page/派生资产隔离仍阻断。读取后再次核验，不能通过同名对象换图。
func (s *ReferenceMaterialsService) Resolve(ctx context.Context, tenant, exam, question string, binding *ReferenceMaterialBinding) (ReferenceMaterials, []ResolvedReferenceImage, error) {
	if binding == nil {
		return ReferenceMaterials{}, nil, nil
	}
	m, err := loadReferenceMaterials(ctx, s.db, tenant, exam, question, binding.ID)
	if err != nil {
		return m, nil, err
	}
	if m.UseState != "active" || m.ContentHash != binding.ContentHash || m.Revision != binding.Revision || m.Mode != binding.Mode || len(m.Items) != binding.ImageCount {
		return m, nil, ErrConflict
	}
	out := make([]ResolvedReferenceImage, 0, len(m.Items))
	for _, item := range m.Items {
		for _, ref := range []struct{ id, hash string }{{item.Source.SourceAssetID, item.Source.SourceHash}, {item.Source.PageAssetID, item.Source.SHA256}, {item.FileAssetID, item.SHA256}} {
			a, e := s.assets.Get(ctx, tenant, ref.id)
			if e != nil {
				return m, nil, e
			}
			if a.Lifecycle != files.LifecycleActive || a.DeletedAt != nil || a.HashSHA256 != ref.hash {
				return m, nil, ErrConflict
			}
		}
		a, e := s.assets.Get(ctx, tenant, item.FileAssetID)
		if e != nil {
			return m, nil, e
		}
		data, e := s.readImage(ctx, a)
		if e != nil {
			return m, nil, e
		}
		out = append(out, ResolvedReferenceImage{item, data})
	}
	latest, err := loadReferenceMaterials(ctx, s.db, tenant, exam, question, m.ID)
	if err != nil {
		return m, nil, err
	}
	if latest.AuthorizationRevision != m.AuthorizationRevision || latest.UseState != "active" {
		return m, nil, ErrConflict
	}
	return m, out, nil
}
