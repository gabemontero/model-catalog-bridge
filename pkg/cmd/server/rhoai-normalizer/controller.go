package rhoai_normalizer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/go-resty/resty/v2"
	serverapiv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	"github.com/kubeflow/model-registry/pkg/openapi"
	routev1 "github.com/openshift/api/route/v1"
	routeclient "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/cli/kubeflowmodelregistry"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/server/storage"
	bridgerest "github.com/redhat-ai-dev/model-catalog-bridge/pkg/rest"
	types2 "github.com/redhat-ai-dev/model-catalog-bridge/pkg/types"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/util"
	"io"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"net/http"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"
	"time"
)

var (
	controllerLog = ctrl.Log.WithName("controller")
)

func NewControllerManager(ctx context.Context, cfg *rest.Config, options ctrl.Options, pprofAddr string) (ctrl.Manager, error) {
	apiextensionsClient := apiextensionsclient.NewForConfigOrDie(cfg)
	kserveClient := util.GetKServeClient(cfg)

	if err := wait.PollImmediate(time.Second*5, time.Minute*5, func() (done bool, err error) {
		crdName := fmt.Sprintf("%s.%s", constants.InferenceServiceAPIName, constants.KServeAPIGroupName)
		_, err = apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
		if err != nil {
			controllerLog.Error(err, "get of inferenceservices crd failed")
			return false, nil
		}

		_, err = kserveClient.InferenceServices("").List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			controllerLog.Error(err, "list of inferenceservers failed")
			return false, nil
		}

		controllerLog.Info("list of inferenceservices successful")
		return true, nil
	}); err != nil {
		controllerLog.Error(err, "waiting of inferenceservice CRD to be created")
		return nil, err
	}

	options.Scheme = runtime.NewScheme()
	if err := k8sscheme.AddToScheme(options.Scheme); err != nil {
		return nil, err
	}
	if err := serverapiv1beta1.AddToScheme(options.Scheme); err != nil {
		return nil, err
	}

	mgr, err := ctrl.NewManager(cfg, options)

	err = SetupController(ctx, mgr, cfg, pprofAddr)
	return mgr, err
}

// pprof enablement is OK running in production by default (i.e. you don't do CPU profiling and it allows us
// to get goroutine dumps if we have to diagnose deadlocks and the like

type pprof struct {
	port string
}

func (p *pprof) Start(ctx context.Context) error {
	srv := &http.Server{Addr: ":" + p.port}
	controllerLog.Info(fmt.Sprintf("starting ppprof on %s", p.port))
	go func() {
		err := srv.ListenAndServe()
		if err != nil {
			controllerLog.Info(fmt.Sprintf("pprof server err: %s", err.Error()))
		}
	}()
	<-ctx.Done()
	controllerLog.Info("Shutting down pprof")
	srv.Shutdown(ctx)
	return nil
}

func (r *RHOAINormalizerReconcile) setupKFMR(ctx context.Context) bool {
	if r.kfmrRoute == nil || len(r.kfmrRoute.Status.Ingress) == 0 {
		var err error
		rr := strings.NewReplacer("\r", "", "\n", "")
		mrRoute := os.Getenv("MR_ROUTE")
		rr.Replace(mrRoute)

		r.kfmrRoute, err = r.routeClient.Routes("istio-system").Get(ctx, mrRoute, metav1.GetOptions{})
		if err != nil {
			controllerLog.Error(err, "error fetching model registry route")
			return false
		}
	}
	if r.kfmrRoute != nil && len(r.kfmrRoute.Status.Ingress) > 0 && r.kfmr == nil {
		//TODO fallback until we have the gitops style of KFMR permissions nailed down
		rr := strings.NewReplacer("\r", "", "\n", "")
		kfmrToken := os.Getenv("KFMR_TOKEN")
		kfmrToken = rr.Replace(kfmrToken)
		if len(kfmrToken) == 0 {
			kfmrToken = r.k8sToken
		}
		r.kfmr = &kubeflowmodelregistry.KubeFlowRESTClientWrapper{
			Token:      kfmrToken,
			RootURL:    "https://" + r.kfmrRoute.Status.Ingress[0].Host + bridgerest.KFMR_BASE_URI,
			RESTClient: resty.New(),
		}
		r.kfmr.RESTClient.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true})
	}
	return true
}

func SetupController(ctx context.Context, mgr ctrl.Manager, cfg *rest.Config, pprofPort string) error {
	filter := &RHOAINormalizerFilter{}
	formatEnv := os.Getenv(types2.FormatEnvVar)
	r := strings.NewReplacer("\r", "", "\n", "")
	formatEnv = r.Replace(formatEnv)
	storageURL := os.Getenv("STORAGE_URL")
	storageURL = r.Replace(storageURL)
	if len(storageURL) == 0 {
		podIP := os.Getenv("POD_IP")
		podIP = r.Replace(podIP)
		storageURL = fmt.Sprintf("http://%s:7070", podIP)
		klog.Infof("using %s for the storage URL per our sidecar hack", storageURL)
	}
	reconciler := &RHOAINormalizerReconcile{
		client:        mgr.GetClient(),
		scheme:        mgr.GetScheme(),
		eventRecorder: mgr.GetEventRecorderFor("RHOAINormalizer"),
		k8sToken:      util.GetCurrentToken(cfg),
		routeClient:   routeclient.NewForConfigOrDie(cfg),
		storage:       storage.SetupBridgeStorageRESTClient(storageURL, util.GetCurrentToken(cfg)),
		format:        types2.NormalizerFormat(formatEnv),
	}

	switch reconciler.format {
	case types2.JsonArrayForamt:
	case types2.CatalogInfoYamlFormat:
	default:
		//TODO eventually switch the defaulting to json array
		reconciler.format = types2.CatalogInfoYamlFormat
	}

	reconciler.myNS = util.GetCurrentProject()

	var err error
	mrRoute := os.Getenv("MR_ROUTE")
	rr := strings.NewReplacer("\r", "", "\n", "")
	mrRoute = rr.Replace(mrRoute)
	reconciler.kfmrRoute, err = reconciler.routeClient.Routes("istio-system").Get(context.TODO(), mrRoute, metav1.GetOptions{})
	if err == nil {
		controllerLog.Error(err, "error getting model registry route, will try again later")
	}

	reconciler.setupKFMR(ctx)

	err = ctrl.NewControllerManagedBy(mgr).For(&serverapiv1beta1.InferenceService{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 32}).
		WithEventFilter(filter).
		Complete(reconciler)
	if err != nil {
		return err
	}

	err = mgr.Add(reconciler)
	if err != nil {
		return err
	}

	if len(pprofPort) > 0 {
		pp := &pprof{port: pprofPort}
		err = mgr.Add(pp)
		if err != nil {
			return err
		}
	}

	return nil
}

type RHOAINormalizerFilter struct {
}

func (f *RHOAINormalizerFilter) Generic(event.GenericEvent) bool {
	return false
}

func (f *RHOAINormalizerFilter) Create(event.CreateEvent) bool {
	return true
}

func (f *RHOAINormalizerFilter) Delete(event.DeleteEvent) bool {
	return true
}

func (f RHOAINormalizerFilter) Update(e event.UpdateEvent) bool {
	return true
}

type RHOAINormalizerReconcile struct {
	client        client.Client
	scheme        *runtime.Scheme
	eventRecorder record.EventRecorder
	k8sToken      string
	kfmrRoute     *routev1.Route
	myNS          string
	routeClient   *routeclient.RouteV1Client
	kfmr          *kubeflowmodelregistry.KubeFlowRESTClientWrapper
	storage       *storage.BridgeStorageRESTClient
	format        types2.NormalizerFormat
}

func (r *RHOAINormalizerReconcile) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(ctx)
	defer cancel()
	log := log.FromContext(ctx)

	is := &serverapiv1beta1.InferenceService{}
	name := types.NamespacedName{Namespace: request.Namespace, Name: request.Name}
	err := r.client.Get(ctx, name, is)
	if err != nil && !errors.IsNotFound(err) {
		return reconcile.Result{}, err
	}

	if err != nil {
		log.V(4).Info(fmt.Sprintf("initiating delete processing for %s", name.String()))
		// now, delete of the inference service does not mean the model has been deleted from model registry,
		// so we don't remove the model from our catalog, but may simply remove the URL associated with the inference
		// service; initiate a KFMR poll to remove any URLs/route from the model entries which depended on the inference service here; also,
		// if the delete of the kserve inference service happened to result from an archiving of the model, the
		// innerStart call will detect that and then initiate removal of the model from the storage and location services
		b := []byte{}
		buf := bytes.NewBuffer(b)
		bwriter := bufio.NewWriter(buf)
		r.innerStart(ctx, buf, bwriter)

		return reconcile.Result{}, nil
	}

	b := []byte{}
	buf := bytes.NewBuffer(b)
	bwriter := bufio.NewWriter(buf)
	importKey := ""

	//TODO fill in lifecycle from kfmr k/v pairs perhaps
	if r.kfmrRoute != nil {
		importKey, err = r.processKFMR(ctx, name, is, bwriter, log)
		if err != nil {
			return reconcile.Result{}, err
		}
	}
	if len(importKey) == 0 {
		// KServe only
		//TODO we will deal with kserve-only and reconciling timing issues where a model is discovered via kserve-only
		// mode first, but then later on we are able to correlate that running model with a KFMR entry

		//TODO do we mandate a prop be set on the RM or MV for lifecycle?
		//err = kserve.CallBackstagePrinters(is.Namespace, "developement", is, bwriter)
		//
		//if err != nil {
		//	return reconcile.Result{}, nil
		//}
		//
		//importKey, importURI = util.BuildImportKeyAndURI(is.Namespace, is.Name)
		klog.Infof("inference service %s:%s does not correlate to a kubeflow model registry entry", is.Namespace, is.Name)
		return reconcile.Result{}, nil
	}

	err = r.processBWriter(bwriter, buf, importKey)
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *RHOAINormalizerReconcile) processBWriter(bwriter *bufio.Writer, buf *bytes.Buffer, importKey string) error {
	err := bwriter.Flush()
	if err != nil {
		return err
	}

	httpRC := 0
	msg := ""
	httpRC, msg, _, err = r.storage.UpsertModel(importKey, buf.Bytes())
	if err != nil {
		return err
	}
	if httpRC != http.StatusCreated && httpRC != http.StatusOK {
		return fmt.Errorf("post to storage returned rc %d: %s", httpRC, msg)
	}
	return nil
}

func (r *RHOAINormalizerReconcile) processKFMR(ctx context.Context, name types.NamespacedName, is *serverapiv1beta1.InferenceService, bwriter io.Writer, log logr.Logger) (string, error) {
	ready := r.setupKFMR(ctx)
	if !ready {
		log.V(4).Info(fmt.Sprintf("reconciling inferenceservice %s, kmr route %s has no ingress", name.String(), r.kfmrRoute.Name))
		return "", nil
	}

	var kfmrRMs []openapi.RegisteredModel
	var err error
	kfmrRMs, err = r.kfmr.ListRegisteredModels()
	if err != nil {
		log.Error(err, fmt.Sprintf("reconciling inferenceservice %s, error listing kfmr registered models", name.String()))
		return "", err
	}

	var kfmrISs []openapi.InferenceService
	kfmrISs, err = r.kfmr.ListInferenceServices()
	if err != nil {
		log.Error(err, fmt.Sprintf("reconciling inferenceservice %s, error listing kfmr registered models", name.String()))
	}

	if len(kfmrISs) > 0 {
		for _, rm := range kfmrRMs {
			if rm.Id == nil {
				log.Info(fmt.Sprintf("reconciling inferenceservice %s, registered model %s has no ID", name.String(), rm.Name))
				continue
			}

			for _, kfmrIS := range kfmrISs {
				if kfmrIS.Id != nil && kfmrIS.RegisteredModelId == *rm.Id && strings.HasPrefix(kfmrIS.GetName(), is.Name) {
					seId := kfmrIS.GetServingEnvironmentId()
					var se *openapi.ServingEnvironment
					se, err = r.kfmr.GetServingEnvironment(seId)
					if err != nil {
						log.Error(err, fmt.Sprintf("reconciling inferenceservice %s, error getting kfmr serving environment %s", name.String(), seId))
						continue
					}

					if se.Name != nil && *se.Name == is.Namespace {
						// FOUND the match !!
						// reminder based on explanations about model artifact actually being the "root" of their model, and what has been observed in testing,
						mvId := kfmrIS.GetModelVersionId()
						var mas []openapi.ModelArtifact
						var mv *openapi.ModelVersion
						mv, err = r.kfmr.GetModelVersions(mvId)
						if err != nil {
							log.Error(err, fmt.Sprintf("reconciling inferenceservice %s, error getting kfmr model version %s", name.String(), mvId))
							// don't just continue, try to build a catalog entry with the subset of info available
						}
						mas, err = r.kfmr.ListModelArtifacts(mvId)
						if err != nil {
							log.Error(err, fmt.Sprintf("reconciling inferenceservice %s, error getting kfmr model artifacts for %s", name.String(), mvId))
							// don't just continue, try to build a catalog entry with the subset of info available
						}

						if mv == nil || mas == nil {
							log.Info(fmt.Sprintf("either mv %#v or mas %#v is nil, bypassing CallBackstagePrinters", mv, mas))
							continue
						}

						//TODO do we mandate a prop be set on the RM or MV for owner,lifecycle?
						err = kubeflowmodelregistry.CallBackstagePrinters(util.DefaultOwner,
							util.DefaultLifecycle,
							&rm,
							//TODO deal with multiple versions
							[]openapi.ModelVersion{*mv},
							map[string][]openapi.ModelArtifact{mvId: mas},
							[]openapi.InferenceService{kfmrIS},
							is,
							r.kfmr,
							r.client,
							bwriter,
							r.format)

						if err != nil {
							return "", err
						}

						//TODO iterate on the the REST URI's for our models if multi model?
						importKey, _ := util.BuildImportKeyAndURI(rm.Name, mv.Name, r.format)
						return importKey, nil

					}

				}
			}
		}

	}

	// no match to kfmr, but do not return error, as caller can still process this as kserve only
	return "", nil
}

// Start - supplement with background polling as controller relist does not duplicate delete events, and we can be more
// fine grained on what we attempt to relist vs. just increasing the frequency of all the controller's watches

func (r *RHOAINormalizerReconcile) Start(ctx context.Context) error {
	eventTicker := time.NewTicker(2 * time.Minute)
	for {
		select {
		case <-eventTicker.C:
			r.innerStart(ctx, nil, nil)

		case <-ctx.Done():
		}
	}
}

func (r *RHOAINormalizerReconcile) innerStart(ctx context.Context, buf *bytes.Buffer, bwriter *bufio.Writer) {
	ready := r.setupKFMR(ctx)
	if !ready {
		return
	}

	var err error
	var rms []openapi.RegisteredModel
	var mvs map[string][]openapi.ModelVersion
	var mas map[string]map[string][]openapi.ModelArtifact
	var isl []openapi.InferenceService

	//TODO what do we do with owner/lifecycle when we poll
	rms, mvs, mas, err = kubeflowmodelregistry.LoopOverKFMR([]string{}, r.kfmr)
	if err != nil {
		controllerLog.Error(err, "err looping over KFMR")
		return
	}
	keys := []string{}
	for _, rm := range rms {
		mva, ok := mvs[rm.Name]
		if !ok {
			continue
		}
		maa, ok2 := mas[rm.Name]
		if !ok2 {
			continue
		}
		for _, mv := range mva {

			importKey, _ := util.BuildImportKeyAndURI(rm.Name, mv.Name, r.format)
			keys = append(keys, importKey)
			eb := []byte{}
			ebuf := bytes.NewBuffer(eb)
			ewriter := bufio.NewWriter(ebuf)
			if buf != nil && bwriter != nil {
				ebuf = buf
				ewriter = bwriter
			}
			isl, err = r.kfmr.ListInferenceServices()
			if err != nil {
				controllerLog.Error(err, "error listing kubeflow inference services")
				continue
			}
			err = kubeflowmodelregistry.CallBackstagePrinters(util.DefaultOwner, util.DefaultLifecycle, &rm, mva, maa, isl, nil, r.kfmr, r.client, ewriter, r.format)
			if err != nil {
				controllerLog.Error(err, "error processing calling backstage printer")
				continue
			}
			err = r.processBWriter(ewriter, ebuf, importKey)
			if err != nil {
				controllerLog.Error(err, "error processing KFMR writer")
				continue
			}
		}
	}

	rc := 0
	msg := ""
	rc, msg, err = r.storage.PostCurrentKeySet(keys)
	if err != nil {
		controllerLog.Error(err, "error updating current key set")
		return
	}
	if rc != http.StatusCreated && rc != http.StatusOK {
		controllerLog.Error(fmt.Errorf("post to storage returned rc %d: %s", rc, msg), "bad rc updating current key set")
	}

}
