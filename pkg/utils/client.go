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

package utils

import (
	fluxhelm "github.com/fluxcd/helm-controller/api/v2"
	fluxsrc "github.com/fluxcd/source-controller/api/v1"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	clientsetscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdutil "kmodules.xyz/client-go/tools/clientcmd"
	uiapi "kmodules.xyz/resource-metadata/apis/ui/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"
	workv1alpha1 "open-cluster-management.io/api/work/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	driversapi "x-helm.dev/apimachinery/apis/drivers/v1alpha1"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientsetscheme.AddToScheme(scheme))
	utilruntime.Must(core.AddToScheme(scheme))
	utilruntime.Must(fluxhelm.AddToScheme(scheme))
	utilruntime.Must(fluxsrc.AddToScheme(scheme))
	utilruntime.Must(metav1.AddMetaToScheme(scheme))
	utilruntime.Must(clusterv1.Install(scheme))
	utilruntime.Must(rbac.AddToScheme(scheme))
	utilruntime.Must(uiapi.AddToScheme(scheme))
	utilruntime.Must(workv1.Install(scheme))
	utilruntime.Must(workv1alpha1.Install(scheme))
	utilruntime.Must(driversapi.AddToScheme(scheme))
}

func GetNewRuntimeClient(restConfig *rest.Config) (client.Client, error) {
	hc, err := rest.HTTPClientFor(restConfig)
	if err != nil {
		return nil, err
	}
	mapper, err := apiutil.NewDynamicRESTMapper(restConfig, hc)
	if err != nil {
		return nil, err
	}

	return client.New(restConfig, client.Options{
		Scheme: scheme,
		Mapper: mapper,
		//WarningHandler: client.WarningHandlerOptions{
		//	SuppressWarnings:   true,
		//	AllowDuplicateLogs: false,
		//},
	})
}

func GetClientGetter(clientConfig clientcmd.ClientConfig) (genericclioptions.RESTClientGetter, error) {
	rawConfig, err := clientConfig.RawConfig()
	if err != nil {
		return nil, err
	}

	return clientcmdutil.NewClientGetter(&rawConfig), nil
}
