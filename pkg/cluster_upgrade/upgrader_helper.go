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
	"strings"

	profilev1alpha1 "github.com/kluster-manager/cluster-profile/apis/profile/v1alpha1"
	"github.com/kluster-manager/cluster-profile/pkg/common"
	"github.com/kluster-manager/cluster-profile/pkg/feature_installer"
	"github.com/kluster-manager/cluster-profile/pkg/utils"

	fluxhelm "github.com/fluxcd/helm-controller/api/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/klog/v2"
	kmapi "kmodules.xyz/client-go/api/v1"
	cu "kmodules.xyz/client-go/client"
	"kmodules.xyz/resource-metadata/hub"
	workv1 "open-cluster-management.io/api/work/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	releasesapi "x-helm.dev/apimachinery/apis/releases/v1alpha1"
)

func helmReleaseFeedback(mw *workv1.ManifestWork, hrNamespace, hrName string) ([]workv1.FeedbackValue, bool) {
	for _, mc := range mw.Status.ResourceStatus.Manifests {
		if mc.ResourceMeta.Kind != fluxhelm.HelmReleaseKind || mc.ResourceMeta.Name != hrName || mc.ResourceMeta.Namespace != hrNamespace {
			continue
		}
		return mc.StatusFeedbacks.Values, meta.IsStatusConditionTrue(mc.Conditions, workv1.ManifestApplied)
	}
	return nil, false
}

func feedbackInt(values []workv1.FeedbackValue, name string) (int64, bool) {
	for _, v := range values {
		if v.Name == name && v.Value.Integer != nil {
			return *v.Value.Integer, true
		}
	}
	return 0, false
}

func feedbackString(values []workv1.FeedbackValue, name string) (string, bool) {
	for _, v := range values {
		if v.Name == name && v.Value.String != nil {
			return *v.Value.String, true
		}
	}
	return "", false
}

// helmReleaseGeneration reports the spoke-side generation of the HelmRelease as
// last synced back by the work agent, or 0 if it was never synced.
func helmReleaseGeneration(mw *workv1.ManifestWork, hrNamespace, hrName string) int64 {
	values, _ := helmReleaseFeedback(mw, hrNamespace, hrName)
	generation, _ := feedbackInt(values, common.HelmReleaseGenerationFeedback)
	return generation
}

// helmReleaseReady reports whether mw's status shows target's HelmRelease as
// applied and Ready *for the spec this upgrade wrote*. Feedback values lag the
// spec patch and flux keeps Ready=True until it notices the new spec, so a Ready
// feedback alone can still describe the pre-upgrade release. The generation
// feedback pins it down: it must have reached target.MinGeneration (proving the
// object is the one we wrote) and flux must have observed it (proving flux is
// done with it, not about to start).
func helmReleaseReady(mw *workv1.ManifestWork, target upgradeTarget) bool {
	values, applied := helmReleaseFeedback(mw, target.HelmReleaseNamespace, target.HelmReleaseName)
	if !applied {
		return false
	}

	if ready, ok := feedbackString(values, common.HelmReleaseReadyFeedback); !ok || ready != string(metav1.ConditionTrue) {
		return false
	}

	generation, ok := feedbackInt(values, common.HelmReleaseGenerationFeedback)
	if !ok || generation < target.MinGeneration {
		return false
	}

	observedGeneration, ok := feedbackInt(values, common.HelmReleaseObservedGenerationFeedback)
	return ok && observedGeneration == generation
}

// ensureHelmReleaseFeedbackRules makes mw report back everything helmReleaseReady
// needs for the given HelmRelease. The upgrade path never goes through
// updateManifestWork, so a ManifestWork installed by an older build carries only
// the rules it was created with.
func ensureHelmReleaseFeedbackRules(mw *workv1.ManifestWork, hrNamespace, hrName string) {
	rules := []workv1.FeedbackRule{
		{
			Type: workv1.JSONPathsType,
			JsonPaths: []workv1.JsonPath{
				{
					Name: common.HelmReleaseReadyFeedback,
					Path: `.status.conditions[?(@.type=="Ready")].status`,
				},
				{
					Name: "Released",
					Path: `.status.conditions[?(@.type=="Released")].status`,
				},
				{
					Name: common.HelmReleaseGenerationFeedback,
					Path: ".metadata.generation",
				},
				{
					Name: common.HelmReleaseObservedGenerationFeedback,
					Path: ".status.observedGeneration",
				},
			},
		},
	}

	for i, config := range mw.Spec.ManifestConfigs {
		if config.ResourceIdentifier.Resource == "helmreleases" &&
			config.ResourceIdentifier.Name == hrName &&
			config.ResourceIdentifier.Namespace == hrNamespace {
			mw.Spec.ManifestConfigs[i].FeedbackRules = rules
			return
		}
	}

	mw.Spec.ManifestConfigs = append(mw.Spec.ManifestConfigs, workv1.ManifestConfigOption{
		ResourceIdentifier: workv1.ResourceIdentifier{
			Group:     fluxhelm.GroupVersion.Group,
			Resource:  "helmreleases",
			Name:      hrName,
			Namespace: hrNamespace,
		},
		FeedbackRules: rules,
	})
}

// evaluateTargets marks every target that has become ready in configMap and
// returns the ones still pending. A ManifestWork read error keeps its target
// pending rather than failing the upgrade: the specs are already on their way to
// the spoke, so the only sane response is to look again next tick.
func evaluateTargets(ctx context.Context, kc client.Client, targets []upgradeTarget, configMap *corev1.ConfigMap) ([]upgradeTarget, error) {
	logger := klog.FromContext(ctx)
	pending := make([]upgradeTarget, 0, len(targets))
	becameReady := false

	for _, target := range targets {
		var mw workv1.ManifestWork
		if err := kc.Get(ctx, types.NamespacedName{Name: target.ManifestWorkName, Namespace: target.ManifestWorkNamespace}, &mw); err != nil {
			logger.Error(err, "failed to read ManifestWork while waiting for HelmRelease", "manifestWork", klog.KRef(target.ManifestWorkNamespace, target.ManifestWorkName))
			pending = append(pending, target)
			continue
		}

		if helmReleaseReady(&mw, target) {
			if configMap.Data[target.HelmReleaseName] != string(metav1.ConditionTrue) {
				configMap.Data[target.HelmReleaseName] = string(metav1.ConditionTrue)
				becameReady = true
			}
		} else {
			pending = append(pending, target)
		}
	}

	if becameReady {
		if err := patchConfigMapData(ctx, kc, configMap); err != nil {
			return nil, fmt.Errorf("failed to mark HelmRelease(s) ready in upgrader ConfigMap %s: %w", client.ObjectKeyFromObject(configMap), err)
		}
	}

	return pending, nil
}

func targetNames(targets []upgradeTarget) string {
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, fmt.Sprintf("%s/%s", target.HelmReleaseNamespace, target.HelmReleaseName))
	}
	return strings.Join(names, ", ")
}

// findPendingUpgrade returns the ConfigMap and targets of an upgrade this
// controller already applied and is still waiting on, or nil if there is none.
// A ConfigMap without the targets annotation was written by an older build and
// cannot be resumed, so it is reported as no pending upgrade; re-applying is
// harmless because every patch is idempotent.
func findPendingUpgrade(ctx context.Context, kc client.Client, profileBinding *profilev1alpha1.ManagedClusterProfileBinding) (*corev1.ConfigMap, []upgradeTarget, error) {
	var configMaps corev1.ConfigMapList
	if err := kc.List(ctx, &configMaps, client.InNamespace(profileBinding.Namespace), client.MatchingLabels{common.ACEUpgrader: "true"}); err != nil {
		return nil, nil, err
	}

	for i := range configMaps.Items {
		configMap := &configMaps.Items[i]
		if configMap.Labels[common.ACEUpgraderVersion] != profileBinding.Spec.OpscenterFeaturesVersion ||
			configMap.Data[common.UpgradeStatusKey] != common.UpgradeStatusPending ||
			// A repeated force-upgrade at the same version is a new run, not a resume.
			configMap.Annotations[common.UpgradeAnnotation] != profileBinding.Annotations[common.UpgradeAnnotation] {
			continue
		}

		raw, ok := configMap.Annotations[common.UpgradeTargetsAnnotation]
		if !ok {
			continue
		}
		var targets []upgradeTarget
		if err := json.Unmarshal([]byte(raw), &targets); err != nil {
			return nil, nil, fmt.Errorf("failed to read upgrade targets from ConfigMap %s: %w", client.ObjectKeyFromObject(configMap), err)
		}
		return configMap, targets, nil
	}

	return nil, nil, nil
}

// recordPendingUpgrade stores what the observing reconciles need to resume the
// wait without re-rendering the chart.
func recordPendingUpgrade(ctx context.Context, kc client.Client, configMap *corev1.ConfigMap, targets []upgradeTarget, upgradeAt string) error {
	raw, err := json.Marshal(targets)
	if err != nil {
		return err
	}
	if configMap.Annotations == nil {
		configMap.Annotations = make(map[string]string)
	}
	configMap.Annotations[common.UpgradeTargetsAnnotation] = string(raw)
	if upgradeAt != "" {
		configMap.Annotations[common.UpgradeAnnotation] = upgradeAt
	}
	return patchConfigMapData(ctx, kc, configMap)
}

// finishUpgrade closes out the run. "completed" only tells the UI that the
// upgrade job is done; whether it succeeded is read from the per-feature values,
// so it is set even when some features never became ready.
func finishUpgrade(ctx context.Context, kc client.Client, configMap *corev1.ConfigMap, upgradeErr error) error {
	configMap.Data[common.UpgradeStatusKey] = common.UpgradeStatusCompleted
	delete(configMap.Annotations, common.UpgradeTargetsAnnotation)
	return errors.Join(upgradeErr, patchConfigMapData(ctx, kc, configMap))
}

// deleteStaleUpgraderConfigMaps drops upgrader ConfigMaps left behind by earlier
// runs, so the UI always has exactly one to read. Reaching this point means no
// resumable run exists, so nothing here is still in use.
func deleteStaleUpgraderConfigMaps(ctx context.Context, kc client.Client, clusterName string) error {
	var configMaps corev1.ConfigMapList
	if err := kc.List(ctx, &configMaps, client.InNamespace(clusterName), client.MatchingLabels{common.ACEUpgrader: "true"}); err != nil {
		return err
	}
	for i := range configMaps.Items {
		if err := kc.Delete(ctx, &configMaps.Items[i]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func patchConfigMapData(ctx context.Context, kc client.Client, configMap *corev1.ConfigMap) error {
	_, err := cu.CreateOrPatch(ctx, kc, configMap, func(obj client.Object, createOp bool) client.Object {
		in := obj.(*corev1.ConfigMap)
		in.Data = configMap.Data
		// Merge rather than assign: only the upgrade bookkeeping is ours to set.
		for k, v := range configMap.Annotations {
			if in.Annotations == nil {
				in.Annotations = make(map[string]string)
			}
			in.Annotations[k] = v
		}
		if _, ours := configMap.Annotations[common.UpgradeTargetsAnnotation]; !ours {
			delete(in.Annotations, common.UpgradeTargetsAnnotation)
		}
		return in
	})
	return err
}

func createConfigMapInSpokeClusterNamespace(ctx context.Context, kc client.Client, ver, clusterName string) (*corev1.ConfigMap, error) {
	if err := deleteStaleUpgraderConfigMaps(ctx, kc, clusterName); err != nil {
		return nil, err
	}

	var err error
	cmData := make(map[string]string)
	cmData["opscenter-features"] = string(metav1.ConditionFalse)
	cmData["version"] = ver
	cmData[common.UpgradeStatusKey] = common.UpgradeStatusPending

	var mwList workv1.ManifestWorkList
	if err := kc.List(ctx, &mwList, client.InNamespace(clusterName)); err != nil {
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

	if err = kc.Create(ctx, &cm); err != nil {
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
