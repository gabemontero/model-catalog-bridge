package kubeflowmodelregistry

import (
	"context"
	"fmt"
	"io"
	corev1 "k8s.io/api/core/v1"
	"regexp"
	"strings"

	serverv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kubeflow/model-registry/pkg/openapi"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/cli/backstage"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/cli/kserve"
	brdgtypes "github.com/redhat-ai-dev/model-catalog-bridge/pkg/types"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/util"
	"github.com/redhat-ai-dev/model-catalog-bridge/schema/types/golang"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (

	// pulled from makeValidator.ts in the catalog-model package in core backstage
	tagRegexp = "^[a-z0-9:+#]+(\\-[a-z0-9:+#]+)*$"

	nameInvalidCharRegexp = `[^a-zA-Z0-9\-_.]`

	nameNoDuplicateSpecialCharRegexp = `[-_.]{2,}`
)

func LoopOverKFMR(ids []string, kfmr *KubeFlowRESTClientWrapper) ([]openapi.RegisteredModel, map[string][]openapi.ModelVersion, map[string]map[string][]openapi.ModelArtifact, error) {
	var err error
	rmArray := []openapi.RegisteredModel{}
	mvsMap := map[string][]openapi.ModelVersion{}
	masMap := map[string]map[string][]openapi.ModelArtifact{}

	if len(ids) == 0 {
		var rms []openapi.RegisteredModel
		rms, err = kfmr.ListRegisteredModels()
		if err != nil {
			klog.Errorf("list registered models error: %s", err.Error())
			klog.Flush()
			return nil, nil, nil, err
		}
		for _, rm := range rms {
			if rm.State != nil && *rm.State == openapi.REGISTEREDMODELSTATE_ARCHIVED {
				klog.V(4).Infof("LoopOverKFMR skipping archived registered model %s", rm.Name)
				continue
			}
			var mvs []openapi.ModelVersion
			var mas map[string][]openapi.ModelArtifact
			mvs, mas, err = callKubeflowREST(*rm.Id, kfmr)
			if err != nil {
				klog.Errorf("%s", err.Error())
				klog.Flush()
				return nil, nil, nil, err
			}

			rmArray = append(rmArray, rm)
			mvsMap[rm.Name] = mvs
			masMap[rm.Name] = mas
		}
	} else {
		for _, id := range ids {
			var rm *openapi.RegisteredModel
			rm, err = kfmr.GetRegisteredModel(id)
			if err != nil {
				klog.Errorf("get registered model error for %s: %s", id, err.Error())
				klog.Flush()
				return nil, nil, nil, err
			}
			if rm.State != nil && *rm.State == openapi.REGISTEREDMODELSTATE_ARCHIVED {
				klog.V(4).Infof("LoopOverKFMR skipping archived registered model %s", rm.Name)
				continue
			}
			var mvs []openapi.ModelVersion
			var mas map[string][]openapi.ModelArtifact
			mvs, mas, err = callKubeflowREST(*rm.Id, kfmr)
			if err != nil {
				klog.Errorf("get model version/artifact error for %s: %s", id, err.Error())
				klog.Flush()
				return nil, nil, nil, err
			}
			rmArray = append(rmArray, *rm)
			mvsMap[rm.Name] = mvs
			masMap[rm.Name] = mas
		}
	}
	return rmArray, mvsMap, masMap, nil
}

func callKubeflowREST(id string, kfmr *KubeFlowRESTClientWrapper) (mvs []openapi.ModelVersion, ma map[string][]openapi.ModelArtifact, err error) {
	mvs, err = kfmr.ListModelVersions(id)
	if err != nil {
		klog.Errorf("ERROR: error list model versions for %s: %s", id, err.Error())
		return
	}
	ma = map[string][]openapi.ModelArtifact{}
	for _, mv := range mvs {
		if mv.State != nil && *mv.State == openapi.MODELVERSIONSTATE_ARCHIVED {
			klog.V(4).Infof("callKubeflowREST skipping archived model version %s", mv.Name)
			continue
		}
		var v []openapi.ModelArtifact
		v, err = kfmr.ListModelArtifacts(*mv.Id)
		if err != nil {
			klog.Errorf("ERROR error list model artifacts for %s:%s: %s", id, *mv.Id, err.Error())
			return
		}
		if len(v) == 0 {
			v, err = kfmr.ListModelArtifacts(id)
			if err != nil {
				klog.Errorf("ERROR error list model artifacts for %s:%s: %s", id, *mv.Id, err.Error())
				return
			}
		}
		ma[*mv.Id] = v
	}
	return
}

// json array schema populator

type CommonSchemaPopulator struct {
	// reuse the component populator as it houses all the KFMR artifacts of noew
	ComponentPopulator
}

type ModelCatalogPopulator struct {
	CommonSchemaPopulator
	MSPop *ModelServerPopulator
	MPops []*ModelPopulator
}

func (m *ModelCatalogPopulator) GetModels() []golang.Model {
	models := []golang.Model{}
	for mvidx, mv := range m.ModelVersions {
		mPop := m.MPops[mvidx]
		mPop.MVIndex = mvidx
		mas := m.ModelArtifacts[mv.GetId()]
		for maidx, ma := range mas {
			if ma.GetId() == m.RegisteredModel.GetId() {
				mPop.MAIndex = maidx
				break
			}
		}

		model := golang.Model{
			ArtifactLocationURL: mPop.GetArtifactLocationURL(),
			Description:         mPop.GetDescription(),
			Ethics:              mPop.GetEthics(),
			HowToUseURL:         mPop.GetHowToUseURL(),
			Lifecycle:           mPop.Lifecycle,
			Name:                mPop.GetName(),
			Owner:               mPop.GetOwner(),
			Support:             mPop.GetSupport(),
			Tags:                mPop.GetTags(),
			Training:            mPop.GetTraining(),
			Usage:               mPop.GetUsage(),
		}
		models = append(models, model)
	}
	return models
}

func (m *ModelCatalogPopulator) GetModelServer() *golang.ModelServer {
	infSvcIdx := 0
	mvIndex := 0
	maIndex := 0

	kfmrIS := openapi.InferenceService{}
	foundInferenceService := false
	for isidx, is := range m.InferenceServices {
		if is.RegisteredModelId == m.RegisteredModel.GetId() {
			infSvcIdx = isidx
			kfmrIS = is
			foundInferenceService = true
			break
		}
	}

	if !foundInferenceService {
		return nil
	}

	mas := []openapi.ModelArtifact{}
	for mvidx, mv := range m.ModelVersions {
		if mv.RegisteredModelId == m.RegisteredModel.GetId() && mv.GetId() == kfmrIS.GetModelVersionId() {
			mvIndex = mvidx
			mas = m.ModelArtifacts[mv.GetId()]
			break
		}
	}

	// reminder based on explanations about model artifact actually being the "root" of their model, and what has been observed in testing,
	// the ID for the registered model and model artifact appear to match
	maId := m.RegisteredModel.GetId()
	for maidx, ma := range mas {
		if ma.GetId() == maId {
			maIndex = maidx
			break
		}
	}

	m.MSPop.InfSvcIndex = infSvcIdx
	m.MSPop.MVIndex = mvIndex
	m.MSPop.MAIndex = maIndex

	return &golang.ModelServer{
		API:            m.MSPop.GetAPI(),
		Authentication: m.MSPop.GetAuthentication(),
		Description:    m.MSPop.GetDescription(),
		HomepageURL:    m.MSPop.GetHomepageURL(),
		Lifecycle:      m.MSPop.GetLifecycle(),
		Name:           m.MSPop.GetName(),
		Owner:          m.MSPop.GetOwner(),
		Tags:           m.MSPop.GetTags(),
		Usage:          m.MSPop.GetUsage(),
	}

	return nil
}

type ModelPopulator struct {
	CommonSchemaPopulator
	MVIndex int
	MAIndex int
}

func (m *ModelPopulator) GetName() string {
	if len(m.ModelVersions) > m.MVIndex {
		mv := m.ModelVersions[m.MVIndex]
		//GGM to John Collier: during the demo, we talked about the Resource name being the same name
		// we put in the model name field of the chat application template, i.e. the name/id we see from /v1/models
		// query of the model's REST API.  Pretty sure our combo ^^ is not quite that, as the ID from /v1/models had
		// the dots removed.  So I am adding that removal here.
		name := m.RegisteredModel.Name + "-" + mv.GetName()
		replacer := strings.NewReplacer(".", "")
		name = replacer.Replace(name)
		return name
	}
	return ""
}

func (m *ModelPopulator) GetOwner() string {
	//TODO need to specify a well known k/v pair or env var for default
	return util.DefaultOwner
}

func (m *ModelPopulator) GetLifecycle() string {
	//TODO need to specify a well known k/v pair or env var for default
	return util.DefaultLifecycle
}

func (m *ModelPopulator) GetDescription() string {
	desc := ""
	if len(m.ModelVersions) > m.MVIndex {
		mv := m.ModelVersions[m.MVIndex]
		desc = mv.GetDescription()
	}
	if len(desc) == 0 {
		return m.RegisteredModel.GetDescription()
	}
	return desc
}

func (m *ModelPopulator) GetTags() []string {
	tags := []string{}
	regex, _ := regexp.Compile(tagRegexp)
	if len(m.ModelVersions) > m.MVIndex {
		mv := m.ModelVersions[m.MVIndex]
		if mv.HasCustomProperties() {
			for cpk, cpv := range mv.GetCustomProperties() {
				switch {
				case cpk == brdgtypes.RHOAIModelCatalogSourceModelVersion:
					fallthrough
				case cpk == brdgtypes.RHOAIModelCatalogSourceModelKey:
					fallthrough
				case cpk == brdgtypes.RHOAIModelCatalogRegisteredFromKey:
					fallthrough
				case cpk == brdgtypes.RHOAIModelCatalogProviderKey:
					fallthrough
				case cpk == brdgtypes.RHOAIModelRegistryRegisteredFromCatalogRepositoryName:
					v := ""
					if cpv.MetadataStringValue != nil {
						v = cpv.MetadataStringValue.StringValue
					}
					if len(v) > 0 && regex.MatchString(v) && len(v) <= 63 {
						tags = append(tags, v)
					}
				case cpk == brdgtypes.RHOAIModelRegistryLastModified:
					v := ""
					if cpv.MetadataStringValue != nil {
						v = cpv.MetadataStringValue.StringValue
						v = fmt.Sprintf("LastModifiedTime_%s", v)
					}
					if len(v) > 0 && regex.MatchString(v) && len(v) <= 63 {
						tags = append(tags, v)
					}
				default:
					continue
				}
			}
		}
		// any MA custom props will be user defined so just add
		mas, ok := m.ModelArtifacts[mv.Name]
		if ok {
			ma := mas[m.MAIndex]
			if ma.HasCustomProperties() {
				for _, cpv := range ma.GetCustomProperties() {
					if cpv.MetadataStringValue == nil {
						continue
					}
					v := cpv.MetadataStringValue.StringValue
					if regex.MatchString(v) && len(v) <= 63 {
						tags = append(tags, v)
					}
				}
			}
		}
	}
	return tags
}

func (m *ModelPopulator) GetArtifactLocationURL() *string {
	if len(m.ModelVersions) > m.MVIndex {
		mv := m.ModelVersions[m.MVIndex]
		mas, ok := m.ModelArtifacts[mv.GetId()]
		if ok {
			if len(mas) > m.MAIndex {
				ma := mas[m.MAIndex]
				return ma.Uri
			}
		}
	}
	return nil
}

func (m *ModelPopulator) getStringPropVal(key string) *string {
	if len(m.ModelVersions) <= m.MVIndex {
		return nil
	}
	mv := m.ModelVersions[m.MVIndex]
	if !mv.HasCustomProperties() {
		return nil
	}
	vmap, ok := mv.GetCustomPropertiesOk()
	if !ok || vmap == nil {
		return nil
	}
	v, o := (*vmap)[key]
	if !o {
		return nil
	}
	if v.MetadataStringValue != nil {
		return &v.MetadataStringValue.StringValue
	}
	return nil
}

func (m *ModelPopulator) GetEthics() *string {
	return m.getStringPropVal(brdgtypes.EthicsKey)
}

func (m *ModelPopulator) GetHowToUseURL() *string {
	return m.getStringPropVal(brdgtypes.HowToUseKey)
}

func (m *ModelPopulator) GetSupport() *string {
	return m.getStringPropVal(brdgtypes.SupportKey)
}

func (m *ModelPopulator) GetTraining() *string {
	return m.getStringPropVal(brdgtypes.TrainingKey)
}

func (m *ModelPopulator) GetUsage() *string {
	return m.getStringPropVal(brdgtypes.UsageKey)
}

type ModelServerPopulator struct {
	CommonSchemaPopulator
	ApiPop      ModelServerAPIPopulator
	InfSvcIndex int
	MVIndex     int
	MAIndex     int
}

func (m *ModelServerPopulator) getStringPropVal(key string) *string {
	if len(m.ModelVersions) <= m.MVIndex {
		return nil
	}
	mv := m.ModelVersions[m.MVIndex]
	if !mv.HasCustomProperties() {
		return nil
	}
	vmap, ok := mv.GetCustomPropertiesOk()
	if !ok || vmap == nil {
		return nil
	}
	v, o := (*vmap)[key]
	if !o {
		return nil
	}
	if v.MetadataStringValue != nil {
		return &v.MetadataStringValue.StringValue
	}
	return nil
}

func (m *ModelServerPopulator) GetUsage() *string {
	return m.getStringPropVal(brdgtypes.UsageKey)
}

func (m *ModelServerPopulator) GetHomepageURL() *string {
	return m.getStringPropVal(brdgtypes.HomepageURLKey)
}

func (m *ModelServerPopulator) GetAuthentication() *bool {
	auth := false
	// auth is configured, a service account whose name is prefixed with the inference service's name, and with the
	// inference service set as an owner reference; so we'll look for the owner ref in case the naming apporach changes
	if m.Kis == nil {
		return &auth
	}
	listOptions := &client.ListOptions{Namespace: m.Kis.Namespace}
	saList := &corev1.ServiceAccountList{}
	err := m.CtrlClient.List(m.Ctx, saList, listOptions)
	if err != nil {
		return &auth
	}
	for _, sa := range saList.Items {
		if sa.OwnerReferences == nil {
			continue
		}
		for _, o := range sa.OwnerReferences {
			if o.Kind == "InferenceService" &&
				o.Name == m.Kis.Name {
				auth = true
				break
			}
		}
	}
	return &auth
}

// GetName returns the inference server name, sanitized to meet the following criteria
// "a string that is sequences of [a-zA-Z0-9] separated by any of [-_.], at most 63 characters in total"
func (m *ModelServerPopulator) GetName() string {
	if len(m.InferenceServices) > m.InfSvcIndex {
		sanitizedName := sanitizeName(m.InferenceServices[m.InfSvcIndex].GetName())
		return sanitizedName
	}
	return ""
}

func sanitizeName(name string) string {
	sanitizedName := name

	// Replace any invalid characters with an empty character
	validChars := regexp.MustCompile(nameInvalidCharRegexp)
	sanitizedName = validChars.ReplaceAllString(sanitizedName, "")

	// Remove duplicated special characters
	noDupeChars := regexp.MustCompile(nameNoDuplicateSpecialCharRegexp)
	sanitizedName = noDupeChars.ReplaceAllString(sanitizedName, "")

	// Trim to no more than 63 characters
	if len(sanitizedName) > 63 {
		sanitizedName = sanitizedName[:63]
	}

	// Finally, ensure only alphanumeric characters at beginning and end of the name
	sanitizedName = strings.Trim(sanitizedName, "-_.")
	return sanitizedName

}

func (m *ModelServerPopulator) GetTags() []string {
	tags := m.ApiPop.GetTags()
	regex, _ := regexp.Compile(tagRegexp)
	if len(m.ModelVersions) > m.MVIndex {
		mv := m.ModelVersions[m.MVIndex]
		if mv.HasCustomProperties() {
			for cpk, cpv := range mv.GetCustomProperties() {
				switch {
				case cpk == brdgtypes.RHOAIModelCatalogSourceModelVersion:
					fallthrough
				case cpk == brdgtypes.RHOAIModelCatalogSourceModelKey:
					fallthrough
				case cpk == brdgtypes.RHOAIModelCatalogRegisteredFromKey:
					fallthrough
				case cpk == brdgtypes.RHOAIModelCatalogProviderKey:
					fallthrough
				case cpk == brdgtypes.RHOAIModelRegistryRegisteredFromCatalogRepositoryName:
					v := ""
					if cpv.MetadataStringValue != nil {
						v = cpv.MetadataStringValue.StringValue
					}
					if len(v) > 0 && regex.MatchString(v) && len(v) <= 63 {
						tags = append(tags, v)
					}
				case cpk == brdgtypes.RHOAIModelRegistryLastModified:
					v := ""
					if cpv.MetadataStringValue != nil {
						v = cpv.MetadataStringValue.StringValue
						v = fmt.Sprintf("LastModifiedTime_%s", v)
					}
					if len(v) > 0 {
						tags = append(tags, v)
					}
				default:
					continue
				}
			}
		}
		// any MA custom props will be user defined so just add
		mas, ok := m.ModelArtifacts[mv.Name]
		if ok {
			if len(mas) > m.MAIndex {
				ma := mas[m.MAIndex]
				if ma.HasCustomProperties() {
					for _, cpv := range ma.GetCustomProperties() {
						if cpv.MetadataStringValue == nil {
							continue
						}
						v := cpv.MetadataStringValue.StringValue
						if regex.MatchString(v) && len(v) <= 63 {
							tags = append(tags, v)
						}
					}
				}

			}
		}
	}
	return tags
}

func (m *ModelServerPopulator) GetAPI() *golang.API {
	m.ApiPop.MVIndex = m.MVIndex
	m.ApiPop.MAIndex = m.MAIndex
	m.ApiPop.Ctx = m.Ctx
	api := &golang.API{
		Spec: m.ApiPop.GetSpec(),
		Tags: m.ApiPop.GetTags(),
		Type: m.ApiPop.GetType(),
		URL:  m.ApiPop.GetURL(),
	}
	return api
}

func (m *ModelServerPopulator) GetOwner() string {
	//TODO need to specify a well known k/v pair or env var for default
	return util.DefaultOwner
}

func (m *ModelServerPopulator) GetLifecycle() string {
	//TODO need to specify a well known k/v pair or env var for default
	return util.DefaultLifecycle
}

func (m *ModelServerPopulator) GetDescription() string {
	desc := ""
	if len(m.ModelVersions) > m.MVIndex {
		mv := m.ModelVersions[m.MVIndex]
		desc = mv.GetDescription()
	}
	if len(desc) == 0 {
		return m.RegisteredModel.GetDescription()
	}
	return desc
}

type ModelServerAPIPopulator struct {
	CommonSchemaPopulator
	MVIndex int
	MAIndex int
}

func (m *ModelServerAPIPopulator) getStringPropVal(key string) *string {
	if len(m.ModelVersions) <= m.MVIndex {
		return nil
	}
	mv := m.ModelVersions[m.MVIndex]
	if !mv.HasCustomProperties() {
		return nil
	}
	vmap, ok := mv.GetCustomPropertiesOk()
	if !ok || vmap == nil {
		return nil
	}
	v, o := (*vmap)[key]
	if !o {
		return nil
	}
	if v.MetadataStringValue != nil {
		return &v.MetadataStringValue.StringValue
	}

	if !m.RegisteredModel.HasCustomProperties() {
		return nil
	}

	vmap, ok = m.RegisteredModel.GetCustomPropertiesOk()
	if !ok || vmap == nil {
		return nil
	}

	v, o = (*vmap)[key]
	if !o {
		return nil
	}
	if v.MetadataStringValue != nil {
		return &v.MetadataStringValue.StringValue
	}

	return nil
}

func (m *ModelServerAPIPopulator) GetSpec() string {
	ret := m.getStringPropVal(brdgtypes.APISpecKey)
	if ret == nil {
		return "TBD"
	}
	return *ret
}

func (m *ModelServerAPIPopulator) GetTags() []string {
	tags := []string{}
	regex, _ := regexp.Compile(tagRegexp)
	if m.RegisteredModel.CustomProperties != nil {
		for _, cpv := range *m.RegisteredModel.CustomProperties {
			if cpv.MetadataStringValue == nil {
				continue
			}
			v := cpv.MetadataStringValue.StringValue
			if len(v) > 0 && regex.MatchString(v) && len(v) <= 63 {
				tags = append(tags, v)
			}
		}
	}
	return tags
}

func (m *ModelServerAPIPopulator) GetType() golang.Type {
	t := m.getStringPropVal(brdgtypes.APITypeKey)
	if t == nil {
		// assume open api
		return golang.Openapi
	}
	switch {
	case golang.Type(*t) == golang.Graphql:
		return golang.Graphql
	case golang.Type(*t) == golang.Asyncapi:
		return golang.Asyncapi
	case golang.Type(*t) == golang.Grpc:
		return golang.Grpc
	}
	return golang.Openapi
}

func (m *ModelServerAPIPopulator) GetURL() string {
	if m.Kis == nil {
		m.getLinksFromInferenceServices()
	}
	if m.Kis == nil {
		return ""
	}
	if m.Kis.Status.URL != nil && m.Kis.Status.URL.URL() != nil {
		// return the KServe InferenceService Route URL
		return m.Kis.Status.URL.URL().String()
	}
	// if an external route was not exposed, find the service
	listOptions := &client.ListOptions{Namespace: m.Kis.Namespace}
	svcList := &corev1.ServiceList{}
	err := m.CtrlClient.List(m.Ctx, svcList, listOptions)
	if err != nil {
		return ""
	}
	for _, svc := range svcList.Items {
		if svc.OwnerReferences == nil {
			continue
		}
		for _, o := range svc.OwnerReferences {
			if o.Kind == "InferenceService" &&
				o.Name == m.Kis.Name {
				return fmt.Sprintf("%s.%s.openshift.io", svc.Name, svc.Namespace)
			}
		}
	}

	return ""
}

// catalog-info.yaml populators

func CallBackstagePrinters(ctx context.Context, owner, lifecycle string, rm *openapi.RegisteredModel, mvs []openapi.ModelVersion, mas map[string][]openapi.ModelArtifact, isl []openapi.InferenceService, is *serverv1beta1.InferenceService, kfmr *KubeFlowRESTClientWrapper, client client.Client, writer io.Writer, format brdgtypes.NormalizerFormat) error {
	compPop := ComponentPopulator{}
	compPop.Owner = owner
	compPop.Lifecycle = lifecycle
	compPop.Kfmr = kfmr
	compPop.RegisteredModel = rm
	compPop.ModelVersions = mvs
	compPop.ModelArtifacts = mas
	compPop.InferenceServices = isl
	compPop.Kis = is
	compPop.CtrlClient = client
	compPop.Ctx = ctx

	switch format {
	case brdgtypes.JsonArrayForamt:
		mcPop := ModelCatalogPopulator{CommonSchemaPopulator: CommonSchemaPopulator{compPop}}
		msPop := ModelServerPopulator{
			CommonSchemaPopulator: CommonSchemaPopulator{compPop},
			ApiPop:                ModelServerAPIPopulator{CommonSchemaPopulator: CommonSchemaPopulator{compPop}},
		}
		mcPop.MSPop = &msPop
		mPop := ModelPopulator{CommonSchemaPopulator: CommonSchemaPopulator{compPop}}
		mcPop.MPops = []*ModelPopulator{&mPop}
		return backstage.PrintModelCatalogPopulator(&mcPop, writer)
	case brdgtypes.CatalogInfoYamlFormat:
		fallthrough
	default:
		err := backstage.PrintComponent(&compPop, writer)
		if err != nil {
			return err
		}

		resPop := ResourcePopulator{}
		resPop.Owner = owner
		resPop.Lifecycle = lifecycle
		resPop.Kfmr = kfmr
		resPop.RegisteredModel = rm
		resPop.ModelVersions = mvs
		resPop.Kis = is
		resPop.CtrlClient = client
		for _, mv := range mvs {
			resPop.ModelVersion = &mv
			m, _ := mas[*mv.Id]
			resPop.ModelArtifacts = m
			err = backstage.PrintResource(&resPop, writer)
			if err != nil {
				return err
			}
		}

		apiPop := ApiPopulator{}
		apiPop.Owner = owner
		apiPop.Lifecycle = lifecycle
		apiPop.Kfmr = kfmr
		apiPop.RegisteredModel = rm
		apiPop.ModelVersions = mvs
		apiPop.InferenceServices = isl
		apiPop.Kis = is
		apiPop.CtrlClient = client
		return backstage.PrintAPI(&apiPop, writer)
	}

	return nil

}

type CommonPopulator struct {
	Owner             string
	Lifecycle         string
	RegisteredModel   *openapi.RegisteredModel
	ModelVersions     []openapi.ModelVersion
	InferenceServices []openapi.InferenceService
	Kfmr              *KubeFlowRESTClientWrapper
	Kis               *serverv1beta1.InferenceService
	CtrlClient        client.Client
	Ctx               context.Context
}

func (pop *CommonPopulator) GetOwner() string {
	if pop.RegisteredModel.Owner != nil {
		return *pop.RegisteredModel.Owner
	}
	return pop.Owner
}

func (pop *CommonPopulator) GetLifecycle() string {
	return pop.Lifecycle
}

func (pop *CommonPopulator) GetDescription() string {
	if pop.RegisteredModel.Description != nil {
		return *pop.RegisteredModel.Description
	}
	return ""
}

func (pop *CommonPopulator) GetProvidedAPIs() []string {
	return []string{}
}

type ComponentPopulator struct {
	CommonPopulator
	ModelArtifacts map[string][]openapi.ModelArtifact
}

func (pop *ComponentPopulator) GetName() string {
	return pop.RegisteredModel.Name
}

func (pop *ComponentPopulator) GetLinks() []backstage.EntityLink {
	links := pop.getLinksFromInferenceServices()
	//TODO maybe multi resource / multi model indication
	for _, maa := range pop.ModelArtifacts {
		for _, ma := range maa {
			if ma.Uri != nil {
				links = append(links, backstage.EntityLink{
					URL:   *ma.Uri,
					Title: ma.GetDescription(),
					Icon:  backstage.LINK_ICON_WEBASSET,
					Type:  backstage.LINK_TYPE_WEBSITE,
				})
			}
		}
	}

	return links
}

func (pop *CommonPopulator) getLinksFromInferenceServices() []backstage.EntityLink {
	links := []backstage.EntityLink{}
	// if for some reason kserve/kubeflow reconciliation is not working and there are no kubeflow inference services,
	// let's match up based on registered model / model version name
	if len(pop.InferenceServices) == 0 {
		if pop.Kis != nil {
			kpop := kserve.CommonPopulator{InferSvc: pop.Kis}
			links = append(links, kpop.GetLinks()...)
			return links
		}
		iss := []serverv1beta1.InferenceService{}
		switch {
		case pop.CtrlClient != nil:
			isList := &serverv1beta1.InferenceServiceList{}
			err := pop.CtrlClient.List(pop.Ctx, isList)
			if err != nil {
				klog.Errorf("getLinksFromInferenceServices list all inferenceservices error: %s", err.Error())
			}
			iss = append(iss, isList.Items...)

		case pop.Kfmr != nil && pop.Kfmr.Config != nil && pop.Kfmr.Config.ServingClient != nil:
			isList, err := pop.Kfmr.Config.ServingClient.InferenceServices(metav1.NamespaceAll).List(pop.Ctx, metav1.ListOptions{})
			if err != nil {
				klog.Errorf("getLinksFromInferenceServices list all inferenceservices error: %s", err.Error())
			}
			if isList != nil {
				iss = append(iss, isList.Items...)
			}
		}
		replacer := strings.NewReplacer(" ", "")
		rName := pop.RegisteredModel.Name
		rName = replacer.Replace(rName)
		for _, mv := range pop.ModelVersions {
			mn := mv.Name
			mn = replacer.Replace(mn)
			key := fmt.Sprintf("%s-%s", rName, mn)
			for _, is := range iss {
				if is.Name == key {
					pop.Kis = &is
					kpop := kserve.CommonPopulator{InferSvc: pop.Kis}
					links = append(links, kpop.GetLinks()...)
					return links
				}
			}
		}

	}

	for _, is := range pop.InferenceServices {
		var rmid *string
		var ok bool
		rmid, ok = pop.RegisteredModel.GetIdOk()
		if !ok {
			continue
		}
		if is.RegisteredModelId != *rmid {
			continue
		}
		var iss *openapi.InferenceServiceState
		iss, ok = is.GetDesiredStateOk()
		if !ok {
			continue
		}
		if *iss != openapi.INFERENCESERVICESTATE_DEPLOYED {
			continue
		}
		se, err := pop.Kfmr.GetServingEnvironment(is.ServingEnvironmentId)
		if err != nil {
			klog.Errorf("ComponentPopulator GetLinks: %s", err.Error())
			continue
		}
		if pop.Kis == nil {
			kisns := se.GetName()
			kisnm := is.GetRuntime()
			var kis *serverv1beta1.InferenceService
			if pop.Kfmr != nil && pop.Kfmr.Config != nil && pop.Kfmr.Config.ServingClient != nil {
				kis, err = pop.Kfmr.Config.ServingClient.InferenceServices(kisns).Get(context.Background(), kisnm, metav1.GetOptions{})
			}
			if kis == nil && pop.CtrlClient != nil {
				kis = &serverv1beta1.InferenceService{}
				err = pop.CtrlClient.Get(context.Background(), types.NamespacedName{Namespace: kisns, Name: kisnm}, kis)
			}

			if err != nil {
				klog.Errorf("ComponentPopulator GetLinks: %s", err.Error())
				continue
			}

			pop.Kis = kis
		}
		kpop := kserve.CommonPopulator{InferSvc: pop.Kis}
		links = append(links, kpop.GetLinks()...)
	}
	return links
}

func (pop *ComponentPopulator) GetTags() []string {
	tags := []string{}
	regex, _ := regexp.Compile(tagRegexp)
	for key, value := range pop.RegisteredModel.GetCustomProperties() {
		if !regex.MatchString(key) {
			klog.Infof("skipping custom prop %s for tags", key)
			continue
		}
		tag := key
		if value.MetadataStringValue != nil {
			strVal := value.MetadataStringValue.StringValue
			if !regex.MatchString(fmt.Sprintf("%v", strVal)) {
				klog.Infof("skipping custom prop value %v for tags", value.GetActualInstance())
				continue
			}
			tag = fmt.Sprintf("%s-%s", tag, strVal)
		}

		if len(tag) > 63 {
			klog.Infof("skipping tag %s because its length is greater than 63", tag)
		}

		tags = append(tags, tag)
	}

	return tags
}

func (pop *ComponentPopulator) GetDependsOn() []string {
	depends := []string{}
	for _, mv := range pop.ModelVersions {
		depends = append(depends, "resource:"+mv.Name)
	}
	for _, mas := range pop.ModelArtifacts {
		for _, ma := range mas {
			depends = append(depends, "api:"+*ma.Name)
		}
	}
	return depends
}

func (pop *ComponentPopulator) GetTechdocRef() string {
	return "./"
}

func (pop *ComponentPopulator) GetDisplayName() string {
	return pop.GetName()
}

type ResourcePopulator struct {
	CommonPopulator
	ModelVersion   *openapi.ModelVersion
	ModelArtifacts []openapi.ModelArtifact
}

func (pop *ResourcePopulator) GetName() string {
	return pop.ModelVersion.Name
}

func (pop *ResourcePopulator) GetTechdocRef() string {
	return "resource/"
}

func (pop *ResourcePopulator) GetLinks() []backstage.EntityLink {
	links := []backstage.EntityLink{}
	//TODO maybe multi resource / multi model indication
	for _, ma := range pop.ModelArtifacts {
		if ma.Uri != nil {
			links = append(links, backstage.EntityLink{
				URL:   *ma.Uri,
				Title: ma.GetDescription(),
				Icon:  backstage.LINK_ICON_WEBASSET,
				Type:  backstage.LINK_TYPE_WEBSITE,
			})
		}
	}
	return links
}

func (pop *ResourcePopulator) GetTags() []string {
	tags := []string{}
	regex, _ := regexp.Compile(tagRegexp)
	for key, value := range pop.ModelVersion.GetCustomProperties() {
		if !regex.MatchString(key) {
			klog.Infof("skipping custom prop %s for tags", key)
			continue
		}
		tag := key
		if value.MetadataStringValue != nil {
			strVal := value.MetadataStringValue.StringValue
			if !regex.MatchString(fmt.Sprintf("%v", strVal)) {
				klog.Infof("skipping custom prop value %v for tags", value.GetActualInstance())
				continue
			}
			tag = fmt.Sprintf("%s-%s", tag, strVal)
		}
		if len(tag) > 63 {
			klog.Infof("skipping tag %s because its length is greater than 63", tag)
		}

		tags = append(tags, tag)
	}

	for _, ma := range pop.ModelArtifacts {
		for k, v := range ma.GetCustomProperties() {
			if !regex.MatchString(k) {
				klog.Infof("skipping custom prop %s for tags", k)
				continue
			}
			tag := k
			if v.MetadataStringValue != nil {
				strVal := v.MetadataStringValue.StringValue
				if !regex.MatchString(fmt.Sprintf("%v", strVal)) {
					klog.Infof("skipping custom prop value %v for tags", v.GetActualInstance())
					continue
				}
				tag = fmt.Sprintf("%s-%s", tag, strVal)
			}

			if len(tag) > 63 {
				klog.Infof("skipping tag %s because its length is greater than 63", tag)
			}

			tags = append(tags, tag)
		}
	}
	return tags
}

func (pop *ResourcePopulator) GetDependencyOf() []string {
	return []string{fmt.Sprintf("component:%s", pop.RegisteredModel.Name)}
}

func (pop *ResourcePopulator) GetDisplayName() string {
	return pop.GetName()
}

type ApiPopulator struct {
	CommonPopulator
}

func (pop *ApiPopulator) GetName() string {
	return pop.RegisteredModel.Name
}

func (pop *ApiPopulator) GetDependencyOf() []string {
	return []string{fmt.Sprintf("component:%s", pop.RegisteredModel.Name)}
}

func (pop *ApiPopulator) GetDefinition() string {
	// definition must be set to something to pass backstage validation
	return "no-definition-yet"
}

func (pop *ApiPopulator) GetTechdocRef() string {
	// TODO in theory the Kfmr modelcard support when it arrives will replace this
	return "api/"
}

func (pop *ApiPopulator) GetTags() []string {
	return []string{}
}

func (pop *ApiPopulator) GetLinks() []backstage.EntityLink {
	return pop.getLinksFromInferenceServices()
}

func (pop *ApiPopulator) GetDisplayName() string {
	return pop.GetName()
}
