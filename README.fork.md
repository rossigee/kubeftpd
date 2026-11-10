# kubeftpd (fork)

Fork of [rossigee/kubeftpd](https://github.com/rossigee/kubeftpd) at `v0.10.1`
with two fixes that the itx-master deployment of the document scanner needs.

The operator image of this fork is published as
`zot.bankrut.lan/bankrut/kubeftpd:v0.10.3` and is deployed by
`rossgolderltd/flux-itx-master` (`50-applications/kubeftpd`).

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

## Build

```bash
docker build --network=host \
  --build-arg VERSION=v0.10.3 \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t zot.bankrut.lan/bankrut/kubeftpd:v0.10.3 .
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
