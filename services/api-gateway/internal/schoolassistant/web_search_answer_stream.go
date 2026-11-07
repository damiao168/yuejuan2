package schoolassistant

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func publicSearchDecisionContext(ctx context.Context, limit time.Duration) (context.Context, func(), func()) {
	decisionCtx, cancel := context.WithCancelCause(ctx)
	timer := time.AfterFunc(limit, func() { cancel(context.DeadlineExceeded) })
	return decisionCtx, func() { timer.Stop() }, func() { timer.Stop(); cancel(nil) }
}

// publicSearchAnswerPreview 只预览已确认answer/clarify动作的顶层answer字符串。
// 证据、检索词和JSON参数不能流入聊天正文；完整对象仍须在done之前通过严格校验。
func publicSearchAnswerPreview(raw string) string {
	decoder := json.NewDecoder(strings.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return ""
	}
	action := ""
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		key, ok := token.(string)
		if !ok {
			return ""
		}
		if key == "answer" {
			if action != "answer" && action != "clarify" {
				return ""
			}
			value := strings.TrimSpace(raw[decoder.InputOffset():])
			if !strings.HasPrefix(value, ":") {
				return ""
			}
			return partialPublicJSONString(strings.TrimSpace(value[1:]))
		}
		if key == "action" {
			if err := decoder.Decode(&action); err != nil {
				return ""
			}
			if action != "answer" && action != "clarify" {
				return ""
			}
		} else {
			var ignored json.RawMessage
			if err := decoder.Decode(&ignored); err != nil {
				return ""
			}
		}
	}
	return ""
}

func partialPublicJSONString(raw string) string {
	if len(raw) == 0 || raw[0] != '"' {
		return ""
	}
	end := 1
	for end < len(raw) {
		if raw[end] == '"' {
			break
		}
		if raw[end] == '\\' {
			if end+1 >= len(raw) {
				break
			}
			if raw[end+1] == 'u' {
				if end+6 > len(raw) {
					break
				}
				code, err := strconv.ParseUint(raw[end+2:end+6], 16, 16)
				if err != nil {
					break
				}
				if code >= 0xD800 && code <= 0xDBFF {
					// 高代理项须等低代理项完整后再解码，避免先输出替代字符又回头改写正文。
					if end+12 > len(raw) || raw[end+6:end+8] != `\u` {
						break
					}
					low, err := strconv.ParseUint(raw[end+8:end+12], 16, 16)
					if err != nil || low < 0xDC00 || low > 0xDFFF {
						break
					}
					end += 12
				} else {
					end += 6
				}
			} else {
				end += 2
			}
			continue
		}
		_, size := utf8.DecodeRuneInString(raw[end:])
		if size == 1 && raw[end] >= utf8.RuneSelf {
			break
		}
		end += size
	}
	var preview string
	if json.Unmarshal([]byte(raw[:end]+`"`), &preview) != nil {
		return ""
	}
	return preview
}
