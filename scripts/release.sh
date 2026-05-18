#!/bin/bash
#
# Release script for kubeftpd
# Updates version references across all files before creating a release
#
# Usage: ./scripts/release.sh <new-version>
#   e.g. ./scripts/release.sh v0.2.5

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

function log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

function log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

function log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

function usage() {
    echo "Usage: $0 <new-version>"
    echo ""
    echo "Examples:"
    echo "  $0 v0.2.5"
    echo "  $0 v1.0.0"
    echo ""
    echo "This script will:"
    echo "  1. Validate the new version format"
    echo "  2. Get the current version from git tags"
    echo "  3. Update version references in all relevant files"
    echo "  4. Show a diff of changes"
    echo "  5. Ask for confirmation before committing"
    echo ""
    exit 1
}

function validate_version() {
    local version="$1"
    if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        log_error "Invalid version format: $version"
        log_error "Expected format: vMAJOR.MINOR.PATCH (e.g., v0.2.5)"
        exit 1
    fi
}

function get_current_version() {
    cd "$ROOT_DIR"
    git tag --sort=-version:refname | head -1 | tr -d '\n'
}

function sed_replace() {
    local file="$1"
    local pattern="$2"
    if [[ "$OSTYPE" == "darwin"* ]]; then
        sed -i '' "$pattern" "$file"
    else
        sed -i "$pattern" "$file"
    fi
}

function update_versions() {
    local new_version="$1"
    local new_version_no_v="${new_version#v}"

    cd "$ROOT_DIR"

    log_info "Updating version to $new_version"

    # cmd/main.go - version constant
    if [[ -f "cmd/main.go" ]]; then
        log_info "Updating cmd/main.go"
        sed_replace "cmd/main.go" "s/version = \"v[0-9]\+\.[0-9]\+\.[0-9]\+\"/version = \"$new_version\"/"
    fi

    # Makefile - version
    if [[ -f "Makefile" ]]; then
        log_info "Updating Makefile"
        sed_replace "Makefile" "s/^VERSION ?= v[0-9]\+\.[0-9]\+\.[0-9]\+$/VERSION ?= $new_version/"
    fi

    # Dockerfile - version ARG
    if [[ -f "Dockerfile" ]]; then
        log_info "Updating Dockerfile"
        sed_replace "Dockerfile" "s/ARG VERSION=v[0-9]\+\.[0-9]\+\.[0-9]\+/ARG VERSION=$new_version/"
    fi

    # Legacy chart: chart/kubeftpd/Chart.yaml
    if [[ -f "chart/kubeftpd/Chart.yaml" ]]; then
        log_info "Updating chart/kubeftpd/Chart.yaml"
        sed_replace "chart/kubeftpd/Chart.yaml" "s/^version: [0-9]\+\.[0-9]\+\.[0-9]\+$/version: $new_version_no_v/"
        sed_replace "chart/kubeftpd/Chart.yaml" "s/^appVersion: \"v[0-9]\+\.[0-9]\+\.[0-9]\+\"$/appVersion: \"$new_version\"/"
    fi

    # CI-relevant chart: charts/kubeftpd/Chart.yaml
    if [[ -f "charts/kubeftpd/Chart.yaml" ]]; then
        log_info "Updating charts/kubeftpd/Chart.yaml"
        sed_replace "charts/kubeftpd/Chart.yaml" "s/^version: [0-9]\+\.[0-9]\+\.[0-9]\+$/version: $new_version_no_v/"
        sed_replace "charts/kubeftpd/Chart.yaml" "s/^appVersion: \"v[0-9]\+\.[0-9]\+\.[0-9]\+\"$/appVersion: \"$new_version\"/"
    fi

    # Legacy Helm values.yaml - image tag
    if [[ -f "chart/kubeftpd/values.yaml" ]]; then
        log_info "Updating chart/kubeftpd/values.yaml"
        sed_replace "chart/kubeftpd/values.yaml" "s/tag: \"v[0-9]\+\.[0-9]\+\.[0-9]\+\"/tag: \"$new_version\"/"
    fi

    # config/manager/kustomization.yaml
    if [[ -f "config/manager/kustomization.yaml" ]]; then
        log_info "Updating config/manager/kustomization.yaml"
        sed_replace "config/manager/kustomization.yaml" "s/newTag: v[0-9]\+\.[0-9]\+\.[0-9]\+/newTag: $new_version/"
    fi

    # config/production/kustomization.yaml
    if [[ -f "config/production/kustomization.yaml" ]]; then
        log_info "Updating config/production/kustomization.yaml"
        sed_replace "config/production/kustomization.yaml" "s/newTag: v[0-9]\+\.[0-9]\+\.[0-9]\+/newTag: $new_version/"
    fi

    # README.md - container image examples
    if [[ -f "README.md" ]]; then
        log_info "Updating README.md"
        sed_replace "README.md" "s/ghcr.io\/rossigee\/kubeftpd:v[0-9]\+\.[0-9]\+\.[0-9]\+/ghcr.io\/rossigee\/kubeftpd:$new_version/g"
        sed_replace "README.md" "s/controller.image.tag=v[0-9]\+\.[0-9]\+\.[0-9]\+/controller.image.tag=$new_version/g"
    fi

    # Legacy chart README.md
    if [[ -f "chart/kubeftpd/README.md" ]]; then
        log_info "Updating chart/kubeftpd/README.md"
        sed_replace "chart/kubeftpd/README.md" "s/rossigee\/kubeftpd:v[0-9]\+\.[0-9]\+\.[0-9]\+/rossigee\/kubeftpd:$new_version/g"
    fi

    # Release manifests directory - update contents only
    if [[ -d "releases" ]]; then
        log_info "Checking releases/ directory for version references"
        for file in releases/*; do
            if [[ -f "$file" ]]; then
                sed_replace "$file" "s/v[0-9]\+\.[0-9]\+\.[0-9]\+/$new_version/g"
            fi
        done
    fi
}

function show_changes() {
    cd "$ROOT_DIR"
    log_info "Changes to be committed:"
    git diff --stat
    echo ""
    log_info "Detailed changes:"
    git diff
}

function commit_changes() {
    local new_version="$1"
    cd "$ROOT_DIR"

    git add .
    git commit -m "chore: bump version to $new_version

- Update version references across all files
- Update Helm chart version and appVersion
- Update container image tags
- Update documentation examples
- Update release manifests for new version"

    log_info "Changes committed. Ready to tag and push:"
    echo "  git tag $new_version"
    echo "  git push origin master"
    echo "  git push origin $new_version"
}

function main() {
    if [[ $# -ne 1 ]]; then
        usage
    fi

    local new_version="$1"
    validate_version "$new_version"

    local current_version
    current_version=$(get_current_version)

    if [[ -z "$current_version" ]]; then
        log_error "No existing version tags found"
        exit 1
    fi

    if [[ "$current_version" == "$new_version" ]]; then
        log_error "New version $new_version is the same as current version $current_version"
        exit 1
    fi

    log_info "Current version: $current_version"
    log_info "New version: $new_version"

    # Check if working directory is clean
    if [[ -n "$(git status --porcelain)" ]]; then
        log_error "Working directory is not clean. Please commit or stash changes first."
        exit 1
    fi

    # Update versions
    update_versions "$new_version"

    # Show changes
    show_changes

    # Confirm
    echo ""
    read -p "Do you want to commit these changes? (y/N): " -n 1 -r
    echo ""

    if [[ $REPLY =~ ^[Yy]$ ]]; then
        commit_changes "$new_version"
        log_info "Release preparation complete!"
        log_info "Next steps:"
        echo "  1. Review the changes"
        echo "  2. git tag $new_version"
        echo "  3. git push origin master"
        echo "  4. git push origin $new_version"
        echo "  5. GitHub Actions will build and publish the release"
    else
        log_info "Changes not committed. You can review with 'git diff' and commit manually."
    fi
}

main "$@"
