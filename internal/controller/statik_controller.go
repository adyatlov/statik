/*
Copyright 2024.

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
	"encoding/json"
	"net/url"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	webv1 "github.com/adyatlov/statik/api/v1"
)

// StatikReconciler reconciles a Statik object
type StatikReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=web.dyatlov.net,resources=statiks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=web.dyatlov.net,resources=statiks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=web.dyatlov.net,resources=statiks/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Statik object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *StatikReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the Statik CR instance
	var statik webv1.Statik
	err := r.Get(ctx, req.NamespacedName, &statik)
	if err != nil {
		if errors.IsNotFound(err) {
			// Handle the delete case (CR has been deleted)
			log.Info("Statik resource deleted", "name", req.Name, "namespace", req.Namespace)
			return r.handleDelete(ctx, req.NamespacedName)
		}
		// Other errors, requeue the request
		log.Error(err, "Failed to get Statik resource")
		return ctrl.Result{}, err
	}

	// Log the Spec and Status
	specJSON, _ := json.MarshalIndent(statik.Spec, "", "  ")
	statusJSON, _ := json.MarshalIndent(statik.Status, "", "  ")
	log.Info("Reconciling Statik resource",
		"name", req.Name,
		"namespace", req.Namespace,
		"spec", string(specJSON),
		"status", string(statusJSON),
	)

	// Check if the resource is marked for deletion
	if statik.ObjectMeta.DeletionTimestamp != nil {
		// Handle finalization logic
		log.Info("Statik resource marked for deletion", "name", req.Name, "namespace", req.Namespace)
		return r.handleDelete(ctx, req.NamespacedName)
	}

	// Check if the resource is being created
	if statik.Status.DeploymentStatus == "" {
		// Handle creation logic
		log.Info("Handling creation of Statik resource", "name", req.Name, "namespace", req.Namespace)
		return r.handleCreate(ctx, &statik)
	}

	// Handle updates if the CommitHash has changed
	if statik.Spec.CommitHash != statik.Status.DeployedCommitHash {
		log.Info("Handling update of Statik resource", "name", req.Name, "namespace", req.Namespace)
		return r.handleUpdate(ctx, &statik)
	}

	// No changes detected, no need to requeue
	log.Info("No changes detected for Statik resource", "name", req.Name, "namespace", req.Namespace)
	return ctrl.Result{}, nil
}

func (r *StatikReconciler) handleCreate(ctx context.Context, statik *webv1.Statik) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Extract the repository name from the RepoURL
	repoName := extractRepoName(statik.Spec.RepoURL)
	deploymentName := repoName + "-deployment"

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: statik.Namespace,
			Labels: map[string]string{
				"app": repoName,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1), // Create a single replica (pod)
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": repoName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": repoName,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "statik-nginx",
							Image: "adyatlov/statik-nginx:1.1.0",
							Env: []corev1.EnvVar{
								{
									Name:  "STATIK_REPO",
									Value: statik.Spec.RepoURL, // Use the RepoURL from the CR Spec
								},
								{
									Name:  "STATIK_COMMIT_HASH",
									Value: statik.Spec.CommitHash, // Use the CommitHash from the CR Spec
								},
							},
						},
					},
				},
			},
		},
	}

	// Set the owner reference so that the deployment gets deleted when the Statik resource is deleted
	if err := ctrl.SetControllerReference(statik, deployment, r.Scheme); err != nil {
		log.Error(err, "Failed to set owner reference for deployment")
		return ctrl.Result{}, err
	}

	// Create the Deployment in the Kubernetes cluster
	err := r.Client.Create(ctx, deployment)
	if err != nil {
		log.Error(err, "Failed to create Deployment")
		return ctrl.Result{}, err
	}

	log.Info("Created Deployment for Statik resource", "Deployment.Name", deployment.Name)

	// Update the Statik status after successful deployment
	statik.Status.DeployedCommitHash = statik.Spec.CommitHash
	statik.Status.DeploymentStatus = "Deployed"
	if err := r.Status().Update(ctx, statik); err != nil {
		log.Error(err, "Failed to update Statik status after creating Deployment")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *StatikReconciler) handleUpdate(ctx context.Context, statik *webv1.Statik) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Derive the deployment name from the repo URL
	repoName := extractRepoName(statik.Spec.RepoURL)
	deploymentName := repoName + "-deployment"

	// Fetch the existing deployment
	var deployment appsv1.Deployment
	err := r.Client.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: statik.Namespace}, &deployment)
	if err != nil {
		log.Error(err, "Failed to fetch existing Deployment for update", "Deployment.Name", deploymentName)
		return ctrl.Result{}, err
	}

	// Update the deployment if the commit hash has changed
	updated := false
	for i, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == "statik-nginx" {
			// Update environment variables if necessary
			for j, envVar := range container.Env {
				if envVar.Name == "STATIK_COMMIT_HASH" && envVar.Value != statik.Spec.CommitHash {
					deployment.Spec.Template.Spec.Containers[i].Env[j].Value = statik.Spec.CommitHash
					updated = true
				}
				if envVar.Name == "STATIK_REPO" && envVar.Value != statik.Spec.RepoURL {
					deployment.Spec.Template.Spec.Containers[i].Env[j].Value = statik.Spec.RepoURL
					updated = true
				}
			}
		}
	}

	if updated {
		// Update the deployment in the cluster
		if err := r.Client.Update(ctx, &deployment); err != nil {
			log.Error(err, "Failed to update Deployment", "Deployment.Name", deploymentName)
			return ctrl.Result{}, err
		}
		log.Info("Updated Deployment with new CommitHash or RepoURL", "Deployment.Name", deploymentName)

		// Update the Statik status after successful update
		statik.Status.DeployedCommitHash = statik.Spec.CommitHash
		statik.Status.DeploymentStatus = "Updated"
		if err := r.Status().Update(ctx, statik); err != nil {
			log.Error(err, "Failed to update Statik status after updating Deployment")
			return ctrl.Result{}, err
		}
	} else {
		log.Info("No changes detected for Deployment", "Deployment.Name", deploymentName)
	}

	return ctrl.Result{}, nil
}

func (r *StatikReconciler) handleDelete(ctx context.Context, namespacedName types.NamespacedName) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Derive the deployment name from the Statik CR name
	repoName := extractRepoName(namespacedName.Name)
	deploymentName := repoName + "-deployment"

	// Fetch the deployment
	var deployment appsv1.Deployment
	err := r.Client.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: namespacedName.Namespace}, &deployment)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Deployment not found for deletion, might have been already deleted", "Deployment.Name", deploymentName)
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to fetch Deployment for deletion", "Deployment.Name", deploymentName)
		return ctrl.Result{}, err
	}

	// Delete the deployment
	if err := r.Client.Delete(ctx, &deployment); err != nil {
		log.Error(err, "Failed to delete Deployment", "Deployment.Name", deploymentName)
		return ctrl.Result{}, err
	}

	log.Info("Deleted Deployment", "Deployment.Name", deploymentName)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *StatikReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webv1.Statik{}).
		Complete(r)
}

// Helper function to extract the repo name from the Git URL
func extractRepoName(repoURL string) string {
	u, err := url.Parse(repoURL)
	if err != nil {
		return "default-repo" // Fallback if URL parsing fails
	}

	parts := strings.Split(u.Path, "/")
	repo := parts[len(parts)-1]
	// Remove ".git" extension if present
	return strings.TrimSuffix(repo, ".git")
}

func int32Ptr(i int32) *int32 {
	return &i
}
