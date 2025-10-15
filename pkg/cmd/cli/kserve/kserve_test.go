package kserve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"testing"

	serverapiv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	fakeservingv1beta1 "github.com/kserve/kserve/pkg/client/clientset/versioned/fake"
     "github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/cli/backstage"
     "github.com/redhat-ai-dev/model-catalog-bridge/pkg/config"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/types"
	"github.com/redhat-ai-dev/model-catalog-bridge/schema/types/golang"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func setupConfig(cfg *config.Config, obj serverapiv1beta1.InferenceService) {
	cfg.ServingClient = fakeservingv1beta1.NewSimpleClientset().ServingV1beta1()
	cfg.ServingClient.InferenceServices(obj.Namespace).Create(context.TODO(), &obj, metav1.CreateOptions{})
	cfg.Namespace = obj.Namespace

}

func TestKserveBackstagePrinters(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	for _, tc := range []struct {
		name string
		args []string
		is   serverapiv1beta1.InferenceService
		mc   *golang.ModelCatalog
	}{
		{
			name: "Owner and Lifecycle set and data and url",
			args: []string{"Owner", "Lifecycle"},
			is: serverapiv1beta1.InferenceService{

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
			mc: &golang.ModelCatalog{

				Models: []golang.Model{
					{
						Name:        "default-InferSvc-1",
						Description: "",
						Lifecycle:   "Lifecycle",
						Owner:       "Owner",
					},
				},
				ModelServer: &golang.ModelServer{
					API: &golang.API{
						Spec: "TBD",
						Type: "openapi",
						URL:  "https://kserve.com",
                        Annotations: map[string]string{
                             backstage.EXTERNAL_ROUTE_URL: "https://kserve.com",
                        },
					},
					Authentication: &falseVal,
					Description:    "",
					Lifecycle:      "Lifecycle",
					Name:           "default-InferSvc-1",
					Owner:          "Owner",
				},
			},
		},
		{
			name: "use everything including bunch of tags",
			args: []string{"Owner", "Lifecycle"},
			is: serverapiv1beta1.InferenceService{

				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "InferSvc-2",
					Annotations: map[string]string{
						types.AnnotationPrefix + fixKeyForAnnotation(types.EthicsKey):      types.EthicsKey,
						types.AnnotationPrefix + fixKeyForAnnotation(types.HowToUseKey):    types.HowToUseKey,
						types.AnnotationPrefix + fixKeyForAnnotation(types.SupportKey):     types.SupportKey,
						types.AnnotationPrefix + fixKeyForAnnotation(types.TrainingKey):    types.TrainingKey,
						types.AnnotationPrefix + fixKeyForAnnotation(types.UsageKey):       types.UsageKey,
						types.AnnotationPrefix + fixKeyForAnnotation(types.HomepageURLKey): types.HomepageURLKey,
						types.AnnotationPrefix + fixKeyForAnnotation(types.APISpecKey):     types.APISpecKey,
						types.AnnotationPrefix + fixKeyForAnnotation(types.APITypeKey):     string(golang.Openapi),
						types.AnnotationPrefix + fixKeyForAnnotation(types.Owner):          types.Owner,
						types.AnnotationPrefix + fixKeyForAnnotation(types.Lifecycle):      types.Lifecycle,
						types.AnnotationPrefix + fixKeyForAnnotation(types.TechDocsKey):    types.TechDocsKey,
						types.AnnotationPrefix + fixKeyForAnnotation(types.LicenseKey):     types.LicenseKey,
						types.AnnotationPrefix + fixKeyForAnnotation(types.DescriptionKey): types.DescriptionKey,
					},
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
			mc: &golang.ModelCatalog{

				Models: []golang.Model{
					{
						Name:        "default-InferSvc-2",
						Description: types.DescriptionKey,
						Lifecycle:   types.Lifecycle,
						Owner:       types.Owner,
						Ethics:      &ethicsKey,
						HowToUseURL: &howToUseKey,
						License:     &license,
						Support:     &support,
						Training:    &training,
						Usage:       &usage,
					},
				},
				ModelServer: &golang.ModelServer{
					API: &golang.API{
						Spec: types.APISpecKey,
						Type: golang.Openapi,
						URL:  "https://kserve.com",
                        Annotations: map[string]string{
                             backstage.EXTERNAL_ROUTE_URL: "https://kserve.com/",
                        },
					},
					Authentication: &falseVal,
					Description:    types.DescriptionKey,
					Lifecycle:      types.Lifecycle,
					Owner:          types.Owner,
					Name:           "default-InferSvc-2",
					HomepageURL:    &homepage,
					Usage:          &usage,
				},
			},
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
		for _, is := range isl.Items {
			b := []byte{}
			buf := bytes.NewBuffer(b)
			bwriter := bufio.NewWriter(buf)
			objs := []client.Object{}
			objs = append(objs, &is)
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

			err = CallBackstagePrinters(context.Background(), owner, lifecycle, &is, c, bwriter, types.JsonArrayForamt)
			common.AssertError(t, err)
			bwriter.Flush()
			// so the order of the tags array is random so we can't just do json as a string compare, so we have to
			// hydrate back to a &golang.ModelCatalog to compare fields
			outMc := &golang.ModelCatalog{}
			err = json.Unmarshal(buf.Bytes(), outMc)
			common.AssertError(t, err)
			common.AssertEqual(t, len(tc.mc.Models), len(outMc.Models))
			common.AssertEqual(t, tc.mc.ModelServer != nil, outMc.ModelServer != nil)
			for i, m := range tc.mc.Models {
				om := outMc.Models[i]
				common.AssertEqual(t, m.ArtifactLocationURL, om.ArtifactLocationURL)
				common.AssertEqual(t, m.Description, om.Description)
				common.AssertEqual(t, m.Ethics != nil, om.Ethics != nil)
				if m.Ethics != nil {
					common.AssertEqual(t, *m.Ethics, *om.Ethics)
				}
				common.AssertEqual(t, m.HowToUseURL != nil, om.HowToUseURL != nil)
				if m.HowToUseURL != nil {
					common.AssertEqual(t, *m.HowToUseURL, *om.HowToUseURL)
				}
				common.AssertEqual(t, m.License != nil, om.License != nil)
				if m.License != nil {
					common.AssertEqual(t, *m.License, *om.License)
				}
				common.AssertEqual(t, m.Lifecycle, om.Lifecycle)
				common.AssertEqual(t, m.Name, om.Name)
				common.AssertEqual(t, m.Owner, om.Owner)
				common.AssertEqual(t, m.Support != nil, om.Support != nil)
				if m.Support != nil {
					common.AssertEqual(t, *m.Support, *om.Support)
				}
				common.AssertEqual(t, m.Training != nil, om.Training != nil)
				if m.Training != nil {
					common.AssertEqual(t, *m.Training, *om.Training)
				}
				common.AssertEqual(t, m.Usage != nil, om.Usage != nil)
				if m.Usage != nil {
					common.AssertEqual(t, *m.Usage, *om.Usage)
				}
			}
			if tc.mc.ModelServer != nil {
				common.AssertEqual(t, tc.mc.ModelServer.API != nil, outMc.ModelServer.API != nil)
				if tc.mc.ModelServer.API != nil {
					tm := tc.mc.ModelServer.API
					om := outMc.ModelServer.API
					common.AssertEqual(t, tm.Spec, om.Spec)
					common.AssertEqual(t, tm.Type, om.Type)
					common.AssertEqual(t, tm.URL, om.URL)
				}
				tm := tc.mc.ModelServer
				om := outMc.ModelServer
				common.AssertEqual(t, tm.Authentication != nil, om.Authentication != nil)
				if tm.Authentication != nil {
					common.AssertEqual(t, *tm.Authentication, *om.Authentication)
				}
				common.AssertEqual(t, tm.Description, om.Description)
				common.AssertEqual(t, tm.HomepageURL != nil, om.HomepageURL != nil)
				if tm.HomepageURL != nil {
					common.AssertEqual(t, *tm.HomepageURL, *om.HomepageURL)
				}
				common.AssertEqual(t, tm.Lifecycle, om.Lifecycle)
				common.AssertEqual(t, tm.Name, om.Name)
				common.AssertEqual(t, tm.Usage != nil, om.Usage != nil)
				if tm.Usage != nil {
					common.AssertEqual(t, *tm.Usage, *om.Usage)
				}

				tmAPI := tm.API
				omAPI := outMc.ModelServer.API
				tmAPISet := tmAPI == nil
				omAPISet := outMc.ModelServer.API == nil
				if tmAPISet != omAPISet {
					t.Logf("api set mismatch %s tm %v om %v", tc.name, tmAPISet, omAPISet)
					common.AssertEqual(t, tmAPISet, omAPISet)
				}
				if tmAPI != nil {
					tmAnno := tmAPI.Annotations == nil
					omAnno := omAPI == nil
					if tmAnno != omAnno {
						t.Logf("api annontation mismatch %s tm %v om %v", tc.name, tmAnno, omAnno)
						common.AssertEqual(t, tmAnno, omAnno)
					}
					if tmAPI.Annotations != nil {
						if len(tmAPI.Annotations) != len(tmAPI.Annotations) {
							t.Logf("api num of annotations mismatch %s tm %d om %d", tc.name, len(tmAPI.Annotations), len(omAPI.Annotations))
                            common.AssertEqual(t, len(tmAPI.Annotations), len(omAPI.Annotations))
						}
					}
				}

			}
		}

	}
}

var (
	version     = "v1.0"
	falseVal    = false
	trueVal     = true
	ethicsKey   = types.EthicsKey
	howToUseKey = types.HowToUseKey
	license     = types.LicenseKey
	support     = types.SupportKey
	training    = types.TrainingKey
	usage       = types.UsageKey
	homepage    = types.HomepageURLKey
)
