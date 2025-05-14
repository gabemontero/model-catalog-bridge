package rest

const (
	KFMR_BASE_URI                    = "/api/model_registry/v1alpha3"
	GET_REG_MODEL_URI                = "/registered_models/%s"
	LIST_VERSIONS_OFF_REG_MODELS_URI = "/registered_models/%s/versions"
	LIST_ARTFIACTS_OFF_VERSIONS_URI  = "/model_versions/%s/artifacts"
	LIST_INFERENCE_SERVICES_URI      = "/inference_services"
	LIST_REG_MODEL_URI               = "/registered_models"
	GET_SERVING_ENV_URI              = "/serving_environments/%s"
	GET_MODEL_ARTIFACT_URI           = "/model_artifacts/%s"
	GET_MODEL_VERSION_URI            = "/model_versions/%s"
	INF_SVC_MV_ID_LABEL              = "modelregistry.opendatahub.io/model-version-id"
	INF_SVC_RM_ID_LABEL              = "modelregistry.opendatahub.io/registered-model-id"
	INF_SVC_IngressReady_CONDITION   = "IngressReady"
	INF_SVC_PredictorReady_CONDITION = "PredictorReady"
	INF_SVC_Ready_CONDITION          = "Ready"
)
