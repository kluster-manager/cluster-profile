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

package controller

import (
	"context"
	"fmt"

	profilev1alpha1 "github.com/kluster-manager/cluster-profile/apis/profile/v1alpha1"
	"github.com/kluster-manager/cluster-profile/pkg/cluster_upgrade"
	"github.com/kluster-manager/cluster-profile/pkg/feature_installer"
	"github.com/kluster-manager/cluster-profile/pkg/utils"

	fluxhelm "github.com/fluxcd/helm-controller/api/v2"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	workv1 "open-cluster-management.io/api/work/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ManagedClusterProfileBindingReconciler reconciles a ManagedClusterProfileBinding object
type ManagedClusterProfileBindingReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=profile.k8s.appscode.com,resources=managedclusterprofilebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=profile.k8s.appscode.com,resources=managedclusterprofilebindings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=profile.k8s.appscode.com,resources=managedclusterprofilebindings/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ManagedClusterProfileBinding object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *ManagedClusterProfileBindingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Start reconciling")

	profileBinding := &profilev1alpha1.ManagedClusterProfileBinding{}
	err := r.Client.Get(ctx, req.NamespacedName, profileBinding)
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	profile := &profilev1alpha1.ManagedClusterSetProfile{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: profileBinding.Spec.ProfileRef.Name}, profile)
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if err = validateFeatureList(profile); err != nil {
		return reconcile.Result{}, err
	}

	featureInfo := make(map[string][]string)
	for f, val := range profile.Spec.Features {
		featureInfo[val.FeatureSet] = append(featureInfo[val.FeatureSet], f)
	}

	if profileBinding.Spec.OpscenterFeaturesVersion != "" && profileBinding.Spec.OpscenterFeaturesVersion != profileBinding.Status.ObservedOpscenterFeaturesVersion {
		if err := cluster_upgrade.UpgradeCluster(profileBinding, profile, r.Client); err != nil {
			return reconcile.Result{}, r.setOpscenterFeaturesVersion(ctx, profileBinding, err)
		}
	} else if profile.Spec.Features["opscenter-features"].Chart.Version == profileBinding.Spec.OpscenterFeaturesVersion || profileBinding.Spec.OpscenterFeaturesVersion == "" {
		if err = feature_installer.EnableFeatures(ctx, r.Client, profileBinding, featureInfo, profile); err != nil {
			return reconcile.Result{}, r.setOpscenterFeaturesVersion(ctx, profileBinding, err)
		}
	}

	return reconcile.Result{}, r.setOpscenterFeaturesVersion(ctx, profileBinding, nil)
}

func (r *ManagedClusterProfileBindingReconciler) mapClusterProfileToClusterProfileBinding(ctx context.Context, obj client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)
	profile, ok := obj.(*profilev1alpha1.ManagedClusterSetProfile)
	if !ok {
		return nil
	}

	profileBindingList := &profilev1alpha1.ManagedClusterProfileBindingList{}
	err := r.List(ctx, profileBindingList)
	if err != nil {
		logger.Error(err, "Failed to list ManagedClusterProfileBinding objects")
		return nil
	}

	var requests []reconcile.Request
	for _, profileBinding := range profileBindingList.Items {
		if profileBinding.Spec.ProfileRef.Name == profile.GetName() {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      profileBinding.Name,
					Namespace: profileBinding.Namespace,
				},
			})
			logger.Info("Enqueuing request", "name", profileBinding.Name, "namespace", profileBinding.Namespace)
		}
	}

	return requests
}

func (r *ManagedClusterProfileBindingReconciler) setOpscenterFeaturesVersion(ctx context.Context, profileBinding *profilev1alpha1.ManagedClusterProfileBinding, err error) error {
	var pb profilev1alpha1.ManagedClusterProfileBinding
	// Re-fetch the latest version of the Account object
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(profileBinding), &pb); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get latest account object: %w", err)
	}

	pb.Status.ObservedOpscenterFeaturesVersion = setOpscenterFeaturesVersion(ctx, r.Client, pb.Namespace)
	if updateErr := r.Client.Status().Update(ctx, &pb); updateErr != nil {
		return fmt.Errorf("failed to update status to Failed: %w", updateErr)
	}
	return err
}

func setOpscenterFeaturesVersion(ctx context.Context, kc client.Client, profileBindingNamespace string) string {
	var mw workv1.ManifestWork
	if err := kc.Get(ctx, types.NamespacedName{Name: "opscenter-core", Namespace: profileBindingNamespace}, &mw); err != nil {
		return ""
	}
	for _, m := range mw.Spec.Workload.Manifests {
		object := map[string]interface{}{}
		if err := utils.Copy(m, &object); err != nil {
			return ""
		}

		_, name, _, err := feature_installer.GetKindNameNamespace(object)
		if err != nil {
			return ""
		}

		if name == "opscenter-features" {
			hr := fluxhelm.HelmRelease{}
			if err = utils.Copy(m, &hr); err != nil {
				return ""
			}
			return hr.Spec.Chart.Spec.Version
		}
	}
	return ""
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManagedClusterProfileBindingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&profilev1alpha1.ManagedClusterProfileBinding{}).
		Watches(
			&profilev1alpha1.ManagedClusterSetProfile{},
			handler.EnqueueRequestsFromMapFunc(r.mapClusterProfileToClusterProfileBinding),
		).
		Complete(r)
}
