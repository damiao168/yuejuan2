package server

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/org"
)

type IdentityStores struct {
	Auth auth.Store
	Org  org.Store
}

type IdentityModule struct {
	AuthStore   auth.Store
	OrgStore    org.Store
	AuthHandler *auth.Handler
	OrgHandler  *org.Handler
}

func NewIdentityModule(cfg config.Config, stores IdentityStores, loginGuard auth.LoginAttemptGuard) *IdentityModule {
	return &IdentityModule{
		AuthStore: stores.Auth,
		OrgStore:  stores.Org,
		AuthHandler: auth.NewHandler(stores.Auth, cfg.Auth.SessionTTL, auth.HandlerOptions{
			LoginFailureLimit:      cfg.Auth.LoginFailureLimit,
			LoginFailureWindow:     cfg.Auth.LoginFailureWindow,
			RememberedSessionTTL:   cfg.Auth.RememberedSessionTTL,
			PublicSessionTTL:       cfg.Auth.PublicSessionTTL,
			CookieName:             cfg.Auth.SessionCookieName,
			DeviceCookieName:       cfg.Auth.DeviceCookieName,
			CookieSecure:           cfg.Auth.SessionCookieSecure,
			RiskMode:               cfg.Auth.RiskMode,
			DeviceBindingTTL:       cfg.Auth.DeviceBindingTTL,
			MFAEnabled:             cfg.Auth.MFAEnabled,
			MFAMasterKey:           cfg.Auth.MFAMasterKey,
			LoginGuard:             loginGuard,
			LoginLimiterFailClosed: cfg.Auth.LoginLimiterFailClosed,
			TrustedProxyCIDRs:      cfg.Security.TrustedProxyCIDRs,
			WechatEnabled:          cfg.Auth.WechatEnabled,
			WechatAppID:            cfg.Auth.WechatAppID,
			WechatAppSecret:        cfg.Auth.WechatAppSecret,
			WechatRedirectURL:      cfg.Auth.WechatRedirectURL,
			WechatChallengeTTL:     cfg.Auth.WechatChallengeTTL,
		}),
		OrgHandler: org.NewHandler(stores.Org, stores.Auth),
	}
}
