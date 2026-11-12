# kubeftpd (fork)

Fork of [rossigee/kubeftpd](https://github.com/rossigee/kubeftpd) at `v0.10.1`
with five fixes that the itx-master deployment of the document scanner needs.

The operator image of this fork is published as
`zot.bankrut.lan/bankrut/kubeftpd:v0.10.4` and is deployed by
`rossgolderltd/flux-itx-master` (`50-applications/kubeftpd`).

**Upstream status.** Upstream released `v0.10.2` on 2026-11-12 with Fix 1: its
session isolation in `internal/ftp/server.go` and
`internal/ftp/session_isolation_test.go` are this change, and its
`CHANGELOG.md` describes it as the root cause of the cross-user access bugs.
Fixes 2 to 5 are not upstream. They are queued as the branch
`fix/operator-defects`, rebuilt on top of upstream master and verified there
(`gofmt`, `go build ./...`, `go vet`, the unit tests of `internal/ftp`,
`internal/storage` and `internal/backends`), waiting for a GitHub pull request;
until it lands this fork is the only image with them.

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

## Releases and tag numbering

`.gitea/workflows/image.yml` publishes a tag of this repository to
`zot.bankrut.lan/bankrut/kubeftpd`. Fork builds were published as `v0.10.2` and
`v0.10.3` before upstream released its own, different `v0.10.2`; today the only
`v0.10.x` tag of this repository is `v0.10.4`, so those two images in zot have no
matching commit here. Do not reuse those numbers: a fork build of new content is
published under a fresh tag.

The cluster's `ImagePolicy` resolves the highest semver tag of the zot
repository. Upstream's next release is expected to be `v0.10.3` — a lower number
than the deployed `v0.10.4`, although its content is newer — so **a fork release
rebased on upstream is tagged `v0.10.5` or later**. That keeps the policy
selecting a build that carries Fixes 2 to 5 whatever upstream numbers its
releases.

Adopting upstream's own image is a deliberate change, made in one PR: move the
`ImageRepository` and the HelmRelease image to the upstream registry and raise
the policy range to the upstream version that carries Fixes 2 to 5. Never adopt
an image because its tag happens to sort higher than the deployed one.

## Build

```bash
docker build --network=host \
  --build-arg VERSION=v0.10.4 \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t zot.bankrut.lan/bankrut/kubeftpd:v0.10.4 .
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
