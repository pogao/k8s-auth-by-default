package webhook

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestHandleAdmission(t *testing.T) {
	tests := []struct {
		name            string
		kind            metav1.GroupVersionKind
		object          runtime.Object
		expectedPatch   string
		expectedAllowed bool
	}{
		{
			name: "Ingress: No annotations",
			kind: metav1.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
			object: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ingress",
				},
			},
			expectedPatch:   `[{"op":"add","path":"/metadata/annotations","value":{"annotations.pedro.wtf/oauth":"true"}}]`,
			expectedAllowed: true,
		},
		{
			name: "Ingress: Nil annotations map",
			kind: metav1.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
			object: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-ingress",
					Annotations: nil,
				},
			},
			expectedPatch:   `[{"op":"add","path":"/metadata/annotations","value":{"annotations.pedro.wtf/oauth":"true"}}]`,
			expectedAllowed: true,
		},
		{
			name: "Ingress: Existing other annotations",
			kind: metav1.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
			object: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ingress",
					Annotations: map[string]string{
						"other-annotation": "value",
					},
				},
			},
			expectedPatch:   `[{"op":"add","path":"/metadata/annotations/annotations.pedro.wtf~1oauth","value":"true"}]`,
			expectedAllowed: true,
		},
		{
			name: "Ingress: OAuth annotation already set to true",
			kind: metav1.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
			object: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ingress",
					Annotations: map[string]string{
						OAuthAnnotation: "true",
					},
				},
			},
			expectedPatch:   "",
			expectedAllowed: true,
		},
		{
			name: "Ingress: OAuth annotation already set to false",
			kind: metav1.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
			object: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ingress",
					Annotations: map[string]string{
						OAuthAnnotation: "false",
					},
				},
			},
			expectedPatch:   "",
			expectedAllowed: true,
		},
		{
			name: "Service: No annotations",
			kind: metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"},
			object: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-service",
				},
			},
			expectedPatch:   `[{"op":"add","path":"/metadata/annotations","value":{"beta.cloud.google.com/backend-config":"{\"default\": \"iap-config\"}"}}]`,
			expectedAllowed: true,
		},
		{
			name: "Service: Existing backend config",
			kind: metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"},
			object: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-service",
					Annotations: map[string]string{
						BackendConfigAnnotation: "existing-config",
					},
				},
			},
			expectedPatch:   "",
			expectedAllowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawObject, err := json.Marshal(tt.object)
			assert.NoError(t, err)

			ar := admissionv1.AdmissionReview{
				Request: &admissionv1.AdmissionRequest{
					UID:  "12345",
					Kind: tt.kind,
					Object: runtime.RawExtension{
						Raw: rawObject,
					},
				},
			}
			rawAR, err := json.Marshal(ar)
			assert.NoError(t, err)

			req := httptest.NewRequest("POST", "/mutate", strings.NewReader(string(rawAR)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			h := NewHandler(logger)
			h.HandleAdmission(w, req)

			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			var admissionReview admissionv1.AdmissionReview
			err = json.NewDecoder(resp.Body).Decode(&admissionReview)
			assert.NoError(t, err)
			assert.NotNil(t, admissionReview.Response)
			assert.Equal(t, tt.expectedAllowed, admissionReview.Response.Allowed)

			if tt.expectedPatch != "" {
				assert.NotNil(t, admissionReview.Response.Patch)
				assert.JSONEq(t, tt.expectedPatch, string(admissionReview.Response.Patch))
			} else {
				assert.Nil(t, admissionReview.Response.Patch)
			}
		})
	}
}
