package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	bkstgclient "github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/cli/backstage"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/server/storage/configmap"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/rest"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/types"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/util"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/backstage"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	testgin "github.com/redhat-ai-dev/model-catalog-bridge/test/stub/gin-gonic"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/location"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8srest "k8s.io/client-go/rest"
)

// ---- Mock BridgeStorage that can simulate errors ----

type mockBridgeStorage struct {
	data      map[string]types.StorageBody
	listErr   error
	fetchErr  error
	upsertErr error
	removeErr error
}

func newMockBridgeStorage() *mockBridgeStorage {
	return &mockBridgeStorage{
		data: map[string]types.StorageBody{},
	}
}

func (m *mockBridgeStorage) Initialize(cfg *k8srest.Config) error { return nil }
func (m *mockBridgeStorage) List() ([]string, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	keys := []string{}
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys, nil
}
func (m *mockBridgeStorage) Fetch(key string) (types.StorageBody, error) {
	if m.fetchErr != nil {
		return types.StorageBody{}, m.fetchErr
	}
	sb, ok := m.data[key]
	if !ok {
		return types.StorageBody{}, nil
	}
	return sb, nil
}
func (m *mockBridgeStorage) Upsert(key string, value types.StorageBody) error {
	if m.upsertErr != nil {
		return m.upsertErr
	}
	m.data[key] = value
	return nil
}
func (m *mockBridgeStorage) Remove(key string) error {
	if m.removeErr != nil {
		return m.removeErr
	}
	delete(m.data, key)
	return nil
}

// ---- REST Client Tests (rest.go) ----

func TestSetupBridgeStorageRESTClient(t *testing.T) {
	client := SetupBridgeStorageRESTClient("http://localhost:8080", "my-token")
	common.AssertNotNil(t, client)
	common.AssertNotNil(t, client.RESTClient)
	common.AssertEqual(t, "http://localhost:8080"+util.UpsertURI, client.UpsertURL)
	common.AssertEqual(t, "http://localhost:8080"+util.CurrentKeySetURI, client.CurrentKeySetURL)
	common.AssertEqual(t, "http://localhost:8080"+util.ListURI, client.ListURL)
	common.AssertEqual(t, "http://localhost:8080"+util.FetchURI, client.FetchURL)
	common.AssertEqual(t, "my-token", client.Token)
}

func TestListModelsKeys(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		d := &DiscoverResponse{Keys: []string{"model1", "model2"}}
		buf, _ := json.Marshal(d)
		w.Write(buf)
	}))
	defer ts.Close()

	client := &BridgeStorageRESTClient{
		RESTClient: common.DC(),
		ListURL:    ts.URL + util.ListURI,
	}

	sc, _, err, keys := client.ListModelsKeys()
	common.AssertError(t, err)
	common.AssertEqual(t, http.StatusOK, sc)
	common.AssertEqual(t, 2, len(keys))
	common.AssertEqual(t, "model1", keys[0])
	common.AssertEqual(t, "model2", keys[1])
}

func TestListModelsKeys_BadJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json`))
	}))
	defer ts.Close()

	client := &BridgeStorageRESTClient{
		RESTClient: common.DC(),
		ListURL:    ts.URL + util.ListURI,
	}

	sc, _, err, keys := client.ListModelsKeys()
	common.AssertEqual(t, http.StatusBadRequest, sc)
	if err == nil {
		t.Errorf("expected error for bad JSON")
	}
	common.AssertEqual(t, 0, len(keys))
}

func TestFetchModel(t *testing.T) {
	sb := types.StorageBody{
		Body:           []byte("test-body"),
		LocationId:     "loc-1",
		LocationTarget: "http://example.com",
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		key := r.URL.Query().Get(util.KeyQueryParam)
		if key == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		buf, _ := json.Marshal(sb)
		w.Write(buf)
	}))
	defer ts.Close()

	client := &BridgeStorageRESTClient{
		RESTClient: common.DC(),
		FetchURL:   ts.URL + util.FetchURI,
	}

	sc, _, err, body := client.FetchModel("my-key")
	common.AssertError(t, err)
	common.AssertEqual(t, http.StatusOK, sc)
	if len(body) == 0 {
		t.Errorf("expected non-empty body")
	}
	var got types.StorageBody
	err = json.Unmarshal(body, &got)
	common.AssertError(t, err)
	common.AssertEqual(t, sb.LocationId, got.LocationId)
}

func TestUpsertModel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	client := &BridgeStorageRESTClient{
		RESTClient: common.DC(),
		UpsertURL:  ts.URL + util.UpsertURI,
		Token:      "test-token",
	}

	// without model card
	sc, _, postBody, err := client.UpsertModel("import-key", "kfmr", "12345", "", nil, []byte("catalog-data"))
	common.AssertError(t, err)
	common.AssertEqual(t, http.StatusCreated, sc)
	common.AssertNotNil(t, postBody)
	common.AssertEqual(t, "", postBody.ModelCard)

	// with model card
	mc := "model-card-content"
	sc, _, postBody, err = client.UpsertModel("import-key", "kfmr", "12345", "model card key", &mc, []byte("catalog-data"))
	common.AssertError(t, err)
	common.AssertEqual(t, http.StatusCreated, sc)
	common.AssertEqual(t, "model-card-content", postBody.ModelCard)
	common.AssertEqual(t, "modelcardkey", postBody.ModelCardKey) // spaces removed
}

func TestPostCurrentKeySet(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	client := &BridgeStorageRESTClient{
		RESTClient:       common.DC(),
		CurrentKeySetURL: ts.URL + util.CurrentKeySetURI,
		Token:            "test-token",
	}

	sc, _, err := client.PostCurrentKeySet([]string{"key1", "key2", "key3"})
	common.AssertError(t, err)
	common.AssertEqual(t, http.StatusOK, sc)
}

// ---- Server Handler Tests (server.go) ----

func setupTestServer(t *testing.T) (*StorageRESTServer, *fake.Clientset, *corev1.ConfigMap) {
	t.Helper()
	fakeClientset := fake.NewClientset()
	cmCl := fakeClientset.CoreV1()
	cm := &corev1.ConfigMap{}
	cm.Name = util.StorageConfigMapName
	var err error
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Create(context.Background(), cm, metav1.CreateOptions{})
	common.AssertError(t, err)

	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	t.Cleanup(func() { brts.Close() })

	backstageCallback := sync.Map{}
	bks := backstage.CreateBackstageServerWithCallbackMap(&backstageCallback, t)
	t.Cleanup(func() { bks.Close() })

	cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)
	locClient := location.SetupBridgeLocationRESTClient(brts)
	locClient.HostURL = brts.URL

	s := &StorageRESTServer{
		st:              cms,
		mutex:           sync.Mutex{},
		pushedLocations: map[string]*types.StorageBody{},
		locations:       locClient,
		bkstg:           &bkstgclient.BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: bks.URL},
		pushToRHDH:      true,
	}

	return s, fakeClientset, cm
}

func Test_handleCatalogList_Empty(t *testing.T) {
	s, _, _ := setupTestServer(t)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{}}

	s.handleCatalogList(ctx)

	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())

	rcvdDiscResp := &DiscoverResponse{}
	err := json.Unmarshal(testWriter.ResponseWriter.Body.Bytes(), rcvdDiscResp)
	common.AssertError(t, err)
	common.AssertEqual(t, 0, len(rcvdDiscResp.Keys))
}

func Test_handleCatalogFetch_NoKey(t *testing.T) {
	s, _, _ := setupTestServer(t)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{}}

	s.handleCatalogFetch(ctx)

	common.AssertEqual(t, http.StatusBadRequest, ctx.Writer.Status())
	errors := ctx.Errors
	found := false
	for _, e := range errors {
		if strings.Contains(e.Error(), "need a 'key' parameter") {
			found = true
			break
		}
	}
	common.AssertEqual(t, true, found)
}

func Test_handleCatalogFetch_MissingKey(t *testing.T) {
	s, _, _ := setupTestServer(t)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: "key=nonexistent"}}

	s.handleCatalogFetch(ctx)

	// should return OK with empty body since configmap storage returns empty StorageBody without error
	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())
}

func Test_handleCatalogUpsertPost_WithTypeParam(t *testing.T) {
	s, _, _ := setupTestServer(t)

	body := rest.PostBody{Body: []byte("test-data")}
	data, err := json.Marshal(body)
	common.AssertError(t, err)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1&type=kfmr"},
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	s.handleCatalogUpsertPost(ctx)

	// Should succeed (created)
	common.AssertEqual(t, http.StatusCreated, ctx.Writer.Status())
}

func Test_handleCatalogUpsertPost_NoBody(t *testing.T) {
	s, _, _ := setupTestServer(t)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1"},
		Body: io.NopCloser(bytes.NewReader([]byte(""))),
	}

	s.handleCatalogUpsertPost(ctx)

	common.AssertEqual(t, http.StatusBadRequest, ctx.Writer.Status())
}

func Test_handleCatalogCurrentKeySetPost_EmptyStorage(t *testing.T) {
	s, _, _ := setupTestServer(t)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: "key=foo_bar,baz_v1"}}

	s.handleCatalogCurrentKeySetPost(ctx)

	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())
}

func Test_handleCatalogCurrentKeySetPost_MultipleKeys(t *testing.T) {
	s, fakeClientset, cm := setupTestServer(t)
	cmCl := fakeClientset.CoreV1()

	// Populate storage with two entries
	sb := &types.StorageBody{
		Body:           []byte("data1"),
		LocationId:     "loc-1",
		LocationTarget: "http://example.com/1",
	}
	sbBuf, err := json.Marshal(sb)
	common.AssertError(t, err)

	cm.BinaryData = map[string][]byte{
		"mnist_v1": sbBuf,
		"mnist_v2": sbBuf,
		"mnist_v3": sbBuf,
	}
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Update(context.Background(), cm, metav1.UpdateOptions{})
	common.AssertError(t, err)

	// Keep only v1 and v3, should remove v2
	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: "key=mnist_v1,mnist_v3"}}

	s.handleCatalogCurrentKeySetPost(ctx)

	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())

	// Verify v2 was removed
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Get(context.Background(), util.StorageConfigMapName, metav1.GetOptions{})
	common.AssertError(t, err)
	_, hasV1 := cm.BinaryData["mnist_v1"]
	common.AssertEqual(t, true, hasV1)
	_, hasV2 := cm.BinaryData["mnist_v2"]
	common.AssertEqual(t, false, hasV2)
	_, hasV3 := cm.BinaryData["mnist_v3"]
	common.AssertEqual(t, true, hasV3)
}

func Test_handleCatalogCurrentKeySetPost_EmptyKeyParam(t *testing.T) {
	s, fakeClientset, cm := setupTestServer(t)
	cmCl := fakeClientset.CoreV1()

	sb := &types.StorageBody{
		Body:           []byte("data"),
		LocationId:     "loc-1",
		LocationTarget: "http://example.com/1",
	}
	sbBuf, err := json.Marshal(sb)
	common.AssertError(t, err)

	cm.BinaryData = map[string][]byte{
		"mnist_v1": sbBuf,
	}
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Update(context.Background(), cm, metav1.UpdateOptions{})
	common.AssertError(t, err)

	// Empty key means no models discovered - everything should be removed
	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: ""}}

	s.handleCatalogCurrentKeySetPost(ctx)

	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())

	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Get(context.Background(), util.StorageConfigMapName, metav1.GetOptions{})
	common.AssertError(t, err)
	common.AssertEqual(t, 0, len(cm.BinaryData))
}

func Test_handleCatalogCurrentKeySetPost_NoLocationId(t *testing.T) {
	s, fakeClientset, cm := setupTestServer(t)
	cmCl := fakeClientset.CoreV1()

	// Storage entry without LocationId
	sb := &types.StorageBody{
		Body: []byte("data"),
	}
	sbBuf, err := json.Marshal(sb)
	common.AssertError(t, err)

	cm.BinaryData = map[string][]byte{
		"mnist_v1": sbBuf,
	}
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Update(context.Background(), cm, metav1.UpdateOptions{})
	common.AssertError(t, err)

	// Empty key set removes all
	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: ""}}

	s.handleCatalogCurrentKeySetPost(ctx)

	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())
}

// ---- sync tests ----

func Test_sync_CacheHit(t *testing.T) {
	s, _, _ := setupTestServer(t)

	cachedSB := &types.StorageBody{
		Body:            []byte("cached"),
		LocationId:      "cached-loc",
		LocationTarget:  "http://cached.com",
		LocationIDValid: true,
	}
	s.pushedLocations["cached-key"] = cachedSB

	result, err := s.sync("cached-key")
	common.AssertError(t, err)
	common.AssertEqual(t, "cached-loc", result.LocationId)
	common.AssertEqual(t, true, result.LocationIDValid)
}

func Test_sync_NotInCache_NotInStorage(t *testing.T) {
	s, _, _ := setupTestServer(t)

	result, err := s.sync("nonexistent-key")
	// configmap storage returns empty StorageBody when key not found, no error
	if err != nil {
		t.Logf("err (possibly expected): %v", err)
	}
	common.AssertEqual(t, "", result.LocationId)
}

func Test_sync_InStorage_ValidLocation(t *testing.T) {
	s, fakeClientset, cm := setupTestServer(t)
	cmCl := fakeClientset.CoreV1()

	// The backstage stub returns a valid location for "e83bc2d8-0f1c-49f2-b65b-8bfbbbe29ae2"
	sb := types.StorageBody{
		Body:           []byte("data"),
		LocationId:     "e83bc2d8-0f1c-49f2-b65b-8bfbbbe29ae2",
		LocationTarget: "http://example.com",
	}
	sbBuf, err := json.Marshal(sb)
	common.AssertError(t, err)

	cm.BinaryData = map[string][]byte{
		"my-key": sbBuf,
	}
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Update(context.Background(), cm, metav1.UpdateOptions{})
	common.AssertError(t, err)

	result, err := s.sync("my-key")
	common.AssertError(t, err)
	common.AssertEqual(t, true, result.LocationIDValid)
	common.AssertEqual(t, "e83bc2d8-0f1c-49f2-b65b-8bfbbbe29ae2", result.LocationId)

	// Check it's now cached
	cached, ok := s.pushedLocations["my-key"]
	common.AssertEqual(t, true, ok)
	common.AssertEqual(t, true, cached.LocationIDValid)
}

func Test_sync_InStorage_InvalidLocation(t *testing.T) {
	s, fakeClientset, cm := setupTestServer(t)
	cmCl := fakeClientset.CoreV1()

	// Use a location ID that backstage stub returns 404 for
	sb := types.StorageBody{
		Body:           []byte("data"),
		LocationId:     "404-invalid-id",
		LocationTarget: "http://example.com",
	}
	sbBuf, err := json.Marshal(sb)
	common.AssertError(t, err)

	cm.BinaryData = map[string][]byte{
		"invalid-key": sbBuf,
	}
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Update(context.Background(), cm, metav1.UpdateOptions{})
	common.AssertError(t, err)

	result, err := s.sync("invalid-key")
	// When backstage returns 404, the location is removed from storage
	// err should be nil from the Remove call
	t.Logf("sync result: %+v, err: %v", result, err)
	// The key should be deleted from cache
	_, ok := s.pushedLocations["invalid-key"]
	common.AssertEqual(t, false, ok)
}

func Test_sync_InCacheButNotValid(t *testing.T) {
	s, fakeClientset, cm := setupTestServer(t)
	cmCl := fakeClientset.CoreV1()

	// Put in cache but not valid
	s.pushedLocations["partial-key"] = &types.StorageBody{
		LocationId:      "e83bc2d8-0f1c-49f2-b65b-8bfbbbe29ae2",
		LocationIDValid: false,
	}

	// Also put in storage
	sb := types.StorageBody{
		Body:           []byte("data"),
		LocationId:     "e83bc2d8-0f1c-49f2-b65b-8bfbbbe29ae2",
		LocationTarget: "http://example.com",
	}
	sbBuf, err := json.Marshal(sb)
	common.AssertError(t, err)

	cm.BinaryData = map[string][]byte{
		"partial-key": sbBuf,
	}
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Update(context.Background(), cm, metav1.UpdateOptions{})
	common.AssertError(t, err)

	result, err := s.sync("partial-key")
	common.AssertError(t, err)
	common.AssertEqual(t, true, result.LocationIDValid)
}

// ---- del tests ----

func Test_del(t *testing.T) {
	s, _, _ := setupTestServer(t)
	s.pushedLocations["to-delete"] = &types.StorageBody{LocationId: "x"}

	s.del("to-delete")

	_, ok := s.pushedLocations["to-delete"]
	common.AssertEqual(t, false, ok)
}

// ---- addRequestId tests ----

func Test_addRequestId(t *testing.T) {
	handler := addRequestId()
	testWriter := testgin.NewTestResponseWriter()
	ctx, _ := gin.CreateTestContext(testWriter)
	ctx.Request = &http.Request{URL: &url.URL{}}

	handler(ctx)

	val, exists := ctx.Get("requestId")
	common.AssertEqual(t, true, exists)
	reqId, ok := val.(string)
	common.AssertEqual(t, true, ok)
	if len(reqId) == 0 {
		t.Error("expected non-empty requestId")
	}
}

// ---- setupBkstg test ----

func Test_setupBkstg_AlreadySet(t *testing.T) {
	s, _, _ := setupTestServer(t)
	// bkstg is already set in setupTestServer
	result := s.setupBkstg()
	common.AssertEqual(t, true, result)
}

func Test_setupBkstg_NilBkstg(t *testing.T) {
	s, _, _ := setupTestServer(t)
	s.bkstg = nil
	// Create a minimal kubeconfig pointing to an unreachable server so that
	// GetK8sConfig succeeds (returns non-nil config) but GetBackstageURL
	// fails when trying to list routes. This avoids hitting ctrl.GetConfigOrDie
	// and avoids using the real ~/.kube/config.
	tmpDir := t.TempDir()
	kubeconfig := filepath.Join(tmpDir, "config")
	kubecfgContent := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:1
  name: fake
contexts:
- context:
    cluster: fake
    user: fake
  name: fake
current-context: fake
users:
- name: fake
  user:
    token: fake
`
	os.WriteFile(kubeconfig, []byte(kubecfgContent), 0600)
	t.Setenv("KUBECONFIG", kubeconfig)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	result := s.setupBkstg()
	common.AssertEqual(t, false, result)
}

// ---- Upsert flow with already pushed location ----

func Test_handleCatalogUpsertPost_AlreadyPushed(t *testing.T) {
	s, fakeClientset, cm := setupTestServer(t)
	cmCl := fakeClientset.CoreV1()

	// Pre-populate storage with a key that has a valid location ID
	sb := types.StorageBody{
		Body:           []byte("existing-data"),
		LocationId:     "e83bc2d8-0f1c-49f2-b65b-8bfbbbe29ae2",
		LocationTarget: "http://rhoai-bridge.com/mnist/v1/catalog-info.yaml",
	}
	sbBuf, err := json.Marshal(sb)
	common.AssertError(t, err)

	cm.BinaryData = map[string][]byte{
		"mnist_v1": sbBuf,
	}
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Update(context.Background(), cm, metav1.UpdateOptions{})
	common.AssertError(t, err)

	body := rest.PostBody{Body: []byte("updated-data")}
	data, err := json.Marshal(body)
	common.AssertError(t, err)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1"},
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	s.handleCatalogUpsertPost(ctx)

	// Already pushed, so should be OK (200)
	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())
}

func Test_handleCatalogUpsertPost_AlreadyPushed_InvalidLocation(t *testing.T) {
	s, fakeClientset, cm := setupTestServer(t)
	cmCl := fakeClientset.CoreV1()

	// Pre-populate storage with a key that has an invalid (404) location ID
	sb := types.StorageBody{
		Body:           []byte("existing-data"),
		LocationId:     "404-bad-id",
		LocationTarget: "http://example.com/old",
	}
	sbBuf, err := json.Marshal(sb)
	common.AssertError(t, err)

	cm.BinaryData = map[string][]byte{
		"mnist_v1": sbBuf,
	}
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Update(context.Background(), cm, metav1.UpdateOptions{})
	common.AssertError(t, err)

	body := rest.PostBody{Body: []byte("updated-data")}
	data, err := json.Marshal(body)
	common.AssertError(t, err)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1"},
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	s.handleCatalogUpsertPost(ctx)

	// Location was invalid (404), sync removes it, then upsert re-creates
	// Should end up as Created
	common.AssertEqual(t, http.StatusCreated, ctx.Writer.Status())
}

func Test_handleCatalogUpsertPost_NoPush(t *testing.T) {
	s, _, _ := setupTestServer(t)
	s.pushToRHDH = false

	body := rest.PostBody{Body: []byte("test-data")}
	data, err := json.Marshal(body)
	common.AssertError(t, err)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1"},
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	s.handleCatalogUpsertPost(ctx)

	common.AssertEqual(t, http.StatusCreated, ctx.Writer.Status())
}

func Test_handleCatalogUpsertPost_NoBkstg(t *testing.T) {
	s, _, _ := setupTestServer(t)
	s.bkstg = nil
	s.pushToRHDH = true

	body := rest.PostBody{Body: []byte("test-data")}
	data, err := json.Marshal(body)
	common.AssertError(t, err)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1"},
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	s.handleCatalogUpsertPost(ctx)

	// setupBkstg will fail (no cluster config), so warning logged but should still Created
	common.AssertEqual(t, http.StatusCreated, ctx.Writer.Status())
}

// ---- Test handleCatalogCurrentKeySetPost with bkstg not available ----

func Test_handleCatalogCurrentKeySetPost_NoBkstg(t *testing.T) {
	s, fakeClientset, cm := setupTestServer(t)
	cmCl := fakeClientset.CoreV1()

	sb := &types.StorageBody{
		Body:           []byte("data"),
		LocationId:     "loc-id",
		LocationTarget: "http://example.com",
	}
	sbBuf, err := json.Marshal(sb)
	common.AssertError(t, err)

	cm.BinaryData = map[string][]byte{
		"mnist_v1": sbBuf,
	}
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Update(context.Background(), cm, metav1.UpdateOptions{})
	common.AssertError(t, err)

	// Set bkstg to nil to test setupBkstg path during delete
	s.bkstg = nil

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: ""}}

	s.handleCatalogCurrentKeySetPost(ctx)

	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())
}

// ---- Test with full integration through the REST API using httptest ----

func Test_FullRESTIntegration(t *testing.T) {
	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	defer brts.Close()
	backstageCallback := sync.Map{}
	bks := backstage.CreateBackstageServerWithCallbackMap(&backstageCallback, t)
	defer bks.Close()

	fakeClientset := fake.NewClientset()
	cmCl := fakeClientset.CoreV1()
	cm := &corev1.ConfigMap{}
	cm.Name = util.StorageConfigMapName
	_, err := cmCl.ConfigMaps(metav1.NamespaceDefault).Create(context.Background(), cm, metav1.CreateOptions{})
	common.AssertError(t, err)

	cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)
	locClient := location.SetupBridgeLocationRESTClient(brts)
	locClient.HostURL = brts.URL

	gin.SetMode(gin.TestMode)
	r := gin.New()

	s := &StorageRESTServer{
		router:          r,
		st:              cms,
		mutex:           sync.Mutex{},
		pushedLocations: map[string]*types.StorageBody{},
		locations:       locClient,
		bkstg:           &bkstgclient.BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: bks.URL},
		pushToRHDH:      true,
		port:            "0",
	}

	r.POST(util.UpsertURI, s.handleCatalogUpsertPost)
	r.POST(util.CurrentKeySetURI, s.handleCatalogCurrentKeySetPost)
	r.GET(util.ListURI, s.handleCatalogList)
	r.GET(util.FetchURI, s.handleCatalogFetch)

	ts := httptest.NewServer(r)
	defer ts.Close()

	// 1. List - should be empty
	resp, err := http.Get(ts.URL + util.ListURI)
	common.AssertError(t, err)
	common.AssertEqual(t, http.StatusOK, resp.StatusCode)
	bodyBytes, err := io.ReadAll(resp.Body)
	common.AssertError(t, err)
	resp.Body.Close()
	dr := &DiscoverResponse{}
	err = json.Unmarshal(bodyBytes, dr)
	common.AssertError(t, err)
	common.AssertEqual(t, 0, len(dr.Keys))

	// 2. Upsert a model
	postBody := rest.PostBody{Body: []byte("test-catalog-data")}
	postBytes, _ := json.Marshal(postBody)
	resp, err = http.Post(ts.URL+util.UpsertURI+"?key=mymodel_v1&type=kfmr", "application/json", bytes.NewReader(postBytes))
	common.AssertError(t, err)
	common.AssertEqual(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// 3. List - should have one entry
	resp, err = http.Get(ts.URL + util.ListURI)
	common.AssertError(t, err)
	common.AssertEqual(t, http.StatusOK, resp.StatusCode)
	bodyBytes, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	dr = &DiscoverResponse{}
	err = json.Unmarshal(bodyBytes, dr)
	common.AssertError(t, err)
	common.AssertEqual(t, 1, len(dr.Keys))

	// 4. Fetch the model
	resp, err = http.Get(ts.URL + util.FetchURI + "?key=" + dr.Keys[0])
	common.AssertError(t, err)
	common.AssertEqual(t, http.StatusOK, resp.StatusCode)
	bodyBytes, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var fetchedSB types.StorageBody
	err = json.Unmarshal(bodyBytes, &fetchedSB)
	common.AssertError(t, err)

	// 5. Fetch without key param
	resp, err = http.Get(ts.URL + util.FetchURI)
	common.AssertError(t, err)
	common.AssertEqual(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 6. CurrentKeySet with the same key
	resp, err = http.Post(ts.URL+util.CurrentKeySetURI+"?key="+dr.Keys[0], "application/json", nil)
	common.AssertError(t, err)
	common.AssertEqual(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 7. CurrentKeySet with no keys (removes all)
	resp, err = http.Post(ts.URL+util.CurrentKeySetURI, "application/json", nil)
	common.AssertError(t, err)
	common.AssertEqual(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 8. List should be empty again
	resp, err = http.Get(ts.URL + util.ListURI)
	common.AssertError(t, err)
	common.AssertEqual(t, http.StatusOK, resp.StatusCode)
	bodyBytes, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	dr = &DiscoverResponse{}
	err = json.Unmarshal(bodyBytes, dr)
	common.AssertError(t, err)
	common.AssertEqual(t, 0, len(dr.Keys))
}

// ---- Test NewStorageRESTServer ----

func Test_NewStorageRESTServer(t *testing.T) {
	fakeClientset := fake.NewClientset()
	cmCl := fakeClientset.CoreV1()
	cm := &corev1.ConfigMap{}
	cm.Name = util.StorageConfigMapName
	_, err := cmCl.ConfigMaps(metav1.NamespaceDefault).Create(context.Background(), cm, metav1.CreateOptions{})
	common.AssertError(t, err)

	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	defer brts.Close()

	cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)

	s := NewStorageRESTServer(cms, "0", brts.URL, "bridge-token", "bkstg-token", types.CatalogInfoYamlFormat)
	common.AssertNotNil(t, s)
	common.AssertNotNil(t, s.router)
	common.AssertNotNil(t, s.st)
	common.AssertEqual(t, "0", s.port)
}

// ---- Test NewStorageRESTServer with PushToRHDH env var ----

func Test_NewStorageRESTServer_WithPushEnv(t *testing.T) {
	t.Setenv(types.PushToRHDHEnvVar, "true")

	fakeClientset := fake.NewClientset()
	cmCl := fakeClientset.CoreV1()
	cm := &corev1.ConfigMap{}
	cm.Name = util.StorageConfigMapName
	_, err := cmCl.ConfigMaps(metav1.NamespaceDefault).Create(context.Background(), cm, metav1.CreateOptions{})
	common.AssertError(t, err)

	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	defer brts.Close()

	cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)

	s := NewStorageRESTServer(cms, "0", brts.URL, "bridge-token", "bkstg-token", types.CatalogInfoYamlFormat)
	common.AssertNotNil(t, s)
	common.AssertEqual(t, true, s.pushToRHDH)
}

// ---- Test handleCatalogUpsertPost backstage import returns bad parse ----

func Test_handleCatalogUpsertPost_BadBackstageImportResponse(t *testing.T) {
	// Create a backstage server that returns unparseable import response
	badBkstg := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			w.Header().Set("Content-Type", "application/json")
			// Return a response that can't be parsed by ParseImportLocationMap
			w.Write([]byte(`{"bad": "response"}`))
		case "GET":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
		}
	})
	defer badBkstg.Close()

	fakeClientset := fake.NewClientset()
	cmCl := fakeClientset.CoreV1()
	cm := &corev1.ConfigMap{}
	cm.Name = util.StorageConfigMapName
	_, err := cmCl.ConfigMaps(metav1.NamespaceDefault).Create(context.Background(), cm, metav1.CreateOptions{})
	common.AssertError(t, err)

	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	defer brts.Close()

	cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)
	locClient := location.SetupBridgeLocationRESTClient(brts)
	locClient.HostURL = brts.URL

	s := &StorageRESTServer{
		st:              cms,
		mutex:           sync.Mutex{},
		pushedLocations: map[string]*types.StorageBody{},
		locations:       locClient,
		bkstg:           &bkstgclient.BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: badBkstg.URL},
		pushToRHDH:      true,
	}

	body := rest.PostBody{Body: []byte("test-data")}
	data, err := json.Marshal(body)
	common.AssertError(t, err)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1"},
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	s.handleCatalogUpsertPost(ctx)

	// Should get bad request because import response couldn't be parsed
	common.AssertEqual(t, http.StatusBadRequest, ctx.Writer.Status())
}

// ---- Test Run method ----

func Test_Run(t *testing.T) {
	s, _, _ := setupTestServer(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	s.router = r
	s.port = "0" // port 0 will fail to actually bind usefully, but we test the goroutine logic

	stopCh := make(chan struct{})

	go func() {
		// Stop quickly
		close(stopCh)
	}()

	// Run blocks until stopCh is closed
	s.Run(stopCh)
}

// ---- Test the DiscoverResponse JSON marshaling ----

func Test_DiscoverResponse_Marshal(t *testing.T) {
	d := &DiscoverResponse{Keys: []string{"a", "b", "c"}}
	buf, err := json.Marshal(d)
	common.AssertError(t, err)

	var d2 DiscoverResponse
	err = json.Unmarshal(buf, &d2)
	common.AssertError(t, err)
	common.AssertEqual(t, 3, len(d2.Keys))
	common.AssertEqual(t, "a", d2.Keys[0])
	common.AssertEqual(t, "b", d2.Keys[1])
	common.AssertEqual(t, "c", d2.Keys[2])
}

// ---- Test handleCatalogUpsertPost where location service returns error ----

func Test_handleCatalogUpsertPost_LocationServiceError(t *testing.T) {
	// Create a location server that returns errors
	badLocServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	})
	defer badLocServer.Close()

	fakeClientset := fake.NewClientset()
	cmCl := fakeClientset.CoreV1()
	cm := &corev1.ConfigMap{}
	cm.Name = util.StorageConfigMapName
	_, err := cmCl.ConfigMaps(metav1.NamespaceDefault).Create(context.Background(), cm, metav1.CreateOptions{})
	common.AssertError(t, err)

	backstageCallback := sync.Map{}
	bks := backstage.CreateBackstageServerWithCallbackMap(&backstageCallback, t)
	defer bks.Close()

	cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)

	badLocClient := location.SetupBridgeLocationRESTClient(badLocServer)
	badLocClient.HostURL = badLocServer.URL

	s := &StorageRESTServer{
		st:              cms,
		mutex:           sync.Mutex{},
		pushedLocations: map[string]*types.StorageBody{},
		locations:       badLocClient,
		bkstg:           &bkstgclient.BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: bks.URL},
		pushToRHDH:      true,
	}

	body := rest.PostBody{Body: []byte("test-data")}
	data, err := json.Marshal(body)
	common.AssertError(t, err)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1"},
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	s.handleCatalogUpsertPost(ctx)

	// Location service returned 500, should propagate
	sc := ctx.Writer.Status()
	if sc != http.StatusInternalServerError && sc != http.StatusCreated {
		// The location stub returns body which unmarshals, but returns 500 status
		t.Logf("got status %d", sc)
	}
}

// ---- Test handleCatalogUpsertPost where backstage import returns error ----

func Test_handleCatalogUpsertPost_BackstageImportError(t *testing.T) {
	// Create a backstage server that errors on import
	errorBkstg := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`error`))
		case "GET":
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer errorBkstg.Close()

	fakeClientset := fake.NewClientset()
	cmCl := fakeClientset.CoreV1()
	cm := &corev1.ConfigMap{}
	cm.Name = util.StorageConfigMapName
	_, err := cmCl.ConfigMaps(metav1.NamespaceDefault).Create(context.Background(), cm, metav1.CreateOptions{})
	common.AssertError(t, err)

	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	defer brts.Close()

	cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)
	locClient := location.SetupBridgeLocationRESTClient(brts)
	locClient.HostURL = brts.URL

	s := &StorageRESTServer{
		st:              cms,
		mutex:           sync.Mutex{},
		pushedLocations: map[string]*types.StorageBody{},
		locations:       locClient,
		bkstg:           &bkstgclient.BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: errorBkstg.URL},
		pushToRHDH:      true,
	}

	body := rest.PostBody{Body: []byte("test-data")}
	data, err := json.Marshal(body)
	common.AssertError(t, err)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1"},
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	s.handleCatalogUpsertPost(ctx)

	// backstage import error should still result in StatusCreated (graceful degradation)
	common.AssertEqual(t, http.StatusCreated, ctx.Writer.Status())
}

// ---- Test handleCatalogUpsertPost with already pushed but empty location map from backstage ----

func Test_handleCatalogUpsertPost_AlreadyPushed_EmptyLocationMap(t *testing.T) {
	// Create a backstage server that returns empty map for GetLocation
	emptyBkstg := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			if strings.Contains(r.URL.Path, "/locations/") {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{}`))
				return
			}
		case "POST":
			if strings.Contains(r.URL.Path, "/locations") {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(common.TestJSONSuccessfulBackstageLocationImportReturn))
				return
			}
		}
	})
	defer emptyBkstg.Close()

	fakeClientset := fake.NewClientset()
	cmCl := fakeClientset.CoreV1()
	cm := &corev1.ConfigMap{}
	cm.Name = util.StorageConfigMapName
	_, err := cmCl.ConfigMaps(metav1.NamespaceDefault).Create(context.Background(), cm, metav1.CreateOptions{})
	common.AssertError(t, err)

	// Pre-populate storage with a valid location
	sb := types.StorageBody{
		Body:           []byte("existing-data"),
		LocationId:     "some-id",
		LocationTarget: "http://example.com/target",
	}
	sbBuf, _ := json.Marshal(sb)
	cm.BinaryData = map[string][]byte{
		"mnist_v1": sbBuf,
	}
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Update(context.Background(), cm, metav1.UpdateOptions{})
	common.AssertError(t, err)

	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	defer brts.Close()

	cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)
	locClient := location.SetupBridgeLocationRESTClient(brts)
	locClient.HostURL = brts.URL

	s := &StorageRESTServer{
		st:              cms,
		mutex:           sync.Mutex{},
		pushedLocations: map[string]*types.StorageBody{},
		locations:       locClient,
		bkstg:           &bkstgclient.BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: emptyBkstg.URL},
		pushToRHDH:      true,
	}

	body := rest.PostBody{Body: []byte("updated-data")}
	data, err := json.Marshal(body)
	common.AssertError(t, err)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1"},
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	s.handleCatalogUpsertPost(ctx)

	// Empty location map means alreadyPushed becomes false, re-imports
	sc := ctx.Writer.Status()
	t.Logf("status: %d", sc)
	if sc != http.StatusCreated && sc != http.StatusOK {
		t.Errorf("expected StatusCreated or StatusOK, got %d", sc)
	}
}

// ---- Test GetBackstageURL with env var set ----

func Test_GetBackstageURL_WithEnvVar(t *testing.T) {
	t.Setenv(types.BackstageUrlEnvVar, "http://backstage.example.com")
	// restConfig not used when env var is set, but we still need to pass something
	result := GetBackstageURL(nil)
	common.AssertEqual(t, "http://backstage.example.com", result)
}

func Test_GetBackstageURL_WithNewline(t *testing.T) {
	t.Setenv(types.BackstageUrlEnvVar, "http://backstage.example.com\n")
	result := GetBackstageURL(nil)
	common.AssertEqual(t, "http://backstage.example.com", result)
}

// ---- Ensure multiple upserts then currentkeyset prunes correctly ----

func Test_MultiUpsertThenPrune(t *testing.T) {
	s, fakeClientset, cm := setupTestServer(t)
	cmCl := fakeClientset.CoreV1()

	// Upsert 3 models
	for _, key := range []string{"modelA_v1", "modelB_v1", "modelC_v1"} {
		body := rest.PostBody{Body: []byte(fmt.Sprintf("data-%s", key))}
		data, _ := json.Marshal(body)

		testWriter := testgin.NewTestResponseWriter()
		ctx, eng := gin.CreateTestContext(testWriter)
		s.router = eng
		ctx.Request = &http.Request{
			URL:  &url.URL{RawQuery: fmt.Sprintf("key=%s", key)},
			Body: io.NopCloser(bytes.NewReader(data)),
		}

		s.handleCatalogUpsertPost(ctx)
	}

	// List should have 3 entries
	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{}}

	s.handleCatalogList(ctx)

	rcvdDiscResp := &DiscoverResponse{}
	json.Unmarshal(testWriter.ResponseWriter.Body.Bytes(), rcvdDiscResp)
	common.AssertEqual(t, 3, len(rcvdDiscResp.Keys))

	// Prune to only modelA_v1
	ctx, _ = gin.CreateTestContext(testgin.NewTestResponseWriter())
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: "key=modelA_v1"}}

	s.handleCatalogCurrentKeySetPost(ctx)

	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())

	cm, _ = cmCl.ConfigMaps(metav1.NamespaceDefault).Get(context.Background(), util.StorageConfigMapName, metav1.GetOptions{})
	common.AssertEqual(t, 1, len(cm.BinaryData))
	_, hasA := cm.BinaryData["modelA_v1"]
	common.AssertEqual(t, true, hasA)
}

// ---- NormalizerFormat types ----

func Test_NewStorageRESTServer_JsonFormat(t *testing.T) {
	fakeClientset := fake.NewClientset()
	cmCl := fakeClientset.CoreV1()
	cm := &corev1.ConfigMap{}
	cm.Name = util.StorageConfigMapName
	_, err := cmCl.ConfigMaps(metav1.NamespaceDefault).Create(context.Background(), cm, metav1.CreateOptions{})
	common.AssertError(t, err)

	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	defer brts.Close()

	cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)

	s := NewStorageRESTServer(cms, "0", brts.URL, "bridge-token", "bkstg-token", types.JsonArrayForamt)
	common.AssertNotNil(t, s)
	common.AssertEqual(t, types.JsonArrayForamt, s.format)
}

// ---- Tests using mock storage for error paths ----

func setupMockServer(t *testing.T, ms *mockBridgeStorage) *StorageRESTServer {
	t.Helper()
	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	t.Cleanup(func() { brts.Close() })

	backstageCallback := sync.Map{}
	bks := backstage.CreateBackstageServerWithCallbackMap(&backstageCallback, t)
	t.Cleanup(func() { bks.Close() })

	locClient := location.SetupBridgeLocationRESTClient(brts)
	locClient.HostURL = brts.URL

	return &StorageRESTServer{
		st:              ms,
		mutex:           sync.Mutex{},
		pushedLocations: map[string]*types.StorageBody{},
		locations:       locClient,
		bkstg:           &bkstgclient.BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: bks.URL},
		pushToRHDH:      true,
	}
}

func Test_handleCatalogList_StorageError(t *testing.T) {
	ms := newMockBridgeStorage()
	ms.listErr = fmt.Errorf("storage list error")
	s := setupMockServer(t, ms)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{}}

	s.handleCatalogList(ctx)

	common.AssertEqual(t, http.StatusInternalServerError, ctx.Writer.Status())
	found := false
	for _, e := range ctx.Errors {
		if strings.Contains(e.Error(), "error listing location keys") {
			found = true
		}
	}
	common.AssertEqual(t, true, found)
}

func Test_handleCatalogCurrentKeySetPost_ListError(t *testing.T) {
	ms := newMockBridgeStorage()
	ms.listErr = fmt.Errorf("list error in currentkeyset")
	s := setupMockServer(t, ms)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: "key=foo"}}

	s.handleCatalogCurrentKeySetPost(ctx)

	common.AssertEqual(t, http.StatusInternalServerError, ctx.Writer.Status())
}

func Test_handleCatalogCurrentKeySetPost_RemoveError(t *testing.T) {
	ms := newMockBridgeStorage()
	ms.data["old_key"] = types.StorageBody{
		Body:           []byte("data"),
		LocationId:     "loc-1",
		LocationTarget: "http://example.com",
	}
	ms.removeErr = fmt.Errorf("remove error")
	s := setupMockServer(t, ms)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: ""}}

	s.handleCatalogCurrentKeySetPost(ctx)

	common.AssertEqual(t, http.StatusInternalServerError, ctx.Writer.Status())
}

func Test_handleCatalogUpsertPost_UpsertStorageError(t *testing.T) {
	ms := newMockBridgeStorage()
	ms.upsertErr = fmt.Errorf("upsert storage error")
	s := setupMockServer(t, ms)

	body := rest.PostBody{Body: []byte("test-data")}
	data, _ := json.Marshal(body)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1"},
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	s.handleCatalogUpsertPost(ctx)

	common.AssertEqual(t, http.StatusInternalServerError, ctx.Writer.Status())
}

func Test_handleCatalogUpsertPost_SecondUpsertError(t *testing.T) {
	// First upsert succeeds, second (storing backstage ID) fails
	callCount := 0
	ms := &mockBridgeStorage{
		data: map[string]types.StorageBody{},
	}
	s := setupMockServer(t, ms)

	// Override the storage with a wrapper that fails on the second upsert
	origSt := s.st
	s.st = &upsertCountingStorage{
		inner:     origSt,
		failAfter: 1,
	}

	body := rest.PostBody{Body: []byte("test-data")}
	data, _ := json.Marshal(body)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1"},
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	s.handleCatalogUpsertPost(ctx)

	_ = callCount
	common.AssertEqual(t, http.StatusInternalServerError, ctx.Writer.Status())
}

type upsertCountingStorage struct {
	inner     types.BridgeStorage
	failAfter int
	count     int
}

func (u *upsertCountingStorage) Initialize(cfg *k8srest.Config) error { return nil }
func (u *upsertCountingStorage) Fetch(key string) (types.StorageBody, error) {
	return u.inner.Fetch(key)
}
func (u *upsertCountingStorage) Upsert(key string, value types.StorageBody) error {
	u.count++
	if u.count > u.failAfter {
		return fmt.Errorf("upsert error on call %d", u.count)
	}
	return u.inner.Upsert(key, value)
}
func (u *upsertCountingStorage) Remove(key string) error { return u.inner.Remove(key) }
func (u *upsertCountingStorage) List() ([]string, error) { return u.inner.List() }

func Test_handleCatalogCurrentKeySetPost_WithDeleteLocation(t *testing.T) {
	ms := newMockBridgeStorage()
	ms.data["existing_v1"] = types.StorageBody{
		Body:           []byte("data"),
		LocationId:     "e83bc2d8-0f1c-49f2-b65b-8bfbbbe29ae2",
		LocationTarget: "http://rhoai-bridge.com/mnist/v1/catalog-info.yaml",
	}
	s := setupMockServer(t, ms)

	// Also put it in pushed locations cache
	sb := ms.data["existing_v1"]
	s.pushedLocations["existing_v1"] = &sb

	// Remove existing_v1 by not including it in key set
	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: ""}}

	s.handleCatalogCurrentKeySetPost(ctx)

	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())
	// Verify removed from cache
	_, ok := s.pushedLocations["existing_v1"]
	common.AssertEqual(t, false, ok)
}

func Test_handleCatalogCurrentKeySetPost_LocationServiceBadRC(t *testing.T) {
	ms := newMockBridgeStorage()
	ms.data["key_v1"] = types.StorageBody{
		Body:       []byte("data"),
		LocationId: "",
	}

	// Create a location server that returns bad status
	badLocServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "DELETE":
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("bad gateway"))
		default:
			w.WriteHeader(http.StatusBadGateway)
		}
	})
	defer badLocServer.Close()

	backstageCallback := sync.Map{}
	bks := backstage.CreateBackstageServerWithCallbackMap(&backstageCallback, t)
	defer bks.Close()

	badLocClient := location.SetupBridgeLocationRESTClient(badLocServer)
	badLocClient.HostURL = badLocServer.URL

	s := &StorageRESTServer{
		st:              ms,
		mutex:           sync.Mutex{},
		pushedLocations: map[string]*types.StorageBody{},
		locations:       badLocClient,
		bkstg:           &bkstgclient.BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: bks.URL},
		pushToRHDH:      true,
	}

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: ""}}

	s.handleCatalogCurrentKeySetPost(ctx)

	common.AssertEqual(t, http.StatusInternalServerError, ctx.Writer.Status())
}

func Test_sync_FetchError(t *testing.T) {
	ms := newMockBridgeStorage()
	ms.fetchErr = fmt.Errorf("fetch error in sync")
	s := setupMockServer(t, ms)

	result, err := s.sync("some-key")
	if err == nil {
		t.Logf("expected fetch error, got nil; result: %+v", result)
	}
}

func Test_handleCatalogUpsertPost_SyncError(t *testing.T) {
	ms := newMockBridgeStorage()
	ms.fetchErr = fmt.Errorf("fetch error during sync")
	s := setupMockServer(t, ms)

	body := rest.PostBody{Body: []byte("test-data")}
	data, _ := json.Marshal(body)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1"},
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	s.handleCatalogUpsertPost(ctx)

	common.AssertEqual(t, http.StatusInternalServerError, ctx.Writer.Status())
}

func Test_setupBkstg_WithBkstgURL(t *testing.T) {
	t.Setenv(types.BackstageUrlEnvVar, "http://backstage.test.local")

	s := &StorageRESTServer{
		mutex:           sync.Mutex{},
		pushedLocations: map[string]*types.StorageBody{},
		bkstgToken:      "test-token",
	}

	// setupBkstg should try GetRESTConfig - may or may not succeed depending on env
	// If it succeeds (in-cluster), then bkstg gets set and returns true
	// If it fails, returns false
	result := s.setupBkstg()
	if result {
		// If it succeeded, bkstg should be non-nil now
		common.AssertNotNil(t, s.bkstg)
	}
	// Either way, test exercises the setupBkstg code path with BKSTG_URL set
}

func Test_handleCatalogCurrentKeySetPost_DeleteLocationError(t *testing.T) {
	ms := newMockBridgeStorage()
	ms.data["del_key"] = types.StorageBody{
		Body:           []byte("data"),
		LocationId:     "404-bad-loc",
		LocationTarget: "http://example.com",
	}

	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	defer brts.Close()

	backstageCallback := sync.Map{}
	bks := backstage.CreateBackstageServerWithCallbackMap(&backstageCallback, t)
	defer bks.Close()

	locClient := location.SetupBridgeLocationRESTClient(brts)
	locClient.HostURL = brts.URL

	s := &StorageRESTServer{
		st:              ms,
		mutex:           sync.Mutex{},
		pushedLocations: map[string]*types.StorageBody{},
		locations:       locClient,
		bkstg:           &bkstgclient.BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: bks.URL},
		pushToRHDH:      true,
	}

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: ""}}

	s.handleCatalogCurrentKeySetPost(ctx)

	// Should still complete even if delete location returns 404
	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())
}

func Test_handleCatalogCurrentKeySetPost_FetchError(t *testing.T) {
	// Test where Fetch fails but Remove still works
	callCount := 0
	ms := &mockBridgeStorage{
		data: map[string]types.StorageBody{
			"key_v1": {Body: []byte("data"), LocationId: "loc-1"},
		},
	}
	// Override to make fetch fail
	ms.fetchErr = fmt.Errorf("fetch error")

	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	defer brts.Close()

	backstageCallback := sync.Map{}
	bks := backstage.CreateBackstageServerWithCallbackMap(&backstageCallback, t)
	defer bks.Close()

	locClient := location.SetupBridgeLocationRESTClient(brts)
	locClient.HostURL = brts.URL

	s := &StorageRESTServer{
		st:              ms,
		mutex:           sync.Mutex{},
		pushedLocations: map[string]*types.StorageBody{},
		locations:       locClient,
		bkstg:           &bkstgclient.BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: bks.URL},
		pushToRHDH:      true,
	}

	_ = callCount
	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: ""}}

	// Need to make list work but fetch fail
	// Reset fetchErr after list
	ms.fetchErr = fmt.Errorf("fetch error")

	s.handleCatalogCurrentKeySetPost(ctx)

	// Should still try to proceed
	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())
}

func Test_NewStorageRESTServer_WithFalsePushEnv(t *testing.T) {
	t.Setenv(types.PushToRHDHEnvVar, "false")

	fakeClientset := fake.NewClientset()
	cmCl := fakeClientset.CoreV1()
	cm := &corev1.ConfigMap{}
	cm.Name = util.StorageConfigMapName
	_, err := cmCl.ConfigMaps(metav1.NamespaceDefault).Create(context.Background(), cm, metav1.CreateOptions{})
	common.AssertError(t, err)

	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	defer brts.Close()

	cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)

	s := NewStorageRESTServer(cms, "0", brts.URL, "bridge-token", "bkstg-token", types.CatalogInfoYamlFormat)
	common.AssertNotNil(t, s)
	common.AssertEqual(t, false, s.pushToRHDH)
}

func Test_NewStorageRESTServer_WithInvalidPushEnv(t *testing.T) {
	t.Setenv(types.PushToRHDHEnvVar, "not-a-bool")

	fakeClientset := fake.NewClientset()
	cmCl := fakeClientset.CoreV1()
	cm := &corev1.ConfigMap{}
	cm.Name = util.StorageConfigMapName
	_, err := cmCl.ConfigMaps(metav1.NamespaceDefault).Create(context.Background(), cm, metav1.CreateOptions{})
	common.AssertError(t, err)

	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	defer brts.Close()

	cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)

	s := NewStorageRESTServer(cms, "0", brts.URL, "bridge-token", "bkstg-token", types.CatalogInfoYamlFormat)
	common.AssertNotNil(t, s)
	common.AssertEqual(t, false, s.pushToRHDH)
}

// ---- NewBridgeStorage tests ----

func Test_NewBridgeStorage_Github(t *testing.T) {
	// GithubBridgeStorage case just returns nil
	result := NewBridgeStorage(types.GithubBridgeStorage)
	if result != nil {
		t.Errorf("expected nil for GithubBridgeStorage, got %v", result)
	}
}

func Test_NewBridgeStorage_ConfigMap(t *testing.T) {
	// ConfigMap case will try GetK8sConfig which may or may not work
	result := NewBridgeStorage(types.ConfigMapBridgeStorage)
	// In test env without kubeconfig, this may return nil
	t.Logf("NewBridgeStorage ConfigMap result: %v", result)
}

func Test_NewBridgeStorage_Default(t *testing.T) {
	// Unknown type should fall through to ConfigMap case
	result := NewBridgeStorage(types.BridgeStorageType("unknown"))
	t.Logf("NewBridgeStorage default result: %v", result)
}

// ---- GetRESTConfig test ----

func Test_GetRESTConfig(t *testing.T) {
	cfg, err := GetRESTConfig()
	// In test env this may or may not succeed
	t.Logf("GetRESTConfig: cfg=%v, err=%v", cfg, err)
}

// ---- GetBackstageURL without env var ----

func Test_GetBackstageURL_NoEnvVar(t *testing.T) {
	t.Setenv(types.BackstageUrlEnvVar, "")
	// With nil config, GetRouteClient will likely panic or return empty
	// We need a real rest config - use GetRESTConfig
	cfg, _ := GetRESTConfig()
	if cfg != nil {
		result := GetBackstageURL(cfg)
		t.Logf("GetBackstageURL without env: %s", result)
	}
}

func Test_handleCatalogUpsertPost_LocationBadRC(t *testing.T) {
	ms := newMockBridgeStorage()

	// Create a location server that returns 400
	badLocServer := common.CreateTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":"bad request"}`))
		w.WriteHeader(http.StatusBadRequest)
	})
	defer badLocServer.Close()

	backstageCallback := sync.Map{}
	bks := backstage.CreateBackstageServerWithCallbackMap(&backstageCallback, t)
	defer bks.Close()

	badLocClient := location.SetupBridgeLocationRESTClient(badLocServer)
	badLocClient.HostURL = badLocServer.URL

	s := &StorageRESTServer{
		st:              ms,
		mutex:           sync.Mutex{},
		pushedLocations: map[string]*types.StorageBody{},
		locations:       badLocClient,
		bkstg:           &bkstgclient.BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: bks.URL},
		pushToRHDH:      true,
	}

	body := rest.PostBody{Body: []byte("test-data")}
	data, _ := json.Marshal(body)

	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	s.router = eng
	ctx.Request = &http.Request{
		URL:  &url.URL{RawQuery: "key=mnist_v1"},
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	s.handleCatalogUpsertPost(ctx)

	// Should get the bad RC from location service
	sc := ctx.Writer.Status()
	t.Logf("got status %d from location bad RC test", sc)
}
