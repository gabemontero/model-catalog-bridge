package kubeflowmodelregistry

import (
	"context"
	"fmt"
	"github.com/kubeflow/model-registry/pkg/openapi"
	"github.com/redhat-ai-dev/rhdh-ai-catalog-cli/pkg/cmd/cli/backstage"
	"github.com/redhat-ai-dev/rhdh-ai-catalog-cli/pkg/cmd/cli/kserve"
	"github.com/redhat-ai-dev/rhdh-ai-catalog-cli/pkg/config"
	"github.com/redhat-ai-dev/rhdh-ai-catalog-cli/pkg/util"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"strings"
)

const (
	kubeflowExample = `
# Both owner and lifecycle are required parameters.  Examine Backstage Catalog documentation for details.
# This will query all the RegisteredModel, ModelVersion, ModelArtifact, and InferenceService instances in the Kubeflow Model Registry and build Catalog Component, Resource, and
# API Entities from the data.
$ %s new-model kubeflow <owner> <lifecycle> <args...>

# This will set the URL, Token, and Skip TLS when accessing Kubeflow
$ %s new-model kubeflow <owner> <lifecycle> --model-metadata-url=https://my-kubeflow.com --model-metadata-token=my-token --model-metadata-skip-tls=true

# This form will pull in only the RegisteredModels with the specified IDs '1' and '2' and the ModelVersion, ModelArtifact, and InferenceService
# artifacts that are linked to those RegisteredModels in order to build Catalog Component, Resource, and API Entities.
$ %s new-model kubeflow <owner> <lifecycle> 1 2 
`
)

func NewCmd(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "kubeflow",
		Aliases: []string{"kf"},
		Short:   "Kubeflow Model Registry related API",
		Long:    "Interact with the Kubeflow Model Registry REST API as part of managing AI related catalog entities in a Backstage instance.",
		Example: strings.ReplaceAll(kubeflowExample, "%s", util.ApplicationName),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids := []string{}

			if len(args) < 2 {
				err := fmt.Errorf("need to specify an Owner and Lifecycle setting")
				klog.Errorf("%s", err.Error())
				klog.Flush()
				return err
			}
			owner := args[0]
			lifecycle := args[1]

			if len(args) > 2 {
				ids = args[2:]
			}

			kfmr := SetupKubeflowRESTClient(cfg)

			var err error
			var isl []openapi.InferenceService

			isl, err = kfmr.ListInferenceServices()

			if len(ids) == 0 {
				var rms []openapi.RegisteredModel
				rms, err = kfmr.ListRegisteredModels()
				if err != nil {
					klog.Errorf("list registered models error: %s", err.Error())
					klog.Flush()
					return err
				}
				for _, rm := range rms {
					var mvs []openapi.ModelVersion
					var mas map[string][]openapi.ModelArtifact
					mvs, mas, err = callKubeflowREST(*rm.Id, kfmr)
					if err != nil {
						klog.Errorf("%s", err.Error())
						klog.Flush()
						return err
					}
					err = callBackstagePrinters(owner, lifecycle, &rm, mvs, mas, isl, kfmr, cmd)
					if err != nil {
						klog.Errorf("print model catalog: %s", err.Error())
						klog.Flush()
						return err
					}
				}
			} else {
				for _, id := range ids {
					var rm *openapi.RegisteredModel
					rm, err = kfmr.GetRegisteredModel(id)
					if err != nil {
						klog.Errorf("get registered model error for %s: %s", id, err.Error())
						klog.Flush()
						return err
					}
					var mvs []openapi.ModelVersion
					var mas map[string][]openapi.ModelArtifact
					mvs, mas, err = callKubeflowREST(*rm.Id, kfmr)
					if err != nil {
						klog.Errorf("get model version/artifact error for %s: %s", id, err.Error())
						klog.Flush()
						return err
					}
					err = callBackstagePrinters(owner, lifecycle, rm, mvs, mas, isl, kfmr, cmd)
				}
			}
			return nil
		},
	}

	return cmd
}

func callKubeflowREST(id string, kfmr *KubeFlowRESTClientWrapper) (mvs []openapi.ModelVersion, ma map[string][]openapi.ModelArtifact, err error) {
	mvs, err = kfmr.ListModelVersions(id)
	if err != nil {
		klog.Errorf("ERROR: error list model versions for %s: %s", id, err.Error())
		return
	}
	ma = map[string][]openapi.ModelArtifact{}
	for _, mv := range mvs {
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

func callBackstagePrinters(owner, lifecycle string, rm *openapi.RegisteredModel, mvs []openapi.ModelVersion, mas map[string][]openapi.ModelArtifact, isl []openapi.InferenceService, kfmr *KubeFlowRESTClientWrapper, cmd *cobra.Command) error {
	compPop := componentPopulator{}
	compPop.owner = owner
	compPop.lifecycle = lifecycle
	compPop.kfmr = kfmr
	compPop.registeredModel = rm
	compPop.modelVersions = mvs
	compPop.modelArtifacts = mas
	compPop.inferenceServices = isl
	err := backstage.PrintComponent(&compPop, cmd)
	if err != nil {
		return err
	}

	resPop := resourcePopulator{}
	resPop.owner = owner
	resPop.lifecycle = lifecycle
	resPop.kfmr = kfmr
	resPop.registeredModel = rm
	for _, mv := range mvs {
		resPop.modelVersion = &mv
		m, _ := mas[*mv.Id]
		resPop.modelArtifacts = m
		err = backstage.PrintResource(&resPop, cmd)
		if err != nil {
			return err
		}
	}

	apiPop := apiPopulator{}
	apiPop.owner = owner
	apiPop.lifecycle = lifecycle
	apiPop.kfmr = kfmr
	apiPop.registeredModel = rm
	apiPop.inferenceServices = isl
	return backstage.PrintAPI(&apiPop, cmd)
}

type commonPopulator struct {
	owner             string
	lifecycle         string
	registeredModel   *openapi.RegisteredModel
	inferenceServices []openapi.InferenceService
	kfmr              *KubeFlowRESTClientWrapper
}

func (pop *commonPopulator) GetOwner() string {
	if pop.registeredModel.Owner != nil {
		return *pop.registeredModel.Owner
	}
	return pop.owner
}

func (pop *commonPopulator) GetLifecycle() string {
	return pop.lifecycle
}

func (pop *commonPopulator) GetDescription() string {
	if pop.registeredModel.Description != nil {
		return *pop.registeredModel.Description
	}
	return ""
}

// TODO won't have API until KubeFlow Model Registry gets us inferenceservice endpoints
func (pop *commonPopulator) GetProvidedAPIs() []string {
	return []string{}
}

type componentPopulator struct {
	commonPopulator
	modelVersions  []openapi.ModelVersion
	modelArtifacts map[string][]openapi.ModelArtifact
}

func (pop *componentPopulator) GetName() string {
	return pop.registeredModel.Name
}

func (pop *componentPopulator) GetLinks() []backstage.EntityLink {
	links := pop.getLinksFromInferenceServices()
	// GGM maybe multi resource / multi model indication
	for _, maa := range pop.modelArtifacts {
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

func (pop *commonPopulator) getLinksFromInferenceServices() []backstage.EntityLink {
	links := []backstage.EntityLink{}
	for _, is := range pop.inferenceServices {
		var rmid *string
		var ok bool
		rmid, ok = pop.registeredModel.GetIdOk()
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
		se, err := pop.kfmr.GetServingEnvironment(is.ServingEnvironmentId)
		if err != nil {
			klog.Errorf("componentPopulator GetLinks: %s", err.Error())
			continue
		}
		kisns := se.GetName()
		kisnm := is.GetRuntime()
		kis, err := pop.kfmr.Config.ServingClient.InferenceServices(kisns).Get(context.Background(), kisnm, metav1.GetOptions{})
		if err != nil {
			klog.Errorf("componentPopulator GetLinks: %s", err.Error())
			continue
		}
		kpop := kserve.CommonPopulator{InferSvc: kis}
		links = append(links, kpop.GetLinks()...)
	}
	return links
}

func (pop *componentPopulator) GetTags() []string {
	tags := []string{}
	for key, value := range pop.registeredModel.GetCustomProperties() {
		tags = append(tags, fmt.Sprintf("%s:%v", key, value.GetActualInstance()))
	}

	return tags
}

func (pop *componentPopulator) GetDependsOn() []string {
	depends := []string{}
	for _, mv := range pop.modelVersions {
		depends = append(depends, "resource:"+mv.Name)
	}
	for _, mas := range pop.modelArtifacts {
		for _, ma := range mas {
			depends = append(depends, "api:"+*ma.Name)
		}
	}
	return depends
}

func (pop *componentPopulator) GetTechdocRef() string {
	return "./"
}

func (pop *componentPopulator) GetDisplayName() string {
	return pop.GetName()
}

type resourcePopulator struct {
	commonPopulator
	modelVersion   *openapi.ModelVersion
	modelArtifacts []openapi.ModelArtifact
}

func (pop *resourcePopulator) GetName() string {
	return pop.modelVersion.Name
}

func (pop *resourcePopulator) GetTechdocRef() string {
	return "resource/"
}

func (pop *resourcePopulator) GetLinks() []backstage.EntityLink {
	links := []backstage.EntityLink{}
	// GGM maybe multi resource / multi model indication
	for _, ma := range pop.modelArtifacts {
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

func (pop *resourcePopulator) GetTags() []string {
	tags := []string{}
	for key := range pop.modelVersion.GetCustomProperties() {
		tags = append(tags, key)
	}

	for _, ma := range pop.modelArtifacts {
		for k := range ma.GetCustomProperties() {
			tags = append(tags, k)
		}
	}
	return tags
}

func (pop *resourcePopulator) GetDependencyOf() []string {
	return []string{fmt.Sprintf("component:%s", pop.registeredModel.Name)}
}

func (pop *resourcePopulator) GetDisplayName() string {
	return pop.GetName()
}

// TODO Until we get the inferenceservice endpoint URL associated with the model registry related API won't have much for Backstage API here
type apiPopulator struct {
	commonPopulator
}

func (pop *apiPopulator) GetName() string {
	return pop.registeredModel.Name
}

func (pop *apiPopulator) GetDependencyOf() []string {
	return []string{fmt.Sprintf("component:%s", pop.registeredModel.Name)}
}

func (pop *apiPopulator) GetDefinition() string {
	// definition must be set to something to pass backstage validation
	return "no-definition-yet"
}

func (pop *apiPopulator) GetTechdocRef() string {
	// TODO in theory the kfmr modelcard support when it arrives will influcen this
	return "api/"
}

func (pop *apiPopulator) GetTags() []string {
	return []string{}
}

func (pop *apiPopulator) GetLinks() []backstage.EntityLink {
	return pop.getLinksFromInferenceServices()
}

func (pop *apiPopulator) GetDisplayName() string {
	return pop.GetName()
}
