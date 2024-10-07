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
	"strings"

	kmapi "kmodules.xyz/client-go/api/v1"
	"kmodules.xyz/resource-metadata/apis/meta/v1alpha1"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (s *Server) APIResourceList(w http.ResponseWriter, r *http.Request) {
	gv := schema.GroupVersion{
		Group:   chi.URLParam(r, "group"),
		Version: chi.URLParam(r, "version"),
	}
	if gv.Version == "" && gv.Group == "" {
		gv.Version = "v1"
	}

	resp := metav1.APIResourceList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "APIResourceList",
		},
		GroupVersion: gv.String(),
		APIResources: nil,
	}
	/*
	   {
	     "name": "daemonsets",
	     "singularName": "daemonset",
	     "namespaced": true,
	     "kind": "DaemonSet",
	     "verbs": [
	       "create",
	       "delete",
	       "deletecollection",
	       "get",
	       "list",
	       "patch",
	       "update",
	       "watch"
	     ],
	     "shortNames": [
	       "ds"
	     ],
	     "categories": [
	       "all"
	     ],
	     "storageVersionHash": "dd7pWHUlMKQ="
	   },
	*/
	s.reg.Visit(func(_ string, rd *v1alpha1.ResourceDescriptor) {
		if rd.Spec.Resource.Name == "" {
			return
		}

		if rd.Spec.Resource.GroupVersion() == gv {
			resp.APIResources = append(resp.APIResources, metav1.APIResource{
				Name:         rd.Spec.Resource.Name,
				SingularName: strings.ToLower(rd.Spec.Resource.Kind),
				Namespaced:   rd.Spec.Resource.Scope == kmapi.NamespaceScoped,
				Group:        rd.Spec.Resource.Group,
				Kind:         rd.Spec.Resource.Kind,
				Verbs: []string{
					"create",
					"delete",
					"deletecollection",
					"get",
					"list",
					"patch",
					"update",
				},
				ShortNames:         nil,
				Categories:         nil,
				StorageVersionHash: "",
			})
		}
	})
	sort.Slice(resp.APIResources, func(i, j int) bool {
		return resp.APIResources[i].Name < resp.APIResources[j].Name
	})

	_ = s.encoder(w, r).Encode(&resp, w)
}
