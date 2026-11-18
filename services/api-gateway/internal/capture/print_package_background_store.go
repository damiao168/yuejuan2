package capture

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

const maxStudentPrintSourceBytes = 100 << 20

type studentPrintBackground struct {
	contentType string
	payload     []byte
	sha256      string
}

type printSourceQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// GetStudentPrintSource 沿模板归属读取打印底图；调用方须先核验模板资源范围与 exam:manage。
// 复用签发和下载时的底图校验，不能以此入口下载客户端指定的任意文件。
func (s *PostgresStore) GetStudentPrintSource(ctx context.Context, tenantID, templateID string) (StudentPrintSourceContent, error) {
	var hash string
	var layout []byte
	if err := s.db.QueryRowContext(ctx, `SELECT content_hash,layout FROM answer_sheet_template
WHERE tenant_id=$1 AND id=$2::uuid AND status='locked' AND deleted_at IS NULL`, tenantID, templateID).Scan(&hash, &layout); err != nil {
		return StudentPrintSourceContent{}, mapNotFound(err)
	}
	background, err := s.readStudentPrintBackground(ctx, s.db, tenantID, templateID, hash, layout)
	if err != nil {
		return StudentPrintSourceContent{}, err
	}
	return StudentPrintSourceContent{ContentType: background.contentType, SHA256: background.sha256, Payload: background.payload}, nil
}

func (s *PostgresStore) readStudentPrintBackground(ctx context.Context, db printSourceQueryer, tenantID, templateID, templateHash string, layoutRaw []byte) (*studentPrintBackground, error) {
	if s.printObjects == nil {
		return nil, errors.New("answer sheet object storage is unavailable")
	}
	var assetID, bucket, key, contentType, hash string
	var size int64
	// 底图来自同租户、同场考试的锁定答题卡版本。数据存储只返回内部对象位置。
	err := db.QueryRowContext(ctx, `
SELECT f.id::text,f.storage_bucket,f.storage_key,f.content_type,f.hash_sha256,f.size_bytes
FROM answer_sheet_template t
JOIN exam_paper p ON p.tenant_id=t.tenant_id AND p.id=t.exam_paper_id AND p.exam_id=t.exam_id AND p.deleted_at IS NULL
JOIN file_asset f ON f.tenant_id=p.tenant_id AND f.id=p.file_asset_id AND f.deleted_at IS NULL AND f.lifecycle_status='active'
WHERE t.tenant_id=$1 AND t.id=$2::uuid AND t.content_hash=$3 AND t.status='locked' AND t.deleted_at IS NULL
`, tenantID, templateID, templateHash).Scan(&assetID, &bucket, &key, &contentType, &hash, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, printPackageInputError{"未找到锁定模板的答题卡底图，请检查答题卡来源。"}
	}
	if err != nil {
		return nil, err
	}
	if size <= 0 || size > maxStudentPrintSourceBytes {
		return nil, printPackageInputError{"答题卡底图为空或超过 100 MiB，请使用较小的答题卡底图。"}
	}
	var layout paper.TemplateLayout
	if err := json.Unmarshal(layoutRaw, &layout); err != nil {
		return nil, ErrInvalidTransition
	}
	if layout.EditorState != nil && !strings.EqualFold(layout.EditorState.SourceSHA256, hash) ||
		layout.OMRProfile.Reference != nil && (layout.OMRProfile.Reference.FileAssetID != assetID ||
			!strings.EqualFold(layout.OMRProfile.Reference.HashSHA256, hash)) {
		return nil, printPackageInputError{"答题卡底图与锁定版本不一致，请重新核对模板。"}
	}
	switch contentType {
	case "application/pdf", "image/png", "image/jpeg":
	default:
		return nil, printPackageInputError{"答题卡打印底图需使用 PDF、PNG 或 JPEG。"}
	}
	r, err := s.printObjects.Get(ctx, bucket, key)
	if err != nil {
		return nil, fmt.Errorf("open answer sheet source: %w", err)
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, size+1))
	if err != nil {
		return nil, fmt.Errorf("read answer sheet source: %w", err)
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != size || !strings.EqualFold(hex.EncodeToString(digest[:]), hash) {
		return nil, printPackageInputError{"答题卡底图内容已变更或损坏，请重新核对模板。"}
	}
	return &studentPrintBackground{contentType: contentType, payload: data, sha256: strings.ToLower(hash)}, nil
}
