import concurrent.futures
import hashlib
import json
import pathlib
import time
import urllib.request

URL = "https://github.com/github/codeql-action/releases/download/codeql-bundle-v2.27.2/codeql-bundle-win64.tar.gz"
SIZE = 698921984
SHA256 = "c4834b337e2db85bdd4e7e97455319ff70d28f06abb2987eff9ec0f62d51dcea"
ROOT = pathlib.Path(__file__).parent
PARTS = ROOT / "bundle-parts"
PARTS.mkdir(exist_ok=True)
CHUNK = 4 * 1024 * 1024
START = time.monotonic()


def fetch(index):
    begin = index * CHUNK
    end = min(SIZE, begin + CHUNK) - 1
    target = PARTS / f"{index:05d}.part"
    if target.exists() and target.stat().st_size == end - begin + 1:
        return target
    for attempt in range(5):
        try:
            # Range 必须被官方 CDN 接受；不能把整包响应误当作一个分片。
            request = urllib.request.Request(URL, headers={"Range": f"bytes={begin}-{end}"})
            with urllib.request.urlopen(request, timeout=90) as response:
                expected = f"bytes {begin}-{end}/{SIZE}"
                if response.status != 206 or response.headers.get("Content-Range") != expected:
                    raise RuntimeError(f"Unexpected range response for part {index}")
                with target.open("wb") as output:
                    while block := response.read(64 * 1024):
                        output.write(block)
            if target.stat().st_size != end - begin + 1:
                raise RuntimeError(f"Incomplete part {index}")
            return target
        except Exception:
            if attempt == 4:
                raise
            time.sleep(min(attempt + 1, 5))


if __name__ == "__main__":
    count = (SIZE + CHUNK - 1) // CHUNK
    finished = 0
    # 只并发下载公共工具；源码、数据库和扫描结果都不会进入 HTTP 请求。
    with concurrent.futures.ThreadPoolExecutor(max_workers=24) as pool:
        for future in concurrent.futures.as_completed([pool.submit(fetch, i) for i in range(count)]):
            future.result()
            finished += 1
            if finished % 8 == 0 or finished == count:
                print(json.dumps({"parts": finished, "total": count, "elapsed_seconds": round(time.monotonic() - START, 1)}), flush=True)
    bundle = ROOT / "codeql-bundle-win64.tar.gz"
    digest = hashlib.sha256()
    with bundle.open("wb") as output:
        for i in range(count):
            with (PARTS / f"{i:05d}.part").open("rb") as source:
                while block := source.read(1024 * 1024):
                    output.write(block)
                    digest.update(block)
    actual = digest.hexdigest()
    if actual != SHA256:
        raise RuntimeError("Official SHA256 mismatch")
    print(json.dumps({"complete": True, "sha256": actual, "bytes": SIZE, "elapsed_seconds": round(time.monotonic() - START, 1)}), flush=True)
