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

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
)

func (s *Server) APIGroup(w http.ResponseWriter, r *http.Request) {
	group := chi.URLParam(r, "group")

	/*
		{
		  "kind": "APIGroup",
		  "apiVersion": "v1",
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
		}
	*/

	versions := sets.NewString()
	s.reg.Visit(func(_ string, rd *v1alpha1.ResourceDescriptor) {
		if rd.Spec.Resource.Name == "" {
			return
		}

		if rd.Spec.Resource.Group == group {
			versions.Insert(rd.Spec.Resource.Version)
		}
	})

	list := versions.UnsortedList()
	sort.Slice(list, func(i, j int) bool {
		return apiversion.MustCompare(list[i], list[j]) > 0
	})

	resp := metav1.APIGroup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "APIGroup",
		},
		Name:     group,
		Versions: make([]metav1.GroupVersionForDiscovery, 0, len(list)),
	}
	for _, version := range list {
		resp.Versions = append(resp.Versions, metav1.GroupVersionForDiscovery{
			GroupVersion: schema.GroupVersion{
				Group:   group,
				Version: version,
			}.String(),
			Version: version,
		})
	}
	resp.PreferredVersion = resp.Versions[0]

	_ = s.encoder(w, r).Encode(&resp, w)
}
