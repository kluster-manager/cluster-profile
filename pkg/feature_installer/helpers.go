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
	"fmt"
	golog "log"
	"net/http"
	"os"
	"time"

	profilev1alpha1 "github.com/kluster-manager/cluster-profile/apis/profile/v1alpha1"
	"github.com/kluster-manager/cluster-profile/pkg/common"
	"github.com/kluster-manager/cluster-profile/pkg/utils"

	fluxhelm "github.com/fluxcd/helm-controller/api/v2"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"
	cu "kmodules.xyz/client-go/client"
	"kmodules.xyz/client-go/tools/clientcmd"
	clientcmdutil "kmodules.xyz/client-go/tools/clientcmd"
	"kmodules.xyz/fake-apiserver/pkg"
	uiapi "kmodules.xyz/resource-metadata/apis/ui/v1alpha1"
	"kubepack.dev/lib-helm/pkg/repo"
	workv1 "open-cluster-management.io/api/work/v1"
	work "open-cluster-management.io/api/work/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	releasesapi "x-helm.dev/apimachinery/apis/releases/v1alpha1"
)

const (
	pullInterval = 2 * time.Second
	waitTimeout  = 10 * time.Minute
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

func startFakeApiServerAndApplyBaseManifestWorkReplicaSets(ctx context.Context, kc client.Client) (*FakeServer, error) {
	var fakeServer FakeServer
	var err error
	fakeServer.FakeSrv, fakeServer.FakeS, fakeServer.FakeRestConfig, fakeServer.FakeApiConfig = initializeFakeServer()
	fakeServer.FakeClient, err = utils.GetNewRuntimeClient(fakeServer.FakeRestConfig)
	if err != nil {
		return nil, err
	}

	var bootstrapMWRS work.ManifestWorkReplicaSet
	if err = kc.Get(ctx, client.ObjectKey{Name: "ace-bootstrap", Namespace: "open-cluster-management-addon"}, &bootstrapMWRS); err != nil {
		return nil, err
	}

	if err = applyManifestWorkReplicaSet(ctx, fakeServer.FakeClient, bootstrapMWRS); err != nil {
		return nil, err
	}

	var namespaceMWRS work.ManifestWorkReplicaSet
	if err = kc.Get(ctx, client.ObjectKey{Name: "ace-namespace", Namespace: "open-cluster-management-addon"}, &namespaceMWRS); err != nil {
		return nil, err
	}

	if err = applyManifestWorkReplicaSet(ctx, fakeServer.FakeClient, namespaceMWRS); err != nil {
		return nil, err
	}

	return &fakeServer, nil
}

func applyManifestWorkReplicaSet(ctx context.Context, kc client.Client, workReplicaSet work.ManifestWorkReplicaSet) error {
	for _, m := range workReplicaSet.Spec.ManifestWorkTemplate.Workload.Manifests {
		data, err := m.MarshalJSON()
		if err != nil {
			return err
		}

		if err = applyManifest(ctx, kc, data); err != nil {
			return err
		}
	}
	return nil
}

func applyManifestWork(ctx context.Context, kc client.Client, workReplicaSet workv1.ManifestWork) error {
	for _, m := range workReplicaSet.Spec.Workload.Manifests {
		data, err := m.MarshalJSON()
		if err != nil {
			return err
		}

		if err = applyManifest(ctx, kc, data); err != nil {
			return err
		}
	}
	return nil
}

func applyManifest(ctx context.Context, kc client.Client, data []byte) error {
	req, err := runtime.Decode(unstructured.UnstructuredJSONScheme, data)
	if err != nil {
		return err
	}

	return kc.Create(ctx, req.(*unstructured.Unstructured))
}

func diff(features1, features2 []string) []string {
	features2Map := make(map[string]struct{})
	for _, feature := range features2 {
		features2Map[feature] = struct{}{}
	}

	var missingFeatures []string
	for _, feature := range features1 {
		if _, found := features2Map[feature]; !found {
			missingFeatures = append(missingFeatures, feature)
		}
	}
	return missingFeatures
}

func waitForReleaseToBeCreated(kc client.Client, name []string) error {
	rel := fluxhelm.HelmRelease{}
	return wait.PollUntilContextTimeout(context.Background(), pullInterval, waitTimeout, true, func(ctx context.Context) (done bool, err error) {
		ok := true
		for _, featureName := range name {
			err = kc.Get(ctx, types.NamespacedName{Name: featureName, Namespace: "kubeops"}, &rel)
			if err != nil && !errors.IsNotFound(err) {
				return false, err
			} else if err != nil && errors.IsNotFound(err) {
				ok = false
				return false, nil
			}
		}

		if ok {
			return true, nil
		}

		return false, nil
	})
}

func updateManifestWork(ctx context.Context, fakeServer *FakeServer, kc client.Client, mw *workv1.ManifestWork) error {
	logger := log.FromContext(ctx)
	// fake-apiserver shutdown
	if err := fakeServer.FakeSrv.Shutdown(ctx); err != nil {
		return err
	}
	logger.Info("Server Exited Properly")

	current, _ := fakeServer.FakeS.Export()
	for _, item := range current {
		ww := workv1.Manifest{}
		metadata := item.Object["metadata"].(map[string]interface{})
		delete(metadata, "resourceVersion")

		err := utils.Copy(item.Object, &ww)
		if err != nil {
			return err
		}
		mw.Spec.Workload.Manifests = append(mw.Spec.Workload.Manifests, ww)
	}

	_, err := cu.CreateOrPatch(ctx, kc, mw, func(obj client.Object, createOp bool) client.Object {
		in := obj.(*workv1.ManifestWork)
		in = mw
		return in
	})
	if err != nil {
		return err
	}

	logger.Info(fmt.Sprintf("ManifestWork %s created or updated in namespace %s", mw.Name, mw.Namespace))
	return nil
}

func UpdateFeatureSetValues(ctx context.Context, fs string, kc client.Client, values map[string]any) ([]string, error) {
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
			if err = updateHelmReleaseDependency(ctx, kc, values, &feature, true); err != nil {
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

func getDefaultValues(reg repo.IRegistry, chartRef releasesapi.ChartSourceRef) (map[string]interface{}, error) {
	chart, err := reg.GetChart(chartRef)
	if err != nil {
		return nil, err
	}
	return chart.Values, nil
}

func getHelmReleaseStatus(apiConfig *api.Config) (*release.Release, error) {
	clientGetter := clientcmdutil.NewClientGetter(apiConfig)
	cfg := new(action.Configuration)
	err := cfg.Init(clientGetter, "kubeops", "secret", golog.New(os.Stderr, "", golog.Flags()).Printf)
	if err != nil {
		return nil, fmt.Errorf("helm config initialization: %v", err)
	}

	return action.NewStatus(cfg).Run("opscenter-features")
}

func getReleaseStatus(apiConfig *api.Config) (*release.Release, error) {
	status, err := getHelmReleaseStatus(apiConfig)
	if err != nil {
		return nil, err
	}

	return status, nil
}

func getAppliedFeatureList(ctx context.Context, kc client.Client, profileBinding *profilev1alpha1.ManagedClusterProfileBinding) ([]string, error) {
	var err error
	appliedFeatures := make([]string, 0)

	var mwList workv1.ManifestWorkList
	err = kc.List(ctx, &mwList, &client.ListOptions{
		Namespace: profileBinding.Namespace,
		LabelSelector: labels.SelectorFromSet(map[string]string{
			common.LabelAceFeatureSet: "true",
		}),
	})

	if err != nil {
		return nil, err
	}

	for _, manifestWork := range mwList.Items {
		for _, manifest := range manifestWork.Spec.Workload.Manifests {
			unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&manifest)
			if err != nil {
				return nil, err
			}

			unstructuredResource := unstructured.Unstructured{Object: unstructuredObj}
			kind, found, err := unstructured.NestedString(unstructuredResource.Object, "kind")
			if err != nil || !found {
				return nil, err
			}

			if kind == "HelmRelease" {
				name, found, err := unstructured.NestedString(unstructuredResource.Object, "metadata", "name")
				if err != nil || !found {
					return nil, err
				}
				appliedFeatures = append(appliedFeatures, name)
			}
		}
	}
	return appliedFeatures, nil
}
