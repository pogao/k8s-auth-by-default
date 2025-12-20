#!/bin/bash

set -e

SERVICE_NAME=k8s-auth-by-default
NAMESPACE=default

# Create certs directory
mkdir -p certs

# Generate CA
openssl genrsa -out certs/ca.key 2048
openssl req -new -x509 -days 365 -key certs/ca.key -subj "/CN=K8s Auth By Default CA" -out certs/ca.crt

# Generate Server Key and CSR
openssl genrsa -out certs/server.key 2048
openssl req -new -key certs/server.key -subj "/CN=${SERVICE_NAME}.${NAMESPACE}.svc" -out certs/server.csr

# Create config file for SAN
cat > certs/csr.conf <<EOF
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
[req_distinguished_name]
[ v3_req ]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names
[alt_names]
DNS.1 = ${SERVICE_NAME}
DNS.2 = ${SERVICE_NAME}.${NAMESPACE}
DNS.3 = ${SERVICE_NAME}.${NAMESPACE}.svc
EOF

# Sign the CSR
openssl x509 -req -in certs/server.csr -CA certs/ca.crt -CAkey certs/ca.key -CAcreateserial -out certs/server.crt -days 365 -extensions v3_req -extfile certs/csr.conf

# Create Secret
kubectl create secret tls ${SERVICE_NAME}-certs \
    --cert=certs/server.crt \
    --key=certs/server.key \
    --dry-run=client -o yaml | kubectl apply -f -

# Build Docker image (assuming Docker is available and pointing to the cluster registry if needed)
# For Minikube/Kind, you might need to load the image manually.
# docker build -t k8s-auth-by-default:latest .

# Apply RBAC, Deployment, and Service
kubectl apply -f deploy/rbac.yaml
kubectl apply -f deploy/deployment.yaml
kubectl apply -f deploy/service.yaml

# Patch CA Bundle into Webhook Config and Apply
CA_BUNDLE=$(cat certs/ca.crt | base64 | tr -d '\n')
sed "s/caBundle: .*/caBundle: ${CA_BUNDLE}/" deploy/webhook-config.yaml > deploy/webhook-config-patched.yaml
# If caBundle line doesn't exist, we need to add it. The simple sed above assumes it exists or we append it.
# Better approach: use yq or just simple replacement if we know the structure.
# Let's just overwrite the clientConfig part using a template approach or just kubectl patch.

# Using kubectl patch is cleaner but requires the object to exist.
# Let's generate the final file with the CA bundle.

cat > deploy/webhook-config-patched.yaml <<EOF
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: k8s-auth-by-default
webhooks:
- name: k8s-auth-by-default.pedro.wtf
  clientConfig:
    service:
      name: k8s-auth-by-default
      namespace: default
      path: "/mutate"
    caBundle: ${CA_BUNDLE}
  rules:
  - operations: ["CREATE", "UPDATE"]
    apiGroups: ["networking.k8s.io"]
    apiVersions: ["v1"]
    resources: ["ingresses"]
  admissionReviewVersions: ["v1"]
  sideEffects: None
  timeoutSeconds: 5
EOF

kubectl apply -f deploy/webhook-config-patched.yaml

# Cleanup
rm -rf certs
rm deploy/webhook-config-patched.yaml

echo "Deployment complete!"
