# Change Log

All notable changes to this project will be documented in this file.
This project adheres to [Semantic Versioning](http://semver.org/).

## [0.8.0] - 2022-05-12

- Remove support for Helm 2
- Fix build to properly include VERSION
- Standardize error handling
- Add support for running Kustomize as a post-renderer
- Upgrade cobra to 1.4.0
- Upgrade viper to 1.11.0
- Upgrade to go 1.18.2
- Upgrade logrus to 1.8.1
- Replace use of ghodss/yaml library with go-yaml.v3

## [0.7.1] - 2022-05-05

Changes:

- Pass chart namespace to helm in `binnacle diff` and `binnacle status` commands

## [0.7.0] - 2021-11-22

Changes:

- Upgrade to go 1.17.3
- Use go modules instead of govendor
- Add Darwin ARM64 as a build target to generate an artifact for M1 Macs
- Remove code for building in Docker and generating docs

## [0.6.0] - 2021-09-14

Changes:

- Forward args to the ReleaseExists function and to Helm

## [0.5.1] - 2021-04-19

Changes:

- Use Github Actions for releases

## [0.5.0] - 2021-04-19

Changes:

- Repositories listed under the `repositories` key are now added/updated for the `template` and `diff` commands. Previously, these repositories were only used when running `sync`.
- Due to build issues, this release was never created. 0.5.1 supersedes it


## [0.4.0] - 2020-11-12

Changes:

- Previously when syncing a configuration file containing repositories and charts, there was no way to utilize newly added charts in a single run because a `helm repo update` needed to be executed after the repositories were added.  A call to `helm repo update` has been added whenever a new repository is added.

- Binnacles whose state is set to absent will no longer be rendered via the `template` command.

- Introduced logic that checks to see if a release exists prior to attempting to remove the release.

Fixes:

- Invalid configuration files were not properly reported as errors to the user.  This has been corrected.

## [0.3.1] - 2020-04-01

Fixes:

- When generating a template the chart version was not properly provided for Helm3.

## [0.3.0] - 2020-02-04

Breaking Changes:

- Prior to this release if the `repo` for a chart was not specified it defaulted to `local`.  This default has been changed to an empty string.

Changes:

- Added the ability to reference a chart on the local file system or URL.  To utilize this functionality leave the repo empty for a chart and pass the necessary path/URL as the `name` of the chart.

```yaml
charts:
  - name: https://github.com/pantsel/konga/blob/master/charts/konga/konga-1.0.0.tgz?raw=true
    namespace: kube-system
    release: konga
    state: present
```

This example shows the `repo` has been omitted and the name pointing to a URL used to access the desired version of the chart.

## [0.2.1] - 2020-01-129

- Remove the explicit '--force' from the command passed to helm3 upgrade during a `binnacle sync`.

## [0.2.0] - 2020-01-13

- This release introduces Helm 3 support by adding a lightweight touchpoint to detect if helm2 or helm3 is getting targetted and treating helm2 as the exception case for processing.  This will allow helm2 support to be easily removed upon its EOL.  To facilitate this detected binnacle will run `helm version` during certain commands to help determine the target version and change the underlying helm commands accordingly.

## [0.1.1] - 2018-11-09

- The 0.1.0 release improperly used the 0.0.5 version.  This change is the exact functionality as 0.1.0 but with the version correctly updated.

## [0.1.0] - 2018-11-09

- maps read from YAML values were being transformed into `map[string]string`, but will now be `map[string]interface{}` to maintain the values' types

## [0.0.5] - 2018-07-20

### Notes

- The 0.0.4 release improperly used the 0.0.3 version.  This change is the exact functionality as 0.0.4 but with the version correctly updated.

## [0.0.4] - 2018-07-20

### Notes

- The `binnacle` binaries were improperly build as non-static binaries.  They have been converted to static binaries.
- The Darwin build of `binnacle` was not working properly on Travis.  This has been resolved.

## [0.0.3] - 2018-07-12

### Notes

- The 0.0.2 release improperly used the 0.0.1 version.  This change is the exact functionality as 0.0.2 but with the version correctly updated.

## [0.0.2] - 2018-05-11

### Notes

- Added support for the `helm-diff` plugin via the `diff` subcommand.

## [0.0.1] - 2018-04-27

### Notes

- Initial release

[0.8.0]: https://github.com/traackr/binnacle/tree/0.8.0
[0.7.1]: https://github.com/traackr/binnacle/tree/0.7.1
[0.7.0]: https://github.com/traackr/binnacle/tree/0.7.0
[0.6.0]: https://github.com/traackr/binnacle/tree/0.6.0
[0.5.1]: https://github.com/traackr/binnacle/tree/0.5.1
[0.5.0]: https://github.com/traackr/binnacle/tree/0.5.0
[0.4.0]: https://github.com/traackr/binnacle/tree/0.4.0
[0.3.1]: https://github.com/traackr/binnacle/tree/0.3.1
[0.3.0]: https://github.com/traackr/binnacle/tree/0.3.0
[0.2.1]: https://github.com/traackr/binnacle/tree/0.2.1
[0.2.0]: https://github.com/traackr/binnacle/tree/0.2.0
[0.1.0]: https://github.com/traackr/binnacle/tree/0.1.0
[0.0.5]: https://github.com/traackr/binnacle/tree/0.0.5
[0.0.4]: https://github.com/traackr/binnacle/tree/0.0.4
[0.0.3]: https://github.com/traackr/binnacle/tree/0.0.3
[0.0.2]: https://github.com/traackr/binnacle/tree/0.0.2
[0.0.1]: https://github.com/traackr/binnacle/tree/0.0.1
