import argparse,datetime,json,os,pathlib,subprocess,time
REPORT=pathlib.Path("D:/project/yuejuan/reports/platform-audit-2026-10-10/codeql")
TZ=datetime.timezone(datetime.timedelta(hours=8))
p=argparse.ArgumentParser();p.add_argument("--source",default="D:/tools/audit-codeql-20261010/source-20261011");a=p.parse_args()
source=pathlib.Path(a.source)/"apps/desktop-client/src-tauri"
temporary=pathlib.Path("D:/tools/audit-codeql-20261010/rust-context-probe-temp");temporary.mkdir(exist_ok=True)
environment=dict(os.environ)
for key in ("GITHUB_TOKEN","GH_TOKEN","CODEQL_REGISTRIES_AUTH"):environment.pop(key,None)
environment.update(CARGO_NET_OFFLINE="true",CARGO_BUILD_JOBS="1",RAYON_NUM_THREADS="1",TEMP=str(temporary),TMP=str(temporary))
command=["rustup","run","1.98.1","cargo","check","--offline","--locked","--workspace","--all-features","--all-targets","--message-format=json","--target-dir","D:/tools/audit-codeql-20261010/databases/rust-20261011-toolchain-ready/working/target"]
started=time.monotonic();metadata={"command":command,"working_directory":str(source),"started":datetime.datetime.now(TZ).isoformat(timespec="seconds"),"purpose":"串行定位 generate_context 的实际编译诊断；离线锁定依赖，不执行测试或应用，不修改维护源码。","environment_overrides":{key:environment[key] for key in ("CARGO_NET_OFFLINE","CARGO_BUILD_JOBS","RAYON_NUM_THREADS","TEMP","TMP")}}
with (REPORT/"rust-macro-probe.stdout.jsonl").open("w",encoding="utf-8") as out,(REPORT/"rust-macro-probe.stderr.log").open("w",encoding="utf-8") as err:
 result=subprocess.run(command,cwd=source,env=environment,stdout=out,stderr=err)
metadata.update(exit_code=result.returncode,finished=datetime.datetime.now(TZ).isoformat(timespec="seconds"),elapsed_seconds=round(time.monotonic()-started,2))
(REPORT/"rust-macro-probe-execution.json").write_text(json.dumps(metadata,ensure_ascii=False,indent=2),encoding="utf-8")
print(json.dumps(metadata,ensure_ascii=False),flush=True)
raise SystemExit(result.returncode)
