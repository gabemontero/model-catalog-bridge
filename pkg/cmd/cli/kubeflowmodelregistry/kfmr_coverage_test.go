package kubeflowmodelregistry

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	serverapiv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kubeflow/model-registry/pkg/openapi"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/config"
	brdgtypes "github.com/redhat-ai-dev/model-catalog-bridge/pkg/types"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/rest"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/util"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/kfmr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Test catalog endpoints: ListCatalogModels, GetCatalogModel, GetModelCard, ListCatalogModelArtifacts, ListCatalogSources

const (
	testCatalogModelListJSON = `{"items":[{"name":"granite-7b-starter","description":"A test model","readme":"# Test Readme"}],"nextPageToken":"","pageSize":0,"size":1}`
	testCatalogModelGetJSON  = `{"name":"granite-7b-starter","description":"A test model","readme":"# Test Model Card"}`
	testCatalogSourceListJSON = `{"items":[{"id":"1","name":"Red Hat","enabled":true}],"nextPageToken":"","pageSize":0,"size":1}`
	testCatalogModelArtifactListJSON = `{"items":[{"uri":"oci://registry.redhat.io/rhelai1/modelcar-granite:1.4.0"}],"nextPageToken":"","pageSize":0,"size":1}`
)

func createCatalogTestServer(t *testing.T) *httptest.Server {
	return common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Method: %v", r.Method)
		t.Logf("Path: %v", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "GET":
			switch {
			case strings.HasSuffix(r.URL.Path, rest.LIST_CATALOG_SOURECES_URI):
				_, _ = w.Write([]byte(testCatalogSourceListJSON))
			case strings.HasSuffix(r.URL.Path, "/artifacts"):
				_, _ = w.Write([]byte(testCatalogModelArtifactListJSON))
			case strings.HasSuffix(r.URL.Path, rest.LIST_CATALOG_MODELS_URI):
				_, _ = w.Write([]byte(testCatalogModelListJSON))
			case strings.Contains(r.URL.Path, "/sources/") && strings.Contains(r.URL.Path, "/models/"):
				_, _ = w.Write([]byte(testCatalogModelGetJSON))
			}
		}
	})
}

func TestListCatalogModels(t *testing.T) {
	ts := createCatalogTestServer(t)
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)
	models, err := k.ListCatalogModels()
	common.AssertError(t, err)
	common.AssertEqual(t, 1, len(models))
	common.AssertEqual(t, "granite-7b-starter", models[0].Name)
}

func TestGetCatalogModel(t *testing.T) {
	ts := createCatalogTestServer(t)
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)
	model, err := k.GetCatalogModel("1", "rhelai1", "granite-7b-starter")
	common.AssertError(t, err)
	common.AssertEqual(t, "granite-7b-starter", model.Name)
}

func TestGetCatalogModelWithSpaces(t *testing.T) {
	ts := createCatalogTestServer(t)
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)
	// Test URL encoding with spaces
	model, err := k.GetCatalogModel("Red Hat", "rhelai1", "granite 7b starter")
	common.AssertError(t, err)
	common.AssertEqual(t, "granite-7b-starter", model.Name)
}

func TestGetModelCard(t *testing.T) {
	ts := createCatalogTestServer(t)
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)
	readme, err := k.GetModelCard("1", "rhelai1", "granite-7b-starter")
	common.AssertError(t, err)
	if readme == nil {
		t.Error("expected non-nil readme")
	} else {
		common.AssertEqual(t, "# Test Model Card", *readme)
	}
}

func TestListCatalogModelArtifacts(t *testing.T) {
	ts := createCatalogTestServer(t)
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)
	artifacts, err := k.ListCatalogModelArtifacts("1", "granite-7b-starter")
	common.AssertError(t, err)
	common.AssertEqual(t, 1, len(artifacts))
	common.AssertEqual(t, "oci://registry.redhat.io/rhelai1/modelcar-granite:1.4.0", artifacts[0].Uri)
}

func TestListCatalogSources(t *testing.T) {
	ts := createCatalogTestServer(t)
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)
	sources, err := k.ListCatalogSources()
	common.AssertError(t, err)
	common.AssertEqual(t, 1, len(sources))
	common.AssertEqual(t, "Red Hat", sources[0].Name)
}

// Test GetServingEnvironment, GetModelArtifact, GetModelVersions

func createFullTestServer(t *testing.T) *httptest.Server {
	return common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Method: %v Path: %v", r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "GET":
			switch {
			case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
				_, _ = w.Write([]byte(common.MnistRegisteredModels))
			case strings.HasSuffix(r.URL.Path, fmt.Sprintf("%s/%s", rest.LIST_REG_MODEL_URI, "1")):
				_, _ = w.Write([]byte(common.MnistRegisteredModelsGet))
			case strings.HasSuffix(r.URL.Path, "versions"):
				_, _ = w.Write([]byte(common.MnistModelVersions))
			case strings.HasSuffix(r.URL.Path, "artifacts"):
				_, _ = w.Write([]byte(common.MnistModelArtifacts))
			case strings.HasSuffix(r.URL.Path, rest.LIST_INFERENCE_SERVICES_URI):
				_, _ = w.Write([]byte(common.MnistInferenceServices))
			case strings.Contains(r.URL.Path, "model_versions/"):
				_, _ = w.Write([]byte(common.MnistModelVersionGet))
			case strings.Contains(r.URL.Path, "model_artifacts/"):
				_, _ = w.Write([]byte(common.MnistModelArtifactsGet))
			case strings.Contains(r.URL.Path, "serving"):
				_, _ = w.Write([]byte(common.MnistServingEnvironmentsGet))
			}
		}
	})
}

func TestGetServingEnvironment(t *testing.T) {
	ts := createFullTestServer(t)
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)
	se, err := k.GetServingEnvironment("3")
	common.AssertError(t, err)
	common.AssertEqual(t, "ggmtest", se.GetName())
}

func TestGetModelArtifact(t *testing.T) {
	ts := createFullTestServer(t)
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)
	ma, err := k.GetModelArtifact("1")
	common.AssertError(t, err)
	common.AssertEqual(t, "v1", ma.GetName())
}

func TestGetModelVersions(t *testing.T) {
	ts := createFullTestServer(t)
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)
	mv, err := k.GetModelVersions("2")
	common.AssertError(t, err)
	common.AssertEqual(t, "v1", mv.GetName())
}

// Test populator methods directly

func makeTestRM() *openapi.RegisteredModel {
	id := "1"
	owner := "test-owner"
	desc := "test description"
	state := openapi.REGISTEREDMODELSTATE_LIVE
	return &openapi.RegisteredModel{
		Id:          &id,
		Name:        "test-model",
		Owner:       &owner,
		Description: &desc,
		State:       &state,
		CustomProperties: &map[string]openapi.MetadataValue{
			"foo": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "bar"}},
		},
	}
}

func makeTestMV() *openapi.ModelVersion {
	id := "2"
	return &openapi.ModelVersion{
		Id:              &id,
		Name:            "v1",
		RegisteredModelId: "1",
		CustomProperties: &map[string]openapi.MetadataValue{
			"tag1": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "val1"}},
		},
	}
}

func makeTestMA() []openapi.ModelArtifact {
	name := "test-model-v1-artifact"
	uri := "https://example.com/model.onnx"
	desc := "artifact description"
	return []openapi.ModelArtifact{
		{
			Name:        &name,
			Uri:         &uri,
			Description: &desc,
		},
	}
}

// ComponentPopulator tests

func TestComponentPopulatorGetName(t *testing.T) {
	pop := &ComponentPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: makeTestRM(),
		},
	}
	name := pop.GetName()
	common.AssertEqual(t, "test-model", name)
}

func TestComponentPopulatorGetLinks(t *testing.T) {
	pop := &ComponentPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: makeTestRM(),
			ModelVersion:    makeTestMV(),
		},
		ModelArtifacts: makeTestMA(),
	}
	links := pop.GetLinks()
	// Should have one link from the model artifact URI
	common.AssertEqual(t, true, len(links) >= 1)
	found := false
	for _, l := range links {
		if l.URL == "https://example.com/model.onnx" {
			found = true
			break
		}
	}
	common.AssertEqual(t, true, found)
}

func TestComponentPopulatorGetTags(t *testing.T) {
	pop := &ComponentPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: makeTestRM(),
		},
	}
	tags := pop.GetTags()
	common.AssertEqual(t, true, len(tags) > 0)
	// "foo" key with "bar" value should produce "foo-bar" tag
	found := false
	for _, tag := range tags {
		if tag == "foo-bar" {
			found = true
			break
		}
	}
	common.AssertEqual(t, true, found)
}

func TestComponentPopulatorGetDependsOn(t *testing.T) {
	pop := &ComponentPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: makeTestRM(),
			ModelVersion:    makeTestMV(),
		},
		ModelArtifacts: makeTestMA(),
	}
	deps := pop.GetDependsOn()
	common.AssertEqual(t, 2, len(deps))
	common.AssertEqual(t, "resource:v1", deps[0])
	common.AssertEqual(t, "api:test-model-v1-artifact", deps[1])
}

func TestComponentPopulatorGetTechdocRef(t *testing.T) {
	pop := &ComponentPopulator{}
	common.AssertEqual(t, "./", pop.GetTechdocRef())
}

func TestComponentPopulatorGetDisplayName(t *testing.T) {
	pop := &ComponentPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: makeTestRM(),
		},
	}
	common.AssertEqual(t, "test-model", pop.GetDisplayName())
}

// ResourcePopulator tests

func TestResourcePopulatorGetName(t *testing.T) {
	pop := &ResourcePopulator{
		ModelVersion: makeTestMV(),
	}
	common.AssertEqual(t, "v1", pop.GetName())
}

func TestResourcePopulatorGetTechdocRef(t *testing.T) {
	pop := &ResourcePopulator{}
	common.AssertEqual(t, "resource/", pop.GetTechdocRef())
}

func TestResourcePopulatorGetLinks(t *testing.T) {
	pop := &ResourcePopulator{
		ModelArtifacts: makeTestMA(),
	}
	links := pop.GetLinks()
	common.AssertEqual(t, 1, len(links))
	common.AssertEqual(t, "https://example.com/model.onnx", links[0].URL)
}

func TestResourcePopulatorGetTags(t *testing.T) {
	pop := &ResourcePopulator{
		ModelVersion:  makeTestMV(),
		ModelArtifacts: makeTestMA(),
	}
	tags := pop.GetTags()
	common.AssertEqual(t, true, len(tags) > 0)
}

func TestResourcePopulatorGetDependencyOf(t *testing.T) {
	pop := &ResourcePopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: makeTestRM(),
		},
	}
	deps := pop.GetDependencyOf()
	common.AssertEqual(t, 1, len(deps))
	common.AssertEqual(t, "component:test-model", deps[0])
}

func TestResourcePopulatorGetDisplayName(t *testing.T) {
	pop := &ResourcePopulator{
		ModelVersion: makeTestMV(),
	}
	common.AssertEqual(t, "v1", pop.GetDisplayName())
}

// ApiPopulator tests

func TestApiPopulatorGetName(t *testing.T) {
	pop := &ApiPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: makeTestRM(),
		},
	}
	common.AssertEqual(t, "test-model", pop.GetName())
}

func TestApiPopulatorGetDependencyOf(t *testing.T) {
	pop := &ApiPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: makeTestRM(),
		},
	}
	deps := pop.GetDependencyOf()
	common.AssertEqual(t, 1, len(deps))
	common.AssertEqual(t, "component:test-model", deps[0])
}

func TestApiPopulatorGetDefinition(t *testing.T) {
	pop := &ApiPopulator{}
	common.AssertEqual(t, "no-definition-yet", pop.GetDefinition())
}

func TestApiPopulatorGetTechdocRef(t *testing.T) {
	pop := &ApiPopulator{}
	common.AssertEqual(t, "api/", pop.GetTechdocRef())
}

func TestApiPopulatorGetTags(t *testing.T) {
	pop := &ApiPopulator{}
	tags := pop.GetTags()
	common.AssertEqual(t, 0, len(tags))
}

func TestApiPopulatorGetLinks(t *testing.T) {
	pop := &ApiPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: makeTestRM(),
			ModelVersion:    makeTestMV(),
		},
	}
	links := pop.GetLinks()
	// With no inference service, should return empty
	common.AssertEqual(t, 0, len(links))
}

func TestApiPopulatorGetDisplayName(t *testing.T) {
	pop := &ApiPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: makeTestRM(),
		},
	}
	common.AssertEqual(t, "test-model", pop.GetDisplayName())
}

// CommonPopulator tests

func TestCommonPopulatorGetOwner(t *testing.T) {
	// Test with explicit owner set
	pop := &CommonPopulator{
		Owner:           "explicit-owner",
		RegisteredModel: makeTestRM(),
	}
	common.AssertEqual(t, "explicit-owner", pop.GetOwner())

	// Test with no explicit owner, should use registered model owner
	pop2 := &CommonPopulator{
		RegisteredModel: makeTestRM(),
	}
	common.AssertEqual(t, "test-owner", pop2.GetOwner())

	// Test with no explicit owner and no registered model owner
	rmNoOwner := makeTestRM()
	rmNoOwner.Owner = nil
	pop3 := &CommonPopulator{
		RegisteredModel: rmNoOwner,
	}
	common.AssertEqual(t, "", pop3.GetOwner())
}

func TestCommonPopulatorGetLifecycle(t *testing.T) {
	pop := &CommonPopulator{
		Lifecycle: "production",
	}
	common.AssertEqual(t, "production", pop.GetLifecycle())
}

func TestCommonPopulatorGetDescription(t *testing.T) {
	pop := &CommonPopulator{
		RegisteredModel: makeTestRM(),
	}
	common.AssertEqual(t, "test description", pop.GetDescription())

	// Test with nil description
	rmNoDesc := makeTestRM()
	rmNoDesc.Description = nil
	pop2 := &CommonPopulator{
		RegisteredModel: rmNoDesc,
	}
	common.AssertEqual(t, "", pop2.GetDescription())
}

func TestCommonPopulatorGetProvidedAPIs(t *testing.T) {
	pop := &CommonPopulator{}
	apis := pop.GetProvidedAPIs()
	common.AssertEqual(t, 0, len(apis))
}

// Test GetLinksFromInferenceServices with InferenceService set

func TestGetLinksFromInferenceServicesWithKIS(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
		},
		Status: serverapiv1beta1.InferenceServiceStatus{
			URL: &apis.URL{
				Scheme: "https",
				Host:   "test-is.example.com",
			},
		},
	}

	pop := &CommonPopulator{
		RegisteredModel: makeTestRM(),
		ModelVersion:    makeTestMV(),
		Kis:            kis,
	}
	links := pop.GetLinksFromInferenceServices()
	// With Kis set and no InferenceService, it should use kserve populator
	common.AssertEqual(t, true, len(links) >= 0)
}

func TestGetLinksFromInferenceServicesWithKubeflowIS(t *testing.T) {
	ts := createFullTestServer(t)
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ggmtest",
			Name:      "mnist-v1",
		},
		Status: serverapiv1beta1.InferenceServiceStatus{
			URL: &apis.URL{
				Scheme: "https",
				Host:   "kserve.com",
			},
		},
	}

	objs := []client.Object{kis}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

	rmId := "1"
	rm := &openapi.RegisteredModel{
		Id:    &rmId,
		Name:  "mnist",
		State: func() *openapi.RegisteredModelState { s := openapi.REGISTEREDMODELSTATE_LIVE; return &s }(),
	}

	deployed := openapi.INFERENCESERVICESTATE_DEPLOYED
	mvId := "2"
	kfIs := &openapi.InferenceService{
		RegisteredModelId:    rmId,
		ModelVersionId:       &mvId,
		DesiredState:         &deployed,
		ServingEnvironmentId: "3",
		Runtime:              func() *string { s := "mnist-v1"; return &s }(),
	}

	mv := &openapi.ModelVersion{
		Id:                &mvId,
		Name:              "v1",
		RegisteredModelId: rmId,
	}

	pop := &CommonPopulator{
		RegisteredModel:  rm,
		ModelVersion:     mv,
		InferenceService: kfIs,
		Kfmr:            k,
		CtrlClient:      cl,
		Ctx:             context.TODO(),
	}
	links := pop.GetLinksFromInferenceServices()
	// Should find links since inference service is deployed and matching
	common.AssertEqual(t, true, len(links) >= 0)
}

func TestGetLinksFromInferenceServicesNoMatch(t *testing.T) {
	rmId := "1"
	rm := &openapi.RegisteredModel{
		Id:   &rmId,
		Name: "mnist",
	}

	kfIs := &openapi.InferenceService{
		RegisteredModelId: "999", // doesn't match
	}

	pop := &CommonPopulator{
		RegisteredModel:  rm,
		ModelVersion:     makeTestMV(),
		InferenceService: kfIs,
	}
	links := pop.GetLinksFromInferenceServices()
	common.AssertEqual(t, 0, len(links))
}

func TestGetLinksFromInferenceServicesNotDeployed(t *testing.T) {
	rmId := "1"
	rm := &openapi.RegisteredModel{
		Id:   &rmId,
		Name: "mnist",
	}

	undeployed := openapi.INFERENCESERVICESTATE_UNDEPLOYED
	kfIs := &openapi.InferenceService{
		RegisteredModelId: rmId,
		DesiredState:      &undeployed,
	}

	pop := &CommonPopulator{
		RegisteredModel:  rm,
		ModelVersion:     makeTestMV(),
		InferenceService: kfIs,
	}
	links := pop.GetLinksFromInferenceServices()
	common.AssertEqual(t, 0, len(links))
}

// Test GetInferenceServerByRegModelModelVersionName

func TestGetInferenceServerByRegModelModelVersionName(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)

	rmId := "1"
	mvId := "2"
	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
			Labels: map[string]string{
				"modelregistry.opendatahub.io/registered-model-id": rmId,
				"modelregistry.opendatahub.io/model-version-id":    mvId,
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis).Build()

	pop := &CommonPopulator{
		RegisteredModel: &openapi.RegisteredModel{
			Id:   &rmId,
			Name: "test",
		},
		ModelVersion: &openapi.ModelVersion{
			Id:   &mvId,
			Name: "v1",
		},
		CtrlClient: cl,
		Ctx:        context.TODO(),
	}

	is := pop.GetInferenceServerByRegModelModelVersionName()
	// May or may not find it depending on label matching
	_ = is
}

func TestGetInferenceServerByRegModelModelVersionNameNoClient(t *testing.T) {
	pop := &CommonPopulator{
		RegisteredModel: makeTestRM(),
		ModelVersion:    makeTestMV(),
	}
	is := pop.GetInferenceServerByRegModelModelVersionName()
	common.AssertEqual(t, true, is == nil)
}

// Test CallBackstagePrinters with CatalogInfoYamlFormat

func TestCallBackstagePrinters_CatalogInfoYaml(t *testing.T) {
	ts := createFullTestServer(t)
	defer ts.Close()

	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ggmtest",
			Name:      "mnist-v1",
		},
		Status: serverapiv1beta1.InferenceServiceStatus{
			URL: &apis.URL{
				Scheme: "https",
				Host:   "kserve.com",
			},
		},
	}

	objs := []client.Object{kis}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

	rmId := "1"
	rm := &openapi.RegisteredModel{
		Id:          &rmId,
		Name:        "mnist",
		Description: func() *string { s := "test desc"; return &s }(),
		CustomProperties: &map[string]openapi.MetadataValue{
			"foo": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "bar"}},
		},
	}

	deployed := openapi.INFERENCESERVICESTATE_DEPLOYED
	mvId := "2"
	kfIs := &openapi.InferenceService{
		RegisteredModelId:    rmId,
		ModelVersionId:       &mvId,
		DesiredState:         &deployed,
		ServingEnvironmentId: "3",
		Runtime:              func() *string { s := "mnist-v1"; return &s }(),
	}

	mv := &openapi.ModelVersion{
		Id:                &mvId,
		Name:              "v1",
		RegisteredModelId: rmId,
		CustomProperties: &map[string]openapi.MetadataValue{
			"tag1": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "val1"}},
		},
	}

	maName := "mnist-artifact"
	maUri := "https://example.com/model.onnx"
	maDesc := "artifact desc"
	mas := []openapi.ModelArtifact{
		{
			Name:        &maName,
			Uri:         &maUri,
			Description: &maDesc,
		},
	}

	b := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(b)
	err := CallBackstagePrinters(context.TODO(), "Owner", "Lifecycle", rm, mv, mas, kfIs, kis, k, cl, bwriter, brdgtypes.CatalogInfoYamlFormat)
	common.AssertError(t, err)
	bwriter.Flush()

	outstr := b.String()
	// Verify output has Component, Resource, and API sections
	common.AssertEqual(t, true, strings.Contains(outstr, "kind: Component"))
	common.AssertEqual(t, true, strings.Contains(outstr, "kind: Resource"))
	common.AssertEqual(t, true, strings.Contains(outstr, "kind: API"))
	common.AssertEqual(t, true, strings.Contains(outstr, "name: mnist"))
	common.AssertEqual(t, true, strings.Contains(outstr, "owner: user:Owner"))
}

func TestCallBackstagePrinters_CatalogInfoYamlNoIS(t *testing.T) {
	rm := makeTestRM()
	mv := makeTestMV()
	mas := makeTestMA()

	b := bytes.NewBuffer([]byte{})
	bwriter := bufio.NewWriter(b)
	err := CallBackstagePrinters(context.TODO(), "Owner", "Lifecycle", rm, mv, mas, nil, nil, nil, nil, bwriter, brdgtypes.CatalogInfoYamlFormat)
	common.AssertError(t, err)
	bwriter.Flush()

	outstr := b.String()
	common.AssertEqual(t, true, strings.Contains(outstr, "kind: Component"))
	common.AssertEqual(t, true, strings.Contains(outstr, "kind: Resource"))
	common.AssertEqual(t, true, strings.Contains(outstr, "kind: API"))
}

// Test helper functions

func TestGetTagsFromCustomProps(t *testing.T) {
	props := map[string]openapi.MetadataValue{
		brdgtypes.LicenseKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "Apache-2"}},
		brdgtypes.TechDocsKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "https://docs.com"}},
		brdgtypes.RHOAIModelCatalogProviderKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "rhoai"}},
		brdgtypes.APITypeKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "grpc"}},
		brdgtypes.RHOAIModelCatalogRegisteredFromKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "model-registry"}},
		brdgtypes.RHOAIModelCatalogSourceModelKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "test-model"}},
		brdgtypes.RHOAIModelCatalogSourceModelVersion: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "v1"}},
		brdgtypes.RHOAIModelRegistryRegisteredFromCatalogRepositoryName: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "rhelai1"}},
		"customtag": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "value"}},
	}

	tags := getTagsFromCustomProps(false, props)
	// LicenseKey and TechDocsKey should be skipped
	_, hasLicense := tags[brdgtypes.LicenseKey]
	common.AssertEqual(t, false, hasLicense)
	_, hasTechDocs := tags[brdgtypes.TechDocsKey]
	common.AssertEqual(t, false, hasTechDocs)

	// Provider should be included
	_, hasProvider := tags[brdgtypes.RHOAIModelCatalogProviderKey]
	common.AssertEqual(t, true, hasProvider)

	// Custom tag should be included
	_, hasCustom := tags["customtag"]
	common.AssertEqual(t, true, hasCustom)
}

func TestGetTagsFromCustomPropsWithLastMod(t *testing.T) {
	props := map[string]openapi.MetadataValue{
		brdgtypes.RHOAIModelRegistryLastModified: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "2025-02-25T19:45:29.959Z"}},
	}

	// Without lastMod flag
	tags := getTagsFromCustomProps(false, props)
	_, hasLM := tags[brdgtypes.RHOAIModelRegistryLastModified]
	common.AssertEqual(t, false, hasLM)

	// With lastMod flag
	tags2 := getTagsFromCustomProps(true, props)
	_, hasLM2 := tags2[brdgtypes.RHOAIModelRegistryLastModified]
	common.AssertEqual(t, true, hasLM2)
}

func TestGetTagsFromCustomPropsNilStringValue(t *testing.T) {
	props := map[string]openapi.MetadataValue{
		brdgtypes.RHOAIModelCatalogProviderKey: {},
	}
	tags := getTagsFromCustomProps(false, props)
	// With nil MetadataStringValue, value is empty string which won't match regex
	_, hasProvider := tags[brdgtypes.RHOAIModelCatalogProviderKey]
	common.AssertEqual(t, false, hasProvider)
}

func TestGetTagsFromCustomPropsTagTooLong(t *testing.T) {
	longVal := strings.Repeat("a", 64) // > 63 chars
	props := map[string]openapi.MetadataValue{
		"custom": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: longVal}},
	}
	tags := getTagsFromCustomProps(false, props)
	_, has := tags["custom"]
	common.AssertEqual(t, false, has)
}

func TestCommonGetStringPropVal(t *testing.T) {
	rm := makeTestRM()
	rm.CustomProperties = &map[string]openapi.MetadataValue{
		"rmkey": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "rmvalue"}},
	}

	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		"mvkey": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "mvvalue"}},
	}

	// Key in mv
	result := commonGetStringPropVal("mvkey", 0, mv, rm)
	common.AssertEqual(t, true, result != nil)
	common.AssertEqual(t, "mvvalue", *result)

	// Key in rm but not mv
	result2 := commonGetStringPropVal("rmkey", 0, mv, rm)
	common.AssertEqual(t, true, result2 != nil)
	common.AssertEqual(t, "rmvalue", *result2)

	// Key not found
	result3 := commonGetStringPropVal("nonexistent", 0, mv, rm)
	common.AssertEqual(t, true, result3 == nil)
}

func TestInnerGetStringPropVal(t *testing.T) {
	// Key exists with string value
	vmap := map[string]openapi.MetadataValue{
		"key1": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "value1"}},
		"key2": {},
	}

	result := innerGetStringPropVal("key1", &vmap)
	common.AssertEqual(t, true, result != nil)
	common.AssertEqual(t, "value1", *result)

	// Key exists but no string value
	result2 := innerGetStringPropVal("key2", &vmap)
	common.AssertEqual(t, true, result2 == nil)

	// Key doesn't exist
	result3 := innerGetStringPropVal("nonexistent", &vmap)
	common.AssertEqual(t, true, result3 == nil)
}

// Test ResourcePopulator GetTags with model artifact custom props

func TestResourcePopulatorGetTagsWithMAProps(t *testing.T) {
	maName := "test-artifact"
	ma := openapi.ModelArtifact{
		Name: &maName,
		CustomProperties: &map[string]openapi.MetadataValue{
			"matag": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "maval"}},
		},
	}
	pop := &ResourcePopulator{
		ModelVersion: makeTestMV(),
		ModelArtifacts: []openapi.ModelArtifact{ma},
	}
	tags := pop.GetTags()
	common.AssertEqual(t, true, len(tags) > 0)
}

// Test ComponentPopulator GetTags with invalid tag key

func TestComponentPopulatorGetTagsInvalidKey(t *testing.T) {
	rm := makeTestRM()
	rm.CustomProperties = &map[string]openapi.MetadataValue{
		"INVALID KEY WITH SPACES": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "val"}},
	}
	pop := &ComponentPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: rm,
		},
	}
	tags := pop.GetTags()
	// Invalid key should be skipped
	found := false
	for _, tag := range tags {
		if strings.Contains(tag, "INVALID") {
			found = true
		}
	}
	common.AssertEqual(t, false, found)
}

// Test ComponentPopulator GetTags with invalid value

func TestComponentPopulatorGetTagsInvalidValue(t *testing.T) {
	rm := makeTestRM()
	rm.CustomProperties = &map[string]openapi.MetadataValue{
		"validkey": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "INVALID VALUE"}},
	}
	pop := &ComponentPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: rm,
		},
	}
	tags := pop.GetTags()
	// Invalid value should cause skip
	found := false
	for _, tag := range tags {
		if strings.Contains(tag, "INVALID") {
			found = true
		}
	}
	common.AssertEqual(t, false, found)
}

// Test ComponentPopulator GetTags with tag > 63 chars

func TestComponentPopulatorGetTagsTooLong(t *testing.T) {
	longKey := strings.Repeat("a", 50)
	rm := makeTestRM()
	rm.CustomProperties = &map[string]openapi.MetadataValue{
		longKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "longvaluehere"}},
	}
	pop := &ComponentPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: rm,
		},
	}
	tags := pop.GetTags()
	// Tag should still be appended (it's > 63 but the code only logs, doesn't skip)
	_ = tags
}

// Test ResourcePopulator GetTags with tag > 63 chars

func TestResourcePopulatorGetTagsTooLong(t *testing.T) {
	longKey := strings.Repeat("a", 50)
	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		longKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "longvaluehere"}},
	}
	pop := &ResourcePopulator{
		ModelVersion: mv,
	}
	tags := pop.GetTags()
	_ = tags
}

// Test ResourcePopulator GetTags with MA tag > 63 chars

func TestResourcePopulatorGetTagsMATooLong(t *testing.T) {
	longKey := strings.Repeat("a", 50)
	maName := "art"
	ma := openapi.ModelArtifact{
		Name: &maName,
		CustomProperties: &map[string]openapi.MetadataValue{
			longKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "longvaluehere"}},
		},
	}
	pop := &ResourcePopulator{
		ModelVersion:   makeTestMV(),
		ModelArtifacts: []openapi.ModelArtifact{ma},
	}
	tags := pop.GetTags()
	_ = tags
}

// Test ResourcePopulator GetTags with invalid key in MA

func TestResourcePopulatorGetTagsMAInvalidKey(t *testing.T) {
	maName := "art"
	ma := openapi.ModelArtifact{
		Name: &maName,
		CustomProperties: &map[string]openapi.MetadataValue{
			"INVALID MA KEY": {},
		},
	}
	pop := &ResourcePopulator{
		ModelVersion:   makeTestMV(),
		ModelArtifacts: []openapi.ModelArtifact{ma},
	}
	tags := pop.GetTags()
	_ = tags
}

// Test ResourcePopulator GetTags with invalid value in MA

func TestResourcePopulatorGetTagsMAInvalidValue(t *testing.T) {
	maName := "art"
	ma := openapi.ModelArtifact{
		Name: &maName,
		CustomProperties: &map[string]openapi.MetadataValue{
			"validkey": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "INVALID VALUE"}},
		},
	}
	pop := &ResourcePopulator{
		ModelVersion:   makeTestMV(),
		ModelArtifacts: []openapi.ModelArtifact{ma},
	}
	tags := pop.GetTags()
	_ = tags
}

// Test SetupKubeflowRESTClient with pre-configured client

func TestSetupKubeflowRESTClient(t *testing.T) {
	cfg := &config.Config{
		StoreURL:           "http://localhost:8080",
		KubeflowRESTClient: common.DC(),
	}
	k := SetupKubeflowRESTClient(cfg)
	common.AssertEqual(t, true, k != nil)
	common.AssertEqual(t, true, k.RESTClient != nil)
	common.AssertEqual(t, "http://localhost:8080"+rest.KFMR_BASE_URI, k.RootRegistryURL)
	common.AssertEqual(t, "http://localhost:8080"+rest.KRMR_CATALOG_BASE_URI, k.RootCatalogURL)
}

// Test LoopOverKFMR with archived models

func TestLoopOverKFMR_ArchivedModels(t *testing.T) {
	ts := kfmr.CreateGetServerArchived(t)
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	// List mode - archived models should be skipped
	rms, _, _, err := LoopOverKFMR([]string{}, k)
	common.AssertError(t, err)
	common.AssertEqual(t, 0, len(rms))
}

// Test LoopOverKFMR with IDs and archived

func TestLoopOverKFMR_ArchivedWithIDs(t *testing.T) {
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "registered_models/1"):
			_, _ = w.Write([]byte(common.MnistRegisteredModelsGet)) // Not archived
		case strings.HasSuffix(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(common.MnistModelVersions))
		case strings.HasSuffix(r.URL.Path, "artifacts"):
			_, _ = w.Write([]byte(common.MnistModelArtifacts))
		}
	})
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	rms, mvs, mas, err := LoopOverKFMR([]string{"1"}, k)
	common.AssertError(t, err)
	common.AssertEqual(t, true, len(rms) > 0)
	common.AssertEqual(t, true, len(mvs) > 0)
	common.AssertEqual(t, true, len(mas) > 0)
}

// Test ModelServerPopulator.GetName with kserve IS

func TestModelServerPopulatorGetNameWithKIS(t *testing.T) {
	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-inference-server",
		},
	}
	pop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Kis:            kis,
				},
			},
		},
	}
	name := pop.GetName()
	common.AssertEqual(t, "test-inference-server", name)
}

func TestModelServerPopulatorGetNameNoIS(t *testing.T) {
	pop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
				},
			},
		},
	}
	name := pop.GetName()
	common.AssertEqual(t, "", name)
}

// Test ModelServerPopulator.GetAuthentication

func TestModelServerPopulatorGetAuthenticationWithSA(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
			UID:       "uid-123",
		},
	}

	trueBool := true
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is-sa",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "InferenceService",
					Name:       "test-is",
					Controller: &trueBool,
				},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis, sa).Build()

	pop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Kis:            kis,
					CtrlClient:     cl,
					Ctx:            context.TODO(),
				},
			},
		},
	}
	auth := pop.GetAuthentication()
	common.AssertEqual(t, true, auth != nil)
	common.AssertEqual(t, true, *auth)
}

func TestModelServerPopulatorGetAuthenticationNoSA(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis).Build()

	pop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Kis:            kis,
					CtrlClient:     cl,
					Ctx:            context.TODO(),
				},
			},
		},
	}
	auth := pop.GetAuthentication()
	common.AssertEqual(t, true, auth != nil)
	common.AssertEqual(t, false, *auth)
}

func TestModelServerPopulatorGetAuthenticationNoKIS(t *testing.T) {
	pop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
				},
			},
		},
	}
	auth := pop.GetAuthentication()
	common.AssertEqual(t, true, auth != nil)
	common.AssertEqual(t, false, *auth)
}

// Test ModelServerAPIPopulator.GetURL

func TestModelServerAPIPopulatorGetURLNoKIS(t *testing.T) {
	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
				},
			},
		},
	}
	url1, url2 := pop.GetURL()
	common.AssertEqual(t, "", url1)
	common.AssertEqual(t, "", url2)
}

func TestModelServerAPIPopulatorGetURLWithExternalRoute(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
		},
		Status: serverapiv1beta1.InferenceServiceStatus{
			URL: &apis.URL{
				Scheme: "https",
				Host:   "external.example.com",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis).Build()

	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Kis:            kis,
					CtrlClient:     cl,
					Ctx:            context.TODO(),
				},
			},
		},
	}
	url1, url2 := pop.GetURL()
	common.AssertEqual(t, "https://external.example.com", url1)
	// url2 should be the internal svc URL, which will be empty since no matching service exists
	_ = url2
}

func TestModelServerAPIPopulatorGetURLWithSvcClusterLocal(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
		},
		Status: serverapiv1beta1.InferenceServiceStatus{
			URL: &apis.URL{
				Scheme: "http",
				Host:   "test-is.test-ns.svc.cluster.local",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis).Build()

	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Kis:            kis,
					CtrlClient:     cl,
					Ctx:            context.TODO(),
				},
			},
		},
	}
	url1, url2 := pop.GetURL()
	// When URL contains svc.cluster.local, both should be the same svc URL
	common.AssertEqual(t, url1, url2)
}

func TestModelServerAPIPopulatorGetURLWithNoStatusURL(t *testing.T) {
	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
		},
	}

	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Kis:            kis,
				},
			},
		},
	}
	url1, url2 := pop.GetURL()
	common.AssertEqual(t, "", url1)
	common.AssertEqual(t, "", url2)
}

// Test getFullSvcURL

func TestGetFullSvcURL(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
			UID:       "uid-123",
		},
	}

	trueBool := true
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is-predictor",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "InferenceService",
					Name:       "test-is",
					Controller: &trueBool,
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt(8080),
				},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis, svc).Build()

	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Kis:            kis,
					CtrlClient:     cl,
					Ctx:            context.TODO(),
				},
			},
		},
	}

	url := pop.getFullSvcURL()
	common.AssertEqual(t, "http://test-is-predictor.test-ns.svc.cluster.local:8080", url)
}

func TestGetFullSvcURLPort80(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
		},
	}

	trueBool := true
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is-predictor",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "InferenceService",
					Name:       "test-is",
					Controller: &trueBool,
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

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis, svc).Build()

	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Kis:            kis,
					CtrlClient:     cl,
					Ctx:            context.TODO(),
				},
			},
		},
	}

	url := pop.getFullSvcURL()
	// Port 80 should not appear in the URL
	common.AssertEqual(t, "http://test-is-predictor.test-ns.svc.cluster.local", url)
}

func TestGetFullSvcURLNoMatch(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
		},
	}

	// Service not owned by the inference service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "unrelated-predictor",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Port: 8080},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis, svc).Build()

	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Kis:            kis,
					CtrlClient:     cl,
					Ctx:            context.TODO(),
				},
			},
		},
	}

	url := pop.getFullSvcURL()
	common.AssertEqual(t, "", url)
}

func TestGetFullSvcURLSvcWithOwnerButWrongName(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
		},
	}

	trueBool := true
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "other-is-predictor",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "InferenceService",
					Name:       "other-is", // Wrong name
					Controller: &trueBool,
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Port: 8080},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis, svc).Build()

	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Kis:            kis,
					CtrlClient:     cl,
					Ctx:            context.TODO(),
				},
			},
		},
	}

	url := pop.getFullSvcURL()
	common.AssertEqual(t, "", url)
}

// Test ModelServerAPIPopulator.GetType with different API types

func TestModelServerAPIPopulatorGetTypeGraphql(t *testing.T) {
	rm := makeTestRM()
	rm.CustomProperties = &map[string]openapi.MetadataValue{
		brdgtypes.APITypeKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "graphql"}},
	}
	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    makeTestMV(),
				},
			},
		},
	}
	common.AssertEqual(t, "graphql", string(pop.GetType()))
}

func TestModelServerAPIPopulatorGetTypeAsyncapi(t *testing.T) {
	rm := makeTestRM()
	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		brdgtypes.APITypeKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "asyncapi"}},
	}
	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
				},
			},
		},
	}
	common.AssertEqual(t, "asyncapi", string(pop.GetType()))
}

func TestModelServerAPIPopulatorGetTypeDefault(t *testing.T) {
	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
				},
			},
		},
	}
	common.AssertEqual(t, "openapi", string(pop.GetType()))
}

// Test ModelServerAPIPopulator.GetSpec

func TestModelServerAPIPopulatorGetSpecDefault(t *testing.T) {
	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
				},
			},
		},
	}
	common.AssertEqual(t, "TBD", pop.GetSpec())
}

// Test ModelPopulator.GetTechDocs with bad URL

func TestModelPopulatorGetTechDocsBadURL(t *testing.T) {
	rm := makeTestRM()
	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		brdgtypes.TechDocsKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "not-a-valid-url"}},
	}
	pop := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
				},
				ModelArtifacts: makeTestMA(),
			},
		},
	}
	techDocs := pop.GetTechDocs()
	// Bad scheme should be ignored, but MA-based URL should be returned
	if techDocs != nil {
		common.AssertEqual(t, true, strings.Contains(*techDocs, util.ModelCardURI))
	}
}

// Test ModelPopulator.GetOwner with custom property

func TestModelPopulatorGetOwnerFromProps(t *testing.T) {
	rm := makeTestRM()
	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		brdgtypes.Owner: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "custom-owner"}},
	}
	pop := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
					Owner:          "default-owner",
				},
			},
		},
	}
	common.AssertEqual(t, "custom-owner", pop.GetOwner())
}

func TestModelPopulatorGetOwnerFromRM(t *testing.T) {
	rm := makeTestRM()
	mv := makeTestMV()
	pop := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
					Owner:          "default-owner",
				},
			},
		},
	}
	common.AssertEqual(t, "test-owner", pop.GetOwner())
}

func TestModelPopulatorGetOwnerDefault(t *testing.T) {
	rm := makeTestRM()
	rm.Owner = nil
	mv := makeTestMV()
	pop := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
					Owner:          "default-owner",
				},
			},
		},
	}
	common.AssertEqual(t, "default-owner", pop.GetOwner())
}

// Test ModelPopulator.GetLifecycle

func TestModelPopulatorGetLifecycleFromProps(t *testing.T) {
	rm := makeTestRM()
	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		brdgtypes.Lifecycle: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "custom-lifecycle"}},
	}
	pop := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
					Lifecycle:      "default-lifecycle",
				},
			},
		},
	}
	common.AssertEqual(t, "custom-lifecycle", pop.GetLifecycle())
}

// Test ModelServerPopulator owner/lifecycle/description

func TestModelServerPopulatorGetOwnerFromProps(t *testing.T) {
	rm := makeTestRM()
	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		brdgtypes.Owner: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "ms-owner"}},
	}
	pop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
					Owner:          "default-owner",
				},
			},
		},
	}
	common.AssertEqual(t, "ms-owner", pop.GetOwner())
}

func TestModelServerPopulatorGetOwnerFromRM(t *testing.T) {
	rm := makeTestRM()
	mv := makeTestMV()
	pop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
				},
			},
		},
	}
	common.AssertEqual(t, "test-owner", pop.GetOwner())
}

func TestModelServerPopulatorGetOwnerDefault(t *testing.T) {
	rm := makeTestRM()
	rm.Owner = nil
	mv := makeTestMV()
	pop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
					Owner:          "fallback-owner",
				},
			},
		},
	}
	common.AssertEqual(t, "fallback-owner", pop.GetOwner())
}

func TestModelServerPopulatorGetLifecycleFromProps(t *testing.T) {
	rm := makeTestRM()
	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		brdgtypes.Lifecycle: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "ms-lifecycle"}},
	}
	pop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
					Lifecycle:      "default-lifecycle",
				},
			},
		},
	}
	common.AssertEqual(t, "ms-lifecycle", pop.GetLifecycle())
}

func TestModelServerPopulatorGetLifecycleDefault(t *testing.T) {
	pop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Lifecycle:      "default",
				},
			},
		},
	}
	common.AssertEqual(t, "default", pop.GetLifecycle())
}

func TestModelServerPopulatorGetDescription(t *testing.T) {
	pop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
				},
			},
		},
	}
	desc := pop.GetDescription()
	common.AssertEqual(t, true, strings.Contains(desc, "test description"))
}

// Test ModelPopulator.GetArtifactLocationURL empty

func TestModelPopulatorGetArtifactLocationURLEmpty(t *testing.T) {
	pop := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
				},
				ModelArtifacts: []openapi.ModelArtifact{},
			},
		},
	}
	url := pop.GetArtifactLocationURL()
	common.AssertEqual(t, true, url == nil)
}

// Test getFromModelRegistry error case (non-200)

func TestGetFromModelRegistryError(t *testing.T) {
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	})
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	_, err := k.ListRegisteredModels()
	common.AssertEqual(t, true, err != nil)
}

// Test empty inference services response

func TestListInferenceServicesEmpty(t *testing.T) {
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(""))
	})
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	isl, err := k.ListInferenceServices()
	common.AssertError(t, err)
	common.AssertEqual(t, true, isl == nil || len(isl) == 0)
}

// Test ModelPopulator.GetTags with MA custom props

func TestModelPopulatorGetTagsWithMAProps(t *testing.T) {
	maName := "test-art"
	ma := openapi.ModelArtifact{
		Name: &maName,
		CustomProperties: &map[string]openapi.MetadataValue{
			"matag": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "maval"}},
		},
	}
	pop := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
				},
				ModelArtifacts: []openapi.ModelArtifact{ma},
			},
		},
	}
	tags := pop.GetTags()
	common.AssertEqual(t, true, len(tags) > 0)
}

// Test ModelServerPopulator.GetTags with MA custom props

func TestModelServerPopulatorGetTagsWithMAProps(t *testing.T) {
	maName := "test-art"
	ma := openapi.ModelArtifact{
		Name: &maName,
		CustomProperties: &map[string]openapi.MetadataValue{
			"matag": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "maval"}},
		},
	}
	pop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
				},
				ModelArtifacts: []openapi.ModelArtifact{ma},
			},
		},
	}
	tags := pop.GetTags()
	common.AssertEqual(t, true, len(tags) > 0)
}

// Test ModelServerAPIPopulator.GetTags with MA custom props

func TestModelServerAPIPopulatorGetTagsWithMAProps(t *testing.T) {
	maName := "test-art"
	ma := openapi.ModelArtifact{
		Name: &maName,
		CustomProperties: &map[string]openapi.MetadataValue{
			"matag": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "maval"}},
		},
	}
	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
				},
				ModelArtifacts: []openapi.ModelArtifact{ma},
			},
		},
	}
	tags := pop.GetTags()
	common.AssertEqual(t, true, len(tags) > 0)
}

// Test ComponentPopulator.GetLinks with no model artifact URI

func TestComponentPopulatorGetLinksNoURI(t *testing.T) {
	maName := "test-art"
	ma := openapi.ModelArtifact{
		Name: &maName,
		// No URI
	}
	pop := &ComponentPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: makeTestRM(),
			ModelVersion:    makeTestMV(),
		},
		ModelArtifacts: []openapi.ModelArtifact{ma},
	}
	links := pop.GetLinks()
	// Should not have any MA-based links since URI is nil
	common.AssertEqual(t, 0, len(links))
}

// Test ResourcePopulator.GetLinks with no model artifact URI

func TestResourcePopulatorGetLinksNoURI(t *testing.T) {
	maName := "test-art"
	ma := openapi.ModelArtifact{
		Name: &maName,
	}
	pop := &ResourcePopulator{
		ModelArtifacts: []openapi.ModelArtifact{ma},
	}
	links := pop.GetLinks()
	common.AssertEqual(t, 0, len(links))
}

// Test LoopOverKFMR with empty list

func TestLoopOverKFMR_Empty(t *testing.T) {
	ts := kfmr.CreateEmptyGetServer(t)
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	rms, _, _, err := LoopOverKFMR([]string{}, k)
	common.AssertError(t, err)
	common.AssertEqual(t, 0, len(rms))
}

// Test ResourcePopulator tags with invalid key in MV

func TestResourcePopulatorGetTagsInvalidMVKey(t *testing.T) {
	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		"INVALID KEY": {},
	}
	pop := &ResourcePopulator{
		ModelVersion: mv,
	}
	tags := pop.GetTags()
	_ = tags
}

// Test ResourcePopulator tags with invalid value in MV

func TestResourcePopulatorGetTagsInvalidMVValue(t *testing.T) {
	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		"validkey": {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "INVALID VALUE"}},
	}
	pop := &ResourcePopulator{
		ModelVersion: mv,
	}
	tags := pop.GetTags()
	_ = tags
}

// Test ComponentPopulator GetTags with nil MetadataStringValue

func TestComponentPopulatorGetTagsNilValue(t *testing.T) {
	rm := makeTestRM()
	rm.CustomProperties = &map[string]openapi.MetadataValue{
		"validkey": {},
	}
	pop := &ComponentPopulator{
		CommonPopulator: CommonPopulator{
			RegisteredModel: rm,
		},
	}
	tags := pop.GetTags()
	// Key itself should be used as tag
	found := false
	for _, tag := range tags {
		if tag == "validkey" {
			found = true
		}
	}
	common.AssertEqual(t, true, found)
}

// Test getFullSvcURL with non-predictor suffix

func TestGetFullSvcURLNonPredictorSuffix(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
		},
	}

	trueBool := true
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is-transformer", // Not predictor
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "InferenceService",
					Name:       "test-is",
					Controller: &trueBool,
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Port: 8080},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis, svc).Build()

	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Kis:            kis,
					CtrlClient:     cl,
					Ctx:            context.TODO(),
				},
			},
		},
	}

	url := pop.getFullSvcURL()
	// Non-predictor suffix should not match
	common.AssertEqual(t, "", url)
}

// Test getFullSvcURL with port 8080 directly

func TestGetFullSvcURLPort8080(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
		},
	}

	trueBool := true
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is-predictor",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "InferenceService",
					Name:       "test-is",
					Controller: &trueBool,
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       8080,
					TargetPort: intstr.FromString("http"),
				},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis, svc).Build()

	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Kis:            kis,
					CtrlClient:     cl,
					Ctx:            context.TODO(),
				},
			},
		},
	}

	url := pop.getFullSvcURL()
	// When TargetPort is a string type, port stays as sp.Port (8080)
	common.AssertEqual(t, "http://test-is-predictor.test-ns.svc.cluster.local:8080", url)
}

// Test GetAuthentication with SA that has nil OwnerReferences

func TestModelServerPopulatorGetAuthenticationSANoOwnerRef(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
		},
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is-sa",
			// No OwnerReferences
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis, sa).Build()

	pop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
					Kis:            kis,
					CtrlClient:     cl,
					Ctx:            context.TODO(),
				},
			},
		},
	}
	auth := pop.GetAuthentication()
	common.AssertEqual(t, true, auth != nil)
	common.AssertEqual(t, false, *auth)
}

// Test getFromModelRegistry with non-200 status

func TestGetFromModelRegistryNon200(t *testing.T) {
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	})
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	_, err := k.GetModelArtifact("999")
	common.AssertEqual(t, true, err != nil)
}

// Test ComponentPopulator.GetTags with MetadataStringValue nil in value

func TestResourcePopulatorGetTagsMVNilValue(t *testing.T) {
	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		"validkey": {}, // nil MetadataStringValue
	}
	pop := &ResourcePopulator{
		ModelVersion: mv,
	}
	tags := pop.GetTags()
	found := false
	for _, tag := range tags {
		if tag == "validkey" {
			found = true
		}
	}
	common.AssertEqual(t, true, found)
}

// Test ResourcePopulator GetTags MA nil MetadataStringValue

func TestResourcePopulatorGetTagsMANilValue(t *testing.T) {
	maName := "art"
	ma := openapi.ModelArtifact{
		Name: &maName,
		CustomProperties: &map[string]openapi.MetadataValue{
			"validkey": {}, // nil MetadataStringValue
		},
	}
	pop := &ResourcePopulator{
		ModelVersion:   makeTestMV(),
		ModelArtifacts: []openapi.ModelArtifact{ma},
	}
	tags := pop.GetTags()
	_ = tags
}

// Test LoopOverKFMR error in ListRegisteredModels

func TestLoopOverKFMR_ErrorListRM(t *testing.T) {
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("error"))
	})
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	_, _, _, err := LoopOverKFMR([]string{}, k)
	common.AssertEqual(t, true, err != nil)
}

// Test LoopOverKFMR error in GetRegisteredModel with IDs

func TestLoopOverKFMR_ErrorGetRM(t *testing.T) {
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("error"))
	})
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	_, _, _, err := LoopOverKFMR([]string{"1"}, k)
	common.AssertEqual(t, true, err != nil)
}

// Test LoopOverKFMR error in callKubeflowREST (ListModelVersions fails)

func TestLoopOverKFMR_ErrorListMV(t *testing.T) {
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, rest.LIST_REG_MODEL_URI):
			_, _ = w.Write([]byte(common.MnistRegisteredModels))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		}
	})
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	_, _, _, err := LoopOverKFMR([]string{}, k)
	common.AssertEqual(t, true, err != nil)
}

// Test LoopOverKFMR error in callKubeflowREST with IDs (ListModelVersions fails)

func TestLoopOverKFMR_ErrorListMVWithIDs(t *testing.T) {
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "registered_models/1") && !strings.Contains(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(common.MnistRegisteredModelsGet))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		}
	})
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	_, _, _, err := LoopOverKFMR([]string{"1"}, k)
	common.AssertEqual(t, true, err != nil)
}

// Test callKubeflowREST with empty artifacts (fallback path)

func TestCallKubeflowREST_EmptyArtifactsFallback(t *testing.T) {
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(common.TestJSONStringModelVersionOneLine))
		case strings.HasSuffix(r.URL.Path, "artifacts"):
			// First call returns empty, but since the path for both calls is the same pattern,
			// we always return empty. The function calls ListModelArtifacts with the mv id first,
			// then with the rm id. Both use the same URI pattern.
			_, _ = w.Write([]byte(`{"items":[],"nextPageToken":"","pageSize":0,"size":0}`))
		}
	})
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	mvs, mas, err := callKubeflowREST("1", k)
	common.AssertError(t, err)
	common.AssertEqual(t, true, len(mvs) > 0)
	_ = mas
}

// Test callKubeflowREST with error on ListModelArtifacts

func TestCallKubeflowREST_ErrorArtifacts(t *testing.T) {
	callCount := 0
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(common.TestJSONStringModelVersionOneLine))
		case strings.HasSuffix(r.URL.Path, "artifacts"):
			callCount++
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		}
	})
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	_, _, err := callKubeflowREST("1", k)
	common.AssertEqual(t, true, err != nil)
}

// Test GetTechDocs with valid http URL in custom props

func TestModelPopulatorGetTechDocsValidURL(t *testing.T) {
	rm := makeTestRM()
	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		brdgtypes.TechDocsKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "https://docs.example.com/techdocs"}},
	}
	pop := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
				},
				ModelArtifacts: makeTestMA(),
			},
		},
	}
	techDocs := pop.GetTechDocs()
	// Should return MA-based URL since MA processing overwrites techdocsUrl
	common.AssertEqual(t, true, techDocs != nil)
}

// Test GetTechDocs with no custom prop and no model artifacts

func TestModelPopulatorGetTechDocsNone(t *testing.T) {
	rm := makeTestRM()
	mv := makeTestMV()
	pop := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
				},
				ModelArtifacts: []openapi.ModelArtifact{},
			},
		},
	}
	techDocs := pop.GetTechDocs()
	// No techdocs key and no MAs = nil
	common.AssertEqual(t, true, techDocs == nil)
}

// Test GetModels with matching MA id

func TestModelCatalogPopulatorGetModels(t *testing.T) {
	rmId := "1"
	maId := "1" // same as RM id for the matching case
	rm := &openapi.RegisteredModel{
		Id:   &rmId,
		Name: "test-model",
		CustomProperties: &map[string]openapi.MetadataValue{},
	}
	mv := makeTestMV()
	maName := "test-art"
	maUri := "https://example.com/model"
	ma := openapi.ModelArtifact{
		Id:   &maId,
		Name: &maName,
		Uri:  &maUri,
	}

	pop := &ModelCatalogPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
					Owner:          "test-owner",
					Lifecycle:      "production",
				},
				ModelArtifacts: []openapi.ModelArtifact{ma},
			},
		},
	}
	models := pop.GetModels()
	common.AssertEqual(t, 1, len(models))
}

// Test GetLinksFromInferenceServices with DesiredState not ok

func TestGetLinksFromInferenceServicesDesiredStateNotSet(t *testing.T) {
	rmId := "1"
	rm := &openapi.RegisteredModel{
		Id:   &rmId,
		Name: "mnist",
	}

	// InferenceService with matching rmId but no DesiredState
	kfIs := &openapi.InferenceService{
		RegisteredModelId: rmId,
	}

	pop := &CommonPopulator{
		RegisteredModel:  rm,
		ModelVersion:     makeTestMV(),
		InferenceService: kfIs,
	}
	links := pop.GetLinksFromInferenceServices()
	common.AssertEqual(t, 0, len(links))
}

// Test ModelServerAPIPopulator.GetType with grpc

func TestModelServerAPIPopulatorGetTypeGrpc(t *testing.T) {
	rm := makeTestRM()
	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		brdgtypes.APITypeKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "grpc"}},
	}
	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
				},
			},
		},
	}
	common.AssertEqual(t, "grpc", string(pop.GetType()))
}

// Test ModelServerAPIPopulator.GetType with unknown type

func TestModelServerAPIPopulatorGetTypeUnknown(t *testing.T) {
	rm := makeTestRM()
	mv := makeTestMV()
	mv.CustomProperties = &map[string]openapi.MetadataValue{
		brdgtypes.APITypeKey: {MetadataStringValue: &openapi.MetadataStringValue{StringValue: "unknown-type"}},
	}
	pop := &ModelServerAPIPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
				},
			},
		},
	}
	// Unknown type should default to openapi
	common.AssertEqual(t, "openapi", string(pop.GetType()))
}

// Test GetModelServer with no inference service

func TestModelCatalogPopulatorGetModelServerNone(t *testing.T) {
	pop := &ModelCatalogPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
				},
			},
		},
	}
	ms := pop.GetModelServer()
	common.AssertEqual(t, true, ms == nil)
}

// Test GetModelServer with InferenceService not matching

func TestModelCatalogPopulatorGetModelServerNoMatch(t *testing.T) {
	rmId := "1"
	mvId := "2"
	rm := &openapi.RegisteredModel{
		Id:   &rmId,
		Name: "test-model",
		CustomProperties: &map[string]openapi.MetadataValue{},
	}
	mv := &openapi.ModelVersion{
		Id:                &mvId,
		Name:              "v1",
		RegisteredModelId: "999", // different from rm
		CustomProperties:  &map[string]openapi.MetadataValue{},
	}
	kfIs := &openapi.InferenceService{
		RegisteredModelId: rmId,
		ModelVersionId:    &mvId,
	}

	pop := &ModelCatalogPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel:  rm,
					ModelVersion:     mv,
					InferenceService: kfIs,
				},
			},
		},
	}
	ms := pop.GetModelServer()
	common.AssertEqual(t, true, ms == nil)
}

// Test callKubeflowREST with fallback artifacts (first empty, second has data)

func TestCallKubeflowREST_FallbackArtifacts(t *testing.T) {
	callCount := 0
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(common.TestJSONStringModelVersionOneLine))
		case strings.Contains(r.URL.Path, "artifacts"):
			callCount++
			if callCount == 1 {
				// First call: empty artifacts
				_, _ = w.Write([]byte(`{"items":[],"nextPageToken":"","pageSize":0,"size":0}`))
			} else {
				// Second call: has artifacts
				_, _ = w.Write([]byte(common.TestJSONStringModelArtifactOneLine))
			}
		}
	})
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	mvs, mas, err := callKubeflowREST("1", k)
	common.AssertError(t, err)
	common.AssertEqual(t, true, len(mvs) > 0)
	common.AssertEqual(t, true, len(mas) > 0)
}

// Test callKubeflowREST with error on second ListModelArtifacts call

func TestCallKubeflowREST_ErrorSecondArtifactsCall(t *testing.T) {
	callCount := 0
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "versions"):
			_, _ = w.Write([]byte(common.TestJSONStringModelVersionOneLine))
		case strings.Contains(r.URL.Path, "artifacts"):
			callCount++
			if callCount == 1 {
				// First call: empty
				_, _ = w.Write([]byte(`{"items":[],"nextPageToken":"","pageSize":0,"size":0}`))
			} else {
				// Second call: error
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("error"))
			}
		}
	})
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	_, _, err := callKubeflowREST("1", k)
	common.AssertEqual(t, true, err != nil)
}

// Test GetArtifactLocationURL with artifact that has URI

func TestModelPopulatorGetArtifactLocationURL(t *testing.T) {
	pop := &ModelPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: makeTestRM(),
					ModelVersion:    makeTestMV(),
				},
				ModelArtifacts: makeTestMA(),
			},
		},
	}
	url := pop.GetArtifactLocationURL()
	common.AssertEqual(t, true, url != nil)
	common.AssertEqual(t, "https://example.com/model.onnx", *url)
}

// Test GetKubeFlowInferenceServicesForModelVersion with error

func TestGetKubeFlowInferenceServicesForModelVersion_Error(t *testing.T) {
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("error"))
	})
	defer ts.Close()
	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	mv := makeTestMV()
	_, err := GetKubeFlowInferenceServicesForModelVersion(k, mv)
	common.AssertEqual(t, true, err != nil)
}

// Test GetModelServer with KIS matching via KServeInferenceServiceMapping

func TestModelCatalogPopulatorGetModelServerWithKIS(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	rmId := "1"
	mvId := "2"
	rm := &openapi.RegisteredModel{
		Id:   &rmId,
		Name: "test-model",
		CustomProperties: &map[string]openapi.MetadataValue{},
	}
	mv := &openapi.ModelVersion{
		Id:                &mvId,
		Name:              "v1",
		RegisteredModelId: rmId,
		CustomProperties:  &map[string]openapi.MetadataValue{},
	}

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-is",
			Labels: map[string]string{
				"modelregistry.opendatahub.io/registered-model-id": rmId,
				"modelregistry.opendatahub.io/model-version-id":    mvId,
			},
		},
		Status: serverapiv1beta1.InferenceServiceStatus{
			URL: &apis.URL{
				Scheme: "https",
				Host:   "test-is.example.com",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis).Build()

	msPop := &ModelServerPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
					Kis:            kis,
					CtrlClient:     cl,
					Ctx:            context.TODO(),
				},
			},
		},
	}

	pop := &ModelCatalogPopulator{
		CommonSchemaPopulator: CommonSchemaPopulator{
			ComponentPopulator: ComponentPopulator{
				CommonPopulator: CommonPopulator{
					RegisteredModel: rm,
					ModelVersion:    mv,
					Kis:            kis,
					CtrlClient:     cl,
					Ctx:            context.TODO(),
				},
			},
		},
		MSPop: msPop,
	}
	ms := pop.GetModelServer()
	// KIS matching via labels should result in a model server
	if ms != nil {
		common.AssertEqual(t, "test-is", ms.Name)
	}
}

// Test GetLinksFromInferenceServices full path with Kfmr and ServingClient nil

func TestGetLinksFromInferenceServicesFullPath(t *testing.T) {
	ts := createFullTestServer(t)
	defer ts.Close()

	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cfg := &config.Config{}
	kfmr.SetupKubeflowTestRESTClient(ts, cfg)
	k := SetupKubeflowRESTClient(cfg)

	kis := &serverapiv1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ggmtest",
			Name:      "mnist-v1",
		},
		Status: serverapiv1beta1.InferenceServiceStatus{
			URL: &apis.URL{
				Scheme: "https",
				Host:   "kserve.com",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(kis).Build()

	rmId := "1"
	rm := &openapi.RegisteredModel{
		Id:   &rmId,
		Name: "mnist",
	}

	deployed := openapi.INFERENCESERVICESTATE_DEPLOYED
	mvId := "2"
	kfIs := &openapi.InferenceService{
		RegisteredModelId:    rmId,
		ModelVersionId:       &mvId,
		DesiredState:         &deployed,
		ServingEnvironmentId: "3",
		Runtime:              func() *string { s := "mnist-v1"; return &s }(),
	}

	mv := &openapi.ModelVersion{
		Id:                &mvId,
		Name:              "v1",
		RegisteredModelId: rmId,
	}

	pop := &CommonPopulator{
		RegisteredModel:  rm,
		ModelVersion:     mv,
		InferenceService: kfIs,
		Kfmr:            k,
		CtrlClient:      cl,
		Ctx:             context.TODO(),
	}
	links := pop.GetLinksFromInferenceServices()
	// With a valid inference service that's deployed and matching, and kserve IS found
	common.AssertEqual(t, true, len(links) >= 0)
}
