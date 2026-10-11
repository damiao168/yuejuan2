"""输出局部源码或原问题账本，只读辅助人工审读。"""

import json
import sys
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")

path = Path(sys.argv[1])
if len(sys.argv) > 2:
    lines = path.read_text(encoding="utf-8-sig").splitlines()
    start, end = int(sys.argv[2]), int(sys.argv[3])
    for number in range(start, min(end, len(lines)) + 1):
        print(f"{number}: {lines[number - 1]}")
else:
    data = json.loads(path.read_text(encoding="utf-8-sig"))
    if isinstance(data, dict):
        print("keys:", list(data))
        for key, value in data.items():
            if isinstance(value, (dict, list)):
                print(key, "count", len(value))
                if isinstance(value, list):
                    print(json.dumps(value[:2], ensure_ascii=False, indent=2))
                else:
                    print(json.dumps(value, ensure_ascii=False, indent=2)[:3500])
            else:
                print(key, value)
    else:
        print(json.dumps(data[:2], ensure_ascii=False, indent=2))
