package capture

import (
	"math"
	"strings"
)

// 模板目前只有像素宽高，不足以还原真实DPI或A3/A4纸型；默认短边210mm并保留原始宽高比。
// 可信物理尺寸一旦可用须成对传入且保持相同比例。所有几何都使用这一张整页，不能另设题区内框。
func printPageSizeMM(page PrintPackagePage) (float64, float64, error) {
	if page.TemplateWidth <= 0 || page.TemplateHeight <= 0 {
		return 0, 0, ErrInvalidInput
	}
	ratio := float64(page.TemplateWidth) / float64(page.TemplateHeight)
	width, height := page.PhysicalWidthMM, page.PhysicalHeightMM
	if width != 0 || height != 0 {
		if !finitePrintNumber(width) || !finitePrintNumber(height) || width <= 0 || height <= 0 || math.Abs(width/height-ratio) > ratio*.001 {
			return 0, 0, ErrInvalidInput
		}
		return width, height, nil
	}
	if ratio >= 1 {
		return 210 * ratio, 210, nil
	}
	return 210, 210 / ratio, nil
}

func finitePrintNumber(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func overlapsPrintRegion(a, b PrintPackageRegion) bool {
	return a.X < b.X+b.Width && b.X < a.X+a.Width && a.Y < b.Y+b.Height && b.Y < a.Y+a.Height
}

func namedQRRegion(region PrintPackageRegion) bool {
	name := strings.ToLower(region.ID + " " + region.Label)
	return strings.Contains(name, "qr") || strings.Contains(name, "barcode") || strings.Contains(name, "二维码") || strings.Contains(name, "条码")
}

func printQuestionOptions(question PrintPackageRegion) []PrintPackageRegion {
	relative := question.OptionCoordinateSpace == "question_relative"
	if question.OptionCoordinateSpace == "" {
		// 沿用评分运行对历史选项坐标的整组判定，不能逐个选项猜测后混用两种坐标系。
		for _, option := range question.OptionRegions {
			if option.X < question.X || option.Y < question.Y || option.X+option.Width > question.X+question.Width+.000001 || option.Y+option.Height > question.Y+question.Height+.000001 {
				relative = true
				break
			}
		}
	}
	out := append([]PrintPackageRegion{}, question.OptionRegions...)
	if relative {
		for index := range out {
			option := &out[index]
			option.X, option.Y = question.X+option.X*question.Width, question.Y+option.Y*question.Height
			option.Width, option.Height = option.Width*question.Width, option.Height*question.Height
		}
	}
	return out
}

func printQRRegion(page PrintPackagePage, width, height float64) (PrintPackageRegion, error) {
	blocked, dedicated := qrRegionCandidates(page)
	if len(dedicated) == 0 && page.confirmedQR != nil {
		dedicated = []PrintPackageRegion{*page.confirmedQR}
	}
	if err := validateQRRegions(blocked, dedicated, width, height); err != nil {
		return PrintPackageRegion{}, err
	}
	if len(dedicated) > 0 {
		return dedicatedQRRegion(dedicated, blocked, width, height)
	}
	// 区域标注不包含所有页眉和说明，不能将未标注的位置当成空白。
	return PrintPackageRegion{}, ErrPrintLayoutConfirmationRequired
}

func qrRegionCandidates(page PrintPackagePage) ([]PrintPackageRegion, []PrintPackageRegion) {
	blocked := append(append(append([]PrintPackageRegion{}, page.QuestionRegions...), page.GradingGroups...), page.RegistrationMarks...)
	var dedicated []PrintPackageRegion
	for _, region := range page.IdentityRegions {
		if namedQRRegion(region) {
			dedicated = append(dedicated, region)
			continue
		}
		blocked = append(blocked, region)
	}
	return blocked, dedicated
}

func validateQRRegions(blocked, dedicated []PrintPackageRegion, width, height float64) error {
	for _, region := range append(append([]PrintPackageRegion{}, blocked...), dedicated...) {
		if _, _, _, _, ok := mapPrintRegion(region, 0, 0, width, height); !ok {
			return ErrInvalidInput
		}
	}
	return nil
}

func clearQRRegion(candidate PrintPackageRegion, blocked []PrintPackageRegion) bool {
	for _, region := range blocked {
		if overlapsPrintRegion(candidate, region) {
			return false
		}
	}
	return true
}

func dedicatedQRRegion(dedicated, blocked []PrintPackageRegion, width, height float64) (PrintPackageRegion, error) {
	// 明确声明的二维码区不能被打印器挪走；与答题区重叠或过小就拒绝签发该版式。
	if len(dedicated) != 1 {
		return PrintPackageRegion{}, ErrInvalidTransition
	}
	box := dedicated[0]
	size := math.Min(box.Width*width, box.Height*height)
	if size < 24 {
		return PrintPackageRegion{}, ErrInvalidTransition
	}
	qr := PrintPackageRegion{X: box.X + (box.Width-size/width)/2, Y: box.Y + (box.Height-size/height)/2, Width: size / width, Height: size / height}
	if !clearQRRegion(qr, blocked) {
		return PrintPackageRegion{}, ErrInvalidTransition
	}
	return qr, nil
}
