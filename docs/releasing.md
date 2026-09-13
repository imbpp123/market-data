# Release guide

This guide describes image publication for maintainers. Publishing an image does not deploy the service; deployment settings and checks are in the [operations guide](operations.md).

## Publish a release image

The [release workflow](../.github/workflows/release.yml) builds the tagged source with the existing Dockerfile and pushes a Linux/amd64 image to `ghcr.io/<owner>/<repository>`. It runs when a version tag is pushed or a GitHub Release is published, including pre-releases. Use semantic versions with an optional `v` prefix: `v1.2.3` and `1.2.3` both publish `ghcr.io/imbpp123/market-data:1.2.3`; `v1.2.3-rc.1` publishes `:1.2.3-rc.1`. Invalid version tags fail before registry login. No `latest` tag is published.

Before building or publishing the image, the workflow runs `make check-api`, then `make check` on the tagged commit with Go 1.27.1 and Python 3.13 and 3.14. API checks cover the nested Go module and isolated Python installations. Formatting, build, example configuration, lint, tests, and race checks must also pass before registry login. Any failure stops publication. See the [development checks](development.md#checks) and [API tooling guide](../api/README.md#reproduce-and-check).

From the repository root, tag the commit to release and push the tag. The commands below use `v1.2.3` as an example; choose the intended version:

```sh
git tag v1.2.3
git push origin v1.2.3
```

Alternatively, publish a GitHub Release for the version tag. Publishing a release after pushing its tag runs the workflow again and replaces the same image tag. Use one trigger per version when a second build is not needed. The workflow uses the repository's `GITHUB_TOKEN` with `contents: read` and `packages: write`; no separate registry secret is needed. If the GHCR package already exists, it must grant this repository write access. See [GitHub's registry publishing guide](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images).
