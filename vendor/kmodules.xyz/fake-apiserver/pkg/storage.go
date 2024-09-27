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
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type APIStorage struct {
	m sync.RWMutex

	s *Server

	GVR        schema.GroupVersionResource
	GVK        schema.GroupVersionKind
	Namespaced bool
	Current    map[types.NamespacedName]*unstructured.Unstructured
	Deleted    map[types.NamespacedName]*unstructured.Unstructured
}

func (s *APIStorage) Items() []unstructured.Unstructured {
	s.m.RLock()
	defer s.m.RUnlock()

	result := make([]unstructured.Unstructured, 0, len(s.Current))
	for _, obj := range s.Current {
		result = append(result, *obj)
	}
	return result
}

func (s *APIStorage) Get(key types.NamespacedName) (*unstructured.Unstructured, bool) {
	s.m.RLock()
	defer s.m.RUnlock()

	obj, found := s.Current[key]
	return obj, found
}

func (s *APIStorage) Insert(obj *unstructured.Unstructured) {
	s.m.Lock()
	defer s.m.Unlock()

	rv := s.s.NextResourceVersion()
	obj.SetResourceVersion(fmt.Sprintf("%d", rv))

	key := types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
	s.Current[key] = obj
	delete(s.Deleted, key)
}

var nsGVK = schema.GroupVersionKind{
	Group:   "",
	Version: "v1",
	Kind:    "Namespace",
}

func (s *APIStorage) Remove(key types.NamespacedName) (*unstructured.Unstructured, bool) {
	s.m.Lock()
	defer s.m.Unlock()

	obj, exists := s.Current[key]
	delete(s.Current, key)
	if exists {
		rv := s.s.NextResourceVersion()
		obj.SetResourceVersion(fmt.Sprintf("%d", rv))

		s.Deleted[key] = obj

		if s.GVK == nsGVK {
			s.s.RemoveNamespace(key.Name)
		}
	}
	return obj, exists
}

func (s *APIStorage) RemoveObj(obj *unstructured.Unstructured) (*unstructured.Unstructured, bool) {
	key := types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
	return s.Remove(key)
}

func (s *APIStorage) RemoveForNamespace(ns string) {
	s.m.Lock()
	defer s.m.Unlock()

	if !s.Namespaced || ns == "" {
		return
	}

	for key := range s.Current {
		if key.Namespace == ns {
			delete(s.Current, key)
		}
	}
}
