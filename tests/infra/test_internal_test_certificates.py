import importlib.util
import os
import shutil
import stat
import subprocess
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest

GENERATOR_PATH = (
    Path(__file__).resolve().parents[2]
    / "infra/docker-compose/scripts/generate-internal-test-certificates.py"
)
SERVICE_CERTIFICATES = (
    ("grading-agent", "grading-agent"),
    ("math-verification-worker", "math-verifier"),
)


@pytest.fixture(scope="module")
def certificate_generator():
    # 脚本名含连字符；直接加载实际文件，避免测试另写一套签发流程。
    spec = importlib.util.spec_from_file_location("internal_test_certificate_generator", GENERATOR_PATH)
    assert spec is not None
    assert spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@pytest.fixture(scope="module")
def openssl_executable() -> Path:
    # 缺少真实工具应失败，不能用跳过代替证书签发和校验结果。
    override = os.environ.get("EDUGRADE_TEST_OPENSSL", "").strip()
    if override and (not Path(override).is_absolute() or not Path(override).is_file()):
        pytest.fail("EDUGRADE_TEST_OPENSSL 必须是现有 OpenSSL 可执行文件的绝对路径")
    executable = override or shutil.which("openssl")
    if not executable:
        pytest.fail("真实证书回归需要 OpenSSL；可用 EDUGRADE_TEST_OPENSSL 指定可执行文件")
    return Path(executable).resolve(strict=True)


def _openssl(executable: Path, *arguments) -> str:
    result = subprocess.run(
        [str(executable), *map(str, arguments)],
        check=True,
        capture_output=True,
        text=True,
        timeout=30,
    )
    return result.stdout


def _invoke_main(generator, monkeypatch, output: Path, environment: str, executable, days=None) -> None:
    arguments = [
        str(GENERATOR_PATH),
        "--output-dir", str(output),
        "--environment", environment,
        "--openssl", str(executable),
    ]
    if days is not None:
        arguments.extend(("--days", str(days)))
    monkeypatch.setattr(
        sys,
        "argv",
        arguments,
    )
    generator.main()


def _directory_snapshot(directory: Path) -> dict:
    snapshot = {}
    for path in [directory, *directory.rglob("*")]:
        metadata = path.stat()
        snapshot[path.relative_to(directory).as_posix()] = (
            stat.S_IFMT(metadata.st_mode),
            stat.S_IMODE(metadata.st_mode),
            metadata.st_mtime_ns,
            path.read_bytes() if path.is_file() else None,
        )
    return snapshot


@pytest.mark.parametrize("days", [None, 365])
def test_main_generates_verifiable_isolated_certificates(
    certificate_generator, openssl_executable, tmp_path, monkeypatch, capsys, days,
):
    output = tmp_path / "certificates"
    _invoke_main(certificate_generator, monkeypatch, output, "test", openssl_executable, days)

    public = output / "internal-ca"
    private = output / "private-ca"
    ca_certificate = public / "ca.crt"
    ca_key = private / "ca.key"
    assert {path.name for path in output.iterdir()} == {
        "internal-ca", "private-ca", "grading-agent", "math-verifier",
    }
    assert {path.name for path in public.iterdir()} == {"ca.crt"}
    assert {path.name for path in private.iterdir()} == {
        "ca.cnf", "ca.key", "grading-agent.cnf", "grading-agent.csr",
        "math-verification-worker.cnf", "math-verification-worker.csr",
    }
    assert "CA:TRUE" in _openssl(openssl_executable, "x509", "-in", ca_certificate,
                                 "-noout", "-ext", "basicConstraints")
    assert _openssl(openssl_executable, "verify", "-CAfile", ca_certificate,
                    "-check_ss_sig", ca_certificate).strip().endswith(": OK")

    key_pairs = [(ca_certificate, ca_key)]
    for service, directory in SERVICE_CERTIFICATES:
        destination = output / directory
        certificate = destination / "server.crt"
        key = destination / "server.key"
        assert {path.name for path in destination.iterdir()} == {"server.crt", "server.key"}
        # 名称校验与 CA 链校验一起执行，不能仅凭证书文本中的 CN 认定 DNS 可用。
        for hostname in (service, "localhost"):
            assert _openssl(
                openssl_executable, "verify", "-CAfile", ca_certificate,
                "-purpose", "sslserver", "-verify_hostname", hostname, certificate,
            ).strip().endswith(": OK")
        san = _openssl(openssl_executable, "x509", "-in", certificate,
                       "-noout", "-ext", "subjectAltName")
        assert f"DNS:{service}" in san
        assert "DNS:localhost" in san
        assert "IP Address:127.0.0.1" in san
        assert san.count("DNS:") == 2
        assert "CA:FALSE" in _openssl(openssl_executable, "x509", "-in", certificate,
                                      "-noout", "-ext", "basicConstraints")
        key_pairs.append((certificate, key))

    for certificate, key in key_pairs:
        # 读取实际 X.509 日期，防止只把 CLI 参数接通而 CA 或某个服务仍只签发两天。
        dates = _openssl(openssl_executable, "x509", "-in", certificate, "-noout", "-dates")
        boundaries = [
            datetime.strptime(line.split("=", 1)[1], "%b %d %H:%M:%S %Y GMT").replace(tzinfo=timezone.utc)
            for line in dates.splitlines()
        ]
        assert boundaries[1] - boundaries[0] == timedelta(days=2 if days is None else days)
        certificate_public_key = _openssl(openssl_executable, "x509", "-in", certificate,
                                          "-pubkey", "-noout")
        private_public_key = _openssl(openssl_executable, "pkey", "-in", key, "-pubout")
        assert certificate_public_key.strip() == private_public_key.strip()

    # Windows 由 ACL 管理访问权；这里只核对 POSIX 上脚本实际设置的权限位。
    if os.name != "nt":
        assert stat.S_IMODE(private.stat().st_mode) == 0o700
        assert stat.S_IMODE(ca_key.stat().st_mode) == 0o600
        for directory in (output, public, *(output / name for _, name in SERVICE_CERTIFICATES)):
            assert stat.S_IMODE(directory.stat().st_mode) == 0o755
        for certificate, key in key_pairs:
            assert stat.S_IMODE(certificate.stat().st_mode) == 0o444
            if key != ca_key:
                assert stat.S_IMODE(key.stat().st_mode) == 0o444

    messages = capsys.readouterr().out
    assert f"Disposable certificates created: {output.resolve()}" in messages
    assert "never mount private-ca" in messages


@pytest.mark.parametrize("days", [0, -1, 3651, "1.5", "invalid"])
def test_main_rejects_invalid_validity_before_creating_output(
    certificate_generator, tmp_path, monkeypatch, days,
):
    output = tmp_path / "uncreated-parent" / "certificates"
    with pytest.raises(SystemExit) as error:
        _invoke_main(certificate_generator, monkeypatch, output, "local",
                     tmp_path / "unused-openssl.exe", days)
    assert error.value.code == 2
    assert not output.parent.exists()


@pytest.mark.parametrize("days", [0, -1, 3651, 1.5, "365", True])
def test_direct_generation_rejects_invalid_validity_before_creating_output(
    certificate_generator, tmp_path, days,
):
    output = tmp_path / "uncreated-parent" / "certificates"
    with pytest.raises(ValueError, match="validity must be an integer"):
        certificate_generator.generate_certificates(output, tmp_path / "unused-openssl.exe", valid_days=days)
    assert not output.parent.exists()


def test_main_rejects_existing_output_without_changing_contents(
    certificate_generator, openssl_executable, tmp_path, monkeypatch,
):
    output = tmp_path / "existing"
    private = output / "private-ca"
    private.mkdir(parents=True)
    (private / "ca.key").write_bytes(b"synthetic-existing-ca-key\x00")
    (output / "sentinel.txt").write_text("keep-existing-output", encoding="ascii")
    (output / "empty-directory").mkdir()
    # 连目录结构和修改时间一起保留，拒绝操作不能先清空旧证书再抛出异常。
    before = _directory_snapshot(output)

    with pytest.raises(FileExistsError):
        _invoke_main(certificate_generator, monkeypatch, output, "test", openssl_executable)

    assert _directory_snapshot(output) == before


def test_main_rejects_production_before_creating_output(
    certificate_generator, tmp_path, monkeypatch, capsys,
):
    output = tmp_path / "uncreated-parent" / "certificates"
    with pytest.raises(SystemExit) as error:
        _invoke_main(certificate_generator, monkeypatch, output, "production",
                     tmp_path / "unused-openssl.exe")

    assert error.value.code == 2
    assert "invalid choice: 'production'" in capsys.readouterr().err
    assert not output.parent.exists()


@pytest.mark.parametrize("path_kind", ["relative", "missing_absolute"])
def test_main_rejects_invalid_openssl_before_creating_output(
    certificate_generator, tmp_path, monkeypatch, capsys, path_kind,
):
    output = tmp_path / "uncreated-parent" / "certificates"
    executable = "openssl" if path_kind == "relative" else tmp_path / "missing-openssl.exe"
    with pytest.raises(SystemExit) as error:
        _invoke_main(certificate_generator, monkeypatch, output, "test", executable)

    assert error.value.code == 2
    assert "Provide an installed OpenSSL executable by absolute path." in capsys.readouterr().err
    assert not output.parent.exists()
