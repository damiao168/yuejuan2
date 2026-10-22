package schoolassistant

import (
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

func TestConversationCarriesMathFormatIntoPlanningAndFinalAnswer(t *testing.T) {
	for _, useTools := range []bool{false, true} {
		name := "general conversation"
		if useTools {
			name = "answer after tool lookup"
		}
		t.Run(name, func(t *testing.T) {
			answer := "欧拉公式：$e^{i\\theta}=\\cos\\theta+i\\sin\\theta$。\n\n$$\n\\int_{-\\infty}^{+\\infty}e^{-x^2}\\,dx=\\sqrt{\\pi}\n$$"
			model := &conversationScriptModel{answer: answer}
			item := toolTestExamItem(toolTestExam, time.Now().UTC())
			reader := &toolTestExams{items: []exam.Exam{item}}
			if useTools {
				model.plans = [][]modelgovernance.AgentToolCall{{conversationCall("search", ToolSearchExams, `{}`)}}
			}
			service := NewConversationService(NewService(Dependencies{Exams: reader}), ConversationDependencies{Models: conversationStore(), Model: model})
			var events []ConversationEvent
			input := conversationInput("解释欧拉公式和高斯积分")
			if err := service.Converse(toolTestContext(t, "school_admin", "exam:manage"), input, func(event ConversationEvent) error {
				events = append(events, event)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, request := range model.requests {
				assertMathFormatMessage(t, request.Messages[0])
			}
			messages := model.answerRequest.Messages
			assertMathFormatMessage(t, messages[0])
			assertMathFormatMessage(t, messages[len(messages)-1])
			if model.answerRequest.ToolChoice != "none" || messages[1].Content != input.Messages[0].Content {
				t.Fatal("format rules changed tool restrictions or original user text")
			}
			// 脚本模型仅验证格式规范到达模型，以及公式字符经过 SSE 事件后不变；不代表外部模型必然遵循提示。
			var streamed strings.Builder
			for _, event := range events {
				if event.Type == "delta" && event.Channel == "content" {
					streamed.WriteString(event.Delta)
				}
			}
			last := events[len(events)-1]
			if streamed.String() != answer || last.Completion == nil || last.Completion.Message.Content != answer {
				t.Fatal("formula delimiters changed between model, delta and completion")
			}
		})
	}
}

func assertMathFormatMessage(t *testing.T, message modelgovernance.AgentMessage) {
	t.Helper()
	if message.Role != "system" {
		t.Fatalf("format rule must be a system instruction: %s", message.Role)
	}
	for _, required := range []string{"KaTeX", "$...$", "$$...$$", "分隔符分别独占一行", `\int_{-\infty}^{+\infty}`, "不要把公式包在反引号或代码块中", "纯文本、代码或 LaTeX 源码"} {
		if !strings.Contains(message.Content, required) {
			t.Fatalf("model request missing format rule %q", required)
		}
	}
}
