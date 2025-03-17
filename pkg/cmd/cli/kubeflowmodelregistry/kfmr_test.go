package kubeflowmodelregistry

import (
	"bufio"
	"bytes"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/config"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/kfmr"
	"testing"
)

func TestLookOverKFMR(t *testing.T) {
	ts := kfmr.CreateGetServer(t)
	defer ts.Close()
	for _, tc := range []struct {
		args []string
		// we do output compare in chunks as ranges over the components status map are non-deterministic wrt order
		outStr []string
	}{
		{
			args:   []string{"Owner", "Lifecycle"},
			outStr: []string{listOutput},
		},
		{
			args:   []string{"Owner", "Lifecycle", "1"},
			outStr: []string{listOutput},
		},
	} {
		cfg := &config.Config{}
		kfmr.SetupKubeflowTestRESTClient(ts, cfg)
		k := SetupKubeflowRESTClient(cfg)
		owner := tc.args[0]
		lifecycle := tc.args[1]
		ids := []string{}
		if len(tc.args) > 2 {
			ids = tc.args[2:]
		}
		b := []byte{}
		buf := bytes.NewBuffer(b)
		bwriter := bufio.NewWriter(buf)
		_, _, err := LoopOverKFMR(owner, lifecycle, ids, bwriter, k, nil)
		common.AssertError(t, err)
		bwriter.Flush()
		outstr := buf.String()
		for _, str := range tc.outStr {
			common.AssertLineCompare(t, outstr, str, 0)
		}

	}

}

const (
	listOutput = `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  annotations:
    backstage.io/techdocs-ref: ./
  description: dummy model 1
  links:
  - icon: WebAsset
    title: version 1
    type: website
    url: https://foo.com
  name: model-1
  tags:
  - foo-bar
spec:
  dependsOn:
  - resource:v1
  - api:model-1-v1-artifact
  lifecycle: Lifecycle
  owner: user:kube:admin
  profile:
    displayName: model-1
  type: model-server
---
apiVersion: backstage.io/v1alpha1
kind: Resource
metadata:
  annotations:
    backstage.io/techdocs-ref: resource/
  description: dummy model 1
  links:
  - icon: WebAsset
    title: version 1
    type: website
    url: https://foo.com
  name: v1
spec:
  dependencyOf:
  - component:model-1
  lifecycle: Lifecycle
  owner: user:kube:admin
  profile:
    displayName: v1
  type: ai-model
---
apiVersion: backstage.io/v1alpha1
kind: API
metadata:
  annotations:
    backstage.io/techdocs-ref: api/
  description: dummy model 1
  name: model-1
spec:
  definition: no-definition-yet
  dependencyOf:
  - component:model-1
  lifecycle: Lifecycle
  owner: user:kube:admin
  profile:
    displayName: model-1
  type: unknown
`
)
