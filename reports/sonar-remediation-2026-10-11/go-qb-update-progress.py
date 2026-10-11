"""维护移交题库原项的真实源码与验证状态，平台关闭需独立复扫证据。"""
import datetime
import hashlib
import json
from pathlib import Path

root = Path(__file__).resolve().parent
path = root / "go-server-question-bank-progress.json"
data = json.loads(path.read_text(encoding="utf-8"))
points = {
    "e2e_question_bank_acl_ux_test.go": "按历史ACL保留/幂等更新、目录与RBAC交集、学校范围、失活成员边界分阶段；保留更新顺序与原断言。",
    "e2e_question_bank_import_test.go": "按冻结来源、回执与来源追踪、去重、部分失败恢复、发布复用、撤权重放分阶段；保留原SQL、命令和原断言。",
    "e2e_question_bank_metadata_acl_search_test.go": "按元数据定义、版本与ACL、搜索与素材、撤权、停用与历史保护分阶段；范围和版本顺序不变。",
    "e2e_question_bank_publication_test.go": "共享版本指针保留原子场景状态；独立审批和发布各保留并发Wait、结果收集及错误分类；保留复制失败恢复和快照保护。",
    "e2e_question_bank_test.go": "按并发版本、乐观更新、复合身份、归档、回滚恢复、租户RLS、运行权限、撤权分解真实子场景；保留此前qbRequest参数结构和调用。",
}
data["updated_at"] = datetime.datetime.now().astimezone().isoformat()
data["source_changed"] = 5
data["verification_evidence"] = {
    "structure": "go-server-question-bank-structure-execution.json",
    "static": "go-server-question-bank-static-review.json",
    "dynamic": "pending frontend unified server validation",
    "sonar": "pending root formal rescan",
}
data["source_frozen_for_server_validation"] = True
for issue in data["issue_states"]:
    issue["state"] = "changed_awaiting_tests"
    issue["test_validation"] = "pending"
for item in data["files"]:
    item["source_sha256"] = hashlib.sha256(Path(item["path"]).read_bytes()).hexdigest()
    item["review_state"] = "read_and_refactored"
    item["maintenance_points"] = points[Path(item["path"]).name]
path.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
print("5 source changes recorded, dynamic validation and platform rescan pending")
