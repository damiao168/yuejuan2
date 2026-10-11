package auth

import (
	"testing"
	"time"
)

func TestBatch30HandlerOptionsPreserveDefaultsAndFirstOption(t *testing.T) {
	for _, option := range []HandlerOptions{{}, {
		LoginFailureLimit: -1, LoginFailureWindow: -time.Second,
		RememberedSessionTTL: -time.Second, PublicSessionTTL: -time.Second,
		DeviceBindingTTL: -time.Second, CookieName: "  ", DeviceCookieName: "\t",
		WechatChallengeTTL: -time.Second,
	}} {
		h := NewHandler(NewMemoryStore(), time.Hour, option, HandlerOptions{
			CookieName: "ignored", RememberedSessionTTL: time.Minute,
		})
		if h.cookieName != DefaultSessionCookieName || h.deviceCookieName != "edugrade_device" ||
			h.rememberedTTL != 30*24*time.Hour || h.publicTTL != 4*time.Hour ||
			h.wechatChallengeTTL != 5*time.Minute || h.sessionTTL != time.Hour {
			t.Fatalf("defaults or first option changed: %+v", h)
		}
		if h.loginService.options.DeviceBindingTTL != DefaultDeviceBindingTTL || h.loginService.options.RiskMode != "shadow" || h.loginGuard == nil {
			t.Fatal("login service defaults diverged from handler")
		}
	}
}

func TestBatch30HandlerOptionsKeepExplicitGuardAndNormalizedValues(t *testing.T) {
	guard := NewMemoryLoginAttemptGuard(2, time.Minute)
	limiter := NewLoginFailureLimiter(3, time.Minute)
	h := NewHandler(NewMemoryStore(), 2*time.Hour, HandlerOptions{
		CookieName: " session ", DeviceCookieName: " device ", CookieSecure: true,
		RememberedSessionTTL: time.Hour, PublicSessionTTL: time.Minute,
		DeviceBindingTTL: 3 * time.Hour, RiskMode: " OFF ", LoginGuard: guard, LoginLimiter: limiter,
		LoginLimiterFailClosed: true, MFAEnabled: true, MFAMasterKey: "invalid",
		WechatEnabled: true, WechatAppID: " app ", WechatAppSecret: " secret ",
		WechatRedirectURL: " url ", WechatChallengeTTL: time.Minute,
	})
	if h.loginGuard != guard || h.loginService.guard != guard || h.cookieName != "session" ||
		h.deviceCookieName != "device" || !h.cookieSecure || !h.loginLimiterFailClosed ||
		h.rememberedTTL != time.Hour || h.publicTTL != time.Minute {
		t.Fatal("explicit guard or session options changed")
	}
	if h.loginService.options.DeviceBindingTTL != 3*time.Hour || h.loginService.options.RiskMode != "off" ||
		!h.mfaEnabled || h.mfaCipher != nil || !h.wechatEnabled || h.wechatAppID != "app" ||
		h.wechatAppSecret != "secret" || h.wechatRedirectURL != "url" || h.wechatChallengeTTL != time.Minute {
		t.Fatal("normalization or invalid MFA key handling changed")
	}
	withLimiter := NewHandler(NewMemoryStore(), time.Hour, HandlerOptions{LoginLimiter: limiter})
	layered, ok := withLimiter.loginGuard.(*LayeredLoginAttemptGuard)
	if !ok || layered.pair != limiter {
		t.Fatal("distributed limiter was not retained when no explicit guard was supplied")
	}
}
