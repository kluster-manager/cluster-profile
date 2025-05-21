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
	"kmodules.xyz/fake-apiserver/pkg/resources"
	uiapi "kmodules.xyz/resource-metadata/apis/ui/v1alpha1"
	"kmodules.xyz/resource-metadata/hub"
	"kubepack.dev/lib-helm/pkg/repo"
	"kubepack.dev/lib-helm/pkg/values"
	workv1 "open-cluster-management.io/api/work/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	klog "sigs.k8s.io/controller-runtime/pkg/log"
	releasesapi "x-helm.dev/apimachinery/apis/releases/v1alpha1"
)

func UpgradeCluster(profileBinding *profilev1alpha1.ManagedClusterProfileBinding, profile *profilev1alpha1.ManagedClusterSetProfile, kc client.Client) error {
	logger := klog.FromContext(context.Background())
	logger.Info(fmt.Sprintf("Upgrading Cluster: %s", profileBinding.Namespace))

	var fakeServer *feature_installer.FakeServer
	var err error
	if fakeServer, err = feature_installer.StartFakeApiServerAndApplyBaseManifestWorkReplicaSets(context.Background(), kc); err != nil {
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

	var overrideValues map[string]interface{}
	if profileBinding.Spec.Features == nil || profileBinding.Spec.Features[hub.ChartOpscenterFeatures].Values == nil {
		return errors.New("no values found in profileBinding")
	}
	if err := json.Unmarshal(profileBinding.Spec.Features[hub.ChartOpscenterFeatures].Values.Raw, &overrideValues); err != nil {
		return fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	if overrideValues, err = InstallOpscenterFeaturesOnFakeServer(fakeServer, overrideValues, &profileBinding.Spec.ClusterMetadata, &chartRef); err != nil {
		return err
	}

	if err := resources.RegisterCRDs(fakeServer.FakeRestConfig); err != nil {
		return err
	}

	defaultValues, err := feature_installer.GetDefaultValues(repo.NewRegistry(fakeServer.FakeClient, repo.DefaultDiskCache()), chartRef)
	if err != nil {
		return err
	}

	mergedValues := values.MergeMaps(defaultValues, overrideValues)

	var mw workv1.ManifestWork
	if err := kc.Get(context.Background(), types.NamespacedName{Name: "opscenter-core", Namespace: profileBinding.GetNamespace()}, &mw); err != nil {
		return err
	}

	configMap, err := createConfigMapInSpokeClusterNamespace(kc, profileBinding.Spec.OpscenterFeaturesVersion, profileBinding.Namespace)
	if err != nil {
		return err
	}

	for i, m := range mw.Spec.Workload.Manifests {
		object := map[string]interface{}{}
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

			mw.Spec.Workload.Manifests[i] = manifest
			configMap.Data[hr.Name] = string(metav1.ConditionTrue)

			_, err := cu.CreateOrPatch(context.Background(), kc, &mw, func(obj client.Object, createOp bool) client.Object {
				in := obj.(*workv1.ManifestWork)
				in.Spec = mw.Spec
				return in
			})
			if err != nil {
				return err
			}

			_, err = cu.CreateOrPatch(context.Background(), kc, configMap, func(obj client.Object, createOp bool) client.Object {
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
	if err := kc.List(context.Background(), &mwList, client.InNamespace(profileBinding.Namespace)); err != nil {
		return err
	}

	for i, mw := range mwList.Items {
		if l, exists := mw.Labels["featureset.appscode.com/managed"]; !exists || l != "true" {
			continue
		}

		for j, m := range mwList.Items[i].Spec.Workload.Manifests {
			object := map[string]interface{}{}
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

			var currValues map[string]interface{}
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
			if err := fakeServer.FakeClient.Get(context.Background(), types.NamespacedName{Name: hr.Name}, &feature); err != nil {
				return err
			}
			var featureValues map[string]interface{}
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

			mwList.Items[i].Spec.Workload.Manifests[j] = manifest
			configMap.Data[hr.Name] = string(metav1.ConditionTrue)
		}
		_, err := cu.CreateOrPatch(context.Background(), kc, &mwList.Items[i], func(obj client.Object, createOp bool) client.Object {
			in := obj.(*workv1.ManifestWork)
			in.Spec = mwList.Items[i].Spec
			return in
		})
		if err != nil {
			return err
		}

		_, err = cu.CreateOrPatch(context.Background(), kc, configMap, func(obj client.Object, createOp bool) client.Object {
			in := obj.(*corev1.ConfigMap)
			in.Data = configMap.Data
			return in
		})
		if err != nil {
			return err
		}
	}
	configMap.Data["status"] = "completed"
	_, err = cu.CreateOrPatch(context.Background(), kc, configMap, func(obj client.Object, createOp bool) client.Object {
		in := obj.(*corev1.ConfigMap)
		in.Data = configMap.Data
		return in
	})
	if err != nil {
		return err
	}

	return nil
}
