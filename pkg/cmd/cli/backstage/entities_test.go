package backstage

import (
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/backstage"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	"testing"
)

func TestListEntities(t *testing.T) {
	ts := backstage.CreateServer(t)
	defer ts.Close()

	str, err := (&BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: ts.URL}).ListEntities()
	common.AssertError(t, err)
	common.AssertEqual(t, common.TestJSONStringIndented, str)
}
