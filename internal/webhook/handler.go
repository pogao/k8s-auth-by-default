package webhook

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

const (
	OAuthAnnotation         = "annotations.pedro.wtf/oauth"
	BackendConfigAnnotation = "beta.cloud.google.com/backend-config"
)

var (
	RuntimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(RuntimeScheme)
	deserializer  = codecs.UniversalDeserializer()
)

func init() {
	_ = admissionv1.AddToScheme(RuntimeScheme)
	_ = admissionv1.AddToScheme(RuntimeScheme)
	_ = networkingv1.AddToScheme(RuntimeScheme)
	_ = corev1.AddToScheme(RuntimeScheme)
}

type PatchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

type Handler struct {
	Logger *slog.Logger
}

func NewHandler(logger *slog.Logger) *Handler {
	return &Handler{
		Logger: logger,
	}
}

func (h *Handler) HandleAdmission(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		if data, err := io.ReadAll(r.Body); err == nil {
			body = data
		}
	}

	if len(body) == 0 {
		h.Logger.Error("empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	// Verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		h.Logger.Error("invalid content type", "expected", "application/json", "actual", contentType)
		http.Error(w, "invalid Content-Type, expect `application/json`", http.StatusUnsupportedMediaType)
		return
	}

	var admissionResponse *admissionv1.AdmissionResponse
	ar := admissionv1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &ar); err != nil {
		h.Logger.Error("could not decode body", "error", err)
		admissionResponse = &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	} else {
		if ar.Request.Kind.Kind == "Ingress" {
			admissionResponse = h.mutateIngress(&ar)
		} else if ar.Request.Kind.Kind == "Service" {
			admissionResponse = h.mutateService(&ar)
		} else {
			h.Logger.Info("ignoring unknown kind", "kind", ar.Request.Kind.Kind)
			admissionResponse = &admissionv1.AdmissionResponse{
				Allowed: true,
			}
		}
	}

	admissionReview := admissionv1.AdmissionReview{}
	if admissionResponse != nil {
		admissionReview.Response = admissionResponse
		if ar.Request != nil {
			admissionReview.Response.UID = ar.Request.UID
		}
	}

	// We need to set the TypeMeta for the response to be valid
	admissionReview.TypeMeta = metav1.TypeMeta{
		Kind:       "AdmissionReview",
		APIVersion: "admission.k8s.io/v1",
	}

	resp, err := json.Marshal(admissionReview)
	if err != nil {
		h.Logger.Error("could not encode response", "error", err)
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
		return
	}

	if _, err := w.Write(resp); err != nil {
		h.Logger.Error("could not write response", "error", err)
		http.Error(w, fmt.Sprintf("could not write response: %v", err), http.StatusInternalServerError)
		return
	}
}

func (h *Handler) mutateIngress(ar *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	req := ar.Request

	logger := h.Logger.With(
		slog.String("uid", string(req.UID)),
		slog.String("kind", req.Kind.Kind),
		slog.String("namespace", req.Namespace),
		slog.String("name", req.Name),
		slog.String("operation", string(req.Operation)),
	)

	var ingress networkingv1.Ingress
	if err := json.Unmarshal(req.Object.Raw, &ingress); err != nil {
		logger.Error("could not unmarshal ingress", "error", err)
		return &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	logger.Info("processing ingress")

	// Determine if we need to patch
	// If annotation exists, do nothing (respect user choice)
	// If annotation missing, add it with "true"

	var patches []PatchOperation

	if ingress.Annotations == nil {
		logger.Info("annotations are nil, initializing and adding default")
		patches = append(patches, PatchOperation{
			Op:    "add",
			Path:  "/metadata/annotations",
			Value: map[string]string{OAuthAnnotation: "true"},
		})
	} else {
		if _, ok := ingress.Annotations[OAuthAnnotation]; !ok {
			logger.Info("annotation missing, adding default")
			patches = append(patches, PatchOperation{
				Op:    "add",
				Path:  "/metadata/annotations/" + escapeJSONPointer(OAuthAnnotation),
				Value: "true",
			})
		} else {
			logger.Info("annotation present, skipping", "value", ingress.Annotations[OAuthAnnotation])
		}
	}

	reviewResponse := &admissionv1.AdmissionResponse{
		Allowed: true,
	}

	if len(patches) > 0 {
		patchBytes, err := json.Marshal(patches)
		if err != nil {
			logger.Error("could not marshal patches", "error", err)
			return &admissionv1.AdmissionResponse{
				Result: &metav1.Status{
					Message: err.Error(),
				},
			}
		}
		reviewResponse.Patch = patchBytes
		pt := admissionv1.PatchTypeJSONPatch
		reviewResponse.PatchType = &pt
		logger.Info("patch generated", "patches", string(patchBytes))
	}

	return reviewResponse
}

func (h *Handler) mutateService(ar *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	req := ar.Request

	logger := h.Logger.With(
		slog.String("uid", string(req.UID)),
		slog.String("kind", req.Kind.Kind),
		slog.String("namespace", req.Namespace),
		slog.String("name", req.Name),
		slog.String("operation", string(req.Operation)),
	)

	var service corev1.Service
	if err := json.Unmarshal(req.Object.Raw, &service); err != nil {
		logger.Error("could not unmarshal service", "error", err)
		return &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	logger.Info("processing service")

	var patches []PatchOperation

	// Check for BackendConfig annotation
	// If missing, add it with value `{"default": "iap-config"}`
	defaultBackendConfig := `{"default": "iap-config"}`

	if service.Annotations == nil {
		logger.Info("annotations are nil, initializing and adding default backend config")
		patches = append(patches, PatchOperation{
			Op:    "add",
			Path:  "/metadata/annotations",
			Value: map[string]string{BackendConfigAnnotation: defaultBackendConfig},
		})
	} else {
		if _, ok := service.Annotations[BackendConfigAnnotation]; !ok {
			logger.Info("backend config annotation missing, adding default")
			patches = append(patches, PatchOperation{
				Op:    "add",
				Path:  "/metadata/annotations/" + escapeJSONPointer(BackendConfigAnnotation),
				Value: defaultBackendConfig,
			})
		} else {
			logger.Info("backend config annotation present, skipping", "value", service.Annotations[BackendConfigAnnotation])
		}
	}

	reviewResponse := &admissionv1.AdmissionResponse{
		Allowed: true,
	}

	if len(patches) > 0 {
		patchBytes, err := json.Marshal(patches)
		if err != nil {
			logger.Error("could not marshal patches", "error", err)
			return &admissionv1.AdmissionResponse{
				Result: &metav1.Status{
					Message: err.Error(),
				},
			}
		}
		reviewResponse.Patch = patchBytes
		pt := admissionv1.PatchTypeJSONPatch
		reviewResponse.PatchType = &pt
		logger.Info("patch generated", "patches", string(patchBytes))
	}

	return reviewResponse
}

// escapeJSONPointer escapes the forward slash in the annotation key for JSON Patch
func escapeJSONPointer(s string) string {
	// https://tools.ietf.org/html/rfc6901#section-3
	// ~ -> ~0
	// / -> ~1
	// We need to replace / with ~1 because it's a path separator
	// We also need to replace ~ with ~0
	// But standard strings.Replace is enough
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}
