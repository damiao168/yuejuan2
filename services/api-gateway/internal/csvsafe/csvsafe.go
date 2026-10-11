// Package csvsafe 消除导出 CSV 中来自用户的单元格公式；仅用 CSV 引号包裹，不能阻止电子表格执行公式。
package csvsafe

import "strings"

func Cell(value string) string {
	trimmed := strings.TrimLeft(value, " \u00a0")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	default:
		return value
	}
}

func Row(values []string) []string {
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = Cell(value)
	}
	return out
}
