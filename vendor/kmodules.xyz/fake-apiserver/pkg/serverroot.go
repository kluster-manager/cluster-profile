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
	"encoding/json"
	"fmt"
	"net/http"

	"kmodules.xyz/resource-metadata/apis/meta/v1alpha1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/version"
)

func (s *Server) APIRoot(w http.ResponseWriter, r *http.Request) {
	paths := sets.NewString(
		"/api",
		"/api/v1",
		"/apis",
		"/apis/",
		"/healthz",
		"/version",
	)
	s.reg.Visit(func(_ string, rd *v1alpha1.ResourceDescriptor) {
		if rd.Spec.Resource.Name == "" {
			return
		}

		if rd.Spec.Resource.Group != "" {
			paths.Insert(fmt.Sprintf("/apis/%s", rd.Spec.Resource.Group))
			paths.Insert(fmt.Sprintf("/apis/%s/%s", rd.Spec.Resource.Group, rd.Spec.Resource.Version))
		}
	})

	resp := struct {
		Paths []string `json:"paths"`
	}{
		Paths: paths.List(),
	}
	data, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", runtime.ContentTypeJSON)
	_, _ = w.Write(data)
}

func (s *Server) Version(w http.ResponseWriter, r *http.Request) {
	resp := version.Info{
		Major:        "1",
		Minor:        "28",
		GitVersion:   "v1.28.1",
		GitCommit:    "8dc49c4b984b897d423aab4971090e1879eb4f23",
		GitTreeState: "clean",
		BuildDate:    "2023-08-24T11:16:29Z",
		GoVersion:    "go1.20.7",
		Compiler:     "gc",
		Platform:     "darwin/arm64",
	}
	data, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", runtime.ContentTypeJSON)
	_, _ = w.Write(data)
}

func (s *Server) Healthz(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("OK"))
}
