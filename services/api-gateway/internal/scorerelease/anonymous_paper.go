package scorerelease

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"mime"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/binaryresource"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"github.com/google/uuid"
)

const anonymousRedactionVersion = "identity-regions-v1"
const maxAnonymousSourceBytes int64 = 40 << 20
const maxAnonymousPixels = 40_000_000

// HighScorePaperManager is the only route from a published release to peer
// page bytes. The manager stores a new PNG with its own object and asset ID.
type HighScorePaperManager interface {
	Prepare(context.Context, string, string, string) error
	Revoke(context.Context, string, string, string) error
	ReadAnonymousPage(context.Context, string, string, string, string) (binaryresource.Resource, error)
}

type PostgresHighScorePaperManager struct {
	db      *sql.DB
	files   files.Store
	objects files.ObjectStorage
	bucket  string
}

func NewPostgresHighScorePaperManager(db *sql.DB, fileStore files.Store, objects files.ObjectStorage, bucket string) *PostgresHighScorePaperManager {
	return &PostgresHighScorePaperManager{db: db, files: fileStore, objects: objects, bucket: bucket}
}

type anonymousSourcePage struct {
	pageID, sourceAssetID, bucket, key, sourceHash, contentType, templateID, templateHash string
	pageNo                                                                                int
	size                                                                                  int64
	layout                                                                                paper.TemplateLayout
}

func (m *PostgresHighScorePaperManager) Prepare(ctx context.Context, tenantID, releaseID, actorID string) error {
	if m == nil || m.db == nil || m.files == nil || m.objects == nil || m.bucket == "" {
		return ErrAnonymousPaperUnavailable
	}
	var examID, submissionID, status string
	err := m.db.QueryRowContext(ctx, `
SELECT release.exam_id::text, item.submission_id::text, release.status
FROM score_release release
JOIN LATERAL (
  SELECT submission_id FROM score_release_item
  WHERE tenant_id=release.tenant_id AND release_id=release.id AND student_id IS NOT NULL
  ORDER BY total_score DESC, submission_id LIMIT 1
) item ON true
WHERE release.tenant_id=$1::uuid AND release.id=$2::uuid
  AND COALESCE((release.visibility_policy->>'show_high_score_paper')::boolean,false)
  AND COALESCE((release.visibility_policy->>'show_question_scores')::boolean,false)
`, tenantID, releaseID).Scan(&examID, &submissionID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAnonymousPaperUnavailable
	}
	if err != nil {
		return err
	}
	if status != StatusDraft {
		return ErrInvalidTransition
	}
	pages, err := m.sourcePages(ctx, tenantID, examID, submissionID)
	if err != nil || len(pages) == 0 {
		return ErrAnonymousPaperUnavailable
	}
	// Validate every page before writing even one derived asset. Missing or
	// ambiguous identity configuration blocks the entire release.
	for _, page := range pages {
		if !validAnonymousLayout(page) {
			return ErrAnonymousPaperUnavailable
		}
	}
	for _, page := range pages {
		if err := m.preparePage(ctx, tenantID, examID, releaseID, actorID, page); err != nil {
			return err
		}
	}
	return nil
}

func (m *PostgresHighScorePaperManager) sourcePages(ctx context.Context, tenantID, examID, submissionID string) ([]anonymousSourcePage, error) {
	rows, err := m.db.QueryContext(ctx, `
SELECT sp.id::text,sp.page_no,fa.id::text,fa.storage_bucket,fa.storage_key,
       fa.hash_sha256,fa.size_bytes,fa.content_type,t.id::text,t.content_hash,t.layout
FROM submission_page sp
JOIN LATERAL (
  SELECT r.* FROM page_registration_run r
  WHERE r.tenant_id=sp.tenant_id AND r.submission_page_id=sp.id AND r.deleted_at IS NULL
  ORDER BY r.created_at DESC,r.id DESC LIMIT 1
) r ON r.processing_status='completed' AND r.match_status='matched'
    AND r.registered_file_asset_id IS NOT NULL AND r.page_no=sp.page_no
JOIN file_asset fa ON fa.tenant_id=sp.tenant_id AND fa.id=r.registered_file_asset_id
    AND fa.exam_id=$3::uuid AND fa.deleted_at IS NULL AND fa.lifecycle_status='active'
JOIN answer_sheet_template t ON t.tenant_id=r.tenant_id AND t.id=r.template_id
    AND t.content_hash=r.template_content_hash AND t.status='locked' AND t.deleted_at IS NULL
WHERE sp.tenant_id=$1::uuid AND sp.submission_id=$2::uuid
  AND sp.deleted_at IS NULL AND sp.status='accepted'
ORDER BY sp.page_no
`, tenantID, submissionID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pages := []anonymousSourcePage{}
	for rows.Next() {
		var page anonymousSourcePage
		var raw []byte
		if err := rows.Scan(&page.pageID, &page.pageNo, &page.sourceAssetID, &page.bucket, &page.key,
			&page.sourceHash, &page.size, &page.contentType, &page.templateID, &page.templateHash, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &page.layout); err != nil {
			return nil, err
		}
		pages = append(pages, page)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var acceptedCount int
	if err := m.db.QueryRowContext(ctx, `SELECT count(*) FROM submission_page WHERE tenant_id=$1::uuid AND submission_id=$2::uuid AND deleted_at IS NULL AND status='accepted'`, tenantID, submissionID).Scan(&acceptedCount); err != nil {
		return nil, err
	}
	if acceptedCount == 0 || acceptedCount != len(pages) {
		return nil, ErrAnonymousPaperUnavailable
	}
	return pages, nil
}

func validAnonymousLayout(page anonymousSourcePage) bool {
	if page.pageNo <= 0 || page.size <= 0 || page.size > maxAnonymousSourceBytes || len(page.sourceHash) != 64 ||
		!strings.HasPrefix(page.contentType, "image/") || page.templateID == "" || page.templateHash == "" {
		return false
	}
	for _, templatePage := range page.layout.Pages {
		if templatePage.PageNo != page.pageNo {
			continue
		}
		if len(templatePage.IdentityRegions) == 0 || templatePage.Width <= 0 || templatePage.Height <= 0 {
			return false
		}
		for _, region := range templatePage.IdentityRegions {
			if !validIdentityRegion(region) {
				return false
			}
		}
		return true
	}
	return false
}

func validIdentityRegion(region paper.LayoutRegion) bool {
	values := []float64{region.X, region.Y, region.Width, region.Height}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return region.X >= 0 && region.Y >= 0 && region.Width > 0 && region.Height > 0 &&
		region.X+region.Width <= 1.000001 && region.Y+region.Height <= 1.000001
}

func (m *PostgresHighScorePaperManager) preparePage(ctx context.Context, tenantID, examID, releaseID, actorID string, page anonymousSourcePage) error {
	var existingSourceHash, existingTemplateHash string
	err := m.db.QueryRowContext(ctx, `SELECT source_sha256,template_content_hash FROM score_release_anonymous_page
WHERE tenant_id=$1::uuid AND release_id=$2::uuid AND source_submission_page_id=$3::uuid AND revoked_at IS NULL`,
		tenantID, releaseID, page.pageID).Scan(&existingSourceHash, &existingTemplateHash)
	if err == nil {
		if existingSourceHash == page.sourceHash && existingTemplateHash == page.templateHash {
			return nil
		}
		return ErrAnonymousPaperUnavailable
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	reader, err := m.objects.Get(ctx, page.bucket, page.key)
	if err != nil {
		return ErrAnonymousPaperUnavailable
	}
	sourceBytes, err := io.ReadAll(io.LimitReader(reader, maxAnonymousSourceBytes+1))
	closeErr := reader.Close()
	if err != nil || closeErr != nil || int64(len(sourceBytes)) != page.size || int64(len(sourceBytes)) > maxAnonymousSourceBytes || sha256Hex(sourceBytes) != page.sourceHash {
		return ErrAnonymousPaperUnavailable
	}
	anonymousBytes, err := redactAnonymousPNG(sourceBytes, page)
	if err != nil {
		return ErrAnonymousPaperUnavailable
	}
	pageID := uuid.NewString()
	key := fmt.Sprintf("tenant/%s/score-release/%s/anonymous/%s.png", tenantID, releaseID, pageID)
	if err := m.objects.Put(ctx, m.bucket, key, bytes.NewReader(anonymousBytes), int64(len(anonymousBytes)), "image/png"); err != nil {
		return err
	}
	asset, err := m.files.Create(ctx, files.CreateAssetInput{
		TenantID: tenantID, ExamID: examID, OwnerType: "score_release_anonymous_page", OwnerID: pageID,
		OriginalName: "anonymous-paper-page.png", ContentType: "image/png", SizeBytes: int64(len(anonymousBytes)),
		HashSHA256: sha256Hex(anonymousBytes), StorageBucket: m.bucket, StorageKey: key,
		Visibility: "private", UploadedBy: actorID,
	})
	if err != nil {
		// 对象已写入但资产登记失败时尝试清理；清理错误不覆盖本次登记错误。
		_ = m.objects.Remove(ctx, m.bucket, key)
		return err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		_, _ = m.files.Delete(ctx, tenantID, asset.ID)
		_ = m.objects.Remove(ctx, m.bucket, key)
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO score_release_anonymous_page
(id,tenant_id,release_id,source_submission_page_id,page_no,file_asset_id,source_sha256,derived_sha256,template_id,template_content_hash,redaction_version,created_by)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,$8,$9::uuid,$10,$11,$12::uuid)`,
		pageID, tenantID, releaseID, page.pageID, page.pageNo, asset.ID, page.sourceHash, asset.HashSHA256,
		page.templateID, page.templateHash, anonymousRedactionVersion, actorID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE file_asset SET legal_hold=true,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND lifecycle_status='active' AND deleted_at IS NULL`, tenantID, asset.ID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_, _ = m.files.Delete(ctx, tenantID, asset.ID)
		_ = m.objects.Remove(ctx, m.bucket, key)
		return err
	}
	return nil
}

func redactAnonymousPNG(source []byte, page anonymousSourcePage) ([]byte, error) {
	// 身份区域按模板中的归一化坐标遮白，再重新编码 PNG，原文件附带的元数据不会复制过去。
	config, _, err := image.DecodeConfig(bytes.NewReader(source))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxAnonymousPixels {
		return nil, ErrAnonymousPaperUnavailable
	}
	decoded, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		return nil, err
	}
	pageBounds := decoded.Bounds()
	output := image.NewRGBA(image.Rect(0, 0, pageBounds.Dx(), pageBounds.Dy()))
	draw.Draw(output, output.Bounds(), decoded, pageBounds.Min, draw.Src)
	for _, templatePage := range page.layout.Pages {
		if templatePage.PageNo != page.pageNo {
			continue
		}
		for _, region := range templatePage.IdentityRegions {
			// Expand by four pixels to cover anti-aliasing and small registration
			// differences at the configured boundary.
			x0 := max(0, int(math.Floor(region.X*float64(output.Bounds().Dx())))-4)
			y0 := max(0, int(math.Floor(region.Y*float64(output.Bounds().Dy())))-4)
			x1 := min(output.Bounds().Dx(), int(math.Ceil((region.X+region.Width)*float64(output.Bounds().Dx())))+4)
			y1 := min(output.Bounds().Dy(), int(math.Ceil((region.Y+region.Height)*float64(output.Bounds().Dy())))+4)
			draw.Draw(output, image.Rect(x0, y0, x1, y1), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
		}
		var result bytes.Buffer
		if err := png.Encode(&result, output); err != nil {
			return nil, err
		}
		return result.Bytes(), nil
	}
	return nil, ErrAnonymousPaperUnavailable
}

func (m *PostgresHighScorePaperManager) Revoke(ctx context.Context, tenantID, releaseID, actorID string) error {
	if m == nil || m.db == nil {
		return ErrAnonymousPaperUnavailable
	}
	var status string
	err := m.db.QueryRowContext(ctx, `SELECT status FROM score_release WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, releaseID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != StatusPublished {
		return ErrInvalidTransition
	}
	_, err = m.db.ExecContext(ctx, `UPDATE score_release_anonymous_page SET revoked_by=$3::uuid,revoked_at=now()
WHERE tenant_id=$1::uuid AND release_id=$2::uuid AND revoked_at IS NULL`, tenantID, releaseID, actorID)
	return err
}

func (m *PostgresHighScorePaperManager) ReadAnonymousPage(ctx context.Context, tenantID, releaseID, pageID, assetID string) (binaryresource.Resource, error) {
	if m == nil || m.db == nil || m.files == nil || m.objects == nil {
		return binaryresource.Resource{}, ErrAnonymousPaperUnavailable
	}
	var derivedHash string
	err := m.db.QueryRowContext(ctx, `SELECT ap.derived_sha256 FROM score_release_anonymous_page ap
JOIN score_release_current current_release ON current_release.tenant_id=ap.tenant_id AND current_release.release_id=ap.release_id
JOIN score_release release ON release.tenant_id=ap.tenant_id AND release.id=ap.release_id AND release.status='published'
WHERE ap.tenant_id=$1::uuid AND ap.release_id=$2::uuid AND ap.id=$3::uuid AND ap.file_asset_id=$4::uuid
  AND ap.revoked_at IS NULL AND COALESCE((release.visibility_policy->>'show_high_score_paper')::boolean,false)`,
		tenantID, releaseID, pageID, assetID).Scan(&derivedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return binaryresource.Resource{}, ErrNotFound
	}
	if err != nil {
		return binaryresource.Resource{}, err
	}
	asset, err := m.files.Get(ctx, tenantID, assetID)
	if err != nil || asset.OwnerType != "score_release_anonymous_page" || asset.OwnerID != pageID ||
		asset.ContentType != "image/png" || asset.HashSHA256 != derivedHash || asset.Lifecycle != files.LifecycleActive ||
		asset.DeletedAt != nil || !asset.LegalHold || asset.SizeBytes <= 0 {
		return binaryresource.Resource{}, ErrAnonymousPaperUnavailable
	}
	return binaryresource.Resource{
		ContentType: "image/png", Size: asset.SizeBytes,
		Disposition:  mime.FormatMediaType("inline", map[string]string{"filename": "anonymous-paper-page.png"}),
		CacheControl: "private, no-store",
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			body, err := m.objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
			if err != nil {
				return nil, err
			}
			data, readErr := io.ReadAll(io.LimitReader(body, asset.SizeBytes+1))
			closeErr := body.Close()
			if readErr != nil || closeErr != nil || int64(len(data)) != asset.SizeBytes || sha256Hex(data) != derivedHash {
				return nil, ErrAnonymousPaperUnavailable
			}
			return io.NopCloser(bytes.NewReader(data)), nil
		},
		OpenErrorMessage: "failed to read anonymous paper page",
		AuditAction:      "student.anonymous_paper_page_viewed", AuditTargetType: "score_release_anonymous_page", AuditTargetID: pageID,
		AuditReason: "view released anonymous high-score paper page",
	}, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
