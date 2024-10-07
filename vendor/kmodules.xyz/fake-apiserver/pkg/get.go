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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

func (s *Server) Get(w http.ResponseWriter, r *http.Request) {
	store := s.Store(r)
	codec := s.codec(w, r)

	obj, err := s.GetImpl(store, r)
	if err != nil {
		_ = codec.Encode(httpw.ErrorToAPIStatus(err), w)
		return
	}
	_ = codec.Encode(obj, w)
}

func (s *Server) GetImpl(store *APIStorage, r *http.Request) (runtime.Object, error) {
	key := types.NamespacedName{
		Namespace: chi.URLParam(r, "namespace"),
		Name:      chi.URLParam(r, "name"),
	}

	obj, exists := store.Get(key)
	if !exists {
		return nil, apierrors.NewNotFound(store.GVR.GroupResource(), key.String())
	}
	return obj, nil
}
