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
	"fmt"

	"github.com/kluster-manager/cluster-profile/pkg/common"
	"github.com/kluster-manager/cluster-profile/pkg/feature_installer"
	"github.com/kluster-manager/cluster-profile/pkg/utils"

	fluxhelm "github.com/fluxcd/helm-controller/api/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/rand"
	kmapi "kmodules.xyz/client-go/api/v1"
	"kmodules.xyz/resource-metadata/hub"
	workv1 "open-cluster-management.io/api/work/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	releasesapi "x-helm.dev/apimachinery/apis/releases/v1alpha1"
)

func createConfigMapInSpokeClusterNamespace(kc client.Client, ver, clusterName string) (*corev1.ConfigMap, error) {
	var err error
	cmData := make(map[string]string)
	cmData["opscenter-features"] = string(metav1.ConditionFalse)
	cmData["version"] = ver
	cmData["status"] = "pending"

	var mwList workv1.ManifestWorkList
	if err := kc.List(context.Background(), &mwList, client.InNamespace(clusterName)); err != nil {
		return nil, err
	}
	for _, mw := range mwList.Items {
		if l, exists := mw.Labels["featureset.appscode.com/managed"]; !exists || l != "true" {
			continue
		}

		for _, m := range mw.Spec.Workload.Manifests {
			object := map[string]interface{}{}
			if err = utils.Copy(m, &object); err != nil {
				return nil, err
			}

			kind, _, err := feature_installer.GetKindAndName(object)
			if err != nil {
				return nil, err
			}

			if kind != "HelmRelease" {
				continue
			}

			hr := fluxhelm.HelmRelease{}
			if err = utils.Copy(m, &hr); err != nil {
				return nil, err
			}
			if name, exists := hr.Labels["app.kubernetes.io/name"]; !exists || name != "featuresets.ui.k8s.appscode.com" {
				continue
			}

			if hr.Name != hub.ChartFluxCD && hr.Name != hub.ChartOpscenterFeatures {
				cmData[hr.Name] = string(metav1.ConditionFalse)
			}
		}
	}

	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("features-upgrader-%s", rand.String(10)),
			Namespace: clusterName,
			Labels: map[string]string{
				common.ACEUpgrader:        "true",
				common.ACEUpgraderVersion: ver,
			},
		},
		Data: cmData,
	}

	if err = kc.Create(context.Background(), &cm); err != nil {
		return nil, err
	}
	return &cm, nil
}

func InstallOpscenterFeaturesOnFakeServer(fakeServer *feature_installer.FakeServer, clusterMetadata *kmapi.ClusterInfo, chartRef *releasesapi.ChartSourceRef) (map[string]interface{}, error) {
	overrides := make(map[string]interface{})
	overrides, err := feature_installer.GetOverrideValues(overrides, clusterMetadata)
	if err != nil {
		return nil, err
	}
	overrideValues, err := json.Marshal(overrides)
	if err != nil {
		return nil, err
	}

	if err := feature_installer.InstallOpscenterFeatures(overrideValues, fakeServer, chartRef); err != nil {
		return nil, err
	}
	return overrides, nil
}
