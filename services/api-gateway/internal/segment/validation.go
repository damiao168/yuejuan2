package segment

import (
	"fmt"
	"math"
)

var validStatuses = map[string]bool{
	"generated":           true,
	"accepted":            true,
	"needs_manual_review": true,
	"rejected":            true,
}

func IsValidStatus(status string) bool {
	return validStatuses[status]
}

func ValidateBBox(bbox []float64) error {
	// bbox 约定为 [x, y, width, height]；这里只校验结构和数值，页面范围由上游坐标系统负责。
	if len(bbox) != 4 {
		return ErrInvalidInput
	}
	for _, value := range bbox {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return ErrInvalidInput
		}
	}
	if bbox[2] <= 0 || bbox[3] <= 0 {
		return ErrInvalidInput
	}
	return nil
}

func ParseAnswerArea(area map[string]any) (int, []float64, error) {
	// 题目配置可能来自 JSON，数字类型需统一转换后再进入片段存储。
	page, ok := numberFromMap(area, "page")
	if !ok || page <= 0 {
		return 0, nil, fmt.Errorf("answer_area.page is required")
	}
	x, okX := numberFromMap(area, "x")
	y, okY := numberFromMap(area, "y")
	w, okW := numberFromMap(area, "w")
	h, okH := numberFromMap(area, "h")
	if !okX || !okY || !okW || !okH {
		return 0, nil, fmt.Errorf("answer_area x/y/w/h are required")
	}
	bbox := []float64{x, y, w, h}
	if err := ValidateBBox(bbox); err != nil {
		return 0, nil, err
	}
	return int(page), bbox, nil
}

func numberFromMap(area map[string]any, key string) (float64, bool) {
	value, ok := area[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	default:
		return 0, false
	}
}
