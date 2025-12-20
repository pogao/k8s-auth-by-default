package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestWebhookE2E(t *testing.T) {
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "webhook")
	buildCmd := exec.Command("go", "build", "-o", binaryPath, "../../cmd/webhook")
	output, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "Failed to build webhook binary: %s", string(output))

	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	caCertPEM := generateSelfSignedCert(t, certPath, keyPath)

	port := getFreePort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath,
		"-port", fmt.Sprintf("%d", port),
		"-tlsCertFile", certPath,
		"-tlsKeyFile", keyPath,
	)
	// Capture stdout/stderr for debugging
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Start()
	require.NoError(t, err, "Failed to start webhook server")

	// Ensure process is killed on exit
	defer func() {
		cancel()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("Webhook Stdout:\n%s", stdout.String())
			t.Logf("Webhook Stderr:\n%s", stderr.String())
		}
	}()

	baseURL := fmt.Sprintf("https://localhost:%d", port)
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: func() *x509.CertPool {
					pool := x509.NewCertPool()
					pool.AppendCertsFromPEM(caCertPEM)
					return pool
				}(),
			},
		},
		Timeout: 2 * time.Second,
	}

	require.Eventually(t, func() bool {
		// Just try to connect, even 404 is fine, means server is up
		// But we can also hit the mutate endpoint with bad data to check connectivity
		resp, err := client.Get(baseURL) // Method Not Allowed is expected
		if err == nil {
			resp.Body.Close()
			return true
		}
		// Connection refused error is what we expect while starting
		return false
	}, 10*time.Second, 100*time.Millisecond, "Server failed to start")

	t.Run("Mutate Ingress", func(t *testing.T) {
		ingress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ingress",
				Namespace: "default",
			},
		}
		rawIngress, err := json.Marshal(ingress)
		require.NoError(t, err)

		ar := admissionv1.AdmissionReview{
			Request: &admissionv1.AdmissionRequest{
				UID: "12345",
				Kind: metav1.GroupVersionKind{
					Group:   "networking.k8s.io",
					Version: "v1",
					Kind:    "Ingress",
				},
				Object: runtime.RawExtension{
					Raw: rawIngress,
				},
			},
		}
		rawAR, err := json.Marshal(ar)
		require.NoError(t, err)

		resp, err := client.Post(baseURL+"/mutate", "application/json", bytes.NewReader(rawAR))
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var admissionResp admissionv1.AdmissionReview
		err = json.Unmarshal(body, &admissionResp)
		require.NoError(t, err)

		require.True(t, admissionResp.Response.Allowed)
		require.NotNil(t, admissionResp.Response.Patch)

		expectedPatch := `[{"op":"add","path":"/metadata/annotations","value":{"annotations.pedro.wtf/oauth":"true"}}]`
		assert.JSONEq(t, expectedPatch, string(admissionResp.Response.Patch))
	})

	t.Run("Mutate Service", func(t *testing.T) {
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service",
				Namespace: "default",
			},
		}
		rawService, err := json.Marshal(service)
		require.NoError(t, err)

		ar := admissionv1.AdmissionReview{
			Request: &admissionv1.AdmissionRequest{
				UID: "67890",
				Kind: metav1.GroupVersionKind{
					Group:   "",
					Version: "v1",
					Kind:    "Service",
				},
				Object: runtime.RawExtension{
					Raw: rawService,
				},
			},
		}
		rawAR, err := json.Marshal(ar)
		require.NoError(t, err)

		resp, err := client.Post(baseURL+"/mutate", "application/json", bytes.NewReader(rawAR))
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var admissionResp admissionv1.AdmissionReview
		err = json.Unmarshal(body, &admissionResp)
		require.NoError(t, err)

		require.True(t, admissionResp.Response.Allowed)
		require.NotNil(t, admissionResp.Response.Patch)

		expectedPatch := `[{"op":"add","path":"/metadata/annotations","value":{"beta.cloud.google.com/backend-config":"{\"default\": \"iap-config\"}"}}]`
		assert.JSONEq(t, expectedPatch, string(admissionResp.Response.Patch))
	})

	t.Run("Controller Opt-Out Logic", func(t *testing.T) {
		// Since we don't have a real K8s cluster in this environment, we should add a UNIT TEST for the controller instead.
		t.Skip("Skipping controller E2E test as it requires a real K8s cluster")
	})
}

func getFreePort(t *testing.T) int {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	require.NoError(t, err)
	l, err := net.ListenTCP("tcp", addr)
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func generateSelfSignedCert(t *testing.T, certPath, keyPath string) []byte {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Org"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(time.Hour),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	require.NoError(t, err)

	certOut, err := os.Create(certPath)
	require.NoError(t, err)
	defer certOut.Close()
	require.NoError(t, pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}))

	keyOut, err := os.Create(keyPath)
	require.NoError(t, err)
	defer keyOut.Close()
	require.NoError(t, pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}))

	var certPEM bytes.Buffer
	require.NoError(t, pem.Encode(&certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}))
	return certPEM.Bytes()
}
