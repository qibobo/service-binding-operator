package annotations

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/redhat-developer/service-binding-operator/pkg/controller/servicebindingrequest/nested"
)

// resourceHandler handles annotations related to external resources.
type resourceHandler struct {
	// bindingInfo contains the binding details related to the annotation handler.
	bindingInfo *bindingInfo
	// client is the client used to retrieve a related secret.
	client dynamic.Interface
	// relatedGroupVersionResource is the related resource GVR, used to retrieve the related resource
	// using the client.
	relatedGroupVersionResource schema.GroupVersionResource
	// relatedResourceName is the name of the related resource that is referenced by the handler
	// annotation key/value pair.
	relatedResourceName string
	// resource is the unstructured object to extract data using inputPath.
	resource unstructured.Unstructured
	// stringValue is a function used to decode values from the resource being handled; for example,
	// to decode Base64 keys the decodeBase64String can be used.
	stringValue func(interface{}) (string, error)
	// inputPathRoot indicates the root where input paths will be applied to extract a value from the
	// resource.
	inputPathRoot *string
	// restMapper allows clients to map resources to kind, and map kind and version
	// to interfaces for manipulating those objects.
	restMapper meta.RESTMapper
}

// discoverRelatedResourceName returns the resource name referenced by the handler. Can return an
// error in the case the expected information doesn't exist in the handler's resource object.
func discoverRelatedResourceName(obj map[string]interface{}, bi *bindingInfo) (string, error) {
	resourceNameValue, ok, err := unstructured.NestedFieldCopy(
		obj,
		strings.Split(bi.ResourceReferencePath, ".")...,
	)
	if !ok {
		return "", resourceNameFieldNotFoundErr
	}
	if err != nil {
		return "", err
	}
	name, ok := resourceNameValue.(string)
	if !ok {
		return "", invalidArgumentErr(bi.ResourceReferencePath)
	}
	return name, nil
}

// discoverBindingType attempts to extract a binding type from the given annotation value val.
func discoverBindingType(val string) (bindingType, error) {
	re := regexp.MustCompile("^binding:(.*?):.*$")
	parts := re.FindStringSubmatch(val)
	if len(parts) == 0 {
		return "", errInvalidBindingValue(val)
	}
	t := bindingType(parts[1])
	_, ok := supportedBindingTypes[t]
	if !ok {
		return "", unknownBindingTypeErr(t)
	}
	return t, nil
}

// getInputPathFields infers the input path fields based on the given bindingInfo value.
//
// In the case the resource reference path and source path are the same and no input path prefix has
// been given, an empty slice is returned.
//
// In the case inputPathPrefix is present, it is prepended to the resulting slice.
//
// In the case the resource reference and source paths are different, the source path is appended to
// the resulting slice.
func getInputPathFields(bi *bindingInfo, inputPathPrefix *string) []string {
	inputPathFields := []string{}
	if bi.SourcePath != "" {
		inputPathFields = append(inputPathFields, bi.SourcePath)
	}
	if inputPathPrefix != nil && len(*inputPathPrefix) > 0 {
		inputPathFields = append([]string{*inputPathPrefix}, inputPathFields...)
	}
	return inputPathFields
}

// Handle returns the value for an external resource strategy.
func (h *resourceHandler) Handle() (result, error) {
	ns := h.resource.GetNamespace()
	resource, err := h.
		client.
		Resource(h.relatedGroupVersionResource).
		Namespace(ns).
		Get(h.relatedResourceName, metav1.GetOptions{})
	if err != nil {
		return result{}, err
	}
	fmt.Printf("---------------Handle(): bindinginfo: %v, inputPathRoot: %v\n", h.bindingInfo, h.inputPathRoot)
	inputPathFields := getInputPathFields(h.bindingInfo, h.inputPathRoot)
	fmt.Printf("---------------inputPathFields: %v\n", inputPathFields)
	val, ok, err := unstructured.NestedFieldCopy(resource.Object, inputPathFields...)
	fmt.Printf("---------------val: %v\n", val)
	if !ok {
		return result{}, invalidArgumentErr(strings.Join(inputPathFields, ", "))
	}
	if err != nil {
		return result{}, err
	}

	if mapVal, ok := val.(map[string]interface{}); ok {
		tmpVal := make(map[string]interface{})
		for k, v := range mapVal {
			decodedVal, err := h.stringValue(v)
			if err != nil {
				return result{}, err
			}
			tmpVal[k] = decodedVal
		}
		val = tmpVal
	} else {
		val, err = h.stringValue(val)
		if err != nil {
			return result{}, err
		}
	}

	typ, err := discoverBindingType(h.bindingInfo.Value)
	if err != nil {
		return result{}, err
	}

	// get resource's kind.
	gvk, err := h.restMapper.KindFor(h.relatedGroupVersionResource)
	fmt.Printf("---------------gvk: %s\n", gvk)
	if err != nil {
		return result{}, err
	}

	// prefix the output path with the kind of the resource.
	outputPath := strings.Join([]string{
		strings.ToLower(gvk.Kind),
		h.bindingInfo.SourcePath,
	}, ".")
	rawDataPath := ""
	if h.bindingInfo.SourcePath == "" {
		rawDataPath = h.bindingInfo.ResourceReferencePath
	} else {
		rawDataPath = strings.Join([]string{
			h.bindingInfo.ResourceReferencePath,
			h.bindingInfo.SourcePath,
		}, ".")
	}

	fmt.Printf("---------------h.bindingInfo: %v\n", h.bindingInfo)
	fmt.Printf("---------------outputPath: %s\n", outputPath)
	fmt.Printf("---------------rawDataPath: %s\n", rawDataPath)
	return result{
		Data: nested.ComposeValue(val, nested.NewPath(outputPath)),
		Type: typ,
		Path: outputPath,

		RawData: nested.ComposeValue(val, nested.NewPath(rawDataPath)),
	}, nil
}

// stringValue asserts the given value 'v' and returns its string value.
func stringValue(v interface{}) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("value is not a string")
	}
	return s, nil
}

// NewSecretHandler constructs a SecretHandler.
func NewResourceHandler(
	client dynamic.Interface,
	bi *bindingInfo,
	resource unstructured.Unstructured,
	relatedGroupVersionResource schema.GroupVersionResource,
	inputPathPrefix *string,
	restMapper meta.RESTMapper,
) (*resourceHandler, error) {
	if client == nil {
		return nil, invalidArgumentErr("client")
	}

	if bi == nil {
		return nil, invalidArgumentErr("bi")
	}

	// if len(bi.SourcePath) == 0 {
	// 	return nil, invalidArgumentErr("bi.Path")
	// }

	if len(bi.ResourceReferencePath) == 0 {
		return nil, invalidArgumentErr("bi.ResourceReferencePath")
	}

	relatedResourceName, err := discoverRelatedResourceName(resource.Object, bi)
	fmt.Printf("---------------relatedResourceName: %s\n", relatedResourceName)
	if err != nil {
		return nil, err
	}

	return &resourceHandler{
		bindingInfo:                 bi,
		client:                      client,
		inputPathRoot:               inputPathPrefix,
		relatedGroupVersionResource: relatedGroupVersionResource,
		relatedResourceName:         relatedResourceName,
		resource:                    resource,
		stringValue:                 stringValue,
		restMapper:                  restMapper,
	}, nil
}
