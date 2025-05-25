#!/bin/bash

# Function to execute when SIGINT is caught
ctrl_c() {
    echo ""
    echo "CTRL+C was pressed. Cleaning up..."    

    exit 0
}

# Trap SIGINT (Ctrl-C) and call ctrl_c() function
trap ctrl_c INT

# Check if minikube is running
if minikube status &> /dev/null 
then
  echo "Minikube is running. Stopping it now..."
  minikube stop
fi

echo "Starting minikube..."

minikube start --container-runtime containerd \
  --extra-config=controller-manager.horizontal-pod-autoscaler-upscale-delay=1m \
  --extra-config=controller-manager.horizontal-pod-autoscaler-downscale-delay=1m \
  --extra-config=controller-manager.horizontal-pod-autoscaler-sync-period=10s  \
  --extra-config=controller-manager.horizontal-pod-autoscaler-downscale-stabilization=1m

minikube addons enable metrics-server

echo "Loading image"

minikube image load dummyload:latest

echo "Applying YAMLs at ./k8s/"

kubectl apply -f ./k8s/

# Set the desired metric resolution
METRIC_RESOLUTION=${1:-"10s"}

# Edit the metrics-server deployment to change the metric resolution
kubectl -n kube-system get deployments.apps metrics-server -o yaml | \
sed "s/--metric-resolution=[^ ]*/--metric-resolution=${METRIC_RESOLUTION}/" | \
kubectl apply -f -

echo "Metric resolution set to ${METRIC_RESOLUTION}. Please wait for changes to take effect."

minikube dashboard --url=true