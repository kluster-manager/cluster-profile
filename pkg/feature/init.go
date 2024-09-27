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
	"encoding/json"

	profilev1alpha1 "github.com/kluster-manager/cluster-profile/apis/profile/v1alpha1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kmapi "kmodules.xyz/client-go/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func initializeServer(kc client.Client, fakeServer *FakeServer, profile *profilev1alpha1.ManagedClusterSetProfile, clusterMetadata kmapi.ClusterInfo) (map[string]interface{}, error) {
	overrides := make(map[string]interface{})
	if profile.Spec.Features["opscenter-features"].Values != nil {
		if err := json.Unmarshal(profile.Spec.Features["opscenter-features"].Values.Raw, &overrides); err != nil {
			return nil, err
		}
	}

	overrides["clusterMetadata"] = map[string]interface{}{
		"uid":  clusterMetadata.UID,
		"name": clusterMetadata.Name,
	}

	if clusterMetadata.CAPI.Provider != "" {
		if err := unstructured.SetNestedField(overrides, clusterMetadata.CAPI.Provider, "clusterMetadata", "capi", "provider"); err != nil {
			return nil, err
		}
	}
	if clusterMetadata.CAPI.Namespace != "" {
		if err := unstructured.SetNestedField(overrides, clusterMetadata.CAPI.Namespace, "clusterMetadata", "capi", "namespace"); err != nil {
			return nil, err
		}
	}
	if clusterMetadata.CAPI.ClusterName != "" {
		if err := unstructured.SetNestedField(overrides, clusterMetadata.CAPI.Namespace, "clusterMetadata", "capi", "clusterName"); err != nil {
			return nil, err
		}
	}
	if len(clusterMetadata.ClusterManagers) > 0 {
		if err := unstructured.SetNestedStringSlice(overrides, clusterMetadata.ClusterManagers, "clusterMetadata", "clusterManagers"); err != nil {
			return nil, err
		}
	}

	overrideValues, err := json.Marshal(overrides)
	if err != nil {
		return nil, err
	}

	if err := installOpscenterFeatures(kc, overrideValues, fakeServer); err != nil {
		return nil, err
	}
	return overrides, nil
}
