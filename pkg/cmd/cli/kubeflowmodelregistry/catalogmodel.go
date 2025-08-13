package kubeflowmodelregistry

import (
	"github.com/kubeflow/model-registry/catalog/pkg/openapi"
)

type CatalogModelWrapper struct {
	catalogSource *openapi.CatalogModel
}
