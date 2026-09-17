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

package cluster_upgrade

import (
	"fmt"
	"testing"

	fluxhelm "github.com/fluxcd/helm-controller/api/v2"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"
	workv1 "open-cluster-management.io/api/work/v1"
)

// storedManifest builds the manifest as the ManifestWork carries it. The bytes
// are handed over verbatim, so a case can describe exactly what the API server
// kept -- pruned nulls included.
func storedManifest(t *testing.T, spec string) workv1.Manifest {
	t.Helper()
	raw := fmt.Sprintf(`{"apiVersion":"helm.toolkit.fluxcd.io/v2","kind":"HelmRelease","metadata":{"name":"prom-label-proxy","namespace":"kubeops"},"spec":%s}`, spec)
	return workv1.Manifest{RawExtension: runtime.RawExtension{Raw: []byte(raw)}}
}

func renderedSpec(t *testing.T, spec string) fluxhelm.HelmReleaseSpec {
	t.Helper()
	var out fluxhelm.HelmReleaseSpec
	if err := json.Unmarshal([]byte(spec), &out); err != nil {
		t.Fatalf("failed to build spec from %s: %v", spec, err)
	}
	return out
}

func TestSpecChanged(t *testing.T) {
	const pruned = `{"interval":"5m0s","chart":{"spec":{"chart":"prom-label-proxy","version":"v2026.6.2"}},"values":{"infra":{"tls":{"jks":{"password":"s3cr3t"}}}}}`

	cases := []struct {
		name        string
		oldManifest string
		newSpec     string
		want        bool
	}{
		{
			name:        "identical spec",
			oldManifest: pruned,
			newSpec:     pruned,
		},
		{
			// A JSON merge patch drops null-valued keys, so the nulls the chart
			// renders never reach the stored manifest. Reporting a change here
			// would bump MinGeneration past a generation the spoke never reaches.
			name:        "chart renders nulls the stored manifest cannot keep",
			oldManifest: pruned,
			newSpec:     `{"interval":"5m0s","chart":{"spec":{"chart":"prom-label-proxy","version":"v2026.6.2"}},"values":{"infra":{"tls":{"jks":{"keystore":null,"password":"s3cr3t","truststore":null}}}},"platform":null}`,
		},
		{
			name:        "nulls nested inside a list",
			oldManifest: `{"interval":"5m0s","values":{"tenants":[{"name":"a"},{"name":"b"}]}}`,
			newSpec:     `{"interval":"5m0s","values":{"tenants":[{"name":"a","quota":null},{"name":"b","quota":null}]}}`,
		},
		{
			name:        "stored manifest still carries a null",
			oldManifest: `{"interval":"5m0s","values":{"infra":{"tls":{"jks":{"keystore":null,"password":"s3cr3t"}}}}}`,
			newSpec:     `{"interval":"5m0s","values":{"infra":{"tls":{"jks":{"password":"s3cr3t"}}}}}`,
		},
		{
			name:        "chart version moves",
			oldManifest: pruned,
			newSpec:     `{"interval":"5m0s","chart":{"spec":{"chart":"prom-label-proxy","version":"v2026.7.10"}},"values":{"infra":{"tls":{"jks":{"password":"s3cr3t"}}}}}`,
			want:        true,
		},
		{
			name:        "a value changes",
			oldManifest: pruned,
			newSpec:     `{"interval":"5m0s","chart":{"spec":{"chart":"prom-label-proxy","version":"v2026.6.2"}},"values":{"infra":{"tls":{"jks":{"password":"rotated"}}}}}`,
			want:        true,
		},
		{
			name:        "a value is dropped rather than nulled",
			oldManifest: pruned,
			newSpec:     `{"interval":"5m0s","chart":{"spec":{"chart":"prom-label-proxy","version":"v2026.6.2"}},"values":{"infra":{"tls":{"jks":{}}}}}`,
			want:        true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := specChanged(storedManifest(t, tc.oldManifest), renderedSpec(t, tc.newSpec))
			if err != nil {
				t.Fatalf("specChanged returned an error: %v", err)
			}
			if got != tc.want {
				t.Errorf("specChanged = %v, want %v", got, tc.want)
			}
		})
	}
}

// Only the spec is compared, so the hub-side noise a typed round trip leaves
// behind -- an empty status, a null creationTimestamp -- must not read as a
// change the spoke will act on.
func TestSpecChangedIgnoresNonSpecNoise(t *testing.T) {
	const spec = `{"interval":"5m0s","values":{"replicas":1}}`
	raw := fmt.Sprintf(`{"apiVersion":"helm.toolkit.fluxcd.io/v2","kind":"HelmRelease","metadata":{"name":"prom-label-proxy","namespace":"kubeops","creationTimestamp":null},"spec":%s,"status":{}}`, spec)
	manifest := workv1.Manifest{RawExtension: runtime.RawExtension{Raw: []byte(raw)}}

	changed, err := specChanged(manifest, renderedSpec(t, spec))
	if err != nil {
		t.Fatalf("specChanged returned an error: %v", err)
	}
	if changed {
		t.Error("specChanged = true, want false for a spec that only differs outside .spec")
	}
}

func TestSpecChangedRejectsAnUnreadableManifest(t *testing.T) {
	manifest := workv1.Manifest{RawExtension: runtime.RawExtension{Raw: []byte(`{"spec":`)}}
	if _, err := specChanged(manifest, fluxhelm.HelmReleaseSpec{}); err == nil {
		t.Fatal("expected an error for a malformed manifest")
	}
}

func TestNewUpgradeTargetBumpsOnlyOnAChange(t *testing.T) {
	mw := manifestWork("observability", "prom-label-proxy", true, readyFeedback()...)
	hr := &fluxhelm.HelmRelease{}
	hr.Name = "prom-label-proxy"
	hr.Namespace = "kubeops"

	if unchanged := newUpgradeTarget(mw, hr, false); unchanged.MinGeneration != testGeneration {
		t.Errorf("MinGeneration = %d, want the reported generation %d", unchanged.MinGeneration, testGeneration)
	}
	if changed := newUpgradeTarget(mw, hr, true); changed.MinGeneration != testGeneration+1 {
		t.Errorf("MinGeneration = %d, want %d for a changed spec", changed.MinGeneration, testGeneration+1)
	}
}
