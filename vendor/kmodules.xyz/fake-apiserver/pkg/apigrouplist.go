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

package pkg

import (
	"net/http"
	"sort"

	"kmodules.xyz/apiversion"
	"kmodules.xyz/resource-metadata/apis/meta/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
)

func (s *Server) APIGroupList(w http.ResponseWriter, r *http.Request) {
	resp := metav1.APIGroupList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "APIGroupList",
		},
	}

	/*
		{
		  "kind": "APIGroupList",
		  "apiVersion": "v1",
		  "groups": [
		    {
		      "name": "apiregistration.k8s.io",
		      "versions": [
		        {
		          "groupVersion": "apiregistration.k8s.io/v1",
		          "version": "v1"
		        }
		      ],
		      "preferredVersion": {
		        "groupVersion": "apiregistration.k8s.io/v1",
		        "version": "v1"
		      }
		    },
		    {
		      "name": "apps",
		      "versions": [
		        {
		          "groupVersion": "apps/v1",
		          "version": "v1"
		        }
		      ],
		      "preferredVersion": {
		        "groupVersion": "apps/v1",
		        "version": "v1"
		      }
		    },
		    {
		      "name": "events.k8s.io",
		      "versions": [
		        {
		          "groupVersion": "events.k8s.io/v1",
		          "version": "v1"
		        }
		      ],
		      "preferredVersion": {
		        "groupVersion": "events.k8s.io/v1",
		        "version": "v1"
		      }
		    },
		    {
		      "name": "authentication.k8s.io",
		      "versions": [
		        {
		          "groupVersion": "authentication.k8s.io/v1",
		          "version": "v1"
		        }
		      ],
		      "preferredVersion": {
		        "groupVersion": "authentication.k8s.io/v1",
		        "version": "v1"
		      }
		    },
		    {
		      "name": "authorization.k8s.io",
		      "versions": [
		        {
		          "groupVersion": "authorization.k8s.io/v1",
		          "version": "v1"
		        }
		      ],
		      "preferredVersion": {
		        "groupVersion": "authorization.k8s.io/v1",
		        "version": "v1"
			  }
			}
		  ]
		}
	*/

	groups := map[string]sets.Set[string]{}
	s.reg.Visit(func(_ string, rd *v1alpha1.ResourceDescriptor) {
		if rd.Spec.Resource.Name == "" {
			return
		}

		if rd.Spec.Resource.Group == "" {
			return
		}

		apiGroup, exists := groups[rd.Spec.Resource.Group]
		if !exists {
			apiGroup = sets.New[string]()
		}
		apiGroup.Insert(rd.Spec.Resource.Version)
		groups[rd.Spec.Resource.Group] = apiGroup
	})

	resp.Groups = make([]metav1.APIGroup, 0, len(groups))
	for group, versions := range groups {
		list := versions.UnsortedList()
		sort.Slice(list, func(i, j int) bool {
			return apiversion.MustCompare(list[i], list[j]) > 0
		})

		apiGroup := metav1.APIGroup{
			Name:     group,
			Versions: make([]metav1.GroupVersionForDiscovery, 0, len(list)),
		}
		for _, version := range list {
			apiGroup.Versions = append(apiGroup.Versions, metav1.GroupVersionForDiscovery{
				GroupVersion: schema.GroupVersion{
					Group:   group,
					Version: version,
				}.String(),
				Version: version,
			})
		}
		apiGroup.PreferredVersion = apiGroup.Versions[0]
		resp.Groups = append(resp.Groups, apiGroup)
	}

	_ = s.encoder(w, r).Encode(&resp, w)
}
