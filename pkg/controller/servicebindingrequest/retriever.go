package servicebindingrequest

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/redhat-developer/service-binding-operator/pkg/controller/servicebindingrequest/envvars"
	"github.com/redhat-developer/service-binding-operator/pkg/log"
)

// retriever reads all data referred in plan instance, and store in a secret.
type retriever struct {
	logger *log.Log          // logger instance
	client dynamic.Interface // Kubernetes API client
}

// createServiceIndexPath returns a string slice with fields representing a path to a resource in the
// environment variable context. This function cleans fields that might contain invalid characters to
// be used in Go template; for example, a Group might contain the "." character, which makes it
// harder to refer using Go template direct accessors and is substituted by an underbar "_".
func createServiceIndexPath(name string, gvk schema.GroupVersionKind) []string {
	return []string{
		gvk.Version,
		strings.ReplaceAll(gvk.Group, ".", "_"),
		gvk.Kind,
		strings.ReplaceAll(name, "-", "_"),
	}

}

func buildServiceEnvVars(svcCtx *serviceContext, globalEnvVarPrefix string) (map[string]string, error) {
	prefixes := []string{}
	if len(globalEnvVarPrefix) > 0 {
		prefixes = append(prefixes, globalEnvVarPrefix)
	}
	if svcCtx.envVarPrefix != nil && len(*svcCtx.envVarPrefix) > 0 {
		prefixes = append(prefixes, *svcCtx.envVarPrefix)
	}
	if svcCtx.envVarPrefix == nil {
		prefixes = append(prefixes, svcCtx.service.GroupVersionKind().Kind)
	}
	fmt.Printf("-------------buildServiceEnvVars, prefixes: %v\n", prefixes)
	fmt.Printf("-------------buildServiceEnvVars, svcCtx.EnvVars: %v\n", svcCtx.envVars)
	return envvars.Build(svcCtx.envVars, prefixes...)
}

func (r *retriever) processServiceContext(
	svcCtx *serviceContext,
	customEnvVarCtx map[string]interface{},
	globalEnvVarPrefix string,
) (map[string][]byte, []string, error) {
	r.logger.Info("-------------processServiceContext", "svcCtx", *svcCtx, "customEnvVarCtx", customEnvVarCtx, "globalEnvVarPrefix", globalEnvVarPrefix)
	svcEnvVars, err := buildServiceEnvVars(svcCtx, globalEnvVarPrefix)

	if err != nil {
		return nil, nil, err
	}
	r.logger.Info("-------------processServiceContext", "svcEnvVars", svcEnvVars)
	// contribute the entire resource to the context shared with the custom env parser
	gvk := svcCtx.service.GetObjectKind().GroupVersionKind()

	// add an entry in the custom environment variable context, allowing the user to use the
	// following expression:
	//
	// `{{ index "v1alpha1" "postgresql.baiju.dev" "Database", "db-testing", "status", "connectionUrl" }}`
	err = unstructured.SetNestedField(
		customEnvVarCtx, svcCtx.service.Object, gvk.Version, gvk.Group, gvk.Kind,
		svcCtx.service.GetName())
	r.logger.Info("-------------processServiceContext1", "customEnvVarCtx", customEnvVarCtx)
	if err != nil {
		return nil, nil, err
	}

	// add an entry in the custom environment variable context with modified key names (group
	// names have the "." separator changed to underbar and "-" in the resource name is changed
	// to underbar "_" as well).
	//
	// `{{ .v1alpha1.postgresql_baiju_dev.Database.db_testing.status.connectionUrl }}`
	err = unstructured.SetNestedField(
		customEnvVarCtx,
		svcCtx.service.Object,
		createServiceIndexPath(svcCtx.service.GetName(), svcCtx.service.GroupVersionKind())...,
	)
	r.logger.Info("-------------processServiceContext2", "customEnvVarCtx", customEnvVarCtx)
	if err != nil {
		return nil, nil, err
	}

	envVars := make(map[string][]byte, len(svcEnvVars))
	for k, v := range svcEnvVars {
		envVars[k] = []byte(v)
	}

	var volumeKeys []string
	volumeKeys = append(volumeKeys, svcCtx.volumeKeys...)
	r.logger.Info("-------------processServiceContext", "svcEnvVars", svcEnvVars)
	return envVars, volumeKeys, nil
}

// ProcessServiceContexts returns environment variables and volume keys from a ServiceContext slice.
func (r *retriever) ProcessServiceContexts(
	globalEnvVarPrefix string,
	svcCtxs serviceContextList,
	envVarTemplates []corev1.EnvVar,
) (map[string][]byte, []string, error) {
	customEnvVarCtx := make(map[string]interface{})
	volumeKeys := make([]string, 0)
	envVars := make(map[string][]byte)
	r.logger.Info("-------------ProcessServiceContexts", "globalEnvVarPrefix", globalEnvVarPrefix, "svcCtxs", svcCtxs, "envVarTemplates", envVarTemplates)
	for _, svcCtx := range svcCtxs {
		s, v, err := r.processServiceContext(svcCtx, customEnvVarCtx, globalEnvVarPrefix)
		if err != nil {
			return nil, nil, err
		}
		for k, v := range s {
			envVars[k] = []byte(v)
		}
		volumeKeys = append(volumeKeys, v...)
	}
	r.logger.Info("-------------ProcessServiceContexts1", "envVars", envVars, "volumeKeys", volumeKeys, "customEnvVarCtx", customEnvVarCtx)
	envParser := newCustomEnvParser(envVarTemplates, customEnvVarCtx)
	customEnvVars, err := envParser.Parse()
	if err != nil {
		r.logger.Error(
			err, "Creating envVars", "Templates", envVarTemplates, "TemplateContext", customEnvVarCtx)
		return nil, nil, err
	}
	r.logger.Info("-------------ProcessServiceContexts2", "customEnvVars", customEnvVars)
	for k, v := range customEnvVars {
		prefix := []string{}
		if len(globalEnvVarPrefix) > 0 {
			prefix = append(prefix, globalEnvVarPrefix)
		}
		prefix = append(prefix, k)
		k = strings.Join(prefix, "_")
		envVars[k] = []byte(v.(string))
	}
	r.logger.Info("-------------ProcessServiceContexts3", "envVars", envVars, "volumeKeys", volumeKeys, "customEnvVarCtx", customEnvVarCtx)
	return envVars, volumeKeys, nil
}

// NewRetriever instantiate a new retriever instance.
func NewRetriever(
	client dynamic.Interface,
) *retriever {
	return &retriever{
		logger: log.NewLog("retriever"),
		client: client,
	}
}
