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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func CreateKubeRootCACert() *unstructured.Unstructured {
	obj := core.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-root-ca.crt",
			Namespace: "change-it",
		},
		Data: map[string]string{
			"ca.crt": `-----BEGIN CERTIFICATE-----
MIIC/jCCAeagAwIBAgIBADANBgkqhkiG9w0BAQsFADAVMRMwEQYDVQQDEwprdWJl
cm5ldGVzMB4XDTIzMDkxMDE0NDMyMVoXDTMzMDkwNzE0NDMyMVowFTETMBEGA1UE
AxMKa3ViZXJuZXRlczCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBAOue
C32NfqwQXJoE92nZ+hwesN85EYBfN1k+mBVBVQB9DqsGNTBnvilwnu5/STewdSsl
EYk+1RLRWsOuWrtpTb78LfhFPufLbL5Y9//+r1d5ifmyrHK6YwXoMV/XWza/tRM+
8ivZSzvIvkCLmiWuKPHQvgZeenGiVM8rXXA9bOm6sYdB10gMxRK7XcpmGqRbNRIJ
GOrtnz4fvBZLSJyg0kAjkkxMEFOGKutSFj2YdzZ4UgqtmqceCZMDzT9zuCmfJUzR
koZQJa9C5bPD4z6C4TMuQzpSR+F8NlnQdxwiSOojgaN00Q4B6c4SscuKr6MAXvYh
CI07G9WMovMl9DamZ4kCAwEAAaNZMFcwDgYDVR0PAQH/BAQDAgKkMA8GA1UdEwEB
/wQFMAMBAf8wHQYDVR0OBBYEFEioPg1/7puhRV8JS7JrPb3Mg3QTMBUGA1UdEQQO
MAyCCmt1YmVybmV0ZXMwDQYJKoZIhvcNAQELBQADggEBACreM1/afpMQiVUXoshb
gkYku64jdW+NJjs8N60u3/5zq4q2JUL4vhKeOS+fz64Tz7TmuK1xYmzs0pBQ1/vL
zEWK6LVdB4C3Bet9hw9OaYh+gEFQlSPPcx5K6kY4iQdGRUDduuWo+3PVG5zjbqs3
wPsxxaCgTrsZIVWrOq0KEBG74jdawIAr0vgQ3D0Ym6EahPXtGGIAV+T2ZjJsp86i
LwHIWhQ+Ucs3Ehm5yQdN7F6VI+d5ENL3rKr1T4ryPo2V0BZR8fV3+HbiSR2bcthR
9LvHOeAn8cSOxbFuf70WpgvwMlmW+jOKrWcMul1gh74aBRQV7jWBxR++JeV+rF3s
p/g=
-----END CERTIFICATE-----`,
		},
	}

	result, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
	if err != nil {
		panic(err)
	}
	var u unstructured.Unstructured
	u.SetUnstructuredContent(result)
	return &u
}
