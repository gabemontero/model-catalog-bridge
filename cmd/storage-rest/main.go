package main

import (
	goflag "flag"
	"fmt"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/server/storage"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/types"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/util"
	"k8s.io/klog/v2"
	"os"
	"strings"
)

func main() {
	var address string
	goflag.StringVar(&address, "address", "7070", "The port the storage service listens on.")
	flagset := goflag.NewFlagSet("storage-rest", goflag.ContinueOnError)
	flagset.Parse(goflag.CommandLine.Args())
	klog.InitFlags(flagset)
	goflag.Parse()

	st := os.Getenv(types.StorageTypeEnvVar)
	storageType := types.BridgeStorageType(st)

	bs := storage.NewBridgeStorage(storageType)

	// setup ca.crt for TLS, get k8s cfg to find bkstg route
	restConfig, err := storage.GetRESTConfig()
	if err != nil {
		klog.Errorf("%s", err.Error())
		klog.Flush()
		os.Exit(1)
	}

	r := strings.NewReplacer("\r", "", "\n", "")

	bridgeURL := os.Getenv(types.LocationUrlEnvVar)
	bridgeURL = r.Replace(bridgeURL)
	klog.Infof("%s set to %s", types.LocationUrlEnvVar, bridgeURL)
	bridgeToken := util.GetCurrentToken(restConfig)

	bkstgToken := os.Getenv(types.RHDHTokenEnvVar)
	bkstgToken = r.Replace(bkstgToken)

	podIP := os.Getenv(util.PodIPEnvVar)
	podIP = r.Replace(podIP)
	klog.Infof("pod IP from env var is %s", podIP)
	if len(podIP) > 0 && len(bridgeURL) == 0 {
		// neither inter-Pod nor service IPs worked for backstage access in testing; have to use the route
		bridgeURL = fmt.Sprintf("http://%s:9090", podIP)
		klog.Infof("using %s env var vs. %s for location service access", util.PodIPEnvVar, types.LocationUrlEnvVar)
	}

	nfstr := os.Getenv(types.FormatEnvVar)
	nf := types.NormalizerFormat(nfstr)

	server := storage.NewStorageRESTServer(bs, address, bridgeURL, bridgeToken, bkstgToken, nf)
	stopCh := util.SetupSignalHandler()
	server.Run(stopCh)

}
