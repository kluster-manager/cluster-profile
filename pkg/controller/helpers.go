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

package controller

import (
	"errors"
	"fmt"

	profilev1alpha1 "github.com/kluster-manager/cluster-profile/apis/profile/v1alpha1"
	"github.com/kluster-manager/cluster-profile/pkg/common"

	kmapi "kmodules.xyz/client-go/api/v1"
	v1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/yaml"
)

func getClusterMetadata(cluster v1.ManagedCluster) (kmapi.ClusterInfo, error) {
	var clusterInfo kmapi.ClusterInfo
	var clusterMetadata struct {
		ClusterMetadata kmapi.ClusterInfo `yaml:"clusterMetadata"`
	}

	for _, claim := range cluster.Status.ClusterClaims {
		if claim.Name == common.ClusterClaimClusterInfo {
			yamlData := []byte(claim.Value)
			if err := yaml.Unmarshal(yamlData, &clusterMetadata); err != nil {
				return clusterInfo, err // Return error if YAML unmarshaling fails
			}
			clusterInfo = clusterMetadata.ClusterMetadata
			return clusterInfo, nil
		}
	}

	return clusterInfo, errors.New("cluster info not found")
}

func isOpscenterFeaturesExistsInProfile(profile *profilev1alpha1.ManagedClusterSetProfile) bool {
	if _, exists := profile.Spec.Features["opscenter-features"]; exists {
		return true
	}
	return false
}

func validateFeatureList(profile *profilev1alpha1.ManagedClusterSetProfile) error {
	requiredFeatures := []string{"opscenter-features", "kube-ui-server"}
	for _, f := range requiredFeatures {
		if _, found := profile.Spec.Features[f]; !found {
			return fmt.Errorf("%s not found in feature list", f)
		}
	}
	return nil
}
