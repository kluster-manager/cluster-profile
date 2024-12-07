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
	"github.com/kluster-manager/cluster-profile/pkg/common"

	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	cu "kmodules.xyz/client-go/client"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1beta2 "open-cluster-management.io/api/cluster/v1beta2"
	clustersdkv1beta2 "open-cluster-management.io/sdk-go/pkg/apis/cluster/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ManagedClusterSetProfileReconciler reconciles a ManagedClusterSetProfile object
type ManagedClusterSetProfileReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=profile.k8s.appscode.com,resources=managedclustersetprofiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=profile.k8s.appscode.com,resources=managedclustersetprofiles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=profile.k8s.appscode.com,resources=managedclustersetprofiles/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ManagedClusterSetProfile object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *ManagedClusterSetProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Start reconciling")

	profile := &profilev1alpha1.ManagedClusterSetProfile{}
	err := r.Client.Get(ctx, req.NamespacedName, profile)
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	var managedClusterSet clusterv1beta2.ManagedClusterSet
	err = r.Get(ctx, types.NamespacedName{Name: profile.Name}, &managedClusterSet)
	if err != nil && errors.IsNotFound(err) {
		if err = r.Delete(ctx, profile); err != nil {
			return reconcile.Result{}, err
		}
	} else if err != nil {
		return reconcile.Result{}, err
	}

	sel, err := clustersdkv1beta2.BuildClusterSelector(&managedClusterSet)
	if err != nil {
		return reconcile.Result{}, err
	}

	var clusters clusterv1.ManagedClusterList
	err = r.List(ctx, &clusters, client.MatchingLabelsSelector{Selector: sel})
	if err != nil {
		return reconcile.Result{}, err
	}

	// create ManagedClusterProfileBinding for every cluster of this clusterSet
	for _, cluster := range clusters.Items {
		clusterMetadata, err := GetClusterMetadata(cluster)
		if err != nil {
			return reconcile.Result{}, err
		}

		profileBinding := &profilev1alpha1.ManagedClusterProfileBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.Name,
				Namespace: cluster.Name,
				Labels: map[string]string{
					common.ProfileLabel: profile.Name,
				},
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(profile, profilev1alpha1.SchemeGroupVersion.WithKind(profilev1alpha1.ResourceKindManagedClusterSetProfile)),
				},
			},
			Spec: profilev1alpha1.ManagedClusterProfileBindingSpec{
				ProfileRef:      core.LocalObjectReference{Name: profile.Name},
				ClusterMetadata: clusterMetadata,
			},
		}

		var profileBindingList profilev1alpha1.ManagedClusterProfileBindingList
		err = r.Client.List(ctx, &profileBindingList, &client.ListOptions{
			Namespace: cluster.Name,
		})
		if err != nil {
			return reconcile.Result{}, err
		}

		if len(profileBindingList.Items) > 0 {
			profileBinding.Name = profileBindingList.Items[0].Name
		}

		_, err = cu.CreateOrPatch(context.Background(), r.Client, profileBinding, func(obj client.Object, createOp bool) client.Object {
			in := obj.(*profilev1alpha1.ManagedClusterProfileBinding)
			in.Labels = profileBinding.Labels
			in.OwnerReferences = profileBinding.OwnerReferences
			in.Spec = profileBinding.Spec
			return in
		})
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *ManagedClusterSetProfileReconciler) mapManagedClusterSetToProfile(ctx context.Context, obj client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)
	managedClusterSet, ok := obj.(*clusterv1beta2.ManagedClusterSet)
	if !ok {
		return nil
	}

	profileList := &profilev1alpha1.ManagedClusterSetProfileList{}
	err := r.List(ctx, profileList)
	if err != nil {
		logger.Error(err, "Failed to list ManagedClusterSetRoleBinding objects")
		return nil
	}

	var requests []reconcile.Request
	for _, profile := range profileList.Items {
		if profile.Name == managedClusterSet.GetName() {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: profile.Name,
				},
			})
			logger.Info("Enqueuing request", "name", profile.Name)
		}
	}

	return requests
}

func (r *ManagedClusterSetProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&profilev1alpha1.ManagedClusterSetProfile{}).
		Watches(
			&clusterv1beta2.ManagedClusterSet{},
			handler.EnqueueRequestsFromMapFunc(r.mapManagedClusterSetToProfile),
		).
		Complete(r)
}
