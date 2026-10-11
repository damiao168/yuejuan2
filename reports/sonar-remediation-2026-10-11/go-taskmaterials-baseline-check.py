"""以 Go overlay 验证已保存的原测试夹具，不改工作区源码。"""
import datetime
import json
import os
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parent
workspace = root.parent.parent
target = workspace / "services/api-gateway/internal/taskmaterials/management_scope_test.go"
baseline = Path("D:/tools/sonar-remediation-20261011/baseline/services/api-gateway/internal/taskmaterials/management_scope_test.go")
overlay = root / "go-taskmaterials-baseline-overlay.json"
overlay.write_text(json.dumps({"Replace": {str(target): str(baseline)}}), encoding="utf-8")
env = dict(os.environ)
env.update(GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOMAXPROCS="1")
command = ["go", "test", "-json", "-p", "1", "-count=1", "-timeout=5m", "-overlay", str(overlay), "-run", "^TestGroupMemberMaterialRejectsOutsiderAndChangedRepresentation$", "./internal/taskmaterials"]
started = datetime.datetime.now().astimezone().isoformat()
result = subprocess.run(command, cwd=workspace / "services/api-gateway", env=env, capture_output=True, text=True, encoding="utf-8", errors="replace")
(root / "go-taskmaterials-baseline-test-events.jsonl").write_text(result.stdout + result.stderr, encoding="utf-8")
summary = dict(started_at=started, finished_at=datetime.datetime.now().astimezone().isoformat(), command=command, exit_code=result.returncode, overlay_original_test=str(baseline), note="原测试夹具覆盖当前测试文件，生产代码使用当前本地工作区；用于确认夹具缺失不由职责拆分引起。")
(root / "go-taskmaterials-baseline-tests.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps(summary, ensure_ascii=True))
