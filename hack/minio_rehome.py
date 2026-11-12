#!/usr/bin/env python3
"""Move the scanner uploads that the home-directory defect placed one level deep.

Both the operator that served the printer before kubeftpd and kubeftpd itself
applied the home directory of the FTP user twice, so an upload of the user
scanner-archive landed at archive/archive/<file> in the bucket document-scans
instead of archive/<file>. The users' own FTP view (their home directory)
therefore never showed their scans.

The directory of a doubled key is the backend of the FTP session that carried
the upload, which was not always the session of the profile that scanned the
page: kubeftpd pinned the backend of the first session for the whole process,
and the printer keeps a control connection alive across a profile change. The
file name is the reliable part: the printer names a scan after the profile
that produced it (archive_001845.jpg, general_001866.jpg, legal_001884.jpg,
timesheet_001892.jpg, ...), one counter shared by all profiles.

So a scan goes to the home directory of the profile in its name, not to the
directory it landed in:

    archive/archive/general_001866.jpg  ->  general/general_001866.jpg

That matters downstream: a timesheet_ scan that stayed in receipts/ would be
picked up as an expense.

Only the scans themselves are moved. The test artifacts of earlier probes are
left for minio_cleanup.py.

Run with --apply to copy every scan to its intended key, check the size, and
delete the doubled key. Without --apply it only reports.
"""

import argparse
import base64
import datetime
import hashlib
import hmac
import os
import re
import subprocess
import sys
import urllib.parse
import urllib.request

HOST = "minio.golder.lan"
BUCKET = "document-scans"
REGION = "us-east-1"

# The profile in the file name, and the home directory of that profile.
HOME_BY_PROFILE = {
    "archive": "archive",
    "general": "general",
    "legal": "legal",
    "receipts": "receipts",
    "invoice": "invoices",
    "timesheet": "timesheets",
}


def sign(key: bytes, msg: str) -> bytes:
    return hmac.new(key, msg.encode(), hashlib.sha256).digest()


class Client:
    def __init__(self, access_key: str, secret_key: str):
        self.access_key = access_key
        self.secret_key = secret_key

    def _auth(self, method: str, path: str, query: str, payload_hash: str, amz_date: str, headers):
        date = amz_date[:8]
        scope = f"{date}/{REGION}/s3/aws4_request"
        signed = {"host": HOST, "x-amz-content-sha256": payload_hash, "x-amz-date": amz_date}
        for name, value in (headers or {}).items():
            if name.lower().startswith("x-amz-"):
                signed[name.lower()] = value
        signed_headers = ";".join(sorted(signed))
        canonical = "\n".join(
            [
                method,
                path,
                query,
                "".join(f"{name}:{signed[name]}\n" for name in sorted(signed)),
                signed_headers,
                payload_hash,
            ]
        )
        string_to_sign = "\n".join(
            [
                "AWS4-HMAC-SHA256",
                amz_date,
                scope,
                hashlib.sha256(canonical.encode()).hexdigest(),
            ]
        )
        key = sign(("AWS4" + self.secret_key).encode(), date)
        key = sign(key, REGION)
        key = sign(key, "s3")
        key = sign(key, "aws4_request")
        signature = hmac.new(key, string_to_sign.encode(), hashlib.sha256).hexdigest()
        return (
            f"AWS4-HMAC-SHA256 Credential={self.access_key}/{scope}, "
            f"SignedHeaders={signed_headers}, Signature={signature}",
            payload_hash,
        )

    def request(self, method: str, key: str = "", params=None, headers=None, body=b""):
        # The canonical query string is the parameters sorted by name, so the
        # request and the signature cannot disagree about their order.
        params = sorted(params or [])
        query = urllib.parse.urlencode(params)
        path = f"/{BUCKET}" + (f"/{urllib.parse.quote(key)}" if key else "")
        payload_hash = hashlib.sha256(body).hexdigest()
        amz_date = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        auth, payload_hash = self._auth(method, path, query, payload_hash, amz_date, headers)
        url = f"https://{HOST}{path}" + (f"?{query}" if query else "")
        req = urllib.request.Request(url, method=method, data=body if body else None)
        req.add_header("Host", HOST)
        req.add_header("x-amz-date", amz_date)
        req.add_header("x-amz-content-sha256", payload_hash)
        req.add_header("Authorization", auth)
        for name, value in (headers or {}).items():
            req.add_header(name, value)
        return urllib.request.urlopen(req, timeout=60)

    def list_keys(self):
        keys = []
        token = None
        while True:
            params = [("list-type", "2"), ("max-keys", "1000")]
            if token:
                params.append(("continuation-token", token))
            with self.request("GET", params=params) as resp:
                body = resp.read().decode(errors="replace")
            keys += re.findall(r"<Key>(.*?)</Key>", body)
            match = re.search(r"<NextContinuationToken>(.*?)</NextContinuationToken>", body)
            token = match.group(1) if match else None
            if "<IsTruncated>true</IsTruncated>" not in body:
                return keys

    def copy(self, source: str, target: str):
        quoted = urllib.parse.quote(f"{BUCKET}/{source}", safe="/")
        self.request(
            "PUT", target, headers={"x-amz-copy-source": quoted}
        ).close()

    def head(self, key: str):
        with self.request("HEAD", key) as resp:
            return int(resp.headers["Content-Length"])

    def delete(self, key: str):
        self.request("DELETE", key).close()


# A doubled key: <prefix>/<prefix>/<rest>, the home directory of the session
# applied twice. <rest> may hold further directories (receipts/invoices/...).
DOUBLED = re.compile(r"^(?P<dir>[a-z]+)/(?P=dir)/(?P<rest>.+)$")
# A scan of the printer: the profile it was scanned with and its counter.
SCAN = re.compile(r"^(?P<profile>[a-z]+)_\d+\.(?:jpg|jpeg|pdf)$", re.IGNORECASE)


def intended_key(key: str):
    """Return the key the scan was meant for, or None if it is not a scan."""
    doubled = DOUBLED.match(key)
    if not doubled:
        return None
    rest = doubled.group("rest")
    scan = SCAN.match(rest.rsplit("/", 1)[-1])
    if not scan:
        return None
    home = HOME_BY_PROFILE.get(scan.group("profile").lower())
    if not home:
        return None
    return f"{home}/{rest}", doubled.group("dir") != home


def secret(name: str, key: str) -> str:
    out = subprocess.run(
        ["kubectl", "--context", "itx-master", "-n", "kubeftpd", "get",
         "secret", name, "-o", f"jsonpath={{.data.{key}}}"],
        capture_output=True, text=True, check=True).stdout
    return base64.b64decode(out).decode()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()

    access_key = os.environ.get("MINIO_ACCESS_KEY")
    secret_key = os.environ.get("MINIO_SECRET_KEY")
    if not access_key or not secret_key:
        access_key = secret("scanner-minio-credentials", "accessKeyID")
        secret_key = secret("scanner-minio-credentials", "secretAccessKey")

    client = Client(access_key, secret_key)
    moved = 0
    moved_across = 0
    for key in sorted(client.list_keys()):
        result = intended_key(key)
        if not result:
            continue
        target, across = result
        if not args.apply:
            note = "  (scanned by another profile than the session)" if across else ""
            print(f"would move {key} -> {target}{note}")
            moved += 1
            moved_across += 1 if across else 0
            continue
        client.copy(key, target)
        size = client.head(target)
        source_size = client.head(key)
        if size != source_size:
            sys.exit(f"copy of {key} is {size} bytes, source is {source_size}; stopped")
        client.delete(key)
        print(f"moved {key} -> {target} ({size} bytes)")
        moved += 1
        moved_across += 1 if across else 0

    print(
        f"{moved} scan(s) {'moved' if args.apply else 'to move'}, "
        f"{moved_across} of them into the home of their own profile"
    )


if __name__ == "__main__":
    main()
