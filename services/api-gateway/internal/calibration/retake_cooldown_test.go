package calibration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRetakeCooldownReturnsRemainingDelay(t *testing.T) {
	// 已经过了大部分冷却期的重试，只返回剩余90秒，不能让前端再次等待24小时。
	err := fmt.Errorf("create calibration: %w", &RetakeCooldownError{AvailableAt: time.Now().Add(90 * time.Second)})
	rec := httptest.NewRecorder()
	writeHandlerError(rec, httptest.NewRequest(http.MethodPost, "/calibrations", nil), err)
	seconds, parseErr := strconv.Atoi(rec.Header().Get("Retry-After"))
	if rec.Code != http.StatusTooManyRequests || parseErr != nil || seconds < 89 || seconds > 91 || !strings.Contains(rec.Body.String(), "calibration_retake_cooldown") {
		t.Fatalf("cooldown response status=%d delay=%q body=%s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
}
