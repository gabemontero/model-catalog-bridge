package storage

import (
	"encoding/json"
	"fmt"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/server/storage"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/rest"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/types"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/util"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func SetupBridgeStorageRESTClient(ts *httptest.Server) *storage.BridgeStorageRESTClient {
	storageTC := &storage.BridgeStorageRESTClient{}
	storageTC.RESTClient = common.DC()
	storageTC.UpsertURL = ts.URL + util.UpsertURI
	storageTC.ListURL = ts.URL + util.ListURI
	storageTC.FetchURL = ts.URL + util.FetchURI
	return storageTC
}

func CreateBridgeStorageREST(t *testing.T, called *sync.Map) *httptest.Server {
	ts := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Method: %v", r.Method)
		t.Logf("Path: %v", r.URL.Path)

		switch r.Method {
		case common.MethodGet:
			switch {
			case strings.Contains(r.URL.Path, util.ListURI):
				called.Store(util.ListURI, util.ListURI)
				w.Header().Set("Content-Type", "application/json")
				d := &storage.DiscoverResponse{Keys: []string{"foo_bar"}}
				buf, _ := json.Marshal(d)
				w.Write(buf)
				w.WriteHeader(http.StatusOK)
			case strings.Contains(r.URL.Path, util.FetchURI):
				called.Store(util.FetchURI, util.FetchURI)
				w.Header().Set("Content-Type", "application/json")
				sb := types.StorageBody{
					Body:            []byte{},
					LocationId:      "foo-id",
					LocationTarget:  "http://foo.io",
					LocationIDValid: false,
				}
				buf, _ := json.Marshal(&sb)
				w.Write(buf)
				w.WriteHeader(http.StatusOK)
			}
		case common.MethodPost:
			switch r.URL.Path {
			default:
				w.Header().Set("Content-Type", "application/json")
				bodyBuf, err := io.ReadAll(r.Body)
				if err != nil {
					_, _ = w.Write([]byte(fmt.Sprintf(common.TestPostJSONStringOneLinePlusBody, err.Error())))
					w.WriteHeader(500)
					return
				}
				if len(bodyBuf) == 0 {
					w.WriteHeader(500)
					return
				}
				data := rest.PostBody{}
				err = json.Unmarshal(bodyBuf, &data)
				if err != nil {
					t.Logf("error unmarshall into storage PostBody: %s", err.Error())
					_, _ = w.Write([]byte(fmt.Sprintf(common.TestPostJSONStringOneLinePlusBody, err.Error())))
					w.WriteHeader(500)
					return
				}
				t.Logf("got buf of len %d", len(data.Body))
				called.Store(r.URL.Path, string(data.Body))
				_, _ = w.Write([]byte(fmt.Sprintf(common.TestPostJSONStringOneLinePlusBody, string(data.Body))))
				w.WriteHeader(201)

			}
		}
	})
	return ts
}
