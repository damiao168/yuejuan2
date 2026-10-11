"""用临时图纸验证验收入口拒绝实体声明，不改写已有图纸或历史验收证据。"""

import hashlib
import json
from pathlib import Path

import build_diagrams
import pytest
import verify_delivery
from defusedxml.common import DTDForbidden


def test_generator_round_trip_keeps_native_editable_xml(tmp_path, monkeypatch):
    monkeypatch.setattr(build_diagrams, "BASE", tmp_path)
    (tmp_path / "specs").mkdir()
    spec = {
        "file": "fixture.drawio",
        "pages": [{"id": "fixture", "title": "安全回读", "nodes": [], "edges": []}],
    }
    (tmp_path / "specs/fixture.json").write_text(json.dumps(spec), encoding="utf-8")

    assert build_diagrams.build() is False
    monkeypatch.setattr(verify_delivery, "BASE", tmp_path)
    path = tmp_path / "diagrams/fixture.drawio"
    errors = []
    result = verify_delivery.validate_diagram(
        path.name, {path.name: spec}, {path.name: hashlib.sha256(path.read_bytes()).hexdigest()}, errors
    )
    assert errors == []
    assert result["pages"] == 1


@pytest.mark.parametrize("declaration", [
    '<!DOCTYPE mxfile>',
    '<!DOCTYPE mxfile [<!ENTITY text "expanded">]>',
    '<!DOCTYPE mxfile [<!ENTITY text SYSTEM "file:///synthetic-secret">]>',
    '<!DOCTYPE mxfile SYSTEM "http://127.0.0.1:9/synthetic.dtd">',
    '<!DOCTYPE mxfile [<!ENTITY a "x"><!ENTITY text "&a;&a;&a;">]>',
])
@pytest.mark.parametrize("encoding", ["utf-8", "utf-16"])
def test_delivery_rejects_dtd_before_entity_resolution(tmp_path, monkeypatch, declaration, encoding):
    monkeypatch.setattr(verify_delivery, "BASE", tmp_path)
    (tmp_path / "diagrams").mkdir()
    payload = f'<?xml version="1.0" encoding="{encoding}"?>{declaration}<mxfile/>'
    (tmp_path / "diagrams/fixture.drawio").write_bytes(payload.encode(encoding))

    with pytest.raises(DTDForbidden):
        verify_delivery.validate_diagram("fixture.drawio", {}, {}, [])


def test_existing_native_diagrams_remain_readable():
    diagrams = Path(build_diagrams.__file__).resolve().parents[1] / "diagrams"
    for name in verify_delivery.EXPECTED:
        root = verify_delivery.safe_xml.parse(diagrams / name, forbid_dtd=True).getroot()
        assert root.tag == "mxfile"
        assert root.findall("diagram")
