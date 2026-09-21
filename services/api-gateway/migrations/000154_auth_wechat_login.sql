CREATE TABLE IF NOT EXISTS wechat_identity (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL,
  user_id UUID NOT NULL,
  app_id TEXT NOT NULL CHECK (length(app_id) BETWEEN 1 AND 128),
  open_id TEXT NOT NULL CHECK (length(open_id) BETWEEN 1 AND 128),
  union_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ,
  CONSTRAINT fk_wechat_identity_user_tenant
    FOREIGN KEY (tenant_id, user_id) REFERENCES app_user(tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_wechat_identity_app_open_tenant_active
  ON wechat_identity (app_id, open_id, tenant_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_wechat_identity_app_union_tenant_active
  ON wechat_identity (app_id, union_id, tenant_id)
  WHERE union_id IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_wechat_identity_user_app_active
  ON wechat_identity (tenant_id, user_id, app_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS auth_wechat_login_challenge (
  id UUID PRIMARY KEY,
  state_hash TEXT NOT NULL UNIQUE CHECK (state_hash ~ '^[0-9a-f]{64}$'),
  poll_token_hash TEXT NOT NULL UNIQUE CHECK (poll_token_hash ~ '^[0-9a-f]{64}$'),
  tenant_code TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending','authorized','failed','consumed')),
  error_code TEXT,
  tenant_id UUID,
  user_id UUID,
  remember_device BOOLEAN NOT NULL DEFAULT FALSE,
  public_device BOOLEAN NOT NULL DEFAULT FALSE,
  expires_at TIMESTAMPTZ NOT NULL,
  authorized_at TIMESTAMPTZ,
  consumed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT fk_wechat_challenge_user_tenant
    FOREIGN KEY (tenant_id, user_id) REFERENCES app_user(tenant_id, id),
  CONSTRAINT auth_wechat_challenge_device_mode CHECK (NOT (remember_device AND public_device))
);

CREATE INDEX IF NOT EXISTS idx_auth_wechat_challenge_expiry
  ON auth_wechat_login_challenge (expires_at) WHERE status='pending';
