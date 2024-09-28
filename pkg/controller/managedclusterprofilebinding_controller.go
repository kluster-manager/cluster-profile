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

	profilev1alpha1 "github.com/kluster-manager/cluster-profile/apis/profile/v1alpha1"
	"github.com/kluster-manager/cluster-profile/pkg/feature_installer"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
		return reconcile.Result{}, err
	}

	if err = validateFeatureList(profile); err != nil {
		return reconcile.Result{}, err
	}

	featureInfo := make(map[string][]string)
	for f, val := range profile.Spec.Features {
		featureInfo[val.FeatureSet] = append(featureInfo[val.FeatureSet], f)
	}

	if err = feature_installer.EnableFeatures(ctx, r.Client, profileBinding, featureInfo, profile); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ManagedClusterProfileBindingReconciler) mapManagedClusterProfileBindingToProfile(ctx context.Context, obj client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)
	profile, ok := obj.(*profilev1alpha1.ManagedClusterSetProfile)
	if !ok {
		return nil
	}

	logger.Info("ManagedClusterSetProfile updated", "name", profile.GetName())

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

// SetupWithManager sets up the controller with the Manager.
func (r *ManagedClusterProfileBindingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&profilev1alpha1.ManagedClusterProfileBinding{}).
		Watches(
			&profilev1alpha1.ManagedClusterSetProfile{},
			handler.EnqueueRequestsFromMapFunc(r.mapManagedClusterProfileBindingToProfile),
		).
		Complete(r)
}
