package paper

const (
	PaperPurposeQuestion    = "question_paper"
	PaperPurposeAnswerSheet = "answer_sheet"
)

func normalizePaperPurpose(value string) (string, error) {
	if value == "" {
		return PaperPurposeQuestion, nil
	}
	if value != PaperPurposeQuestion && value != PaperPurposeAnswerSheet {
		return "", ErrInvalidInput
	}
	return value, nil
}

// PapersForPurpose 区分试卷与空白答题卡；旧记录未提供用途时仍按试卷处理。
// 两者复用版本存储，但答题卡不能满足试卷上传门禁，也不进入试卷资料列表。
func PapersForPurpose(items []Paper, purpose string) []Paper {
	out := []Paper{}
	for _, item := range items {
		value, err := normalizePaperPurpose(item.Purpose)
		if err == nil && value == purpose {
			out = append(out, item)
		}
	}
	return out
}
