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
	"slices"
	"strings"
	"time"

	"github.com/kluster-manager/cluster-profile/pkg/common"
	"github.com/kluster-manager/cluster-profile/pkg/feature_installer"
	"github.com/kluster-manager/cluster-profile/pkg/utils"

	fluxhelm "github.com/fluxcd/helm-controller/api/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	kmapi "kmodules.xyz/client-go/api/v1"
	cu "kmodules.xyz/client-go/client"
	"kmodules.xyz/resource-metadata/hub"
	workv1 "open-cluster-management.io/api/work/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	releasesapi "x-helm.dev/apimachinery/apis/releases/v1alpha1"
)

// helmReleaseReady reports whether mw's status shows the HelmRelease
// identified by (hrNamespace, hrName) as applied and feedback-Ready.
func helmReleaseReady(mw *workv1.ManifestWork, hrNamespace, hrName string) bool {
	for _, mc := range mw.Status.ResourceStatus.Manifests {
		if mc.ResourceMeta.Kind != fluxhelm.HelmReleaseKind || mc.ResourceMeta.Name != hrName || mc.ResourceMeta.Namespace != hrNamespace {
			continue
		}

		if !meta.IsStatusConditionTrue(mc.Conditions, workv1.ManifestApplied) {
			return false
		}

		for _, v := range mc.StatusFeedbacks.Values {
			if v.Name == common.HelmReleaseReadyFeedback && v.Value.String != nil && *v.Value.String == string(metav1.ConditionTrue) {
				return true
			}
		}
		return false
	}

	return false
}

// waitForHelmReleasesReady marks each target ready in configMap as it becomes
// ready, until none are left or timeout elapses. A timeout is an error: the
// targets left "false" never became ready, so the upgrade did not succeed.
func waitForHelmReleasesReady(kc client.Client, targets []upgradeTarget, configMap *corev1.ConfigMap, interval, timeout time.Duration) error {
	pending := slices.Clone(targets)

	err := wait.PollUntilContextTimeout(context.Background(), interval, timeout, true, func(ctx context.Context) (bool, error) {
		logger := klog.FromContext(ctx)
		notReady := make([]upgradeTarget, 0, len(pending))
		becameReady := false

		for _, target := range pending {
			var mw workv1.ManifestWork
			if err := kc.Get(ctx, types.NamespacedName{Name: target.manifestWorkName, Namespace: target.manifestWorkNamespace}, &mw); err != nil {
				// The upgrade is already in flight on the spoke; a read error must not
				// abort it, so keep the target pending and retry on the next tick.
				logger.Error(err, "failed to read ManifestWork while waiting for HelmRelease", "manifestWork", klog.KRef(target.manifestWorkNamespace, target.manifestWorkName))
				notReady = append(notReady, target)
				continue
			}

			if helmReleaseReady(&mw, target.helmReleaseNamespace, target.helmReleaseName) {
				configMap.Data[target.helmReleaseName] = string(metav1.ConditionTrue)
				becameReady = true
			} else {
				notReady = append(notReady, target)
			}
		}

		if becameReady {
			if err := patchConfigMapData(ctx, kc, configMap); err != nil {
				// pending is left untouched so the marks are re-applied next tick.
				logger.Error(err, "failed to mark HelmRelease(s) ready in upgrader ConfigMap", "configMap", klog.KObj(configMap))
				return false, nil
			}
		}

		pending = notReady

		return len(pending) == 0, nil
	})
	if wait.Interrupted(err) {
		names := make([]string, 0, len(pending))
		for _, target := range pending {
			names = append(names, fmt.Sprintf("%s/%s", target.helmReleaseNamespace, target.helmReleaseName))
		}
		return fmt.Errorf("timed out after %s waiting for HelmRelease(s) to become ready: %s", timeout, strings.Join(names, ", "))
	}
	return err
}

func patchConfigMapData(ctx context.Context, kc client.Client, configMap *corev1.ConfigMap) error {
	_, err := cu.CreateOrPatch(ctx, kc, configMap, func(obj client.Object, createOp bool) client.Object {
		in := obj.(*corev1.ConfigMap)
		in.Data = configMap.Data
		return in
	})
	return err
}

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
			object := map[string]any{}
			if err = utils.Copy(m, &object); err != nil {
				return nil, err
			}

			kind, _, _, err := feature_installer.GetKindNameNamespace(object)
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

func InstallOpscenterFeaturesOnFakeServer(fakeServer *feature_installer.FakeServer, overrides map[string]any, clusterMetadata *kmapi.ClusterInfo, chartRef *releasesapi.ChartSourceRef) (map[string]any, error) {
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
