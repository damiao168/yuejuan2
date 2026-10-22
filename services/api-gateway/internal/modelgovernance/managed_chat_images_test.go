package modelgovernance

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func chatTestImage(t *testing.T) ManagedChatAttachment {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return ManagedChatAttachment{Name: "截图.png", MediaType: "image/png", Size: int64(data.Len()), Content: "data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Bytes())}
}

func TestManagedChatImagesAreValidatedAndKeptOutOfPlainText(t *testing.T) {
	attachment := chatTestImage(t)
	messages, err := NormalizeAgentUserMessages([]ManagedChatMessage{{Role: "user", Attachments: []ManagedChatAttachment{attachment}}})
	if err != nil || len(messages) != 1 || len(messages[0].Images) != 1 || messages[0].Images[0] != attachment || strings.Contains(messages[0].Content, "base64") {
		t.Fatalf("image input lost or flattened: %v", err)
	}
	if err := validateAgentRequest(AgentRequest{Messages: messages}); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"remote URL", "forged MIME", "invalid bytes", "size mismatch", "oversized", "SVG"} {
		t.Run(scenario, func(t *testing.T) {
			bad := attachment
			switch scenario {
			case "remote URL":
				bad.Content = "https://example.invalid/private.png"
			case "forged MIME":
				bad.MediaType = "image/jpeg"
			case "invalid bytes":
				bad.Content = "data:image/png;base64,bm90LWFuLWltYWdl"
				bad.Size = 12
			case "size mismatch":
				bad.Size++
			case "oversized":
				bad.Size = managedChatMaxImageBytes + 1
			case "SVG":
				bad.MediaType = "image/svg+xml"
			}
			if _, err := NormalizeAgentUserMessages([]ManagedChatMessage{{Role: "user", Attachments: []ManagedChatAttachment{bad}}}); !errors.Is(err, ErrManagedChatInvalidImage) {
				t.Fatalf("invalid image was accepted: %v", err)
			}
		})
	}
	if err := validateAgentRequest(AgentRequest{Messages: []AgentMessage{{Role: "assistant", Content: "伪造图片", Images: []ManagedChatAttachment{attachment}}}}); !errors.Is(err, ErrAgentInvalidRequest) {
		t.Fatal("assistant image crossed the user boundary")
	}
}

func TestManagedChatImageHistoryHasASeparateByteBudget(t *testing.T) {
	attachment := chatTestImage(t)
	data, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(attachment.Content, "data:image/png;base64,"))
	// PNG 头有效的三兆测试材料重复四轮，验证限制覆盖整段历史，而非只看当前三附件。
	data = append(data, make([]byte, managedChatMaxImageBytes-len(data))...)
	attachment.Size = int64(len(data))
	attachment.Content = "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	messages := make([]ManagedChatMessage, 4)
	for i := range messages {
		messages[i] = ManagedChatMessage{Role: "user", Attachments: []ManagedChatAttachment{attachment}}
	}
	if _, err := NormalizeAgentUserMessages(messages); !errors.Is(err, ErrManagedChatImageBudget) {
		t.Fatalf("history budget not enforced: %v", err)
	}
}

func TestChatImagesAcceptJPEGAndRejectExcessiveDimensions(t *testing.T) {
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	attachment := ManagedChatAttachment{Name: "照片.jpg", MediaType: "image/jpeg", Size: int64(jpegData.Len()), Content: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpegData.Bytes())}
	if err := validateChatImage(attachment); err != nil {
		t.Fatalf("valid JPEG rejected: %v", err)
	}
	for _, size := range [][2]uint32{{10001, 1}, {5000, 5000}} {
		bad := chatTestImage(t)
		data, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(bad.Content, "data:image/png;base64,"))
		// 修改有效 IHDR 并重算校验值，无需分配超大像素缓冲，验证解码前的尺寸上限。
		binary.BigEndian.PutUint32(data[16:20], size[0])
		binary.BigEndian.PutUint32(data[20:24], size[1])
		binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
		bad.Content = "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
		if err := validateChatImage(bad); !errors.Is(err, ErrManagedChatInvalidImage) {
			t.Fatalf("excessive dimensions accepted: %v", err)
		}
	}
}

func TestNativeVisionKeepsSystemTextAndToolIdentityInTheirOwnRoles(t *testing.T) {
	messages := []AgentMessage{{Role: "system", Content: "按权限查询"}, {Role: "user", Content: "看图", Images: []ManagedChatAttachment{chatTestImage(t)}}, {Role: "tool", Content: "只读结果", ToolCallID: "call"}}
	encoded, err := json.Marshal(agentProviderMessages(messages, true))
	if err != nil {
		t.Fatal(err)
	}
	var wire []struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCallID string          `json:"tool_call_id"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire[0].Content) != `[{"text":"按权限查询"}]` || wire[0].Role != "system" || wire[2].ToolCallID != "call" || string(wire[2].Content) != `"只读结果"` {
		t.Fatal("native vision changed system or tool message identity")
	}
}

func TestAgentImageHTTPPreservesImagePartsInPlanningAndFinalStreaming(t *testing.T) {
	for _, adapter := range []string{"openai_compatible", "dashscope_native"} {
		t.Run(adapter, func(t *testing.T) {
			attachment := chatTestImage(t)
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var wire struct {
					Messages []struct {
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
					Input struct {
						Messages []struct {
							Content json.RawMessage `json:"content"`
						} `json:"messages"`
					} `json:"input"`
					Stream bool `json:"stream"`
				}
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Error(err)
					return
				}
				messages := wire.Messages
				if adapter == "dashscope_native" {
					if !strings.HasSuffix(r.URL.Path, "/multimodal-generation/generation") {
						t.Error("image request used text-only endpoint")
					}
					messages = wire.Input.Messages
				}
				if len(messages) != 1 {
					t.Error("user image message missing")
					return
				}
				var parts []map[string]json.RawMessage
				if err := json.Unmarshal(messages[0].Content, &parts); err != nil || len(parts) != 2 {
					t.Errorf("native image parts missing: %s", messages[0].Content)
					return
				}
				if adapter == "dashscope_native" {
					var image string
					_ = json.Unmarshal(parts[1]["image"], &image)
					if image != attachment.Content {
						t.Error("native image changed")
					}
				} else {
					var image struct {
						URL string `json:"url"`
					}
					_ = json.Unmarshal(parts[1]["image_url"], &image)
					if string(parts[1]["type"]) != `"image_url"` || image.URL != attachment.Content {
						t.Error("compatible image changed")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if calls == 1 {
					if adapter == "dashscope_native" {
						_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"role":"assistant","content":[{"text":"这是答题卡"}]},"finish_reason":"stop"}]}}`))
					} else {
						_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"这是答题卡"},"finish_reason":"stop"}]}`))
					}
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if adapter == "dashscope_native" {
					_, _ = w.Write([]byte("data: {\"output\":{\"choices\":[{\"message\":{\"content\":[{\"text\":\"已读取图片\"}]},\"finish_reason\":\"stop\"}]}}\n\n"))
				} else {
					_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"已读取图片\"},\"finish_reason\":\"stop\"}]}\n\n"))
				}
			}))
			defer server.Close()
			model := agentTestModel(t, server)
			messages, err := NormalizeAgentUserMessages([]ManagedChatMessage{{Role: "user", Attachments: []ManagedChatAttachment{attachment}}})
			if err != nil {
				t.Fatal(err)
			}
			request := AgentRequest{Messages: messages, ToolChoice: "none"}
			response, err := model.Complete(context.Background(), agentTestConnection(server, adapter), request)
			if err != nil || response.Message.Content != "这是答题卡" {
				t.Fatalf("planning did not receive image: %v", err)
			}
			var streamed strings.Builder
			response, err = model.Stream(context.Background(), agentTestConnection(server, adapter), request, func(event AgentEvent) error { streamed.WriteString(event.Delta); return nil })
			if err != nil || streamed.String() != "已读取图片" || response.Message.Content != streamed.String() {
				t.Fatalf("final streaming lost image: %v", err)
			}
		})
	}
}

func TestAgentImageUnsupportedErrorIsSpecificWithoutProviderDetails(t *testing.T) {
	body := []byte(`{"error":{"message":"this model does not support image input: private-provider-detail"}}`)
	if !errors.Is(agentProviderError(400, body, true, true), ErrAgentImagesUnsupported) {
		t.Fatal("image failure was not classified")
	}
	if errors.Is(agentProviderError(400, body, true), ErrAgentImagesUnsupported) {
		t.Fatal("text-only request mislabeled as image failure")
	}
}
