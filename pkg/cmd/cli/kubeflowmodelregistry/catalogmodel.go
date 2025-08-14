package kubeflowmodelregistry

import (
     "encoding/json"
     "fmt"

     "github.com/kubeflow/model-registry/catalog/pkg/openapi"
     "github.com/redhat-ai-dev/model-catalog-bridge/pkg/rest"
)

func (k *KubeFlowRESTClientWrapper) ListCatalogModels() ([]openapi.CatalogModel, error) {
     buf, err := k.getFromModelRegistry(k.RootCatalogURL + rest.LIST_CATALOG_MODELS_URI)
     if err != nil {
          return nil, err
     }

     cml := openapi.CatalogModelList{}
     err = json.Unmarshal(buf, &cml)
     if err != nil {
          return nil, err
     }
     return cml.Items, nil
}

func (k *KubeFlowRESTClientWrapper) GetCatalogModel(sourceId, modelName string) (*openapi.CatalogModel, error) {
     buf, err := k.getFromModelRegistry(k.RootCatalogURL + fmt.Sprintf(rest.GET_CATALOG_MODEL_URI, sourceId, modelName))
     if err != nil {
          return nil, err
     }

     cm := openapi.CatalogModel{}
     err = json.Unmarshal(buf, &cm)
     if err != nil {
          return nil, err
     }
     return &cm, nil
}

func (k *KubeFlowRESTClientWrapper) GetModelCard(sourceId, modelName string) (*string, error) {
     cm, err := k.GetCatalogModel(sourceId, modelName)
     if err != nil {
          return nil, err
     }
     return cm.Readme, nil
}
