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
		key, ok := token.(string)
		if err != nil || !ok {
			return ""
		}
		if key == "answer" {
			return publicAnswerFieldPreview(raw[decoder.InputOffset():], action)
		}
		if !readPublicAnswerPreviewField(decoder, key, &action) {
			return ""
		}
	}
	return ""
}

func publicAnswerFieldPreview(raw, action string) string {
	if action != "answer" && action != "clarify" {
		return ""
	}
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, ":") {
		return ""
	}
	return partialPublicJSONString(strings.TrimSpace(value[1:]))
}

func readPublicAnswerPreviewField(decoder *json.Decoder, key string, action *string) bool {
	if key == "action" {
		return decoder.Decode(action) == nil && (*action == "answer" || *action == "clarify")
	}
	var ignored json.RawMessage
	return decoder.Decode(&ignored) == nil
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
			size := publicJSONStringEscapeSize(raw[end:])
			if size == 0 {
				break
			}
			end += size
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

// 高代理项须等低代理项完整后再解码，避免先输出替代字符又回头改写正文。
func publicJSONStringEscapeSize(raw string) int {
	if len(raw) < 2 {
		return 0
	}
	if raw[1] != 'u' {
		return 2
	}
	if len(raw) < 6 {
		return 0
	}
	code, err := strconv.ParseUint(raw[2:6], 16, 16)
	if err != nil {
		return 0
	}
	if code < 0xD800 || code > 0xDBFF {
		return 6
	}
	if len(raw) < 12 || raw[6:8] != `\u` {
		return 0
	}
	low, err := strconv.ParseUint(raw[8:12], 16, 16)
	if err != nil || low < 0xDC00 || low > 0xDFFF {
		return 0
	}
	return 12
}
