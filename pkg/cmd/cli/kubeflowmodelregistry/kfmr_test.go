package kubeflowmodelregistry

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"

	serverapiv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kubeflow/model-registry/pkg/openapi"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/cli/backstage"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/config"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/types"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/util"
	"github.com/redhat-ai-dev/model-catalog-bridge/schema/types/golang"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/common"
	"github.com/redhat-ai-dev/model-catalog-bridge/test/stub/kfmr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestLoopOverKRMR_JsonArray(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	ts := kfmr.CreateGetServerWithInference(t)
	ethics := "some ethics related prose like you see on hugging face"
	howTo := "some curl or python invocation examples"
	support := "is this supported in a GA fashion, how to ask questions"
	training := "how the model was trained and perhaps fine tuned"
	usage := "some basic usage examples"
	license := "Apache-2"
	falsePtr := false
	homepageURL := "https://mymodel.io/welcome"
	defer ts.Close()
	for _, tc := range []struct {
		// we do output compare in chunks as ranges over the components status map are non-deterministic wrt order
		name  string
		outMc []golang.ModelCatalog
		is    *serverapiv1beta1.InferenceService
	}{
		{
			name: "2 model catalogs, neither with a model server",
			outMc: []golang.ModelCatalog{
				{
					Models: []golang.Model{
						{
							Description: "\nsimple model that does not require a GPU",
							Ethics:      &ethics,
							HowToUseURL: &howTo,
							Lifecycle:   util.DefaultLifecycle,
							Name:        "mnist-v1",
							Owner:       "kubeadmin",
							Support:     &support,
							Tags:        []string{"rhoai", "rhoai-model-registry", "matteos-lightweight-test-model", "v1", "grpc", "last-modified-time-2025-02-25-19-45-29-959"},
							Training:    &training,
							License:     &license,
							Usage:       &usage,
						},
					},
				},
				{
					Models: []golang.Model{
						{
							Description: "\nsimple model that does not require a GPU",
							Ethics:      &ethics,
							HowToUseURL: &howTo,
							Lifecycle:   util.DefaultLifecycle,
							Name:        "mnist-v3",
							Owner:       "kubeadmin",
							Support:     &support,
							Tags:        []string{"rhoai", "rhoai-model-registry", "matteos-lightweight-test-model", "v3", "grpc", "last-modified-time-2025-02-25-19-45-29-959"},
							Training:    &training,
							Usage:       &usage,
						},
					},
				},
			},
		},
		{
			name: "2 model catalogs, 1 has as model server",
			outMc: []golang.ModelCatalog{
				{
					Models: []golang.Model{
						{
							Description: "\nsimple model that does not require a GPU",
							Ethics:      &ethics,
							HowToUseURL: &howTo,
							Lifecycle:   util.DefaultLifecycle,
							Name:        "mnist-v1",
							Owner:       "kubeadmin",
							Support:     &support,
							Tags:        []string{"rhoai", "rhoai-model-registry", "matteos-lightweight-test-model", "v1", "grpc", "last-modified-time-2025-02-25-19-45-29-959"},
							Training:    &training,
							License:     &license,
							Usage:       &usage,
						},
					},
					ModelServer: &golang.ModelServer{
						API: &golang.API{
							Annotations: map[string]string{backstage.EXTERNAL_ROUTE_URL: "https://kserve.com"},
							Spec:        "a openapi spec string",
							Tags:        []string{"rhoai", "rhoai-model-registry", "matteos-lightweight-test-model", "v1", "last-modified-time-2025-02-25-19-45-29-959", "grpc"},
							Type:        golang.Grpc,
							URL:         "https://kserve.com",
						},
						Authentication: &falsePtr,
						Description:    "\nsimple model that does not require a GPU",
						HomepageURL:    &homepageURL,
						Lifecycle:      util.DefaultLifecycle,
						Name:           "mnist-v18c2c357f-bf82-4d2d-a254-43eca96fd31d",
						Owner:          "kubeadmin",
						Tags:           []string{"rhoai", "rhoai-model-registry", "matteos-lightweight-test-model", "v1", "last-modified-time-2025-02-25-19-45-29-959", "grpc"},
						Usage:          &usage,
					},
				},
				{
					Models: []golang.Model{
						{
							Description: "\nsimple model that does not require a GPU",
							Ethics:      &ethics,
							HowToUseURL: &howTo,
							Lifecycle:   util.DefaultLifecycle,
							Name:        "mnist-v3",
							Owner:       "kubeadmin",
							Support:     &support,
							Tags:        []string{"rhoai", "rhoai-model-registry", "matteos-lightweight-test-model", "v3", "grpc", "last-modified-time-2025-02-25-19-45-29-959"},
							Training:    &training,
							Usage:       &usage,
						},
					},
				},
			},
			is: &serverapiv1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					// see test/stub/common/MnistInferenceServices and test/stub/common/MinstServingEnvironment
					Namespace: "ggmtest",
					Name:      "mnist-v1",
				},
				Spec: serverapiv1beta1.InferenceServiceSpec{},
				Status: serverapiv1beta1.InferenceServiceStatus{URL: &apis.URL{
					Scheme: "https",
					Host:   "kserve.com",
				}},
			},
		},
	} {
		cfg := &config.Config{}
		kfmr.SetupKubeflowTestRESTClient(ts, cfg)
		k := SetupKubeflowRESTClient(cfg)
		ids := []string{}
		var cl client.Client
		if tc.is != nil {
			objs := []client.Object{tc.is}
			cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
		}
		rms, mvs, mas, err := LoopOverKFMR(ids, k)
		common.AssertError(t, err)
		common.AssertEqual(t, true, len(rms) > 0)
		common.AssertEqual(t, true, len(mvs) > 0)
		common.AssertEqual(t, true, len(mas) > 0)
		for _, rm := range rms {
			mva, ok := mvs[util.SanitizeName(rm.Name)]
			common.AssertEqual(t, true, ok)
			maa, ok2 := mas[util.SanitizeName(rm.Name)]
			common.AssertEqual(t, true, ok2)
			for _, mv := range mva {
				mvISL := []openapi.InferenceService{}
				mvISL, err = GetKubeFlowInferenceServicesForModelVersion(k, &mv)
				if err != nil {
					t.Logf("GetKubeFlowInferenceServicesForModelVersion err %s", err.Error())
					continue
				}
				b := []byte{}
				buf := bytes.NewBuffer(b)
				bwriter := bufio.NewWriter(buf)
				if len(mvISL) == 0 {
					err = CallBackstagePrinters(context.TODO(), util.DefaultOwner, util.DefaultLifecycle, &rm, &mv, maa[mv.Name], nil, tc.is, k, cl, bwriter, types.JsonArrayForamt)
				} else {
					err = CallBackstagePrinters(context.TODO(), util.DefaultOwner, util.DefaultLifecycle, &rm, &mv, maa[mv.Name], &mvISL[0], tc.is, k, cl, bwriter, types.JsonArrayForamt)
				}
				if err != nil {
					t.Logf("CallBackstagePrinters err %s", err.Error())
					continue
				}
				bwriter.Flush()
				// so the order of the tags array is random so we can't just do json as a string compare, so we have to
				// hydrate back to a &golang.ModelCatalog to compare fields
				outMc := &golang.ModelCatalog{}
				err = json.Unmarshal(buf.Bytes(), outMc)
				common.AssertError(t, err)
				found := false
				idx := 0
				for oidx, omc := range tc.outMc {
					idx = oidx
					tcModelServer := omc.ModelServer == nil
					outModelServer := outMc.ModelServer == nil
					if tcModelServer != outModelServer {
						t.Logf("model server diff oidx %d", oidx)
						continue
					}
					tcModels := omc.Models == nil
					outModels := outMc.Models == nil
					if tcModels != outModels {
						t.Logf("models diff nil check oidx %d", oidx)
						continue
					}
					if len(omc.Models) != len(outMc.Models) {
						t.Logf("models diff len oidx %d", oidx)
						continue
					}
					if len(omc.Models) > 0 {
						if len(outMc.Models) == 0 {
							t.Logf("models length mismatch oidx %d", oidx)
							continue
						}
						tcModel := omc.Models[0]
						outModel := outMc.Models[0]
						if tcModel.Name != outModel.Name {
							t.Logf("name mismatch oidx %d", oidx)
							continue
						}
						if tcModel.Description != outModel.Description {
							t.Logf("description mismatch oidx %d", oidx)
							continue
						}
						if tcModel.Lifecycle != outModel.Lifecycle {
							t.Logf("lifecycle mismatch oidx %d", oidx)
							continue
						}
						if tcModel.Owner != outModel.Owner {
							t.Logf("owner mismatch oidx %d", oidx)
							continue
						}
						if len(tcModel.Tags) != len(outModel.Tags) {
							t.Logf("tags len mismatch oidx %d", oidx)
							continue
						}
						for _, tag := range tcModel.Tags {
							tagFound := false
							for _, otag := range outModel.Tags {
								if otag == tag {
									tagFound = true
									break
								}
							}
							if !tagFound {
								t.Logf("tag %s not found oidx %d", tag, oidx)
								continue
							}
						}
						tcEthics := tcModel.Ethics == nil
						outEthics := outModel.Ethics == nil
						if tcEthics != outEthics {
							t.Logf("ethics nil mismatch oidx %d", oidx)
							continue
						}
						tcHowToUseURL := tcModel.HowToUseURL == nil
						outHowToUseURL := outModel.HowToUseURL == nil
						if tcHowToUseURL != outHowToUseURL {
							t.Logf("howToUseURL nil mismatch oidx %d", oidx)
							continue
						}
						tcSupport := tcModel.Support == nil
						outSupport := outModel.Support == nil
						if tcSupport != outSupport {
							t.Logf("support nil mismatch oidx %d", oidx)
							continue
						}
						tcTraining := tcModel.Training == nil
						outTraining := outModel.Training == nil
						if tcTraining != outTraining {
							t.Logf("training nil mismatch oidx %d", oidx)
							continue
						}
						tcLicense := tcModel.License == nil
						outLicense := outModel.License == nil
						if tcLicense != outLicense {
							t.Logf("license nil mismatch oidx %d", oidx)
							continue
						}
						tcUsage := tcModel.Usage == nil
						outUsage := outModel.Usage == nil
						if tcUsage != outUsage {
							t.Logf("usage nil mismatch oidx %d", oidx)
							continue
						}

						if tcModel.Ethics != nil {
							if *(tcModel.Ethics) != *(outModel.Ethics) {
								t.Logf("ethics mismatch oidx %d", oidx)
								continue
							}
						}
						if tcModel.HowToUseURL != nil {
							if *(tcModel.HowToUseURL) != *(outModel.HowToUseURL) {
								t.Logf("howToUseURL mismatch oidx %d", oidx)
								continue
							}
						}
						if tcModel.Support != nil {
							if *(tcModel.Support) != *(outModel.Support) {
								t.Logf("support mismatch oidx %d", oidx)
								continue
							}
						}
						if tcModel.Training != nil {
							if *(tcModel.Training) != *(outModel.Training) {
								t.Logf("training mismatch oidx %d", oidx)
								continue
							}
						}
						if tcModel.Usage != nil {
							if *(tcModel.Usage) != *(outModel.Usage) {
								t.Logf("usage mismatch oidx %d", oidx)
								continue
							}
						}
						if tcModel.License != nil {
							if *(tcModel.License) != *(outModel.License) {
								t.Logf("license mismatch oidx %d", oidx)
								continue
							}
						}
						if tcModel.Annotations != nil {
							if tcModel.Annotations[types.TechDocsKey] != outModel.Annotations[types.TechDocsKey] {
								t.Logf("annotation mismatch oidx %d", oidx)
								continue
							}
						}
					}
					if omc.ModelServer != nil {
						tms := omc.ModelServer
						oms := outMc.ModelServer
						if tms.Name != oms.Name {
							t.Logf("svr name mismatch oidx %d", oidx)
							continue
						}
						if tms.Description != oms.Description {
							t.Logf("svr description mismatch oidx %d", oidx)
							continue
						}
						if tms.Lifecycle != oms.Lifecycle {
							t.Logf("svr lifecycle mismatch oidx %d", oidx)
							continue
						}
						if tms.Owner != oms.Owner {
							t.Logf("svr owner mismatch oidx %d", oidx)
							continue
						}
						tmsAPI := tms.API == nil
						omsAPI := oms.API == nil
						if tmsAPI != omsAPI {
							t.Logf("svr api nil mismatch oidx %d", oidx)
							continue
						}
						tmsAuth := tms.Authentication == nil
						omsAuth := oms.Authentication == nil
						if tmsAuth != omsAuth {
							t.Logf("svr auth nil mismatch oidx %d", oidx)
							continue
						}
						tmsHomepage := tms.HomepageURL == nil
						omsHomepage := oms.HomepageURL == nil
						if tmsHomepage != omsHomepage {
							t.Logf("svr homepage nil mismatch oidx %d", oidx)
							continue
						}
						tmsUsage := tms.Usage == nil
						omsUsage := oms.Usage == nil
						if tmsUsage != omsUsage {
							t.Logf("svr usage nil mismatch oidx %d", oidx)
							continue
						}
						if len(oms.Tags) != len(tms.Tags) {
							t.Logf("svr tags len mismatch oidx %d", oidx)
							continue
						}
						if tms.API != nil {
							tmsAnnotations := tms.API.Annotations == nil
							omsAnnotations := oms.API.Annotations == nil
							if tmsAnnotations != omsAnnotations {
								t.Logf("svr annotations mismatch oidx %d", oidx)
								continue
							}
							if tms.API.Annotations != nil {
                                 if len(tms.API.Annotations) != len(oms.API.Annotations) {
                                      t.Logf("svr annotations len mismatch oidx %d", oidx)
                                      continue
                                 }
							}
							if tms.API.Spec != oms.API.Spec {
								t.Logf("svr api spec mismatch oidx %d", oidx)
								continue
							}
							if tms.API.URL != oms.API.URL {
								t.Logf("svr api url mismatch oidx %d", oidx)
								continue
							}
							if tms.API.Type != oms.API.Type {
								t.Logf("svr api type mismatch oidx %d", oidx)
								continue
							}
							if len(oms.API.Tags) != len(tms.API.Tags) {
								t.Logf("svr api tags len mismatch oidx %d", oidx)
								continue
							}
						}
						if tms.Authentication != nil {
							if *(tms.Authentication) != *(oms.Authentication) {
								t.Logf("svr authentication mismatch oidx %d", oidx)
								continue
							}
						}
						if tms.HomepageURL != nil {
							if *(tms.HomepageURL) != *(oms.HomepageURL) {
								t.Logf("svr homepage url mismatch oidx %d", oidx)
								continue
							}
						}
						if tms.Usage != nil {
							if *(tms.Usage) != *(oms.Usage) {
								t.Logf("svr usage mismatch oidx %d", oidx)
								continue
							}
						}
						for _, tag := range tms.Tags {
							foundTag := false
							for _, otag := range oms.Tags {
								if tag == otag {
									foundTag = true
									break
								}
							}
							if !foundTag {
								t.Logf("sbr tag %s mismatch oidx %d", tag, oidx)
								continue
							}
						}
					}
					found = true
					t.Logf("FOUND MATCH %s idx %d", tc.name, oidx)
					break
				}
				if !found {
					t.Errorf("did not find match for test %s idx %d and model catalog %#v", tc.name, idx, outMc)
				}

			}
		}

	}
}

func TestLoopOverKRMR_JsonArrayMultiModel(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = serverapiv1beta1.AddToScheme(scheme)
	ts := kfmr.CreateGetServerWithMixInferenceMultiModel(t)
	defer ts.Close()
	for _, tc := range []struct {
		// we do output compare in chunks as ranges over the components status map are non-deterministic wrt order
		outMc map[string]*golang.ModelCatalog
		is    *serverapiv1beta1.InferenceService
	}{
		{
			outMc: map[string]*golang.ModelCatalog{
				"1": {
					Models: []golang.Model{{
						Name: "granite-31-8b-lab-v1-140-v1",
					}},
					ModelServer: nil,
				},
				"3": {
					Models: []golang.Model{{
						Name: "granite-8b-code-instruct-140-v1",
					}},
					ModelServer: nil,
				},
				"5": {
					Models: []golang.Model{{
						Name: "mnist-v1",
					}},
					ModelServer: &golang.ModelServer{
						Name: "mnist-v10abd9005-9642-4cbf-848b-1c4da91c3437",
						API: &golang.API{
							URL: "https://kserve.com",
						},
					},
				}},
			is: &serverapiv1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					// see test/stub/common/MnistInferenceServices and test/stub/common/MinstServingEnvironment
					Namespace: "ggmtest",
					Name:      "mnist-v1",
				},
				Spec: serverapiv1beta1.InferenceServiceSpec{},
				Status: serverapiv1beta1.InferenceServiceStatus{URL: &apis.URL{
					Scheme: "https",
					Host:   "kserve.com",
				}},
			},
		},
	} {
		cfg := &config.Config{}
		kfmr.SetupKubeflowTestRESTClient(ts, cfg)
		k := SetupKubeflowRESTClient(cfg)
		ids := []string{}

		var cl client.Client
		if tc.is != nil {
			objs := []client.Object{tc.is}
			cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
		}
		rms, mvs, mas, err := LoopOverKFMR(ids, k)
		common.AssertError(t, err)
		common.AssertEqual(t, true, len(rms) > 0)
		common.AssertEqual(t, true, len(mvs) > 0)
		common.AssertEqual(t, true, len(mas) > 0)
		common.AssertEqual(t, true, len(rms) == len(tc.outMc))
		for _, rm := range rms {
			mva, ok := mvs[util.SanitizeName(rm.Name)]
			common.AssertEqual(t, true, ok)
			maa, ok2 := mas[util.SanitizeName(rm.Name)]
			common.AssertEqual(t, true, ok2)
			isl, e := k.ListInferenceServices()
			common.AssertError(t, e)
			b := []byte{}
			buf := bytes.NewBuffer(b)
			bwriter := bufio.NewWriter(buf)
			for _, mv := range mva {
				for _, is := range isl {
					err = CallBackstagePrinters(context.TODO(), util.DefaultOwner, util.DefaultLifecycle, &rm, &mv, maa[mv.Name], &is, tc.is, k, cl, bwriter, types.JsonArrayForamt)
					common.AssertError(t, err)
				}
			}
			bwriter.Flush()
			testMc, ok := tc.outMc[rm.GetId()]
			common.AssertEqual(t, true, ok)
			// so the order of the tags array is random so we can't just do json as a string compare, so we have to
			// hydrate back to a &golang.ModelCatalog to compare fields
			outMc := &golang.ModelCatalog{}
			err = json.Unmarshal(buf.Bytes(), outMc)
			common.AssertError(t, err)
			common.AssertEqual(t, testMc.ModelServer == nil, outMc.ModelServer == nil)
			common.AssertEqual(t, testMc.Models == nil, outMc.Models == nil)
			common.AssertEqual(t, len(testMc.Models), len(outMc.Models))
			if len(testMc.Models) > 0 {
				common.AssertEqual(t, testMc.Models[0].Name, outMc.Models[0].Name)
			}
			if testMc.ModelServer != nil {
				common.AssertEqual(t, testMc.ModelServer.Name, outMc.ModelServer.Name)
				common.AssertEqual(t, testMc.ModelServer.API == nil, outMc.ModelServer.API == nil)
				if testMc.ModelServer.API != nil {
					common.AssertEqual(t, testMc.ModelServer.API.URL, outMc.ModelServer.API.URL)
				}
			}
		}

	}
}
func TestLoopOverKFMR_CatalogInfoYaml(t *testing.T) {
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
		rms, mvs, mas, err := LoopOverKFMR(ids, k)
		common.AssertError(t, err)
		common.AssertError(t, err)
		common.AssertEqual(t, true, len(rms) > 0)
		common.AssertEqual(t, true, len(mvs) > 0)
		common.AssertEqual(t, true, len(mas) > 0)
		for _, rm := range rms {
			mva, ok := mvs[util.SanitizeName(rm.Name)]
			common.AssertEqual(t, true, ok)
			maa, ok2 := mas[util.SanitizeName(rm.Name)]
			common.AssertEqual(t, true, ok2)
			isl, _ := k.ListInferenceServices()
			for _, mv := range mva {
				for _, is := range isl {
					err = CallBackstagePrinters(context.TODO(), owner, lifecycle, &rm, &mv, maa[mv.Name], &is, nil, k, nil, bwriter, types.CatalogInfoYamlFormat)
					common.AssertError(t, err)
				}
			}
		}
		bwriter.Flush()
		outstr := buf.String()
		for _, str := range tc.outStr {
			common.AssertLineCompare(t, outstr, str, 0)
		}

	}

}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Basic inference name",
			input:    "InferenceServer123",
			expected: "InferenceServer123",
		},
		{
			name:     "Name with valid special characters",
			input:    "Inference_Server123",
			expected: "Inference_Server123",
		},
		{
			name:     "Name with invalid characters",
			input:    "Inference_Server#$Test",
			expected: "Inference_ServerTest",
		},
		{
			name:     "Name with beginning and ending invalid characters",
			input:    ".-ValidName-_.",
			expected: "ValidName",
		},
		{
			name:     "Name with beginning and ending invalid characters",
			input:    "Test-Name--Tester",
			expected: "Test-NameTester",
		},
		{
			name:     "Valid name with length greater than 63",
			input:    "InferenceServer" + strings.Repeat("b", 64) + "test",
			expected: "InferenceServer" + strings.Repeat("b", 48),
		},
		{
			name:     "Invalid name with only special characters",
			input:    "!@#$%^&*()",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := util.SanitizeName(tt.input)
			common.AssertEqual(t, tt.expected, result)
		})
	}
}

const (
	jsonListWithInferenceOutputJSON = `{"models":[{"artifactLocationURL":"https://huggingface.co/tarilabs/mnist/resolve/v20231206163028/mnist.onnx","description":"","lifecycle":"Lifecycle","name":"mnist-v1","owner":"rhdh-rhoai-bridge"}],"modelServer":{"API":{"spec":"TBD","type":"openapi","url":"https://kserve.com"},"authentication":false,"description":"","lifecycle":"development","name":"mnist-v18c2c357f-bf82-4d2d-a254-43eca96fd31d","owner":"rhdh-rhoai-bridge","tags":["LastModifiedTime_2025-02-25T19:45:29.959Z"]}}`
	jsonListWithInferenceOutputYAML = `modelServer:
  API:
    spec: ""
    type: openapi
    url: https://kserve.com
  authentication: false
  description: ""
  lifecycle: development
  name: mnist-v1/8c2c357f-bf82-4d2d-a254-43eca96fd31d
  owner: rhdh-rhoai-bridge
  tags:
  - _lastModified
models:
- artifactLocationURL: https://huggingface.co/tarilabs/mnist/resolve/v20231206163028/mnist.onnx
  description: ""
  lifecycle: Lifecycle
  name: v1
  owner: rhdh-rhoai-bridge
  tags:
  - _lastModified`
	jsonListOutputJSON = `{"models":[{"artifactLocationURL":"https://huggingface.co/tarilabs/mnist/resolve/v20231206163028/mnist.onnx","description":"","ethics":"some ethics related prose like you see on hugging face","howToUseURL":"some curl or python invocation examples","lifecycle":"Lifecycle","name":"mnist-v1","owner":"rhdh-rhoai-bridge","support":"is this supported in a GA fashion, how to ask questions","tags":["rhoai","v1","rhoai-model-registry","matteos-lightweight-test-model"],"training":"how the model was trained and perhaps fine tuned","license": "Apache-2","usage":"some basic usage examples"}]}`
	jsonListOutputYAML = `models:
- artifactLocationURL: https://huggingface.co/tarilabs/mnist/resolve/v20231206163028/mnist.onnx
  description: ""
  lifecycle: Lifecycle
  name: v1
  owner: rhdh-rhoai-bridge
  tags:
  - _lastModified
`
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
  owner: user:Owner
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
  owner: user:Owner
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
  owner: user:Owner
  profile:
    displayName: model-1
  type: unknown
`
)
