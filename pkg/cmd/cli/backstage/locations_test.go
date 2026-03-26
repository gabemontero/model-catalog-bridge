package backstage

import (
	"fmt"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/backstage"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	"testing"
)

func TestListLocations(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	str, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).ListLocations()
	common.AssertError(t, err)
	common.AssertEqual(t, common.TestJSONStringIndented, str)
}

func TestGetLocations(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	key := "key1"
	str, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).GetLocations(key)
	common.AssertError(t, err)
	common.AssertContains(t, str, []string{key})
}

func TestGetLocation(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	key := "TestGet"
	m, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).GetLocation(key)
	common.AssertError(t, err)
	keysStr := ""
	for k := range m {
		keysStr = fmt.Sprintf("%s;%s", keysStr, k)
	}
	common.AssertContains(t, keysStr, []string{key})
}

func TestGetLocationsError(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	nsName := "404:404"
	_, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).GetLocations(nsName)
	if err == nil {
		t.Error("expected error")
	}
}

func TestGetLocationError(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	nsName := "404:404"
	_, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).GetLocation(nsName)
	if err == nil {
		t.Error("expected error")
	}
}

func TestImportLocation(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	arg := "http://rhoai-bridge.com/mnist/v1/catalog-info.yaml"
	retJSON, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).ImportLocation(arg)
	common.AssertError(t, err)
	str, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).PrintImportLocation(retJSON)
	common.AssertError(t, err)
	common.AssertContains(t, str, []string{arg})
}

func TestImportLocationGithub(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	arg := "https://github.com/my-org/my-repo/blob/main/catalog-info.yaml"
	retJSON, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).ImportLocation(arg)
	common.AssertError(t, err)
	str, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).PrintImportLocation(retJSON)
	common.AssertError(t, err)
	if len(str) == 0 {
		t.Error("expected non-empty output")
	}
}

func TestImportLocationError(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	arg := ":"
	_, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).ImportLocation(arg)
	if err == nil {
		t.Error("expected error")
	}
}

func TestDeleteLocation(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	arg := "my-location-id"
	str, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).DeleteLocation(arg)
	common.AssertError(t, err)
	common.AssertContains(t, str, []string{arg})
}

func TestDeleteLocationsError(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	nsName := "404:404"
	_, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).DeleteLocation(nsName)
	if err == nil {
		t.Error("expected error")
	}
}

func TestParseImportLocationMapWithLocationKey(t *testing.T) {
	retJSON := map[string]any{
		"location": map[string]interface{}{
			"id":     "loc-123",
			"target": "http://example.com/catalog-info.yaml",
		},
	}
	id, target, ok := (&BackstageRESTClientWrapper{}).ParseImportLocationMap(retJSON)
	if !ok {
		t.Fatal("expected ok to be true")
	}
	if id != "loc-123" {
		t.Errorf("expected id loc-123, got %s", id)
	}
	if target != "http://example.com/catalog-info.yaml" {
		t.Errorf("expected target http://example.com/catalog-info.yaml, got %s", target)
	}
}

func TestParseImportLocationMapWithIDTargetKeys(t *testing.T) {
	retJSON := map[string]any{
		"id":     "loc-456",
		"target": "http://example.com/other.yaml",
	}
	id, target, ok := (&BackstageRESTClientWrapper{}).ParseImportLocationMap(retJSON)
	if !ok {
		t.Fatal("expected ok to be true")
	}
	if id != "loc-456" {
		t.Errorf("expected id loc-456, got %s", id)
	}
	if target != "http://example.com/other.yaml" {
		t.Errorf("expected target http://example.com/other.yaml, got %s", target)
	}
}

func TestParseImportLocationMapEmpty(t *testing.T) {
	retJSON := map[string]any{}
	_, _, ok := (&BackstageRESTClientWrapper{}).ParseImportLocationMap(retJSON)
	if ok {
		t.Error("expected ok to be false for empty map")
	}
}

func TestParseImportLocationMapLocationNotMap(t *testing.T) {
	retJSON := map[string]any{
		"location": "just-a-string",
	}
	_, _, ok := (&BackstageRESTClientWrapper{}).ParseImportLocationMap(retJSON)
	if ok {
		t.Error("expected ok to be false when location is not a map")
	}
}

func TestParseImportLocationMapIDOnly(t *testing.T) {
	retJSON := map[string]any{
		"id": "loc-789",
	}
	_, _, ok := (&BackstageRESTClientWrapper{}).ParseImportLocationMap(retJSON)
	if ok {
		t.Error("expected ok to be false when target is missing")
	}
}

func TestPrintImportLocationWithLocationKey(t *testing.T) {
	retJSON := map[string]any{
		"location": map[string]interface{}{
			"id":     "loc-123",
			"target": "http://example.com/catalog-info.yaml",
		},
	}
	str, err := (&BackstageRESTClientWrapper{}).PrintImportLocation(retJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	common.AssertContains(t, str, []string{"loc-123", "http://example.com/catalog-info.yaml"})
}

func TestPrintImportLocationWithIDTarget(t *testing.T) {
	retJSON := map[string]any{
		"id":     "loc-456",
		"target": "http://example.com/other.yaml",
	}
	str, err := (&BackstageRESTClientWrapper{}).PrintImportLocation(retJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	common.AssertContains(t, str, []string{"loc-456", "http://example.com/other.yaml"})
}

func TestPrintImportLocationWithIDOnly(t *testing.T) {
	retJSON := map[string]any{
		"id": "loc-789",
	}
	str, err := (&BackstageRESTClientWrapper{}).PrintImportLocation(retJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	common.AssertContains(t, str, []string{"loc-789"})
}

func TestPrintImportLocationEmpty(t *testing.T) {
	retJSON := map[string]any{}
	str, err := (&BackstageRESTClientWrapper{}).PrintImportLocation(retJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(str) == 0 {
		t.Error("expected non-empty output")
	}
}
