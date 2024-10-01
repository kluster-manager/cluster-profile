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

package feature_installer

import (
	"context"
	"encoding/json"
	"log"
	"time"

	profilev1alpha1 "github.com/kluster-manager/cluster-profile/apis/profile/v1alpha1"

	fluxhelm "github.com/fluxcd/helm-controller/api/v2"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cu "kmodules.xyz/client-go/client"
	uiapi "kmodules.xyz/resource-metadata/apis/ui/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func createHR(featureName, featureSetName, ns string, profile *profilev1alpha1.ManagedClusterSetProfile, featureObj uiapi.Feature, fakeServer *FakeServer, values map[string]interface{}) error {
	hr := &fluxhelm.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      featureName,
			Namespace: ns,
			Labels: map[string]string{
				"app.kubernetes.io/component": featureName,
				"app.kubernetes.io/part-of":   featureSetName,
				"app.kubernetes.io/instance":  featureSetName,
			},
		},
		Spec: fluxhelm.HelmReleaseSpec{
			Chart: &fluxhelm.HelmChartTemplate{
				Spec: fluxhelm.HelmChartTemplateSpec{
					Chart:   featureName,
					Version: profile.Spec.Features[featureName].Chart.Version,
					SourceRef: fluxhelm.CrossNamespaceObjectReference{
						Kind:      profile.Spec.Features[featureName].Chart.SourceRef.Kind,
						Name:      profile.Spec.Features[featureName].Chart.SourceRef.Name,
						Namespace: profile.Spec.Features[featureName].Chart.SourceRef.Namespace,
					},
				},
			},
			Install: &fluxhelm.Install{
				CreateNamespace: profile.Spec.Features[featureName].Chart.CreateNamespace,
				Remediation: &fluxhelm.InstallRemediation{
					Retries: -1,
				},
			},
			Interval:         metav1.Duration{Duration: time.Minute * 5},
			ReleaseName:      featureName,
			StorageNamespace: featureObj.Spec.Chart.Namespace,
			TargetNamespace:  featureObj.Spec.Chart.Namespace,
			Timeout:          &metav1.Duration{Duration: time.Minute * 30},
			Upgrade: &fluxhelm.Upgrade{
				CRDs: fluxhelm.CreateReplace,
				Remediation: &fluxhelm.UpgradeRemediation{
					Retries: -1,
				},
			},
			Values: profile.Spec.Features[featureName].Values,
		},
	}

	if len(profile.Spec.Features[featureName].ValuesFrom) > 0 {
		valuesFrom := make([]fluxhelm.ValuesReference, 0)
		for _, vf := range profile.Spec.Features[featureName].ValuesFrom {
			valuesFrom = append(valuesFrom, fluxhelm.ValuesReference{
				Kind:      vf.Kind,
				Name:      vf.Name,
				Optional:  vf.Optional,
				ValuesKey: vf.ValuesKey,
			})
		}
		hr.Spec.ValuesFrom = valuesFrom
	}
	if values != nil {
		// Convert map to JSON
		jsonData, err := json.Marshal(values)
		if err != nil {
			log.Fatalf("Error marshaling values: %v", err)
		}

		// Create an instance of apiextensionsv1.JSON
		var apiextensionsJSON v1.JSON
		// Unmarshal the JSON data into apiextensionsv1.JSON
		if err := json.Unmarshal(jsonData, &apiextensionsJSON); err != nil {
			log.Fatalf("Error unmarshaling to apiextensionsv1.JSON: %v", err)
		}
		hr.Spec.Values = &apiextensionsJSON
	}
	_, err := cu.CreateOrPatch(context.Background(), fakeServer.FakeClient, hr, func(obj client.Object, createOp bool) client.Object {
		in := obj.(*fluxhelm.HelmRelease)
		in = hr
		return in
	})
	if err != nil {
		return err
	}
	return nil
}
