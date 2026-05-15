# AGENTS.md

This file provides guidance to coding agents (e.g. Claude Code, claude.ai/code) when working with code in this repository.

## Repository purpose

Go module `github.com/kluster-manager/cluster-profile` — an OCM (Open Cluster Management) controller that applies a "profile" of features to managed clusters. A `ManagedClusterSetProfile` describes a set of features (feature sets, Helm releases, AppsCode AceOpsCenter features) to enable; a `ManagedClusterProfileBinding` binds a profile to a `ManagedClusterSet`. The controller resolves the binding, materializes the requested features on each member cluster (via Helm + the AceOpsCenter feature installer), and drives cluster upgrades.

The produced binary is `cluster-profile`. The README is a Kubebuilder scaffold stub; treat this file as the source of truth.

The local filesystem path is `open-cluster-management.io/cluster-profile`; the **actual Go module is `github.com/kluster-manager/cluster-profile`**.

## Architecture

- `cmd/cluster-profile/main.go` — entry point.
- `pkg/cmds/`:
  - `root.go` — top-level Cobra command.
  - `manager.go` — the long-running manager command.
- `apis/profile/v1alpha1/` — Kubernetes API types:
  - `managedclustersetprofile_types.go` — `ManagedClusterSetProfile` (cluster-scoped).
  - `managedclusterprofilebinding_types.go` — `ManagedClusterProfileBinding`.
  - `groupversion_info.go`, `helpers.go`, generated `zz_generated.deepcopy.go` / `openapi_generated.go`.
  - `install/`, `fuzzer/`, `doc.go` — scheme registration and fuzz helpers.
- `crds/` — generated CRD YAMLs (`profile.k8s.appscode.com_managedclustersetprofiles.yaml`, `profile.k8s.appscode.com_managedclusterprofilebindings.yaml`) plus `lib.go` exposing them via `go:embed`.
- `pkg/controller/` — controller-runtime reconcilers:
  - `managedclustersetprofile_controller.go` — reconciles the profile definition.
  - `managedclusterprofilebinding_controller.go` — reconciles a binding (resolves `ManagedClusterSet` → member clusters and triggers feature install per cluster).
  - `helpers.go` — shared helpers.
- `pkg/feature_installer/` — feature installation strategies invoked from the controllers:
  - `enable_featureset.go`, `helmrelease_create.go`, `opscenter_features.go` — three flavors of "make this feature live on the cluster" (kube FeatureSet objects, Helm releases, AceOpsCenter features).
  - `helpers.go` — kubeconfig assembly via `client-go/tools/clientcmd`.
- `pkg/cluster_upgrade/` — `upgrader.go`, `upgrader_helper.go` — controls cluster-version upgrades that the profile may request.
- `pkg/common/`, `pkg/utils/` — shared constants and helpers.
- `Dockerfile.in` (PROD, distroless) + `Dockerfile.dbg` (debian) — two image variants (no UBI for this one).
- `hack/`, `Makefile` — AppsCode build harness (runs everything inside `ghcr.io/appscode/golang-dev`).
- `vendor/` — checked-in deps.

The CRD API group is `profile.k8s.appscode.com` (AppsCode-specific extension, not upstream OCM).

## Common commands

All Make targets run inside `ghcr.io/appscode/golang-dev` — Docker must be running.

- `make build` / `make all-build` — build host or all-platform binaries.
- `make gen` — regenerate clientset + manifests (CRDs). Run after any change to `apis/**/*_types.go`.
- `make manifests` — regenerate CRDs only.
- `make clientset` — regenerate client code.
- `make openapi` — regenerate OpenAPI definitions (currently commented out of `gen`).
- `make fmt` — gofmt + goimports.
- `make lint` — golangci-lint.
- `make unit-tests` / `make test` — Go unit tests.
- `make verify` — `verify-gen verify-modules`; `go mod tidy && go mod vendor` must leave the tree clean.
- `make container` — build PROD and DBG images.
- `make push` — push both; `make docker-manifest` writes multi-arch manifests; `make release` is the full publish flow.
- `make push-to-kind` / `make deploy-to-kind` — load into Kind and Helm-install.
- `make install` / `make uninstall` / `make purge` — Helm install lifecycle.
- `make add-license` / `make check-license` — manage license headers.

Run a single Go test (requires a local Go toolchain):

```
go test ./pkg/controller/... -run TestName -v
```

## Conventions

- Module path is `github.com/kluster-manager/cluster-profile`. Filesystem location under `open-cluster-management.io/` is for layout only — imports use the GitHub path.
- License: Apache-2.0 (`LICENSE`); new files need the standard "Copyright AppsCode Inc. and Contributors." header (`make add-license`).
- Sign off commits (`git commit -s`); contributions follow the DCO (`DCO` file).
- Vendor directory is checked in — `go mod tidy && go mod vendor` must leave the tree clean (enforced by `verify-modules`).
- Do not hand-edit `zz_generated.*.go`, `openapi_generated.go`, or anything under `crds/` — change `apis/profile/v1alpha1/*_types.go` and re-run `make gen`.
- New feature-install strategies belong in `pkg/feature_installer/` as their own file; route them from the binding controller, don't fan out across `pkg/controller/`.
- The cluster upgrade flow is intentionally separate (`pkg/cluster_upgrade/`) — keep version-upgrade logic out of `pkg/feature_installer/`.
- Two Dockerfiles, one binary — keep `Dockerfile.in` and `Dockerfile.dbg` in sync.
