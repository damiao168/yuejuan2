// 裁片维护先核对配准、来源和 ROI，再比较旧摘要；全部源页解码成功后才开始写入。
// 上传与 SQL 绑定仍按原顺序执行，单个失败不代表此前对象写入已回滚。
package main

import (
	"context"
	"database/sql"
	"fmt"
	"image"
	"path/filepath"
	"strings"
)

type cropRepairResult struct {
	DatasetID string `json:"dataset_id"`
	ExamID    string `json:"exam_id"`
	Found     int    `json:"found"`
	Updated   int    `json:"updated"`
	Skipped   int    `json:"skipped"`
}

type cropRepairTarget struct {
	segmentID     string
	candidate     string
	questionNo    string
	registration  string
	cropHash      string
	cropOwnerType string
	cropOwnerID   string
}

func repairExistingCrops(
	ctx context.Context,
	cfg config,
	data dataset,
	client *apiClient,
	db *sql.DB,
	tenantID string,
	examID string,
) (cropRepairResult, error) {
	result := cropRepairResult{DatasetID: datasetID, ExamID: examID}
	rows, err := db.QueryContext(ctx, `
SELECT seg.id::text,sub.candidate_no,q.question_no,COALESCE(seg.registration_run_id::text,''),
       COALESCE(seg.crop_sha256,''),COALESCE(fa.owner_type,''),COALESCE(fa.owner_id::text,'')
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
LEFT JOIN file_asset fa ON fa.tenant_id=seg.tenant_id AND fa.id=seg.crop_file_asset_id AND fa.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND seg.deleted_at IS NULL
ORDER BY sub.candidate_no,q.sort_order
`, tenantID, examID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	targets := make([]cropRepairTarget, 0, len(data.Candidates)*len(data.Questions))
	for rows.Next() {
		var item cropRepairTarget
		if err = rows.Scan(&item.segmentID, &item.candidate, &item.questionNo, &item.registration, &item.cropHash, &item.cropOwnerType, &item.cropOwnerID); err != nil {
			return result, err
		}
		targets = append(targets, item)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	result.Found = len(targets)
	want := len(data.Candidates) * len(data.Questions)
	if result.Found != want {
		return result, fmt.Errorf("found %d answer segments, want %d", result.Found, want)
	}

	// 全部源页先解码成功，再开始修复；重复考生编号仍以后一次记录覆盖。
	candidates, pageImages, err := loadRepairPageImages(cfg.datasetDir, data.Candidates)
	if err != nil {
		return result, err
	}

	for _, item := range targets {
		content, cropErr := prepareRepairCrop(item, candidates, data.Regions, pageImages)
		if cropErr != nil {
			return result, cropErr
		}
		hash := sha256Hex(content)
		if item.cropHash == hash && item.cropOwnerType == "answer_segment_crop" && item.cropOwnerID == item.registration {
			result.Skipped++
			continue
		}
		filename := strings.ToLower(item.candidate + "-" + item.questionNo + "-crop.png")
		asset, uploadErr := client.uploadBytes(ctx, filename, content, map[string]string{
			"owner_type": "answer_segment_crop",
			"owner_id":   item.registration,
			"exam_id":    examID,
		})
		if uploadErr != nil {
			return result, fmt.Errorf("upload %s/%s crop: %w", item.candidate, item.questionNo, uploadErr)
		}
		if err = attachSegmentCrop(ctx, db, tenantID, item.segmentID, asset); err != nil {
			return result, fmt.Errorf("attach %s/%s crop: %w", item.candidate, item.questionNo, err)
		}
		result.Updated++
	}
	return result, nil
}

func prepareRepairCrop(item cropRepairTarget, candidates map[string]candidateDef, regions map[string]map[string]region, pageImages map[string][]image.Image) ([]byte, error) {
	// 先核对配准身份和源页，再生成证据；即使旧哈希匹配也不能跳过这些前提。
	if item.registration == "" {
		return nil, fmt.Errorf("segment %s has no completed registration", item.segmentID)
	}
	candidate, ok := candidates[item.candidate]
	if !ok {
		return nil, fmt.Errorf("segment references unknown candidate %s", item.candidate)
	}
	itemRegion, ok := regions[item.candidate][item.questionNo]
	if !ok {
		return nil, fmt.Errorf("missing region for %s/%s", item.candidate, item.questionNo)
	}
	pageIndex := 0
	if questionNumber(item.questionNo) >= 17 {
		pageIndex = 1
	}
	if pageIndex >= len(candidate.PageFiles) {
		return nil, fmt.Errorf("missing page %d for %s/%s", pageIndex+1, item.candidate, item.questionNo)
	}
	content, _, err := cropAnswerRegion(pageImages[item.candidate][pageIndex], itemRegion, cropPadding)
	if err != nil {
		return nil, fmt.Errorf("crop %s/%s: %w", item.candidate, item.questionNo, err)
	}
	return content, nil
}

func loadRepairPageImages(datasetDir string, definitions []candidateDef) (map[string]candidateDef, map[string][]image.Image, error) {
	candidates := make(map[string]candidateDef, len(definitions))
	pageImages := make(map[string][]image.Image, len(definitions))
	for _, candidate := range definitions {
		candidates[candidate.CandidateNo] = candidate
		images := make([]image.Image, len(candidate.PageFiles))
		for index, relativePath := range candidate.PageFiles {
			var err error
			images[index], err = decodeImage(filepath.Join(datasetDir, filepath.FromSlash(relativePath)))
			if err != nil {
				return nil, nil, fmt.Errorf("decode %s page %d: %w", candidate.CandidateNo, index+1, err)
			}
		}
		pageImages[candidate.CandidateNo] = images
	}
	return candidates, pageImages, nil
}
