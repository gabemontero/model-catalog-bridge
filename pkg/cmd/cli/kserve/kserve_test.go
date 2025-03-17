package kserve

import (
	"bufio"
	"bytes"
	"context"
	serverapiv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	fakeservingv1beta1 "github.com/kserve/kserve/pkg/client/clientset/versioned/fake"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/config"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	"testing"
)

func setupConfig(cfg *config.Config, objs []serverapiv1beta1.InferenceService) {
	cfg.ServingClient = fakeservingv1beta1.NewSimpleClientset().ServingV1beta1()
	for _, obj := range objs {
		cfg.ServingClient.InferenceServices(obj.Namespace).Create(context.TODO(), &obj, metav1.CreateOptions{})
		cfg.Namespace = obj.Namespace
	}
}

func TestKserveBackstagePrinters(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		// we do output compare in chunks as ranges over the components status map are non-deterministic wrt order
		outStr []string
		is     []serverapiv1beta1.InferenceService
	}{
		{
			name: "Owner and Lifecycle but no data",
			args: []string{"Owner", "Lifecycle"},
		},
		{
			name: "Owner and Lifecycle and data but no url",
			args: []string{"Owner", "Lifecycle"},
			is: []serverapiv1beta1.InferenceService{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "InferSvc-1",
					},
				},
			},
			outStr: []string{urlNotSet},
		},
		{
			name: "Owner and Lifecycle set and data and url",
			args: []string{"Owner", "Lifecycle"},
			is: []serverapiv1beta1.InferenceService{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "InferSvc-1",
					},
					Status: serverapiv1beta1.InferenceServiceStatus{
						URL: &apis.URL{
							Scheme: "https",
							Host:   "kserve.com",
						},
					},
				},
			},
			outStr: []string{urlSet},
		},
		{
			name: "use everything including bunch of tags",
			args: []string{"Owner", "Lifecycle"},
			is: []serverapiv1beta1.InferenceService{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "InferSvc-1",
					},
					Status: serverapiv1beta1.InferenceServiceStatus{
						URL: &apis.URL{
							Scheme: "https",
							Host:   "kserve.com",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "InferSvc-2",
					},
					Spec: serverapiv1beta1.InferenceServiceSpec{
						Predictor: serverapiv1beta1.PredictorSpec{
							SKLearn:     &serverapiv1beta1.SKLearnSpec{},
							XGBoost:     &serverapiv1beta1.XGBoostSpec{},
							Tensorflow:  &serverapiv1beta1.TFServingSpec{},
							PyTorch:     &serverapiv1beta1.TorchServeSpec{},
							Triton:      &serverapiv1beta1.TritonSpec{},
							ONNX:        &serverapiv1beta1.ONNXRuntimeSpec{},
							HuggingFace: &serverapiv1beta1.HuggingFaceRuntimeSpec{},
							PMML:        &serverapiv1beta1.PMMLSpec{},
							LightGBM:    &serverapiv1beta1.LightGBMSpec{},
							Paddle:      &serverapiv1beta1.PaddleServerSpec{},
							Model:       &serverapiv1beta1.ModelSpec{ModelFormat: serverapiv1beta1.ModelFormat{Name: "f1", Version: &version}},
						},
						Explainer: &serverapiv1beta1.ExplainerSpec{
							ART: &serverapiv1beta1.ARTExplainerSpec{Type: serverapiv1beta1.ARTSquareAttackExplainer},
						},
					},
					Status: serverapiv1beta1.InferenceServiceStatus{
						URL: &apis.URL{
							Scheme: "https",
							Host:   "kserve.com",
						},
						Components: map[serverapiv1beta1.ComponentType]serverapiv1beta1.ComponentStatusSpec{
							serverapiv1beta1.PredictorComponent: {
								URL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "docs",
								},
								RestURL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "rest",
								},
								GrpcURL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "grpc",
								},
							},
							serverapiv1beta1.ExplainerComponent: {
								URL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "docs",
								},
								RestURL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "rest",
								},
								GrpcURL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "grpc",
								},
							},
							serverapiv1beta1.TransformerComponent: {
								URL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "docs",
								},
								RestURL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "rest",
								},
								GrpcURL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "grpc",
								},
							},
						},
					},
				},
			},
			outStr: []string{urlSet, description2, link21, link22, link23, link24, link25, link26, link27, link28, link29, link30, link31, link32, link33, nameTags2, compSpec2, resourceSpec2, apiSpec2},
		},
		{
			name: "fetch 2 specific inferenceservices",
			args: []string{"Owner", "Lifecycle", "InferSvc-1", "InferSvc-2"},
			is: []serverapiv1beta1.InferenceService{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "InferSvc-1",
					},
					Status: serverapiv1beta1.InferenceServiceStatus{
						URL: &apis.URL{
							Scheme: "https",
							Host:   "kserve.com",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "InferSvc-2",
					},
					Spec: serverapiv1beta1.InferenceServiceSpec{
						Predictor: serverapiv1beta1.PredictorSpec{
							SKLearn:     &serverapiv1beta1.SKLearnSpec{},
							XGBoost:     &serverapiv1beta1.XGBoostSpec{},
							Tensorflow:  &serverapiv1beta1.TFServingSpec{},
							PyTorch:     &serverapiv1beta1.TorchServeSpec{},
							Triton:      &serverapiv1beta1.TritonSpec{},
							ONNX:        &serverapiv1beta1.ONNXRuntimeSpec{},
							HuggingFace: &serverapiv1beta1.HuggingFaceRuntimeSpec{},
							PMML:        &serverapiv1beta1.PMMLSpec{},
							LightGBM:    &serverapiv1beta1.LightGBMSpec{},
							Paddle:      &serverapiv1beta1.PaddleServerSpec{},
							Model:       &serverapiv1beta1.ModelSpec{ModelFormat: serverapiv1beta1.ModelFormat{Name: "f1", Version: &version}},
						},
						Explainer: &serverapiv1beta1.ExplainerSpec{
							ART: &serverapiv1beta1.ARTExplainerSpec{Type: serverapiv1beta1.ARTSquareAttackExplainer},
						},
					},
					Status: serverapiv1beta1.InferenceServiceStatus{
						URL: &apis.URL{
							Scheme: "https",
							Host:   "kserve.com",
						},
						Components: map[serverapiv1beta1.ComponentType]serverapiv1beta1.ComponentStatusSpec{
							serverapiv1beta1.PredictorComponent: {
								URL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "docs",
								},
								RestURL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "rest",
								},
								GrpcURL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "grpc",
								},
							},
							serverapiv1beta1.ExplainerComponent: {
								URL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "docs",
								},
								RestURL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "rest",
								},
								GrpcURL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "grpc",
								},
							},
							serverapiv1beta1.TransformerComponent: {
								URL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "docs",
								},
								RestURL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "rest",
								},
								GrpcURL: &apis.URL{
									Scheme: "https",
									Host:   "kserve.com",
									Path:   "grpc",
								},
							},
						},
					},
				},
			},
			outStr: []string{urlSet, description2, link21, link22, link23, link24, link25, link26, link27, link28, link29, link30, link31, link32, link33, nameTags2, compSpec2, resourceSpec2, apiSpec2},
		},
	} {
		cfg := &config.Config{}
		setupConfig(cfg, tc.is)
		namespace := cfg.Namespace
		servingClient := cfg.ServingClient
		owner := tc.args[0]
		lifecycle := tc.args[1]

		isl, err := servingClient.InferenceServices(namespace).List(context.Background(), metav1.ListOptions{})
		common.AssertError(t, err)
		b := []byte{}
		buf := bytes.NewBuffer(b)
		bwriter := bufio.NewWriter(buf)
		for _, is := range isl.Items {
			err = CallBackstagePrinters(owner, lifecycle, &is, bwriter)
			common.AssertError(t, err)
		}

		bwriter.Flush()
		outstr := buf.String()
		if len(tc.outStr) == 1 {
			common.AssertLineCompare(t, outstr, tc.outStr[0], 0)
			continue
		}
		common.AssertContains(t, outstr, tc.outStr)
	}
}

var (
	version = "v1.0"
)

const (
	urlNotSet = `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  annotations:
    backstage.io/techdocs-ref: ./
  description: KServe instance default:InferSvc-1
  name: default_InferSvc-1
spec:
  dependsOn:
  - resource:default_InferSvc-1
  - api:default_InferSvc-1
  lifecycle: Lifecycle
  owner: user:Owner
  profile:
    displayName: default_InferSvc-1
  providesApis:
  - default_InferSvc-1
  type: model-server
---
apiVersion: backstage.io/v1alpha1
kind: Resource
metadata:
  annotations:
    backstage.io/techdocs-ref: resource/
  description: KServe instance default:InferSvc-1
  name: default_InferSvc-1
spec:
  dependencyOf:
  - component:default_InferSvc-1
  lifecycle: Lifecycle
  owner: user:Owner
  profile:
    displayName: default_InferSvc-1
  providesApis:
  - default_InferSvc-1
  type: ai-model
---
apiVersion: backstage.io/v1alpha1
kind: API
metadata:
  annotations:
    backstage.io/techdocs-ref: api/
  description: KServe instance default:InferSvc-1
  name: default_InferSvc-1
spec:
  definition: ""
  dependencyOf:
  - component:default_InferSvc-1
  lifecycle: Lifecycle
  owner: user:Owner
  profile:
    displayName: default_InferSvc-1
  type: unknown
`
	urlSet = `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  annotations:
    backstage.io/techdocs-ref: ./
  description: KServe instance default:InferSvc-1
  links:
  - icon: WebAsset
    title: API URL
    type: website
    url: https://kserve.com
  name: default_InferSvc-1
spec:
  dependsOn:
  - resource:default_InferSvc-1
  - api:default_InferSvc-1
  lifecycle: Lifecycle
  owner: user:Owner
  profile:
    displayName: default_InferSvc-1
  providesApis:
  - default_InferSvc-1
  type: model-server
---
apiVersion: backstage.io/v1alpha1
kind: Resource
metadata:
  annotations:
    backstage.io/techdocs-ref: resource/
  description: KServe instance default:InferSvc-1
  links:
  - icon: WebAsset
    title: API URL
    type: website
    url: https://kserve.com
  name: default_InferSvc-1
spec:
  dependencyOf:
  - component:default_InferSvc-1
  lifecycle: Lifecycle
  owner: user:Owner
  profile:
    displayName: default_InferSvc-1
  providesApis:
  - default_InferSvc-1
  type: ai-model
---
apiVersion: backstage.io/v1alpha1
kind: API
metadata:
  annotations:
    backstage.io/techdocs-ref: api/
  description: KServe instance default:InferSvc-1
  links:
  - icon: WebAsset
    title: API URL
    type: website
    url: https://kserve.com
  name: default_InferSvc-1
spec:
  definition: ""
  dependencyOf:
  - component:default_InferSvc-1
  lifecycle: Lifecycle
  owner: user:Owner
  profile:
    displayName: default_InferSvc-1
  type: unknown
`

	description2 = "description: KServe instance default:InferSvc-2"
	link21       = `  - icon: WebAsset
    title: API URL
    type: website
    url: https://kserve.com
`
	link22 = `  - icon: WebAsset
    title: transformer FastAPI URL
    type: website
    url: https://kserve.com/docs/docs
`
	link23 = `  - icon: WebAsset
    title: transformer model serving URL
    type: website
    url: https://kserve.com/docs
`
	link24 = `  - icon: WebAsset
    title: transformer REST model serving URL
    type: website
    url: https://kserve.com/rest
`
	link25 = `  - icon: WebAsset
    title: transformer GRPC model serving URL
    type: website
    url: https://kserve.com/grpc
`
	link26 = `  - icon: WebAsset
    title: predictor FastAPI URL
    type: website
    url: https://kserve.com/docs/docs
`
	link27 = `  - icon: WebAsset
    title: predictor model serving URL
    type: website
    url: https://kserve.com/docs
`
	link28 = `  - icon: WebAsset
    title: predictor REST model serving URL
    type: website
    url: https://kserve.com/rest
`
	link29 = `  - icon: WebAsset
    title: predictor GRPC model serving URL
    type: website
    url: https://kserve.com/grpc
`
	link30 = `  - icon: WebAsset
    title: explainer FastAPI URL
    type: website
    url: https://kserve.com/docs/docs
`
	link31 = `  - icon: WebAsset
    title: explainer model serving URL
    type: website
    url: https://kserve.com/docs
`
	link32 = `  - icon: WebAsset
    title: explainer REST model serving URL
    type: website
    url: https://kserve.com/rest
`
	link33 = `  - icon: WebAsset
    title: explainer GRPC model serving URL
    type: website
    url: https://kserve.com/grpc
`
	nameTags2 = `  name: default_InferSvc-2
  tags:
  - sklearn
  - xgboost
  - tensorflow
  - pytorch
  - triton
  - onnx
  - huggingface
  - pmml
  - lightgbm
  - paddle
  - f1-v1.0
  - squareattack
`
	compSpec2 = `spec:
  dependsOn:
  - resource:default_InferSvc-2
  - api:default_InferSvc-2
  lifecycle: Lifecycle
  owner: user:Owner
  profile:
    displayName: default_InferSvc-2
  providesApis:
  - default_InferSvc-2
  type: model-server
`
	resourceSpec2 = `spec:
  dependencyOf:
  - component:default_InferSvc-2
  lifecycle: Lifecycle
  owner: user:Owner
  profile:
    displayName: default_InferSvc-2
  providesApis:
  - default_InferSvc-2
  type: ai-model
`
	apiSpec2 = `spec:
  definition: ""
  dependencyOf:
  - component:default_InferSvc-2
  lifecycle: Lifecycle
  owner: user:Owner
  profile:
    displayName: default_InferSvc-2
  type: unknown
`
)
