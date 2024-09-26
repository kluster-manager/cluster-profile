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
	"encoding/json"

	"github.com/kluster-manager/cluster-profile/pkg/common"

	"github.com/pkg/errors"
	kmapi "kmodules.xyz/client-go/api/v1"
	v1 "open-cluster-management.io/api/cluster/v1"
)

func getClusterMetadata(cluster v1.ManagedCluster) (kmapi.ClusterInfo, error) {
	var clusterInfo kmapi.ClusterInfo
	for _, claim := range cluster.Status.ClusterClaims {
		if claim.Name == common.ClusterClaimClusterInfo {
			err := json.Unmarshal([]byte(claim.Value), &clusterInfo)
			return clusterInfo, err
		}
	}

	return clusterInfo, errors.New("cluster info not found")
}