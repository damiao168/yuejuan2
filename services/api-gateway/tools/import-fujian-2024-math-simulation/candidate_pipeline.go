// 单个考生先完成加页、质量版本校验、配准与裁片，再播种合成 OCR 并记录答案。
// 各阶段失败立即停止，不能越过失败阶段继续创建评分事实。
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
)

type pageRecord struct {
	ID             string
	PageNo         int
	FileAsset      map[string]any
	Path           string
	Image          image.Image
	RegistrationID string
}

type candidateImport struct {
	cfg               config
	data              dataset
	client            *apiClient
	db                *sql.DB
	users             importUsers
	roster            importRoster
	questions         importQuestionBindings
	examID            string
	captureBatchID    string
	templateID        string
	templateHash      string
	ocrStore          *ocrpkg.PostgresStore
	candidate         candidateDef
	candidateIndex    int
	submissionID      string
	pages             []pageRecord
	segmentByQuestion map[string]string
}

func (run *candidateImport) run(ctx context.Context, result *importResult, aiTargets *[]aiTarget) error {
	// 页面配准、裁剪和 OCR 播种依次完成，之后才记录答案并分流评分。
	if err := run.createPages(ctx); err != nil {
		return err
	}
	if err := run.prepareSegments(ctx, result); err != nil {
		return err
	}
	if err := run.registerPages(ctx); err != nil {
		return err
	}
	if err := run.attachCrops(ctx); err != nil {
		return err
	}
	if err := run.seedOCR(ctx, result); err != nil {
		return err
	}
	return run.recordAnswers(ctx, result, aiTargets)
}

func (run *candidateImport) createPages(ctx context.Context) error {
	candidate, client, cfg := run.candidate, run.client, run.cfg
	examID, schoolID, studentIDs := run.examID, run.roster.schoolID, run.roster.studentIDs
	submissionResponse, createErr := client.json(ctx, http.MethodPost, examRoutePrefix+examID+"/submissions", map[string]any{
		"student_id":          studentIDs[candidate.CandidateNo],
		"candidate_no":        candidate.CandidateNo,
		"source_type":         "scanner_upload",
		"expected_page_count": 2,
	}, http.StatusCreated)
	if createErr != nil {
		return fmt.Errorf("create submission %s: %w", candidate.CandidateNo, createErr)
	}
	submissionID := nestedStringField(submissionResponse, "submission", "id")
	pages := make([]pageRecord, 0, 2)
	for pageIndex, relativePath := range candidate.PageFiles {
		pageNo := pageIndex + 1
		sourcePath := filepath.Join(cfg.datasetDir, filepath.FromSlash(relativePath))
		sourceImage, decodeErr := decodeImage(sourcePath)
		if decodeErr != nil {
			return fmt.Errorf("decode %s page %d: %w", candidate.CandidateNo, pageNo, decodeErr)
		}
		sourceAsset, uploadErr := client.upload(ctx, sourcePath, map[string]string{
			"owner_type":    "submission",
			"owner_id":      submissionID,
			"submission_id": submissionID,
			"exam_id":       examID,
			"school_id":     schoolID,
		})
		if uploadErr != nil {
			return fmt.Errorf("upload %s page %d: %w", candidate.CandidateNo, pageNo, uploadErr)
		}
		pageResponse, pageErr := client.json(ctx, http.MethodPost, submissionRoutePrefix+submissionID+"/pages", map[string]any{
			"file_asset_id": stringField(sourceAsset, "id"),
			"page_no":       pageNo,
		}, http.StatusCreated)
		if pageErr != nil {
			return fmt.Errorf("add %s page %d: %w", candidate.CandidateNo, pageNo, pageErr)
		}
		pages = append(pages, pageRecord{
			ID:        nestedStringField(pageResponse, "page", "id"),
			PageNo:    pageNo,
			FileAsset: sourceAsset,
			Path:      sourcePath,
			Image:     sourceImage,
		})
	}
	run.submissionID, run.pages = submissionID, pages
	return nil
}

func (run *candidateImport) prepareSegments(ctx context.Context, result *importResult) error {
	candidate, client, submissionID, data := run.candidate, run.client, run.submissionID, run.data
	integrityResponse, integrityErr := client.json(ctx, http.MethodPost, submissionRoutePrefix+submissionID+"/quality-check", map[string]any{}, http.StatusOK)
	if integrityErr != nil {
		return fmt.Errorf("quality-check %s: %w", candidate.CandidateNo, integrityErr)
	}
	if integrityResult := nestedMap(integrityResponse, "result"); !boolField(integrityResult, "valid") {
		return fmt.Errorf("collection integrity failed for %s: %v", candidate.CandidateNo, integrityResult["issues"])
	}
	if _, createErr := client.json(ctx, http.MethodPost, submissionRoutePrefix+submissionID+"/run-quality-check", map[string]any{}, http.StatusAccepted); createErr != nil {
		return fmt.Errorf("run image quality check %s: %w", candidate.CandidateNo, createErr)
	}
	revision, createErr := waitForSubmissionImageQuality(ctx, client, submissionID)
	if createErr != nil {
		return fmt.Errorf("image quality %s: %w", candidate.CandidateNo, createErr)
	}
	// 加页和质量回执都会推进修订号；只转换刚确认通过质量检查的版本，冲突时不能越过新事实重试。
	if _, createErr := client.json(ctx, http.MethodPost, submissionRoutePrefix+submissionID+"/status", map[string]any{"status": "ready_for_ocr", "expected_revision": revision}, http.StatusOK); createErr != nil {
		return fmt.Errorf("mark %s ready for OCR: %w", candidate.CandidateNo, createErr)
	}
	segmentResponse, segmentErr := client.json(ctx, http.MethodPost, submissionRoutePrefix+submissionID+"/segment-answers", map[string]any{}, http.StatusOK)
	if segmentErr != nil {
		return fmt.Errorf("generate segments for %s: %w", candidate.CandidateNo, segmentErr)
	}
	segments := sliceOfMaps(nestedMap(segmentResponse, "result")["segments"])
	if len(segments) != len(data.Questions) {
		return fmt.Errorf("%s generated %d segments, want %d", candidate.CandidateNo, len(segments), len(data.Questions))
	}
	result.AnswerSegmentCount += len(segments)
	segmentByQuestion := map[string]string{}
	for _, segment := range segments {
		segmentByQuestion[stringField(segment, "question_no")] = stringField(segment, "id")
	}
	run.segmentByQuestion = segmentByQuestion
	return nil
}

func (run *candidateImport) registerPages(ctx context.Context) error {
	pages, candidate, client, db := run.pages, run.candidate, run.client, run.db
	tenantID, adminID, examID := run.users.tenantID, run.users.adminID, run.examID
	captureBatchID, submissionID := run.captureBatchID, run.submissionID
	templateID, templateHash := run.templateID, run.templateHash
	for pageIndex := range pages {
		page := &pages[pageIndex]
		registrationID, registrationErr := createRegistration(ctx, db, registrationCreateInput{
			tenantID: tenantID, adminID: adminID, captureBatchID: captureBatchID,
			submissionID: submissionID, page: *page, templateID: templateID,
			templateHash: templateHash, candidateNo: candidate.CandidateNo,
		})
		if registrationErr != nil {
			return registrationErr
		}
		page.RegistrationID = registrationID
		registeredAsset, uploadErr := client.upload(ctx, page.Path, map[string]string{
			"owner_type":    "page_registration_output",
			"owner_id":      registrationID,
			"exam_id":       examID,
			"submission_id": submissionID,
		})
		if uploadErr != nil {
			return fmt.Errorf("upload registered page: %w", uploadErr)
		}
		if registrationErr = completeRegistration(ctx, db, registrationCompleteInput{
			tenantID: tenantID, submissionID: submissionID, submissionPageID: page.ID,
			registrationID: registrationID, templateID: templateID, templateHash: templateHash,
			registered: registeredAsset,
		}); registrationErr != nil {
			return registrationErr
		}
	}
	return nil
}

func (run *candidateImport) attachCrops(ctx context.Context) error {
	data, candidate, pages, client, db := run.data, run.candidate, run.pages, run.client, run.db
	segmentByQuestion, tenantID, examID, submissionID := run.segmentByQuestion, run.users.tenantID, run.examID, run.submissionID
	for _, question := range data.Questions {
		segmentID := segmentByQuestion[question.QuestionNo]
		itemRegion, ok := data.Regions[candidate.CandidateNo][question.QuestionNo]
		if !ok {
			return fmt.Errorf("missing region for %s/%s", candidate.CandidateNo, question.QuestionNo)
		}
		pageIndex := 0
		if questionNumber(question.QuestionNo) >= 17 {
			pageIndex = 1
		}
		cropContent, _, cropErr := cropAnswerRegion(pages[pageIndex].Image, itemRegion, cropPadding)
		if cropErr != nil {
			return fmt.Errorf("crop %s/%s: %w", candidate.CandidateNo, question.QuestionNo, cropErr)
		}
		filename := strings.ToLower(candidate.CandidateNo + "-" + question.QuestionNo + "-crop.png")
		cropAsset, uploadErr := client.uploadBytes(ctx, filename, cropContent, map[string]string{
			"owner_type":    "answer_segment_crop",
			"owner_id":      pages[pageIndex].RegistrationID,
			"exam_id":       examID,
			"submission_id": submissionID,
		})
		if uploadErr != nil {
			return fmt.Errorf("upload %s/%s crop: %w", candidate.CandidateNo, question.QuestionNo, uploadErr)
		}
		if cropErr = attachSegmentCrop(ctx, db, tenantID, segmentID, cropAsset); cropErr != nil {
			return fmt.Errorf("attach %s/%s crop: %w", candidate.CandidateNo, question.QuestionNo, cropErr)
		}
	}
	return nil
}

func (run *candidateImport) seedOCR(ctx context.Context, result *importResult) error {
	data, candidate, pages := run.data, run.candidate, run.pages
	ocrStore, tenantID, submissionID, adminID := run.ocrStore, run.users.tenantID, run.submissionID, run.users.adminID
	candidateIndex := run.candidateIndex
	ocrResults := []ocrpkg.ResultInput{}
	for _, question := range data.Questions {
		number := questionNumber(question.QuestionNo)
		if number <= 10 {
			continue
		}
		answerData := candidate.Answers[question.QuestionNo]
		if displayText(answerData.Text) == "" {
			continue
		}
		itemRegion := data.Regions[candidate.CandidateNo][question.QuestionNo]
		page := pages[0]
		if number >= 17 {
			page = pages[1]
		}
		ocrResults = append(ocrResults, ocrpkg.ResultInput{
			SubmissionPageID:  page.ID,
			Text:              displayText(answerData.Text),
			BBox:              []float64{itemRegion.X, itemRegion.Y, itemRegion.Width, itemRegion.Height},
			Confidence:        answerData.Confidence,
			SourceImageFileID: stringField(page.FileAsset, "id"),
		})
	}
	inputHash := sha256Hex([]byte(candidate.CandidateNo + ":" + datasetID))
	completedOCR, ocrErr := ocrStore.SeedCompletedTask(
		ctx,
		tenantID,
		submissionID,
		adminID,
		ocrpkg.CreateTaskInput{
			Engine:         "paddleocr",
			EngineVersion:  "simulation-seeded-v1",
			MinConfidence:  0.85,
			IdempotencyKey: "simulation:" + datasetID + ":" + candidate.CandidateNo + ":ocr:v1",
		},
		ocrpkg.CompleteTaskInput{
			Results:           ocrResults,
			ModelVersion:      "simulation-seeded-transcript-v1",
			ConfigHash:        sha256Hex([]byte("simulation-ocr-config-v1")),
			InputHash:         inputHash,
			DurationMS:        300 + candidateIndex*45,
			WorkerID:          "simulation-importer",
			PreprocessProfile: "synthetic-handwriting-scan-v1",
		},
	)
	if ocrErr != nil {
		return fmt.Errorf("atomically seed OCR task for %s: %w", candidate.CandidateNo, ocrErr)
	}
	result.CompletedOCRTaskCount++
	result.SeededOCRResultCount += completedOCR.ResultCount
	return nil
}

func waitForSubmissionImageQuality(ctx context.Context, client *apiClient, submissionID string) (int, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Minute)
	defer timeout.Stop()
	for {
		response, err := client.json(ctx, http.MethodGet, submissionRoutePrefix+submissionID, nil, http.StatusOK)
		if err != nil {
			return 0, err
		}
		item := nestedMap(response, "submission")
		switch stringField(item, "quality_status") {
		case "passed":
			revision := intField(item, "revision")
			if revision < 1 || numberField(item, "revision") != float64(revision) {
				return 0, errors.New("passed submission lacks a valid revision")
			}
			return revision, nil
		case "review", "failed":
			return 0, fmt.Errorf("submission requires operator action: status=%s issues=%v", stringField(item, "quality_status"), item["quality_issues"])
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-timeout.C:
			return 0, errors.New("timed out waiting for image quality worker")
		case <-ticker.C:
		}
	}
}
