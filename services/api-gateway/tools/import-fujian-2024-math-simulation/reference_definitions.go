// 试卷模板布局和固定题目量规属于本数据集定义；坐标和评分点与仿真输入配套。
package main

import (
	"fmt"
	"strconv"
	"strings"
)

func rubricFor(questionNo string) map[string]any {
	points := []any{}
	switch questionNo {
	case "Q17":
		points = []any{
			rubricPoint("q17-p1", "正确计算(-1)^0=1", 2),
			rubricPoint("q17-p2", "正确计算|-5|=5", 2),
			rubricPoint("q17-p3", "正确计算√4=2", 2),
			rubricPoint("q17-p4", "得到结果4", 2),
		}
	case "Q18":
		points = []any{
			rubricPoint("q18-p1", "由菱形得到AB=AD且∠B=∠D", 3),
			rubricPoint("q18-p2", "使用已知∠AEB=∠AFD", 1),
			rubricPoint("q18-p3", "证明△ABE≌△ADF", 3),
			rubricPoint("q18-p4", "得出BE=DF", 1),
		}
	case "Q19":
		points = []any{
			rubricPoint("q19-p1", "注意分母不为0", 1),
			rubricPoint("q19-p2", "正确去分母并列出等式", 3),
			rubricPoint("q19-p3", "解得x=10", 3),
			rubricPoint("q19-p4", "检验x=10成立", 1),
		}
	}
	return map[string]any{
		"status":     "locked",
		"max_score":  8,
		"points":     points,
		"deductions": []any{},
		"examples":   []any{},
	}
}

func rubricPoint(id string, description string, score float64) map[string]any {
	return map[string]any{"id": id, "description": description, "score": score, "required": true}
}

func buildTemplateLayout(questionIDs map[string]string, regions map[string]region) map[string]any {
	pages := []any{}
	for pageNo := 1; pageNo <= 2; pageNo++ {
		questionRegions := []any{}
		for number := 1; number <= 19; number++ {
			if (pageNo == 1 && number >= 17) || (pageNo == 2 && number < 17) {
				continue
			}
			questionNo := "Q" + strconv.Itoa(number)
			item := regions[questionNo]
			regionData := map[string]any{
				"id":          "region-" + strings.ToLower(questionNo),
				"question_id": questionIDs[questionNo],
				"label":       questionNo,
				"x":           item.X, "y": item.Y, "width": item.Width, "height": item.Height,
			}
			if number <= 10 {
				regionData["option_regions"] = buildObjectiveOptionRegions(number, questionNo)
			}
			questionRegions = append(questionRegions, regionData)
		}
		pages = append(pages, map[string]any{
			"page_no":            pageNo,
			"width":              imageWidth,
			"height":             imageHeight,
			"registration_marks": []any{},
			"identity_regions": []any{
				map[string]any{
					"id":    fmt.Sprintf("identity-page-%d", pageNo),
					"label": "anonymous_candidate_code",
					"x":     0.06, "y": 0.03, "width": 0.42, "height": 0.09,
				},
			},
			"question_regions": questionRegions,
		})
	}
	return map[string]any{
		"omr_profile": map[string]any{
			"mode":    "template_difference",
			"version": "opencv-template-difference-bubble-v1",
		},
		"pages": pages,
	}
}

func buildObjectiveOptionRegions(number int, questionNo string) []any {
	row := (number - 1) / 5
	col := (number - 1) % 5
	x0 := 105 + col*302
	y0 := 402 + row*160
	options := []any{}
	for optionIndex, option := range []string{"A", "B", "C", "D"} {
		cx := x0 + 66 + optionIndex*53
		cy := y0 + 26
		options = append(options, map[string]any{
			"id":     strings.ToLower(questionNo) + "-" + strings.ToLower(option),
			"label":  option,
			"x":      float64(cx-17) / imageWidth,
			"y":      float64(cy-17) / imageHeight,
			"width":  float64(34) / imageWidth,
			"height": float64(34) / imageHeight,
		})
	}
	return options
}
