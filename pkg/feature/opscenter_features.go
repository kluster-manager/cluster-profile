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

package feature

import (
	"fmt"
	"strings"
	"time"

	"github.com/kluster-manager/cluster-profile/pkg/utils"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"kmodules.xyz/resource-metadata/hub"
	"kubepack.dev/lib-helm/pkg/action"
	"kubepack.dev/lib-helm/pkg/repo"
	"kubepack.dev/lib-helm/pkg/values"
	"sigs.k8s.io/controller-runtime/pkg/client"
	releasesapi "x-helm.dev/apimachinery/apis/releases/v1alpha1"
)

func installOpscenterFeatures(kc client.Client, overrideValues []byte, fakeServer *FakeServer) error {
	chartRef := releasesapi.ChartSourceRef{
		Name:      hub.ChartOpscenterFeatures,
		Version:   "",
		SourceRef: hub.BootstrapHelmRepository(kc),
	}

	deployOpts := &action.DeployOptions{
		ChartSourceFlatRef: releasesapi.ChartSourceFlatRef{
			Name:            chartRef.Name,
			Version:         chartRef.Version,
			SourceAPIGroup:  chartRef.SourceRef.APIGroup,
			SourceKind:      chartRef.SourceRef.Kind,
			SourceNamespace: chartRef.SourceRef.Namespace,
			SourceName:      chartRef.SourceRef.Name,
		},
		Options: values.Options{
			ValueBytes: [][]byte{
				overrideValues,
			},
		},
		Timeout:                  time.Minute * 10,
		Namespace:                hub.BootstrapHelmRepositoryNamespace(),
		ReleaseName:              hub.ChartOpscenterFeatures,
		Description:              "Required for Cluster UI, KubeDB UI, Grafana Server",
		CreateNamespace:          false,
		ReuseValues:              true,
		DisableOpenAPIValidation: true,
	}

	return installChart(hub.ChartOpscenterFeatures, hub.BootstrapHelmRepositoryNamespace(), fakeServer, chartRef, deployOpts)
}

func installChart(name, namespace string, fakeServer *FakeServer, chartRef releasesapi.ChartSourceRef, deployOpts *action.DeployOptions) error {
	err := applyCRDs(fakeServer.FakeRestConfig, NewVirtualRegistry(fakeServer.FakeClient), chartRef)
	if err != nil {
		return err
	}
	if err = DeployRelease(fakeServer.FakeApiConfig, deployOpts); err != nil {
		return err
	}
	return err
}

func DeployRelease(apiConfig *api.Config, deployOpts *action.DeployOptions) error {
	konfig := clientcmd.NewNonInteractiveClientConfig(*apiConfig, apiConfig.CurrentContext, &clientcmd.ConfigOverrides{}, nil)

	clientGetter, err := utils.GetClientGetter(konfig)
	if err != nil {
		return err
	}
	if err != nil {
		fmt.Println(err)
		return err
	}
	// configuration for upgrade/installer
	cfg := new(action.Configuration)
	err = cfg.Init(clientGetter, "kubeops", strings.ToLower("secret"))
	if err != nil {
		return fmt.Errorf("helm config initialization: %v", err)
	}

	cc, err := action.NewUncachedClient(clientGetter)
	if err != nil {
		return fmt.Errorf("kube client initialization: %v", err)
	}
	reg := repo.NewRegistry(cc, DefaultCache)

	deploy := action.NewDeployerForConfig(cfg).
		WithRegistry(reg).
		WithOptions(*deployOpts)
	_, err = deploy.Run()
	if err != nil {
		fmt.Println(err)
	}
	return err
}
