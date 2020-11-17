Feature: Reconcile when BackingService CR got deleted and recreated

    As a user of Service Binding Operator
    I want the SBR to be reconciled when there backend service was deleted and created again

    Background:
        Given Namespace [TEST_NAMESPACE] is used
        * Service Binding Operator is running
        * PostgreSQL DB operator is installed 
    Scenario: Reconcile when BackingService CR got deleted and recreated 
        Given OLM Operator "backend-new-spec" is running
        * The Custom Resource is present
            """
            apiVersion: "stable.example.com/v1"
            kind: Backend
            metadata:
                name: backend-demo
            spec:
                host: example.common
            """
        * Nodejs application "node-todo-git" imported from "quay.io/pmacik/node-todo" image is running
        When Service Binding is applied
            """
            apiVersion: operators.coreos.com/v1alpha1
            kind: ServiceBinding
            metadata:
                name: binding-request-backend
            spec:
                application:
                    group: apps
                    version: v1
                    resource: deployments
                    name: node-todo-git
                services:
                -   group: stable.example.com
                    version: v1
                    kind: Backend
                    name: backend-demo
            """
        Then jq ".status.conditions[] | select(.type=="CollectionReady").status" of Service Binding "binding-request-backend" should be changed to "True"
        And jq ".status.conditions[] | select(.type=="InjectionReady").status" of Service Binding "binding-request-backend" should be changed to "True"
        And jq ".status.conditions[] | select(.type=="Ready").status" of Service Binding "binding-request-backend" should be changed to "True"
        And Secret "binding-request-backend" contains "BACKEND_HOST" key with value "example.common"
        When BackingService "backend-demo" is deleted       
        And The Custom Resource is present
            """
            apiVersion: "stable.example.com/v1"
            kind: Backend
            metadata:
                name: backend-demo
            spec:
                host: example.common
            """
        Then jq ".status.conditions[] | select(.type=="CollectionReady").status" of Service Binding "binding-request-backend" should be changed to "True"
        And jq ".status.conditions[] | select(.type=="InjectionReady").status" of Service Binding "binding-request-backend" should be changed to "True"
        And jq ".status.conditions[] | select(.type=="Ready").status" of Service Binding "binding-request-backend" should be changed to "True"
        And Secret "binding-request-backend" contains "BACKEND_HOST" key with value "example.common"