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

	"github.com/go-chi/chi/v5"
	httpw "go.wandrs.dev/http"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

func (s *Server) DeleteCollection(w http.ResponseWriter, r *http.Request) {
	store := s.Store(r)
	codec := s.codec(w, r)

	obj, err := s.DeleteCollectionImpl(store, r)
	if err != nil {
		_ = codec.Encode(httpw.ErrorToAPIStatus(err), w)
		return
	}
	_ = codec.Encode(obj, w)
}

func (s *Server) DeleteCollectionImpl(store *APIStorage, r *http.Request) (runtime.Object, error) {
	var opts metav1.ListOptions
	err := s.opts.ParameterCodec.DecodeParameters(r.URL.Query(), metav1.SchemeGroupVersion, &opts)
	if err != nil {
		return nil, err
	}

	// List type
	items := store.Items()

	ns := chi.URLParam(r, "namespace")
	if ns != "" {
		filtered := items[:0]
		for _, item := range items {
			if item.GetNamespace() == ns {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	if opts.LabelSelector != "" {
		sel, err := labels.Parse(opts.LabelSelector)
		if err != nil {
			return nil, err
		}
		filtered := items[:0]
		for _, item := range items {
			if sel.Matches(labels.Set(item.GetLabels())) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	if opts.FieldSelector != "" {
		sel, err := fields.ParseSelector(opts.FieldSelector)
		if err != nil {
			return nil, err
		}
		filtered := items[:0]
		for _, item := range items {
			if sel.Matches(ObjectToFieldLabels(&item)) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	for _, item := range items {
		store.RemoveObj(&item)
	}

	list := unstructured.UnstructuredList{
		Items: items,
	}
	list.SetAPIVersion("v1")
	list.SetKind("List")

	return &list, err
}
