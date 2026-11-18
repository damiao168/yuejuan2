package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// ErrPrintLayoutConfirmationRequired 表示模板缺少专用区，必须先在原底图上确认可覆盖位置。
var ErrPrintLayoutConfirmationRequired = errors.New("student print layout confirmation is required")

func missingPrintQRPages(pages map[int]printPackagePageLayout) []int {
	missing := []int{}
	for pageNo, page := range pages {
		_, dedicated := qrRegionCandidates(PrintPackagePage{IdentityRegions: page.identity})
		if len(dedicated) == 0 {
			missing = append(missing, pageNo)
		}
	}
	sort.Ints(missing)
	return missing
}

// 只接受未声明专用区的全部页面。来源哈希固定原底图，额外页或重复页不能改变模板中已有的位置。
func normalizedStudentPrintLayout(pages map[int]printPackagePageLayout, sourceHash string, input *StudentPrintLayout) (*StudentPrintLayout, error) {
	missing := missingPrintQRPages(pages)
	if input == nil {
		if len(missing) > 0 {
			return nil, ErrPrintLayoutConfirmationRequired
		}
		return nil, nil
	}
	if !strings.EqualFold(input.SourceSHA256, sourceHash) || len(input.Pages) != len(missing) {
		return nil, printPackageInputError{"打印位置与当前底图或需要确认的页数不一致，请重新读取底图。"}
	}
	wanted := make(map[int]bool, len(missing))
	for _, pageNo := range missing {
		wanted[pageNo] = true
	}
	out := &StudentPrintLayout{SourceSHA256: strings.ToLower(sourceHash), Pages: make([]StudentPrintQRPage, 0, len(missing))}
	for _, page := range input.Pages {
		if !wanted[page.PageNo] {
			return nil, printPackageInputError{"打印位置包含重复页或不需要确认的页面。"}
		}
		region := PrintPackageRegion{X: page.X, Y: page.Y, Width: page.Width, Height: page.Height}
		if _, _, _, _, ok := mapPrintRegion(region, 0, 0, 1, 1); !ok {
			return nil, printPageError(page.PageNo, "确认的二维码区域超出页面或尺寸无效。")
		}
		delete(wanted, page.PageNo)
		out.Pages = append(out.Pages, page)
	}
	sort.Slice(out.Pages, func(i, j int) bool { return out.Pages[i].PageNo < out.Pages[j].PageNo })
	return out, nil
}

func printPageWithConfirmedQR(page PrintPackagePage, layout *StudentPrintLayout) PrintPackagePage {
	if layout != nil {
		for _, area := range layout.Pages {
			if area.PageNo == page.PageNo {
				page.confirmedQR = &PrintPackageRegion{ID: "qr", X: area.X, Y: area.Y, Width: area.Width, Height: area.Height}
				break
			}
		}
	}
	return page
}

func printLayoutJSON(layout *StudentPrintLayout) []byte {
	if layout == nil {
		return nil
	}
	raw, _ := json.Marshal(layout)
	return raw
}

func decodeStudentPrintLayout(raw []byte) (*StudentPrintLayout, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var layout StudentPrintLayout
	if err := json.Unmarshal(raw, &layout); err != nil {
		return nil, err
	}
	return &layout, nil
}

func studentBarcodePrintRequestHash(studentIDs []string, layout *StudentPrintLayout) string {
	if layout == nil {
		return studentBarcodeRequestHash(studentIDs)
	}
	// 继承原签发的学生集合语义；调整名单或页面的排列不应创建新命令。
	students := append([]string{}, studentIDs...)
	sort.Strings(students)
	raw, _ := json.Marshal(struct {
		Students []string
		Layout   *StudentPrintLayout
	}{students, canonicalPrintLayout(layout)})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func sameStudentPrintLayout(a, b *StudentPrintLayout) bool {
	return bytes.Equal(printLayoutJSON(canonicalPrintLayout(a)), printLayoutJSON(canonicalPrintLayout(b)))
}

// 页码顺序和十六进制大小写不代表版式变更，幂等比较使用同一规范形式。
func canonicalPrintLayout(layout *StudentPrintLayout) *StudentPrintLayout {
	if layout == nil {
		return nil
	}
	out := *layout
	out.SourceSHA256 = strings.ToLower(out.SourceSHA256)
	out.Pages = append([]StudentPrintQRPage{}, out.Pages...)
	sort.Slice(out.Pages, func(i, j int) bool { return out.Pages[i].PageNo < out.Pages[j].PageNo })
	return &out
}
