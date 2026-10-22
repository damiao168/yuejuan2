package schoolassistant

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

func TestConversationImageRequestAboveOldBodyLimitReachesBothModelRounds(t *testing.T) {
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	data.Write(make([]byte, 800*1024))
	attachment := modelgovernance.ManagedChatAttachment{Name: "截图.png", MediaType: "image/png", Size: int64(data.Len()), Content: "data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Bytes())}
	input := conversationInput("")
	input.Messages[0].Attachments = []modelgovernance.ManagedChatAttachment{attachment}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= 1<<20 {
		t.Fatal("fixture does not cross the old request limit")
	}
	model := &conversationScriptModel{answer: "已看到图片"}
	handler := NewHandler(Dependencies{Conversation: ConversationDependencies{Models: conversationStore(), Model: model}})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/school-assistant/conversations/stream", bytes.NewReader(body)).WithContext(toolTestContext(t, "school_admin"))
	w := httptest.NewRecorder()
	handler.ConversationStream(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"type":"done"`) {
		t.Fatalf("image did not pass HTTP boundary: %d %s", w.Code, w.Body.String())
	}
	requests := append(model.requests, model.answerRequest)
	for _, request := range requests {
		if len(request.Messages) < 2 || len(request.Messages[1].Images) != 1 || request.Messages[1].Images[0].Content != attachment.Content {
			t.Fatal("image lost between user input, planning and final answer")
		}
	}
}

func TestConversationImageErrorsExplainTheRequiredAction(t *testing.T) {
	code, message := conversationError(modelgovernance.ErrAgentImagesUnsupported)
	if code != "ai_chat_images_unsupported" || !strings.Contains(message, "更换支持图片的对话模型") {
		t.Fatal("image capability failure is not actionable")
	}
}
