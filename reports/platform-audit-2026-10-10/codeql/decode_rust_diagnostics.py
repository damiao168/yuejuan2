"""解码当前已成功分析数据库的官方诊断 BQRS，不执行或替换查询。"""

import datetime
import hashlib
import json
import pathlib
import subprocess

REPORT = pathlib.Path(__file__).parent
CLI = pathlib.Path("D:/tools/audit-codeql-20261010/codeql/codeql.exe")


if __name__ == "__main__":
    create = json.loads((REPORT / "rust-create-execution.json").read_text(encoding="utf-8"))
    analyze = json.loads((REPORT / "rust-analyze-execution.json").read_text(encoding="utf-8"))
    database = pathlib.Path(create["command"][3])
    if create["exit_code"] != 0 or analyze["exit_code"] != 0 or analyze["command"][3] != str(database):
        raise RuntimeError("当前数据库未完成有效分析，不能套用旧库的解码结果")
    root = database / "results/codeql/rust-queries/queries"
    records, counts = [], {}
    for directory, name in [("diagnostics", item) for item in ("ExtractedFiles", "ExtractionErrors", "ExtractionWarnings", "UnextractedElements", "UnresolvedMacroCalls")] + [("summary", item) for item in ("NumberOfFilesExtractedWithErrors", "NumberOfSuccessfullyExtractedFiles")]:
        original = root / directory / (name + ".bqrs")
        target = REPORT / ("rust-extraction-warning-query.json" if name == "ExtractionWarnings" else "rust-" + name + "-decoded.json")
        command = [str(CLI), "bqrs", "decode", "--format=json", "--output=" + str(target), str(original)]
        result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        if result.returncode:
            raise RuntimeError(f"官方 BQRS 解码失败 {name}: {result.returncode}: {result.stdout}")
        payload = json.loads(target.read_text(encoding="utf-8"))
        rows = payload["#select"]["tuples"]
        count = rows[0][0] if directory == "summary" else len(rows)
        counts[name] = count
        records.append({"query": name, "command": command, "exit_code": result.returncode,
                        "input_bqrs_sha256": hashlib.sha256(original.read_bytes()).hexdigest(),
                        "output_file": target.name, "output_sha256": hashlib.sha256(target.read_bytes()).hexdigest(),
                        "row_count": len(rows), "count": count})
    metadata = {"decoded_at": datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).isoformat(timespec="seconds"),
                "database": str(database), "analyze_finished": analyze["finished"],
                "sarif_sha256": hashlib.sha256((REPORT / "rust.sarif").read_bytes()).hexdigest(),
                "counts": counts, "queries": records,
                "interpretation": "诊断和文件度量不能算作安全候选；文件 with-errors 度量包括 warnings，而 ExtractionErrors 只枚举 error。"}
    (REPORT / "rust-diagnostic-query-summary.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"database": str(database), "counts": counts}))
