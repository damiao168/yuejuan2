"""保存本次实际审读范围；哈希只用于失效检查，不把文件读取自动当成审读完成。"""
from pathlib import Path
import hashlib
import json

BASE = Path(__file__).resolve().parents[1]
ROOT = BASE.parents[1]

# complete 是本次人工审读记录；仅检查片段或搜索的文件明确保留 partial。
COMPLETE = [
    'AGENTS.md', 'lab/AGENTS.md', 'docs/engineering/commenting-standard.md',
    'package.json', 'lab/package.json', 'services/api-gateway/go.mod',
    'docs/architecture/overview.md', 'apps/teacher-portal/README.md',
    'services/api-gateway/internal/server/application_modules.go',
    'services/api-gateway/internal/server/infrastructure.go',
    'services/api-gateway/internal/server/router_guards.go',
    'services/api-gateway/internal/server/routes_release.go',
    'services/api-gateway/internal/server/routes_system.go',
    'services/api-gateway/internal/server/module_identity.go',
    'services/api-gateway/internal/server/module_exam.go',
    'services/api-gateway/internal/server/module_capture.go',
    'services/api-gateway/internal/server/module_grading.go',
    'services/api-gateway/internal/server/module_release.go',
    'services/api-gateway/internal/server/module_ai_governance.go',
    'services/api-gateway/internal/server/module_ai_foundation.go',
    'services/api-gateway/internal/server/module_school_assistant.go',
    'services/api-gateway/internal/auth/middleware.go',
    'services/api-gateway/internal/auth/role_policy.go',
    'services/api-gateway/internal/auth/token.go',
    'services/api-gateway/internal/auth/password.go',
    'services/api-gateway/internal/db/tenant_context.go',
    'services/api-gateway/internal/outbox/publisher_log.go',
    'services/api-gateway/internal/middleware/middleware.go',
    'services/api-gateway/internal/logger/logger.go',
    'packages/sdk/src/index.ts',
    'infra/docker-compose/nginx/nginx.conf',
]
PARTIAL = [
    'README.md', '.github/workflows/ci.yml',
    'services/api-gateway/internal/server/server.go',
    'services/api-gateway/internal/server/application_stores.go',
    'services/api-gateway/internal/config/config.go',
    'services/api-gateway/internal/auth/store_postgres.go',
    'services/api-gateway/internal/auth/managed_access_store.go',
    'services/api-gateway/internal/auth/resource_scope.go',
    'services/api-gateway/internal/idempotency/middleware.go',
    'services/api-gateway/internal/idempotency/store_postgres.go',
    'services/api-gateway/internal/server/recent_auth_test.go',
    'services/api-gateway/internal/deps/deps.go',
    'apps/desktop-client/src-tauri/src/lib.rs',
    'packages/sdk/src/runtime.ts', 'apps/web-admin/src/router/routes.tsx',
    'infra/docker-compose/docker-compose.yml',
    'scripts/check-architecture-boundaries.mjs',
]

def review_record(path, requested_status, prior):
    target = ROOT / path
    if not target.is_file():
        raise ValueError(f'审读记录找不到源码：{path}')
    current_hash = hashlib.sha256(target.read_bytes()).hexdigest()
    changed = prior and prior.get('source_sha256') != current_hash
    # 重跑记录脚本不是重新审读；变化的版本必须降级，不能继承原完成声明。
    if changed:
        read_status = 'partial'
        reason = '旧审读hash与当前源码不同，必须人工重新审读；脚本不能自动续认'
    elif prior:
        read_status = prior.get('read_status', requested_status)
        reason = prior.get('reason')
    else:
        read_status, reason = requested_status, None
    complete = read_status == 'complete'
    default_reason = '完整审读架构规则与实现；没有改动业务文件' if complete else '核对相关调用或配置片段；其余内容仍pending'
    return {'file': path, 'read_status': read_status,
            'analysis_status': 'reviewed' if complete else 'pending',
            'status': 'existing_sufficient' if complete else 'pending',
            'modified': False, 'reason': reason or default_reason, 'source_sha256': current_hash}


def main():
    output = BASE / 'evidence/root-reviewed-files.json'
    previous = {entry['file']: entry for entry in json.loads(output.read_text(encoding='utf-8'))['files']} if output.exists() else {}
    records = [review_record(path, status, previous.get(path))
               for status, paths in [('complete', COMPLETE), ('partial', PARTIAL)] for path in paths]
    output.write_text(json.dumps({
        'scope': '组合根、工程、认证授权与部署；不是全仓完成声明',
        'automatic_enumeration_is_review': False, 'files': records}, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'complete': len(COMPLETE), 'partial': len(PARTIAL)}))

if __name__ == '__main__':
    main()
