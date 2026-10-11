"""提炼后静态核对诊断文本与单次出现的绑定；不代替编译或平台规则结果。"""
import collections
import json
import re
from pathlib import Path

root = Path(__file__).resolve().parent
data = json.loads((root / "go-server-question-bank-progress.json").read_text(encoding="utf-8"))
string_re = re.compile(r'"(?:[^"\\]|\\.)*"|`[^`]*`')
report = []
for item in data["files"]:
    path = Path(item["path"])
    text = path.read_text(encoding="utf-8")
    old = (root / "go-server-question-bank-handoff-source" / path.name).read_text(encoding="utf-8")
    def diagnostics(source):
        return collections.Counter(match[1] for match in re.finditer(r'\.(?:Fatalf?|Errorf?|Skipf?)\(("(?:[^"\\]|\\.)*")', source))
    before, after = diagnostics(old), diagnostics(text)
    before_literals = collections.Counter(string_re.findall(old))
    after_literals = collections.Counter(string_re.findall(text))
    source = string_re.sub('""', text)
    source = re.sub(r'//[^\n]*|/\*.*?\*/', '', source, flags=re.S)
    suspects = []
    for function in re.finditer(r'^func (\w+)\(', source, flags=re.M):
        start = source.index('{', function.end())
        depth, end = 1, start + 1
        while depth:
            depth += (source[end] == '{') - (source[end] == '}')
            end += 1
        body = source[start + 1:end - 1]
        for binding in re.finditer(r'^\s*(\w+) := (?:state|env|actors|definition|access|update|binding)\.[^\n]*', body, flags=re.M):
            name = binding[1]
            remaining = body[:binding.start()] + body[binding.end():]
            if not re.search(r'\b' + re.escape(name) + r'\b', remaining):
                suspects.append({"function": function[1], "alias": name})
    report.append({"path": str(path).replace('\\', '/'), "removed_diagnostics": list((before - after).elements()), "added_diagnostics": list((after - before).elements()), "removed_string_literals": list((before_literals - after_literals).elements()), "added_string_literals": list((after_literals - before_literals).elements()), "qb_request_call_count_before": old.count('qbRequest('), "qb_request_call_count_after": text.count('qbRequest('), "subtest_count_before": old.count('t.Run('), "subtest_count_after": text.count('t.Run('), "single_occurrence_bindings": suspects})
(root / "go-server-question-bank-static-review.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps(report, ensure_ascii=False, indent=2))
