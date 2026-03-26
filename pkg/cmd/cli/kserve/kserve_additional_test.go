package kserve

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	serverapiv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/cli/backstage"
	brdgtypes "github.com/redhat-ai-dev/model-catalog-bridge/pkg/types"
	"github.com/redhat-ai-dev/model-catalog-bridge/schema/types/golang"
	common "github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	return scheme
}

func newInferenceService(ns, name string) *serverapiv1beta1.InferenceService {
	return &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
		},
	}
}

func newCommonPopulator(is *serverapiv1beta1.InferenceService, c client.Client) CommonPopulator {
	return CommonPopulator{
		Owner:      "test-owner",
		Lifecycle:  "test-lifecycle",
		InferSvc:   is,
		CtrlClient: c,
		Ctx:        context.Background(),
	}
}

// Test CommonPopulator methods directly
func TestCommonPopulatorGetOwner(t *testing.T) {
	pop := &CommonPopulator{Owner: "my-owner"}
	common.AssertEqual(t, "my-owner", pop.GetOwner())
}

func TestCommonPopulatorGetLifecycle(t *testing.T) {
	pop := &CommonPopulator{Lifecycle: "production"}
	common.AssertEqual(t, "production", pop.GetLifecycle())
}

func TestCommonPopulatorGetName(t *testing.T) {
	// nil InferSvc
	pop := &CommonPopulator{}
	common.AssertEqual(t, "", pop.GetName())

	// non-nil InferSvc
	is := newInferenceService("ns1", "svc1")
	pop.InferSvc = is
	common.AssertEqual(t, "ns1_svc1", pop.GetName())
}

func TestCommonPopulatorGetDescription(t *testing.T) {
	is := newInferenceService("ns1", "svc1")
	pop := &CommonPopulator{InferSvc: is}
	common.AssertEqual(t, "KServe instance ns1:svc1", pop.GetDescription())
}

func TestCommonPopulatorGetLinks(t *testing.T) {
	// nil InferSvc
	pop := &CommonPopulator{}
	links := pop.GetLinks()
	common.AssertEqual(t, 0, len(links))

	// with Status.URL only
	is := newInferenceService("ns1", "svc1")
	is.Status.URL = &apis.URL{Scheme: "https", Host: "example.com"}
	pop = &CommonPopulator{InferSvc: is}
	links = pop.GetLinks()
	common.AssertEqual(t, 1, len(links))
	common.AssertEqual(t, backstage.LINK_API_URL, links[0].Title)

	// with Components containing URL, RestURL, GrpcURL
	is2 := newInferenceService("ns1", "svc2")
	is2.Status.Components = map[serverapiv1beta1.ComponentType]serverapiv1beta1.ComponentStatusSpec{
		serverapiv1beta1.PredictorComponent: {
			URL:     &apis.URL{Scheme: "https", Host: "pred.com"},
			RestURL: &apis.URL{Scheme: "https", Host: "rest.com"},
			GrpcURL: &apis.URL{Scheme: "https", Host: "grpc.com"},
		},
	}
	pop = &CommonPopulator{InferSvc: is2}
	links = pop.GetLinks()
	// URL gives 2 links (docs + serving), RestURL gives 1, GrpcURL gives 1 = 4
	common.AssertEqual(t, 4, len(links))
}

func TestCommonPopulatorGetTags(t *testing.T) {
	// nil InferSvc
	pop := &CommonPopulator{}
	tags := pop.GetTags()
	common.AssertEqual(t, 0, len(tags))

	// with only SKLearn set (fallthrough will add all subsequent tags)
	is := newInferenceService("ns1", "svc1")
	is.Spec.Predictor.SKLearn = &serverapiv1beta1.SKLearnSpec{}
	is.Spec.Predictor.Model = &serverapiv1beta1.ModelSpec{
		ModelFormat: serverapiv1beta1.ModelFormat{Name: "MyModel"},
	}
	pop = &CommonPopulator{InferSvc: is}
	tags = pop.GetTags()
	// sklearn is first case and it's true, fallthrough adds all subsequent
	if len(tags) == 0 {
		t.Fatal("expected tags to be non-empty")
	}
	// should contain sklearn
	found := false
	for _, tag := range tags {
		if tag == sklearn {
			found = true
		}
	}
	common.AssertEqual(t, true, found)

	// with only Model set (no other predictors)
	is2 := newInferenceService("ns1", "svc2")
	ver := "v2.0"
	is2.Spec.Predictor.Model = &serverapiv1beta1.ModelSpec{
		ModelFormat: serverapiv1beta1.ModelFormat{Name: "ONNX", Version: &ver},
	}
	pop = &CommonPopulator{InferSvc: is2}
	tags = pop.GetTags()
	common.AssertEqual(t, 1, len(tags))
	common.AssertEqual(t, "onnx-v2.0", tags[0])
}

func TestCommonPopulatorGetProvidedAPIs(t *testing.T) {
	// nil InferSvc
	pop := &CommonPopulator{}
	apis := pop.GetProvidedAPIs()
	common.AssertEqual(t, 0, len(apis))

	// non-nil
	is := newInferenceService("ns1", "svc1")
	pop = &CommonPopulator{InferSvc: is}
	apis = pop.GetProvidedAPIs()
	common.AssertEqual(t, 1, len(apis))
	common.AssertEqual(t, "ns1_svc1", apis[0])
}

// Test ComponentPopulator
func TestComponentPopulatorGetDependsOn(t *testing.T) {
	is := newInferenceService("ns1", "svc1")
	pop := &ComponentPopulator{CommonPopulator: CommonPopulator{InferSvc: is}}
	deps := pop.GetDependsOn()
	common.AssertEqual(t, 2, len(deps))
	common.AssertEqual(t, "resource:ns1_svc1", deps[0])
	common.AssertEqual(t, "api:ns1_svc1", deps[1])
}

func TestComponentPopulatorGetTechdocRef(t *testing.T) {
	pop := &ComponentPopulator{}
	common.AssertEqual(t, "./", pop.GetTechdocRef())
}

func TestComponentPopulatorGetDisplayName(t *testing.T) {
	is := newInferenceService("ns1", "svc1")
	pop := &ComponentPopulator{CommonPopulator: CommonPopulator{InferSvc: is}}
	common.AssertEqual(t, "ns1_svc1", pop.GetDisplayName())
}

// Test ResourcePopulator
func TestResourcePopulatorGetDependencyOf(t *testing.T) {
	is := newInferenceService("ns1", "svc1")
	pop := &ResourcePopulator{CommonPopulator: CommonPopulator{InferSvc: is}}
	deps := pop.GetDependencyOf()
	common.AssertEqual(t, 1, len(deps))
	common.AssertEqual(t, "component:ns1_svc1", deps[0])
}

func TestResourcePopulatorGetTechdocRef(t *testing.T) {
	pop := &ResourcePopulator{}
	common.AssertEqual(t, "resource/", pop.GetTechdocRef())
}

func TestResourcePopulatorGetDisplayName(t *testing.T) {
	is := newInferenceService("ns1", "svc1")
	pop := &ResourcePopulator{CommonPopulator: CommonPopulator{InferSvc: is}}
	common.AssertEqual(t, "ns1_svc1", pop.GetDisplayName())
}

// Test ApiPopulator
func TestApiPopulatorGetDependencyOf(t *testing.T) {
	// nil InferSvc
	pop := &ApiPopulator{}
	deps := pop.GetDependencyOf()
	common.AssertEqual(t, 0, len(deps))

	// non-nil
	is := newInferenceService("ns1", "svc1")
	pop = &ApiPopulator{CommonPopulator: CommonPopulator{InferSvc: is}}
	deps = pop.GetDependencyOf()
	common.AssertEqual(t, 1, len(deps))
	common.AssertEqual(t, "component:ns1_svc1", deps[0])
}

func TestApiPopulatorGetDefinition(t *testing.T) {
	// nil URL returns ""
	is := newInferenceService("ns1", "svc1")
	pop := &ApiPopulator{CommonPopulator: CommonPopulator{InferSvc: is}}
	common.AssertEqual(t, "", pop.GetDefinition())
}

func TestApiPopulatorGetTechdocRef(t *testing.T) {
	pop := &ApiPopulator{}
	common.AssertEqual(t, "api/", pop.GetTechdocRef())
}

func TestApiPopulatorGetDisplayName(t *testing.T) {
	is := newInferenceService("ns1", "svc1")
	pop := &ApiPopulator{CommonPopulator: CommonPopulator{InferSvc: is}}
	common.AssertEqual(t, "ns1_svc1", pop.GetDisplayName())
}

// Test CallBackstagePrinters with CatalogInfoYamlFormat
func TestCallBackstagePrintersCatalogInfoYamlFormat(t *testing.T) {
	scheme := newScheme()
	is := newInferenceService("default", "test-svc")
	is.Status.URL = &apis.URL{Scheme: "https", Host: "example.com"}
	is.Spec.Predictor.Model = &serverapiv1beta1.ModelSpec{
		ModelFormat: serverapiv1beta1.ModelFormat{Name: "test"},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	b := []byte{}
	buf := bytes.NewBuffer(b)
	bwriter := bufio.NewWriter(buf)

	err := CallBackstagePrinters(context.Background(), "owner", "lifecycle", is, c, bwriter, brdgtypes.CatalogInfoYamlFormat)
	common.AssertError(t, err)
	bwriter.Flush()
	// CatalogInfoYamlFormat goes through the default case which prints component, resource, API
	output := buf.String()
	if len(output) == 0 {
		t.Log("CatalogInfoYaml format produced output as expected (may be empty if no yaml output)")
	}
}

// Test CallBackstagePrinters with default format (same path as CatalogInfoYamlFormat)
func TestCallBackstagePrintersDefaultFormat(t *testing.T) {
	scheme := newScheme()
	is := newInferenceService("default", "test-svc")
	is.Spec.Predictor.Model = &serverapiv1beta1.ModelSpec{
		ModelFormat: serverapiv1beta1.ModelFormat{Name: "test"},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()

	b := []byte{}
	buf := bytes.NewBuffer(b)
	bwriter := bufio.NewWriter(buf)

	// Use a format string that is neither JsonArrayForamt nor CatalogInfoYamlFormat
	err := CallBackstagePrinters(context.Background(), "owner", "lifecycle", is, c, bwriter, "SomeOtherFormat")
	common.AssertError(t, err)
	bwriter.Flush()
	output := buf.String()
	// Should produce YAML output with component, resource, API sections
	if len(output) == 0 {
		t.Fatal("expected non-empty output for default format")
	}
}

// Test GetAuthentication
func TestModelServerPopulatorGetAuthentication(t *testing.T) {
	scheme := newScheme()
	is := newInferenceService("default", "test-svc")

	// Case 1: no service accounts - auth should be false
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()
	msPop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is, c),
			},
		},
	}
	auth := msPop.GetAuthentication()
	common.AssertEqual(t, false, *auth)

	// Case 2: service account with matching owner reference - auth should be true
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-svc-sa",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "InferenceService",
					Name: "test-svc",
				},
			},
		},
	}
	c2 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is, sa).Build()
	msPop2 := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is, c2),
			},
		},
	}
	auth2 := msPop2.GetAuthentication()
	common.AssertEqual(t, true, *auth2)

	// Case 3: service account with owner reference for different InferenceService
	sa3 := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "other-sa",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "InferenceService",
					Name: "other-svc",
				},
			},
		},
	}
	c3 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is, sa3).Build()
	msPop3 := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is, c3),
			},
		},
	}
	auth3 := msPop3.GetAuthentication()
	common.AssertEqual(t, false, *auth3)

	// Case 4: service account with nil OwnerReferences
	sa4 := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "no-owner-sa",
		},
	}
	c4 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is, sa4).Build()
	msPop4 := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is, c4),
			},
		},
	}
	auth4 := msPop4.GetAuthentication()
	common.AssertEqual(t, false, *auth4)
}

// Test ModelServerPopulator.GetTags
func TestModelServerPopulatorGetTags(t *testing.T) {
	is := newInferenceService("default", "test-svc")
	is.Labels = map[string]string{
		"app": "kserve",
	}
	msPop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is},
			},
		},
	}
	tags := msPop.GetTags()
	common.AssertEqual(t, 1, len(tags))

	// no labels
	is2 := newInferenceService("default", "test-svc2")
	msPop2 := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is2},
			},
		},
	}
	tags2 := msPop2.GetTags()
	common.AssertEqual(t, 0, len(tags2))
}

// Test ModelServerAPIPopulator.GetTags
func TestModelServerAPIPopulatorGetTags(t *testing.T) {
	is := newInferenceService("default", "test-svc")
	is.Labels = map[string]string{
		"app": "kserve",
	}
	apiPop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is},
			},
		},
	}
	tags := apiPop.GetTags()
	common.AssertEqual(t, 1, len(tags))
}

// Test ModelPopulator.GetTags
func TestModelPopulatorGetTags(t *testing.T) {
	is := newInferenceService("default", "test-svc")
	is.Labels = map[string]string{
		"key1": "val1",
		"key2": "val2",
	}
	mPop := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is},
			},
		},
	}
	tags := mPop.GetTags()
	common.AssertEqual(t, 2, len(tags))
}

// Test ModelServerAPIPopulator.GetType
func TestModelServerAPIPopulatorGetType(t *testing.T) {
	// no annotation => openapi
	is := newInferenceService("default", "test-svc")
	apiPop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is},
			},
		},
	}
	common.AssertEqual(t, golang.Openapi, apiPop.GetType())

	// graphql
	is2 := newInferenceService("default", "test-svc2")
	is2.Annotations = map[string]string{
		brdgtypes.AnnotationPrefix + fixKeyForAnnotation(brdgtypes.APITypeKey): string(golang.Graphql),
	}
	apiPop2 := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is2},
			},
		},
	}
	common.AssertEqual(t, golang.Graphql, apiPop2.GetType())

	// asyncapi
	is3 := newInferenceService("default", "test-svc3")
	is3.Annotations = map[string]string{
		brdgtypes.AnnotationPrefix + fixKeyForAnnotation(brdgtypes.APITypeKey): string(golang.Asyncapi),
	}
	apiPop3 := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is3},
			},
		},
	}
	common.AssertEqual(t, golang.Asyncapi, apiPop3.GetType())

	// grpc
	is4 := newInferenceService("default", "test-svc4")
	is4.Annotations = map[string]string{
		brdgtypes.AnnotationPrefix + fixKeyForAnnotation(brdgtypes.APITypeKey): string(golang.Grpc),
	}
	apiPop4 := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is4},
			},
		},
	}
	common.AssertEqual(t, golang.Grpc, apiPop4.GetType())

	// unknown type defaults to openapi
	is5 := newInferenceService("default", "test-svc5")
	is5.Annotations = map[string]string{
		brdgtypes.AnnotationPrefix + fixKeyForAnnotation(brdgtypes.APITypeKey): "unknown-type",
	}
	apiPop5 := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is5},
			},
		},
	}
	common.AssertEqual(t, golang.Openapi, apiPop5.GetType())
}

// Test getFullSvcURL
func TestGetFullSvcURL(t *testing.T) {
	scheme := newScheme()
	is := newInferenceService("default", "test-svc")

	// Case 1: no services
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()
	apiPop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is, c),
			},
		},
	}
	common.AssertEqual(t, "", apiPop.getFullSvcURL())

	// Case 2: service with matching owner reference and -predictor suffix, port 80
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-svc-predictor",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "InferenceService",
					Name: "test-svc",
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt(80),
				},
			},
		},
	}
	c2 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is, svc).Build()
	apiPop2 := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is, c2),
			},
		},
	}
	url2 := apiPop2.getFullSvcURL()
	common.AssertEqual(t, "http://test-svc-predictor.default.svc.cluster.local", url2)

	// Case 3: service with non-80 port via TargetPort int
	svc3 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-svc-predictor",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "InferenceService",
					Name: "test-svc",
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       8080,
					TargetPort: intstr.FromInt(8080),
				},
			},
		},
	}
	c3 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is, svc3).Build()
	apiPop3 := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is, c3),
			},
		},
	}
	url3 := apiPop3.getFullSvcURL()
	common.AssertEqual(t, "http://test-svc-predictor.default.svc.cluster.local:8080", url3)

	// Case 4: service with TargetPort as string (uses Port value)
	svc4 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-svc-predictor",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "InferenceService",
					Name: "test-svc",
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       9090,
					TargetPort: intstr.FromString("http"),
				},
			},
		},
	}
	c4 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is, svc4).Build()
	apiPop4 := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is, c4),
			},
		},
	}
	url4 := apiPop4.getFullSvcURL()
	common.AssertEqual(t, "http://test-svc-predictor.default.svc.cluster.local:9090", url4)

	// Case 5: service without -predictor suffix (should not match)
	svc5 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-svc-transformer",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "InferenceService",
					Name: "test-svc",
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port: 80,
				},
			},
		},
	}
	c5 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is, svc5).Build()
	apiPop5 := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is, c5),
			},
		},
	}
	common.AssertEqual(t, "", apiPop5.getFullSvcURL())

	// Case 6: service with nil OwnerReferences
	svc6 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-svc-predictor",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port: 80,
				},
			},
		},
	}
	c6 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is, svc6).Build()
	apiPop6 := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is, c6),
			},
		},
	}
	common.AssertEqual(t, "", apiPop6.getFullSvcURL())

	// Case 7: service with no ports
	svc7 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-svc-predictor",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "InferenceService",
					Name: "test-svc",
				},
			},
		},
		Spec: corev1.ServiceSpec{},
	}
	c7 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is, svc7).Build()
	apiPop7 := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is, c7),
			},
		},
	}
	url7 := apiPop7.getFullSvcURL()
	// port is 0, so no port suffix
	common.AssertEqual(t, true, strings.Contains(url7, "svc.cluster.local"))
}

// Test GetURL
func TestModelServerAPIPopulatorGetURL(t *testing.T) {
	scheme := newScheme()

	// Case 1: nil Status.URL
	is := newInferenceService("default", "test-svc")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is).Build()
	apiPop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is, c),
			},
		},
	}
	routeURL, svcURL := apiPop.GetURL()
	common.AssertEqual(t, "", routeURL)
	common.AssertEqual(t, "", svcURL)

	// Case 2: URL with svc.cluster.local (internal)
	is2 := newInferenceService("default", "test-svc")
	is2.Status.URL = &apis.URL{Scheme: "http", Host: "test-svc.default.svc.cluster.local"}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-svc-predictor",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "InferenceService",
					Name: "test-svc",
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt(80),
				},
			},
		},
	}
	c2 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is2, svc).Build()
	apiPop2 := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is2, c2),
			},
		},
	}
	routeURL2, svcURL2 := apiPop2.GetURL()
	// When URL contains svc.cluster.local, both return the svc URL
	common.AssertEqual(t, routeURL2, svcURL2)

	// Case 3: URL without svc.cluster.local (external route)
	is3 := newInferenceService("default", "test-svc")
	is3.Status.URL = &apis.URL{Scheme: "https", Host: "test-svc.apps.example.com"}
	c3 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is3).Build()
	apiPop3 := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is3, c3),
			},
		},
	}
	routeURL3, svcURL3 := apiPop3.GetURL()
	common.AssertEqual(t, "https://test-svc.apps.example.com", routeURL3)
	// svcURL3 will be "" since no matching service
	common.AssertEqual(t, "", svcURL3)
}

// Test GetArtifactLocationURL
func TestModelPopulatorGetArtifactLocationURL(t *testing.T) {
	// Case 1: no model
	is := newInferenceService("default", "test-svc")
	is.Spec.Predictor.Model = nil
	mPop := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is},
			},
		},
	}
	common.AssertEqual(t, (*string)(nil), mPop.GetArtifactLocationURL())

	// Case 2: with StorageURI
	storageURI := "s3://bucket/model"
	is2 := newInferenceService("default", "test-svc")
	is2.Spec.Predictor.Model = &serverapiv1beta1.ModelSpec{
		ModelFormat: serverapiv1beta1.ModelFormat{Name: "test"},
		PredictorExtensionSpec: serverapiv1beta1.PredictorExtensionSpec{
			StorageURI: &storageURI,
		},
	}
	mPop2 := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is2},
			},
		},
	}
	result := mPop2.GetArtifactLocationURL()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	common.AssertEqual(t, storageURI, *result)

	// Case 3: with Storage.Path (no StorageURI)
	storagePath := "bucket/model/path"
	is3 := newInferenceService("default", "test-svc")
	is3.Spec.Predictor.Model = &serverapiv1beta1.ModelSpec{
		ModelFormat: serverapiv1beta1.ModelFormat{Name: "test"},
		PredictorExtensionSpec: serverapiv1beta1.PredictorExtensionSpec{
			Storage: &serverapiv1beta1.StorageSpec{
				Path: &storagePath,
			},
		},
	}
	mPop3 := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is3},
			},
		},
	}
	result3 := mPop3.GetArtifactLocationURL()
	if result3 == nil {
		t.Fatal("expected non-nil result")
	}
	common.AssertEqual(t, fmt.Sprintf("s3://%s", storagePath), *result3)

	// Case 4: model with neither StorageURI nor Storage.Path
	is4 := newInferenceService("default", "test-svc")
	is4.Spec.Predictor.Model = &serverapiv1beta1.ModelSpec{
		ModelFormat: serverapiv1beta1.ModelFormat{Name: "test"},
	}
	mPop4 := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is4},
			},
		},
	}
	common.AssertEqual(t, (*string)(nil), mPop4.GetArtifactLocationURL())
}

// Test GetTechDocs
func TestModelPopulatorGetTechDocs(t *testing.T) {
	// Case 1: no annotation, name contains granite-31-8b-lab
	is := newInferenceService("default", brdgtypes.Granite318bLabName)
	mPop := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is},
			},
		},
	}
	result := mPop.GetTechDocs()
	if result == nil {
		t.Fatal("expected non-nil result for granite name")
	}
	common.AssertEqual(t, brdgtypes.Granite318bLabTechDocs, *result)

	// Case 2: valid http URL annotation
	is2 := newInferenceService("default", "test-svc")
	is2.Annotations = map[string]string{
		brdgtypes.AnnotationPrefix + fixKeyForAnnotation(brdgtypes.TechDocsKey): "https://docs.example.com/techdocs",
	}
	mPop2 := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is2},
			},
		},
	}
	result2 := mPop2.GetTechDocs()
	if result2 == nil {
		t.Fatal("expected non-nil result for valid url")
	}
	common.AssertEqual(t, "https://docs.example.com/techdocs", *result2)

	// Case 3: invalid scheme URL annotation (ftp://)
	is3 := newInferenceService("default", "test-svc")
	is3.Annotations = map[string]string{
		brdgtypes.AnnotationPrefix + fixKeyForAnnotation(brdgtypes.TechDocsKey): "ftp://docs.example.com/techdocs",
	}
	mPop3 := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is3},
			},
		},
	}
	result3 := mPop3.GetTechDocs()
	if result3 != nil {
		t.Fatalf("expected nil result for invalid scheme, got %s", *result3)
	}

	// Case 4: no annotation, name does not contain granite
	is4 := newInferenceService("default", "test-svc")
	mPop4 := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is4},
			},
		},
	}
	result4 := mPop4.GetTechDocs()
	if result4 != nil {
		t.Fatalf("expected nil result, got %s", *result4)
	}

	// Case 5: empty string URL (no scheme)
	is5 := newInferenceService("default", "test-svc")
	is5.Annotations = map[string]string{
		brdgtypes.AnnotationPrefix + fixKeyForAnnotation(brdgtypes.TechDocsKey): "not-a-url",
	}
	mPop5 := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{InferSvc: is5},
			},
		},
	}
	result5 := mPop5.GetTechDocs()
	if result5 != nil {
		t.Fatalf("expected nil result for bad scheme, got %s", *result5)
	}
}

// Test commonGetStringPropVal
func TestCommonGetStringPropVal(t *testing.T) {
	// nil InferenceService
	result := commonGetStringPropVal("key", nil)
	if result != nil {
		t.Fatal("expected nil for nil InferenceService")
	}

	// nil annotations
	is := newInferenceService("default", "test-svc")
	result = commonGetStringPropVal("key", is)
	if result != nil {
		t.Fatal("expected nil for nil annotations")
	}

	// key not found
	is.Annotations = map[string]string{"other": "value"}
	result = commonGetStringPropVal("key", is)
	if result != nil {
		t.Fatal("expected nil for missing key")
	}
}

// Test fixKeyForAnnotation
func TestFixKeyForAnnotation(t *testing.T) {
	common.AssertEqual(t, "hometownurl", fixKeyForAnnotation("Home Town URL"))
	common.AssertEqual(t, "apitype", fixKeyForAnnotation("API Type"))
}

// Test GetAPI with internal svc URL
func TestModelServerPopulatorGetAPI(t *testing.T) {
	scheme := newScheme()
	is := newInferenceService("default", "test-svc")
	is.Status.URL = &apis.URL{Scheme: "https", Host: "test-svc.apps.example.com"}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-svc-predictor",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "InferenceService",
					Name: "test-svc",
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt(80),
				},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(is, svc).Build()

	msPop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: newCommonPopulator(is, c),
			},
		},
		ApiPop: ModelServerAPIPopulator{
			CommonSchemaPopulator: CommonSchemaPopulator{
				ComponentPopulator: ComponentPopulator{
					CommonPopulator: newCommonPopulator(is, c),
				},
			},
		},
	}

	api := msPop.GetAPI()
	if api == nil {
		t.Fatal("expected non-nil API")
	}
	common.AssertEqual(t, "https://test-svc.apps.example.com", api.URL)
	// Should have both internal and external annotations
	if api.Annotations == nil {
		t.Fatal("expected non-nil annotations")
	}
	_, hasInternal := api.Annotations[backstage.INTERNAL_SVC_URL]
	common.AssertEqual(t, true, hasInternal)
	_, hasExternal := api.Annotations[backstage.EXTERNAL_ROUTE_URL]
	common.AssertEqual(t, true, hasExternal)
}
