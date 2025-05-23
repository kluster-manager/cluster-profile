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
	"fmt"

	"kmodules.xyz/client-go/apiextensions"

	crdv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crd_cs "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

func RegisterCRDs(restcfg *rest.Config, crds []*apiextensions.CustomResourceDefinition) error {
	crds = append(crds, fakeProjectCRD())
	crdClient, err := crd_cs.NewForConfig(restcfg)
	if err != nil {
		return fmt.Errorf("failed to create crd client, reason %v", err)
	}
	err = apiextensions.RegisterCRDs(crdClient, crds)
	if err != nil {
		return fmt.Errorf("failed to register appRelease crd, reason %v", err)
	}
	return nil
}

func fakeProjectCRD() *apiextensions.CustomResourceDefinition {
	return &apiextensions.CustomResourceDefinition{
		V1: &crdv1.CustomResourceDefinition{
			TypeMeta: metav1.TypeMeta{
				Kind:       "CustomResourceDefinition",
				APIVersion: "apiextensions.k8s.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "projects.project.openshift.io",
			},
			Spec: crdv1.CustomResourceDefinitionSpec{
				Group: "project.openshift.io",
				Scope: crdv1.NamespaceScoped,
				Names: crdv1.CustomResourceDefinitionNames{
					Plural:     "projects",
					Singular:   "project",
					Kind:       "Project",
					ShortNames: []string{"proj"},
				},
				Versions: []crdv1.CustomResourceDefinitionVersion{
					{
						Name:    "v1",
						Served:  true,
						Storage: true,
						Schema: &crdv1.CustomResourceValidation{
							OpenAPIV3Schema: &crdv1.JSONSchemaProps{
								Type: "object",
							},
						},
					},
				},
			},
		},
	}
}
