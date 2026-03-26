package backstage

import (
	"net/url"
	"testing"

	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/rest"
)

func TestBuildKeysNoArgs(t *testing.T) {
	keys := buildKeys()
	if len(keys) != 0 {
		t.Errorf("expected empty map, got %v", keys)
	}
}

func TestBuildKeysSingleArgWithoutColon(t *testing.T) {
	keys := buildKeys("myarg")
	arr, ok := keys[rest.DEFAULT_NS]
	if !ok {
		t.Fatal("expected default namespace key")
	}
	if len(arr) != 1 || arr[0] != "myarg" {
		t.Errorf("expected [myarg], got %v", arr)
	}
}

func TestBuildKeysSingleArgWithColon(t *testing.T) {
	keys := buildKeys("ns1:name1")
	arr, ok := keys["ns1"]
	if !ok {
		t.Fatal("expected ns1 key")
	}
	if len(arr) != 1 || arr[0] != "name1" {
		t.Errorf("expected [name1], got %v", arr)
	}
	if _, ok := keys[rest.DEFAULT_NS]; ok {
		t.Error("did not expect default namespace key")
	}
}

func TestBuildKeysMultipleMixedArgs(t *testing.T) {
	keys := buildKeys("ns1:name1", "plain", "ns1:name2", "ns2:name3")
	// ns1 should have two entries
	if len(keys["ns1"]) != 2 {
		t.Errorf("expected 2 entries for ns1, got %d", len(keys["ns1"]))
	}
	// default should have one entry
	if len(keys[rest.DEFAULT_NS]) != 1 || keys[rest.DEFAULT_NS][0] != "plain" {
		t.Errorf("expected [plain] for default ns, got %v", keys[rest.DEFAULT_NS])
	}
	// ns2 should have one entry
	if len(keys["ns2"]) != 1 || keys["ns2"][0] != "name3" {
		t.Errorf("expected [name3] for ns2, got %v", keys["ns2"])
	}
}

func TestPullSavedArgsFromQueryParamsTagsTrue(t *testing.T) {
	b := &BackstageRESTClientWrapper{Tags: true}
	qp := &url.Values{}
	qp.Set("metadata.tags", "tag1 tag2 tag3")
	qp.Set("filter", "kind=component")

	args := b.pullSavedArgsFromQueryParams(qp)
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d: %v", len(args), args)
	}
	if args[0] != "tag1" || args[1] != "tag2" || args[2] != "tag3" {
		t.Errorf("unexpected args: %v", args)
	}
	// metadata.tags should have been deleted
	if qp.Has("metadata.tags") {
		t.Error("expected metadata.tags to be deleted from query params")
	}
	// filter should still be present
	if !qp.Has("filter") {
		t.Error("expected filter to still be present")
	}
}

func TestPullSavedArgsFromQueryParamsTagsFalse(t *testing.T) {
	b := &BackstageRESTClientWrapper{Tags: false}
	qp := &url.Values{}
	qp.Set("metadata.tags", "tag1 tag2")

	args := b.pullSavedArgsFromQueryParams(qp)
	if len(args) != 0 {
		t.Errorf("expected empty args when Tags is false, got %v", args)
	}
}

func TestTagsIncluded(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		tags     []string
		expected bool
	}{
		{"subset match", []string{"a"}, []string{"a", "b"}, true},
		{"exact match", []string{"a", "b"}, []string{"a", "b"}, true},
		{"no match", []string{"c"}, []string{"a", "b"}, false},
		{"args longer than tags", []string{"a", "b", "c"}, []string{"a", "b"}, false},
		{"empty args", []string{}, []string{"a", "b"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tagsIncluded(tc.args, tc.tags)
			if result != tc.expected {
				t.Errorf("tagsIncluded(%v, %v) = %v, want %v", tc.args, tc.tags, result, tc.expected)
			}
		})
	}
}

func TestTagsMatch(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		tags     []string
		expected bool
	}{
		{"exact match same order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"exact match different order", []string{"b", "a"}, []string{"a", "b"}, true},
		{"different lengths", []string{"a"}, []string{"a", "b"}, false},
		{"no match", []string{"a", "c"}, []string{"a", "b"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tagsMatch(tc.args, tc.tags)
			if result != tc.expected {
				t.Errorf("tagsMatch(%v, %v) = %v, want %v", tc.args, tc.tags, result, tc.expected)
			}
		})
	}
}

func TestPullSavedArgsFromQueryParamsTagsTrueEmpty(t *testing.T) {
	b := &BackstageRESTClientWrapper{Tags: true}
	qp := &url.Values{}
	// No metadata.tags set, so Get returns empty string -> Split returns [""]
	args := b.pullSavedArgsFromQueryParams(qp)
	if len(args) != 1 || args[0] != "" {
		t.Errorf("expected [''], got %v", args)
	}
}

func TestUpdateQParams(t *testing.T) {
	// Test non-API kind
	qp := updateQParams("component", "model-server", nil)
	filter := qp.Get("filter")
	if filter != "kind=component,spec.type=model-server" {
		t.Errorf("unexpected filter for component: %s", filter)
	}

	// Test API kind (should not include spec.type)
	qp = updateQParams("api", "openapi", nil)
	filter = qp.Get("filter")
	if filter != "kind=api" {
		t.Errorf("unexpected filter for api: %s", filter)
	}

	// Test API kind case-insensitive
	qp = updateQParams("API", "openapi", nil)
	filter = qp.Get("filter")
	if filter != "kind=API" {
		t.Errorf("unexpected filter for API: %s", filter)
	}
}
