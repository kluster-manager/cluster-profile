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
	"context"
	"errors"
	"testing"

	profilev1alpha1 "github.com/kluster-manager/cluster-profile/apis/profile/v1alpha1"
	"github.com/kluster-manager/cluster-profile/pkg/common"

	fluxhelm "github.com/fluxcd/helm-controller/api/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	workv1 "open-cluster-management.io/api/work/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// stubClient serves the reads these two functions make and records the writes.
// client.Client is embedded unset on purpose: an unexpected call panics instead
// of silently passing.
type stubClient struct {
	client.Client

	manifestWorks map[string]*workv1.ManifestWork
	manifestErr   map[string]error
	configMaps    []corev1.ConfigMap
	listErr       error
	patchErr      error

	listOpts []client.ListOption
	patched  []*corev1.ConfigMap
}

func (c *stubClient) Scheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := workv1.Install(scheme); err != nil {
		panic(err)
	}
	return scheme
}

func (c *stubClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	switch out := obj.(type) {
	case *workv1.ManifestWork:
		if err, found := c.manifestErr[key.String()]; found {
			return err
		}
		mw, found := c.manifestWorks[key.String()]
		if !found {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "manifestworks"}, key.Name)
		}
		mw.DeepCopyInto(out)
		return nil
	case *corev1.ConfigMap:
		for i := range c.configMaps {
			if client.ObjectKeyFromObject(&c.configMaps[i]) == key {
				c.configMaps[i].DeepCopyInto(out)
				return nil
			}
		}
		return apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, key.Name)
	}
	panic("unexpected Get")
}

func (c *stubClient) List(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
	c.listOpts = opts
	if c.listErr != nil {
		return c.listErr
	}
	out, ok := list.(*corev1.ConfigMapList)
	if !ok {
		panic("unexpected List")
	}
	out.Items = append(out.Items, c.configMaps...)
	return nil
}

func (c *stubClient) Patch(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) error {
	if c.patchErr != nil {
		return c.patchErr
	}
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		panic("unexpected Patch")
	}
	c.patched = append(c.patched, cm.DeepCopy())
	return nil
}

const (
	hrNamespace    = "kubeops"
	testGeneration = 2
)

func stringValue(name, value string) workv1.FeedbackValue {
	return workv1.FeedbackValue{Name: name, Value: workv1.FieldValue{Type: workv1.String, String: &value}}
}

func intValue(name string, value int64) workv1.FeedbackValue {
	return workv1.FeedbackValue{Name: name, Value: workv1.FieldValue{Type: workv1.Integer, Integer: &value}}
}

// manifestWork builds a ManifestWork whose status reports hrNamespace/hrName with
// the given feedback. applied controls the Applied condition the work agent sets.
func manifestWork(name, hrName string, applied bool, values ...workv1.FeedbackValue) *workv1.ManifestWork {
	status := metav1.ConditionFalse
	if applied {
		status = metav1.ConditionTrue
	}
	return &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "spoke"},
		Status: workv1.ManifestWorkStatus{
			ResourceStatus: workv1.ManifestResourceStatus{
				Manifests: []workv1.ManifestCondition{
					{
						ResourceMeta:    workv1.ManifestResourceMeta{Kind: fluxhelm.HelmReleaseKind, Name: hrName, Namespace: hrNamespace},
						StatusFeedbacks: workv1.StatusFeedbackResult{Values: values},
						Conditions:      []metav1.Condition{{Type: workv1.ManifestApplied, Status: status, Reason: "test"}},
					},
				},
			},
		},
	}
}

func readyFeedback() []workv1.FeedbackValue {
	return []workv1.FeedbackValue{
		stringValue(common.HelmReleaseReadyFeedback, string(metav1.ConditionTrue)),
		intValue(common.HelmReleaseGenerationFeedback, testGeneration),
		intValue(common.HelmReleaseObservedGenerationFeedback, testGeneration),
	}
}

func target(mwName, hrName string, minGeneration int64) upgradeTarget {
	return upgradeTarget{
		ManifestWorkNamespace: "spoke",
		ManifestWorkName:      mwName,
		HelmReleaseNamespace:  hrNamespace,
		HelmReleaseName:       hrName,
		MinGeneration:         minGeneration,
	}
}

func upgraderConfigMap(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "features-upgrader-x", Namespace: "spoke"},
		Data:       data,
	}
}

func TestEvaluateTargets(t *testing.T) {
	// A HelmRelease flux has not looked at yet: the spec we wrote reached the
	// spoke (generation 2) but observedGeneration still describes the old release,
	// so a Ready=True feedback must not count.
	staleObserved := []workv1.FeedbackValue{
		stringValue(common.HelmReleaseReadyFeedback, string(metav1.ConditionTrue)),
		intValue(common.HelmReleaseGenerationFeedback, 2),
		intValue(common.HelmReleaseObservedGenerationFeedback, 1),
	}

	cases := []struct {
		name        string
		targets     []upgradeTarget
		works       map[string]*workv1.ManifestWork
		workErrs    map[string]error
		data        map[string]string
		wantPending []string
		wantMarked  map[string]string
		wantPatches int
	}{
		{
			name:        "ready target is marked and drops out",
			targets:     []upgradeTarget{target("observability", "prom-label-proxy", 2)},
			works:       map[string]*workv1.ManifestWork{"spoke/observability": manifestWork("observability", "prom-label-proxy", true, readyFeedback()...)},
			data:        map[string]string{"prom-label-proxy": string(metav1.ConditionFalse)},
			wantMarked:  map[string]string{"prom-label-proxy": string(metav1.ConditionTrue)},
			wantPatches: 1,
		},
		{
			name:        "flux has not observed the new spec yet",
			targets:     []upgradeTarget{target("observability", "tenant-operator", 2)},
			works:       map[string]*workv1.ManifestWork{"spoke/observability": manifestWork("observability", "tenant-operator", true, staleObserved...)},
			data:        map[string]string{"tenant-operator": string(metav1.ConditionFalse)},
			wantPending: []string{"tenant-operator"},
			wantPatches: 0,
		},
		{
			name:        "not applied on the spoke",
			targets:     []upgradeTarget{target("observability", "tenant-operator", 2)},
			works:       map[string]*workv1.ManifestWork{"spoke/observability": manifestWork("observability", "tenant-operator", false, readyFeedback()...)},
			data:        map[string]string{"tenant-operator": string(metav1.ConditionFalse)},
			wantPending: []string{"tenant-operator"},
			wantPatches: 0,
		},
		{
			name:        "already marked ready is not patched again",
			targets:     []upgradeTarget{target("observability", "prom-label-proxy", 2)},
			works:       map[string]*workv1.ManifestWork{"spoke/observability": manifestWork("observability", "prom-label-proxy", true, readyFeedback()...)},
			data:        map[string]string{"prom-label-proxy": string(metav1.ConditionTrue)},
			wantPatches: 0,
		},
		{
			name:        "unreadable ManifestWork keeps its target pending",
			targets:     []upgradeTarget{target("observability", "tenant-operator", 2)},
			workErrs:    map[string]error{"spoke/observability": errors.New("boom")},
			data:        map[string]string{"tenant-operator": string(metav1.ConditionFalse)},
			wantPending: []string{"tenant-operator"},
			wantPatches: 0,
		},
		{
			name: "one ready, one pending across separate ManifestWorks",
			targets: []upgradeTarget{
				target("observability", "prom-label-proxy", 2),
				target("core", "kube-ui-server", 3),
			},
			works: map[string]*workv1.ManifestWork{
				"spoke/observability": manifestWork("observability", "prom-label-proxy", true, readyFeedback()...),
				"spoke/core":          manifestWork("core", "kube-ui-server", true, readyFeedback()...),
			},
			data:        map[string]string{"prom-label-proxy": string(metav1.ConditionFalse), "kube-ui-server": string(metav1.ConditionFalse)},
			wantPending: []string{"kube-ui-server"},
			wantMarked:  map[string]string{"prom-label-proxy": string(metav1.ConditionTrue)},
			wantPatches: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configMap := upgraderConfigMap(tc.data)
			kc := &stubClient{
				manifestWorks: tc.works,
				manifestErr:   tc.workErrs,
				configMaps:    []corev1.ConfigMap{*configMap},
			}

			pending, err := evaluateTargets(context.Background(), kc, tc.targets, configMap)
			if err != nil {
				t.Fatalf("evaluateTargets: %v", err)
			}

			got := make([]string, 0, len(pending))
			for _, p := range pending {
				got = append(got, p.HelmReleaseName)
			}
			if len(got) != len(tc.wantPending) {
				t.Fatalf("pending = %v, want %v", got, tc.wantPending)
			}
			for i := range got {
				if got[i] != tc.wantPending[i] {
					t.Fatalf("pending = %v, want %v", got, tc.wantPending)
				}
			}

			for name, want := range tc.wantMarked {
				if configMap.Data[name] != want {
					t.Errorf("ConfigMap[%s] = %q, want %q", name, configMap.Data[name], want)
				}
			}
			if len(kc.patched) != tc.wantPatches {
				t.Errorf("patches = %d, want %d", len(kc.patched), tc.wantPatches)
			}
			if tc.wantPatches > 0 {
				last := kc.patched[len(kc.patched)-1]
				for name, want := range tc.wantMarked {
					if last.Data[name] != want {
						t.Errorf("patched ConfigMap[%s] = %q, want %q", name, last.Data[name], want)
					}
				}
			}
		})
	}
}

func TestEvaluateTargetsPatchFailureIsReported(t *testing.T) {
	configMap := upgraderConfigMap(map[string]string{"prom-label-proxy": string(metav1.ConditionFalse)})
	kc := &stubClient{
		manifestWorks: map[string]*workv1.ManifestWork{
			"spoke/observability": manifestWork("observability", "prom-label-proxy", true, readyFeedback()...),
		},
		configMaps: []corev1.ConfigMap{*configMap},
		patchErr:   errors.New("conflict"),
	}

	if _, err := evaluateTargets(context.Background(), kc, []upgradeTarget{target("observability", "prom-label-proxy", 2)}, configMap); err == nil {
		t.Fatal("expected an error when the ConfigMap patch fails")
	}
}

func pendingConfigMap(name, version, upgradeAt string, targets string, status string) corev1.ConfigMap {
	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "spoke",
			Labels: map[string]string{
				common.ACEUpgrader:        "true",
				common.ACEUpgraderVersion: version,
			},
			Annotations: map[string]string{},
		},
		Data: map[string]string{common.UpgradeStatusKey: status},
	}
	if targets != "" {
		cm.Annotations[common.UpgradeTargetsAnnotation] = targets
	}
	if upgradeAt != "" {
		cm.Annotations[common.UpgradeAnnotation] = upgradeAt
	}
	return cm
}

func profileBinding(version, upgradeAt string) *profilev1alpha1.ManagedClusterProfileBinding {
	pb := &profilev1alpha1.ManagedClusterProfileBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "spoke", Namespace: "spoke"},
		Spec: profilev1alpha1.ManagedClusterProfileBindingSpec{
			OpscenterFeaturesVersion: version,
		},
	}
	if upgradeAt != "" {
		pb.Annotations = map[string]string{common.UpgradeAnnotation: upgradeAt}
	}
	return pb
}

const oneTarget = `[{"manifestWorkNamespace":"spoke","manifestWorkName":"observability","helmReleaseNamespace":"kubeops","helmReleaseName":"prom-label-proxy","minGeneration":2}]`

func TestFindPendingUpgrade(t *testing.T) {
	cases := []struct {
		name       string
		configMaps []corev1.ConfigMap
		binding    *profilev1alpha1.ManagedClusterProfileBinding
		wantFound  string
	}{
		{
			name:       "resumes a pending run for this version",
			configMaps: []corev1.ConfigMap{pendingConfigMap("cm-a", "v1", "", oneTarget, common.UpgradeStatusPending)},
			binding:    profileBinding("v1", ""),
			wantFound:  "cm-a",
		},
		{
			name:       "ignores a run for another version",
			configMaps: []corev1.ConfigMap{pendingConfigMap("cm-a", "v1", "", oneTarget, common.UpgradeStatusPending)},
			binding:    profileBinding("v2", ""),
		},
		{
			name:       "ignores a finished run",
			configMaps: []corev1.ConfigMap{pendingConfigMap("cm-a", "v1", "", oneTarget, common.UpgradeStatusCompleted)},
			binding:    profileBinding("v1", ""),
		},
		{
			name:       "a repeated force-upgrade is a new run",
			configMaps: []corev1.ConfigMap{pendingConfigMap("cm-a", "v1", "2026-08-27T06:00:00Z", oneTarget, common.UpgradeStatusPending)},
			binding:    profileBinding("v1", "2026-08-27T07:00:00Z"),
		},
		{
			name:       "resumes when the force-upgrade stamp matches",
			configMaps: []corev1.ConfigMap{pendingConfigMap("cm-a", "v1", "2026-08-27T06:00:00Z", oneTarget, common.UpgradeStatusPending)},
			binding:    profileBinding("v1", "2026-08-27T06:00:00Z"),
			wantFound:  "cm-a",
		},
		{
			name:       "a run without targets cannot be resumed",
			configMaps: []corev1.ConfigMap{pendingConfigMap("cm-a", "v1", "", "", common.UpgradeStatusPending)},
			binding:    profileBinding("v1", ""),
		},
		{
			name: "picks the resumable run among several",
			configMaps: []corev1.ConfigMap{
				pendingConfigMap("cm-old", "v0", "", oneTarget, common.UpgradeStatusCompleted),
				pendingConfigMap("cm-b", "v1", "", oneTarget, common.UpgradeStatusPending),
			},
			binding:   profileBinding("v1", ""),
			wantFound: "cm-b",
		},
		{
			name:       "no upgrader ConfigMaps at all",
			configMaps: nil,
			binding:    profileBinding("v1", ""),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kc := &stubClient{configMaps: tc.configMaps}

			configMap, targets, err := findPendingUpgrade(context.Background(), kc, tc.binding)
			if err != nil {
				t.Fatalf("findPendingUpgrade: %v", err)
			}

			if tc.wantFound == "" {
				if configMap != nil {
					t.Fatalf("got ConfigMap %q, want none", configMap.Name)
				}
				return
			}

			if configMap == nil {
				t.Fatalf("got no ConfigMap, want %q", tc.wantFound)
			}
			if configMap.Name != tc.wantFound {
				t.Fatalf("got ConfigMap %q, want %q", configMap.Name, tc.wantFound)
			}
			if len(targets) != 1 || targets[0] != target("observability", "prom-label-proxy", 2) {
				t.Fatalf("targets = %+v, want the one recorded on the ConfigMap", targets)
			}
		})
	}
}

func TestFindPendingUpgradeScopesTheListToTheBinding(t *testing.T) {
	kc := &stubClient{}
	if _, _, err := findPendingUpgrade(context.Background(), kc, profileBinding("v1", "")); err != nil {
		t.Fatalf("findPendingUpgrade: %v", err)
	}

	options := &client.ListOptions{}
	for _, opt := range kc.listOpts {
		opt.ApplyToList(options)
	}
	if options.Namespace != "spoke" {
		t.Errorf("namespace = %q, want %q", options.Namespace, "spoke")
	}
	// Without the label selector the controller would sweep every ConfigMap in the
	// cluster namespace.
	if options.LabelSelector == nil || !options.LabelSelector.Matches(labels.Set{common.ACEUpgrader: "true"}) {
		t.Errorf("label selector = %v, want it to select upgrader ConfigMaps", options.LabelSelector)
	}
}

func TestFindPendingUpgradeSurfacesErrors(t *testing.T) {
	t.Run("list error", func(t *testing.T) {
		kc := &stubClient{listErr: errors.New("boom")}
		if _, _, err := findPendingUpgrade(context.Background(), kc, profileBinding("v1", "")); err == nil {
			t.Fatal("expected the list error to surface")
		}
	})

	t.Run("malformed targets", func(t *testing.T) {
		kc := &stubClient{configMaps: []corev1.ConfigMap{pendingConfigMap("cm-a", "v1", "", "{not json", common.UpgradeStatusPending)}}
		if _, _, err := findPendingUpgrade(context.Background(), kc, profileBinding("v1", "")); err == nil {
			t.Fatal("expected the unmarshal error to surface")
		}
	})
}
