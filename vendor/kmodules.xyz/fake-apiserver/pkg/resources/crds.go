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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

/*
status:
  acceptedNames:
    categories:
    - datastore
    - kubedb
    - appscode
    - all
    kind: Postgres
    listKind: PostgresList
    plural: postgreses
    shortNames:
    - pg
    singular: postgres
  conditions:
  - lastTransitionTime: "2023-09-10T13:21:03Z"
    message: no conflicts found
    reason: NoConflicts
    status: "True"
    type: NamesAccepted
  - lastTransitionTime: "2023-09-10T13:21:03Z"
    message: the initial names have been accepted
    reason: InitialNamesAccepted
    status: "True"
    type: Established
  storedVersions:
  - v1alpha2
*/

func ProcessCRD(u *unstructured.Unstructured) error {
	var obj apiextensionsv1.CustomResourceDefinition
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), &obj)
	if err != nil {
		return err
	}
	obj.Status = apiextensionsv1.CustomResourceDefinitionStatus{
		Conditions: []apiextensionsv1.CustomResourceDefinitionCondition{
			{
				Type:               apiextensionsv1.Established,
				Status:             apiextensionsv1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Reason:             "InitialNamesAccepted",
				Message:            "the initial names have been accepted",
			},
			{
				Type:               apiextensionsv1.NamesAccepted,
				Status:             apiextensionsv1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Reason:             "NoConflicts",
				Message:            "no conflicts found",
			},
		},
		AcceptedNames: obj.Spec.Names,
		StoredVersions: []string{
			obj.Spec.Versions[0].Name,
		},
	}

	result, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
	if err != nil {
		return err
	}
	u.SetUnstructuredContent(result)
	return nil
}
