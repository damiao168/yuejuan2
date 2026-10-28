"""本地开发证书轮换：新目录预检、原子切换、健康核验与失败恢复；不删除旧密钥。"""
import argparse
from contextlib import contextmanager
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import uuid

KEYS = ("EDUGRADE_INTERNAL_CA_DIR", "EDUGRADE_GRADING_AGENT_TLS_CERTS_DIR", "EDUGRADE_MATH_VERIFY_TLS_CERTS_DIR")
CONSUMERS = {"api-gateway", "grading-agent", "math-verification-worker", "ocr-worker", "math-recognition-worker", "paper-formula-worker", "image-quality-worker", "page-processing-worker", "subjective-grading-worker", "nginx"}


def load_module(filename):
    spec = importlib.util.spec_from_file_location(filename.replace("-", "_"), Path(__file__).with_name(filename + ".py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def digest(value):
    return hashlib.sha256(value).hexdigest()


def patch_settings(content, values):
    lines = content.decode("utf-8-sig").splitlines(keepends=True)
    found = set()
    result = []
    newline = "\r\n" if b"\r\n" in content else "\n"
    for line in lines:
        key = line.partition("=")[0].strip()
        if key not in values:
            result.append(line)
        elif key not in found:
            found.add(key)
            if values[key] is not None:
                result.append(f"{key}={values[key]}{newline}")
    for key, value in values.items():
        if key not in found and value is not None:
            if result and not result[-1].endswith(("\n", "\r")):
                result[-1] += newline
            result.append(f"{key}={value}{newline}")
    return "".join(result).encode("utf-8")


def atomic_write(path, content):
    descriptor, temporary = tempfile.mkstemp(prefix=".env.rotation-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


@contextmanager
def rotation_lock(path):
    # 由操作系统持有锁；进程终止后会释放，残留空锁文件不会阻止恢复。
    with path.open("a+b") as stream:
        stream.seek(0)
        if os.name == "nt":
            import msvcrt
            if not path.stat().st_size:
                stream.write(b"0"); stream.flush(); stream.seek(0)
            msvcrt.locking(stream.fileno(), msvcrt.LK_NBLCK, 1)
        else:
            import fcntl
            fcntl.flock(stream.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        try:
            yield
        finally:
            if os.name == "nt":
                stream.seek(0); msvcrt.locking(stream.fileno(), msvcrt.LK_UNLCK, 1)


def run_compose(directory, env_file, arguments):
    result = subprocess.run(["docker", "compose", "--env-file", str(env_file), "-f", str(directory / "docker-compose.yml"), *arguments], cwd=directory, capture_output=True, text=True)
    if result.returncode:
        # Compose 的原始输出可能包含配置值；错误只报告阶段，不回显捕获输出。
        raise RuntimeError("Docker Compose stage failed; inspect service logs locally")
    return result.stdout


def restart_services(directory, env_file, services, runner):
    runner(directory, env_file, ["up", "-d", "--no-deps", "--force-recreate", "--wait", "--wait-timeout", "120", *services])


def rotate(env_file, directory, executable, days=365, recover=False, runner=run_compose, validator=None, generator=None):
    tls = load_module("ensure-internal-tls")
    validator = validator or (lambda path: tls.prepare_tls(path, directory, executable))
    generator = generator or load_module("generate-internal-test-certificates").generate_certificates
    if any(key in os.environ for key in KEYS):
        raise ValueError("Remove TLS directory process overrides before rotation; env-file must be authoritative")
    settings = tls.read_settings(env_file)
    if settings.get("EDUGRADE_ENV", "local") not in {"local", "development", "dev", "test"}:
        raise ValueError("Automatic certificate generation is limited to local development")
    journal_file = env_file.with_name(env_file.name + ".tls-rotation.json")
    with rotation_lock(env_file.with_name(env_file.name + ".tls-rotation.lock")):
        settings = tls.read_settings(env_file)
        if settings.get("EDUGRADE_ENV", "local") not in {"local", "development", "dev", "test"}:
            raise ValueError("Automatic certificate generation is limited to local development")
        # 本地生成器只签发两个受管理内部端点；不能更换 CA 后让自定义 API HTTPS 证书失去信任。
        if settings.get("EDUGRADE_INTERNAL_TLS_PORT", "0") not in {"", "0"} or settings.get("EDUGRADE_INTERNAL_API_BASE_URL", "").startswith("https://"):
            raise ValueError("API HTTPS requires coordinated PKI rotation; the local two-endpoint generator cannot replace its CA")
        if recover:
            journal = json.loads(journal_file.read_text(encoding="utf-8"))
            original = env_file.read_bytes()
            if set(journal["old_values"]) != set(KEYS) or not set(journal["services"]).issubset(CONSUMERS) or not {"api-gateway", "grading-agent"}.issubset(journal["services"]):
                raise ValueError("Invalid recovery journal; no configuration changed")
            if any(value is not None and (not isinstance(value, str) or "\n" in value or "\r" in value) for value in journal["old_values"].values()):
                raise ValueError("Invalid recovery directory values")
            if digest(original) not in {journal["candidate_hash"], journal.get("original_hash"), journal.get("restored_hash")}:
                raise ValueError("Configuration changed since rotation; automatic recovery will not overwrite it")
            restored = patch_settings(original, journal["old_values"])
            journal["restored_hash"] = digest(restored)
            atomic_write(journal_file, json.dumps(journal).encode())
            atomic_write(env_file, restored)
            restart_services(directory, env_file, journal["services"], runner)
            journal_file.unlink()
            return "Previous TLS configuration restored and services healthy"
        if journal_file.exists():
            raise ValueError("An unfinished rotation exists; use --recover before another rotation")
        if type(days) is not int or not 1 <= days <= 3650:
            raise ValueError("Certificate validity must be 1..3650 days")
        services = sorted(CONSUMERS.intersection(runner(directory, env_file, ["ps", "--status", "running", "--services"]).split()))
        if "api-gateway" not in services or "grading-agent" not in services:
            raise ValueError("Start the existing local API and grading services before a controlled rotation")
        original = env_file.read_bytes()
        old_values = {key: settings.get(key) for key in KEYS}
        root = directory / "certs" / "rotations" / uuid.uuid4().hex
        generator(root, executable, days)
        values = dict(zip(KEYS, [str(root / "internal-ca"), str(root / "grading-agent"), str(root / "math-verifier")]))
        candidate = patch_settings(original, values)
        staging = env_file.with_name(".env.rotation-validation-" + uuid.uuid4().hex)
        try:
            atomic_write(staging, candidate)
            validator(staging)
            runner(directory, staging, ["config", "--quiet"])
        finally:
            staging.unlink(missing_ok=True)
        # 生成或预检期间的外部配置修改不能被覆盖；日志只留非秘密路径和内容摘要。
        if env_file.read_bytes() != original:
            raise ValueError("Configuration changed during preparation; generated certificates were preserved")
        journal = {"original_hash": digest(original), "candidate_hash": digest(candidate), "old_values": old_values, "services": services, "certificate_directory": str(root)}
        atomic_write(journal_file, json.dumps(journal).encode())
        try:
            atomic_write(env_file, candidate)
            restart_services(directory, env_file, services, runner)
        except Exception as cause:
            if env_file.read_bytes() != candidate:
                raise RuntimeError("Rotation interrupted and configuration changed; recovery journal retained") from cause
            restored = patch_settings(candidate, old_values)
            journal["restored_hash"] = digest(restored)
            atomic_write(journal_file, json.dumps(journal).encode())
            atomic_write(env_file, restored)
            try:
                restart_services(directory, env_file, services, runner)
            except Exception as rollback_error:
                raise RuntimeError("Previous configuration restored; service recovery failed, journal retained for --recover") from rollback_error
            journal_file.unlink()
            raise RuntimeError("New TLS health check failed; previous configuration and services restored") from cause
        journal_file.unlink()
        return "New TLS certificates active; services healthy; old certificate directories preserved"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--compose-directory", type=Path, required=True)
    parser.add_argument("--days", type=int, default=365)
    parser.add_argument("--openssl", default="")
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--recover", action="store_true")
    args = parser.parse_args()
    if not args.apply:
        print("Plan: validate a new local certificate directory, switch TLS directories, recreate running consumers, check health, restore on failure. Use --apply to execute; --recover --apply resumes a failed rollback.")
        return
    try:
        tls = load_module("ensure-internal-tls")
        print(rotate(args.env_file.resolve(strict=True), args.compose_directory.resolve(strict=True), tls.openssl_executable(args.openssl), args.days, args.recover))
    except (OSError, ValueError, RuntimeError) as error:
        parser.exit(1, str(error) + "\n")


if __name__ == "__main__":
    main()
