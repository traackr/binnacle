#!/usr/bin/env bash
#
# End-to-end check of binnacle's kustomize support: the real binnacle, helm,
# helm-diff and kustomize against a throwaway minikube cluster, on Helm 3 and on
# the Helm 4 pinned in .mise.toml. Run it with `mise run e2e`. minikube picks its
# own driver, usually Docker; set E2E_MINIKUBE_DRIVER to choose one.
#
# Everything stays away from your own setup. minikube and kubectl get a private
# KUBECONFIG, so your kubeconfig and current context are never read or changed,
# and helm gets a private plugin directory. The cluster is deleted on exit unless
# E2E_KEEP=1 is set.
#
# Each stage asserts on what the cluster holds, not on what helm printed. Exit
# status is the number of failed checks.

set -uo pipefail

HELM3_VERSION="${E2E_HELM3_VERSION:-3.17.3}"
HELM3_DIFF_VERSION="${E2E_HELM3_DIFF_VERSION:-3.12.5}"
HELM4_VERSION="${E2E_HELM4_VERSION:-$(mise current helm)}"
HELM4_DIFF_VERSION="${E2E_HELM4_DIFF_VERSION:-3.15.15}"
KUBERNETES_VERSION="v1.32.13"

PROFILE="binnacle-e2e"
NAMESPACE="e2e"
RELEASE="e2e-app"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
PROJECT="$WORK/project"
ORIGINAL_PATH="$PATH"
FAILURES=0

export KUBECONFIG="$WORK/kubeconfig"
export MINIKUBE_HOME="${MINIKUBE_HOME:-${XDG_CACHE_HOME:-$HOME/.cache}/binnacle-e2e/minikube}"

cleanup() {
    if [[ "${E2E_KEEP:-}" == "1" ]]; then
        echo "Keeping the cluster. KUBECONFIG=$KUBECONFIG MINIKUBE_HOME=$MINIKUBE_HOME"
        return
    fi
    minikube delete --profile "$PROFILE" >/dev/null 2>&1
    rm -rf "$WORK"
}
trap cleanup EXIT

section() { printf '\n=== %s\n' "$*"; }

pass() { printf '  ok    %s\n' "$*"; }

fail() {
    printf '  FAIL  %s\n' "$*"
    FAILURES=$((FAILURES + 1))
}

strip_ansi() { sed $'s/\x1b\\[[0-9;]*m//g'; }

# assert_eq DESCRIPTION WANT GOT
assert_eq() {
    if [[ "$2" == "$3" ]]; then pass "$1"; else fail "$1: want '$2', got '$3'"; fi
}

# assert_contains DESCRIPTION NEEDLE HAYSTACK
assert_contains() {
    if grep -qF -- "$2" <<<"$3"; then pass "$1"; else fail "$1: '$2' not in output:"$'\n'"$3"; fi
}

# assert_lacks DESCRIPTION NEEDLE HAYSTACK
assert_lacks() {
    if grep -qF -- "$2" <<<"$3"; then fail "$1: '$2' unexpectedly in output:"$'\n'"$3"; else pass "$1"; fi
}

# binnacle_run COMMAND CONFIG [HELM ARGS...]
#
# The context is always explicit, on top of the private KUBECONFIG. Combined
# stdout and stderr go to stdout so callers can assert on either.
binnacle_run() {
    local command=$1 config=$2
    shift 2
    (cd "$PROJECT" && "$WORK/binnacle" "$command" -c "$config" -- --kube-context "$PROFILE" "$@") 2>&1 | strip_ansi
    return "${PIPESTATUS[0]}"
}

kube() { kubectl --context "$PROFILE" --namespace "$NAMESPACE" "$@"; }

live() { kube get "$1" -o "jsonpath=$2" 2>&1; }

helm_query() { helm --kube-context "$PROFILE" --namespace "$NAMESPACE" "$@" 2>&1; }

# use_helm VERSION HELM_DIFF_VERSION selects helm for everything that follows and
# gives it a private plugin directory holding that helm-diff. The tarball is
# unpacked by hand because `helm plugin install <url>` ignores HELM_PLUGINS for
# tarballs and writes to the user's real plugin directory.
use_helm() {
    local version=$1 diff_version=$2 os arch helm_path
    helm_path="$(mise x "helm@$version" -- which helm)"
    PATH="$(dirname "$helm_path"):$ORIGINAL_PATH"

    case "$(uname -s)" in
        Darwin) os=macos ;;
        *) os=linux ;;
    esac
    case "$(uname -m)" in
        arm64 | aarch64) arch=arm64 ;;
        *) arch=amd64 ;;
    esac

    HELM_PLUGINS="$WORK/plugins-$version"
    export HELM_PLUGINS
    if [[ ! -d "$HELM_PLUGINS/diff" ]]; then
        mkdir -p "$HELM_PLUGINS/diff"
        curl -fsSL "https://github.com/databus23/helm-diff/releases/download/v$diff_version/helm-diff-$os-$arch.tgz" |
            tar -xz -C "$HELM_PLUGINS/diff" --strip-components=1
    fi
}

reset_project() {
    rm -rf "$PROJECT"
    cp -R "$ROOT/testdata/e2e" "$PROJECT"
}

reset_namespace() {
    kubectl --context "$PROFILE" delete namespace "$NAMESPACE" --ignore-not-found --wait=true >/dev/null
    kubectl --context "$PROFILE" create namespace "$NAMESPACE" >/dev/null
}

assert_patches_are_live() {
    assert_eq "file patch reached the Deployment" "yes" \
        "$(live deployment/e2e-app '{.metadata.annotations.patched-from-file}')"
    assert_eq "pod template patch reached the Deployment" "from-kustomize" \
        "$(live deployment/e2e-app '{.spec.template.spec.containers[0].env[0].value}')"
    assert_eq "inline JSON patch overrode the chart's replicas" "2" \
        "$(live deployment/e2e-app '{.spec.replicas}')"
    assert_eq "label-selected patch reached the Service" "yes" \
        "$(live service/e2e-app '{.metadata.labels.patched-by-selector}')"
    assert_eq "kustomize.resources created the extra ConfigMap" "kustomize" \
        "$(live configmap/e2e-extra-from-kustomize '{.data.added-by}')"
}

run_suite() {
    local version=$1 diff_version=$2 major=${1%%.*} out status
    section "Helm $version with helm-diff $diff_version"
    use_helm "$version" "$diff_version"
    assert_eq "selected the intended helm" "v$version" "$(helm version --template '{{.Version}}')"
    reset_project
    reset_namespace

    out="$(binnacle_run template binnacle.yml)"
    status=$?
    assert_eq "template exits cleanly" "0" "$status"
    assert_contains "template shows the file patch" 'patched-from-file: "yes"' "$out"
    assert_contains "template shows the extra resource" "e2e-extra-from-kustomize" "$out"

    out="$(binnacle_run diff binnacle.yml)"
    status=$?
    assert_eq "diff before install exits cleanly" "0" "$status"
    assert_contains "diff before install shows the file patch" 'patched-from-file: "yes"' "$out"
    assert_contains "diff before install shows the inline patch" "replicas: 2" "$out"

    out="$(binnacle_run sync binnacle.yml --wait --timeout 3m)"
    status=$?
    assert_eq "sync installs the release" "0" "$status"
    [[ $status -eq 0 ]] || echo "$out"
    assert_patches_are_live
    assert_eq "both replicas became ready" "2" "$(live deployment/e2e-app '{.status.readyReplicas}')"
    assert_eq "the chart's hook Job ran to completion" "1" "$(live job/e2e-app-migrate '{.status.succeeded}')"

    # The behaviour the README documents as differing between Helm majors.
    if [[ "$major" -ge 4 ]]; then
        assert_eq "Helm 4: hooks go through kustomize" "yes" \
            "$(live job/e2e-app-migrate '{.metadata.annotations.patched-by-kustomize}')"
        assert_contains "Helm 4: annotation added by kustomize makes a real hook" \
            "e2e-hook-from-kustomize" "$(helm_query get hooks "$RELEASE")"
        assert_lacks "Helm 4: that hook is not part of the manifest" \
            "e2e-hook-from-kustomize" "$(helm_query get manifest "$RELEASE")"
    else
        assert_eq "Helm 3: hooks skip kustomize" "" \
            "$(live job/e2e-app-migrate '{.metadata.annotations.patched-by-kustomize}')"
        assert_lacks "Helm 3: annotation added by kustomize makes no hook" \
            "e2e-hook-from-kustomize" "$(helm_query get hooks "$RELEASE")"
        assert_contains "Helm 3: that resource stays in the manifest" \
            "e2e-hook-from-kustomize" "$(helm_query get manifest "$RELEASE")"
    fi

    out="$(binnacle_run status binnacle.yml)"
    status=$?
    assert_eq "status exits cleanly" "0" "$status"
    assert_contains "status reports the release deployed" "STATUS: deployed" "$out"

    out="$(binnacle_run diff binnacle.yml)"
    status=$?
    assert_eq "diff after sync exits cleanly" "0" "$status"
    # If the post-renderer dropped out here, the patched fields would show as removed.
    assert_lacks "diff after sync finds nothing to change" "patched-from-file" "$out"

    sed -i.bak 's/revision: "1"/revision: "2"/' "$PROJECT/patches/deployment-annotations.yml"
    out="$(binnacle_run diff binnacle.yml)"
    status=$?
    assert_eq "diff of an edited patch exits cleanly" "0" "$status"
    assert_contains "diff of an edited patch shows the change" 'revision: "2"' "$out"
    out="$(binnacle_run sync binnacle.yml --wait --timeout 3m)"
    assert_eq "sync applies the edited patch" "0" "$?"
    assert_eq "the edited patch is live" "2" "$(live deployment/e2e-app '{.metadata.annotations.revision}')"

    local revision
    revision="$(helm_query history "$RELEASE" --output json | grep -o '"revision":[0-9]*' | tail -1)"
    if [[ -n "$revision" ]]; then pass "helm history reports a revision"; else fail "helm history reports no revision"; fi
    echo 'this: is: not: yaml' >"$PROJECT/patches/deployment-annotations.yml"
    out="$(binnacle_run sync binnacle.yml --wait --timeout 3m)"
    status=$?
    if [[ $status -ne 0 ]]; then pass "sync refuses a broken patch"; else fail "sync accepted a broken patch"; fi
    assert_contains "kustomize's own error reaches the user" "kustomize: " "$out"
    assert_eq "a broken patch adds no release revision" "$revision" \
        "$(helm_query history "$RELEASE" --output json | grep -o '"revision":[0-9]*' | tail -1)"
    assert_eq "a broken patch leaves the live object alone" "2" \
        "$(live deployment/e2e-app '{.metadata.annotations.revision}')"

    reset_project
    out="$(binnacle_run sync binnacle-absent.yml)"
    assert_eq "sync with state: absent exits cleanly" "0" "$?"
    assert_eq "the release is gone" "" "$(helm_query list --short)"
    assert_contains "the Deployment is gone" "NotFound" "$(live deployment/e2e-app '{.metadata.name}')"
    assert_contains "the extra resource is gone" "NotFound" \
        "$(live configmap/e2e-extra-from-kustomize '{.metadata.name}')"
    # Both Helm majors skip anything carrying a hook annotation on uninstall, even
    # Helm 3, which otherwise treats this resource as an ordinary part of the release.
    assert_eq "uninstall leaves a resource with a hook annotation" "e2e-hook-from-kustomize" \
        "$(live configmap/e2e-hook-from-kustomize '{.metadata.name}')"
}

# A release installed on Helm 3 has to survive its first Helm 4 sync, since one
# binnacle release is meant to carry files across that move one at a time.
run_upgrade_across_majors() {
    local out status
    section "Moving a release from Helm $HELM3_VERSION to Helm $HELM4_VERSION"
    reset_project
    reset_namespace

    use_helm "$HELM3_VERSION" "$HELM3_DIFF_VERSION"
    out="$(binnacle_run sync binnacle.yml --wait --timeout 3m)"
    assert_eq "Helm 3 installs the release" "0" "$?"

    use_helm "$HELM4_VERSION" "$HELM4_DIFF_VERSION"
    out="$(binnacle_run diff binnacle.yml)"
    status=$?
    assert_eq "Helm 4 can diff a release Helm 3 installed" "0" "$status"
    out="$(binnacle_run sync binnacle.yml --wait --timeout 3m)"
    status=$?
    assert_eq "Helm 4 can upgrade a release Helm 3 installed" "0" "$status"
    [[ $status -eq 0 ]] || echo "$out"
    assert_patches_are_live
    assert_eq "the release moved to its second revision" '"revision":2' \
        "$(helm_query history "$RELEASE" --output json | grep -o '"revision":[0-9]*' | tail -1)"

    # The trap the README warns about. The resource was in Helm 3's manifest and
    # is a hook on Helm 4, so the upgrade runs the hook and then removes the
    # resource as no longer part of the release, while still exiting 0. If this
    # starts failing, Helm has changed and the README needs to change with it.
    assert_contains "first Helm 4 sync deletes a resource that became a pre-upgrade hook" "NotFound" \
        "$(live configmap/e2e-pre-hook-from-kustomize '{.metadata.name}')"
    assert_eq "first Helm 4 sync keeps a resource that became a post-upgrade hook" "e2e-hook-from-kustomize" \
        "$(live configmap/e2e-hook-from-kustomize '{.metadata.name}')"
    out="$(binnacle_run sync binnacle.yml --wait --timeout 3m)"
    assert_eq "a second Helm 4 sync succeeds" "0" "$?"
    assert_eq "the second Helm 4 sync brings that resource back" "e2e-pre-hook-from-kustomize" \
        "$(live configmap/e2e-pre-hook-from-kustomize '{.metadata.name}')"

    out="$(binnacle_run sync binnacle-absent.yml)"
    assert_eq "Helm 4 can remove the release" "0" "$?"
    assert_eq "the release is gone" "" "$(helm_query list --short)"
}

section "Building binnacle"
(cd "$ROOT" && go build -o "$WORK/binnacle" .) || exit 1
pass "built"

section "Starting minikube profile $PROFILE (Kubernetes $KUBERNETES_VERSION)"
if ! minikube start --profile "$PROFILE" ${E2E_MINIKUBE_DRIVER:+--driver="$E2E_MINIKUBE_DRIVER"} \
    --kubernetes-version="$KUBERNETES_VERSION" --cpus=2 --memory=2g --wait=all --keep-context \
    >"$WORK/minikube.log" 2>&1; then
    cat "$WORK/minikube.log"
    exit 1
fi
SERVER="$(kubectl --context "$PROFILE" config view --minify -o 'jsonpath={.clusters[0].cluster.server}')"
case "$SERVER" in
    https://127.0.0.1:* | https://192.168.*) ;;
    *)
        echo "Context $PROFILE points at $SERVER, not a local cluster; refusing to continue." >&2
        exit 1
        ;;
esac
pass "cluster is up at $SERVER"

run_suite "$HELM3_VERSION" "$HELM3_DIFF_VERSION"
run_suite "$HELM4_VERSION" "$HELM4_DIFF_VERSION"
run_upgrade_across_majors

section "Result"
if [[ $FAILURES -eq 0 ]]; then
    echo "All checks passed."
else
    echo "$FAILURES check(s) failed."
fi
exit "$FAILURES"
