#!/usr/bin/env python3
"""Watch the operator's Go runtime while FTP sessions come and go.

The container restarted at 08:53Z after an OOMKill of its predecessor, so the
baseline is fresh. Each step prints the runtime numbers, then the script drives
FTP sessions the way the printer does - login, LIST, QUIT, a new TCP connection
each time - and prints the same numbers after every batch.
"""

import base64
import ftplib
import subprocess
import urllib.request

METRICS = "http://10.11.4.190:8080/metrics"
FTP_HOST = "10.11.1.94"
FTP_PORT = 2121
INTERESTING = (
    "go_goroutines",
    "go_memstats_heap_inuse_bytes",
    "go_memstats_heap_objects",
    "go_memstats_heap_sys_bytes",
    "go_memstats_sys_bytes",
    "go_memstats_alloc_bytes_total",
    "process_resident_memory_bytes",
)


def secret(name, key):
    out = subprocess.run(
        ["kubectl", "--context", "itx-master", "-n", "kubeftpd", "get", "secret",
         name, "-o", f"jsonpath={{.data.{key}}}"],
        capture_output=True, text=True, check=True).stdout
    return base64.b64decode(out).decode()


def sample(label):
    with urllib.request.urlopen(METRICS, timeout=15) as resp:
        body = resp.read().decode(errors="replace")
    values = {}
    for line in body.splitlines():
        if line.startswith("#"):
            continue
        name, _, value = line.partition(" ")
        if name in INTERESTING:
            values[name] = float(value)
    mb = lambda name: f"{values.get(name, 0) / 1048576:8.1f} MB"
    print(f"{label:<28} goroutines={int(values.get('go_goroutines', 0)):5d} "
          f"heap_inuse={mb('go_memstats_heap_inuse_bytes')} "
          f"objects={int(values.get('go_memstats_heap_objects', 0)):7d} "
          f"heap_sys={mb('go_memstats_heap_sys_bytes')} "
          f"rss={mb('process_resident_memory_bytes')} "
          f"alloc_total={mb('go_memstats_alloc_bytes_total')}")
    return values


def session(user, password, upload=False):
    """One FTP connection: login, list the home, optional small upload, QUIT."""
    ftp = ftplib.FTP()
    ftp.connect(FTP_HOST, FTP_PORT, timeout=30)
    ftp.login(user, password)
    names = ftp.nlst()
    if upload:
        data = b"mem-probe\n"
        import io
        ftp.storbinary("STOR mem-probe.txt", io.BytesIO(data))
    ftp.quit()
    return len(names)


def main():
    users = ["scanner-archive", "scanner-general", "scanner-legal",
             "scanner-timesheets", "scanner-receipts", "scanner-invoices",
             "odoo-family"]
    passwords = {}
    for u in users:
        key = u.replace("scanner-", "") + "_password"
        if u == "odoo-family":
            key = "odoo_family_password"
        try:
            passwords[u] = secret("kubeftpd-scanner-credentials", key)
        except subprocess.CalledProcessError:
            passwords[u] = secret("kubeftpd-webdav-credentials", key)

    before = sample("baseline (fresh container)")
    round_no = 0
    for batch in range(1, 7):
        entries = 0
        for i in range(10):
            u = users[(batch * i) % len(users)]
            entries += session(u, passwords[u])
        round_no += 1
        after = sample(f"after {batch * 10} sessions")
        print(f"    heap_inuse delta from baseline: "
              f"{(after['go_memstats_heap_inuse_bytes'] - before['go_memstats_heap_inuse_bytes']) / 1048576:.1f} MB, "
              f"goroutine delta: "
              f"{int(after['go_goroutines'] - before['go_goroutines'])}")
    sample("immediately after last batch")
    # More work rather than a wait: the question is whether the heap keeps
    # climbing with connections or plateaus, and that is visible across batches.
    for batch in range(7, 9):
        for i in range(10):
            u = users[(batch * i) % len(users)]
            session(u, passwords[u])
        settled = sample(f"after {batch * 10} sessions")
        print(f"    heap_inuse delta from baseline: "
              f"{(settled['go_memstats_heap_inuse_bytes'] - before['go_memstats_heap_inuse_bytes']) / 1048576:.1f} MB, "
              f"goroutine delta: "
              f"{int(settled['go_goroutines'] - before['go_goroutines'])}")


if __name__ == "__main__":
    main()
