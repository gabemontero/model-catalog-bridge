package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/server/storage"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/rest"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/types"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	testgin "github.com/redhat-ai-dev/model-catalog-bridge/test/stub/gin-gonic"
	apijson "k8s.io/apimachinery/pkg/util/json"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.ReleaseMode)
	os.Exit(m.Run())
}

func TestHandleCatalogDiscoveryGet(t *testing.T) {
	for _, tc := range []struct {
		name              string
		content           map[string]*ImportLocation
		expectedSC        int
		expectedBody      string
		expectedBodyParts []string
	}{
		{
			name:         "no contents",
			expectedSC:   http.StatusOK,
			expectedBody: `{"uris":null}`,
		},
		{
			name:       "single line without content",
			expectedSC: http.StatusOK,
			content: map[string]*ImportLocation{
				"/mnist/v1/catalog": {},
			},
			expectedBody: `{"uris":null}`,
		},
		{
			name:       "single line with content",
			expectedSC: http.StatusOK,
			content: map[string]*ImportLocation{
				"/mnist/v1/catalog": {content: []byte{}},
			},
			expectedBody: `{"uris":["/mnist/v1/catalog"]}`,
		},
		{
			name:       "multi line",
			expectedSC: http.StatusOK,
			content: map[string]*ImportLocation{
				"/mnist/v1/catalog": {content: []byte{}},
				"/mnist/v2/catalog": {content: []byte{}},
			},
			// ordering not guaranteed on which uri is first
			expectedBodyParts: []string{"/mnist/v1/catalog", "/mnist/v2/catalog"},
		},
	} {
		testWriter := testgin.NewTestResponseWriter()
		ctx, _ := gin.CreateTestContext(testWriter)
		ils := &ImportLocationServer{content: tc.content, modelcards: map[string]modelCardMetadata{}}

		ils.handleCatalogDiscoveryGet(ctx)

		common.AssertEqual(t, ctx.Writer.Status(), tc.expectedSC)
		bodyBuf := testWriter.ResponseWriter.Body
		common.AssertNotNil(t, bodyBuf)
		if len(tc.expectedBody) > 0 {
			common.AssertEqual(t, tc.expectedBody, bodyBuf.String())
		}
		common.AssertContains(t, bodyBuf.String(), tc.expectedBodyParts)
	}
}

func TestHandleCatalogDiscoveryGetModel(t *testing.T) {
	for _, tc := range []struct {
		name              string
		content           map[string]modelCardMetadata
		param             string
		expectedSC        int
		expectedBody      string
		expectedBodyParts []string
	}{
		{
			name:       "no content",
			param:      "foo",
			expectedSC: http.StatusNotFound,
		},
		{
			name:       "invalid key",
			expectedSC: http.StatusNotFound,
			param:      "foo",
			content: map[string]modelCardMetadata{
				"bar": {content: "bar", needToUpdate: true},
			},
		},
		{
			name:       "valid key",
			expectedSC: http.StatusOK,
			content: map[string]modelCardMetadata{
				"foo": {content: "bar", needToUpdate: true},
			},
			param:        "foo",
			expectedBody: `bar`,
		},
	} {
		testWriter := testgin.NewTestResponseWriter()
		ctx, _ := gin.CreateTestContext(testWriter)
		ils := &ImportLocationServer{content: map[string]*ImportLocation{}, modelcards: tc.content}

		req, _ := http.NewRequest(http.MethodGet, "/modelcard?key="+tc.param, nil)
		ctx.Request = req

		ils.handleModelCardGet(ctx)

		common.AssertEqual(t, ctx.Writer.Status(), tc.expectedSC)
		bodyBuf := testWriter.ResponseWriter.Body
		common.AssertNotNil(t, bodyBuf)
		if len(tc.expectedBody) > 0 {
			common.AssertEqual(t, tc.expectedBody, bodyBuf.String())
		}
		common.AssertContains(t, bodyBuf.String(), tc.expectedBodyParts)
	}
}

func TestHandleCatalogUpsertPost(t *testing.T) {
	// define outside of the test loop so we can vet updates vs. creates
	ils := &ImportLocationServer{content: map[string]*ImportLocation{}, modelcards: map[string]modelCardMetadata{}}
	for _, tc := range []struct {
		name            string
		reqURL          url.URL
		body            rest.PostBody
		expectedErrMsg  string
		expectedSC      int
		expectedContent map[string]*ImportLocation
	}{
		{
			name:           "no query param",
			expectedSC:     http.StatusBadRequest,
			expectedErrMsg: "need a 'key' parameter",
		},
		{
			name:           "bad query param",
			reqURL:         url.URL{RawQuery: "key=mnist"},
			expectedSC:     http.StatusBadRequest,
			expectedErrMsg: "bad key format",
		},
		{
			name:       "new entry",
			reqURL:     url.URL{RawQuery: "key=mnist_v1"},
			body:       rest.PostBody{Body: []byte("create")},
			expectedSC: http.StatusCreated,
			expectedContent: map[string]*ImportLocation{
				"/mnist/v1/catalog-info.yaml": {content: []byte("create")},
			},
		},
		{
			name:       "updated entry",
			reqURL:     url.URL{RawQuery: "key=mnist_v1"},
			body:       rest.PostBody{Body: []byte("update")},
			expectedSC: http.StatusCreated,
			expectedContent: map[string]*ImportLocation{
				"/mnist/v1/catalog-info.yaml": {content: []byte("update")},
			},
		},
	} {
		testWriter := testgin.NewTestResponseWriter()
		data, err := apijson.Marshal(tc.body)
		common.AssertError(t, err)
		ctx, eng := gin.CreateTestContext(testWriter)
		ctx.Request = &http.Request{URL: &tc.reqURL, Body: io.NopCloser(bytes.NewReader(data))}
		ils.router = eng

		ils.handleCatalogUpsertPost(ctx)

		common.AssertEqual(t, ctx.Writer.Status(), tc.expectedSC)
		if len(tc.expectedErrMsg) > 0 {
			errors := ctx.Errors
			found := false
			for _, e := range errors {
				if strings.Contains(e.Error(), tc.expectedErrMsg) {
					found = true
					break
				}
			}
			common.AssertEqual(t, true, found)
		}

		common.AssertEqual(t, len(tc.expectedContent), len(ils.content))
		for key, val := range tc.expectedContent {
			v, ok := ils.content[key]
			common.AssertEqual(t, true, ok)
			common.AssertEqual(t, val, v)
		}
	}
}

func TestHandleCatalogInfoGet(t *testing.T) {
	for _, tc := range []struct {
		name       string
		content    []byte
		expectedSC int
	}{
		{name: "nil content", content: nil, expectedSC: http.StatusNotFound},
		{name: "with content", content: []byte("test-data"), expectedSC: http.StatusOK},
	} {
		testWriter := testgin.NewTestResponseWriter()
		ctx, _ := gin.CreateTestContext(testWriter)
		il := &ImportLocation{content: tc.content}
		il.handleCatalogInfoGet(ctx)
		common.AssertEqual(t, ctx.Writer.Status(), tc.expectedSC)
		if tc.content != nil {
			bodyBuf := testWriter.ResponseWriter.Body
			common.AssertNotNil(t, bodyBuf)
			common.AssertEqual(t, string(tc.content), bodyBuf.String())
		}
	}
}

func TestHandleModelCardGetNotModified(t *testing.T) {
	// Test the NotModified path: needToUpdate=false and updateCount > 10
	testWriter := testgin.NewTestResponseWriter()
	ctx, _ := gin.CreateTestContext(testWriter)
	ils := &ImportLocationServer{
		content: map[string]*ImportLocation{},
		modelcards: map[string]modelCardMetadata{
			"mymodel": {content: "card-content", needToUpdate: false, updateCount: 11},
		},
	}
	req, _ := http.NewRequest(http.MethodGet, "/modelcard?key=mymodel", nil)
	ctx.Request = req
	ils.handleModelCardGet(ctx)
	common.AssertEqual(t, ctx.Writer.Status(), http.StatusNotModified)
}

func TestHandleModelCardGetUpdateCountTracking(t *testing.T) {
	// Call handleModelCardGet multiple times, verify updateCount increments
	// and needToUpdate transitions from true to false
	ils := &ImportLocationServer{
		content: map[string]*ImportLocation{},
		modelcards: map[string]modelCardMetadata{
			"mymodel": {content: "card-content", needToUpdate: true, updateCount: 0},
		},
	}

	// First call: needToUpdate is true, should return OK and set needToUpdate=false, updateCount=1
	// Subsequent calls: needToUpdate is false but updateCount <= 10, still returns OK
	// After 11 OK calls, updateCount=11 and needToUpdate=false, so next call returns NotModified
	for i := 0; i < 11; i++ {
		testWriter := testgin.NewTestResponseWriter()
		ctx, _ := gin.CreateTestContext(testWriter)
		req, _ := http.NewRequest(http.MethodGet, "/modelcard?key=mymodel", nil)
		ctx.Request = req
		ils.handleModelCardGet(ctx)
		common.AssertEqual(t, ctx.Writer.Status(), http.StatusOK)
	}

	// After 11 calls, needToUpdate should be false and updateCount should be 11
	mcm := ils.modelcards["mymodel"]
	common.AssertEqual(t, false, mcm.needToUpdate)
	common.AssertEqual(t, 11, mcm.updateCount)

	// Next call should return NotModified since needToUpdate=false and updateCount > 10
	testWriter := testgin.NewTestResponseWriter()
	ctx, _ := gin.CreateTestContext(testWriter)
	req, _ := http.NewRequest(http.MethodGet, "/modelcard?key=mymodel", nil)
	ctx.Request = req
	ils.handleModelCardGet(ctx)
	common.AssertEqual(t, ctx.Writer.Status(), http.StatusNotModified)
}

func TestHandleCatalogUpsertPostWithModelCard(t *testing.T) {
	ils := &ImportLocationServer{content: map[string]*ImportLocation{}, modelcards: map[string]modelCardMetadata{}}

	// First upsert with model card data
	testWriter := testgin.NewTestResponseWriter()
	body := rest.PostBody{
		Body:                     []byte("create"),
		ModelCardKey:             "model-key",
		ModelCard:                "# Model Card",
		LastUpdateTimeSinceEpoch: "1000",
	}
	data, err := apijson.Marshal(body)
	common.AssertError(t, err)
	ctx, eng := gin.CreateTestContext(testWriter)
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: "key=mnist_v1"}, Body: io.NopCloser(bytes.NewReader(data))}
	ils.router = eng
	ils.handleCatalogUpsertPost(ctx)
	common.AssertEqual(t, ctx.Writer.Status(), http.StatusCreated)

	// Verify model card metadata was created
	mcm, ok := ils.modelcards["model-key"]
	common.AssertEqual(t, true, ok)
	common.AssertEqual(t, "# Model Card", mcm.content)
	common.AssertEqual(t, "1000", mcm.lastUpdateTimeSinceEpoch)
	common.AssertEqual(t, true, mcm.needToUpdate)
	common.AssertEqual(t, 0, mcm.updateCount)

	// Second upsert with same LastUpdateTimeSinceEpoch - should NOT reset needToUpdate
	// First set needToUpdate to false to simulate it was consumed
	mcm.needToUpdate = false
	mcm.updateCount = 5
	ils.modelcards["model-key"] = mcm

	testWriter = testgin.NewTestResponseWriter()
	data, err = apijson.Marshal(body)
	common.AssertError(t, err)
	ctx, eng = gin.CreateTestContext(testWriter)
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: "key=mnist_v1"}, Body: io.NopCloser(bytes.NewReader(data))}
	ils.router = eng
	ils.handleCatalogUpsertPost(ctx)
	common.AssertEqual(t, ctx.Writer.Status(), http.StatusCreated)

	mcm = ils.modelcards["model-key"]
	common.AssertEqual(t, false, mcm.needToUpdate)
	common.AssertEqual(t, 5, mcm.updateCount)

	// Third upsert with different LastUpdateTimeSinceEpoch - should reset needToUpdate
	body.LastUpdateTimeSinceEpoch = "2000"
	testWriter = testgin.NewTestResponseWriter()
	data, err = apijson.Marshal(body)
	common.AssertError(t, err)
	ctx, eng = gin.CreateTestContext(testWriter)
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: "key=mnist_v1"}, Body: io.NopCloser(bytes.NewReader(data))}
	ils.router = eng
	ils.handleCatalogUpsertPost(ctx)
	common.AssertEqual(t, ctx.Writer.Status(), http.StatusCreated)

	mcm = ils.modelcards["model-key"]
	common.AssertEqual(t, true, mcm.needToUpdate)
	common.AssertEqual(t, 0, mcm.updateCount)
	common.AssertEqual(t, "2000", mcm.lastUpdateTimeSinceEpoch)
}

func TestAddRequestId(t *testing.T) {
	testWriter := testgin.NewTestResponseWriter()
	ctx, _ := gin.CreateTestContext(testWriter)
	ctx.Request, _ = http.NewRequest(http.MethodGet, "/", nil)

	handler := addRequestId()
	handler(ctx)

	val, exists := ctx.Get("requestId")
	common.AssertEqual(t, true, exists)
	reqId, ok := val.(string)
	common.AssertEqual(t, true, ok)
	if len(reqId) == 0 {
		t.Errorf("expected non-empty requestId")
	}
}

func TestHandleCatalogUpsertPostBadBody(t *testing.T) {
	ils := &ImportLocationServer{content: map[string]*ImportLocation{}, modelcards: map[string]modelCardMetadata{}}
	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	// Send invalid JSON body
	ctx.Request = &http.Request{
		URL:    &url.URL{RawQuery: "key=mnist_v1"},
		Body:   io.NopCloser(bytes.NewReader([]byte("not-valid-json"))),
		Header: http.Header{"Content-Type": []string{"application/json"}},
	}
	ils.router = eng
	ils.handleCatalogUpsertPost(ctx)
	common.AssertEqual(t, ctx.Writer.Status(), http.StatusBadRequest)
}

// newFakeStorageServer creates an httptest server that mimics the storage REST API.
// listKeys controls what /list returns, and modelData maps keys to their fetch content.
func newFakeStorageServer(listKeys []string, modelData map[string][]byte, listErr, fetchErr bool) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
		if listErr {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp := struct {
			Keys []string `json:"keys"`
		}{Keys: listKeys}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/fetch", func(w http.ResponseWriter, r *http.Request) {
		if fetchErr {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		key := r.URL.Query().Get("key")
		data, ok := modelData[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})
	return httptest.NewServer(mux)
}

func TestLoadFromStorageSuccess(t *testing.T) {
	modelData := map[string][]byte{
		"mnist_v1": []byte(`{"kind":"model"}`),
	}
	ts := newFakeStorageServer([]string{"mnist_v1"}, modelData, false, false)
	defer ts.Close()

	r := gin.New()
	ils := &ImportLocationServer{
		router:     r,
		content:    map[string]*ImportLocation{},
		modelcards: map[string]modelCardMetadata{},
		storage:    storage.SetupBridgeStorageRESTClient(ts.URL, "test-token"),
		lock:       sync.Mutex{},
	}

	ok, err := ils.loadFromStorage()
	common.AssertError(t, err)
	common.AssertEqual(t, true, ok)
	// Verify content was loaded - key "mnist_v1" produces URI "/mnist/v1/catalog-info.yaml"
	common.AssertEqual(t, 1, len(ils.content))
	il, exists := ils.content["/mnist/v1/catalog-info.yaml"]
	common.AssertEqual(t, true, exists)
	common.AssertNotNil(t, il)
	common.AssertEqual(t, `{"kind":"model"}`, string(il.content))
}

func TestLoadFromStorageListError(t *testing.T) {
	ts := newFakeStorageServer(nil, nil, true, false)
	defer ts.Close()

	r := gin.New()
	ils := &ImportLocationServer{
		router:     r,
		content:    map[string]*ImportLocation{},
		modelcards: map[string]modelCardMetadata{},
		storage:    storage.SetupBridgeStorageRESTClient(ts.URL, "test-token"),
		lock:       sync.Mutex{},
	}

	ok, err := ils.loadFromStorage()
	common.AssertError(t, err)
	common.AssertEqual(t, false, ok)
}

func TestLoadFromStorageListBadRC(t *testing.T) {
	// Return a non-200 status code but valid JSON body, so resty doesn't error
	// but loadFromStorage catches the bad RC
	mux := http.NewServeMux()
	mux.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(struct {
			Keys []string `json:"keys"`
		}{Keys: []string{}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	r := gin.New()
	ils := &ImportLocationServer{
		router:     r,
		content:    map[string]*ImportLocation{},
		modelcards: map[string]modelCardMetadata{},
		storage:    storage.SetupBridgeStorageRESTClient(ts.URL, "test-token"),
		lock:       sync.Mutex{},
	}

	ok, err := ils.loadFromStorage()
	common.AssertError(t, err)
	common.AssertEqual(t, false, ok)
}

func TestLoadFromStorageFetchRealError(t *testing.T) {
	// FetchModel returns an error from resty (connection error on fetch)
	mux := http.NewServeMux()
	mux.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Keys []string `json:"keys"`
		}{Keys: []string{"mnist_v1"}})
	})
	mux.HandleFunc("/fetch", func(w http.ResponseWriter, r *http.Request) {
		// Close the connection without sending a response to trigger a resty error
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	r := gin.New()
	ils := &ImportLocationServer{
		router:     r,
		content:    map[string]*ImportLocation{},
		modelcards: map[string]modelCardMetadata{},
		storage:    storage.SetupBridgeStorageRESTClient(ts.URL, "test-token"),
		lock:       sync.Mutex{},
	}

	ok, err := ils.loadFromStorage()
	common.AssertError(t, err)
	common.AssertEqual(t, false, ok)
}

func TestLoadFromStorageFetchError(t *testing.T) {
	ts := newFakeStorageServer([]string{"mnist_v1"}, nil, false, true)
	defer ts.Close()

	r := gin.New()
	ils := &ImportLocationServer{
		router:     r,
		content:    map[string]*ImportLocation{},
		modelcards: map[string]modelCardMetadata{},
		storage:    storage.SetupBridgeStorageRESTClient(ts.URL, "test-token"),
		lock:       sync.Mutex{},
	}

	ok, err := ils.loadFromStorage()
	common.AssertError(t, err)
	common.AssertEqual(t, false, ok)
}

func TestLoadFromStorageBadKey(t *testing.T) {
	// Key without underscore should be skipped but continue processing
	modelData := map[string][]byte{
		"mnist_v1": []byte(`{"kind":"model"}`),
	}
	ts := newFakeStorageServer([]string{"badkey", "mnist_v1"}, modelData, false, false)
	defer ts.Close()

	r := gin.New()
	ils := &ImportLocationServer{
		router:     r,
		content:    map[string]*ImportLocation{},
		modelcards: map[string]modelCardMetadata{},
		storage:    storage.SetupBridgeStorageRESTClient(ts.URL, "test-token"),
		lock:       sync.Mutex{},
	}

	ok, err := ils.loadFromStorage()
	common.AssertError(t, err)
	common.AssertEqual(t, true, ok)
	common.AssertEqual(t, 1, len(ils.content))
}

func TestRunStopsOnSignal(t *testing.T) {
	r := gin.New()
	// Use an invalid port to make router.Run fail immediately with an error,
	// which covers the error handling path inside the goroutine
	ils := &ImportLocationServer{
		router:     r,
		content:    map[string]*ImportLocation{},
		modelcards: map[string]modelCardMetadata{},
		port:       "invalid-port",
		lock:       sync.Mutex{},
	}

	stopCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		ils.Run(stopCh)
		close(done)
	}()

	// Give goroutine time to attempt router.Run and hit the error path
	time.Sleep(100 * time.Millisecond)
	close(stopCh)
	<-done
}

func TestHandleCatalogDiscoveryGetNilContentMap(t *testing.T) {
	// Test with nil content map (no entries at all)
	testWriter := testgin.NewTestResponseWriter()
	ctx, _ := gin.CreateTestContext(testWriter)
	ils := &ImportLocationServer{content: nil, modelcards: map[string]modelCardMetadata{}}

	ils.handleCatalogDiscoveryGet(ctx)

	common.AssertEqual(t, ctx.Writer.Status(), http.StatusOK)
	bodyBuf := testWriter.ResponseWriter.Body
	common.AssertNotNil(t, bodyBuf)
	common.AssertEqual(t, `{"uris":null}`, bodyBuf.String())
}

func TestLoadFromStorageEmptyKeys(t *testing.T) {
	ts := newFakeStorageServer([]string{}, nil, false, false)
	defer ts.Close()

	r := gin.New()
	ils := &ImportLocationServer{
		router:     r,
		content:    map[string]*ImportLocation{},
		modelcards: map[string]modelCardMetadata{},
		storage:    storage.SetupBridgeStorageRESTClient(ts.URL, "test-token"),
		lock:       sync.Mutex{},
	}

	ok, err := ils.loadFromStorage()
	common.AssertError(t, err)
	common.AssertEqual(t, true, ok)
	common.AssertEqual(t, 0, len(ils.content))
}

func TestLoadFromStorageConnectionError(t *testing.T) {
	// Use a URL that will cause a connection error
	r := gin.New()
	ils := &ImportLocationServer{
		router:     r,
		content:    map[string]*ImportLocation{},
		modelcards: map[string]modelCardMetadata{},
		storage:    storage.SetupBridgeStorageRESTClient("http://localhost:1", "test-token"),
		lock:       sync.Mutex{},
	}

	ok, err := ils.loadFromStorage()
	// Connection error results in err != nil from resty, but loadFromStorage catches it
	// and returns false, nil
	common.AssertError(t, err)
	common.AssertEqual(t, false, ok)
}

func TestNewImportLocationServer(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	ils := NewImportLocationServer("http://localhost:1", "0", types.CatalogInfoYamlFormat)
	common.AssertNotNil(t, ils)
	common.AssertNotNil(t, ils.router)
	common.AssertEqual(t, 0, len(ils.content))
	common.AssertEqual(t, 0, len(ils.modelcards))
	common.AssertEqual(t, "0", ils.port)

	// Test the inline /:model/:version/:format route handler via httptest
	// Test not found case
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/mnist/v1/catalog-info.yaml", nil)
	ils.router.ServeHTTP(w, req)
	common.AssertEqual(t, http.StatusNotFound, w.Code)

	// Add content and test found case
	ils.lock.Lock()
	ils.content["/mnist/v1/catalog-info.yaml"] = &ImportLocation{content: []byte("found-data")}
	ils.lock.Unlock()
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/mnist/v1/catalog-info.yaml", nil)
	ils.router.ServeHTTP(w, req)
	common.AssertEqual(t, http.StatusOK, w.Code)
	common.AssertEqual(t, "found-data", w.Body.String())
}

// Suppress unused import errors
var _ = fmt.Sprintf

func TestHandleCatalogDelete(t *testing.T) {
	for _, tc := range []struct {
		name            string
		reqURL          url.URL
		existingContent map[string]*ImportLocation
		expectedErrMsg  string
		expectedSC      int
		expectedContent map[string]*ImportLocation
	}{
		{
			name:           "no query param",
			expectedSC:     http.StatusBadRequest,
			expectedErrMsg: "need a 'key' parameter",
		},
		{
			name:           "bad query param",
			reqURL:         url.URL{RawQuery: "key=mnist"},
			expectedSC:     http.StatusBadRequest,
			expectedErrMsg: "bad key format",
		},
		{
			name:   "entry does not exist",
			reqURL: url.URL{RawQuery: "key=mnist_v2"},
			existingContent: map[string]*ImportLocation{
				"/mnist/v1/catalog-info.yaml": {content: []byte("create")},
			},
			expectedSC: http.StatusOK,
			expectedContent: map[string]*ImportLocation{
				"/mnist/v1/catalog-info.yaml": {content: []byte("create")},
			},
		},
		{
			name:   "entry exists",
			reqURL: url.URL{RawQuery: "key=mnist_v2"},
			existingContent: map[string]*ImportLocation{
				"/mnist/v1/catalog-info.yaml": {content: []byte("create")},
				"/mnist/v2/catalog-info.yaml": {content: []byte("create")},
			},
			expectedSC: http.StatusOK,
			expectedContent: map[string]*ImportLocation{
				"/mnist/v1/catalog-info.yaml": {content: []byte("create")},
				"/mnist/v2/catalog-info.yaml": {content: nil},
			},
		},
	} {
		testWriter := testgin.NewTestResponseWriter()

		ctx, eng := gin.CreateTestContext(testWriter)
		ctx.Request = &http.Request{URL: &tc.reqURL}
		ils := &ImportLocationServer{content: tc.existingContent, modelcards: map[string]modelCardMetadata{}}
		ils.router = eng

		ils.handleCatalogDelete(ctx)

		common.AssertEqual(t, ctx.Writer.Status(), tc.expectedSC)
		if len(tc.expectedErrMsg) > 0 {
			errors := ctx.Errors
			found := false
			for _, e := range errors {
				if strings.Contains(e.Error(), tc.expectedErrMsg) {
					found = true
					break
				}
			}
			common.AssertEqual(t, true, found)
		}

		common.AssertEqual(t, len(ils.content), len(tc.expectedContent))
		for key, val := range tc.expectedContent {
			v, ok := ils.content[key]
			common.AssertEqual(t, ok, true)
			common.AssertEqual(t, v, val)
		}
	}
}
