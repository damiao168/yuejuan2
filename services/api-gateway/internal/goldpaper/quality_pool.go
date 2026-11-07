package goldpaper

import (
	"context"
	"database/sql"
	"encoding/hex"
)

const (
	PoolCalibration = "calibration"
	PoolBlind       = "blind"
)

// 首次提名按内容分池；同一标准卷的新版本可沿用原池，已经登记的图像始终保持首次用途。
func imageQualityPool(hash string) string {
	raw, err := hex.DecodeString(hash)
	if err != nil || len(raw) != 32 || raw[0]%2 == 0 {
		return PoolCalibration
	}
	return PoolBlind
}

// preferred 只用于沿用标准卷的原池；冲突时返回首次登记的池，由调用方拒绝用途不符的新版本。
func registerImagePoolTx(ctx context.Context, tx *sql.Tx, tenant, hash, preferred string) (string, error) {
	if preferred == "" {
		preferred = imageQualityPool(hash)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gold_quality_image_pool(tenant_id,hash_sha256,quality_pool) VALUES($1::uuid,$2,$3) ON CONFLICT(tenant_id,hash_sha256) DO NOTHING`, tenant, hash, preferred); err != nil {
		return "", err
	}
	var pool string
	err := tx.QueryRowContext(ctx, `SELECT quality_pool FROM gold_quality_image_pool WHERE tenant_id=$1::uuid AND hash_sha256=$2`, tenant, hash).Scan(&pool)
	return pool, err
}

func (s *MemoryStore) registerImagePoolLocked(tenant, hash, preferred string) string {
	key := tenant + "|" + hash
	if pool := s.imagePools[key]; pool != "" {
		return pool
	}
	if preferred == "" {
		preferred = imageQualityPool(hash)
	}
	s.imagePools[key] = preferred
	return preferred
}
