package rhoai_normalizer

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	serverapiv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/cli/kubeflowmodelregistry"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/config"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/rest"
	types2 "github.com/redhat-ai-dev/model-catalog-bridge/pkg/types"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/util"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/kfmr"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/storage"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestInnerStart_GetKubeFlowInferenceServicesError_Immediate covers line 702-704:
// GetKubeFlowInferenceServicesForModelVersion returns error on the first call.
func TestInnerStart_GetKubeFlowInferenceServicesError_Immediate(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	// Server that returns valid RM/MV/MA but always errors on inference services
	errAllInfSvcServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"model-1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, fmt.Sprintf(rest.LIST_VERSIONS_OFF_REG_MODELS_URI, "1")):
			_, _ = w.Write([]byte(`{"items":[{"id":"2","name":"v1","registeredModelId":"1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, fmt.Sprintf(rest.LIST_ARTFIACTS_OFF_VERSIONS_URI, "2")):
			_, _ = w.Write([]byte(`{"items":[{"id":"3","name":"art1"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			// Always error - this will be hit by GetKubeFlowInferenceServicesForModelVersion
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`not json`))
		}
	})
	defer errAllInfSvcServer.Close()

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(errAllInfSvcServer, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		format:            types2.JsonArrayForamt,
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	r.innerStart(ctx, nil, nil)
}

// TestProcessKFMR_KubeflowISMatch_ModelCardErrorOnKubeflowPath covers lines 612-614:
// Kubeflow IS match path, full match found, model card fetch errors.
func TestProcessKFMR_KubeflowISMatch_ModelCardErrorOnKubeflowPath(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	// Server returning all valid data but errors on model card (catalog API)
	customServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, rest.KRMR_CATALOG_BASE_URI):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"model card not found"}`))
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(common.MnistRegisteredModels))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			_, _ = w.Write([]byte(common.MnistInferenceServices))
		case strings.Contains(r.URL.Path, "serving"):
			_, _ = w.Write([]byte(common.MnistServingEnvironmentsGet))
		case strings.Contains(r.URL.Path, "versions") && !strings.Contains(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(common.MnistModelVersionGet))
		case strings.Contains(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(common.MnistModelArtifacts))
		}
	})
	defer customServer.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(customServer, cfg)
	kfmrClient := kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)
	kfmrClient.RootCatalogURL = customServer.URL + rest.KRMR_CATALOG_BASE_URI

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kfmrClient},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
		format:            types2.JsonArrayForamt,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: "mnist-v1", Namespace: "ggmtest"},
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "ggmtest", Name: "mnist-v1"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	// Should still return a valid importKey despite model card error
	if len(importKey) == 0 {
		t.Error("expected non-empty importKey despite model card error")
	}
}

// TestProcessKFMR_KubeflowISMatch_RmEpochGreater covers lines 604-606:
// rm.GetLastUpdateTimeSinceEpoch() > mv.GetLastUpdateTimeSinceEpoch()
// The test data MnistRegisteredModels has rm epoch "1740498237116" and
// MnistModelVersionGet has mv epoch "1740512730384". mv epoch > rm epoch.
// We need rm epoch > mv epoch, so use custom test data.
func TestProcessKFMR_KubeflowISMatch_RmEpochGreater(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	// Registered model with very large epoch
	rmJSON := `{"items":[{"createTimeSinceEpoch":"1740498236442","customProperties":{},"id":"1","lastUpdateTimeSinceEpoch":"9999999999999","name":"mnist","owner":"kube:admin","state":"LIVE"}],"nextPageToken":"","pageSize":0,"size":1}`
	mvGetJSON := `{"author":"kube:admin","createTimeSinceEpoch":"1740498236719","customProperties":{},"id":"2","lastUpdateTimeSinceEpoch":"1000000000000","name":"v1","registeredModelId":"1","state":"LIVE"}`

	customServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(rmJSON))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			_, _ = w.Write([]byte(common.MnistInferenceServices))
		case strings.Contains(r.URL.Path, "serving"):
			_, _ = w.Write([]byte(common.MnistServingEnvironmentsGet))
		case strings.Contains(r.URL.Path, "versions") && !strings.Contains(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(mvGetJSON))
		case strings.Contains(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(common.MnistModelArtifacts))
		}
	})
	defer customServer.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(customServer, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
		format:            types2.JsonArrayForamt,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: "mnist-v1", Namespace: "ggmtest"},
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, epoch, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "ggmtest", Name: "mnist-v1"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	if len(importKey) == 0 {
		t.Error("expected non-empty importKey")
	}
	// rm epoch should win
	common.AssertEqual(t, "9999999999999", epoch)
}

// TestInnerStart_ClientListError_ForKServeIS covers lines 799-801:
// client.List for kserve InferenceServices returns error.
// TestInnerStart_PostCurrentKeySetNetworkError covers lines 829-832:
// PostCurrentKeySet returns a Go error (network error, not just bad status).
func TestInnerStart_PostCurrentKeySetNetworkError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateEmptyGetServer(t)
	defer kts.Close()

	// Create and immediately close the storage server to cause network errors
	closedServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {})
	closedServer.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:           storage.SetupBridgeStorageRESTClient(closedServer),
		format:            types2.JsonArrayForamt,
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	// PostCurrentKeySet will fail with network error -> lines 829-832
	r.innerStart(ctx, nil, nil)
}

func TestInnerStart_ClientListError_ForKServeIS(t *testing.T) {
	scheme := runtime.NewScheme()
	// Don't add InferenceService to scheme, so client.List will fail
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	kts := kfmr.CreateEmptyGetServer(t)
	defer kts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		format:            types2.JsonArrayForamt,
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	// Client without IS scheme -> List will fail
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	r.innerStart(ctx, nil, nil)
}
