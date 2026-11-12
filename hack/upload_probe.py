#!/usr/bin/env python3
"""Measure what FTP uploads cost the operator's Go heap.

Run it against a deployed operator before and after an image change: the point is
the live-heap curve (go_memstats_heap_alloc_bytes), not RSS, because pages of an
allocated-but-unfilled buffer are not resident and the scavenger returns them.

minio-go plans an upload of unknown length - which every FTP STOR is - for the
largest object its multipart layout allows, so an image that does not pin the
part size allocates a ~537MiB buffer per transfer.

Everything it writes is named memprobe-* and deleted again at the end; the bucket
object count is printed before and after.
"""

import base64
import concurrent.futures
import ftplib
import io
import os
import re
import subprocess
import sys
import threading
import time
import urllib.parse
import urllib.request
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

METRICS = os.environ.get("PROBE_METRICS", "http://10.11.4.190:8080/metrics")
FTP_HOST = "10.11.1.94"
FTP_PORT = 21
DAV_BASE = "http://10.11.7.16:8069/.dav/webdav-scanner/1"
NAMESPACE = "kubeftpd"

PEAK_KEYS = (
    "go_memstats_heap_alloc_bytes",
    "go_memstats_heap_inuse_bytes",
    "go_memstats_next_gc_bytes",
    "go_memstats_heap_sys_bytes",
    "process_resident_memory_bytes",
    "go_goroutines",
)
STOP = threading.Event()


def kubectl_secret(name, key):
    out = subprocess.run(
        ["kubectl", "--context", "itx-master", "-n", NAMESPACE, "get", "secret",
         name, "-o", f"jsonpath={{.data.{key}}}"],
        capture_output=True, text=True, check=True).stdout
    return base64.b64decode(out).decode()


def read():
    with urllib.request.urlopen(METRICS, timeout=15) as resp:
        body = resp.read().decode(errors="replace")
    values = {}
    for line in body.splitlines():
        if line.startswith("#"):
            continue
        name, _, value = line.partition(" ")
        if name in PEAK_KEYS:
            values[name] = float(value)
    return values


def fmt(values):
    mb = lambda key: f"{values.get(key, 0) / 1048576:7.0f} MB"
    return (f"live={mb('go_memstats_heap_alloc_bytes')} inuse={mb('go_memstats_heap_inuse_bytes')} next_gc={mb('go_memstats_next_gc_bytes')} "
            f"heap_sys={mb('go_memstats_heap_sys_bytes')} rss={mb('process_resident_memory_bytes')} "
            f"goroutines={int(values.get('go_goroutines', 0))}")


def sampler(peak):
    while not STOP.is_set():
        values = read()
        for key in PEAK_KEYS:
            peak[key] = max(peak.get(key, 0), values.get(key, 0))
        time.sleep(0.3)


def bucket_keys():
    from minio_rehome import Client, secret
    client = Client(secret("scanner-minio-credentials", "accessKeyID"),
                    secret("scanner-minio-credentials", "secretAccessKey"))
    return client, sorted(client.list_keys())


def ftp_upload(user, password, name, data):
    ftp = ftplib.FTP()
    ftp.connect(FTP_HOST, FTP_PORT, timeout=180)
    ftp.login(user, password)
    ftp.storbinary(f"STOR {name}", io.BytesIO(data))
    ftp.quit()


def dav_delete(name):
    token = base64.b64encode(
        f"webdav-scanner:{kubectl_secret('kubeftpd-webdav-credentials', 'odoo_family_password')}".encode()
    ).decode()
    req = urllib.request.Request(f"{DAV_BASE}/{urllib.parse.quote(name)}", method="DELETE")
    req.add_header("Authorization", f"Basic {token}")
    with urllib.request.urlopen(req, timeout=30) as resp:
        return resp.status


def dav_names():
    token = base64.b64encode(
        f"webdav-scanner:{kubectl_secret('kubeftpd-webdav-credentials', 'odoo_family_password')}".encode()
    ).decode()
    req = urllib.request.Request(f"{DAV_BASE}/", method="PROPFIND")
    req.add_header("Depth", "1")
    req.add_header("Authorization", f"Basic {token}")
    with urllib.request.urlopen(req, timeout=30) as resp:
        body = resp.read().decode(errors="replace")
    return set(re.findall(r"<displayname>([^<]*)</displayname>", body))


PROFILES = [
    ("scanner-archive", "archive_password", "kubeftpd-scanner-credentials"),
    ("scanner-general", "general_password", "kubeftpd-scanner-credentials"),
    ("scanner-legal", "legal_password", "kubeftpd-scanner-credentials"),
    ("scanner-timesheets", "timesheets_password", "kubeftpd-scanner-credentials"),
    ("scanner-receipts", "receipts_password", "kubeftpd-scanner-credentials"),
    ("scanner-invoices", "invoices_password", "kubeftpd-scanner-credentials"),
    ("odoo-family", "odoo_family_password", "kubeftpd-scanner-credentials"),
]


def run(label, transfers):
    """transfers: list of (user, password, name, data). Runs them concurrently."""
    peak = {}
    STOP.clear()
    thread = threading.Thread(target=sampler, args=(peak,), daemon=True)
    thread.start()
    start = time.monotonic()
    if len(transfers) == 1:
        ftp_upload(*transfers[0])
    else:
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(transfers)) as pool:
            futures = [pool.submit(ftp_upload, *item) for item in transfers]
            for future in futures:
                future.result()
    elapsed = time.monotonic() - start
    STOP.set()
    thread.join(timeout=5)
    total_mb = sum(len(item[3]) for item in transfers) / 1048576
    print(f"{label:<34} {len(transfers)} upload(s), {total_mb:5.0f} MB of payload "
          f"in {elapsed:5.1f}s  peak: {fmt(peak)}")
    return peak


def main():
    sizes = [int(s) for s in os.environ.get("SIZES_MB", "1,10,40").split(",")]
    passwords = {user: kubectl_secret(store, key) for user, key, store in PROFILES}

    client, keys = bucket_keys()
    print(f"bucket objects before: {len(keys)}")
    print(f"idle:                  {fmt(read())}")

    for size_mb in sizes:
        data = os.urandom(size_mb * 1048576)
        run(f"single {size_mb} MB", [("scanner-archive", passwords["scanner-archive"],
                                     f"memprobe-{size_mb}mb.bin", data)])

    each = int(os.environ.get("CONCURRENT_MB", "10")) * 1048576
    data = os.urandom(each)
    transfers = [(user, passwords[user], f"memprobe-concurrent-{i}.bin", data)
                 for i, (user, _, _) in enumerate(PROFILES)]
    run(f"concurrent {len(transfers)}x{each // 1048576} MB", transfers)

    print(f"settled:               {fmt(read())}")

    client, keys = bucket_keys()
    probes = [k for k in keys if "memprobe" in k]
    for key in probes:
        client.delete(key)
    dav_left = [name for name in dav_names() if "memprobe" in name]
    for name in dav_left:
        dav_delete(name)
    _, after = bucket_keys()
    dav_after = [name for name in dav_names() if "memprobe" in name]
    print(f"cleanup: deleted {len(probes)} MinIO probe object(s) {probes}")
    print(f"           deleted {len(dav_left)} DAV probe object(s) {dav_left}")
    print(f"bucket objects after: {len(after)}  (probe left: {[k for k in after if 'memprobe' in k]})")
    print(f"DAV probe objects left: {dav_after}")


if __name__ == "__main__":
    main()
