package backstage

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/redhat-ai-dev/model-catalog-bridge/schema/types/golang"
)

// stub implementations of the populator interfaces

type stubCommon struct{}

func (s *stubCommon) GetOwner() string          { return "test-owner" }
func (s *stubCommon) GetLifecycle() string       { return "production" }
func (s *stubCommon) GetName() string            { return "test-name" }
func (s *stubCommon) GetDescription() string     { return "test-description" }
func (s *stubCommon) GetLinks() []EntityLink     { return []EntityLink{{URL: "http://example.com", Title: "Example"}} }
func (s *stubCommon) GetTags() []string          { return []string{"tag1", "tag2"} }
func (s *stubCommon) GetProvidedAPIs() []string  { return []string{"api1"} }
func (s *stubCommon) GetTechdocRef() string      { return "dir:." }
func (s *stubCommon) GetDisplayName() string     { return "Test Display Name" }

type stubComponentPopulator struct{ stubCommon }

func (s *stubComponentPopulator) GetDependsOn() []string { return []string{"resource:default/dep1"} }

type stubResourcePopulator struct{ stubCommon }

func (s *stubResourcePopulator) GetDependencyOf() []string { return []string{"component:default/comp1"} }

type stubAPIPopulator struct{ stubCommon }

func (s *stubAPIPopulator) GetDefinition() string      { return "openapi: 3.0.0" }
func (s *stubAPIPopulator) GetDependencyOf() []string  { return []string{"component:default/comp1"} }

type stubAPIPopulatorAsyncAPI struct{ stubCommon }

func (s *stubAPIPopulatorAsyncAPI) GetDefinition() string      { return "asyncapi: 2.0.0" }
func (s *stubAPIPopulatorAsyncAPI) GetDependencyOf() []string  { return nil }

type stubAPIPopulatorGraphQL struct{ stubCommon }

func (s *stubAPIPopulatorGraphQL) GetDefinition() string      { return "type Query { graphql }" }
func (s *stubAPIPopulatorGraphQL) GetDependencyOf() []string  { return nil }

type stubAPIPopulatorTRPC struct{ stubCommon }

func (s *stubAPIPopulatorTRPC) GetDefinition() string      { return "trpc router definition" }
func (s *stubAPIPopulatorTRPC) GetDependencyOf() []string  { return nil }

type stubAPIPopulatorGRPC struct{ stubCommon }

func (s *stubAPIPopulatorGRPC) GetDefinition() string      { return "syntax = proto3;" }
func (s *stubAPIPopulatorGRPC) GetDependencyOf() []string  { return nil }

type stubAPIPopulatorUnknown struct{ stubCommon }

func (s *stubAPIPopulatorUnknown) GetDefinition() string      { return "some-random-definition" }
func (s *stubAPIPopulatorUnknown) GetDependencyOf() []string  { return nil }

type stubModelCatalogPopulator struct {
	models      []golang.Model
	modelServer *golang.ModelServer
}

func (s *stubModelCatalogPopulator) GetModels() []golang.Model       { return s.models }
func (s *stubModelCatalogPopulator) GetModelServer() *golang.ModelServer { return s.modelServer }

// failWriter always returns an error on Write
type failWriter struct{}

func (f *failWriter) Write(p []byte) (n int, err error) {
	return 0, fmt.Errorf("intentional write error")
}

func TestPrintComponentError(t *testing.T) {
	err := PrintComponent(&stubComponentPopulator{}, &failWriter{})
	if err == nil {
		t.Error("expected error from failing writer")
	}
}

func TestPrintResourceError(t *testing.T) {
	err := PrintResource(&stubResourcePopulator{}, &failWriter{})
	if err == nil {
		t.Error("expected error from failing writer")
	}
}

func TestPrintAPIError(t *testing.T) {
	err := PrintAPI(&stubAPIPopulator{}, &failWriter{})
	if err == nil {
		t.Error("expected error from failing writer")
	}
}

func TestPrintModelCatalogPopulatorError(t *testing.T) {
	pop := &stubModelCatalogPopulator{
		models: []golang.Model{{Name: "m", Description: "d", Lifecycle: "l", Owner: "o"}},
	}
	err := PrintModelCatalogPopulator(pop, &failWriter{})
	if err == nil {
		t.Error("expected error from failing writer")
	}
}

func TestPrintComponent(t *testing.T) {
	buf := &bytes.Buffer{}
	err := PrintComponent(&stubComponentPopulator{}, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if len(output) == 0 {
		t.Error("expected non-empty output")
	}
	// Verify key fields are present in the YAML output
	for _, expected := range []string{"Component", "test-name", "test-owner", "production", "model-server"} {
		if !bytes.Contains([]byte(output), []byte(expected)) {
			t.Errorf("expected output to contain %q", expected)
		}
	}
}

func TestPrintResource(t *testing.T) {
	buf := &bytes.Buffer{}
	err := PrintResource(&stubResourcePopulator{}, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if len(output) == 0 {
		t.Error("expected non-empty output")
	}
	for _, expected := range []string{"Resource", "test-name", "test-owner", "ai-model"} {
		if !bytes.Contains([]byte(output), []byte(expected)) {
			t.Errorf("expected output to contain %q", expected)
		}
	}
}

func TestPrintAPIOpenAPI(t *testing.T) {
	buf := &bytes.Buffer{}
	err := PrintAPI(&stubAPIPopulator{}, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("openapi")) {
		t.Error("expected output to contain openapi type")
	}
}

func TestPrintAPIAsyncAPI(t *testing.T) {
	buf := &bytes.Buffer{}
	err := PrintAPI(&stubAPIPopulatorAsyncAPI{}, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("asyncapi")) {
		t.Error("expected output to contain asyncapi type")
	}
}

func TestPrintAPIGraphQL(t *testing.T) {
	buf := &bytes.Buffer{}
	err := PrintAPI(&stubAPIPopulatorGraphQL{}, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("graphql")) {
		t.Error("expected output to contain graphql type")
	}
}

func TestPrintAPITRPC(t *testing.T) {
	buf := &bytes.Buffer{}
	err := PrintAPI(&stubAPIPopulatorTRPC{}, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("trpc")) {
		t.Error("expected output to contain trpc type")
	}
}

func TestPrintAPIGRPC(t *testing.T) {
	buf := &bytes.Buffer{}
	err := PrintAPI(&stubAPIPopulatorGRPC{}, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("grpc")) {
		t.Error("expected output to contain grpc type")
	}
}

func TestPrintAPIUnknown(t *testing.T) {
	buf := &bytes.Buffer{}
	err := PrintAPI(&stubAPIPopulatorUnknown{}, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("unknown")) {
		t.Error("expected output to contain unknown type")
	}
}

func TestPrintModelCatalogPopulator(t *testing.T) {
	apiURL := "http://model-server.example.com/v1"
	pop := &stubModelCatalogPopulator{
		models: []golang.Model{
			{
				Name:        "test-model",
				Description: "A test model",
				Lifecycle:   "production",
				Owner:       "test-owner",
			},
		},
		modelServer: &golang.ModelServer{
			Name:        "test-server",
			Description: "A test server",
			Lifecycle:   "production",
			Owner:       "test-owner",
			API: &golang.API{
				URL:  apiURL,
				Spec: "openapi",
				Type: golang.Openapi,
			},
		},
	}
	buf := &bytes.Buffer{}
	err := PrintModelCatalogPopulator(pop, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if len(output) == 0 {
		t.Error("expected non-empty output")
	}
	if !bytes.Contains([]byte(output), []byte("test-model")) {
		t.Error("expected output to contain model name")
	}
}

func TestPrintModelCatalogPopulatorNoModelServer(t *testing.T) {
	pop := &stubModelCatalogPopulator{
		models: []golang.Model{
			{
				Name:        "test-model",
				Description: "A test model",
				Lifecycle:   "production",
				Owner:       "test-owner",
			},
		},
		modelServer: nil,
	}
	buf := &bytes.Buffer{}
	err := PrintModelCatalogPopulator(pop, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if len(output) == 0 {
		t.Error("expected non-empty output")
	}
}

func TestPrintModelCatalogPopulatorModelServerNoAPI(t *testing.T) {
	pop := &stubModelCatalogPopulator{
		models: []golang.Model{
			{
				Name:        "test-model",
				Description: "A test model",
				Lifecycle:   "production",
				Owner:       "test-owner",
			},
		},
		modelServer: &golang.ModelServer{
			Name:        "test-server",
			Description: "A test server",
			Lifecycle:   "production",
			Owner:       "test-owner",
			API:         nil,
		},
	}
	buf := &bytes.Buffer{}
	err := PrintModelCatalogPopulator(pop, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrintModelCatalogPopulatorModelServerEmptyURL(t *testing.T) {
	pop := &stubModelCatalogPopulator{
		models: []golang.Model{
			{
				Name:        "test-model",
				Description: "A test model",
				Lifecycle:   "production",
				Owner:       "test-owner",
			},
		},
		modelServer: &golang.ModelServer{
			Name:        "test-server",
			Description: "A test server",
			Lifecycle:   "production",
			Owner:       "test-owner",
			API: &golang.API{
				URL:  "",
				Spec: "openapi",
				Type: golang.Openapi,
			},
		},
	}
	buf := &bytes.Buffer{}
	err := PrintModelCatalogPopulator(pop, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
