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
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	webv1 "github.com/adyatlov/statik/api/v1"
	"github.com/go-logr/logr"
)

// StatikReconciler reconciles a Statik object
type StatikReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=web.dyatlov.net,resources=statiks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=web.dyatlov.net,resources=statiks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=web.dyatlov.net,resources=statiks/finalizers,verbs=update

// Reconcile implements the main reconciliation loop
func (r *StatikReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the Statik CR instance
	var statik webv1.Statik
	err := r.Get(ctx, req.NamespacedName, &statik)
	if err != nil {
		if errors.IsNotFound(err) {
			// Handle delete case
			log.Info("Statik resource deleted", "name", req.Name, "namespace", req.Namespace)
			return r.handleDelete(ctx, req.NamespacedName)
		}
		log.Error(err, "Failed to get Statik resource")
		return ctrl.Result{}, err
	}

	// Log Spec and Status for visibility
	logStatik(log, req, &statik)

	// Handle resource deletion
	if statik.ObjectMeta.DeletionTimestamp != nil {
		log.Info("Statik resource marked for deletion", "name", req.Name, "namespace", req.Namespace)
		return r.handleDelete(ctx, req.NamespacedName)
	}

	// Handle resource creation
	if statik.Status.DeploymentStatus == "" {
		log.Info("Handling creation of Statik resource", "name", req.Name, "namespace", req.Namespace)
		return r.handleCreate(ctx, &statik)
	}

	// Handle updates if CommitHash has changed
	if statik.Spec.CommitHash != statik.Status.DeployedCommitHash {
		log.Info("Handling update of Statik resource", "name", req.Name, "namespace", req.Namespace)
		return r.handleUpdate(ctx, &statik)
	}

	// No changes detected
	log.Info("No changes detected for Statik resource", "name", req.Name, "namespace", req.Namespace)
	return ctrl.Result{}, nil
}

// handleCreate manages the creation of both the Deployment, Service, and Ingress
func (r *StatikReconciler) handleCreate(ctx context.Context, statik *webv1.Statik) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Create deployment
	repoName := extractRepoName(statik.Spec.RepoURL)
	deploymentName := repoName + "-deployment"

	// Create the Deployment object
	deployment := createDeployment(deploymentName, repoName, statik)
	if err := ctrl.SetControllerReference(statik, deployment, r.Scheme); err != nil {
		log.Error(err, "Failed to set owner reference for Deployment")
		return ctrl.Result{}, err
	}

	// Apply the deployment
	if err := r.Client.Create(ctx, deployment); err != nil {
		log.Error(err, "Failed to create Deployment")
		return ctrl.Result{}, err
	}
	log.Info("Created Deployment", "Deployment.Name", deploymentName)

	// Create the Service for the deployment
	if err := r.createService(ctx, statik); err != nil {
		log.Error(err, "Failed to create Service for Deployment")
		return ctrl.Result{}, err
	}

	// Create the Ingress for the service
	if err := r.createIngress(ctx, statik); err != nil {
		log.Error(err, "Failed to create Ingress for Service")
		return ctrl.Result{}, err
	}

	// Update the Statik status
	statik.Status.DeployedCommitHash = statik.Spec.CommitHash
	statik.Status.DeploymentStatus = "Deployed"
	if err := r.Status().Update(ctx, statik); err != nil {
		log.Error(err, "Failed to update Statik status after creating Deployment")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// handleUpdate checks for changes in the Deployment and Service
func (r *StatikReconciler) handleUpdate(ctx context.Context, statik *webv1.Statik) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Update Deployment and Service if necessary
	repoName := extractRepoName(statik.Spec.RepoURL)
	deploymentName := repoName + "-deployment"

	// Fetch the deployment
	var deployment appsv1.Deployment
	err := r.Client.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: statik.Namespace}, &deployment)
	if err != nil {
		log.Error(err, "Failed to fetch existing Deployment for update", "Deployment.Name", deploymentName)
		return ctrl.Result{}, err
	}

	// Update environment variables if CommitHash or RepoURL changed
	updated := updateDeploymentEnv(&deployment, statik)
	if updated {
		if err := r.Client.Update(ctx, &deployment); err != nil {
			log.Error(err, "Failed to update Deployment", "Deployment.Name", deploymentName)
			return ctrl.Result{}, err
		}
		log.Info("Updated Deployment", "Deployment.Name", deploymentName)

		// Update the Statik status
		statik.Status.DeployedCommitHash = statik.Spec.CommitHash
		statik.Status.DeploymentStatus = "Updated"
		if err := r.Status().Update(ctx, statik); err != nil {
			log.Error(err, "Failed to update Statik status after updating Deployment")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// handleDelete deletes both the Deployment, Service, and Ingress
func (r *StatikReconciler) handleDelete(ctx context.Context, namespacedName types.NamespacedName) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Derive the deployment name
	repoName := extractRepoName(namespacedName.Name)
	deploymentName := repoName + "-deployment"

	// Delete the deployment
	var deployment appsv1.Deployment
	err := r.Client.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: namespacedName.Namespace}, &deployment)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Deployment not found, might already be deleted", "Deployment.Name", deploymentName)
		} else {
			log.Error(err, "Failed to fetch Deployment for deletion", "Deployment.Name", deploymentName)
			return ctrl.Result{}, err
		}
	} else if err := r.Client.Delete(ctx, &deployment); err != nil {
		log.Error(err, "Failed to delete Deployment", "Deployment.Name", deploymentName)
		return ctrl.Result{}, err
	}
	log.Info("Deleted Deployment", "Deployment.Name", deploymentName)

	// Delete the service
	if err := r.deleteService(ctx, namespacedName); err != nil {
		log.Error(err, "Failed to delete Service", "Service.Name", repoName+"-service")
		return ctrl.Result{}, err
	}

	// Delete the ingress
	if err := r.deleteIngress(ctx, namespacedName); err != nil {
		log.Error(err, "Failed to delete Ingress", "Ingress.Name", repoName+"-ingress")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// createService creates a Service of type ClusterIP for the Deployment
func (r *StatikReconciler) createService(ctx context.Context, statik *webv1.Statik) error {
	log := log.FromContext(ctx)
	repoName := extractRepoName(statik.Spec.RepoURL)
	serviceName := repoName + "-service"

	// Define the Service
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: statik.Namespace,
			Labels: map[string]string{
				"app": repoName,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": repoName,
			},
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt(80),
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	// Set owner reference
	if err := ctrl.SetControllerReference(statik, service, r.Scheme); err != nil {
		log.Error(err, "Failed to set owner reference for Service")
		return err
	}

	// Create the Service
	if err := r.Client.Create(ctx, service); err != nil {
		log.Error(err, "Failed to create Service")
		return err
	}

	log.Info("Created Service for Statik resource", "Service.Name", serviceName)
	return nil
}

// createIngress creates an Ingress for the corresponding Service
func (r *StatikReconciler) createIngress(ctx context.Context, statik *webv1.Statik) error {
	log := log.FromContext(ctx)

	repoName := extractRepoName(statik.Spec.RepoURL)
	serviceName := repoName + "-service"
	ingressName := repoName + "-ingress"
	hostName := repoName + ".dyatlov.net" // Static domain name

	// Define the Ingress object
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ingressName,
			Namespace: statik.Namespace,
			Annotations: map[string]string{
				"cert-manager.io/cluster-issuer":                  "letsencrypt",
				"nginx.ingress.kubernetes.io/cors-allow-headers":  "DNT,X-CustomHeader,Keep-Alive,User-Agent,X-Requested-With,If-Modified-Since,Cache-Control,Content-Type,Content-Range,Range",
				"nginx.ingress.kubernetes.io/cors-allow-methods":  "GET, PUT, POST, DELETE, PATCH, OPTIONS",
				"nginx.ingress.kubernetes.io/cors-allow-origin":   "*",
				"nginx.ingress.kubernetes.io/cors-expose-headers": "Content-Length,Content-Range",
				"nginx.ingress.kubernetes.io/enable-cors":         "true",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: stringPtr("public"),
			Rules: []networkingv1.IngressRule{
				{
					Host: hostName, // Here goes the host name
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: pathTypePtr(networkingv1.PathTypePrefix),
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: serviceName, // Corresponding service name
											Port: networkingv1.ServiceBackendPort{
												Number: 80,
											},
										},
									},
								},
							},
						},
					},
				},
			},
			TLS: []networkingv1.IngressTLS{
				{
					Hosts:      []string{hostName}, // Same hostname
					SecretName: "ingress-tls",
				},
			},
		},
	}

	// Set the owner reference so that the Ingress gets deleted when the Statik resource is deleted
	if err := ctrl.SetControllerReference(statik, ingress, r.Scheme); err != nil {
		log.Error(err, "Failed to set owner reference for Ingress")
		return err
	}

	// Create the Ingress in the Kubernetes cluster
	if err := r.Client.Create(ctx, ingress); err != nil {
		log.Error(err, "Failed to create Ingress")
		return err
	}

	log.Info("Created Ingress for Statik resource", "Ingress.Name", ingressName)
	return nil
}

// deleteService deletes a Service associated with the deployment
func (r *StatikReconciler) deleteService(ctx context.Context, namespacedName types.NamespacedName) error {
	log := log.FromContext(ctx)

	repoName := extractRepoName(namespacedName.Name)
	serviceName := repoName + "-service"

	// Fetch the service
	var service corev1.Service
	err := r.Client.Get(ctx, types.NamespacedName{Name: serviceName, Namespace: namespacedName.Namespace}, &service)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Service not found, might already be deleted", "Service.Name", serviceName)
			return nil
		}
		log.Error(err, "Failed to fetch Service for deletion", "Service.Name", serviceName)
		return err
	}

	// Delete the service
	if err := r.Client.Delete(ctx, &service); err != nil {
		log.Error(err, "Failed to delete Service", "Service.Name", serviceName)
		return err
	}

	log.Info("Deleted Service", "Service.Name", serviceName)
	return nil
}

// deleteIngress deletes an Ingress associated with the deployment
func (r *StatikReconciler) deleteIngress(ctx context.Context, namespacedName types.NamespacedName) error {
	log := log.FromContext(ctx)

	repoName := extractRepoName(namespacedName.Name)
	ingressName := repoName + "-ingress"

	// Fetch the ingress
	var ingress networkingv1.Ingress
	err := r.Client.Get(ctx, types.NamespacedName{Name: ingressName, Namespace: namespacedName.Namespace}, &ingress)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Ingress not found, might already be deleted", "Ingress.Name", ingressName)
			return nil
		}
		log.Error(err, "Failed to fetch Ingress for deletion", "Ingress.Name", ingressName)
		return err
	}

	// Delete the ingress
	if err := r.Client.Delete(ctx, &ingress); err != nil {
		log.Error(err, "Failed to delete Ingress", "Ingress.Name", ingressName)
		return err
	}

	log.Info("Deleted Ingress", "Ingress.Name", ingressName)
	return nil
}

// Helper function to log the Statik resource's spec and status
func logStatik(log logr.Logger, req ctrl.Request, statik *webv1.Statik) {
	specJSON, _ := json.MarshalIndent(statik.Spec, "", "  ")
	statusJSON, _ := json.MarshalIndent(statik.Status, "", "  ")
	log.Info("Reconciling Statik resource",
		"name", req.Name,
		"namespace", req.Namespace,
		"spec", string(specJSON),
		"status", string(statusJSON),
	)
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

// Helper function to create a Deployment object
func createDeployment(deploymentName, repoName string, statik *webv1.Statik) *appsv1.Deployment {
	return &appsv1.Deployment{
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
}

// Helper function to update deployment environment variables if necessary
func updateDeploymentEnv(deployment *appsv1.Deployment, statik *webv1.Statik) bool {
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
	return updated
}

// Helper function to return a pointer to a string
func stringPtr(s string) *string {
	return &s
}

// Helper function to return a pointer to a PathType
func pathTypePtr(pathType networkingv1.PathType) *networkingv1.PathType {
	return &pathType
}

// Helper function to return a pointer to an int32
func int32Ptr(i int32) *int32 {
	return &i
}

// SetupWithManager sets up the controller with the Manager.
func (r *StatikReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webv1.Statik{}).
		Complete(r)
}
