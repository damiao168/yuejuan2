"""首次准备本地短期TLS；每次启动前校验实际挂载文件，已有密钥不覆盖。"""

import argparse
import importlib.util
import ipaddress
import os
from pathlib import Path, PurePosixPath
import shutil
import ssl
import subprocess
from urllib.parse import urlsplit


SERVICE_SETTINGS = (
    ("grading-agent", "GRADING_AGENT", "grading-agent-tls", "grading-agent", "EDUGRADE_AI_SERVICE_URL", "https://grading-agent:8100"),
    ("math-verification-worker", "MATH_VERIFY", "math-verifier-tls", "math-verifier", "EDUGRADE_INTERNAL_MATH_VERIFY_BASE_URL", "https://math-verification-worker:8092"),
)


def read_settings(env_file):
    settings = {}
    for line in env_file.read_text(encoding="utf-8-sig").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        key, separator, value = line.partition("=")
        if not separator:
            raise ValueError("Invalid deployment env line")
        settings[key.strip()] = value.strip().strip('"').strip("'")
    # Compose 的宿主机环境变量优先于 --env-file；证书预检使用同一有效值。
    settings.update({key: value for key, value in os.environ.items() if key.startswith("EDUGRADE_")})
    return settings


def host_directory(compose_directory, value):
    return (compose_directory / value).resolve()


def mounted_file(host_directory, container_file, mount):
    path = PurePosixPath(container_file)
    mount_path = PurePosixPath(mount)
    if not path.is_absolute() or ".." in path.parts or not path.is_relative_to(mount_path):
        raise ValueError(f"TLS file must be inside its Compose mount: {container_file!r}")
    relative = path.relative_to(mount_path)
    if not relative.parts:
        raise ValueError("TLS file must name a file within the mount")
    return host_directory.joinpath(*relative.parts)


def openssl_command(executable, *arguments):
    result = subprocess.run([str(executable), *map(str, arguments)], capture_output=True, text=True)
    if result.returncode:
        raise ValueError(f"TLS validation failed: {result.stderr.strip() or result.stdout.strip()}")


def openssl_executable(configured=""):
    executable = configured or os.environ.get("EDUGRADE_TEST_OPENSSL") or shutil.which("openssl")
    if not executable or not Path(executable).is_absolute() or not Path(executable).is_file():
        raise ValueError("Install OpenSSL and set EDUGRADE_TEST_OPENSSL to its absolute executable path")
    return Path(executable).resolve(strict=True)


def url_hostname(value):
    url = urlsplit(value)
    if url.scheme != "https" or not url.hostname or url.username or url.password:
        raise ValueError("Internal service and health URLs require HTTPS without embedded credentials")
    return url.hostname


def prepare_tls(env_file, compose_directory, executable, generate_local=False):
    settings = read_settings(env_file)
    root = compose_directory / "certs"
    ca_directory = host_directory(compose_directory, settings.get("EDUGRADE_INTERNAL_CA_DIR") or "./certs/internal-ca")
    ca_file = mounted_file(ca_directory, settings.get("EDUGRADE_INTERNAL_CA_FILE", ""), "/run/secrets/internal-ca")
    services = []
    for service, prefix, mount, directory, url_key, default_url in SERVICE_SETTINGS:
        base = host_directory(compose_directory, settings.get(f"EDUGRADE_{prefix}_TLS_CERTS_DIR") or f"./certs/{directory}")
        certificate = mounted_file(base, settings.get(f"EDUGRADE_{prefix}_TLS_CERT_FILE") or f"/run/secrets/{mount}/server.crt", f"/run/secrets/{mount}")
        key = mounted_file(base, settings.get(f"EDUGRADE_{prefix}_TLS_KEY_FILE") or f"/run/secrets/{mount}/server.key", f"/run/secrets/{mount}")
        hostnames = {url_hostname(settings.get(url_key, default_url)), url_hostname(settings.get(f"EDUGRADE_{prefix}_HEALTH_URL", default_url))}
        services.append((service, certificate, key, hostnames))

    files = [ca_file, *[path for _, cert, key, _ in services for path in (cert, key)]]
    defaults = [root / "internal-ca/ca.crt", *[path for _, _, _, directory, _, _ in SERVICE_SETTINGS for path in (root / directory / "server.crt", root / directory / "server.key")]]
    # 仅在本地默认目录完全不存在时生成；局部失败、过期或自定义PKI均保留原密钥并停止。
    if not all(path.is_file() for path in files):
        if generate_local and settings.get("EDUGRADE_ENV", "local") in {"local", "development", "dev", "test"} and not root.exists() and files == [path.resolve() for path in defaults]:
            source = Path(__file__).with_name("generate-internal-test-certificates.py")
            spec = importlib.util.spec_from_file_location("disposable_internal_tls", source)
            generator = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(generator)
            generator.generate_certificates(root, executable)
            print(f"Created disposable two-day local certificates: {root}")
        else:
            missing = ", ".join(str(path) for path in files if not path.is_file())
            raise ValueError(f"TLS files missing: {missing}. Existing/custom certificate directories are preserved; prepare a new directory and update the three *_DIR values in .env")

    ssl.create_default_context(cafile=str(ca_file))
    for service, certificate, key, hostnames in services:
        # load_cert_chain 校验证书与私钥配对；OpenSSL同时验证有效期、签发链和实际调用DNS/IP。
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(str(certificate), str(key))
        openssl_command(executable, "x509", "-in", certificate, "-checkend", "0", "-noout")
        for hostname in sorted(hostnames):
            try:
                ipaddress.ip_address(hostname)
                verify_option = "-verify_ip"
            except ValueError:
                verify_option = "-verify_hostname"
            openssl_command(executable, "verify", "-purpose", "sslserver", "-CAfile", ca_file, verify_option, hostname, certificate)
    print("Internal TLS certificate files, keys, trust chains, expiry and hostnames verified.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--compose-directory", type=Path, required=True)
    parser.add_argument("--openssl", default="")
    parser.add_argument("--generate-local-if-missing", action="store_true")
    arguments = parser.parse_args()
    try:
        prepare_tls(arguments.env_file.resolve(strict=True), arguments.compose_directory.resolve(strict=True), openssl_executable(arguments.openssl), arguments.generate_local_if_missing)
    except (ValueError, OSError, ssl.SSLError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"Internal TLS preparation failed: {error}\n")


if __name__ == "__main__":
    main()
