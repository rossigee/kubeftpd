# kubeftpd (fork)

Fork of [rossigee/kubeftpd](https://github.com/rossigee/kubeftpd) at `v0.10.1`
with eight fixes that the itx-master deployment of the document scanner needs.

The operator image of this fork is published as
`zot.bankrut.lan/bankrut/kubeftpd:v0.10.6` and is deployed by
`rossgolderltd/flux-itx-master` (`50-applications/kubeftpd`).

**Upstream status.** Upstream released `v0.10.2` on 2026-11-12 with Fix 1: its
session isolation in `internal/ftp/server.go` and
`internal/ftp/session_isolation_test.go` are this change, and its
`CHANGELOG.md` describes it as the root cause of the cross-user access bugs.
Fixes 2 to 8 are not upstream. They are queued as the branch
`fix/operator-defects`, rebuilt on top of upstream master and verified there
(`gofmt`, `go build ./...`, `go vet`, the unit tests of `internal/ftp`,
`internal/storage` and `internal/backends`), waiting for a GitHub pull request;
until it lands this fork is the only image with them. Fixes 6 and 7 sit in code
that is upstream's own - the `PutObject` call and the two `Close` stubs - while
the registry that has to release them is ours.

## Fix 1 - the storage backend is resolved per FTP session

`fix(ftp): resolve the storage backend per FTP session`

`goftp.io/server/v2` hands every FTP session the same `Driver` value
(`server.Options.Driver`) and offers no per-session driver factory, so a
single `KubeDriver` serves all sessions of the process. The driver kept the
authenticated user and the storage implementation in the struct, and
`ensureUserInitializedWithContext` returned early once they were set:

```go
if driver.user != nil && driver.storageImpl != nil {
    return nil
}
```

The first session that completed a command therefore pinned its backend for
every later session of the process. In the itx-master deployment the FTP user
`scanner-archive` (MinIO, bucket `document-scans`) uploaded into the Odoo
WebDAV collection of the user `odoo-family`, and the controller log showed the
operations of the session of `scanner-archive` under `username=odoo-family`.

The user and the storage implementation of a session now live in a session
registry of the driver, keyed by the goftp session, which is unique per
connection. The username comes from the session itself, which goftp sets when
the `PASS` command succeeds.

The goftp `Perm` interface carries no session context, so the owner, the group
and the mode it asks for cannot be resolved per session. They are reported as
fixed values instead of being read from the storage of the fallback session,
which was the last place where one session could read the state of another.

`internal/ftp/server.go`, `internal/ftp/session_isolation_test.go`

## Fix 2 - a WebDAV collection answered with its children is a directory

`fix(webdav): treat a collection answered with its children as a directory`

The Odoo `bureaucracy` module answers a `PROPFIND` on a collection with the
entries of its children instead of an entry for the collection itself. The
operator could therefore not stat the collection and every user of a WebDAV
backend got `550 Directory change to / failed.` for `CWD /`.

`internal/backends/webdav.go`

## Fix 3 - the connectivity test runs again when the credentials change

`fix(controller): test the connectivity again when the credentials change`

The connectivity test of a backend only ran when the generation of the backend
changed. The credentials of a backend live in a secret, so a change of the
secret left the generation alone and the result of the first test was cached
forever: a backend whose credentials were repaired stayed unready.

The controllers now watch the credentials secret, record the version of the
secret that the test used in the annotation
`ftp.golder.org/observed-secret-version` of the backend, and test again when
the secret changed.

`internal/controller/credentials_version.go`,
`internal/controller/miniobackend_controller.go`,
`internal/controller/webdavbackend_controller.go`

## Fix 4 - the home directory is not applied twice

`fix(ftp): apply the home directory of a user once`

With chroot enabled, `validateChrootPath` resolved a request into the real
path inside the home (`/scan001.pdf` -> `/<home>/scan001.pdf`) and handed that
to the `Storage` implementation. Every `Storage` implementation then joined its
own base path, which is the same `homeDirectory`, with the path it received:

```go
return path.Join(s.basePath, relativePath) // /<home>/<home>/scan001.pdf
```

So the home was applied twice. For the scanner users of itx-master, whose home
is their bucket prefix (`/archive`, `/receipts`, ...), the consequence was that
every upload landed one level deeper than intended, at `archive/archive/<file>`
in the bucket `document-scans`, and their own directory showed nothing. The
WebDAV user masked the defect because its home is `/`, where joining the home
twice is a no-op.

The chroot validation still resolves the real path to decide whether the
request stays inside the home, and now returns the virtual path within the
home (`/scan001.pdf`) to the storage layer, which applies the home exactly
once.

`internal/ftp/server.go`, `internal/ftp/chroot_test.go`,
`internal/ftp/driver_test.go`

## Fix 5 - a MinIO key never starts with a slash, and a directory prefix ends with one

`fix(minio): list the bucket-relative keys below the requested prefix`

With `pathPrefix` empty, `getFullPath` returned the name it was given
unchanged, and the storage layer hands it a path that starts with the home
directory of the user: `/archive`. `PutObject`, `GetObject` and `StatObject`
survive that, because minio-go puts the name in the request path and MinIO
cleans the path, but `ListObjects` sends the name as the `prefix` query
parameter, and a list prefix is matched against the stored keys verbatim. No
key starts with a slash, so listing `/archive` returned nothing:

```
prefix='/archive'  0 keys
prefix='archive'  23 keys  archive/archive/archive_001845.jpg, ...
```

The trailing slash matters as much as the leading one. S3 rolls the keys up by
the delimiter after the prefix, so `archive` is a name prefix, not a directory:
MinIO answers it with the single common prefix `archive/` and no object at all.

```xml
<ListBucketResult><Prefix>archive</Prefix><KeyCount>1</KeyCount>
<CommonPrefixes><Prefix>archive/</Prefix></CommonPrefixes></ListBucketResult>
```

`archive/` is the directory, and it returns the objects in it. Both mistakes
show up the same way, as an empty home directory, which is why the fake S3
endpoint of the tests now rolls keys up into `CommonPrefixes` the way S3 does:
it used to return every key below the prefix whatever the delimiter, and that
hid the defect.

That is the other half of an empty `LIST /`: even with the home applied once
(Fix 4), the user's own directory stayed empty, and `DeleteDir` removed
nothing because it passed the same unusable prefix to `RemoveObjects`. It also
made the doubled uploads of Fix 4 invisible instead of visible as one
`archive` subdirectory.

`getFullPath` now always returns a bucket-relative key, `ListObjects` gives the
prefix its trailing slash and returns the keys relative to that prefix, which
is what its only caller (`minioStorage.ListDir`) already wanted: listing
`archive` yields `scan.jpg` and `sub/x.jpg`. The `no prefix keeps leading
slash` case of `TestMinioBackendImpl_GetFullPath` asserted the defect and is
replaced by `no prefix drops leading slash` and `root`.

`internal/backends/minio.go`, `internal/backends/minio_objects_test.go`,
`internal/storage/minio.go`, `internal/storage/minio_test.go`

## Fix 6 - a streaming upload allocated 537MiB per part

`fix(minio): bound the part size of a streaming upload`

An FTP transfer has no length until its last byte arrives, so
`storage.minioStorage.PutFile` uploads with `size = -1` and
`backends.minioBackendImpl.PutObject` passed no part size. minio-go plans an
unknown-size upload for the largest object its layout allows, and says so
itself in `api-put-object-common.go`:

```
// When object size is unknown (-1), default to 5TiB (or the maximum object
// size, when lower) to limit memory usage. This results in ~537MiB part
// sizes. For larger objects (up to the maximum object size), callers should
// set configuredPartSize explicitly to control memory usage.
```

So one scanner page of 0.3MB allocated a 537MiB buffer of live heap. Measured on
the deployed operator through its own metrics endpoint:

| state | live heap (`go_memstats_heap_inuse_bytes`) |
|---|---|
| idle | 28 MB |
| after a 1 MB upload | 556 MB |
| after a 10 MB upload | 1085 MB |
| idle afterwards, with `next_gc_bytes` | 584 MB, 1.15 GB |

The container limit was 1Gi and the garbage collector does not start before
`next_gc_bytes`, so the *second* upload of a burst crossed the cgroup limit and
the pod was OOMKilled with exit 137 in the middle of the transfer - the client
saw `EOF` on the `STOR` and `Connection refused` on the next login. Passing the
library minimum, 16MiB, bounds one upload to that instead; 10000 parts, the
maximum count, still reach a 156GB object, which no scanner produces.

## Fix 7 - the storage of a finished session is released

`fix(ftp): release the backend of a finished session`

`minioStorage.Close` and `webdavStorage.Close` said the client "does not require
explicit closing" and returned nil, while each backend owns an `http.Transport`
that keeps its connection pool, TLS session cache and read buffers reachable
until `CloseIdleConnections`. The session registry only pruned once it held
`maxDriverSessions` entries, so a hundred sessions of the printer held a hundred
transports. Each backend now closes its own transport, the storages call it
through `io.Closer`, and pruning runs on the age of a session with the bound
enforced by least recent use - `goftp.io/server/v2` never calls `Driver.Close`,
so the registry is the only place that can.

**What the registry cannot do** is release a session the moment its connection
closes, because the engine has no such signal:

- `server.Driver` (`driver.go:29-64` in v2.0.3) declares no close method, and
  nothing in the server calls one; `Session.Serve` ends at `sess.Close()`, which
  only shuts the socket and clears the session's own fields.
- `server.Notifier` (`notifier.go:8-24`) reports logins, transfers, deletions and
  directory changes - no connection end.
- The identity that reaches `KubeLogger.Print` as `session_id`, and which goftp
  logs as `"Connection Terminated"` on every close, is the unexported
  `Session.id`: 20 hex characters from `newSessionID()`'s random bytes, with no
  accessor on the `*server.Session` the driver receives in `ctx.Sess`. The log
  line carries the id, the registry keys on the pointer, and there is no way to
  pair them - pairing them by command order would break the moment two sessions
  run the same command at once.

What that costs in practice: an entry lives up to `driverSessionTTL` (30 minutes,
which is the same as the FTP idle timeout) after its connection ends, holding a
`*ftpv1.User`, its storage and its transport. The transport's idle connections
expire by themselves (`IdleConnTimeout: 30s` on the MinIO transport, the
`http.Transport` default of 90s on the WebDAV one), and 13 real transfers left the
process at exactly the 123 goroutines and ~20MB of live heap it idles at, so the
retained entries are not measurable. Releasing at disconnect would need an engine
that reports it; that is a bigger change than this fork exists to carry, and the
numbers above say it is not what limits this deployment.

## Fix 8 - the WebDAV PUT sends no Content-Type, and Odoo silently stores nothing

`fix(webdav): send image/jpeg on the PUT request`

`WriteFile` builds the PUT with
`http.NewRequest("PUT", ..., countingReader)` and set Basic auth, but no
`Content-Type` header. With nothing set, Go uses chunked transfer encoding and
the Odoo bureaucracy handler comes out with an empty raw body: `dav_upload`
creates an `ir.attachment` with `file_size` 0, `harness` DMS file with no
content, and answers `201` like a successful write. A page of a 402678-byte
scan therefore arrived at the DAV collection with nothing in it, the
verification step said the remote file was empty and returned `450` to the
scanner, and the page was lost.

Measured on the live endpoint:

| PUT sent with | remote file size after |
|---|---|
| nothing set (chunked) | **0 bytes** |
| `image/jpeg` | correct size for every 1–300 KB JPEG tested |
| `application/octet-stream` | **500** Internal Server Error |

The scanner sends JPEGs, so the fix sends `image/jpeg`. The test asserts the
header specifically, so a regression shows up as a missing
`image/jpeg` response of `500` the moment someone refactors the request.

## The routing test against the live backends

`internal/ftp/live_routing_test.go` writes a file through a session of the
MinIO user and through a session of the WebDAV user and checks that every file
is only in the backend of its own user. It needs the custom resources and the
secrets of the cluster, exported with kubectl:

```bash
mkdir -p /tmp/fixtures
kubectl -n kubeftpd get users.ftp.golder.org -o json > /tmp/fixtures/users.json
kubectl -n kubeftpd get webdavbackends.ftp.golder.org -o json > /tmp/fixtures/webdav.json
kubectl -n kubeftpd get miniobackends.ftp.golder.org -o json > /tmp/fixtures/minio.json
for s in kubeftpd-scanner-credentials kubeftpd-webdav-credentials \
         scanner-minio-credentials; do
  kubectl -n kubeftpd get secret "$s" -o json > "/tmp/fixtures/secret-$s.json"
done
# The CA of the MinioBackend is read from a secret of the same name as the
# config map that holds it.
kubectl -n kubeftpd get configmap internal-ca-certs -o jsonpath='{.data.ca\.crt}' > /tmp/ca.crt

go test -tags live -run TestLiveRouting ./internal/ftp/...
```

The test is skipped when `LIVE_FIXTURES` is not set. `LIVE_DAV_ENDPOINT`
overrides the address of the family Odoo instance, which defaults to the
ClusterIP of its service.

## The bucket repair and the end-to-end check (`hack/`)

The three scripts are what Fixes 4 and 5 were proven with on the live cluster.
They are kept so the same checks can be repeated without rewriting them:

* `hack/minio_rehome.py` moves the scanner uploads that the doubled home
  displaced, by the profile in the file name
  (`archive/archive/general_001866.jpg` to `general/general_001866.jpg`): copy,
  compare the size, then delete the displaced key. Without `--apply` it only
  reports what it would move.
* `hack/minio_cleanup.py` deletes the explicit list of probe files the earlier
  checks left in `document-scans`. Nothing is matched by a pattern, and no scan
  is ever deleted.
* `hack/mem_probe.py` watches the operator's Go runtime while FTP sessions come
  and go, so a growth in the session path shows up in `go_goroutines` and
  `go_memstats_heap_alloc_bytes` instead of staying invisible.
* `hack/upload_probe.py` uploads known sizes - single 1/10/40 MB, then one file
  per profile concurrently - and samples the runtime while they are in flight. It
  is the measurement the upload-buffer fix and the memory-limit sizing both rest
  on. Everything it writes is named `memprobe-*` and deleted again at the end.
* `hack/e2e_verify.py` is the routing check against the deployed operator: it
  logs in over FTP as a MinIO user and as the WebDAV user, uploads one file per
  session, and asserts the bucket key, the home listing, the DAV path and that
  neither backend received the other user's file.

Every credential is read from the cluster with `kubectl` when the script runs -
nothing is embedded - and the S3 requests are signed with SigV4 against the
internal CA. `hack/minio_cleanup.py` imports the client of
`hack/minio_rehome.py`, so they stay in the same directory.

## Releases and tag numbering

`.gitea/workflows/image.yml` publishes a tag of this repository to
`zot.bankrut.lan/bankrut/kubeftpd`. Fork builds were published as `v0.10.2` and
`v0.10.3` before upstream released its own, different `v0.10.2`; those two tags
are no longer in this repository, so the two images in zot have no matching
commit here. Do not reuse those numbers: a fork build of new content is
published under a fresh tag.

The cluster's `ImagePolicy` resolves the highest semver tag of the zot
repository, so **every fork release is tagged above the newest upstream release**.
`v0.10.5` carried Fixes 1–7, `v0.10.6` adds the WebDAV Content-Type fix, and the
next one is numbered above whatever upstream has published by then. Upstream's
next release may well be `v0.10.3` - a lower number with newer content - and the
policy must never be led by that.

Adopting upstream's own image is a deliberate change, made in one PR: move the
`ImageRepository` and the HelmRelease image to the upstream registry and raise
the policy range to the upstream version that carries Fixes 2 to 8. Never adopt
an image because its tag happens to sort higher than the deployed one.

## Build

```bash
docker build --network=host \
  --build-arg VERSION=v0.10.6 \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t zot.bankrut.lan/bankrut/kubeftpd:v0.10.6 .
```

The build needs the host network: the module download is very slow over the
docker bridge.

## Tests

```bash
docker run --rm --network=host -v "$PWD:/src" -v "$PWD/.gomodcache:/go/pkg/mod" \
  -v "$PWD/.gocache:/root/.cache/go-build" -w /src golang:1.27 \
  sh -c "go test -buildvcs=false ./internal/..."
```

`internal/controller` needs the envtest binaries (`etcd`, `kube-apiserver`)
and is skipped without them.
