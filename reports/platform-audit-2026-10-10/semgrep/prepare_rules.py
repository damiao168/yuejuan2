"""合并公开规则并记录来源；只读取本地规则文件，不读取项目源码。"""

import collections
import hashlib
import json
from pathlib import Path

report_dir = Path(__file__).resolve().parent
rules = {}
packs = []
for source in sorted(report_dir.glob("rules-*.yaml")):
    content = json.loads(source.read_text(encoding="utf-8-sig"))
    pack_name = source.stem.removeprefix("rules-")
    packs.append({
        "pack": pack_name,
        "rule_count": len(content["rules"]),
        "missed": content.get("missed"),
        "sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
        "source": "https://semgrep.dev/c/p/" + pack_name,
    })
    # 多个官方规则包可能重复收录同一规则；按规则 ID 去重，保留下载内容。
    rules.update({rule["id"]: rule for rule in content["rules"]})

merged = {"rules": list(rules.values())}
manifest = {
    "packs": packs,
    "unique_rules": len(rules),
    "languages": dict(collections.Counter(
        lang for rule in rules.values() for lang in rule.get("languages", [])
    )),
}
(report_dir / "merged-official-rules.json").write_text(
    json.dumps(merged, ensure_ascii=False, indent=2), encoding="utf-8"
)
(report_dir / "rule-manifest.json").write_text(
    json.dumps(manifest, indent=2), encoding="utf-8"
)
print(json.dumps(manifest, indent=2))
