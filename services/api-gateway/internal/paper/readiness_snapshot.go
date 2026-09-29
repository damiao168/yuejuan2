package paper

import "sort"

const readinessSnapshotVersion = 1

// readinessConfigurationSnapshot is deliberately separate from persistence
// DTOs. Adding timestamps, audit fields, storage locations, or import
// provenance must not invalidate a confirmed exam configuration.
type readinessConfigurationSnapshot struct {
	Version    int                         `json:"version"`
	Total      float64                     `json:"total"`
	ClassIDs   []string                    `json:"class_ids"`
	Candidates []string                    `json:"candidate_ids"`
	Papers     []readinessPaperSnapshot    `json:"papers"`
	Questions  []readinessQuestionSnapshot `json:"questions"`
	Template   *readinessTemplateSnapshot  `json:"locked_template,omitempty"`
}

type readinessPaperSnapshot struct {
	ID          string `json:"id"`
	FileAssetID string `json:"file_asset_id"`
	VersionNo   int    `json:"version_no"`
	Status      string `json:"status"`
	FileSHA256  string `json:"file_sha256"`
}

type readinessQuestionSnapshot struct {
	ParentQuestionNo    string                     `json:"parent_question_no,omitempty"`
	SubquestionNo       string                     `json:"subquestion_no,omitempty"`
	Options             []string                   `json:"options,omitempty"`
	BankContent         map[string]any             `json:"bank_content,omitempty"`
	SourceContentHash   string                     `json:"source_content_hash,omitempty"`
	ID                  string                     `json:"id"`
	ExamPaperID         string                     `json:"exam_paper_id"`
	QuestionNo          string                     `json:"question_no"`
	QuestionType        string                     `json:"question_type"`
	AssessmentArchetype string                     `json:"assessment_archetype"`
	Score               float64                    `json:"score"`
	Stem                string                     `json:"stem"`
	KnowledgePoints     []string                   `json:"knowledge_points"`
	AnswerArea          map[string]any             `json:"answer_area"`
	SortOrder           int                        `json:"sort_order"`
	Status              string                     `json:"status"`
	Answer              *readinessAnswerSnapshot   `json:"answer,omitempty"`
	Solution            *readinessSolutionSnapshot `json:"solution,omitempty"`
	Rubric              *readinessRubricSnapshot   `json:"rubric,omitempty"`
}

// readinessImportSnapshot is the content-only companion to a confirmed
// readiness record. It intentionally omits candidates, papers and answer-area
// coordinates: historical question imports need frozen authored facts, not
// student identities or the old paper layout.
type readinessImportSnapshot struct {
	Version           int                               `json:"version"`
	ConfigurationHash string                            `json:"configuration_hash"`
	Questions         []readinessImportQuestionSnapshot `json:"questions"`
}

type readinessImportQuestionSnapshot struct {
	ParentQuestionNo        string                     `json:"parent_question_no,omitempty"`
	SubquestionNo           string                     `json:"subquestion_no,omitempty"`
	Options                 []string                   `json:"options,omitempty"`
	ID                      string                     `json:"id"`
	AssessmentSnapshotID    string                     `json:"assessment_snapshot_id"`
	AssessmentSnapshotHash  string                     `json:"assessment_snapshot_hash"`
	QuestionNo              string                     `json:"question_no"`
	QuestionType            string                     `json:"question_type"`
	AssessmentArchetype     string                     `json:"assessment_archetype"`
	Score                   float64                    `json:"score"`
	Stem                    string                     `json:"stem"`
	KnowledgePoints         []string                   `json:"knowledge_points"`
	SourceType              string                     `json:"source_type,omitempty"`
	SourceBankItemID        string                     `json:"source_bank_item_id,omitempty"`
	SourceBankItemVersionID string                     `json:"source_bank_item_version_id,omitempty"`
	SourceContentHash       string                     `json:"source_content_hash,omitempty"`
	BankContent             map[string]any             `json:"bank_content,omitempty"`
	Answer                  *readinessAnswerSnapshot   `json:"answer,omitempty"`
	Solution                *readinessSolutionSnapshot `json:"solution,omitempty"`
	Rubric                  *readinessRubricSnapshot   `json:"rubric,omitempty"`
}

type readinessAnswerSnapshot struct {
	StandardAnswer    any   `json:"standard_answer"`
	EquivalentAnswers []any `json:"equivalent_answers"`
	Tolerance         any   `json:"tolerance"`
}

type readinessSolutionSnapshot struct {
	RawText            string         `json:"raw_text"`
	Steps              []SolutionStep `json:"steps"`
	VerificationStatus string         `json:"verification_status"`
}

type readinessRubricSnapshot struct {
	Status     string        `json:"status"`
	MaxScore   float64       `json:"max_score"`
	Points     []RubricPoint `json:"points"`
	Deductions []any         `json:"deductions"`
	Examples   []any         `json:"examples"`
}

type readinessTemplateSnapshot struct {
	ID          string                  `json:"id"`
	ExamPaperID string                  `json:"exam_paper_id"`
	VersionNo   int                     `json:"version_no"`
	PageCount   int                     `json:"page_count"`
	OMRProfile  TemplateOMRProfile      `json:"omr_profile"`
	Pages       []readinessTemplatePage `json:"pages"`
}

type readinessTemplatePage struct {
	PageNo            int                       `json:"page_no"`
	Width             int                       `json:"width"`
	Height            int                       `json:"height"`
	RegistrationMarks []readinessTemplateRegion `json:"registration_marks"`
	IdentityRegions   []readinessTemplateRegion `json:"identity_regions"`
	QuestionRegions   []readinessTemplateRegion `json:"question_regions"`
}

type readinessTemplateRegion struct {
	QuestionID    string                    `json:"question_id,omitempty"`
	Label         string                    `json:"label,omitempty"`
	X             float64                   `json:"x"`
	Y             float64                   `json:"y"`
	Width         float64                   `json:"width"`
	Height        float64                   `json:"height"`
	OptionRegions []readinessTemplateOption `json:"option_regions,omitempty"`
}

type readinessTemplateOption struct {
	Label  string  `json:"label"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func newReadinessConfigurationSnapshot(total float64, classIDs, candidateIDs []string, papers []Paper, questions []Question, locked *AnswerSheetTemplate) readinessConfigurationSnapshot {
	snapshot := readinessConfigurationSnapshot{
		Version: readinessSnapshotVersion, Total: total,
		ClassIDs: append([]string(nil), classIDs...), Candidates: append([]string(nil), candidateIDs...),
	}
	sort.Strings(snapshot.ClassIDs)
	sort.Strings(snapshot.Candidates)
	for _, item := range papers {
		snapshot.Papers = append(snapshot.Papers, readinessPaperSnapshot{
			ID: item.ID, FileAssetID: item.FileAssetID, VersionNo: item.VersionNo,
			Status: item.Status, FileSHA256: item.File.HashSHA256,
		})
	}
	sort.Slice(snapshot.Papers, func(i, j int) bool {
		if snapshot.Papers[i].VersionNo == snapshot.Papers[j].VersionNo {
			return snapshot.Papers[i].ID < snapshot.Papers[j].ID
		}
		return snapshot.Papers[i].VersionNo < snapshot.Papers[j].VersionNo
	})
	for _, item := range questions {
		if item.SourceType == "question_bank" {
			snapshot.Version = 2
		}
		question := readinessQuestionSnapshot{
			ParentQuestionNo: item.ParentQuestionNo, SubquestionNo: item.SubquestionNo, Options: append([]string(nil), item.Options...),
			BankContent: item.BankContent, SourceContentHash: item.SourceContentHash,
			ID: item.ID, ExamPaperID: item.ExamPaperID, QuestionNo: item.QuestionNo,
			QuestionType: item.QuestionType, AssessmentArchetype: readinessAssessmentArchetype(item),
			Score: item.Score, Stem: item.Stem, KnowledgePoints: append([]string(nil), item.KnowledgePoints...),
			AnswerArea: item.AnswerArea, SortOrder: item.SortOrder, Status: item.Status,
		}
		sort.Strings(question.KnowledgePoints)
		if item.AnswerKey != nil {
			question.Answer = &readinessAnswerSnapshot{StandardAnswer: item.AnswerKey.StandardAnswer, EquivalentAnswers: item.AnswerKey.EquivalentAnswers, Tolerance: item.AnswerKey.Tolerance}
		}
		if item.Solution != nil {
			question.Solution = &readinessSolutionSnapshot{RawText: item.Solution.RawText, Steps: item.Solution.Steps, VerificationStatus: item.Solution.VerificationStatus}
		}
		if item.Rubric != nil {
			question.Rubric = &readinessRubricSnapshot{Status: item.Rubric.Status, MaxScore: item.Rubric.MaxScore, Points: item.Rubric.Points, Deductions: item.Rubric.Deductions, Examples: item.Rubric.Examples}
		}
		snapshot.Questions = append(snapshot.Questions, question)
	}
	sort.Slice(snapshot.Questions, func(i, j int) bool {
		if snapshot.Questions[i].SortOrder != snapshot.Questions[j].SortOrder {
			return snapshot.Questions[i].SortOrder < snapshot.Questions[j].SortOrder
		}
		if snapshot.Questions[i].QuestionNo != snapshot.Questions[j].QuestionNo {
			return snapshot.Questions[i].QuestionNo < snapshot.Questions[j].QuestionNo
		}
		return snapshot.Questions[i].ID < snapshot.Questions[j].ID
	})
	if locked != nil {
		template := readinessTemplateSnapshot{
			ID: locked.ID, ExamPaperID: locked.ExamPaperID, VersionNo: locked.VersionNo,
			PageCount: locked.PageCount, OMRProfile: NormalizeTemplateLayout(locked.Layout).OMRProfile,
		}
		for _, page := range locked.Layout.Pages {
			template.Pages = append(template.Pages, readinessPageSnapshot(page))
		}
		sort.Slice(template.Pages, func(i, j int) bool { return template.Pages[i].PageNo < template.Pages[j].PageNo })
		snapshot.Template = &template
	}
	return snapshot
}

func newReadinessImportSnapshot(configurationHash string, questions []Question) readinessImportSnapshot {
	snapshot := readinessImportSnapshot{Version: 1, ConfigurationHash: configurationHash, Questions: []readinessImportQuestionSnapshot{}}
	for _, item := range questions {
		question := readinessImportQuestionSnapshot{
			ParentQuestionNo: item.ParentQuestionNo, SubquestionNo: item.SubquestionNo, Options: append([]string(nil), item.Options...),
			ID: item.ID, QuestionNo: item.QuestionNo, QuestionType: item.QuestionType,
			AssessmentArchetype: readinessAssessmentArchetype(item), Score: item.Score,
			Stem: item.Stem, KnowledgePoints: append([]string(nil), item.KnowledgePoints...),
			SourceType: item.SourceType, SourceBankItemID: item.SourceBankItemID,
			SourceBankItemVersionID: item.SourceBankItemVersionID,
			SourceContentHash:       item.SourceContentHash, BankContent: item.BankContent,
		}
		sort.Strings(question.KnowledgePoints)
		if item.AnswerKey != nil {
			question.Answer = &readinessAnswerSnapshot{StandardAnswer: item.AnswerKey.StandardAnswer, EquivalentAnswers: item.AnswerKey.EquivalentAnswers, Tolerance: item.AnswerKey.Tolerance}
		}
		if item.Solution != nil {
			question.Solution = &readinessSolutionSnapshot{RawText: item.Solution.RawText, Steps: item.Solution.Steps, VerificationStatus: item.Solution.VerificationStatus}
		}
		if item.Rubric != nil {
			question.Rubric = &readinessRubricSnapshot{Status: item.Rubric.Status, MaxScore: item.Rubric.MaxScore, Points: item.Rubric.Points, Deductions: item.Rubric.Deductions, Examples: item.Rubric.Examples}
		}
		snapshot.Questions = append(snapshot.Questions, question)
	}
	sort.Slice(snapshot.Questions, func(i, j int) bool { return snapshot.Questions[i].ID < snapshot.Questions[j].ID })
	return snapshot
}

func readinessPageSnapshot(page TemplatePage) readinessTemplatePage {
	out := readinessTemplatePage{PageNo: page.PageNo, Width: page.Width, Height: page.Height}
	out.RegistrationMarks = readinessRegionSnapshots(page.RegistrationMarks)
	out.IdentityRegions = readinessRegionSnapshots(page.IdentityRegions)
	out.QuestionRegions = readinessRegionSnapshots(page.QuestionRegions)
	return out
}

func readinessRegionSnapshots(regions []LayoutRegion) []readinessTemplateRegion {
	out := make([]readinessTemplateRegion, 0, len(regions))
	for _, region := range regions {
		item := readinessTemplateRegion{
			QuestionID: region.QuestionID, Label: region.Label, X: region.X, Y: region.Y,
			Width: region.Width, Height: region.Height,
		}
		for _, option := range region.OptionRegions {
			item.OptionRegions = append(item.OptionRegions, readinessTemplateOption{Label: option.Label, X: option.X, Y: option.Y, Width: option.Width, Height: option.Height})
		}
		sort.Slice(item.OptionRegions, func(i, j int) bool {
			if item.OptionRegions[i].Label != item.OptionRegions[j].Label {
				return item.OptionRegions[i].Label < item.OptionRegions[j].Label
			}
			return regionGeometryLess(item.OptionRegions[i].X, item.OptionRegions[i].Y, item.OptionRegions[j].X, item.OptionRegions[j].Y)
		})
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].QuestionID != out[j].QuestionID {
			return out[i].QuestionID < out[j].QuestionID
		}
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return regionGeometryLess(out[i].X, out[i].Y, out[j].X, out[j].Y)
	})
	return out
}

func regionGeometryLess(leftX, leftY, rightX, rightY float64) bool {
	if leftY == rightY {
		return leftX < rightX
	}
	return leftY < rightY
}
