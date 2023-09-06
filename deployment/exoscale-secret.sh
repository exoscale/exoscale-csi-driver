#!/bin/sh

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: exoscale-csi-credentials
  namespace: kube-system
type: Opaque
data:
  EXOSCALE_API_KEY: '$(printf "%s" "$EXOSCALE_API_KEY" | base64)'
  EXOSCALE_API_SECRET: '$(printf "%s" "$EXOSCALE_API_SECRET" | base64)'
EOF
