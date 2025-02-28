package kubeflowmodelregistry

import (
	"encoding/json"
	"fmt"
	"github.com/kubeflow/model-registry/pkg/openapi"
)

func (k *KubeFlowRESTClientWrapper) ListModelVersions(id string) ([]openapi.ModelVersion, error) {
	buf, err := k.getFromModelRegistry(k.RootURL + fmt.Sprintf(LIST_VERSIONS_OFF_REG_MODELS_URI, id))
	if err != nil {
		return nil, err
	}

	mvs := openapi.ModelVersionList{}
	err = json.Unmarshal(buf, &mvs)
	if err != nil {
		return nil, err
	}
	return mvs.Items, err
}

func (k *KubeFlowRESTClientWrapper) GetModelVersions(id string) (*openapi.ModelVersion, error) {
	buf, err := k.getFromModelRegistry(k.RootURL + fmt.Sprintf(GET_MODEL_VERSION_URI, id))
	if err != nil {
		return nil, err
	}

	mv := openapi.ModelVersion{}
	err = json.Unmarshal(buf, &mv)
	if err != nil {
		return nil, err
	}
	return &mv, err
}
