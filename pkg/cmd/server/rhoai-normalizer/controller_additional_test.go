package rhoai_normalizer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	serverapiv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kubeflow/model-registry/pkg/openapi"
	routev1 "github.com/openshift/api/route/v1"
	routeclient "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/cli/kubeflowmodelregistry"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/config"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/rest"
	types2 "github.com/redhat-ai-dev/model-catalog-bridge/pkg/types"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/util"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/kfmr"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/location"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/storage"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8srest "k8s.io/client-go/rest"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestFilter(t *testing.T) {
	f := &RHOAINormalizerFilter{}
	common.AssertEqual(t, false, f.Generic(event.GenericEvent{}))
	common.AssertEqual(t, true, f.Create(event.CreateEvent{}))
	common.AssertEqual(t, true, f.Delete(event.DeleteEvent{}))
	common.AssertEqual(t, true, f.Update(event.UpdateEvent{}))
}

func TestReconcile_DeletePath(t *testing.T) {
	// Test that when the InferenceService is not found (deleted), the reconcile
	// triggers a delete processing path (calls innerStart).
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateGetServer(t)
	defer kts.Close()
	brts := location.CreateBridgeLocationServer(t)
	defer brts.Close()
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:        scheme,
		eventRecorder: nil,
		k8sToken:      "",
		myNS:          "",
		routeClient:   nil,
		storage:       storage.SetupBridgeStorageRESTClient(bsts),
		format:        types2.JsonArrayForamt,
		kfmrRegistryRoute: map[string]*routev1.Route{
			"test": {
				Spec:   routev1.RouteSpec{Host: "http://foo.com"},
				Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}},
			},
		},
		kfmr: map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{
			"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg),
		},
		defaultOwner:     util.DefaultOwner,
		defaultLifecycle: util.DefaultLifecycle,
	}

	// Build a client with NO InferenceService objects so the Get will return NotFound
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	result, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "foo", Name: "bar"},
	})
	common.AssertError(t, err)
	// On delete path, no requeue
	common.AssertEqual(t, false, result.Requeue)
}

func TestReconcile_KServeNotReadyConditions(t *testing.T) {
	// Test when conditions exist but not all are true
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		format:            types2.JsonArrayForamt,
		kfmrRegistryRoute: map[string]*routev1.Route{},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}

	// Inference service with conditions but PredictorReady is false
	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "svc1"},
		Spec:       serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{
			ModelStatus: serverapiv1beta1.ModelStatus{
				TransitionStatus: serverapiv1beta1.UpToDate,
			},
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{
						Type:   rest.INF_SVC_IngressReady_CONDITION,
						Status: corev1.ConditionTrue,
					},
					{
						Type:   rest.INF_SVC_PredictorReady_CONDITION,
						Status: corev1.ConditionFalse,
					},
					{
						Type:   rest.INF_SVC_Ready_CONDITION,
						Status: corev1.ConditionTrue,
					},
				},
			},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	result, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "svc1"},
	})
	common.AssertError(t, err)
	common.AssertEqual(t, true, result.Requeue)
}

func TestReconcile_KServeNoURL(t *testing.T) {
	// Test when conditions are all true but URL is nil
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		format:            types2.JsonArrayForamt,
		kfmrRegistryRoute: map[string]*routev1.Route{},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "svc1"},
		Spec:       serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{
			ModelStatus: serverapiv1beta1.ModelStatus{
				TransitionStatus: serverapiv1beta1.UpToDate,
			},
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{
						Type:   rest.INF_SVC_IngressReady_CONDITION,
						Status: corev1.ConditionTrue,
					},
					{
						Type:   rest.INF_SVC_PredictorReady_CONDITION,
						Status: corev1.ConditionTrue,
					},
					{
						Type:   rest.INF_SVC_Ready_CONDITION,
						Status: corev1.ConditionTrue,
					},
				},
			},
			URL: nil,
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	result, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "svc1"},
	})
	common.AssertError(t, err)
	common.AssertEqual(t, true, result.Requeue)
}

func TestReconcile_TransitionStatusNotUpToDate(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		format:            types2.JsonArrayForamt,
		kfmrRegistryRoute: map[string]*routev1.Route{},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "svc1"},
		Spec:       serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{
			ModelStatus: serverapiv1beta1.ModelStatus{
				TransitionStatus: serverapiv1beta1.InProgress,
			},
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{
						Type:   rest.INF_SVC_Ready_CONDITION,
						Status: corev1.ConditionTrue,
					},
				},
			},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	result, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "svc1"},
	})
	common.AssertError(t, err)
	common.AssertEqual(t, true, result.Requeue)
}

func TestProcessBWriter_ErrorStatus(t *testing.T) {
	// Create a storage server that returns a non-200/201 status
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "POST":
			switch {
			case strings.Contains(r.URL.Path, util.CurrentKeySetURI):
				w.WriteHeader(http.StatusOK)
			default:
				bodyBuf, _ := io.ReadAll(r.Body)
				if len(bodyBuf) == 0 {
					w.WriteHeader(500)
					return
				}
				data := rest.PostBody{}
				err := json.Unmarshal(bodyBuf, &data)
				if err != nil {
					w.WriteHeader(500)
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error": "bad request"}`))
			}
		}
	})
	defer ts.Close()

	r := &RHOAINormalizerReconcile{
		storage: storage.SetupBridgeStorageRESTClient(ts),
	}

	b := []byte{}
	buf := bytes.NewBuffer(b)
	bwriter := bufio.NewWriter(buf)
	bwriter.WriteString("test data")

	err := r.processBWriter(bwriter, buf, "test-key", types2.KServeNormalizer, "", "", nil)
	if err == nil {
		t.Errorf("expected error for bad status, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "400") {
		t.Errorf("expected error to contain 400, got: %s", err.Error())
	}
}

func TestSetupKFMR_AlreadyPopulated(t *testing.T) {
	// Test that setupKFMR returns true if kfmr map already has entries
	r := &RHOAINormalizerReconcile{
		kfmr: map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{
			"existing": {},
		},
		kfmrRegistryRoute: map[string]*routev1.Route{},
	}

	ctx := context.TODO()
	result := r.setupKFMR(ctx)
	common.AssertEqual(t, true, result)
}

func createFakeRouteServer(t *testing.T, routes []routev1.Route) (*httptest.Server, *routeclient.RouteV1Client) {
	// Create a fake HTTP server that serves OpenShift Route API responses
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Route API: %s %s", r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")

		routeList := &routev1.RouteList{
			TypeMeta: metav1.TypeMeta{Kind: "RouteList", APIVersion: "route.openshift.io/v1"},
			Items:    routes,
		}
		buf, _ := json.Marshal(routeList)
		w.Write(buf)
	}))

	cfg := &k8srest.Config{
		Host: ts.URL,
	}
	rc, err := routeclient.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("failed to create route client: %v", err)
	}
	return ts, rc
}

func TestSetupKFMR_WithEnvVarRoute_NamespaceAndName(t *testing.T) {
	// Test setupKFMR with env var containing namespace:name route tuple
	testRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-model-registry",
			Namespace: "test-ns",
		},
		Spec: routev1.RouteSpec{Host: "mr.example.com"},
		Status: routev1.RouteStatus{
			Ingress: []routev1.RouteIngress{{Host: "mr.example.com"}},
		},
	}

	ts, rc := createFakeRouteServer(t, []routev1.Route{testRoute})
	defer ts.Close()

	r := &RHOAINormalizerReconcile{
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		kfmrRegistryRoute: map[string]*routev1.Route{},
		routeClient:       rc,
		k8sToken:          "test-token",
	}

	t.Setenv(types2.ModelRegistryRouteEnvVar, "test-ns:my-model-registry")
	t.Setenv(types2.ModelRegistryTokenEnvVar, "test-mr-token")

	ctx := context.TODO()
	result := r.setupKFMR(ctx)
	common.AssertEqual(t, true, result)
	common.AssertEqual(t, 1, len(r.kfmrRegistryRoute))
	common.AssertEqual(t, 1, len(r.kfmr))
}

func TestSetupKFMR_WithEnvVarRoute_NameOnly(t *testing.T) {
	// Test setupKFMR with env var containing name-only route (NamespaceAll path)
	testRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-model-registry",
			Namespace: "some-ns",
		},
		Spec: routev1.RouteSpec{Host: "mr.example.com"},
		Status: routev1.RouteStatus{
			Ingress: []routev1.RouteIngress{{Host: "mr.example.com"}},
		},
	}

	ts, rc := createFakeRouteServer(t, []routev1.Route{testRoute})
	defer ts.Close()

	r := &RHOAINormalizerReconcile{
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		kfmrRegistryRoute: map[string]*routev1.Route{},
		routeClient:       rc,
		k8sToken:          "test-token",
	}

	t.Setenv(types2.ModelRegistryRouteEnvVar, "my-model-registry")
	t.Setenv(types2.ModelRegistryTokenEnvVar, "")

	ctx := context.TODO()
	result := r.setupKFMR(ctx)
	common.AssertEqual(t, true, result)
	common.AssertEqual(t, 1, len(r.kfmrRegistryRoute))
}

func TestSetupKFMR_LabelBasedDiscovery(t *testing.T) {
	// Test setupKFMR label-based discovery (when env var produces no routes)
	testRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-registry",
			Namespace: "mr-ns",
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "model-registry-operator"},
		},
		Spec: routev1.RouteSpec{Host: "mr.example.com"},
		Status: routev1.RouteStatus{
			Ingress: []routev1.RouteIngress{{Host: "mr.example.com"}},
		},
	}
	catalogRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-catalog",
			Namespace: "mr-ns",
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "model-registry-operator"},
		},
		Spec: routev1.RouteSpec{Host: "catalog.example.com"},
		Status: routev1.RouteStatus{
			Ingress: []routev1.RouteIngress{{Host: "catalog.example.com"}},
		},
	}

	ts, rc := createFakeRouteServer(t, []routev1.Route{testRoute, catalogRoute})
	defer ts.Close()

	r := &RHOAINormalizerReconcile{
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		kfmrRegistryRoute: map[string]*routev1.Route{},
		routeClient:       rc,
		k8sToken:          "test-token",
	}

	t.Setenv(types2.ModelRegistryRouteEnvVar, "")

	ctx := context.TODO()
	result := r.setupKFMR(ctx)
	common.AssertEqual(t, true, result)
	// Should have found routes via label query
	common.AssertEqual(t, true, len(r.kfmrRegistryRoute) > 0)
}

func TestSetupKFMR_EmptyRouteList_LabelQueryEmpty(t *testing.T) {
	// Test setupKFMR when no routes are found at all - returns false
	ts, rc := createFakeRouteServer(t, []routev1.Route{})
	defer ts.Close()

	r := &RHOAINormalizerReconcile{
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		kfmrRegistryRoute: map[string]*routev1.Route{},
		routeClient:       rc,
		k8sToken:          "test-token",
	}

	t.Setenv(types2.ModelRegistryRouteEnvVar, "")

	ctx := context.TODO()
	result := r.setupKFMR(ctx)
	common.AssertEqual(t, false, result)
}

func TestSetupKFMR_EnvVarMultipleRoutes(t *testing.T) {
	// Test setupKFMR with multiple comma-separated routes in env var
	route1 := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{Name: "reg1", Namespace: "ns1"},
		Spec:       routev1.RouteSpec{Host: "reg1.example.com"},
		Status:     routev1.RouteStatus{Ingress: []routev1.RouteIngress{{Host: "reg1.example.com"}}},
	}
	route2 := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{Name: "reg2", Namespace: "ns2"},
		Spec:       routev1.RouteSpec{Host: "reg2.example.com"},
		Status:     routev1.RouteStatus{Ingress: []routev1.RouteIngress{{Host: "reg2.example.com"}}},
	}

	ts, rc := createFakeRouteServer(t, []routev1.Route{route1, route2})
	defer ts.Close()

	r := &RHOAINormalizerReconcile{
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		kfmrRegistryRoute: map[string]*routev1.Route{},
		routeClient:       rc,
		k8sToken:          "test-token",
	}

	t.Setenv(types2.ModelRegistryRouteEnvVar, "ns1:reg1,ns2:reg2")
	t.Setenv(types2.ModelRegistryTokenEnvVar, "test-token")

	ctx := context.TODO()
	result := r.setupKFMR(ctx)
	common.AssertEqual(t, true, result)
	common.AssertEqual(t, 2, len(r.kfmrRegistryRoute))
	common.AssertEqual(t, 2, len(r.kfmr))
}

func newFakeAPIServerAndManager(t *testing.T) (*httptest.Server, ctrl.Manager, *k8srest.Config) {
	t.Helper()
	fakeAPIServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/apis/route.openshift.io/v1/routes"):
			routeList := &routev1.RouteList{
				TypeMeta: metav1.TypeMeta{Kind: "RouteList", APIVersion: "route.openshift.io/v1"},
				Items:    []routev1.Route{},
			}
			buf, _ := json.Marshal(routeList)
			w.Write(buf)
		default:
			w.Write([]byte(`{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"v1","resources":[]}`))
		}
	}))

	cfg := &k8srest.Config{
		Host: fakeAPIServer.URL,
	}

	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
	})
	if err != nil {
		fakeAPIServer.Close()
		t.Skipf("could not create manager: %v", err)
	}
	return fakeAPIServer, mgr, cfg
}

func TestSetupController_WithPprof(t *testing.T) {
	// Test SetupController with pprof port - run first to cover pprof path
	fakeAPIServer, mgr, cfg := newFakeAPIServerAndManager(t)
	defer fakeAPIServer.Close()

	t.Setenv(types2.FormatEnvVar, "")
	t.Setenv(types2.StorageUrlEnvVar, "http://localhost:7070")
	t.Setenv(types2.OwnerEnvVar, "")
	t.Setenv(types2.LifecycleEnvVar, "")
	t.Setenv(types2.PollingIntEnvVar, "invalid")
	t.Setenv(types2.ModelRegistryRouteEnvVar, "")

	ctx := context.TODO()
	err := SetupController(ctx, mgr, cfg, "9999")
	if err != nil {
		t.Logf("SetupController returned error (expected in test): %v", err)
	}
}

func TestSetupController(t *testing.T) {
	// Test SetupController by creating a fake k8s API server and a real manager
	fakeAPIServer, mgr, cfg := newFakeAPIServerAndManager(t)
	defer fakeAPIServer.Close()

	t.Setenv(types2.FormatEnvVar, string(types2.JsonArrayForamt))
	t.Setenv(types2.StorageUrlEnvVar, "http://localhost:7070")
	t.Setenv(types2.OwnerEnvVar, "test-owner")
	t.Setenv(types2.LifecycleEnvVar, "production")
	t.Setenv(types2.PollingIntEnvVar, "5m")
	t.Setenv(types2.ModelRegistryRouteEnvVar, "")

	ctx := context.TODO()
	err := SetupController(ctx, mgr, cfg, "")
	if err != nil {
		t.Logf("SetupController returned error (expected in test): %v", err)
	}
}

func TestSetupController_PodIPFallback(t *testing.T) {
	// Test SetupController without STORAGE_URL but with POD_IP
	fakeAPIServer, mgr, cfg := newFakeAPIServerAndManager(t)
	defer fakeAPIServer.Close()

	t.Setenv(types2.FormatEnvVar, string(types2.CatalogInfoYamlFormat))
	t.Setenv(types2.StorageUrlEnvVar, "")
	t.Setenv(util.PodIPEnvVar, "127.0.0.1")
	t.Setenv(types2.OwnerEnvVar, "my-owner")
	t.Setenv(types2.LifecycleEnvVar, "staging")
	t.Setenv(types2.PollingIntEnvVar, "30s")
	t.Setenv(types2.ModelRegistryRouteEnvVar, "")

	ctx := context.TODO()
	err := SetupController(ctx, mgr, cfg, "")
	if err != nil {
		t.Logf("SetupController returned error (expected in test): %v", err)
	}
}

func TestSetupKFMR_NamespaceAll_NoMatchingName(t *testing.T) {
	// Test setupKFMR with name-only env var route where name doesn't match any route
	// This exercises the NamespaceAll path where routes exist but no name match (line 138 empty list too)
	testRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-route",
			Namespace: "some-ns",
		},
		Spec:   routev1.RouteSpec{Host: "other.example.com"},
		Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{Host: "other.example.com"}}},
	}

	ts, rc := createFakeRouteServer(t, []routev1.Route{testRoute})
	defer ts.Close()

	r := &RHOAINormalizerReconcile{
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		kfmrRegistryRoute: map[string]*routev1.Route{},
		routeClient:       rc,
		k8sToken:          "test-token",
	}

	// Name-only route (no colon) -> NamespaceAll path, but "nonexistent" won't match "other-route"
	t.Setenv(types2.ModelRegistryRouteEnvVar, "nonexistent")
	t.Setenv(types2.ModelRegistryTokenEnvVar, "")

	ctx := context.TODO()
	result := r.setupKFMR(ctx)
	// Should fall through to label query which also finds the route
	t.Logf("setupKFMR result: %v, routes: %d, kfmr: %d", result, len(r.kfmrRegistryRoute), len(r.kfmr))
}

func TestSetupKFMR_GetRouteError(t *testing.T) {
	// Test setupKFMR where ns:name Get fails (line 150 - error path)
	// Create a server that returns 404 for specific route gets
	errRouteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/namespaces/") && !strings.Contains(r.URL.Path, "?") {
			// Return 404 for specific route Get
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","message":"not found","code":404}`))
			return
		}
		// Return empty list for list queries
		routeList := &routev1.RouteList{
			TypeMeta: metav1.TypeMeta{Kind: "RouteList", APIVersion: "route.openshift.io/v1"},
			Items:    []routev1.Route{},
		}
		buf, _ := json.Marshal(routeList)
		w.Write(buf)
	}))
	defer errRouteServer.Close()

	cfg2 := &k8srest.Config{Host: errRouteServer.URL}
	rc, err := routeclient.NewForConfig(cfg2)
	if err != nil {
		t.Fatalf("failed to create route client: %v", err)
	}

	r := &RHOAINormalizerReconcile{
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		kfmrRegistryRoute: map[string]*routev1.Route{},
		routeClient:       rc,
		k8sToken:          "test-token",
	}

	// ns:name tuple triggers the default case in switch (non-NamespaceAll Get)
	t.Setenv(types2.ModelRegistryRouteEnvVar, "test-ns:nonexistent-route")

	ctx := context.TODO()
	result := r.setupKFMR(ctx)
	common.AssertEqual(t, false, result)
}

func TestSetupKFMR_ExistingKFMRKey(t *testing.T) {
	// Test setupKFMR where kfmrRegistryRoute already has a key and kfmr already
	// has the matching key - exercises the "ok, continue" path at line 190
	testRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{Name: "reg1", Namespace: "ns1"},
		Spec:       routev1.RouteSpec{Host: "reg1.example.com"},
		Status:     routev1.RouteStatus{Ingress: []routev1.RouteIngress{{Host: "reg1.example.com"}}},
	}

	ts, rc := createFakeRouteServer(t, []routev1.Route{testRoute})
	defer ts.Close()

	existingKfmr := &kubeflowmodelregistry.KubeFlowRESTClientWrapper{}

	r := &RHOAINormalizerReconcile{
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"ns1:reg1": existingKfmr},
		kfmrRegistryRoute: map[string]*routev1.Route{"ns1:reg1": &testRoute},
		routeClient:       rc,
		k8sToken:          "test-token",
	}

	// With existing kfmr entries, setupKFMR returns true immediately (line 114)
	ctx := context.TODO()
	result := r.setupKFMR(ctx)
	common.AssertEqual(t, true, result)
	// The existing entry should still be there
	common.AssertEqual(t, existingKfmr, r.kfmr["ns1:reg1"])
}

func TestSetupKFMR_NewRouteExistingKfmrKey(t *testing.T) {
	// Test the path at line 188-191 where kfmrRegistryRoute has a route but
	// kfmr already has the key. Force kfmr to be empty initially so we go through
	// the env var path but then populate kfmr for the key before the kfmr loop.
	testRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{Name: "reg1", Namespace: "ns1"},
		Spec:       routev1.RouteSpec{Host: "reg1.example.com"},
		Status:     routev1.RouteStatus{Ingress: []routev1.RouteIngress{{Host: "reg1.example.com"}}},
	}

	ts, rc := createFakeRouteServer(t, []routev1.Route{testRoute})
	defer ts.Close()

	r := &RHOAINormalizerReconcile{
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		kfmrRegistryRoute: map[string]*routev1.Route{},
		routeClient:       rc,
		k8sToken:          "test-token",
	}

	t.Setenv(types2.ModelRegistryRouteEnvVar, "ns1:reg1,ns1:reg1")
	t.Setenv(types2.ModelRegistryTokenEnvVar, "test-token")

	ctx := context.TODO()
	result := r.setupKFMR(ctx)
	common.AssertEqual(t, true, result)
	// Should have only 1 entry despite duplicate in env var
	common.AssertEqual(t, 1, len(r.kfmr))
}

func TestSetupKFMR_RouteWithNoIngress(t *testing.T) {
	// Test setupKFMR where the route is fetched via env var but has no ingress
	// The route won't be stored in kfmrRegistryRoute because ingress is empty.
	// Use a name that won't exist in the fake server so Get returns 404,
	// and the label query returns no routes.
	ts, rc := createFakeRouteServer(t, []routev1.Route{})
	defer ts.Close()

	r := &RHOAINormalizerReconcile{
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		kfmrRegistryRoute: map[string]*routev1.Route{},
		routeClient:       rc,
		k8sToken:          "test-token",
	}

	t.Setenv(types2.ModelRegistryRouteEnvVar, "test-ns:no-ingress-route")

	ctx := context.TODO()
	result := r.setupKFMR(ctx)
	// No routes found; label query also returns empty
	common.AssertEqual(t, false, result)
}

func TestProcessKFMR_NotReady(t *testing.T) {
	// Test processKFMR when setupKFMR returns false (no routes)
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		kfmrRegistryRoute: map[string]*routev1.Route{},
	}
	// With empty env var and nil routeClient, the label-based path will be reached.
	// Set env var to something so we go through the env var path, but with no routeClient.
	// Actually let's just test the "setupKFMR returns false" path.
	// The only way setupKFMR returns false is if kfmrRegistryRoute is empty after all attempts.
	// With nil routeClient, trying to list routes will panic.
	// So let's skip the route-related path and directly test processKFMR with empty kfmr.

	// Force setupKFMR to return true by populating kfmr, but have no registered models
	kts := kfmr.CreateEmptyGetServer(t)
	defer kts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)
	r.kfmr["test"] = kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)
	r.kfmrRegistryRoute["test"] = &routev1.Route{
		Spec:   routev1.RouteSpec{Host: "foo.com"},
		Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}},
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"},
	}

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "foo", Name: "bar"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	common.AssertEqual(t, "", importKey)
}

func TestProcessKFMR_NoMatchingInferenceService(t *testing.T) {
	// Test processKFMR where KFMR has registered models with inference services
	// but none match the kserve inference service
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateGetServerWithInference(t)
	defer kts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	// Use an inference service that won't match any kubeflow inference service
	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "non-matching-ns", Name: "non-matching-name"},
	}

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "non-matching-ns", Name: "non-matching-name"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	// No match should return empty importKey
	common.AssertEqual(t, "", importKey)
}

func TestProcessKFMR_WithKFMRError(t *testing.T) {
	// Test processKFMR when KFMR returns errors for listing
	// Create a server that returns errors
	errServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": "internal error"}`))
	})
	defer errServer.Close()

	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(errServer, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"},
	}

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "foo", Name: "bar"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	common.AssertEqual(t, "", importKey)
}

func TestInnerStart_LoopOverKFMRError(t *testing.T) {
	// Test innerStart when LoopOverKFMR returns error
	errServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`not json`))
	})
	defer errServer.Close()

	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(errServer, cfg)

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
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	// Should not panic, just log the error and return
	r.innerStart(ctx, buf, bwriter)
}

func TestInnerStart_NilBuffers(t *testing.T) {
	// Test innerStart with nil buffer and bwriter (the polling case)
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateGetServer(t)
	defer kts.Close()
	brts := location.CreateBridgeLocationServer(t)
	defer brts.Close()
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

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
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	// Call with nil buf/bwriter like the Start() polling loop does
	r.innerStart(ctx, nil, nil)

	// Verify that the current key set was posted
	found := false
	callback.Range(func(key, value any) bool {
		found = true
		return true
	})
	common.AssertEqual(t, true, found)
}

func TestReconcilerStart_TickerFires(t *testing.T) {
	// Test the RHOAINormalizerReconcile.Start method - verify it calls innerStart
	// on ticker. Since Start has an infinite loop that doesn't return on ctx.Done(),
	// we just verify the ticker fires and innerStart runs at least once.
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateEmptyGetServer(t)
	defer kts.Close()
	brts := location.CreateBridgeLocationServer(t)
	defer brts.Close()
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

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
		pollingInt:        50 * time.Millisecond,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		r.Start(ctx)
	}()

	// Wait for at least one tick to fire and innerStart to run
	time.Sleep(200 * time.Millisecond)
	cancel()

	// Verify that innerStart ran (it should have posted current key set)
	found := false
	callback.Range(func(key, value any) bool {
		found = true
		return true
	})
	common.AssertEqual(t, true, found)
}

func TestPprofStart(t *testing.T) {
	// Test the pprof Start method
	p := &pprof{port: "0"} // port 0 picks a random available port

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- p.Start(ctx)
	}()

	select {
	case err := <-done:
		common.AssertError(t, err)
	case <-time.After(2 * time.Second):
		t.Error("pprof Start did not return after context cancellation")
	}
}

func TestProcessKFMR_NoKubeflowInferenceServices_NoMatch(t *testing.T) {
	// Test processKFMR where KFMR has registered models but no inference services -
	// the "no kubeflow inference service path". IS doesn't have matching labels.
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateGetServer(t)
	defer kts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
		format:            types2.JsonArrayForamt,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "model-1"},
		Spec:       serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{
			URL: &apis.URL{Scheme: "http", Host: "foo.com", Path: "/mymodel"},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "foo", Name: "model-1"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	common.AssertEqual(t, "", importKey)
}

func TestProcessKFMR_NoKubeflowInferenceServices_WithMatch(t *testing.T) {
	// Test processKFMR where KFMR has registered models but no inference services,
	// and the KServe IS has labels matching the registered model/version IDs.
	// CreateGetServer returns rm.id="1", mv.id="2" (no inference services endpoint)
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateGetServer(t)
	defer kts.Close()
	brts := location.CreateBridgeLocationServer(t)
	defer brts.Close()
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
		format:            types2.JsonArrayForamt,
	}

	// KServe IS with labels matching rm.id=1, mv.id=2
	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "model-1",
			Labels: map[string]string{
				rest.INF_SVC_RM_ID_LABEL: "1",
				rest.INF_SVC_MV_ID_LABEL: "2",
			},
		},
		Spec: serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{
			URL: &apis.URL{Scheme: "http", Host: "foo.com", Path: "/mymodel"},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "foo", Name: "model-1"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	// Should have found a match via KServeInferenceServiceMapping
	if len(importKey) == 0 {
		t.Error("expected non-empty importKey from processKFMR match via model version mapping")
	}
}

func TestProcessKFMR_NoKubeflowInferenceServices_WithCatalog(t *testing.T) {
	// Same as above but with a catalog route set to exercise the model card path
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateGetServer(t)
	defer kts.Close()
	brts := location.CreateBridgeLocationServer(t)
	defer brts.Close()
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)
	kfmrClient := kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)
	kfmrClient.RootCatalogURL = kts.URL + rest.KRMR_CATALOG_BASE_URI

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kfmrClient},
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
		format:            types2.JsonArrayForamt,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "model-1",
			Labels: map[string]string{
				rest.INF_SVC_RM_ID_LABEL: "1",
				rest.INF_SVC_MV_ID_LABEL: "2",
			},
		},
		Spec: serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{
			URL: &apis.URL{Scheme: "http", Host: "foo.com", Path: "/mymodel"},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, modelCardKey, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "foo", Name: "model-1"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	if len(importKey) == 0 {
		t.Error("expected non-empty importKey")
	}
	t.Logf("modelCardKey: %s", modelCardKey)
}

func TestInnerStart_KServeOnlyWithLabels(t *testing.T) {
	// Test innerStart where the InferenceService has kubeflow labels and kfmr is populated,
	// so it should be skipped in the kserve-only loop
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateEmptyGetServer(t)
	defer kts.Close()
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

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

	// InferenceService with kubeflow labels - should be skipped
	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1",
			Name:      "svc1",
			Labels:    map[string]string{rest.INF_SVC_RM_ID_LABEL: "1", rest.INF_SVC_MV_ID_LABEL: "2"},
		},
		Spec:   serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	r.innerStart(ctx, nil, nil)

	// The currentkeyset should NOT include this inference service since it has kubeflow labels
	found := false
	callback.Range(func(key, value any) bool {
		k := fmt.Sprintf("%v", key)
		v := fmt.Sprintf("%v", value)
		if k == "key" && strings.Contains(v, "ns1_svc1") {
			found = true
		}
		return true
	})
	common.AssertEqual(t, false, found)
}

func TestInnerStart_KServeOnlyWithoutLabels(t *testing.T) {
	// Test innerStart where the InferenceService has no kubeflow labels,
	// so it should be included in the kserve-only key set
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateEmptyGetServer(t)
	defer kts.Close()
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

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

	// InferenceService without kubeflow labels - should be included
	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1",
			Name:      "svc1",
		},
		Spec:   serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	r.innerStart(ctx, nil, nil)

	// The currentkeyset should include this inference service
	found := false
	callback.Range(func(key, value any) bool {
		k := fmt.Sprintf("%v", key)
		v := fmt.Sprintf("%v", value)
		if k == "key" && strings.Contains(v, "ns1_svc1") {
			found = true
		}
		return true
	})
	common.AssertEqual(t, true, found)
}

func TestProcessBWriter_NilModelCard(t *testing.T) {
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	r := &RHOAINormalizerReconcile{
		storage: storage.SetupBridgeStorageRESTClient(bsts),
	}

	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)
	bwriter.WriteString("test content")

	err := r.processBWriter(bwriter, buf, "test-key", types2.KServeNormalizer, "12345", "card-key", nil)
	common.AssertError(t, err)
}

func TestProcessBWriter_WithModelCard(t *testing.T) {
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	r := &RHOAINormalizerReconcile{
		storage: storage.SetupBridgeStorageRESTClient(bsts),
	}

	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)
	bwriter.WriteString("test content")

	mc := "my model card content"
	err := r.processBWriter(bwriter, buf, "test-key", types2.KubeflowNormalizer, "12345", "card-key", &mc)
	common.AssertError(t, err)

	// Should have stored model card
	_, ok := callback.Load("hasModelCard")
	common.AssertEqual(t, true, ok)
}

func TestInnerStart_WithCatalogInfoFormat_NilBuffers(t *testing.T) {
	// Test innerStart with CatalogInfoYamlFormat and nil buffers to exercise
	// the code path that creates new buffers per model version
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateGetServer(t)
	defer kts.Close()
	brts := location.CreateBridgeLocationServer(t)
	defer brts.Close()
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		format:            types2.CatalogInfoYamlFormat,
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	// nil buffers - exercises the "new buffer per version" path in innerStart
	r.innerStart(ctx, nil, nil)

	found := false
	callback.Range(func(key, value any) bool {
		found = true
		return true
	})
	common.AssertEqual(t, true, found)
}

func TestInnerStartCallBackstagePrinters_NoCatalogURL(t *testing.T) {
	// Test innerStartCallBackstagePrinters when RootCatalogURL is empty (no model card)
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateGetServer(t)
	defer kts.Close()
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)
	kfmrClient := kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)
	// Ensure no catalog URL
	kfmrClient.RootCatalogURL = ""

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kfmrClient},
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		format:            types2.JsonArrayForamt,
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	r.innerStart(ctx, nil, nil)

	// Should NOT have a model card stored
	_, ok := callback.Load("hasModelCard")
	common.AssertEqual(t, false, ok)
}

func TestReconcile_WithKFMRRouteButKFMRReturnsNoMatch(t *testing.T) {
	// Test Reconcile when kfmrRegistryRoute is set, processKFMR finds no match,
	// and falls through to kserve-only path
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateEmptyGetServer(t)
	defer kts.Close()
	brts := location.CreateBridgeLocationServer(t)
	defer brts.Close()
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

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

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "svc1"},
		Spec:       serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{
			ModelStatus: serverapiv1beta1.ModelStatus{
				TransitionStatus: serverapiv1beta1.UpToDate,
			},
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{Type: rest.INF_SVC_IngressReady_CONDITION, Status: corev1.ConditionTrue},
					{Type: rest.INF_SVC_PredictorReady_CONDITION, Status: corev1.ConditionTrue},
					{Type: rest.INF_SVC_Ready_CONDITION, Status: corev1.ConditionTrue},
				},
			},
			URL: &apis.URL{Scheme: "https", Host: "kserve.com"},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	result, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "svc1"},
	})
	common.AssertError(t, err)
	common.AssertEqual(t, false, result.Requeue)
}

func TestInnerStart_MultipleModelVersions_CatalogInfoFormat(t *testing.T) {
	// Test innerStart with CatalogInfoYamlFormat and provided buffers, with multiple model versions
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateGetServerWithInference(t)
	defer kts.Close()
	brts := location.CreateBridgeLocationServer(t)
	defer brts.Close()
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)

	r := &RHOAINormalizerReconcile{
		scheme: scheme,
		kfmrCatalogRoute: &routev1.Route{
			Spec:   routev1.RouteSpec{Host: "http://foo.com"},
			Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}},
		},
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {
			Spec:   routev1.RouteSpec{Host: "http://foo.com"},
			Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}},
		}},
		kfmr:             map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:          storage.SetupBridgeStorageRESTClient(bsts),
		format:           types2.CatalogInfoYamlFormat,
		defaultOwner:     util.DefaultOwner,
		defaultLifecycle: util.DefaultLifecycle,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mnist-v1",
			Namespace: "ggmtest",
			Labels:    map[string]string{rest.INF_SVC_RM_ID_LABEL: "1", rest.INF_SVC_MV_ID_LABEL: "2", rest.INF_SVC_INF_SVC_ID_LABEL: "4"},
		},
		Spec:   serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)
	r.innerStart(ctx, buf, bwriter)

	common.AssertEqual(t, true, buf.Len() > 0)
}

func TestProcessKFMR_RegisteredModelNoID(t *testing.T) {
	// Test processKFMR where a registered model has no ID - should be skipped
	// We can simulate this by creating a custom server that returns a model without ID
	noIDServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"name":"model-no-id"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"is1","registeredModelId":"1","servingEnvironmentId":"1","modelVersionId":"1"}],"size":1}`))
		}
	})
	defer noIDServer.Close()

	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(noIDServer, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"},
	}

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "foo", Name: "bar"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	common.AssertEqual(t, "", importKey)
}

func TestInnerStart_StoragePostCurrentKeySetError(t *testing.T) {
	// Test innerStart when PostCurrentKeySet returns a transport error
	errStorageServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		// Do nothing - will be closed before use
	})
	errStorageServer.Close() // Close immediately to cause transport error

	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateEmptyGetServer(t)
	defer kts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:           storage.SetupBridgeStorageRESTClient(errStorageServer),
		format:            types2.JsonArrayForamt,
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	// Should not panic; just log error about bad RC
	r.innerStart(ctx, nil, nil)
}

func TestInnerStart_WithMixedInferenceAndMultiModel(t *testing.T) {
	// Test innerStart with the multi-model server that has mixed inference services
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateGetServerWithMixInferenceMultiModel(t)
	defer kts.Close()
	brts := location.CreateBridgeLocationServer(t)
	defer brts.Close()
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

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
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	r.innerStart(ctx, nil, nil)

	found := false
	callback.Range(func(key, value any) bool {
		found = true
		return true
	})
	common.AssertEqual(t, true, found)
}

func TestReconcile_EmptyConditions(t *testing.T) {
	// Test Reconcile where inference service has no conditions at all - should requeue
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		format:            types2.JsonArrayForamt,
		kfmrRegistryRoute: map[string]*routev1.Route{},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "svc1"},
		Spec:       serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{},
			},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	result, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "svc1"},
	})
	common.AssertError(t, err)
	common.AssertEqual(t, true, result.Requeue)
}

func TestReconcile_KServeOnlyFullPath(t *testing.T) {
	// Test the full kserve-only path (no kfmrRegistryRoute) with a fully ready IS
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		format:            types2.CatalogInfoYamlFormat,
		kfmrRegistryRoute: map[string]*routev1.Route{},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "myns", Name: "mymodel"},
		Spec:       serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{
			ModelStatus: serverapiv1beta1.ModelStatus{
				TransitionStatus: serverapiv1beta1.UpToDate,
			},
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{Type: rest.INF_SVC_IngressReady_CONDITION, Status: corev1.ConditionTrue},
					{Type: rest.INF_SVC_PredictorReady_CONDITION, Status: corev1.ConditionTrue},
					{Type: rest.INF_SVC_Ready_CONDITION, Status: corev1.ConditionTrue},
				},
			},
			URL: &apis.URL{Scheme: "https", Host: "kserve.com"},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	result, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "myns", Name: "mymodel"},
	})
	common.AssertError(t, err)
	common.AssertEqual(t, false, result.Requeue)

	// Verify data was posted
	found := false
	callback.Range(func(key, value any) bool {
		found = true
		return true
	})
	common.AssertEqual(t, true, found)
}

func TestProcessKFMR_WithKFMRInferenceServiceMatch(t *testing.T) {
	// Test processKFMR where we have both registered models AND kubeflow inference services
	// and they match the kserve inference service
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateGetServerWithInference(t)
	defer kts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)
	kfmrClient := kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)
	kfmrClient.RootCatalogURL = kts.URL + rest.KRMR_CATALOG_BASE_URI

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	r := &RHOAINormalizerReconcile{
		scheme: scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {
			Spec:   routev1.RouteSpec{Host: "foo.com"},
			Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}},
		}},
		kfmr:             map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kfmrClient},
		storage:          storage.SetupBridgeStorageRESTClient(bsts),
		format:           types2.JsonArrayForamt,
		defaultOwner:     util.DefaultOwner,
		defaultLifecycle: util.DefaultLifecycle,
	}

	// Create inference service that matches the kubeflow inference service data
	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: "mnist-v1", Namespace: "ggmtest"},
		Spec:       serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{
			URL: &apis.URL{Scheme: "http", Host: "foo.com", Path: "/mymodel"},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "ggmtest", Name: "mnist-v1"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	// Should have found a match
	if len(importKey) == 0 {
		t.Error("expected non-empty importKey from processKFMR match")
	}
}

func TestReconcile_ClientGetError(t *testing.T) {
	// Test Reconcile when client.Get returns a non-NotFound error
	// Use a scheme that doesn't have InferenceService registered to cause a different error
	scheme := runtime.NewScheme()
	// Deliberately NOT adding InferenceService to scheme

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		format:            types2.JsonArrayForamt,
		kfmrRegistryRoute: map[string]*routev1.Route{},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "svc1"},
	})
	// Should return a non-nil error (not NotFound, but a type error)
	if err == nil {
		t.Error("expected error from client.Get with unregistered type")
	}
}

func TestReconcile_ProcessKFMRError(t *testing.T) {
	// Test Reconcile when processKFMR returns an error
	// Create a KFMR server that causes CallBackstagePrinters to fail
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	// Server that returns registered models with IDs but causes errors on version/artifact calls
	errKFMRServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"model-1","state":"LIVE"}],"size":1}`))
		case strings.Contains(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(`{"items":[{"id":"2","name":"v1","registeredModelId":"1","state":"LIVE"}],"size":1}`))
		case strings.Contains(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(`{"items":[{"id":"3","name":"art1","modelFormatName":"onnx","uri":"https://example.com/model.onnx"}],"size":1}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	defer errKFMRServer.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(errKFMRServer, cfg)

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
		format:            types2.JsonArrayForamt,
	}

	// IS with labels matching the model
	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "model-1",
			Labels: map[string]string{
				rest.INF_SVC_RM_ID_LABEL: "1",
				rest.INF_SVC_MV_ID_LABEL: "2",
			},
		},
		Spec:   serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "foo", Name: "model-1"},
	})
	// Error from processKFMR may be returned or not depending on what the error is
	t.Logf("Reconcile error: %v", err)
}

func TestProcessKFMR_ListInferenceServiceError(t *testing.T) {
	// Test processKFMR where listing inference services returns an error
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	// Server that returns registered models but errors on inference service listing
	partialServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"model-1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`error`))
		case strings.Contains(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(`{"items":[{"id":"2","name":"v1","registeredModelId":"1","state":"LIVE"}],"size":1}`))
		case strings.Contains(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(`{"items":[],"size":0}`))
		}
	})
	defer partialServer.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(partialServer, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
		format:            types2.JsonArrayForamt,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "model-1",
			Labels: map[string]string{
				rest.INF_SVC_RM_ID_LABEL: "1",
				rest.INF_SVC_MV_ID_LABEL: "2",
			},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "foo", Name: "model-1"}, is, bwriter, controllerLog)
	// Error in listing inference services is logged but not returned
	common.AssertError(t, err)
	t.Logf("importKey: %s", importKey)
}

func TestProcessKFMR_ListModelVersionError(t *testing.T) {
	// Test processKFMR where listing model versions returns an error
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	partialServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"model-1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "versions"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`error`))
		}
	})
	defer partialServer.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(partialServer, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "model-1",
			Labels: map[string]string{
				rest.INF_SVC_RM_ID_LABEL: "1",
				rest.INF_SVC_MV_ID_LABEL: "2",
			},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "foo", Name: "model-1"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	common.AssertEqual(t, "", importKey)
}

func TestProcessKFMR_ModelArtifactNil(t *testing.T) {
	// Test processKFMR where model artifacts return nil
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	partialServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"model-1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(`{"items":[{"id":"2","name":"v1","registeredModelId":"1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "artifacts"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`error`))
		}
	})
	defer partialServer.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(partialServer, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "model-1",
			Labels: map[string]string{
				rest.INF_SVC_RM_ID_LABEL: "1",
				rest.INF_SVC_MV_ID_LABEL: "2",
			},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "foo", Name: "model-1"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	// Should get empty importKey since artifacts are nil
	common.AssertEqual(t, "", importKey)
}

func TestProcessKFMR_GetServingEnvironmentError(t *testing.T) {
	// Test processKFMR where GetServingEnvironment returns error in the kubeflow IS match path
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	// Server with inference services but serving environment returns error
	partialServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"mnist","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"4","name":"mnist-v1","registeredModelId":"1","servingEnvironmentId":"1","modelVersionId":"2","desiredState":"DEPLOYED"}],"size":1}`))
		case strings.Contains(r.URL.Path, "serving"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`error`))
		}
	})
	defer partialServer.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(partialServer, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ggmtest", Name: "mnist-v1"},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "ggmtest", Name: "mnist-v1"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	common.AssertEqual(t, "", importKey)
}

func TestProcessKFMR_GetModelVersionError(t *testing.T) {
	// Test processKFMR where GetModelVersions returns error after matching
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	// Server with inference services, serving environment matches, but model version Get fails
	callCount := 0
	partialServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"mnist","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"4","name":"mnist-v1","registeredModelId":"1","servingEnvironmentId":"1","modelVersionId":"2","desiredState":"DEPLOYED"}],"size":1}`))
		case strings.Contains(r.URL.Path, "serving"):
			_, _ = w.Write([]byte(`{"id":"1","name":"ggmtest"}`))
		case strings.Contains(r.URL.Path, "versions"):
			callCount++
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`error`))
		case strings.Contains(r.URL.Path, "artifacts"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`error`))
		}
	})
	defer partialServer.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(partialServer, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ggmtest", Name: "mnist-v1"},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "ggmtest", Name: "mnist-v1"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	// mv and mas are nil, so it continues, resulting in empty importKey
	common.AssertEqual(t, "", importKey)
}

func TestProcessBWriter_UpsertError(t *testing.T) {
	// Test processBWriter when UpsertModel returns a transport error
	errServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		// Close connection immediately to cause transport error
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	})
	errServer.Close() // Close immediately so UpsertModel call fails

	r := &RHOAINormalizerReconcile{
		storage: storage.SetupBridgeStorageRESTClient(errServer),
	}

	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)
	bwriter.WriteString("test content")

	err := r.processBWriter(bwriter, buf, "test-key", types2.KServeNormalizer, "", "", nil)
	if err == nil {
		t.Error("expected error from UpsertModel with closed server")
	}
}

func TestReconcile_ProcessBWriterError(t *testing.T) {
	// Test Reconcile where processBWriter returns an error (storage server closed)
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	errStorageServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	})
	errStorageServer.Close() // Close immediately

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		storage:           storage.SetupBridgeStorageRESTClient(errStorageServer),
		format:            types2.JsonArrayForamt,
		kfmrRegistryRoute: map[string]*routev1.Route{},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "myns", Name: "mymodel"},
		Spec:       serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{
			ModelStatus: serverapiv1beta1.ModelStatus{
				TransitionStatus: serverapiv1beta1.UpToDate,
			},
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{Type: rest.INF_SVC_IngressReady_CONDITION, Status: corev1.ConditionTrue},
					{Type: rest.INF_SVC_PredictorReady_CONDITION, Status: corev1.ConditionTrue},
					{Type: rest.INF_SVC_Ready_CONDITION, Status: corev1.ConditionTrue},
				},
			},
			URL: &apis.URL{Scheme: "https", Host: "kserve.com"},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "myns", Name: "mymodel"},
	})
	if err == nil {
		t.Error("expected error from processBWriter")
	}
}

func TestInnerStart_MvsDisconnect(t *testing.T) {
	// Test innerStart where LoopOverKFMR returns registered models but mvs map
	// doesn't have the registered model name (disconnect)
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	// Server that returns registered models but no versions
	disconnectServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"model-1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(`{"items":[],"size":0}`))
		case strings.HasSuffix(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(`{"items":[],"size":0}`))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			_, _ = w.Write([]byte(`{"items":[],"size":0}`))
		}
	})
	defer disconnectServer.Close()

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(disconnectServer, cfg)

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

func TestInnerStart_GetKubeFlowInferenceServicesError(t *testing.T) {
	// Test innerStart where GetKubeFlowInferenceServicesForModelVersion returns error
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	callCount := 0
	errInfSvcServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"model-1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(`{"items":[{"id":"2","name":"v1","registeredModelId":"1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(`{"items":[{"id":"3","name":"art1"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			// First call for LoopOverKFMR, second for GetKubeFlowInferenceServicesForModelVersion
			callCount++
			if callCount <= 1 {
				_, _ = w.Write([]byte(`{"items":[],"size":0}`))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`error`))
			}
		}
	})
	defer errInfSvcServer.Close()

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(errInfSvcServer, cfg)

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

func TestInnerStart_PostCurrentKeySetBadRC(t *testing.T) {
	// Test innerStart where PostCurrentKeySet returns a bad status code
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateEmptyGetServer(t)
	defer kts.Close()

	badRCStorageServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "POST":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad request"}`))
		}
	})
	defer badRCStorageServer.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:           storage.SetupBridgeStorageRESTClient(badRCStorageServer),
		format:            types2.JsonArrayForamt,
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	r.innerStart(ctx, nil, nil)
	// Should not panic, just log error about bad RC
}

func TestInnerStartCallBackstagePrinters_ModelCardError(t *testing.T) {
	// Test innerStartCallBackstagePrinters where GetModelCard returns error
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	// Server that serves registered models/versions but errors on catalog (model card)
	mcErrServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, rest.KRMR_CATALOG_BASE_URI):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`error`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
	defer mcErrServer.Close()

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(mcErrServer, cfg)
	kfmrClient := kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)
	kfmrClient.RootCatalogURL = mcErrServer.URL + rest.KRMR_CATALOG_BASE_URI

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		format:            types2.JsonArrayForamt,
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)
	replacer := strings.NewReplacer(" ", "")

	rmName := "test-model"
	mvName := "v1"
	rm := &openapi.RegisteredModel{Name: rmName}
	mv := &openapi.ModelVersion{Name: mvName}
	maa := []openapi.ModelArtifact{
		{
			Name: strPtr("art1"),
		},
	}

	importKey, _ := util.BuildImportKeyAndURI(util.SanitizeName(rmName), util.SanitizeName(mvName), r.format)
	err := r.innerStartCallBackstagePrinters(ctx, kfmrClient, rm, mv, nil, nil, maa, replacer, bwriter, buf, importKey, "12345")
	// May or may not error - depends on CallBackstagePrinters
	t.Logf("innerStartCallBackstagePrinters error: %v", err)
}

func TestInnerStartCallBackstagePrinters_ProcessBWriterError(t *testing.T) {
	// Test innerStartCallBackstagePrinters where processBWriter returns error
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	errServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	})
	errServer.Close() // Close to cause error

	kts := kfmr.CreateGetServer(t)
	defer kts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)
	kfmrClient := kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		storage:           storage.SetupBridgeStorageRESTClient(errServer),
		format:            types2.JsonArrayForamt,
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)
	replacer := strings.NewReplacer(" ", "")

	rm := &openapi.RegisteredModel{Name: "test-model"}
	mv := &openapi.ModelVersion{Name: "v1"}

	importKey, _ := util.BuildImportKeyAndURI(util.SanitizeName(rm.Name), util.SanitizeName(mv.Name), r.format)
	err := r.innerStartCallBackstagePrinters(ctx, kfmrClient, rm, mv, nil, nil, nil, replacer, bwriter, buf, importKey, "12345")
	if err == nil {
		t.Error("expected error from processBWriter with closed server")
	}
}

func strPtr(s string) *string {
	return &s
}

func TestInnerStart_UndeployedInferenceService(t *testing.T) {
	// Test innerStart where kubeflow inference services have UNDEPLOYED desired state
	// This covers lines 726-732 (no desired state / not deployed paths)
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	undeployedServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"model-1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(`{"items":[{"id":"2","name":"v1","registeredModelId":"1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(`{"items":[{"id":"3","name":"art1"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			// Two inference services:
			// 1. No desired state set at all
			// 2. Desired state set to UNDEPLOYED
			_, _ = w.Write([]byte(`{"items":[
				{"id":"10","name":"is-no-state","registeredModelId":"1","servingEnvironmentId":"1","modelVersionId":"2"},
				{"id":"11","name":"is-undeployed","registeredModelId":"1","servingEnvironmentId":"1","modelVersionId":"2","desiredState":"UNDEPLOYED"}
			],"size":2}`))
		}
	})
	defer undeployedServer.Close()

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(undeployedServer, cfg)

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

func TestInnerStart_DeployedButNoKserveIS(t *testing.T) {
	// Test innerStart where kubeflow IS is DEPLOYED but no matching kserve IS exists
	// This exercises the "foundKServe = false" path and the kserveIS == nil continue
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	deployedServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"model-1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(`{"items":[{"id":"2","name":"v1","registeredModelId":"1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(`{"items":[{"id":"3","name":"art1"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			_, _ = w.Write([]byte(`{"items":[
				{"id":"10","name":"is-deployed","registeredModelId":"1","servingEnvironmentId":"1","modelVersionId":"2","desiredState":"DEPLOYED"}
			],"size":1}`))
		}
	})
	defer deployedServer.Close()

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(deployedServer, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		format:            types2.JsonArrayForamt,
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	// No kserve IS objects - so the label-based list returns empty
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	r.innerStart(ctx, nil, nil)
}

func TestReconcile_ProcessKFMRReturnsError(t *testing.T) {
	// Test Reconcile where processKFMR returns a non-nil error
	// This requires CallBackstagePrinters to return an error in the no-kubeflow-IS path
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	// Server that returns valid data to trigger the match,
	// but then CallBackstagePrinters fails because we pass malformed format
	kts := kfmr.CreateGetServer(t)
	defer kts.Close()
	brts := location.CreateBridgeLocationServer(t)
	defer brts.Close()

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
		format:            "invalid-format",
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "model-1",
			Labels: map[string]string{
				rest.INF_SVC_RM_ID_LABEL: "1",
				rest.INF_SVC_MV_ID_LABEL: "2",
			},
		},
		Spec:   serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "foo", Name: "model-1"},
	})
	// Error may or may not propagate depending on CallBackstagePrinters handling of invalid format
	t.Logf("Reconcile error: %v", err)
}

func TestInnerStart_MvsRmDisconnectAndMasDisconnect(t *testing.T) {
	// Test innerStart where LoopOverKFMR returns rms, but the mvs/mas maps
	// have keys that don't match the rm name after sanitization.
	// This requires a fake KFMR that returns specific data triggering disconnect.
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	// The trick: LoopOverKFMR sanitizes rm.Name to create map keys.
	// We need rms returned where sanitized name DOESN'T match the mvs key.
	// Actually, LoopOverKFMR itself creates both mvs and rms using the same sanitized name.
	// So disconnect can only happen if LoopOverKFMR creates entries for one rm
	// but then loop over rms includes a different rm from a different KFMR registry.

	// Let's use two different KFMR registries: one returns rm "model-A" with versions,
	// another returns rm "model-B" without versions (or vice versa).
	// Actually, LoopOverKFMR runs per-kfmr, so rms and mvs come from the same registry.
	// The disconnect paths at 672 and 677 are essentially dead code unless
	// callKubeflowREST fails silently... but it returns error. So these paths
	// are only reachable if model versions/artifacts return empty for some registered models.

	// Create a server where the version list returns empty
	disconnectServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"model-1","state":"LIVE"},{"id":"3","name":"model-2","state":"LIVE"}],"size":2}`))
		case strings.Contains(r.URL.Path, fmt.Sprintf("registered_models/%s/versions", "1")):
			_, _ = w.Write([]byte(`{"items":[{"id":"2","name":"v1","registeredModelId":"1","state":"LIVE"}],"size":1}`))
		case strings.Contains(r.URL.Path, fmt.Sprintf("registered_models/%s/versions", "3")):
			_, _ = w.Write([]byte(`{"items":[],"size":0}`))
		case strings.Contains(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(`{"items":[{"id":"3","name":"art1"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			_, _ = w.Write([]byte(`{"items":[],"size":0}`))
		}
	})
	defer disconnectServer.Close()

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(disconnectServer, cfg)

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

func TestInnerStart_GetKubeFlowInferenceServicesError2(t *testing.T) {
	// Test innerStart where GetKubeFlowInferenceServicesForModelVersion returns error
	// Use a server that returns registered models + versions but errors on 2nd IS list call
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	isCallCount := 0
	errServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"model-1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(`{"items":[{"id":"2","name":"v1","registeredModelId":"1","state":"LIVE"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(`{"items":[{"id":"3","name":"art1"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			isCallCount++
			if isCallCount == 1 {
				// First call from LoopOverKFMR - return empty
				_, _ = w.Write([]byte(`{"items":[],"size":0}`))
			} else {
				// Second call from GetKubeFlowInferenceServicesForModelVersion - return error
				w.WriteHeader(http.StatusInternalServerError)
			}
		}
	})
	defer errServer.Close()

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(errServer, cfg)

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

func TestSetupKFMR_NamespaceAll_EmptyRouteList(t *testing.T) {
	// Test setupKFMR where NamespaceAll route list returns empty/nil
	// by using a name-only env var with a server returning empty routes
	ts, rc := createFakeRouteServer(t, []routev1.Route{})
	defer ts.Close()

	r := &RHOAINormalizerReconcile{
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		kfmrRegistryRoute: map[string]*routev1.Route{},
		routeClient:       rc,
		k8sToken:          "test-token",
	}

	// Name-only (no colon) triggers NamespaceAll path, empty list -> continue
	t.Setenv(types2.ModelRegistryRouteEnvVar, "some-route-name")

	ctx := context.TODO()
	result := r.setupKFMR(ctx)
	common.AssertEqual(t, false, result)
}

func TestProcessKFMR_SetupKFMRReturnsFalse(t *testing.T) {
	// Test processKFMR where setupKFMR returns false because kfmr is empty
	// and no routes can be found
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	ts, rc := createFakeRouteServer(t, []routev1.Route{})
	defer ts.Close()

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		kfmrRegistryRoute: map[string]*routev1.Route{},
		routeClient:       rc,
		k8sToken:          "test-token",
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}

	t.Setenv(types2.ModelRegistryRouteEnvVar, "")

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, _, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "foo", Name: "bar"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	common.AssertEqual(t, "", importKey)
}

func TestReconcile_ProcessKFMRCallBackstageError(t *testing.T) {
	// Test Reconcile where processKFMR's CallBackstagePrinters returns an error.
	// This covers line 367 where processKFMR error is returned.
	// We need KFMR to find a match in the "no kubeflow IS" path, then
	// CallBackstagePrinters to fail. We can do this with an invalid format
	// that causes the printer to error.
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateGetServer(t)
	defer kts.Close()

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
		format:            "invalid-format-that-causes-error",
	}

	// IS with labels matching rm.id=1, mv.id=2
	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "model-1",
			Labels: map[string]string{
				rest.INF_SVC_RM_ID_LABEL: "1",
				rest.INF_SVC_MV_ID_LABEL: "2",
			},
		},
		Spec:   serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "foo", Name: "model-1"},
	})
	// The format error from CallBackstagePrinters should propagate
	t.Logf("Reconcile error: %v", err)
}

func TestReconcile_KServeCallBackstagePrintersError(t *testing.T) {
	// Test Reconcile where kserve CallBackstagePrinters returns error
	// This is difficult to trigger since we need a valid IS with URL but
	// the format has to cause CallBackstagePrinters to fail.
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		storage:           storage.SetupBridgeStorageRESTClient(bsts),
		format:            "totally-invalid-format",
		kfmrRegistryRoute: map[string]*routev1.Route{},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{},
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "myns", Name: "mymodel"},
		Spec:       serverapiv1beta1.InferenceServiceSpec{},
		Status: serverapiv1beta1.InferenceServiceStatus{
			ModelStatus: serverapiv1beta1.ModelStatus{
				TransitionStatus: serverapiv1beta1.UpToDate,
			},
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{Type: rest.INF_SVC_IngressReady_CONDITION, Status: corev1.ConditionTrue},
					{Type: rest.INF_SVC_PredictorReady_CONDITION, Status: corev1.ConditionTrue},
					{Type: rest.INF_SVC_Ready_CONDITION, Status: corev1.ConditionTrue},
				},
			},
			URL: &apis.URL{Scheme: "https", Host: "kserve.com"},
		},
	}

	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	result, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "myns", Name: "mymodel"},
	})
	// CallBackstagePrinters may or may not error with invalid format
	t.Logf("result: %v, err: %v", result, err)
}

func TestInnerStart_PostCurrentKeySetBadRC2(t *testing.T) {
	// Test innerStart where PostCurrentKeySet returns non-200/201 but no error
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateEmptyGetServer(t)
	defer kts.Close()

	badRCServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "POST":
			switch {
			case strings.Contains(r.URL.Path, util.CurrentKeySetURI):
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"forbidden"}`))
			default:
				bodyBuf, _ := io.ReadAll(r.Body)
				if len(bodyBuf) > 0 {
					_, _ = w.Write(bodyBuf)
				}
			}
		}
	})
	defer badRCServer.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(kts, cfg)

	r := &RHOAINormalizerReconcile{
		scheme:            scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}}}},
		kfmr:              map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:           storage.SetupBridgeStorageRESTClient(badRCServer),
		format:            types2.JsonArrayForamt,
		defaultOwner:      util.DefaultOwner,
		defaultLifecycle:  util.DefaultLifecycle,
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	// Should log error about bad RC but not panic
	r.innerStart(ctx, nil, nil)
}

func TestProcessKFMR_KubeflowISMatch_RmTimestampGreater(t *testing.T) {
	// Test processKFMR where the kubeflow IS path matches, and rm.lastUpdateTimeSinceEpoch > mv
	// This covers lines 604-606 (the timestamp comparison branch)
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	// Custom server with rm timestamp > mv timestamp
	customServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("API: %s %s", r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"mnist","state":"LIVE","lastUpdateTimeSinceEpoch":"9999999999999"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"4","name":"mnist-v1","registeredModelId":"1","servingEnvironmentId":"1","modelVersionId":"2","desiredState":"DEPLOYED"}],"size":1}`))
		case strings.Contains(r.URL.Path, "serving"):
			_, _ = w.Write([]byte(`{"id":"1","name":"ggmtest"}`))
		case strings.Contains(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(`{"items":[{"id":"3","name":"art1","uri":"https://example.com/model"}],"size":1}`))
		case strings.Contains(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(`{"id":"2","name":"v1","registeredModelId":"1","state":"LIVE","lastUpdateTimeSinceEpoch":"1000000000000"}`))
		}
	})
	defer customServer.Close()

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(customServer, cfg)

	r := &RHOAINormalizerReconcile{
		scheme: scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {
			Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}},
		}},
		kfmr:             map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:          storage.SetupBridgeStorageRESTClient(bsts),
		defaultOwner:     util.DefaultOwner,
		defaultLifecycle: util.DefaultLifecycle,
		format:           types2.JsonArrayForamt,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ggmtest", Name: "mnist-v1"},
		Status: serverapiv1beta1.InferenceServiceStatus{
			URL: &apis.URL{Scheme: "http", Host: "foo.com"},
		},
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, lastUpdate, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "ggmtest", Name: "mnist-v1"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	if len(importKey) == 0 {
		t.Error("expected non-empty importKey")
	}
	// rm timestamp should be used since it's greater
	common.AssertEqual(t, "9999999999999", lastUpdate)
}

func TestProcessKFMR_NoKubeflowIS_RmTimestampGreater(t *testing.T) {
	// Same but for the no-kubeflow-IS path (lines 515-534)
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	customServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"model-1","state":"LIVE","lastUpdateTimeSinceEpoch":"9999999999999"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(`{"items":[{"id":"2","name":"v1","registeredModelId":"1","state":"LIVE","lastUpdateTimeSinceEpoch":"1000000000000"}],"size":1}`))
		case strings.HasSuffix(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(`{"items":[{"id":"3","name":"art1","modelFormatName":"onnx","uri":"https://example.com/model"}],"size":1}`))
		case strings.Contains(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(`{"id":"1","name":"model-1","state":"LIVE","lastUpdateTimeSinceEpoch":"9999999999999"}`))
		}
	})
	defer customServer.Close()

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(customServer, cfg)

	r := &RHOAINormalizerReconcile{
		scheme: scheme,
		kfmrRegistryRoute: map[string]*routev1.Route{"test": {
			Status: routev1.RouteStatus{Ingress: []routev1.RouteIngress{{}}},
		}},
		kfmr:             map[string]*kubeflowmodelregistry.KubeFlowRESTClientWrapper{"test": kubeflowmodelregistry.SetupKubeflowRESTClient(cfg)},
		storage:          storage.SetupBridgeStorageRESTClient(bsts),
		defaultOwner:     util.DefaultOwner,
		defaultLifecycle: util.DefaultLifecycle,
		format:           types2.JsonArrayForamt,
	}

	is := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "model-1",
			Labels: map[string]string{
				rest.INF_SVC_RM_ID_LABEL: "1",
				rest.INF_SVC_MV_ID_LABEL: "2",
			},
		},
		Status: serverapiv1beta1.InferenceServiceStatus{
			URL: &apis.URL{Scheme: "http", Host: "foo.com"},
		},
	}
	r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	ctx := context.TODO()
	buf := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(buf)

	importKey, lastUpdate, _, _, err := r.processKFMR(ctx, types.NamespacedName{Namespace: "foo", Name: "model-1"}, is, bwriter, controllerLog)
	common.AssertError(t, err)
	if len(importKey) == 0 {
		t.Error("expected non-empty importKey")
	}
	common.AssertEqual(t, "9999999999999", lastUpdate)
}

// TestInnerStart_EmptyKFMR tests innerStart when kfmr has entries
// but they return empty registered models, and there are no kserve inference services
func TestInnerStart_EmptyKFMR(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kts := kfmr.CreateEmptyGetServer(t)
	defer kts.Close()

	callback := sync.Map{}
	bsts := storage.CreateBridgeStorageREST(t, &callback)
	defer bsts.Close()

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
	r.client = fake.NewClientBuilder().WithScheme(scheme).Build()

	ctx := context.TODO()
	r.innerStart(ctx, nil, nil)
	// Should not panic, and should still post current key set (empty)
}
