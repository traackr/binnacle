#!/usr/bin/env bash
set -e

# Get the base repository path
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )/.." && pwd )"
# Get the name of the package we are building
PACKAGE="$(basename "$DIR")"

: ${LOCAL_TARGET="$(go env GOOS)_$(go env GOARCH)"}

# Move into our base repository path
cd "$DIR"

# The version comes from .release-please-manifest.json, which release-please
# rewrites when a release PR merges. It is the single source of truth; there is
# deliberately no VERSION file that could drift from it.
MANIFEST="$DIR/.release-please-manifest.json"
if [[ ! -f "$MANIFEST" ]]; then
  echo "==> ERROR: $MANIFEST not found" >&2
  exit 1
fi
VERSION="$(jq -r '.["."] // empty' "$MANIFEST")"
if [[ -z "$VERSION" ]]; then
  echo "==> ERROR: no version recorded for path '.' in $MANIFEST" >&2
  exit 1
fi

# Clean up old binaries and packages
echo "==> Cleaning up build environment..."
rm -rf pkg/*
rm -rf bin/*
mkdir -p bin
mkdir -p pkg

shasum256() {
  if hash sha256sum 2>/dev/null; then
    sha256sum "$@"
  else
    shasum -a 256 "$@" 
  fi
}

#
# Compile Configuration
#

GIT_COMMIT="$(git rev-parse --short HEAD)"
GIT_DIRTY="$(test -n "`git status --porcelain`" && echo "+CHANGES" || true)"
EXTLDFLAGS="-X github.com/Traackr/binnacle/cmd.GITCOMMIT=${GIT_COMMIT}${GIT_DIRTY} -X github.com/Traackr/binnacle/cmd.VERSION=$VERSION"
STATIC="-extldflags '-static'"

#
# Determine build targets
#

# Default to local os/arch
targets="$LOCAL_TARGET"

# If we are building for release change targets based off of environment
if [[ "$TARGETS" == "release" ]]; then
  targets="darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64"
elif [[ "$TARGETS" != "" ]]; then
  targets="$TARGETS"
fi

for target in $targets; do
  case $target in
    "darwin_amd64")
      echo "==> Building darwin amd64..."
      CGO_ENABLED=$DARWIN_CGO_ENABLED GOARCH="amd64" GOOS="darwin" \
        go build -ldflags "$EXTLDFLAGS" -o "pkg/darwin_amd64/$PACKAGE"
      ;;
    "darwin_arm64")
      echo "==> Building darwin arm64..."
      CGO_ENABLED=$DARWIN_CGO_ENABLED GOARCH="arm64" GOOS="darwin" \
        go build -ldflags "$EXTLDFLAGS" -o "pkg/darwin_arm64/$PACKAGE"
      ;;
    "linux_amd64")
      echo "==> Building linux amd64..."
      CGO_ENABLED=0 GOOS="linux" GOARCH="amd64" \
        go build -ldflags "$STATIC $EXTLDFLAGS" -o "pkg/linux_amd64/$PACKAGE"
      ;;
    "linux_arm64")
      echo "==> Building linux arm64..."
      CGO_ENABLED=0 GOOS="linux" GOARCH="arm64" \
        go build -ldflags "$STATIC $EXTLDFLAGS" -o "pkg/linux_arm64/$PACKAGE"
      ;;
    "windows_amd64")
      echo "==> Building windows amd64..."
      CGO_ENABLED=0 GOOS="windows"  GOARCH="amd64" CXX="x86_64-w64-mingw32-g++" CC="x86_64-w64-mingw32-gcc" \
        go build -ldflags "$STATIC $EXTLDFLAGS" -o "pkg/windows_amd64/$PACKAGE.exe"
      ;;
    *)
      echo "--> Invalid target: $target"
      ;;
  esac
done

set -e

# Copy our local OS/Arch to the bin/ directory
for F in $(find ./pkg/${LOCAL_TARGET} -mindepth 1 -maxdepth 1 -type f); do
  echo "==> Copying ${LOCAL_TARGET} to ./bin"
  cp ${F} bin/
  chmod 755 bin/*
done

# Package up the artifacts
if [[ "$GENERATE_PACKAGES" != "" ]]; then
  for PLATFORM in $(find ./pkg -mindepth 1 -maxdepth 1 -type d); do
    OSARCH=$(basename ${PLATFORM})
    echo "==> Packaging ${OSARCH}"
    pushd $PLATFORM >/dev/null 2>&1
    tar czvf ../${PACKAGE}-${OSARCH}.tar.gz ./* >/dev/null
    popd >/dev/null 2>&1
    #rm -rf $PLATFORM >/dev/null
  done

  echo "==> Generating SHA256..."
  for F in $(find ./pkg -mindepth 1 -maxdepth 1 -type f); do
    FILENAME=$(basename ${F})
    shasum256 "./pkg/${FILENAME}" >> ./pkg/SHA256SUM.txt
  done
  echo
  cat ./pkg/SHA256SUM.txt
fi
