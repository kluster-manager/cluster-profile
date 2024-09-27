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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *Server) APIVersions(w http.ResponseWriter, r *http.Request) {
	/*
			`{
		  "kind": "APIVersions",
		  "versions": [
		    "v1"
		  ],
		  "serverAddressByClientCIDRs": [
		    {
		      "clientCIDR": "0.0.0.0/0",
		      "serverAddress": "172.18.0.2:6443"
		    }
		  ]
		}`
	*/
	resp := metav1.APIVersions{
		TypeMeta: metav1.TypeMeta{
			Kind: "APIVersions",
			// APIVersion: "v1",
		},
		Versions: []string{
			"v1",
		},
	}
	_ = s.encoder(w, r).Encode(&resp, w)
}
