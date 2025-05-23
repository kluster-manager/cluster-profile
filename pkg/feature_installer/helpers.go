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
	"net/http"
	"sort"
	"time"

	profilev1alpha1 "github.com/kluster-manager/cluster-profile/apis/profile/v1alpha1"
	"github.com/kluster-manager/cluster-profile/pkg/common"
	"github.com/kluster-manager/cluster-profile/pkg/utils"

	fluxhelm "github.com/fluxcd/helm-controller/api/v2"
	go_str "gomodules.xyz/x/strings"
	crdv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"
	kmapi "kmodules.xyz/client-go/api/v1"
	"kmodules.xyz/client-go/apiextensions"
	cu "kmodules.xyz/client-go/client"
	"kmodules.xyz/client-go/tools/clientcmd"
	"kmodules.xyz/fake-apiserver/pkg"
	"kmodules.xyz/fake-apiserver/pkg/resources"
	uiapi "kmodules.xyz/resource-metadata/apis/ui/v1alpha1"
	"kmodules.xyz/resource-metadata/hub"
	"kubepack.dev/lib-helm/pkg/repo"
	v1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"
	work "open-cluster-management.io/api/work/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
	driversapi "x-helm.dev/apimachinery/apis/drivers/v1alpha1"
	releasesapi "x-helm.dev/apimachinery/apis/releases/v1alpha1"
)

const (
	pullInterval = 2 * time.Second
	waitTimeout  = 10 * time.Minute

	// addonNamespace must be hardcoded as it is also used from b3
	addonNamespace    = "open-cluster-management-addon"
	mwrsNameNamespace = "ace-namespace"
	mwrsNameBootstrap = "ace-bootstrap"

	fakeProjectCRDName = "projects.project.openshift.io"
)

type FakeServer struct {
	FakeSrv        *http.Server
	FakeS          *pkg.Server
	FakeApiConfig  *api.Config
	FakeRestConfig *rest.Config
	FakeClient     client.Client
}

func GetAPIGroups() []string {
	return []string{
		"addon.open-cluster-management.io",
		"appcatalog.appscode.com",
		"auditor.appscode.com",
		"autoscaling.kubedb.com",
		"aws.kubeform.com",
		"azure.kubeform.com",
		"catalog.kubedb.com",
		"catalog.kubevault.com",
		"catalog.kubeware.dev",
		"charts.x-helm.dev",
		"cluster.open-cluster-management.io",
		"dashboard.kubedb.com",
		"drivers.x-helm.dev",
		"external-dns.appscode.com",
		"falco.appscode.com",
		"gcp.kubeform.com",
		"helm.toolkit.fluxcd.io",
		"kubedb.com",
		"kubevault.com",
		"monitoring.coreos.com",
		"openviz.dev",
		"operator.open-cluster-management.io",
		"ops.kubedb.com",
		"ops.kubevault.com",
		"policy.kubevault.com",
		"postgres.kubedb.com",
		"products.x-helm.dev",
		"repositories.stash.appscode.com",
		"schema.kubedb.com",
		"secrets.crossplane.io",
		"source.toolkit.fluxcd.io",
		"stash.appscode.com",
		"status.gatekeeper.sh",
		"supervisor.appscode.com",
		"ui.k8s.appscode.com",
		"ui.kubedb.com",
		"ui.stash.appscode.com",
		"work.open-cluster-management.io",
		"project.openshift.io",
	}
}

func initializeFakeServer() (*http.Server, *pkg.Server, *rest.Config, *api.Config) {
	apiGroups := GetAPIGroups()
	opts := pkg.NewOptions(apiGroups...)

	s := pkg.NewServer(opts)
	srv, restcfg, err := s.Run()
	if err != nil {
		klog.Fatalln(err)
	}
	klog.Infoln("Server Started")

	kubecfg, err := clientcmd.BuildKubeConfig(restcfg, metav1.NamespaceDefault)
	if err != nil {
		klog.Fatalln(err)
	}

	return srv, s, restcfg, kubecfg
}

func StartFakeApiServerAndApplyBaseManifestWorkReplicaSets(ctx context.Context, kc client.Client) (*FakeServer, error) {
	var fakeServer FakeServer
	var err error
	fakeServer.FakeSrv, fakeServer.FakeS, fakeServer.FakeRestConfig, fakeServer.FakeApiConfig = initializeFakeServer()
	fakeServer.FakeClient, err = utils.GetNewRuntimeClient(fakeServer.FakeRestConfig)
	if err != nil {
		return nil, err
	}

	var mwrsNamespace work.ManifestWorkReplicaSet
	if err = kc.Get(ctx, client.ObjectKey{Name: mwrsNameNamespace, Namespace: addonNamespace}, &mwrsNamespace); err != nil {
		return nil, err
	}

	if err = applyManifestWorkReplicaSet(ctx, fakeServer.FakeClient, mwrsNamespace); err != nil {
		return nil, err
	}

	var mwrsBootstrap work.ManifestWorkReplicaSet
	if err = kc.Get(ctx, client.ObjectKey{Name: mwrsNameBootstrap, Namespace: addonNamespace}, &mwrsBootstrap); err != nil {
		return nil, err
	}

	if err = applyManifestWorkReplicaSet(ctx, fakeServer.FakeClient, mwrsBootstrap); err != nil {
		return nil, err
	}

	return &fakeServer, nil
}

func applyManifestWorkReplicaSet(ctx context.Context, kc client.Client, mwrs work.ManifestWorkReplicaSet) error {
	for _, m := range mwrs.Spec.ManifestWorkTemplate.Workload.Manifests {
		if err := applyRawExtension(ctx, kc, m.RawExtension); err != nil {
			return err
		}
	}
	return nil
}

func applyRawExtension(ctx context.Context, kc client.Client, rawExt runtime.RawExtension) error {
	u := &unstructured.Unstructured{}
	err := json.Unmarshal(rawExt.Raw, u)
	if err != nil {
		return err
	}

	err = kc.Create(ctx, u)
	if err != nil {
		return err
	}

	return nil
}

func waitForReleaseToBeCreated(kc client.Client, name []string) error {
	rel := fluxhelm.HelmRelease{}
	return wait.PollUntilContextTimeout(context.Background(), pullInterval, waitTimeout, true, func(ctx context.Context) (done bool, err error) {
		for _, featureName := range name {
			err = kc.Get(ctx, types.NamespacedName{Name: featureName, Namespace: hub.BootstrapHelmRepositoryNamespace()}, &rel)
			if err != nil && !errors.IsNotFound(err) {
				return false, err
			} else if err != nil && errors.IsNotFound(err) {
				return false, nil
			}
		}

		return true, nil
	})
}

func updateManifestWork(ctx context.Context, fakeServer *FakeServer, kc client.Client, mw *workv1.ManifestWork, profile *profilev1alpha1.ManagedClusterSetProfile) error {
	logger := log.FromContext(ctx)
	// fake-apiserver shutdown
	if err := fakeServer.FakeSrv.Shutdown(ctx); err != nil {
		return err
	}
	logger.Info("Server Exited Properly")

	current, _ := fakeServer.FakeS.Export()
	mw.Spec.Workload.Manifests = nil
	mw.Spec.ManifestConfigs = nil

	configOptions := make([]workv1.ManifestConfigOption, 0)
	for _, item := range current {
		m := workv1.Manifest{}
		kind, name, ns, err := GetKindNameNamespace(item.Object)
		if err != nil {
			return err
		}

		if (kind == "CustomResourceDefinition" && (name == "appreleases.drivers.x-helm.dev" || name == fakeProjectCRDName)) ||
			(kind == driversapi.ResourceKindAppRelease && name == mw.Name) {
			continue
		}

		metadata := item.Object["metadata"].(map[string]interface{})
		delete(metadata, "resourceVersion")

		if err = utils.Copy(item.Object, &m); err != nil {
			return err
		}

		mw.Spec.Workload.Manifests = append(mw.Spec.Workload.Manifests, m)

		configOptions = append(configOptions, workv1.ManifestConfigOption{
			ResourceIdentifier: workv1.ResourceIdentifier{
				Group:     fluxhelm.GroupVersion.Group,
				Resource:  "helmreleases",
				Name:      name,
				Namespace: ns,
			},
			FeedbackRules: []workv1.FeedbackRule{
				{
					Type: workv1.JSONPathsType,
					JsonPaths: []workv1.JsonPath{
						{
							Name: "Ready",
							Path: `.status.conditions[?(@.type=="Ready")].status`,
						},
						{
							Name: "Released",
							Path: `.status.conditions[?(@.type=="Released")].status`,
						},
					},
				},
			},
		})
	}

	mw.Spec.ManifestConfigs = configOptions
	_, err := cu.CreateOrPatch(ctx, kc, mw, func(obj client.Object, createOp bool) client.Object {
		in := obj.(*workv1.ManifestWork)
		if in.Labels == nil {
			in.Labels = map[string]string{}
		}
		in.Labels[kmapi.ClusterProfileLabel] = profile.Name
		in.Labels[common.LabelAceFeatureSet] = "true"
		in.Spec = mw.Spec
		return in
	})
	if err != nil {
		return err
	}

	logger.Info(fmt.Sprintf("ManifestWork %s created or updated in namespace %s", mw.Name, mw.Namespace))
	return nil
}

func UpdateFeatureSetValues(ctx context.Context, fs string, kc client.Client, values map[string]any, mc *v1.ManagedCluster) ([]string, error) {
	featureList, err := GetFeatures(ctx, kc, fs)
	if err != nil {
		return nil, err
	}

	features := make([]string, 0, len(featureList.Items))
	for idx := range featureList.Items {
		feature := featureList.Items[idx]
		if ok, err := featureToBeEnabled(feature.Name, values); err != nil {
			return nil, err
		} else if ok {
			features = append(features, feature.Name)
			if err = updateHelmReleaseDependency(ctx, kc, values, &feature, mc); err != nil {
				return nil, err
			}
		}
	}
	return features, nil
}

func GetFeatures(ctx context.Context, kc client.Client, fs string) (uiapi.FeatureList, error) {
	features := uiapi.FeatureList{}
	err := kc.List(ctx, &features, &client.ListOptions{
		LabelSelector: uiapi.Feature{}.FormatLabels(fs),
	})
	if err != nil {
		return uiapi.FeatureList{}, err
	}
	return features, nil
}

func featureToBeEnabled(feature string, values map[string]any) (bool, error) {
	_, ok, err := unstructured.NestedFieldNoCopy(values, "resources", getFeaturePathInValues(feature))
	return ok, err
}

func GetDefaultValues(reg repo.IRegistry, chartRef releasesapi.ChartSourceRef) (map[string]interface{}, error) {
	chart, err := reg.GetChart(chartRef)
	if err != nil {
		return nil, err
	}
	return chart.Values, nil
}

func GetKindNameNamespace(item map[string]interface{}) (string, string, string, error) {
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&item)
	if err != nil {
		return "", "", "", err
	}

	unstructuredResource := unstructured.Unstructured{Object: unstructuredObj}
	kind, found, err := unstructured.NestedString(unstructuredResource.Object, "kind")
	if err != nil || !found {
		return "", "", "", err
	}

	name, found, err := unstructured.NestedString(unstructuredResource.Object, "metadata", "name")
	if err != nil || !found {
		return "", "", "", err
	}

	namespace, found, _ := unstructured.NestedString(unstructuredResource.Object, "metadata", "namespace")
	if !found {
		return kind, name, "", nil
	}
	return kind, name, namespace, nil
}

func sanitizeFeatures(kc client.Client, clusterName string, features []string) ([]string, error) {
	var mc v1.ManagedCluster
	if err := kc.Get(context.Background(), client.ObjectKey{Name: clusterName}, &mc); err != nil {
		return nil, err
	}

	featuresMap, err := getFeatureStatus(&mc)
	if err != nil {
		return nil, err
	}

	exclusionGroupFeatures := make(map[string][]string) // Tracks features by their exclusion group
	var featureList uiapi.FeatureList
	if err = kc.List(context.Background(), &featureList); err != nil {
		return nil, err
	}
	for _, f := range featureList.Items {
		exclusionGroup := f.Spec.FeatureExclusionGroup
		if exclusionGroup != "" {
			// Mark the exclusion group as having an enabled feature if this feature is enabled
			if go_str.Contains(featuresMap.EnabledFeatures, f.Name) {
				exclusionGroupFeatures[exclusionGroup] = append(exclusionGroupFeatures[exclusionGroup], f.Name)
			}
		}
	}

	sort.Strings(features)
	var sanitizedFeatures []string
	for _, f := range features {
		var feature uiapi.Feature
		if err := kc.Get(context.Background(), types.NamespacedName{Name: f}, &feature); err != nil {
			return nil, err
		}

		if go_str.Contains(featuresMap.ExternallyManagedFeatures, f) || go_str.Contains(featuresMap.DisabledFeatures, f) {
			continue
		}

		exclusionGroup := feature.Spec.FeatureExclusionGroup
		// Skip if an exclusion group already has an enabled feature or features are already present
		exist, err := existInManifestWork(kc, clusterName, feature.Spec.FeatureSet, feature.Name)
		if err != nil {
			return nil, err
		}

		if exclusionGroup != "" && len(exclusionGroupFeatures[exclusionGroup]) > 0 && !exist {
			continue
		}

		// Add the feature to the final list
		sanitizedFeatures = append(sanitizedFeatures, f)
		if exclusionGroup != "" {
			exclusionGroupFeatures[exclusionGroup] = append(exclusionGroupFeatures[exclusionGroup], feature.Name)
		}
	}

	return sanitizedFeatures, nil
}

func getFeatureStatus(cluster *v1.ManagedCluster) (*kmapi.ClusterClaimFeatures, error) {
	var mp kmapi.ClusterClaimFeatures
	for _, claim := range cluster.Status.ClusterClaims {
		if claim.Name == kmapi.ClusterClaimKeyFeatures {
			yamlData := []byte(claim.Value)
			if err := yaml.Unmarshal(yamlData, &mp); err != nil {
				return nil, err
			}
			return &mp, nil
		}
	}

	return nil, pkgerr.New("features cluster claim not found")
}

func existInManifestWork(kc client.Client, clusterName, fSetName, featureName string) (bool, error) {
	var mw workv1.ManifestWork
	err := kc.Get(context.Background(), client.ObjectKey{Name: fSetName, Namespace: clusterName}, &mw)
	if err != nil && !errors.IsNotFound(err) {
		return false, err
	}

	for _, m := range mw.Spec.Workload.Manifests {
		hr := fluxhelm.HelmRelease{}
		if err = utils.Copy(m, &hr); err != nil {
			return false, err
		}
		if hr.Name == featureName {
			return true, nil
		}
	}
	return false, nil
}

func RegisterRequiredCRDs(fakeServer *FakeServer, profileBinding *profilev1alpha1.ManagedClusterProfileBinding) error {
	crds := []*apiextensions.CustomResourceDefinition{
		driversapi.AppRelease{}.CustomResourceDefinition(),
	}

	if profileBinding.Spec.Features != nil {
		if _, ok := profileBinding.Spec.Features["aceshifter"]; ok {
			crds = append(crds, fakeProjectCRD())
		}
	}

	if err := resources.RegisterCRDs(fakeServer.FakeRestConfig, crds); err != nil {
		return err
	}
	return nil
}

func fakeProjectCRD() *apiextensions.CustomResourceDefinition {
	return &apiextensions.CustomResourceDefinition{
		V1: &crdv1.CustomResourceDefinition{
			TypeMeta: metav1.TypeMeta{
				Kind:       "CustomResourceDefinition",
				APIVersion: "apiextensions.k8s.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: fakeProjectCRDName,
			},
			Spec: crdv1.CustomResourceDefinitionSpec{
				Group: "project.openshift.io",
				Scope: crdv1.NamespaceScoped,
				Names: crdv1.CustomResourceDefinitionNames{
					Plural:     "projects",
					Singular:   "project",
					Kind:       "Project",
					ShortNames: []string{"proj"},
				},
				Versions: []crdv1.CustomResourceDefinitionVersion{
					{
						Name:    "v1",
						Served:  true,
						Storage: true,
						Schema: &crdv1.CustomResourceValidation{
							OpenAPIV3Schema: &crdv1.JSONSchemaProps{
								Type: "object",
							},
						},
					},
				},
			},
		},
	}
}
