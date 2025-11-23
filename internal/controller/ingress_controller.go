package controller

import (
	"context"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	OAuthAnnotation         = "annotations.pedro.wtf/oauth"
	BackendConfigAnnotation = "beta.cloud.google.com/backend-config"
)

type IngressReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Logger *slog.Logger
}

func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Logger.With("ingress", req.NamespacedName)

	var ingress networkingv1.Ingress
	if err := r.Get(ctx, req.NamespacedName, &ingress); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error("unable to fetch Ingress", "error", err)
		return ctrl.Result{}, err
	}

	// Check for opt-out annotation
	if val, ok := ingress.Annotations[OAuthAnnotation]; ok && val == "false" {
		logger.Info("ingress opted out of oauth, ensuring backend services are clean")
		if err := r.ensureServicesClean(ctx, &ingress, logger); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *IngressReconciler) ensureServicesClean(ctx context.Context, ingress *networkingv1.Ingress, logger *slog.Logger) error {
	services := getReferencedServices(ingress)
	for _, svcName := range services {
		var service corev1.Service
		nn := types.NamespacedName{Name: svcName, Namespace: ingress.Namespace}
		if err := r.Get(ctx, nn, &service); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			logger.Error("unable to fetch Service", "service", svcName, "error", err)
			return err
		}

		if _, ok := service.Annotations[BackendConfigAnnotation]; ok {
			logger.Info("removing backend config from service", "service", svcName)
			delete(service.Annotations, BackendConfigAnnotation)
			if err := r.Update(ctx, &service); err != nil {
				logger.Error("unable to update Service", "service", svcName, "error", err)
				return err
			}
		}
	}
	return nil
}

func getReferencedServices(ingress *networkingv1.Ingress) []string {
	var services []string
	for _, rule := range ingress.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			if path.Backend.Service != nil {
				services = append(services, path.Backend.Service.Name)
			}
		}
	}
	// Also check default backend
	if ingress.Spec.DefaultBackend != nil && ingress.Spec.DefaultBackend.Service != nil {
		services = append(services, ingress.Spec.DefaultBackend.Service.Name)
	}
	return services
}

func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(r)
}
