# binnacle [![Release][release-image]][release-url] [![Test](https://github.com/Traackr/binnacle/actions/workflows/test.yml/badge.svg)](https://github.com/Traackr/binnacle/actions/workflows/test.yml)

`binnacle` is an opinionated automation tool used to interact with Kubernetes' [Helm][helm].  By offering a single file to manage one or many charts, you can easily control all aspects of your Helm managed applications.

`binnacle` is similar in nature to [Helmfile][helmfile] with a slightly different approach to managing Helm Charts.

## Installation

A binary for various operating systems is available through [Github Releases][github-releases].  Download the appropriate archive, and extract into a directory within your PATH.

## Shell completions

```bash
# zsh, bash or fish — writes the script and prints the one rc line to add
mise run completions zsh
```

Or generate one directly:

```bash
binnacle completion zsh > ~/.zsh/completions/_binnacle
binnacle completion bash > ~/.bash_completion.d/binnacle
binnacle completion fish > ~/.config/fish/completions/binnacle.fish
```

## Usage

For the full list of options:

```shell
binnacle --help
```

To see the version of `binnacle` you can use the following:

```shell
binnacle --version
```

## Getting Started

### Configuration File Format

Configuration files can be written in YAML, TOML or JSON.

```yaml
---
# charts takes a list of chart configurations
charts:
    # This is the name of the chart
  - name: concourse
    # This is the namespace into which the chart is launched
    namespace: apps
    # This is the name for the release of this chart
    release: apps-concourse
    # This is the name of the repository within which the helm chart is located
    repo: stable
    # This determines if the release is created or removed. Default: present Options: absent, present
    state: present
    # Any data under values are passed to Helm to configure the given chart
    values:
      image: concourse/concourse
      imageTag: "3.10.0"
    # This is the version of the Helm chart.  If this is omitted, the latest is used.
    version: 1.3.1

# repositories takes a list of repository configurations
repositories:
    # This is the name of the repository
  - name: stable
    # This is the URL of the repository
    url: https://kubernetes-charts.storage.googleapis.com
    # This determines if the repository is created or removed. Default: present Options: absent, present
    state: present
```

### Kustomize

A chart can carry a `kustomize` block. `template`, `sync` and `diff` then run `kustomize build` as Helm's post-renderer, so `kustomize` has to be on `PATH`.

```yaml
charts:
  - name: concourse
    namespace: apps
    release: apps-concourse
    repo: stable
    state: present
    kustomize:
      # Extra manifests to add to the release
      resources:
        - extra-configmap.yml
      patches:
        # A patch kept in a file
        - path: patches/add-annotation.yml
          target:
            kind: Deployment
        # A patch written inline
        - patch: |-
            - op: replace
              path: /spec/replicas
              value: 2
          target:
            kind: Deployment
            name: apps-concourse
```

Paths are relative to the binnacle file. The files are copied into one temporary directory by basename, so two with the same basename collide.

#### Helm versions

Binnacle works with Helm 3 and Helm 4, and asks `helm version` which it is talking to. Helm 3 is given the path of a script. Helm 4 only accepts the name of a plugin for `--post-renderer`, so binnacle generates one in its temporary directory and points `HELM_PLUGINS` there for that single call. That directory also links your installed plugins, so `helm diff` and the rest keep working, and nothing is added to your real plugin directory.

Moving to Helm 4 changes what a post-renderer sees. `mise run e2e` checks how hooks are routed and what the annotation does against a real cluster:

- **Hooks go through kustomize on Helm 4.** Helm 4 passes hooks to the post-renderer along with the other templates, and its command line has no option to turn that off. A patch can now match a hook resource, and these charts render on Helm 3 but fail on Helm 4 once they have a `kustomize` block:
  - A chart that declares the same resource as both a hook and a regular template, or as two hooks, fails with `may not add resource with an already registered id`.
  - A hook that uses `metadata.generateName` fails with `missing metadata.name in object`.
- **A hook annotation added by kustomize becomes a real hook on Helm 4.** A resource that carries `helm.sh/hook` after kustomize has run is treated as a hook on Helm 4. On Helm 3 it stays in the release manifest and is applied like any other resource. So on the first Helm 4 sync of a release installed on Helm 3, Helm deletes the resource as no longer part of the release, and the sync still exits 0:
  - With a `post-upgrade` hook, the resource is missing until the hook runs. That comes after any `--wait`, so the gap lasts about as long as the rollout.
  - With a `pre-upgrade` hook, the hook has already run by then, so the resource stays missing until the next sync brings it back.

  Remove the annotation before moving a release to Helm 4.

`state: absent` leaves a resource with a hook annotation behind on both Helm 3 and Helm 4.

Two more differences that `mise run e2e` does not cover:

- **`template --include-crds` skips kustomize for CRDs on Helm 4.** Helm 3 sends the chart's `crds/` directory through the post-renderer here, and Helm 4 adds those CRDs afterwards. A patch that targets one stops applying without any error, so check this before piping the output to `kubectl`.
- **`HELM_NAMESPACE` and `HELM_KUBECONTEXT` are not set for the post-renderer** under `template` and `sync` on either version, unless they are already in your environment. helm-diff does set them under `diff`. Binnacle's generated kustomization does not use them.

`binnacle diff` was checked with helm-diff 3.12.5 on Helm 3 and helm-diff 3.15.15 on Helm 4. Helm 3.17.3 refuses to load helm-diff 3.15.15.

### Using Binnacle

The standard workflow when using binnacle is to use the `template` command to verify the desired configuration files are generated, use the `sync` command to create/update the existing release configuration within Helm, and `status` to get the status of a release.

Using the configuration available at `test-data/demo.yml` you can run the template command:

```bash
$ binnacle template -c ./test-data/demo.yml 
Loading config file: ./test-data/demo.yml
---
# Source: concourse/templates/namespace.yaml

---
apiVersion: v1
kind: Namespace
metadata:
  annotations:
    "helm.sh/resource-policy": keep
  name: apps-concourse-main
  labels:
...
```

By reviewing the output you are able to verify that you have specificied all of the necessary configuration aspects of the chart.  Once you are happy with how the chart is configured you can `sync` the charts to Helm:

```bash

$  binnacle sync -c ./test-data/demo.yml 
Loading config file: ./test-data/demo.yml
"stable" has been added to your repositories
Release "apps-concourse" does not exist. Installing it now.
NAME:   apps-concourse
LAST DEPLOYED: Fri Apr 27 14:24:19 2018
NAMESPACE: apps
STATUS: DEPLOYED

RESOURCES:
==> v1beta1/StatefulSet
NAME                   DESIRED  CURRENT  AGE
apps-concourse-worker  2        0        1s

==> v1/Pod(related)
NAME                                        READY  STATUS             RESTARTS  AGE
apps-concourse-postgresql-5f964dd587-6ng8f  0/1    Pending            0         1s
apps-concourse-web-5dd649b7f6-7v78w         0/1    ContainerCreating  0         1s

==> v1/Namespace
NAME                 STATUS  AGE
apps-concourse-main  Active  1s

==> v1/Secret
NAME                       TYPE    DATA  AGE
apps-concourse-postgresql  Opaque  1     1s
apps-concourse-concourse   Opaque  7     1s

==> v1/PersistentVolumeClaim
NAME                       STATUS   VOLUME  CAPACITY  ACCESS MODES  STORAGECLASS  AGE
apps-concourse-postgresql  Pending  1s

==> v1beta1/ClusterRole
NAME                AGE
apps-concourse-web  1s

==> v1/Service
NAME                       TYPE       CLUSTER-IP     EXTERNAL-IP  PORT(S)            AGE
apps-concourse-postgresql  ClusterIP  10.233.89.90   <none>       5432/TCP           1s
apps-concourse-web         ClusterIP  10.233.57.192  <none>       8080/TCP,2222/TCP  1s
apps-concourse-worker      ClusterIP  None           <none>       <none>             1s

==> v1/ServiceAccount
NAME                   SECRETS  AGE
apps-concourse-web     1        1s
apps-concourse-worker  1        1s

==> v1beta1/Role
NAME                   AGE
apps-concourse-worker  1s

==> v1beta1/RoleBinding
NAME                     AGE
apps-concourse-web-main  1s
apps-concourse-worker    1s

==> v1beta1/Deployment
NAME                       DESIRED  CURRENT  UP-TO-DATE  AVAILABLE  AGE
apps-concourse-postgresql  1        1        1           0          1s
apps-concourse-web         1        1        1           0          1s

==> v1beta1/PodDisruptionBudget
NAME                   MIN AVAILABLE  MAX UNAVAILABLE  ALLOWED DISRUPTIONS  AGE
apps-concourse-worker  1              N/A              0                    1s


NOTES:

* Concourse can be accessed:

  * Within your cluster, at the following DNS name at port 8080:

    apps-concourse-web.apps.svc.cluster.local

  * From outside the cluster, run these commands in the same shell:

    export POD_NAME=$(kubectl get pods --namespace apps -l "app=apps-concourse-web" -o jsonpath="{.items[0].metadata.name}")
    echo "Visit http://127.0.0.1:8080 to use Concourse"
    kubectl port-forward --namespace apps $POD_NAME 8080:8080

* Login with the following credentials
  
  Username: concourse
  Password: concourse
  
* If this is your first time using Concourse, follow the tutorial at https://concourse-ci.org/hello-world.html

*******************
******WARNING******
*******************

You are using the "naive" baggage claim driver, which is also the default value for this chart. This is the default for compatibility reasons, but is very space inefficient, and should be changed to either "btrfs" (recommended) or "overlay" depending on that filesystem's support in the Linux kernel your cluster is using. Please see https://github.com/concourse/concourse/issues/1230 and https://github.com/concourse/concourse/issues/1966 for background.
```

From the output you can see that the release did not exist "apps-concourse" so Helm created it.  To get updates on the status of the deployment you can use the `status` command:

```bash

$ binnacle status -c ./test-data/demo.yml 
Loading config file: ./test-data/demo.yml
LAST DEPLOYED: Fri Apr 27 14:24:19 2018
NAMESPACE: apps
STATUS: DEPLOYED

RESOURCES:
==> v1/Pod(related)
NAME                                        READY  STATUS   RESTARTS  AGE
apps-concourse-postgresql-5f964dd587-6ng8f  0/1    Pending  0         1m
apps-concourse-web-5dd649b7f6-7v78w         0/1    Running  0         1m

==> v1/Namespace
NAME                 STATUS  AGE
apps-concourse-main  Active  1m

==> v1/Secret
NAME                       TYPE    DATA  AGE
apps-concourse-postgresql  Opaque  1     1m
apps-concourse-concourse   Opaque  7     1m

==> v1beta1/Role
NAME                   AGE
apps-concourse-worker  1m

==> v1/Service
NAME                       TYPE       CLUSTER-IP     EXTERNAL-IP  PORT(S)            AGE
apps-concourse-postgresql  ClusterIP  10.233.89.90   <none>       5432/TCP           1m
apps-concourse-web         ClusterIP  10.233.57.192  <none>       8080/TCP,2222/TCP  1m
apps-concourse-worker      ClusterIP  None           <none>       <none>             1m

==> v1beta1/PodDisruptionBudget
NAME                   MIN AVAILABLE  MAX UNAVAILABLE  ALLOWED DISRUPTIONS  AGE
apps-concourse-worker  1              N/A              0                    1m

==> v1beta1/StatefulSet
NAME                   DESIRED  CURRENT  AGE
apps-concourse-worker  2        2        1m

==> v1/PersistentVolumeClaim
NAME                       STATUS   VOLUME  CAPACITY  ACCESS MODES  STORAGECLASS  AGE
apps-concourse-postgresql  Pending  1m

==> v1/ServiceAccount
NAME                   SECRETS  AGE
apps-concourse-web     1        1m
apps-concourse-worker  1        1m

==> v1beta1/ClusterRole
NAME                AGE
apps-concourse-web  1m

==> v1beta1/RoleBinding
NAME                     AGE
apps-concourse-web-main  1m
apps-concourse-worker    1m

==> v1beta1/Deployment
NAME                       DESIRED  CURRENT  UP-TO-DATE  AVAILABLE  AGE
apps-concourse-postgresql  1        1        1           0          1m
apps-concourse-web         1        1        1           0          1m


NOTES:

* Concourse can be accessed:

  * Within your cluster, at the following DNS name at port 8080:

    apps-concourse-web.apps.svc.cluster.local

  * From outside the cluster, run these commands in the same shell:

    export POD_NAME=$(kubectl get pods --namespace apps -l "app=apps-concourse-web" -o jsonpath="{.items[0].metadata.name}")
    echo "Visit http://127.0.0.1:8080 to use Concourse"
    kubectl port-forward --namespace apps $POD_NAME 8080:8080

* Login with the following credentials
  
  Username: concourse
  Password: concourse
  
* If this is your first time using Concourse, follow the tutorial at https://concourse-ci.org/hello-world.html

*******************
******WARNING******
*******************

You are using the "naive" baggage claim driver, which is also the default value for this chart. This is the default for compatibility reasons, but is very space inefficient, and should be changed to either "btrfs" (recommended) or "overlay" depending on that filesystem's support in the Linux kernel your cluster is using. Please see https://github.com/concourse/concourse/issues/1230 and https://github.com/concourse/concourse/issues/1966 for background.
```

If you want to remove a release, you can change the `state` from `present` to `absent` and run the `sync` command:

```bash
$ binnacle sync -c ./test-data/demo.yml 
Loading config file: ./test-data/demo.yml
"stable" has been added to your repositories
These resources were kept due to the resource policy:
[Namespace] apps-concourse-main

release "apps-concourse" deleted
```

## Development

Prerequisites:

- go 1.17

### Local Go Environment

First, check out the repository:

```script
git clone https://github.com/Traackr/binnacle.git
cd binnacle
```

To compile a version of binnacle for your local machine you can run:

```script
make build
```

This will generate a binary within the ./bin directory of the project.

`make build` runs `scripts/build.sh`, which requires `jq` on `PATH` to read
the release version from `.release-please-manifest.json`. Prefer
`mise run build`, which installs `jq` (along with go/helm/kustomize) from
`.mise.toml` first.

To run the unit tests:

```script
make test
```

The tests that need `helm`, `kustomize` or the helm-diff plugin skip when they are missing, so install them to get the full run.

To run binnacle against a real cluster on both Helm 3 and Helm 4:

```script
mise run e2e
```

This creates a throwaway minikube cluster, with its own kubeconfig so your current context is left alone, and deletes it afterwards. minikube picks its own driver, usually Docker. Set `E2E_MINIKUBE_DRIVER` to choose another one.

[github-releases]: https://github.com/Traackr/binnacle/releases
[helm]: https://helm.sh/
[helmfile]: https://github.com/roboll/helmfile
[release-url]: https://github.com/Traackr/binnacle/releases/latest
[release-image]: https://img.shields.io/github/release/Traackr/binnacle.svg
