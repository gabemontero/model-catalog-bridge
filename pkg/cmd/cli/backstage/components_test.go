package backstage

import (
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/backstage"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	"testing"
)

func TestListComponents(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	// Get with no args calls List
	str, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).GetComponent()
	common.AssertError(t, err)
	common.AssertLineCompare(t, str, common.Components, 0)
}

func TestGetComponents(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	nsName := "default:ollama-service-component"
	str, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).GetComponent(nsName)

	common.AssertError(t, err)
	common.AssertContains(t, str, []string{nsName})
}

func TestGetComponentsError(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	nsName := "404:404"
	_, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).GetComponent(nsName)
	if err == nil {
		t.Error("expected error")
	}
}

func TestGetComponentsWithTags(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	bs := &BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}
	bs.Tags = true

	for _, tc := range []struct {
		args   []string
		str    string
		subset bool
	}{
		{
			args: []string{"genai", "meta"},
		},
		{
			args: []string{"gateway", "authenticated", "developer-model-service", "llm", "vllm", "ibm-granite", "genai"},
			str:  common.ComponentsFromTagsNoSubset,
		},
		{
			args:   []string{"genai"},
			subset: true,
			str:    common.ComponentsFromTags,
		},
	} {
		bs.Subset = tc.subset
		str, err := bs.GetComponent(tc.args...)
		common.AssertError(t, err)
		common.AssertLineCompare(t, str, tc.str, 0)
	}
}
