BEGIN;

CREATE TABLE IF NOT EXISTS devices (
  id text PRIMARY KEY,
  owner_subject text NOT NULL,
  name text NOT NULL,
  token_hash text NOT NULL UNIQUE CHECK (length(token_hash) = 64),
  created_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz,
  revoked_at timestamptz
);
CREATE INDEX IF NOT EXISTS devices_owner_idx
  ON devices(owner_subject) WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS pairings (
  device_code_hash text PRIMARY KEY CHECK (length(device_code_hash) = 64),
  user_code_hash text NOT NULL UNIQUE CHECK (length(user_code_hash) = 64),
  device_name text NOT NULL,
  expires_at timestamptz NOT NULL,
  owner_subject text,
  approved_at timestamptz,
  consumed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((approved_at IS NULL AND owner_subject IS NULL) OR (approved_at IS NOT NULL AND owner_subject IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS pairings_expiry_idx ON pairings(expires_at);

CREATE TABLE IF NOT EXISTS call_audit (
  id bigserial PRIMARY KEY,
  owner_subject text NOT NULL,
  device_id text NOT NULL REFERENCES devices(id),
  tool_name text NOT NULL,
  status text NOT NULL CHECK (status IN ('ok','error','offline','timeout')),
  duration_ms bigint NOT NULL CHECK (duration_ms >= 0),
  error_code text,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS call_audit_owner_created_idx
  ON call_audit(owner_subject, created_at DESC);
CREATE INDEX IF NOT EXISTS call_audit_device_created_idx
  ON call_audit(device_id, created_at DESC);

COMMIT;
