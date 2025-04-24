package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	"io"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func Test_handleCatalogList_handleCatalogFetch_ConfigMap(t *testing.T) {
	cmCl := fake.NewClientset().CoreV1()
	cm := &corev1.ConfigMap{}
	cm.Name = util.StorageConfigMapName
	var err error
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Create(context.Background(), cm, metav1.CreateOptions{})
	common.AssertError(t, err)

	sb := &types.StorageBody{
		Body:           []byte("foo"),
		LocationId:     "loc-id",
		LocationTarget: "http://foo.com/foo-data",
	}
	sbBuf := []byte{}
	sbBuf, err = json.Marshal(sb)
	common.AssertError(t, err)

	for _, tc := range []struct {
		name         string
		reqURL       url.URL
		expectedSC   int
		expectedKeys []string
		cmData       map[string][]byte
	}{
		{
			name:         "updated entry",
			reqURL:       url.URL{},
			expectedKeys: []string{"v1", "v2", "v3"},
			cmData: map[string][]byte{
				"v1": sbBuf,
				"v2": sbBuf,
				"v3": sbBuf,
			},
			expectedSC: http.StatusOK,
		},
	} {
		testWriter := testgin.NewTestResponseWriter()
		ctx, eng := gin.CreateTestContext(testWriter)
		ctx.Request = &http.Request{URL: &url.URL{}}

		cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)

		s := &StorageRESTServer{
			router:          eng,
			st:              cms,
			mutex:           sync.Mutex{},
			pushedLocations: map[string]*types.StorageBody{},
		}

		cm.BinaryData = tc.cmData
		cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Update(context.Background(), cm, metav1.UpdateOptions{})
		common.AssertError(t, err)

		s.handleCatalogList(ctx)

		common.AssertEqual(t, ctx.Writer.Status(), tc.expectedSC)

		rcvdDiscResp := &DiscoverResponse{}
		err = json.Unmarshal(testWriter.ResponseWriter.Body.Bytes(), rcvdDiscResp)
		common.AssertError(t, err)

		for _, ek := range tc.expectedKeys {
			t.Logf("looking for key %s", ek)
			found := false
			for _, gk := range rcvdDiscResp.Keys {
				if gk == ek {
					t.Logf("found key %s", ek)
					found = true
					break
				}
			}
			common.AssertEqual(t, true, found)
		}

		for _, k := range tc.expectedKeys {
			testWriter = testgin.NewTestResponseWriter()
			ctx, eng = gin.CreateTestContext(testWriter)
			ctx.Request = &http.Request{URL: &url.URL{RawQuery: fmt.Sprintf("key=%s", k)}}

			s.handleCatalogFetch(ctx)

			common.AssertEqual(t, ctx.Writer.Status(), tc.expectedSC)
			rcvdStoBod := &types.StorageBody{}
			err = json.Unmarshal(testWriter.ResponseWriter.Body.Bytes(), rcvdStoBod)
			common.AssertError(t, err)
			common.AssertEqual(t, sb.LocationId, rcvdStoBod.LocationId)
			common.AssertEqual(t, sb.LocationTarget, rcvdStoBod.LocationTarget)
		}

	}

}

func Test_handleCatalogUpsertPost_handleCatalogCurrentKeySetPost_ConfigMap(t *testing.T) {
	locationCallback := sync.Map{}
	brts := location.CreateBridgeLocationServerWithCallbackMap(&locationCallback, t)
	defer brts.Close()
	backstageCallback := sync.Map{}
	bks := backstage.CreateBackstageServerWithCallbackMap(&backstageCallback, t)
	defer bks.Close()

	cmCl := fake.NewClientset().CoreV1()
	cm := &corev1.ConfigMap{}
	cm.Name = util.StorageConfigMapName
	_, err := cmCl.ConfigMaps(metav1.NamespaceDefault).Create(context.Background(), cm, metav1.CreateOptions{})
	common.AssertError(t, err)

	// handleCatalogUpsertPost testing

	for _, tc := range []struct {
		name           string
		reqURL         url.URL
		body           rest.PostBody
		expectedErrMsg string
		expectedSC     int
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
		},
		{
			name:       "updated entry",
			reqURL:     url.URL{RawQuery: "key=mnist_v1"},
			body:       rest.PostBody{Body: []byte("update")},
			expectedSC: http.StatusOK,
		},
	} {
		testWriter := testgin.NewTestResponseWriter()
		var data []byte
		data, err = json.Marshal(tc.body)
		common.AssertError(t, err)
		ctx, eng := gin.CreateTestContext(testWriter)
		ctx.Request = &http.Request{URL: &tc.reqURL, Body: io.NopCloser(bytes.NewReader(data))}

		cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)

		s := &StorageRESTServer{
			router:          eng,
			st:              cms,
			mutex:           sync.Mutex{},
			pushedLocations: map[string]*types.StorageBody{},
			locations:       location.SetupBridgeLocationRESTClient(brts),
			bkstg:           (&bkstgclient.BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: bks.URL}),
		}

		s.handleCatalogUpsertPost(ctx)

		common.AssertEqual(t, tc.expectedSC, ctx.Writer.Status())
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

		keys := tc.reqURL.Query()
		if keys.Has(util.KeyQueryParam) && ctx.Writer.Status() == http.StatusCreated {
			val := keys.Get(util.KeyQueryParam)
			// storage cache should not be populated yet
			_, ok := s.pushedLocations[val]
			common.AssertEqual(t, false, ok)
			cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Get(context.Background(), util.StorageConfigMapName, metav1.GetOptions{})
			common.AssertError(t, err)
			_, ok = cm.BinaryData[val]
			common.AssertEqual(t, true, ok)

			found := false
			locationCallback.Range(func(key, value any) bool {
				found = true
				return true
			})
			// location service called
			common.AssertEqual(t, true, found)

			found = false
			keyList := []any{}
			backstageCallback.Range(func(key, value any) bool {
				found = true
				keyList = append(keyList, key)
				return true
			})
			// backstage called
			common.AssertEqual(t, true, found)

			// clear out call cache for next check
			for _, k := range keyList {
				backstageCallback.Delete(k)
			}
		}
		if keys.Has(util.KeyQueryParam) && ctx.Writer.Status() == http.StatusOK {
			val := keys.Get(util.KeyQueryParam)
			// storage cache should be populated
			_, ok := s.pushedLocations[val]
			common.AssertEqual(t, ok, true)

			// backstage should not be called again
			found := false
			backstageCallback.Range(func(key, value any) bool {
				found = true
				return true
			})
			common.AssertEqual(t, false, found)
		}

	}

	// handleCatalogCurrentKeySetPost testing

	// first, show that when the existing key set is present, the data remains in storage
	testWriter := testgin.NewTestResponseWriter()
	ctx, eng := gin.CreateTestContext(testWriter)
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: "key=mnist_v1"}}
	cms := configmap.NewConfigMapBridgeStorageForTest(metav1.NamespaceDefault, cmCl)

	s := &StorageRESTServer{
		router:          eng,
		st:              cms,
		mutex:           sync.Mutex{},
		pushedLocations: map[string]*types.StorageBody{},
		locations:       location.SetupBridgeLocationRESTClient(brts),
		bkstg:           (&bkstgclient.BackstageRESTClientWrapper{RESTClient: common.DC(), RootURL: bks.URL}),
	}

	s.handleCatalogCurrentKeySetPost(ctx)

	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Get(context.Background(), util.StorageConfigMapName, metav1.GetOptions{})
	common.AssertError(t, err)
	common.AssertEqual(t, 1, len(cm.BinaryData))

	// then no longer include keys, see model removed from storage

	// have to create a new Context as gin-gonic caches query params and does not expose a way to clear that cache
	ctx, _ = gin.CreateTestContext(testWriter)
	ctx.Request = &http.Request{URL: &url.URL{RawQuery: ""}}

	s.handleCatalogCurrentKeySetPost(ctx)

	common.AssertEqual(t, http.StatusOK, ctx.Writer.Status())
	cm, err = cmCl.ConfigMaps(metav1.NamespaceDefault).Get(context.Background(), util.StorageConfigMapName, metav1.GetOptions{})
	common.AssertError(t, err)
	common.AssertEqual(t, 0, len(cm.BinaryData))

	_, ok := locationCallback.Load("delete")
	common.AssertEqual(t, true, ok)

	_, ok = backstageCallback.Load("delete")
	common.AssertEqual(t, true, ok)

}
