package backstage

import (
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/backstage"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	"testing"
)

func TestListAPIs(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	// Get with no args calls List
	str, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).GetAPI()

	common.AssertError(t, err)
	common.AssertLineCompare(t, str, common.Apis, 0)
}

func TestGetAPIs(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	nsName := "default:ollama-service-api"
	str, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).GetAPI(nsName)

	common.AssertError(t, err)
	common.AssertContains(t, str, []string{nsName})
}

func TestGetAPIsError(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	nsName := "404:404"
	_, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).GetAPI(nsName)
	if err == nil {
		t.Error("expected error")
	}
}

func TestGetAPIsWithTags(t *testing.T) {
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
			args: []string{"vllm", "api", "openai"},
			str:  common.ApisFromTagsNoSubset,
		},
		{
			args:   []string{"vllm"},
			subset: true,
			str:    common.ApisFromTags,
		},
	} {
		bs.Subset = tc.subset
		str, err := bs.GetAPI(tc.args...)
		common.AssertError(t, err)
		common.AssertLineCompare(t, str, tc.str, 0)
	}
}
