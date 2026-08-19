/*
Copyright AppsCode Inc. and Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cluster_upgrade

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	profilev1alpha1 "github.com/kluster-manager/cluster-profile/apis/profile/v1alpha1"
	"github.com/kluster-manager/cluster-profile/pkg/feature_installer"
	"github.com/kluster-manager/cluster-profile/pkg/utils"

	fluxhelm "github.com/fluxcd/helm-controller/api/v2"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	cu "kmodules.xyz/client-go/client"
	uiapi "kmodules.xyz/resource-metadata/apis/ui/v1alpha1"
	"kmodules.xyz/resource-metadata/hub"
	"kubepack.dev/lib-helm/pkg/repo"
	"kubepack.dev/lib-helm/pkg/values"
	workv1 "open-cluster-management.io/api/work/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	klog "sigs.k8s.io/controller-runtime/pkg/log"
	releasesapi "x-helm.dev/apimachinery/apis/releases/v1alpha1"
)

const (
	helmReleaseReadyPollInterval = 6 * time.Second
	helmReleaseReadyTimeout      = 30 * time.Minute
)

type upgradeTarget struct {
	manifestWorkNamespace string
	manifestWorkName      string
	helmReleaseNamespace  string
	helmReleaseName       string
	// minGeneration is the spoke-side HelmRelease generation that proves the spec
	// this upgrade wrote has reached the cluster.
	minGeneration int64
}

// newUpgradeTarget derives the generation that marks oldObject as replaced by
// newObject. A rewrite that changes nothing does not bump the generation on the
// spoke, so waiting for a higher one would hang until the timeout.
func newUpgradeTarget(mw *workv1.ManifestWork, hr *fluxhelm.HelmRelease, oldObject, newObject map[string]any) upgradeTarget {
	minGeneration := helmReleaseGeneration(mw, hr.Namespace, hr.Name)
	if !reflect.DeepEqual(oldObject, newObject) {
		minGeneration++
	}

	return upgradeTarget{
		manifestWorkNamespace: mw.Namespace,
		manifestWorkName:      mw.Name,
		helmReleaseNamespace:  hr.Namespace,
		helmReleaseName:       hr.Name,
		minGeneration:         minGeneration,
	}
}

func UpgradeCluster(ctx context.Context, profileBinding *profilev1alpha1.ManagedClusterProfileBinding, profile *profilev1alpha1.ManagedClusterSetProfile, kc client.Client) error {
	logger := klog.FromContext(ctx)
	logger.Info(fmt.Sprintf("Upgrading Cluster: %s", profileBinding.Namespace))

	var fakeServer *feature_installer.FakeServer
	var err error
	if fakeServer, err = feature_installer.StartFakeApiServerAndApplyBaseManifestWorkReplicaSets(ctx, kc, profileBinding); err != nil {
		return err
	}

	defer func() {
		if err := fakeServer.FakeSrv.Shutdown(context.Background()); err != nil {
			logger.Error(err, "server shutdown error")
		}
	}()

	chartRef := releasesapi.ChartSourceRef{
		Name:      hub.ChartOpscenterFeatures,
		Version:   profileBinding.Spec.OpscenterFeaturesVersion,
		SourceRef: hub.BootstrapHelmRepository(fakeServer.FakeClient),
	}

	var overrideValues map[string]any
	if profileBinding.Spec.Features == nil || profileBinding.Spec.Features[hub.ChartOpscenterFeatures].Values == nil {
		return errors.New("no values found in profileBinding")
	}
	if err := json.Unmarshal(profileBinding.Spec.Features[hub.ChartOpscenterFeatures].Values.Raw, &overrideValues); err != nil {
		return fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	if overrideValues, err = InstallOpscenterFeaturesOnFakeServer(fakeServer, overrideValues, &profileBinding.Spec.ClusterMetadata, &chartRef); err != nil {
		return err
	}

	if err := feature_installer.RegisterRequiredCRDs(fakeServer, profileBinding); err != nil {
		return err
	}

	defaultValues, err := feature_installer.GetDefaultValues(repo.NewRegistry(fakeServer.FakeClient, repo.DefaultDiskCache()), chartRef)
	if err != nil {
		return err
	}

	mergedValues := values.MergeMaps(defaultValues, overrideValues)

	var mw workv1.ManifestWork
	if err := kc.Get(ctx, types.NamespacedName{Name: "opscenter-core", Namespace: profileBinding.GetNamespace()}, &mw); err != nil {
		return err
	}

	configMap, err := createConfigMapInSpokeClusterNamespace(ctx, kc, profileBinding.Spec.OpscenterFeaturesVersion, profileBinding.Namespace)
	if err != nil {
		return err
	}

	var upgradeTargets []upgradeTarget

	for i, m := range mw.Spec.Workload.Manifests {
		object := map[string]any{}
		if err = utils.Copy(m, &object); err != nil {
			return err
		}

		_, name, _, err := feature_installer.GetKindNameNamespace(object)
		if err != nil {
			return err
		}

		if name == hub.ChartOpscenterFeatures {
			hr := fluxhelm.HelmRelease{}
			if err = utils.Copy(m, &hr); err != nil {
				return err
			}

			hr.Spec.Chart.Spec.Version = profileBinding.Spec.OpscenterFeaturesVersion
			if mergedValues != nil {
				jsonData, err := json.Marshal(mergedValues)
				if err != nil {
					return err
				}

				var apiextensionsJSON v1.JSON
				if err := json.Unmarshal(jsonData, &apiextensionsJSON); err != nil {
					return err
				}
				hr.Spec.Values = &apiextensionsJSON
			}

			manifest := workv1.Manifest{}
			if err = utils.Copy(hr, &manifest); err != nil {
				return err
			}

			newObject := map[string]any{}
			if err = utils.Copy(manifest, &newObject); err != nil {
				return err
			}

			mw.Spec.Workload.Manifests[i] = manifest
			configMap.Data[hr.Name] = string(metav1.ConditionFalse)
			upgradeTargets = append(upgradeTargets, newUpgradeTarget(&mw, &hr, object, newObject))
			ensureHelmReleaseFeedbackRules(&mw, hr.Namespace, hr.Name)

			_, err := cu.CreateOrPatch(ctx, kc, &mw, func(obj client.Object, createOp bool) client.Object {
				in := obj.(*workv1.ManifestWork)
				in.Spec = mw.Spec
				return in
			})
			if err != nil {
				return err
			}

			_, err = cu.CreateOrPatch(ctx, kc, configMap, func(obj client.Object, createOp bool) client.Object {
				in := obj.(*corev1.ConfigMap)
				in.Data = configMap.Data
				return in
			})
			if err != nil {
				return err
			}
			break
		}
	}

	var mwList workv1.ManifestWorkList
	if err := kc.List(ctx, &mwList, client.InNamespace(profileBinding.Namespace)); err != nil {
		return err
	}

	for i, mw := range mwList.Items {
		if l, exists := mw.Labels["featureset.appscode.com/managed"]; !exists || l != "true" {
			continue
		}

		for j, m := range mwList.Items[i].Spec.Workload.Manifests {
			object := map[string]any{}
			if err = utils.Copy(m, &object); err != nil {
				return err
			}

			kind, name, _, err := feature_installer.GetKindNameNamespace(object)
			if err != nil {
				return err
			}

			if kind != fluxhelm.HelmReleaseKind || name == hub.ChartOpscenterFeatures {
				continue
			}

			hr := fluxhelm.HelmRelease{}
			if err = utils.Copy(m, &hr); err != nil {
				return err
			}
			if label, exists := hr.Labels["app.kubernetes.io/component"]; !exists || label != hr.Name {
				continue
			}

			var currValues map[string]any
			skipManagedClusterSetProfileValues := false
			if profileBinding.Spec.Features != nil {
				if _, exist := profileBinding.Spec.Features[hr.Name]; exist {
					if profileBinding.Spec.Features[hr.Name].Values != nil {
						if err = json.Unmarshal(profileBinding.Spec.Features[hr.Name].Values.Raw, &currValues); err != nil {
							return err
						}
						skipManagedClusterSetProfileValues = true
					}
				}
			}

			if !skipManagedClusterSetProfileValues {
				if profile.Spec.Features[hr.Name].Values != nil {
					err = json.Unmarshal(profile.Spec.Features[hr.Name].Values.Raw, &currValues)
					if err != nil {
						return err
					}
				}
			}

			var feature uiapi.Feature
			if err := fakeServer.FakeClient.Get(ctx, types.NamespacedName{Name: hr.Name}, &feature); err != nil {
				return err
			}
			var featureValues map[string]any
			if feature.Spec.Values != nil {
				if err := json.Unmarshal(feature.Spec.Values.Raw, &featureValues); err != nil {
					return err
				}
			}

			finalValues := values.MergeMaps(featureValues, currValues)
			if finalValues != nil {
				jsonData, err := json.Marshal(finalValues)
				if err != nil {
					return err
				}

				var apiextensionsJSON v1.JSON
				if err := json.Unmarshal(jsonData, &apiextensionsJSON); err != nil {
					return err
				}
				hr.Spec.Values = &apiextensionsJSON
			}
			hr.Spec.Chart.Spec.Version = feature.Spec.Chart.Version
			manifest := workv1.Manifest{}
			if err = utils.Copy(hr, &manifest); err != nil {
				return err
			}

			newObject := map[string]any{}
			if err = utils.Copy(manifest, &newObject); err != nil {
				return err
			}

			mwList.Items[i].Spec.Workload.Manifests[j] = manifest
			configMap.Data[hr.Name] = string(metav1.ConditionFalse)
			upgradeTargets = append(upgradeTargets, newUpgradeTarget(&mwList.Items[i], &hr, object, newObject))
			ensureHelmReleaseFeedbackRules(&mwList.Items[i], hr.Namespace, hr.Name)
		}
		_, err := cu.CreateOrPatch(ctx, kc, &mwList.Items[i], func(obj client.Object, createOp bool) client.Object {
			in := obj.(*workv1.ManifestWork)
			in.Spec = mwList.Items[i].Spec
			return in
		})
		if err != nil {
			return err
		}

		_, err = cu.CreateOrPatch(ctx, kc, configMap, func(obj client.Object, createOp bool) client.Object {
			in := obj.(*corev1.ConfigMap)
			in.Data = configMap.Data
			return in
		})
		if err != nil {
			return err
		}
	}

	waitErr := waitForHelmReleasesReady(ctx, kc, upgradeTargets, configMap, helmReleaseReadyPollInterval, helmReleaseReadyTimeout)
	if waitErr != nil && ctx.Err() != nil {
		// Interrupted, so the upgrade job never finished: leave the ConfigMap pending.
		return waitErr
	}

	// "completed" only tells the UI that the upgrade job is done; whether it
	// succeeded is read from the per-feature values, so it is set even when some
	// features never became ready.
	configMap.Data["status"] = "completed"
	return errors.Join(waitErr, patchConfigMapData(ctx, kc, configMap))
}
