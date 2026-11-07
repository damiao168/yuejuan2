"""从已校验的原版材料生成新布局清单，保留原提示词文件。"""

import argparse
import hashlib
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "ai-services"))
from grading_agent.question_context import (
    QUESTION_CONTEXT_MANIFEST,
    QUESTION_CONTEXT_PROMPT_VERSION,
)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--write", action="store_true", help="生成新布局清单；省略时仅校验")
    options = parser.parse_args()
    prompt_root = ROOT / "ai-services" / "prompts"
    manifest = json.loads((prompt_root / "manifest.json").read_text(encoding="utf-8"))
    # 只接受与旧版已登记字节相同的材料，避免生成新清单时意外批准内容变更。
    for name, digest in manifest["files"].items():
        path = (prompt_root / name).resolve()
        if not path.is_relative_to(prompt_root.resolve()) or hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise SystemExit(f"原版提示词校验失败：{name}")
    manifest["prompt_version"] = QUESTION_CONTEXT_PROMPT_VERSION
    target = prompt_root / QUESTION_CONTEXT_MANIFEST
    expected = json.dumps(manifest, ensure_ascii=False, indent=2) + "\n"
    if options.write:
        target.write_text(expected, encoding="utf-8", newline="\n")
    elif not target.exists() or target.read_text(encoding="utf-8") != expected:
        raise SystemExit("新布局清单不匹配，请使用 --write 重新生成")


if __name__ == "__main__":
    main()
