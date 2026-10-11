"""只读采集当前 CodeQL 查询 JVM 的 GC 计数，不读取参数、环境或应用数据。"""

import argparse
import ctypes
import ctypes.wintypes as w
import datetime
import json
import pathlib
import struct


def collect(pid):
    # Windows HotSpot 已公开此共享映射；只申请 FILE_MAP_READ，不开启代理或修改进程。
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel.OpenFileMappingW.argtypes = [w.DWORD, w.BOOL, w.LPCWSTR]
    kernel.OpenFileMappingW.restype = w.HANDLE
    kernel.MapViewOfFile.argtypes = [w.HANDLE, w.DWORD, w.DWORD, w.DWORD, ctypes.c_size_t]
    kernel.MapViewOfFile.restype = ctypes.c_void_p
    kernel.UnmapViewOfFile.argtypes = [ctypes.c_void_p]
    kernel.CloseHandle.argtypes = [w.HANDLE]
    handle = kernel.OpenFileMappingW(4, False, f"hsperfdata_pc_{pid}")
    if not handle:
        raise ctypes.WinError(ctypes.get_last_error())
    view = kernel.MapViewOfFile(handle, 4, 0, 0, 65536)
    if not view:
        error = ctypes.get_last_error()
        kernel.CloseHandle(handle)
        raise ctypes.WinError(error)
    data = ctypes.string_at(view, 65536)
    kernel.UnmapViewOfFile(view)
    kernel.CloseHandle(handle)
    endian = "<" if data[4] == 1 else ">"
    offset, count = struct.unpack_from(endian + "ii", data, 24)
    values, units = {}, {}
    prefixes = ("sun.gc.collector.", "sun.gc.generation.", "sun.gc.policy.",
                "sun.rt.safepoint", "sun.rt.applicationTime", "sun.os.hrt.frequency", "java.threads.")
    for _ in range(count):
        size, name_offset, vector_length = struct.unpack_from(endian + "iii", data, offset)
        kind = chr(data[offset + 12])
        value_offset = struct.unpack_from(endian + "i", data, offset + 16)[0]
        name_start = offset + name_offset
        name = data[name_start:data.index(0, name_start)].decode("utf-8")
        if name.startswith(prefixes):
            start = offset + value_offset
            if kind == "J":
                value = struct.unpack_from(endian + "q", data, start)[0]
            elif kind == "I":
                value = struct.unpack_from(endian + "i", data, start)[0]
            elif kind == "B" and vector_length:
                value = data[start:start + vector_length].split(b"\0", 1)[0].decode("utf-8")
            else:
                value = None
            values[name] = value
            units[name] = data[offset + 14]
        offset += size
    return {
        "collected_at": datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).isoformat(timespec="seconds"),
        "pid": pid,
        "method": "OpenFileMappingW / MapViewOfFile FILE_MAP_READ; whitelist counters only",
        "counters": values,
        "units_enum": units,
        "reference": "https://raw.githubusercontent.com/openjdk/jdk/master/src/hotspot/os/windows/perfMemory_windows.cpp",
        "limitations": "非同步读取，计数可能在采集时推进；共享映射仅含 JVM 指标，不能证明查询语义完整。",
    }


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("pid", type=int)
    args = parser.parse_args()
    record = collect(args.pid)
    destination = pathlib.Path(__file__).parent / "rust-query-jvm-perf-samples.jsonl"
    with destination.open("a", encoding="utf-8") as stream:
        stream.write(json.dumps(record, ensure_ascii=False) + "\n")
    print(json.dumps(record, ensure_ascii=False))
