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
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	httpw "go.wandrs.dev/http"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func (s *Server) Update(w http.ResponseWriter, r *http.Request) {
	store := s.Store(r)
	codec := s.codec(w, r)

	obj, err := s.UpdateImpl(store, codec, r)
	if err != nil {
		_ = codec.Encode(httpw.ErrorToAPIStatus(err), w)
		return
	}
	_ = codec.Encode(obj, w)
}

func (s *Server) UpdateImpl(store *APIStorage, codec runtime.Codec, r *http.Request) (runtime.Object, error) {
	var opts metav1.UpdateOptions
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
	if store.Namespaced {
		ns := chi.URLParam(r, "namespace")
		obj.SetNamespace(ns)
	} else {
		obj.SetNamespace("")
	}
	store.Insert(&obj)

	return &obj, nil
}
