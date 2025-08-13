package kubeflowmodelregistry

import (
	"github.com/kubeflow/model-registry/catalog/pkg/openapi"
)

type CatalogSourceWrapper struct {
	catalogSource *openapi.CatalogSource
}
