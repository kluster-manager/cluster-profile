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

package feature_installer

import (
	"context"
	pkgerr "errors"
	"fmt"
	"reflect"
	kstr "strings"

	profilev1alpha1 "github.com/kluster-manager/cluster-profile/apis/profile/v1alpha1"
	"github.com/kluster-manager/cluster-profile/pkg/common"
	"github.com/kluster-manager/cluster-profile/pkg/utils"

	fluxhelm "github.com/fluxcd/helm-controller/api/v2"
	"gomodules.xyz/x/strings"
	"helm.sh/helm/v3/pkg/release"
	crdv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crd_cs "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/utils/ptr"
	kmapi "kmodules.xyz/client-go/api/v1"
	crd_util "kmodules.xyz/client-go/apiextensions"
	cu "kmodules.xyz/client-go/client"
	kmeta "kmodules.xyz/client-go/meta"
	"kmodules.xyz/fake-apiserver/pkg/resources"
	uiapi "kmodules.xyz/resource-metadata/apis/ui/v1alpha1"
	"kmodules.xyz/resource-metadata/hub"
	"kubepack.dev/lib-app/pkg/editor"
	"kubepack.dev/lib-app/pkg/handler"
	"kubepack.dev/lib-helm/pkg/repo"
	"kubepack.dev/lib-helm/pkg/values"
	workv1 "open-cluster-management.io/api/work/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	klog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
	releasesapi "x-helm.dev/apimachinery/apis/releases/v1alpha1"
)

type featureStatus struct {
	enabled bool
	managed bool
	ready   bool
}

var DefaultCache = repo.DefaultDiskCache()

func NewVirtualRegistry(kc client.Client) repo.IRegistry {
	return repo.NewRegistry(kc, DefaultCache)
}

func EnableFeatures(ctx context.Context, kc client.Client, profileBinding *profilev1alpha1.ManagedClusterProfileBinding, featureInfo map[string][]string, profile *profilev1alpha1.ManagedClusterSetProfile) error {
	logger := klog.FromContext(ctx)
	logger.Info(fmt.Sprintf("Profile: %s, ProfileBinding: %s, FeatureSetInfo: %+v", profile.Name, profileBinding.Name, featureInfo))

	var err error
	if err = enableFeatureSet(ctx, kc, "opscenter-core", featureInfo["opscenter-core"], profile, profileBinding); err != nil {
		return err
	}
	for fset, featureList := range featureInfo {
		if fset == "opscenter-core" {
			continue
		}

		// profileBinding.Namespace == managed cluster name
		featureList, err = sanitizeFeatures(kc, profileBinding.Namespace, featureList)
		if err != nil {
			return err
		}

		if err = enableFeatureSet(ctx, kc, fset, featureList, profile, profileBinding); err != nil {
			return err
		}
	}

	for fset, featureList := range featureInfo {
		var mw workv1.ManifestWork
		if err = kc.Get(ctx, types.NamespacedName{Name: fset, Namespace: profileBinding.Namespace}, &mw); err != nil && !errors.IsNotFound(err) {
			return err
		}
		if err = removeHRFomManifestWork(ctx, kc, &mw, featureList); err != nil {
			return err
		}
	}
	return nil
}

func enableFeatureSet(ctx context.Context, kc client.Client, featureSet string, features []string, profile *profilev1alpha1.ManagedClusterSetProfile, profileBinding *profilev1alpha1.ManagedClusterProfileBinding) error {
	if featureSet == "opscenter-core" && (!strings.Contains(features, "opscenter-features") || !strings.Contains(features, "kube-ui-server")) {
		return pkgerr.New("ensure opscenter-features and kube-ui-server are included in the feature list")
	}

	logger := klog.FromContext(ctx)
	logger.Info(fmt.Sprintf("Profile: %s,  ProfileBinding: %s, FeatureSet: %s, Features: %+v", profile.Name, profileBinding.Name, featureSet, features))

	// <<<<<<<<       start fake-apiserver and apply base manifestWorkReplicaSet, feature-namespace manifestWork and helm install 'opscenter-features' chart       >>>>>>>
	var err error
	var fakeServer *FakeServer
	if fakeServer, err = StartFakeApiServerAndApplyBaseManifestWorkReplicaSets(ctx, kc); err != nil {
		return err
	}

	mw := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      featureSet,
			Namespace: profileBinding.Namespace,
			Labels: map[string]string{
				common.LabelAceFeatureSet: "true",
				common.ProfileLabel:       profile.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(profileBinding, profilev1alpha1.SchemeGroupVersion.WithKind(profilev1alpha1.ResourceKindManagedClusterProfileBinding)),
			},
		},
	}

	err = kc.Get(ctx, types.NamespacedName{Name: featureSet, Namespace: profileBinding.Namespace}, mw)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	var overrideValues map[string]interface{}
	if overrideValues, err = InstallOpscenterFeaturesOnFakeServer(fakeServer, profile, &profileBinding.Spec.ClusterMetadata, nil); err != nil {
		return err
	}

	fakeServer.FakeS.Checkpoint()

	// <<<<<<<<       all necessary resources applied on fake-apiserver and set 'checkpoint' to differentiate modified objects       >>>>>>>

	err = resources.RegisterCRDs(fakeServer.FakeRestConfig)
	if err != nil {
		return err
	}

	if featureSet == "opscenter-core" {
		var featureObj uiapi.Feature
		if err = fakeServer.FakeClient.Get(context.Background(), types.NamespacedName{Name: "opscenter-features"}, &featureObj); err != nil {
			return err
		}

		chartRef := releasesapi.ChartSourceRef{
			Name:      hub.ChartOpscenterFeatures,
			Version:   profile.Spec.Features["opscenter-features"].Chart.Version,
			SourceRef: hub.BootstrapHelmRepository(fakeServer.FakeClient),
		}

		defaultValues, err := getDefaultValues(NewVirtualRegistry(fakeServer.FakeClient), chartRef)
		if err != nil {
			return err
		}

		mergedValues := values.MergeMaps(defaultValues, overrideValues)
		if err = createHR("opscenter-features", "opscenter-core", hub.BootstrapHelmRepositoryNamespace(), profile, featureObj, fakeServer, mergedValues); err != nil {
			return err
		}

		if err = fakeServer.FakeClient.Get(context.Background(), types.NamespacedName{Name: "kube-ui-server"}, &featureObj); err != nil {
			return err
		}

		return applyFeatureSet(ctx, kc, mw, fakeServer, featureSet, []string{"kube-ui-server"}, profile)
	}
	return applyFeatureSet(ctx, kc, mw, fakeServer, featureSet, features, profile)
}

func applyFeatureSet(ctx context.Context, kc client.Client, mw *workv1.ManifestWork, fakeServer *FakeServer, featureSet string, features []string, profile *profilev1alpha1.ManagedClusterSetProfile) error {
	logger := klog.FromContext(ctx)
	var err error
	var fsObj uiapi.FeatureSet
	if err = fakeServer.FakeClient.Get(ctx, types.NamespacedName{Name: featureSet}, &fsObj); err != nil {
		return err
	}
	model, err := GetFeatureSetValues(ctx, &fsObj, features, hub.BootstrapHelmRepositoryNamespace(), fakeServer.FakeClient)
	if err != nil {
		return err
	}

	if _, err = UpdateFeatureSetValues(ctx, fsObj.Name, fakeServer.FakeClient, model); err != nil {
		return err
	}

	// Update values based on user-provided inputs stored in the profile
	for _, f := range features {
		featureKey := getFeaturePathInValues(f)
		curValues, _, err := unstructured.NestedMap(model, "resources", featureKey, "spec", "values")
		if err != nil {
			return err
		}

		var valuesMap map[string]interface{}
		if profile.Spec.Features[f].Values != nil {
			err = json.Unmarshal(profile.Spec.Features[f].Values.Raw, &valuesMap)
			if err != nil {
				return err
			}
		}
		finalValues := values.MergeMaps(curValues, valuesMap)
		if err = unstructured.SetNestedMap(model, finalValues, "resources", featureKey, "spec", "values"); err != nil {
			return err
		}
	}

	reg := NewVirtualRegistry(fakeServer.FakeClient)
	err = applyCRDs(fakeServer.FakeRestConfig, reg, fsObj.Spec.Chart)
	if err != nil {
		return err
	}

	logger.Info("Applying Resource")

	konfig := clientcmd.NewNonInteractiveClientConfig(*fakeServer.FakeApiConfig, fakeServer.FakeApiConfig.CurrentContext, &clientcmd.ConfigOverrides{}, nil)
	getter, err := utils.GetClientGetter(konfig)
	if err != nil {
		return err
	}

	f := cmdutil.NewFactory(getter)
	_, err = applyResource(f, reg, &fsObj.Spec.Chart, model)
	if err != nil {
		return fmt.Errorf("error while applying resource: %v", err)
	}

	if err = waitForReleaseToBeCreated(fakeServer.FakeClient, features); err != nil {
		return err
	}

	if err = updateManifestWork(ctx, fakeServer, kc, mw); err != nil {
		return err
	}
	return nil
}

func removeHRFomManifestWork(ctx context.Context, kc client.Client, mw *workv1.ManifestWork, features []string) error {
	logger := klog.FromContext(ctx)
	var updatedManifests []workv1.Manifest

	for _, manifest := range mw.Spec.Workload.Manifests {
		unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&manifest)
		if err != nil {
			return err
		}

		unstructuredResource := unstructured.Unstructured{Object: unstructuredObj}
		kind, found, err := unstructured.NestedString(unstructuredResource.Object, "kind")
		if err != nil || !found {
			return err
		}

		if kind == "HelmRelease" {
			name, found, err := unstructured.NestedString(unstructuredResource.Object, "metadata", "name")
			if err != nil || !found {
				return err
			}

			if !strings.Contains(features, name) {
				logger.Info(fmt.Sprintf("Removing HelmRelease manifest with name: %s", name))
				continue
			}
		}
		updatedManifests = append(updatedManifests, manifest)
	}

	if reflect.DeepEqual(mw.Spec.Workload.Manifests, updatedManifests) {
		return nil
	}

	mw.Spec.Workload.Manifests = updatedManifests

	if len(updatedManifests) == 0 {
		if err := kc.Delete(ctx, mw); err != nil {
			return err
		}
	}

	_, err := cu.CreateOrPatch(ctx, kc, mw, func(obj client.Object, createOp bool) client.Object {
		in := obj.(*workv1.ManifestWork)
		in.Spec = mw.Spec
		return in
	})
	if err != nil {
		return err
	}

	logger.Info(fmt.Sprintf("ManifestWork %s created or updated in namespace %s", mw.Name, mw.Namespace))
	return nil
}

func applyResource(f cmdutil.Factory, reg repo.IRegistry, chartRef *releasesapi.ChartSourceRef, model map[string]interface{}) (*release.Release, error) {
	return handler.ApplyResource(f, reg, *chartRef, model, true)
}

func applyCRDs(restConfig *rest.Config, reg repo.IRegistry, chartRef releasesapi.ChartSourceRef) error {
	chart, err := reg.GetChart(chartRef)
	if err != nil {
		return err
	}

	chartCRDs := chart.CRDObjects()
	if len(chartCRDs) == 0 {
		return nil
	}

	kc, err := crd_cs.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	hc, err := rest.HTTPClientFor(restConfig)
	if err != nil {
		return err
	}
	mapper, err := apiutil.NewDynamicRESTMapper(restConfig, hc)
	if err != nil {
		return err
	}

	for i := range chartCRDs {
		var crd crdv1.CustomResourceDefinition
		err = yaml.Unmarshal(chartCRDs[i].File.Data, &crd)
		if err != nil {
			return err
		}

		_, err = mapper.RESTMapping(schema.GroupKind{Group: crd.Spec.Group, Kind: crd.Spec.Names.Kind}, crd.Spec.Versions[0].Name)
		if meta.IsNoMatchError(err) {
			err = crd_util.RegisterCRDs(kc, []*crd_util.CustomResourceDefinition{
				{V1: &crd},
			})
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

func GetFeatureSetValues(ctx context.Context, fs *uiapi.FeatureSet, features []string, releaseNamespace string, kc client.Client) (map[string]interface{}, error) {
	chart, curValues, err := getFeatureSetChartRef(kc, fs, releaseNamespace)
	if err != nil {
		return nil, err
	}

	for i := range features {
		feature := &uiapi.Feature{}
		err = kc.Get(ctx, client.ObjectKey{Name: features[i]}, feature, &client.GetOptions{})
		if err != nil {
			return nil, err
		}
		status, err := calculateFeatureStatus(ctx, kc, feature)
		if err != nil {
			return nil, err
		} else if status.enabled && !status.managed {
			// don't install HelmRelease if the feature is self-managed
			continue
		}

		if err = generateHelmReleaseForFeature(kc, fs, feature, chart, curValues); err != nil {
			return nil, err
		}
	}
	return curValues, nil
}

func getFeatureSetChartRef(kc client.Client, fs *uiapi.FeatureSet, releaseNamespace string) (*repo.ChartExtended, map[string]interface{}, error) {
	reg := NewVirtualRegistry(kc)
	chart, err := reg.GetChart(fs.Spec.Chart)
	if err != nil {
		return nil, nil, err
	}

	curValues, err := utils.DeepCopyMap(chart.Values)
	if err != nil {
		return nil, nil, err
	}
	curValues["resources"] = make(map[string]interface{})

	err = setReleaseNameAndNamespace(fs, releaseNamespace, curValues)
	if err != nil {
		return nil, nil, err
	}
	return chart, curValues, nil
}

func setReleaseNameAndNamespace(fs *uiapi.FeatureSet, namespace string, values map[string]interface{}) error {
	err := unstructured.SetNestedField(values, fs.Name, "metadata", "release", "name")
	if err != nil {
		return err
	}
	return unstructured.SetNestedField(values, namespace, "metadata", "release", "namespace")
}

func calculateFeatureStatus(ctx context.Context, client client.Client, feature *uiapi.Feature) (*featureStatus, error) {
	status := &featureStatus{}
	if ptr.Deref(feature.Status.Enabled, false) {
		return &featureStatus{
			enabled: ptr.Deref(feature.Status.Enabled, false),
			managed: ptr.Deref(feature.Status.Managed, false),
			ready:   ptr.Deref(feature.Status.Ready, false),
		}, nil
	}

	found, err := checkHelmReleaseExistence(ctx, client, feature)
	if err != nil {
		return nil, err
	} else if found {
		return &featureStatus{
			enabled: true,
			managed: true,
		}, nil
	}

	found, err = checkRequiredResourcesExistence(ctx, client, feature)
	if err != nil {
		return nil, err
	} else if !found {
		return status, nil
	}

	status.enabled, err = checkRequiredWorkloadExistence(ctx, client, feature)
	return status, err
}

func checkHelmReleaseExistence(ctx context.Context, kc client.Client, feature *uiapi.Feature) (bool, error) {
	selector := labels.SelectorFromSet(map[string]string{
		kmeta.ComponentLabelKey: feature.Name,
		kmeta.PartOfLabelKey:    feature.Spec.FeatureSet,
	})

	releases := &fluxhelm.HelmReleaseList{}
	if err := kc.List(ctx, releases, &client.ListOptions{LabelSelector: selector}); err != nil {
		return false, err
	}

	return len(releases.Items) > 0, nil
}

func checkRequiredResourcesExistence(ctx context.Context, kc client.Client, feature *uiapi.Feature) (bool, error) {
	for _, gvk := range feature.Spec.ReadinessChecks.Resources {
		objList := unstructured.UnstructuredList{}
		objList.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    gvk.Kind,
		})
		if err := kc.List(ctx, &objList, &client.ListOptions{Limit: 1}); err != nil {
			return false, ignoreNotFoundError(err)
		}
	}
	return true, nil
}

func checkRequiredWorkloadExistence(ctx context.Context, kc client.Client, feature *uiapi.Feature) (bool, error) {
	for _, w := range feature.Spec.ReadinessChecks.Workloads {
		objList := unstructured.UnstructuredList{}
		objList.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   w.Group,
			Version: w.Version,
			Kind:    w.Kind,
		})
		selector := labels.SelectorFromSet(w.Selector)
		if err := kc.List(ctx, &objList, &client.ListOptions{Limit: 1, LabelSelector: selector}); err != nil {
			return false, ignoreNotFoundError(err)
		}
		if len(objList.Items) == 0 {
			return false, nil
		}
	}

	return len(feature.Spec.ReadinessChecks.Workloads) > 0, nil
}

func ignoreNotFoundError(err error) error {
	if meta.IsNoMatchError(err) || errors.IsNotFound(err) {
		return nil
	}
	return err
}

func generateHelmReleaseForFeature(kc client.Client, fs *uiapi.FeatureSet, feature *uiapi.Feature, chart *repo.ChartExtended, curValues map[string]interface{}) error {
	featureKey := getFeaturePathInValues(feature.Name)
	featureDefaultValue, _, err := unstructured.NestedMap(chart.Values, "resources", featureKey)
	if err != nil {
		return err
	}

	if err = unstructured.SetNestedMap(curValues, featureDefaultValue, "resources", featureKey); err != nil {
		return err
	}

	if err = setLabelsToHelmReleases(fs, feature, curValues); err != nil {
		return err
	}

	err = editor.SetChartInfo(kc, feature, featureKey, curValues)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	return updateHelmReleaseDependency(context.Background(), kc, curValues, feature)
}

func getFeaturePathInValues(feature string) string {
	return fmt.Sprintf("helmToolkitFluxcdIoHelmRelease_%s", kstr.ReplaceAll(feature, "-", "_"))
}

func setLabelsToHelmReleases(fs *uiapi.FeatureSet, feature *uiapi.Feature, values map[string]interface{}) error {
	label := map[string]interface{}{
		kmeta.ComponentLabelKey: feature.Name,
		kmeta.PartOfLabelKey:    fs.Name,
	}
	featureKey := getFeaturePathInValues(feature.Name)
	return unstructured.SetNestedField(values, label, "resources", featureKey, "metadata", "labels")
}

func updateHelmReleaseDependency(ctx context.Context, kc client.Client, values map[string]any, feature *uiapi.Feature) (err error) {
	featureKey := getFeaturePathInValues(feature.Name)
	if len(feature.Spec.Requirements.Features) > 0 {
		dependsOn := make([]kmapi.ObjectReference, 0, len(feature.Spec.Requirements.Features))
		for _, featureName := range feature.Spec.Requirements.Features {
			reqFeature := uiapi.Feature{}
			err = kc.Get(ctx, client.ObjectKey{Name: featureName}, &reqFeature, &client.GetOptions{})
			if err != nil {
				return err
			}

			if reqFeature.Name == "license-proxyserver" {
				continue
			}

			// if required feature enabled but not managed by UI
			// don't add this feature in HelmRelease dependsOn field
			status, err := calculateFeatureStatus(ctx, kc, &reqFeature)
			if err != nil {
				return err
			}

			if !status.enabled || status.managed {
				dependsOn = append(dependsOn, kmapi.ObjectReference{
					Name: reqFeature.Name,
				})
			}
		}

		var dependsOnReleases any
		if err = utils.Copy(dependsOn, &dependsOnReleases); err != nil {
			return err
		}
		err = unstructured.SetNestedField(values, dependsOnReleases, "resources", featureKey, "spec", "dependsOn")
	} else {
		unstructured.RemoveNestedField(values, "resources", featureKey, "spec", "dependsOn")
	}
	return
}
