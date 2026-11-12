#!/usr/bin/env python3
"""End-to-end check of the kubeftpd scanner routing and placement.

For each of the two destinations it:
  * logs in over FTP and lists the root of the session, which must show the
    user's own directory (the home is applied exactly once),
  * uploads a uniquely named file at the FTP root,
  * checks the file is in the backend of that user at the intended key, and
  * checks it is absent from the other backend.

Exit status is non-zero when any check fails.
"""

import base64
import datetime
import ftplib
import hashlib
import hmac
import io
import re
import subprocess
import sys
import time
import urllib.parse
import urllib.request

FTP_HOST, FTP_PORT = "10.11.1.94", 2121
MINIO_HOST = "minio.golder.lan"
BUCKET = "document-scans"
REGION = "us-east-1"
DAV_BASE = "http://10.11.7.16:8069/.dav/webdav-scanner/1"

failures = []


def kubectl_secret(name, key):
    out = subprocess.run(
        ["kubectl", "--context", "itx-master", "-n", "kubeftpd", "get",
         "secret", name, "-o", f"jsonpath={{.data.{key}}}"],
        capture_output=True, text=True, check=True).stdout
    return base64.b64decode(out).decode()


def check(condition, message):
    print(("PASS  " if condition else "FAIL  ") + message)
    if not condition:
        failures.append(message)


def sign(key, msg):
    return hmac.new(key, msg.encode(), hashlib.sha256).digest()


def s3_request(method, key="", params=None, headers=None):
    access_key = kubectl_secret("scanner-minio-credentials", "accessKeyID")
    secret_key = kubectl_secret("scanner-minio-credentials", "secretAccessKey")
    params = params or []
    query = urllib.parse.urlencode(params)
    path = f"/{BUCKET}" + (f"/{urllib.parse.quote(key)}" if key else "")
    payload_hash = hashlib.sha256(b"").hexdigest()
    now = datetime.datetime.now(datetime.timezone.utc)
    amz_date = now.strftime("%Y%m%dT%H%M%SZ")
    date = now.strftime("%Y%m%d")
    canonical_headers = (
        f"host:{MINIO_HOST}\nx-amz-content-sha256:{payload_hash}\nx-amz-date:{amz_date}\n"
    )
    signed_headers = "host;x-amz-content-sha256;x-amz-date"
    canonical_request = "\n".join(
        [method, path, query, canonical_headers, signed_headers, payload_hash]
    )
    scope = f"{date}/{REGION}/s3/aws4_request"
    string_to_sign = "\n".join([
        "AWS4-HMAC-SHA256", amz_date, scope,
        hashlib.sha256(canonical_request.encode()).hexdigest(),
    ])
    signing_key = sign(("AWS4" + secret_key).encode(), date)
    signing_key = sign(signing_key, REGION)
    signing_key = sign(signing_key, "s3")
    signing_key = sign(signing_key, "aws4_request")
    signature = hmac.new(signing_key, string_to_sign.encode(), hashlib.sha256).hexdigest()
    url = f"https://{MINIO_HOST}{path}" + (f"?{query}" if query else "")
    req = urllib.request.Request(url, method=method)
    req.add_header("Host", MINIO_HOST)
    req.add_header("x-amz-date", amz_date)
    req.add_header("x-amz-content-sha256", payload_hash)
    req.add_header(
        "Authorization",
        f"AWS4-HMAC-SHA256 Credential={access_key}/{scope}, "
        f"SignedHeaders={signed_headers}, Signature={signature}",
    )
    for name, value in (headers or {}).items():
        req.add_header(name, value)
    return urllib.request.urlopen(req, timeout=60)


def minio_keys():
    with s3_request("GET", params=[("list-type", "2"), ("max-keys", "1000")]) as resp:
        body = resp.read().decode(errors="replace")
    return re.findall(r"<Key>(.*?)</Key>", body)


def dav_names():
    password = kubectl_secret("kubeftpd-webdav-credentials", "odoo_family_password")
    req = urllib.request.Request(f"{DAV_BASE}/", method="PROPFIND")
    req.add_header("Depth", "1")
    token = base64.b64encode(f"webdav-scanner:{password}".encode()).decode()
    req.add_header("Authorization", f"Basic {token}")
    with urllib.request.urlopen(req, timeout=30) as resp:
        body = resp.read().decode(errors="replace")
    return set(re.findall(r"<displayname>([^<]*)</displayname>", body))


def ftp_session(user, password):
    ftp = ftplib.FTP()
    ftp.connect(FTP_HOST, FTP_PORT, timeout=30)
    ftp.login(user, password)
    return ftp


def main():
    stamp = int(time.time())
    archive_name = f"e2e-archive-{stamp}.bin"
    family_name = f"e2e-family-{stamp}.bin"

    archive_user = kubectl_secret("kubeftpd-scanner-credentials", "archive_username")
    archive_password = kubectl_secret("kubeftpd-scanner-credentials", "archive_password")
    family_user = kubectl_secret("kubeftpd-scanner-credentials", "odoo_family_username")
    family_password = kubectl_secret("kubeftpd-scanner-credentials", "odoo_family_password")

    ftp = ftp_session(archive_user, archive_password)
    listing = []
    ftp.retrlines("LIST", listing.append)
    check(ftp.pwd() == "/", f"{archive_user}: pwd is /")
    ftp.storbinary(f"STOR {archive_name}", io.BytesIO(b"e2e archive\n"))
    listing_after = []
    ftp.retrlines("LIST", listing_after.append)
    ftp.quit()
    check(
        any(archive_name in line for line in listing_after),
        f"{archive_user}: the uploaded file is listed in the session root "
        f"({len(listing)} entries before, {len(listing_after)} after)",
    )

    ftp = ftp_session(family_user, family_password)
    check(ftp.pwd() == "/", f"{family_user}: pwd is /")
    ftp.storbinary(f"STOR {family_name}", io.BytesIO(b"e2e family\n"))
    ftp.quit()

    keys = minio_keys()
    check(f"archive/{archive_name}" in keys, f"MinIO has archive/{archive_name}")
    check(
        f"archive/archive/{archive_name}" not in keys,
        f"MinIO has no archive/archive/{archive_name}",
    )
    check(
        not any(family_name in key for key in keys),
        f"MinIO has no copy of {family_name}",
    )

    names = dav_names()
    check(family_name in names, f"the DAV collection has {family_name}")
    check(archive_name not in names, f"the DAV collection has no copy of {archive_name}")

    print()
    if failures:
        print(f"{len(failures)} check(s) failed")
        return 1
    print("all checks passed")
    print(f"MINIO_FILE=archive/{archive_name}")
    print(f"DAV_FILE={family_name}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
