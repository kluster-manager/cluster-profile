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
	"fmt"
	"io"
	"net/http"

	"kmodules.xyz/fake-apiserver/pkg/resources"

	"github.com/go-chi/chi/v5"
	httpw "go.wandrs.dev/http"
	core "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
)

func (s *Server) Create(w http.ResponseWriter, r *http.Request) {
	store := s.Store(r)
	codec := s.codec(w, r)

	obj, err := s.CreateImpl(store, codec, r)
	if err != nil {
		_ = codec.Encode(httpw.ErrorToAPIStatus(err), w)
		return
	}

	w.WriteHeader(http.StatusCreated)
	_ = codec.Encode(obj, w)
}

func (s *Server) CreateImpl(store *APIStorage, codec runtime.Codec, r *http.Request) (runtime.Object, error) {
	var opts metav1.CreateOptions
	err := s.opts.ParameterCodec.DecodeParameters(r.URL.Query(), metav1.SchemeGroupVersion, &opts)
	if err != nil {
		return nil, err
	}

	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	var obj unstructured.Unstructured
	_, _, err = codec.Decode(data, &store.GVK, &obj)
	if err != nil {
		return nil, err
	}
	if obj.GetGenerateName() != "" {
		obj.SetName(fmt.Sprintf("%s-%s", obj.GetGenerateName(), utilrand.String(6)))
	}

	if obj.GetName() == "kubedb" {
		fmt.Println("wait")
	}

	if store.Namespaced {
		ns := chi.URLParam(r, "namespace")
		obj.SetNamespace(ns)
	} else {
		obj.SetNamespace("")
	}

	if store.GVK == core.SchemeGroupVersion.WithKind("Namespace") {
		cm := resources.CreateKubeRootCACert()
		cm.SetNamespace(obj.GetName())
		s.StoreForGVR(core.SchemeGroupVersion.WithResource("configmaps")).Insert(cm)
	} else if store.GVK == apiextensionsv1.SchemeGroupVersion.WithKind("CustomResourceDefinition") {
		err = resources.ProcessCRD(&obj)
		if err != nil {
			return nil, err
		}
	}

	store.Insert(&obj)

	return &obj, nil
}
