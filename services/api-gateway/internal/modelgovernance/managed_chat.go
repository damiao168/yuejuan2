package modelgovernance

import (
	"context"
	"errors"
	"time"
)

const (
	managedChatContentTypeHeader = "Content-Type"
	managedChatJSONContentType   = "application/json"
)

const (
	managedChatMaxMessages     = 50
	managedChatMaxMessageRunes = 20_000
	managedChatMaxAttachments  = 3
	managedChatMaxFileRunes    = 60_000
	managedChatMaxTotalRunes   = 200_000
	managedChatBodyLimit       = ChatRequestBodyLimit
)

var (
	ErrManagedChatInvalidRequest = errors.New("invalid managed chat request")
	ErrManagedChatEmptyResponse  = errors.New("managed chat returned an empty response")
	ErrManagedChatResponseLimit  = errors.New("managed chat response exceeds the size limit")
)

func NewHTTPManagedAPIChatter(timeout time.Duration) *HTTPManagedAPIChatter {
	if timeout <= 0 || timeout > 12*time.Minute {
		timeout = 10 * time.Minute
	}
	return &HTTPManagedAPIChatter{timeout: timeout}
}

func (c *HTTPManagedAPIChatter) Chat(ctx context.Context, connection ManagedAPIConnection, messages []ManagedChatMessage) (ManagedChatCompletion, error) {
	if connection.Config.AdapterType == "dashscope_native" {
		return c.chatDashScope(ctx, connection, messages)
	}
	return c.chatOpenAICompatible(ctx, connection, messages)
}
