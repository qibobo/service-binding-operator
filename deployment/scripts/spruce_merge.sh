#!/usr/bin/env bash
set -eE -o pipefail

WORKSPACE="$(cd "$(dirname "${BASH_SOURCE[0]}")"/.. && pwd)"

function merge() {

    if [[ -z "$1" ]]; then
      echo "Missing the property yaml filepath. Please provide the right path."
      exit 42
    fi 

    local properties_path=$1/properties.yml

    NAMESPACE=$(goml get -f ${properties_path} -p 'environment.namespace')

    if [ $NAMESPACE = "null" ]; then
       echo "failed to get target namespace"
       exit 42
    fi

    export NAMESPACE


    if ! [ -e $properties_path ];then
      echo "property file $properties_path does not exist."
      exit 43 
    fi
    
    echo "Spruce merging the files"

    spruce merge --prune environment ${properties_path} ${WORKSPACE}/300-operator.yaml | tee operator.yaml
    spruce fan   --prune environment ${properties_path} ${WORKSPACE}/200-clusterrole.yaml \
                                                        ${WORKSPACE}/200-clusterrolebinding.yaml \
                                                        ${WORKSPACE}/200-serviceaccount.yaml |tee sa-role-binding.yaml
}

merge $@
