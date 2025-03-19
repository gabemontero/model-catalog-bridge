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
