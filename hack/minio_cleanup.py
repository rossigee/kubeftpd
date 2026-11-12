#!/usr/bin/env python3
"""Delete the probe files the earlier end-to-end checks left in document-scans.

These are not scans: they are the uploads of the routing and placement checks
(e2e-*, livecheck-*, test*) of the earlier sessions. The printer scans that
landed one level deep are moved by minio_rehome.py, not deleted.
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from minio_rehome import Client, secret  # noqa: E402

TEST_KEYS = [
    "archive/livecheck-minio-1794340022.bin",
    "receipts/receipts/final-e2e-1785591732.bin",
    "receipts/receipts/invoices/clean-e2e-1785591762.bin",
    "receipts/receipts/kubeftpd-e2e-1785590367.bin",
    "receipts/receipts/real-e2e-1785591755.bin",
    "receipts/receipts/receipts/test.txt",
    "receipts/receipts/test.bin",
    "test-1782619225.txt",
    "test-final-1782627087.txt",
]


def main():
    access_key = os.environ.get("MINIO_ACCESS_KEY")
    secret_key = os.environ.get("MINIO_SECRET_KEY")
    if not access_key or not secret_key:
        access_key = secret("scanner-minio-credentials", "accessKeyID")
        secret_key = secret("scanner-minio-credentials", "secretAccessKey")

    client = Client(access_key, secret_key)
    present = set(client.list_keys())
    for key in TEST_KEYS:
        if key not in present:
            print(f"absent  {key}")
            continue
        client.delete(key)
        print(f"deleted {key}")
    print("done")


if __name__ == "__main__":
    main()
