package backstage

import (
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	"testing"
)

// Tests that exercise error paths in REST client methods by using an unreachable URL.

func TestListEntitiesConnectionError(t *testing.T) {
	b := &BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: "http://127.0.0.1:1"}
	_, err := b.ListEntities()
	if err == nil {
		t.Error("expected connection error")
	}
}

func TestListLocationsConnectionError(t *testing.T) {
	b := &BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: "http://127.0.0.1:1"}
	_, err := b.ListLocations()
	if err == nil {
		t.Error("expected connection error")
	}
}

func TestGetLocationsConnectionError(t *testing.T) {
	b := &BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: "http://127.0.0.1:1"}
	_, err := b.GetLocations("some-id")
	if err == nil {
		t.Error("expected connection error")
	}
}

func TestGetLocationConnectionError(t *testing.T) {
	b := &BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: "http://127.0.0.1:1"}
	_, err := b.GetLocation("some-id")
	if err == nil {
		t.Error("expected connection error")
	}
}

func TestImportLocationConnectionError(t *testing.T) {
	b := &BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: "http://127.0.0.1:1"}
	_, err := b.ImportLocation("http://example.com/catalog-info.yaml")
	if err == nil {
		t.Error("expected connection error")
	}
}

func TestDeleteLocationConnectionError(t *testing.T) {
	b := &BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: "http://127.0.0.1:1"}
	_, err := b.DeleteLocation("some-id")
	if err == nil {
		t.Error("expected connection error")
	}
}

func TestListComponentsConnectionError(t *testing.T) {
	b := &BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: "http://127.0.0.1:1"}
	_, err := b.ListComponents()
	if err == nil {
		t.Error("expected connection error")
	}
}

func TestListAPIsConnectionError(t *testing.T) {
	b := &BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: "http://127.0.0.1:1"}
	_, err := b.ListAPIs()
	if err == nil {
		t.Error("expected connection error")
	}
}

func TestListResourcesConnectionError(t *testing.T) {
	b := &BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: "http://127.0.0.1:1"}
	_, err := b.ListResources()
	if err == nil {
		t.Error("expected connection error")
	}
}
