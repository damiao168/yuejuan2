"""只修订本次分工的HTML源码；公共CDN字节先与固定npm版本核对。"""
from pathlib import Path
import base64
import datetime as dt
import hashlib
import io
import json
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent
RUNTIME = Path('D:/tools/sonar-remediation-20261011/public-dependencies')
RUNTIME.mkdir(parents=True, exist_ok=True)


def download(url):
    with urllib.request.urlopen(url, timeout=60) as response:
        return response.read(), dict(response.headers)


def main():
    metadata, _ = download('https://registry.npmjs.org/echarts/5.5.1')
    package = json.loads(metadata)
    archive, _ = download(package['dist']['tarball'])
    algorithm, integrity = package['dist']['integrity'].split('-', 1)
    actual = base64.b64encode(hashlib.new(algorithm, archive).digest()).decode()
    if actual != integrity:
        raise RuntimeError('npm tarball integrity mismatch; HTML was not edited')
    with tarfile.open(fileobj=io.BytesIO(archive), mode='r:gz') as bundle:
        packaged = bundle.extractfile('package/dist/echarts.min.js').read()
    cdn_url = 'https://cdn.jsdelivr.net/npm/echarts@5.5.1/dist/echarts.min.js'
    cdn, headers = download(cdn_url)
    if cdn != packaged:
        raise RuntimeError('CDN and pinned npm bytes differ; HTML was not edited')
    sri = 'sha384-' + base64.b64encode(hashlib.sha384(cdn).digest()).decode()
    (RUNTIME / 'echarts-5.5.1.min.js').write_bytes(cdn)
    (OUT / 'echarts-sri-verification.json').write_text(json.dumps({
        'verified_at': dt.datetime.now().astimezone().isoformat(),
        'metadata_url': 'https://registry.npmjs.org/echarts/5.5.1',
        'tarball_url': package['dist']['tarball'],
        'npm_tarball_integrity_verified': True,
        'cdn_url': cdn_url,
        'cdn_equals_npm_distribution': True,
        'cdn_bytes': len(cdn),
        'cdn_sha256': hashlib.sha256(cdn).hexdigest(),
        'integrity_attribute': sri,
        'access_control_allow_origin': next((v for k, v in headers.items() if k.lower() == 'access-control-allow-origin'), None),
        'downloaded_script': str(RUNTIME / 'echarts-5.5.1.min.js'),
        'reference': 'https://developer.mozilla.org/en-US/docs/Web/Security/Defenses/Subresource_Integrity',
        'network_scope': 'Only public fixed-version npm/CDN downloads; no workspace content uploaded',
    }, ensure_ascii=False, indent=2), encoding='utf-8')

    names = ['前端UI优化方案-对标成熟产品.html', '前端UI设计分析.html', '前端代码审查报告.html']
    for name in names:
        path = ROOT / 'frontend-review-report' / name
        original = path.read_bytes()
        # 使用深色实色标签底色，避免半透明白色在不同底图上的可读性依赖。
        before = b'.meta-chip{background:rgba(255,255,255,.1);'
        after = b'.meta-chip{background:#2d476c;'
        if original.count(before) != 1:
            raise RuntimeError(f'Unexpected metadata chip source: {name}')
        updated = original.replace(before, after)
        if name == '前端代码审查报告.html':
            old_script = f'<script src="{cdn_url}"></script>'.encode()
            new_script = f'<script src="{cdn_url}" integrity="{sri}" crossorigin="anonymous"></script>'.encode()
            if updated.count(old_script) != 1:
                raise RuntimeError('Unexpected external script source')
            updated = updated.replace(old_script, new_script)
            old_fallback = '  if(!el||typeof echarts==="undefined"){ if(el) el.innerHTML="<p style=\'color:#7a8699\'>图表库未加载，可参考第 7 节路线图排序。</p>"; return;}'.encode()
            newline = '\r\n' if b'\r\n' in original else '\n'
            new_fallback = newline.join([
                '  // 图表脚本无法加载时保留文字路线图；没有图表容器时直接结束初始化。',
                '  if(!el||typeof echarts==="undefined"){',
                '    if(el){',
                '      el.innerHTML="<p style=\'color:#7a8699\'>图表库未加载，可参考第 7 节路线图排序。</p>";',
                '    }',
                '    return;',
                '  }',
            ]).encode()
            if updated.count(old_fallback) != 1:
                raise RuntimeError('Unexpected chart fallback source')
            updated = updated.replace(old_fallback, new_fallback)
        path.write_bytes(updated)
    print(json.dumps({'modified_html_files': len(names), 'sri_verified': True}, ensure_ascii=False))


if __name__ == '__main__':
    main()
