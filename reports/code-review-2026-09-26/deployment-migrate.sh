lock_log="$(mktemp)"
lock_app="edugrade-migrate-${HOSTNAME}"
case "${EDUGRADE_POSTGRES_APP_USER}" in
  ''|*[!A-Za-z0-9_]*) echo "EDUGRADE_POSTGRES_APP_USER has an invalid role name" >&2; exit 45;;
esac
if [ "${#EDUGRADE_POSTGRES_APP_USER}" -gt 63 ] || [ "${#EDUGRADE_POSTGRES_APP_PASSWORD}" -lt 32 ]; then
  echo "PostgreSQL application identity does not meet deployment requirements" >&2
  exit 45
fi
PGAPPNAME="${lock_app}" psql "${EDUGRADE_POSTGRES_ADMIN_DSN}" -Atc "SELECT pg_advisory_lock(19770428061); SELECT pg_sleep(86400)" >"${lock_log}" 2>&1 &
lock_pid="$!"
cleanup_lock() {
  PGAPPNAME=edugrade-migrate-cleanup psql "${EDUGRADE_POSTGRES_ADMIN_DSN}" -Atc "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = '${lock_app}' AND pid <> pg_backend_pid()" >/dev/null 2>&1 || true
  kill "${lock_pid}" 2>/dev/null || true
  wait "${lock_pid}" 2>/dev/null || true
  rm -f "${lock_log}"
}
trap cleanup_lock EXIT INT TERM
acquired=false
for attempt in $(seq 1 120); do
  if ! kill -0 "${lock_pid}" 2>/dev/null; then cat "${lock_log}" >&2; exit 44; fi
  lock_granted="$(PGAPPNAME=edugrade-migrate-probe psql "${EDUGRADE_POSTGRES_ADMIN_DSN}" -Atc "SELECT EXISTS (SELECT 1 FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid WHERE l.locktype = 'advisory' AND l.granted AND a.application_name = '${lock_app}')")"
  if [ "${lock_granted}" = "t" ]; then acquired=true; break; fi
  sleep 1
done
if [ "${acquired}" != "true" ]; then echo "timed out acquiring migration advisory lock" >&2; exit 44; fi
psql "${EDUGRADE_POSTGRES_ADMIN_DSN}" -v ON_ERROR_STOP=1 -c "
  CREATE TABLE IF NOT EXISTS schema_migration (
    filename TEXT PRIMARY KEY,
    checksum TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
  )"
tracked_count="$(psql "${EDUGRADE_POSTGRES_ADMIN_DSN}" -Atc "SELECT count(*) FROM schema_migration")"
legacy_schema="$(psql "${EDUGRADE_POSTGRES_ADMIN_DSN}" -Atc "SELECT CASE WHEN to_regclass('public.tenant') IS NULL THEN 0 ELSE 1 END")"
if [ "${tracked_count}" = "0" ] && [ "${legacy_schema}" = "1" ]; then
  if [ -z "${EDUGRADE_MIGRATION_BASELINE_VERSION}" ]; then
    echo "existing schema has no migration history; set an explicit EDUGRADE_MIGRATION_BASELINE_VERSION after verifying the schema" >&2
    exit 42
  fi
  if [ "${EDUGRADE_MIGRATION_BASELINE_VERSION}" != "000020" ]; then
    echo "unsupported baseline version; this release has a verified fingerprint only for 000020" >&2
    exit 42
  fi
  psql "${EDUGRADE_POSTGRES_ADMIN_DSN}" -v ON_ERROR_STOP=1 -f /migrations/baseline/000020_fingerprint.sql
  found_baseline=false
  for file in /migrations/*.sql; do
    filename="$(basename "${file}")"
    checksum="$(sha256sum "${file}" | awk '{print $1}')"
    psql "${EDUGRADE_POSTGRES_ADMIN_DSN}" -v ON_ERROR_STOP=1 -c "INSERT INTO schema_migration (filename, checksum) VALUES ('${filename}', '${checksum}') ON CONFLICT (filename) DO NOTHING"
    echo "baselined ${filename}"
    case "${filename}" in "${EDUGRADE_MIGRATION_BASELINE_VERSION}"_*) found_baseline=true; break;; esac
  done
  if [ "${found_baseline}" != "true" ]; then echo "baseline migration file not found" >&2; exit 42; fi
fi
for file in /migrations/*.sql; do
  filename="$(basename "${file}")"
  checksum="$(sha256sum "${file}" | awk '{print $1}')"
  stored="$(psql "${EDUGRADE_POSTGRES_ADMIN_DSN}" -Atc "SELECT checksum FROM schema_migration WHERE filename = '${filename}'")"
  if [ -n "${stored}" ]; then
    if [ "${stored}" != "${checksum}" ]; then
      # One deployed 000155 draft has a known checksum. Keep its true
      # history intact; 000162 adds the guards missing from that draft.
      if [ "${filename}" = "000155_subjective_multi_agent_panel.sql" ] &&
         [ "${stored}" = "dfca807d099fe84d1f12d522c232c4120a617d31e2b659cc9e4426ae15c60c23" ] &&
         [ "${checksum}" = "82db290ede117015fe4dbd7d5d3c6a7ec1328f2713c9eb4ba2425e66ac82a146" ] &&
         [ -f /migrations/000162_subjective_panel_legacy_hardening.sql ]; then
        echo "recognized historical 000155 panel schema; preserving checksum until 000162 hardening"
        continue
      fi
      echo "checksum mismatch for applied migration ${filename}" >&2
      exit 43
    fi
    echo "skipping ${filename}"
    continue
  fi
  echo "applying ${filename}"
  { cat "${file}"; printf "\nINSERT INTO schema_migration (filename, checksum) VALUES ('%s', '%s');\n" "${filename}" "${checksum}"; } |
    psql "${EDUGRADE_POSTGRES_ADMIN_DSN}" -v ON_ERROR_STOP=1 -1
done
psql "${EDUGRADE_POSTGRES_ADMIN_DSN}" -v ON_ERROR_STOP=1 \
  --set=app_user="${EDUGRADE_POSTGRES_APP_USER}" \
  --set=app_password="${EDUGRADE_POSTGRES_APP_PASSWORD}" <<'SQL'
SELECT format(
  'CREATE ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD %L',
  :'app_user', :'app_password'
) WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'app_user') \gexec
SELECT format(
  'ALTER ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD %L',
  :'app_user', :'app_password'
) \gexec
SELECT format('GRANT edugrade_tenant_runtime TO %I', :'app_user') \gexec
SQL
