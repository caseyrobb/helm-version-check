[![Docker Repository on Quay](https://quay.io/repository/carobb/helm-version-check/status "Docker Repository on Quay")](https://quay.io/repository/carobb/helm-version-check)
# helm-version-check

This project provides a tool to list ArgoCD applications that use Helm as their source. It retrieves and displays the Helm chart name, repository URL, and chart version for each application and compares the version to the latest upstream.

## Prerequisites

- Kubernetes cluster with ArgoCD installed
- ArgoCD Application using Helm as a source

## Dashboard
![alt text](https://raw.githubusercontent.com/caseyrobb/helm-version-check/master/dashboard.png)
