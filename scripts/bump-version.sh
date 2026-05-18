#!/bin/bash
#
# Simple version bump helper for kubeftpd
# Automatically calculates next version based on semver increment type
#
# Usage: ./scripts/bump-version.sh [patch|minor|major] [--dry-run] [--force]
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Colors for output
GREEN='\033[0;32m'
NC='\033[0m' # No Color

function log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

function usage() {
    echo "Usage: $0 [patch|minor|major]"
    echo ""
    echo "Arguments:"
    echo "  patch    Bump patch version (e.g., v0.5.0 -> v0.5.1)"
    echo "  minor    Bump minor version (e.g., v0.5.0 -> v0.6.0)"
    echo "  major    Bump major version (e.g., v0.5.0 -> v1.0.0)"
    echo ""
    echo "Examples:"
    echo "  $0 patch                # Bump patch version"
    echo "  $0 minor                # Bump minor version"
    echo "  $0 major                # Bump major version"
    echo ""
    exit 1
}

function get_current_version() {
    cd "$ROOT_DIR"
    # Try to get version from Makefile first (most reliable), then git tags
    if [[ -f "Makefile" ]]; then
        grep "^VERSION ?= " Makefile | cut -d'=' -f2 | tr -d ' '
    else
        git tag --sort=-version:refname | head -1 | tr -d '\n'
    fi
}

function bump_version() {
    local current_version="$1"
    local bump_type="$2"

    # Remove 'v' prefix for calculation
    local version_no_v="${current_version#v}"

    # Split version into parts using parameter expansion
    local major="${version_no_v%%.*}"
    local remaining="${version_no_v#*.}"
    local minor="${remaining%%.*}"
    local patch="${remaining#*.}"

    case "$bump_type" in
        patch)
            patch=$((patch + 1))
            ;;
        minor)
            minor=$((minor + 1))
            patch=0
            ;;
        major)
            major=$((major + 1))
            minor=0
            patch=0
            ;;
        *)
            echo "Invalid bump type: $bump_type"
            usage
            ;;
    esac

    echo "v$major.$minor.$patch"
}

function main() {
    local bump_type=""

    if [[ $# -ne 1 ]]; then
        usage
    fi

    case $1 in
        patch|minor|major)
            bump_type="$1"
            ;;
        --help|-h)
            usage
            ;;
        *)
            echo "Unknown argument: $1"
            usage
            ;;
    esac

    # Get current version
    local current_version
    current_version=$(get_current_version)

    if [[ -z "$current_version" ]]; then
        echo "Could not determine current version"
        exit 1
    fi

    # Calculate new version
    local new_version
    new_version=$(bump_version "$current_version" "$bump_type")

    log_info "Bumping $bump_type version: $current_version -> $new_version"

    # Call the main release script
    exec "$SCRIPT_DIR/release.sh" "$new_version"
}

main "$@"
