# Kubernetes Ingress OAuth Mutating Webhook

This project implements a Kubernetes Mutating Admission Webhook that enforces a default OAuth policy on Ingress resources.

## Overview

The controller intercepts `CREATE` and `UPDATE` operations for `Ingress` resources. It checks for the presence of the `annotations.pedro.wtf/oauth` annotation.

- If the annotation is **missing**, the controller adds it with the value `"true"`.
- If the annotation is **present**, the controller leaves it unchanged, respecting the user's configuration.

The controller also intercepts `CREATE` and `UPDATE` operations for `Service` resources to enable GCP Identity-Aware Proxy (IAP).

- It checks for the `beta.cloud.google.com/backend-config` annotation.
- If missing, it adds the annotation with the value `{"default": "iap-config"}`.

This ensures that by default, all Ingresses are protected by OAuth, and Services are configured for IAP.

Additionally, a **Controller** runs in the background to ensure consistency:
- If an Ingress explicitly opts-out of OAuth (via `annotations.pedro.wtf/oauth: "false"`), the controller **removes** the IAP annotation from the referenced backend Services.
- This ensures that public applications do not have broken IAP configurations attached to their services.

## Prerequisites

- Kubernetes cluster (v1.19+)
- `kubectl` configured to communicate with your cluster
- `openssl` (for generating certificates via the deploy script)
- Go 1.25.4+ (for building locally)

## Building Locally

To build the webhook server:

```bash
go build ./...
```

To run tests:

```bash
go test ./...
```

To run end-to-end (E2E) tests:

```bash
go test -v ./test/e2e/...
```

## Deployment

We provide a helper script to generate self-signed TLS certificates and deploy the webhook to your cluster.

### 1. Build the Docker Image

First, you need to build the Docker image. If you are using a local cluster like Minikube or Kind, make sure to point your Docker client to the cluster's Docker daemon or load the image manually.

```bash
docker build -t k8s-auth-by-default:latest .
```

### 2. Run the Deployment Script

The `scripts/deploy.sh` script will:
1. Generate a CA and a server certificate for the webhook.
2. Create a Kubernetes Secret `k8s-auth-by-default-certs` with the certificates.
3. Apply the necessary RBAC roles for the controller.
4. Apply the Deployment and Service.
5. Patch the `MutatingWebhookConfiguration` with the CA bundle and apply it.

```bash
./scripts/deploy.sh
```

## GCP IAP Integration

To fully enable IAP, you must ensure the following resources exist in your cluster:

1.  **BackendConfig**: A `BackendConfig` resource named `iap-config` (or matching the default injected value).
2.  **Secret**: A Kubernetes Secret containing your OAuth credentials.

> [!IMPORTANT]
> **This controller does NOT provision these secrets.**
> You must create the `iap-config` and the OAuth secret in **every namespace** where you deploy applications that need IAP.
> If these resources are missing, the IAP configuration on the Service will be invalid, and your Load Balancer may fail to provision or configure correctly.

Example `iap-config.yaml`:

```yaml
apiVersion: cloud.google.com/v1
kind: BackendConfig
metadata:
  name: iap-config
  namespace: default
spec:
  iap:
    enabled: true
    oauthclientCredentials:
      secretName: iap-oauth-secret
```

## Configuration

The webhook behavior is controlled by the `annotations.pedro.wtf/oauth` annotation on Ingress resources.

| Annotation Value | Description |
|------------------|-------------|
| `"true"`         | Enables OAuth authentication (Default). |
| `"false"`        | Disables OAuth authentication. |

### Example

To opt-out of OAuth for a specific Ingress:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: public-app
  annotations:
    annotations.pedro.wtf/oauth: "false"
spec:
  rules:
  - host: public.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: public-service
            port:
              number: 80
```

## Troubleshooting

### Check Logs

The webhook uses structured logging in JSON format. You can view the logs using `kubectl logs`:

```bash
kubectl logs -l app=k8s-auth-by-default -f
```

### Verify Webhook Configuration

Ensure the `MutatingWebhookConfiguration` is present and has the correct CA bundle:

```bash
kubectl get mutatingwebhookconfiguration k8s-auth-by-default -o yaml
```
