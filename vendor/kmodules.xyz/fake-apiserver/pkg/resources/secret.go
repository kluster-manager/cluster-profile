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

package resources

import (
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func ProcessSecret(u *unstructured.Unstructured) error {
	var obj core.Secret
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), &obj)
	if err != nil {
		return err
	}

	if len(obj.StringData) > 0 && len(obj.Data) == 0 {
		obj.Data = map[string][]byte{}
	}
	for k, v := range obj.StringData {
		obj.Data[k] = []byte(v)
	}

	result, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
	if err != nil {
		return err
	}
	u.SetUnstructuredContent(result)
	return nil
}
