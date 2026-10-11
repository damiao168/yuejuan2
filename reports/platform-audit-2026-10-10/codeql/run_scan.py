import argparse
import datetime
import json
import os
import pathlib
import subprocess
import time

ROOT = pathlib.Path(__file__).parent
CLI = ROOT / "codeql" / "codeql.exe"
SOURCE = ROOT / "source-20261011"
REPORT = pathlib.Path("D:/project/yuejuan/reports/platform-audit-2026-10-10/codeql")
CACHE = ROOT / "common-caches"
TZ = datetime.timezone(datetime.timedelta(hours=8))


def now():
    return datetime.datetime.now(TZ).isoformat(timespec="seconds")


def execute(language, stage, arguments, environment):
    output = REPORT / f"{language}-{stage}.log"
    command = [str(CLI), *arguments, f"--common-caches={CACHE}"]
    metadata = {"language": language, "stage": stage, "started": now(), "command": command, "log": str(output)}
    if language == "rust":
        metadata["environment_overrides"] = {key: environment[key] for key in ("RAYON_NUM_THREADS", "CARGO_BUILD_JOBS", "CARGO_NET_OFFLINE", "TEMP", "TMP") if key in environment}
    started = time.monotonic()
    with output.open("w", encoding="utf-8") as log:
        process = subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, env=environment, cwd=SOURCE)
    metadata.update(exit_code=process.returncode, finished=now(), elapsed_seconds=round(time.monotonic() - started, 2))
    (REPORT / f"{language}-{stage}-execution.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(metadata, ensure_ascii=False), flush=True)
    return process.returncode


def scan(language, action, ram=2048, threads=2, max_disk_cache=2048):
    # 语言显式指定，查询包来自已校验的 bundle；不请求远端仓库或上传结果。
    environment = dict(os.environ)
    for key in ("GITHUB_TOKEN", "GH_TOKEN", "CODEQL_REGISTRIES_AUTH"):
        environment.pop(key, None)
    environment.update(GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOMAXPROCS=str(threads), GOFLAGS=f"-p={threads}", CARGO_NET_OFFLINE="true", CARGO_BUILD_JOBS=str(threads), PSExecutionPolicyPreference="Bypass")
    database = ROOT / "databases" / f"{language}-20261011"
    if language == "actions":
        database = ROOT / "databases" / "actions-20261011-process-policy"
    if language == "javascript":
        database = ROOT / "databases" / "javascript-20261011-context"
    if language == "rust":
        # 前两次失败数据库分别保留；本次只调整提取缓存、压缩与资源参数后重试。
        database = ROOT / "databases" / ("rust-20261011-low-memory" if SOURCE.name == "source-20261011" else "rust-20261011-context")
        # 只限制本次原生提取器的线程和临时目录；不改变源码、功能配置或查询范围。
        temporary = ROOT / "rust-low-memory-temp"
        temporary.mkdir(exist_ok=True)
        environment.update(RAYON_NUM_THREADS="1", TEMP=str(temporary), TMP=str(temporary))
    database.parent.mkdir(exist_ok=True)
    if action in ("create", "all"):
        # 引擎会把更小的 RAM 预算上调到 2048 MiB，所以显式使用实际下限。
        arguments = ["database", "create", str(database), f"--language={language}", f"--source-root={SOURCE}", f"--threads={threads}", f"--ram={ram}"]
        if language == "go":
            # Go 只提取类型和语法信息，不运行测试或业务服务；测试文件也进入数据库。
            arguments += ["--build-mode=autobuild", f"--working-dir={SOURCE / 'services' / 'api-gateway'}", "--extractor-option=go.extract_tests=true"]
        elif language == "rust":
            # none 模式仍可能编译并运行构建脚本和宏；不是应用的完整构建或测试。
            arguments += ["--build-mode=none", f"--working-dir={SOURCE / 'apps' / 'desktop-client' / 'src-tauri'}", f"--extractor-option=rust.cargo_target_dir={ROOT / 'databases' / 'rust-20261011-toolchain-ready' / 'working' / 'target'}", "--extractor-option=rust.trap.compression=gzip"]
        else:
            arguments += ["--build-mode=none"]
        if execute(language, "create", arguments, environment) != 0:
            return 1
    if action in ("analyze", "all"):
        pack = f"codeql/{language}-queries"
        suite = f"{pack}:codeql-suites/{language}-security-and-quality.qls"
        # RAM 同时分给 Java 堆与原生缓存；失败重试可以加内存、降线程，仍执行完整套件。
        arguments = ["database", "analyze", str(database), suite, f"--threads={threads}", f"--ram={ram}", f"--max-disk-cache={max_disk_cache}", "--min-disk-free=8192", "--no-download", "--format=sarifv2.1.0", f"--output={REPORT / (language + '.sarif')}", f"--sarif-category={language}", "--sarif-include-query-help=always", "--no-sarif-add-file-contents", "--no-sarif-add-snippets"]
        if execute(language, "analyze", arguments, environment) != 0:
            return 1
    return 0


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("language", choices=("python", "javascript", "go", "rust", "actions"))
    parser.add_argument("--action", choices=("create", "analyze", "all"), default="all")
    parser.add_argument("--ram", type=int, default=2048)
    parser.add_argument("--threads", type=int, default=2)
    parser.add_argument("--source", type=pathlib.Path, default=SOURCE)
    parser.add_argument("--max-disk-cache", type=int, default=2048)
    options = parser.parse_args()
    SOURCE = options.source.resolve()
    if options.ram < 2048 or options.threads < 1:
        parser.error("RAM must be at least 2048 MiB and threads must be positive")
    raise SystemExit(scan(options.language, options.action, options.ram, options.threads, options.max_disk_cache))
