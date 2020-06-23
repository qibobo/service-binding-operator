package servicebindingrequest

import (
	"fmt"

	"github.com/imdario/mergo"
	"github.com/redhat-developer/service-binding-operator/pkg/controller/servicebindingrequest/annotations"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	v1alpha1 "github.com/redhat-developer/service-binding-operator/pkg/apis/apps/v1alpha1"
	"github.com/redhat-developer/service-binding-operator/pkg/log"
)

// serviceContext contains information related to a service.
type serviceContext struct {
	// service is the resource of the service being evaluated.
	service *unstructured.Unstructured
	// envVars contains the service's contributed environment variables.
	envVars map[string]interface{}
	// volumeKeys contains the keys that should be mounted as volume from the binding secret.
	volumeKeys []string
	// envVarPrefix indicates the prefix to use in environment variables.
	envVarPrefix *string
}

// serviceContextList is a list of ServiceContext values.
type serviceContextList []*serviceContext

// getServices returns a slice of service unstructured objects contained in the collection.
func (sc serviceContextList) getServices() []*unstructured.Unstructured {
	var crs []*unstructured.Unstructured
	for _, s := range sc {
		crs = append(crs, s.service)
	}
	return crs
}

func stringValueOrDefault(val *string, defaultVal string) string {
	if val != nil && len(*val) > 0 {
		return *val
	}
	return defaultVal
}

// buildServiceContexts return a collection of ServiceContext values from the given service
// selectors.
func buildServiceContexts(
	logger *log.Log,
	client dynamic.Interface,
	defaultNs string,
	selectors []v1alpha1.BackingServiceSelector,
	includeServiceOwnedResources bool,
	restMapper meta.RESTMapper,
) (serviceContextList, error) {
	svcCtxs := make(serviceContextList, 0)

SELECTORS:
	for _, s := range selectors {
		ns := stringValueOrDefault(s.Namespace, defaultNs)
		gvk := schema.GroupVersionKind{Kind: s.Kind, Version: s.Version, Group: s.Group}
		svcCtx, err := buildServiceContext(logger.WithName("buildServiceContexts"), client, ns, gvk,
			s.ResourceRef, s.EnvVarPrefix, restMapper)

		if err != nil {
			// best effort approach; should not break in common cases such as a unknown annotation
			// prefix (other annotations might exist in the resource) or, in the case of a valid
			// annotation, the handler expected for the annotation can't be found.
			if err == annotations.ErrInvalidAnnotationPrefix || annotations.IsErrHandlerNotFound(err) {
				logger.Trace("Continuing to next selector", "Error", err)
				continue SELECTORS
			}
			return nil, err
		}
		svcCtxs = append(svcCtxs, svcCtx)

		if includeServiceOwnedResources {
			// use the selector's kind as owned resources environment variable prefix
			svcEnvVarPrefix := svcCtx.envVarPrefix
			if svcEnvVarPrefix == nil {
				svcEnvVarPrefix = &s.Kind
			}
			ownedResourcesCtxs, err := findOwnedResourcesCtxs(
				client,
				ns,
				svcCtx.service.GetName(),
				svcCtx.service.GetUID(),
				gvk,
				svcEnvVarPrefix,
				restMapper,
			)
			if err != nil {
				return nil, err
			}
			svcCtxs = append(svcCtxs, ownedResourcesCtxs...)
		}
	}
	fmt.Printf("-------------buildServiceContexts:  svcCtxs: %v\n", svcCtxs)
	return svcCtxs, nil
}

func findOwnedResourcesCtxs(
	client dynamic.Interface,
	ns string,
	name string,
	uid types.UID,
	gvk schema.GroupVersionKind,
	envVarPrefix *string,
	restMapper meta.RESTMapper,
) (serviceContextList, error) {
	ownedResources, err := getOwnedResources(
		client,
		ns,
		gvk,
		name,
		uid,
	)
	if err != nil {
		return nil, err
	}

	return buildOwnedResourceContexts(
		client,
		ownedResources,
		envVarPrefix,
		restMapper,
	)
}

func merge(dst map[string]interface{}, src map[string]interface{}) (map[string]interface{}, error) {
	merged := map[string]interface{}{}

	err := mergo.Merge(&merged, src, mergo.WithOverride, mergo.WithOverrideEmptySlice)
	if err != nil {
		return nil, err
	}

	err = mergo.Merge(&merged, dst)
	if err != nil {
		return nil, err
	}

	return merged, nil
}

func runHandler(
	client dynamic.Interface,
	obj *unstructured.Unstructured,
	outputObj *unstructured.Unstructured,
	key string,
	value string,
	envVars map[string]interface{},
	volumeKeys *[]string,
	restMapper meta.RESTMapper,
) error {
	h, err := annotations.BuildHandler(client, obj, key, value, restMapper)
	if err != nil {
		return err
	}
	r, err := h.Handle()
	fmt.Printf("---------------runHandler.Handle(), r: %v\n", r)
	if err != nil {
		return err
	}
	fmt.Printf("---------------runHandler.Handle(), outputObj.Object: %v\n", outputObj.Object)
	fmt.Printf("---------------runHandler.Handle(), r.RawData: %v\n", r.RawData)
	if newObj, err := merge(outputObj.Object, r.RawData); err != nil {
		return err
	} else {
		outputObj.Object = newObj
	}
	fmt.Printf("---------------runHandler.Handle(), envVars: %v\n", envVars)
	fmt.Printf("---------------runHandler.Handle(), r.Data: %v\n", r.Data)
	fmt.Printf("---------------runHandler.Handle(), outputObj.Object: %v\n", outputObj.Object)
	fmt.Printf("---------------runHandler.Handle(), outputObj: %v\n", outputObj)
	err = mergo.Merge(&envVars, r.Data, mergo.WithAppendSlice, mergo.WithOverride)
	if err != nil {
		return err
	}
	fmt.Printf("---------------runHandler.Handle(), envVars: %v\n", envVars)
	if r.Type == annotations.BindingTypeVolumeMount {
		*volumeKeys = []string(append(*volumeKeys, r.Path))
	}

	return nil
}

// buildServiceContext inspects g the API server searching for the service resources, associated CRD
// and OLM's CRDDescription if present, and processes those with relevant annotations to compose a
// ServiceContext.
func buildServiceContext(
	logger *log.Log,
	client dynamic.Interface,
	ns string,
	gvk schema.GroupVersionKind,
	resourceRef string,
	envVarPrefix *string,
	restMapper meta.RESTMapper,
) (*serviceContext, error) {
	obj, err := findService(client, ns, gvk, resourceRef)
	if err != nil {
		return nil, err
	}

	anns := map[string]string{}
	fmt.Printf("-------------buildServiceContext: obj:  %v\n", obj)

	// attempt to search the CRD of given gvk and bail out right away if a CRD can't be found; this
	// means also a CRDDescription can't exist or if it does exist it is not meaningful.
	crd, err := findServiceCRD(client, gvk)
	fmt.Printf("-------------buildServiceContext: findServiceCRD:  %v\n", crd)
	if err != nil && !errors.IsNotFound(err) {
		return nil, err
	} else if !errors.IsNotFound(err) {
		// attempt to search the a CRDDescription related to the obtained CRD.
		crdDescription, err := findCRDDescription(ns, client, gvk, crd)
		fmt.Printf("-------------buildServiceContext: crdDescription:  %v\n", crdDescription)
		if err != nil && !errors.IsNotFound(err) {
			return nil, err
		}
		// start with annotations extracted from CRDDescription
		err = mergo.Merge(
			&anns, convertCRDDescriptionToAnnotations(crdDescription), mergo.WithOverride)
		fmt.Printf("-------------buildServiceContext: convertCRDDescriptionToAnnotations:  %v\n", convertCRDDescriptionToAnnotations(crdDescription))
		fmt.Printf("-------------buildServiceContext: anns1:  %v\n", anns)
		if err != nil {
			return nil, err
		}
		// then override collected annotations with CRD annotations
		err = mergo.Merge(&anns, crd.GetAnnotations(), mergo.WithOverride)
		fmt.Printf("-------------buildServiceContext: crd.GetAnnotations():  %v\n", crd.GetAnnotations())
		fmt.Printf("-------------buildServiceContext: anns2:  %v\n", anns)
		if err != nil {
			return nil, err
		}
	}

	// and finally override collected annotations with own annotations
	err = mergo.Merge(&anns, obj.GetAnnotations(), mergo.WithOverride)
	fmt.Printf("-------------buildServiceContext: obj.GetAnnotations():  %v\n", obj.GetAnnotations())
	fmt.Printf("-------------buildServiceContext: anns3:  %v\n", anns)
	if err != nil {
		return nil, err
	}

	volumeKeys := make([]string, 0)
	envVars := make(map[string]interface{})

	// outputObj will be used to keep the changes processed by the handler.
	outputObj := obj.DeepCopy()

	for k, v := range anns {
		// runHandler modifies 'outputObj', 'envVars' and 'volumeKeys' in place.
		err := runHandler(client, obj, outputObj, k, v, envVars, &volumeKeys, restMapper)
		if err != nil {
			logger.Debug("Failed executing runHandler", "Error", err)
		}
	}
	fmt.Printf("-------------buildServiceContext: envVars-final:  %v\n", envVars)
	serviceCtx := &serviceContext{
		service:      outputObj,
		envVars:      envVars,
		volumeKeys:   volumeKeys,
		envVarPrefix: envVarPrefix,
	}

	return serviceCtx, nil
}
