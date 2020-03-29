#!/usr/bin/env bash
set -eE -o pipefail

WORKSPACE="$(cd "$(dirname "${BASH_SOURCE[0]}")"/.. && pwd)"

source $WORKSPACE/scripts/spruce_merge.sh

function printerr() {
    local str=$1
    RED='\033[0;31m'
    NC='\033[0m'
    printf "$RED[ERROR] $str$NC\n"
}

function usage() {
   printf "Usage: \n  $0 [folder-of-properties.yml]\n"
}

function deploy_namespace(){
    echo "Checking current cluster context"
    if ! kubectl config current-context; then
        printerr "Getting current-context failed. Please export kubeconfig for controlplane cluster."
        exit 1
    fi

    kubectl apply -f 000-namespace.yaml
}

function deploy_ibm_cloud_operator(){
    kubectl apply -f ibm-cloud-operator/
    kubectl patch CustomResourceDefinition bindings.ibmcloud.ibm.com -p '{"metadata":{"annotations":{"servicebindingoperator.redhat.io/status.secretName":"binding:env:object:secret","servicebindingoperator.redhat.io/spec.serviceName": "binding:env:attribute"}}}' 
}

function deploy_service_binding_operator(){

    echo "Deploy service binding request CRD"
    kubectl apply -f crds/

    echo "Create/Update ServiceAccount Role RoleBinding for service-binding-operator"
    kubectl apply -f sa-role-binding.yaml

    echo "Create/Update Service for service-binding-operator"
    kubectl apply -f operator.yaml

}

if [ $# -lt 1 ]; then
  printerr "Invalid argument"
  usage
  exit 1
fi
##################################################################
# Start of main function
##################################################################
merge "$@"
deploy_namespace
deploy_ibm_cloud_operator
deploy_service_binding_operator