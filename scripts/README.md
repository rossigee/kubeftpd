# Release Scripts

This directory contains scripts to help with version management and releases for kubeftpd.

## Scripts

### `release.sh` - Main release script

Updates version references across all files in the project and prepares for release.

**Usage:**
```bash
./scripts/release.sh [OPTIONS] <new-version>

# Examples
./scripts/release.sh v0.5.1
./scripts/release.sh --dry-run v0.6.0
./scripts/release.sh --force v1.0.0
```

**Options:**
- `--dry-run`: Show what would be changed without making changes
- `--force`: Skip version consistency checks
- `--help`: Show help message

**Features:**
- ✅ Validates version format (vMAJOR.MINOR.PATCH)
- ✅ Checks version consistency across all files
- ✅ Updates versions in:
  - `Makefile`
  - `Dockerfile`
  - `cmd/main.go`
  - `README.md`
  - `chart/kubeftpd/Chart.yaml` (if exists)
  - `chart/kubeftpd/values.yaml` (if exists)
  - `config/production/kustomization.yaml` (if exists)
  - Release manifest files in `releases/` directory
- ✅ Shows diff of changes before committing
- ✅ Creates properly formatted commit message
- ✅ Provides next steps for tagging and pushing

### `bump-version.sh` - Semantic version bumper

Automatically calculates the next version based on semantic versioning rules.

**Usage:**
```bash
./scripts/bump-version.sh [patch|minor|major] [OPTIONS]

# Examples  
./scripts/bump-version.sh patch                # v0.5.0 -> v0.5.1
./scripts/bump-version.sh minor --dry-run      # v0.5.0 -> v0.6.0 (preview)
./scripts/bump-version.sh major --force        # v0.5.0 -> v1.0.0 (skip checks)
```

**Bump Types:**
- `patch`: Bug fixes, small updates (v0.5.0 → v0.5.1)
- `minor`: New features, backwards compatible (v0.5.0 → v0.6.0)
- `major`: Breaking changes (v0.5.0 → v1.0.0)

**Options:**
- `--dry-run`: Preview changes without applying them
- `--force`: Skip version consistency checks
- `--help`: Show help message

## Typical Release Workflow

1. **Check current version consistency:**
   ```bash
   ./scripts/release.sh --dry-run v999.999.999  # Just to see current version
   ```

2. **Preview the next version bump:**
   ```bash
   ./scripts/bump-version.sh patch --dry-run     # For bug fixes
   ./scripts/bump-version.sh minor --dry-run     # For new features  
   ./scripts/bump-version.sh major --dry-run     # For breaking changes
   ```

3. **Apply the version bump:**
   ```bash
   ./scripts/bump-version.sh patch               # Commits changes automatically
   ```

4. **Create and push the release tag:**
   ```bash
   git tag v0.5.1
   git push origin master
   git push origin v0.5.1
   ```

5. **GitHub Actions will automatically:**
   - Build and test the release
   - Create container images
   - Publish to GitHub Container Registry
   - Create GitHub release with artifacts

## Version Consistency

The scripts ensure all version references stay in sync across:

- `Makefile` - `VERSION ?= v0.5.0`
- `Dockerfile` - `ARG VERSION=v0.5.0`  
- `cmd/main.go` - `version = "v0.5.0"`
- `README.md` - Image tags and Helm examples
- Helm charts (if present)
- Kustomization files (if present)

If versions are inconsistent, the scripts will:
1. Show which files have mismatched versions
2. Require manual fixes or use of `--force` flag
3. Provide clear error messages for resolution

## Troubleshooting

**"Version inconsistency detected"**
- Fix manually or use `--force` to override
- Check all files listed in the error message

**"Working directory is not clean"**  
- Commit or stash your changes first
- Use `git status` to see what needs attention

**"No existing version tags found"**
- Make sure you have at least one git tag in vX.Y.Z format
- Or use `--force` to override git tag requirement
