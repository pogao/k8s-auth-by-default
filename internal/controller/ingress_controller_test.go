package controller

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestIngressReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = networkingv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	tests := []struct {
		name             string
		ingress          *networkingv1.Ingress
		services         []*corev1.Service
		expectedServices []*corev1.Service
	}{
		{
			name: "Opt-out ingress removes annotation from service",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "opt-out-ingress",
					Namespace: "default",
					Annotations: map[string]string{
						OAuthAnnotation: "false",
					},
				},
				Spec: networkingv1.IngressSpec{
					Rules: []networkingv1.IngressRule{
						{
							IngressRuleValue: networkingv1.IngressRuleValue{
								HTTP: &networkingv1.HTTPIngressRuleValue{
									Paths: []networkingv1.HTTPIngressPath{
										{
											Backend: networkingv1.IngressBackend{
												Service: &networkingv1.IngressServiceBackend{
													Name: "backend-service",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			services: []*corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "backend-service",
						Namespace: "default",
						Annotations: map[string]string{
							BackendConfigAnnotation: `{"default": "iap-config"}`,
						},
					},
				},
			},
			expectedServices: []*corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "backend-service",
						Namespace:   "default",
						Annotations: nil,
					},
				},
			},
		},
		{
			name: "Opt-in ingress does nothing",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "opt-in-ingress",
					Namespace: "default",
					Annotations: map[string]string{
						OAuthAnnotation: "true",
					},
				},
				Spec: networkingv1.IngressSpec{
					Rules: []networkingv1.IngressRule{
						{
							IngressRuleValue: networkingv1.IngressRuleValue{
								HTTP: &networkingv1.HTTPIngressRuleValue{
									Paths: []networkingv1.HTTPIngressPath{
										{
											Backend: networkingv1.IngressBackend{
												Service: &networkingv1.IngressServiceBackend{
													Name: "backend-service",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			services: []*corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "backend-service",
						Namespace: "default",
						Annotations: map[string]string{
							BackendConfigAnnotation: `{"default": "iap-config"}`,
						},
					},
				},
			},
			expectedServices: []*corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "backend-service",
						Namespace: "default",
						Annotations: map[string]string{
							BackendConfigAnnotation: `{"default": "iap-config"}`,
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objs []client.Object
			objs = append(objs, tt.ingress)
			for _, svc := range tt.services {
				objs = append(objs, svc)
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

			r := &IngressReconciler{
				Client: cl,
				Scheme: scheme,
				Logger: logger,
			}

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.ingress.Name,
					Namespace: tt.ingress.Namespace,
				},
			}

			_, err := r.Reconcile(context.Background(), req)
			require.NoError(t, err)

			for _, expectedSvc := range tt.expectedServices {
				var actualSvc corev1.Service
				err := cl.Get(context.Background(), types.NamespacedName{Name: expectedSvc.Name, Namespace: expectedSvc.Namespace}, &actualSvc)
				require.NoError(t, err)
				assert.Equal(t, expectedSvc.Annotations, actualSvc.Annotations)
			}
		})
	}
}
