# Development

## Prerequisites

- Go 1.27 or later
- Docker
- A Kubernetes cluster for end-to-end tests (kind or minikube works)

## Setup

```bash
git clone https://github.com/rossigee/kubeftpd.git
cd kubeftpd
go mod download
./scripts/setup-pre-commit.sh   # installs the pre-commit hooks
```

## Make targets

Run `make help` for the full list. The common ones:

| Target | What it does |
|--------|--------------|
| `make build` | Build the `kubeftpd` binary |
| `make run` | Run the controller from your host against the current kubeconfig |
| `make test` | Unit tests for the controller, FTP server and storage packages |
| `make test-coverage` | Unit tests with a coverage profile |
| `make test-integration` | Integration tests in `test/e2e` (needs a cluster) |
| `make test-all` | Unit, coverage and integration tests |
| `make fmt` / `make vet` | `go fmt` and `go vet` |
| `make lint` | golangci-lint (version pinned in the Makefile) |
| `make security-scan` | gosec |
| `make manifests` | Regenerate CRDs and RBAC from the Go types |
| `make generate` | Regenerate deepcopy code |
| `make docker-build` / `make docker-push` | Build and push the image |
| `make install` / `make uninstall` | Install or remove the CRDs in the current cluster |
| `make deploy` / `make undeploy` | Deploy or remove the controller |

Run `make manifests generate` after changing any API type. CI fails if the generated files are out of date.

## Testing

- **Unit tests** live next to the code (`*_test.go`). They use envtest for controller tests, so `make test` downloads the Kubernetes test binaries on first run.
- **Integration tests** in `test/e2e` need a running cluster.

CI runs `make test-coverage`. Coverage is reported to Codecov but not enforced.

## Code quality

- **golangci-lint** (`make lint`) runs in CI and in pre-commit.
- **gosec** (`make security-scan`) runs in CI and fails the build on findings. Suppress a finding only with a `#nosec` comment that explains why the code is safe.
- **govulncheck** runs in CI.
- **Pre-commit hooks** run formatting, lint and unit tests before each commit.

## Contributing

1. Fork the repository and create a branch from `master`.
2. Make the change with tests.
3. Run `make lint test security-scan` locally.
4. Add an entry under `## [Unreleased]` in [CHANGELOG.md](../CHANGELOG.md).
5. Open a pull request against `master`. CI must pass before merge.

Releases are cut from tags. `scripts/release.sh <version>` refuses to run unless `CHANGELOG.md` has a section for that version.
