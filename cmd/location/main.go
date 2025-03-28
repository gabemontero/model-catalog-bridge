package main

import (
	goflag "flag"
	gin_gonic_http_srv "github.com/redhat-ai-dev/model-catalog-bridge/pkg/cmd/server/location/server"
	"github.com/redhat-ai-dev/model-catalog-bridge/pkg/util"
	"k8s.io/klog/v2"
	"os"
)

func main() {
	flagset := goflag.NewFlagSet("location", goflag.ContinueOnError)
	klog.InitFlags(flagset)

	st := os.Getenv("STORAGE_URL")
	server := gin_gonic_http_srv.NewImportLocationServer(st)
	stopCh := util.SetupSignalHandler()
	server.Run(stopCh)

}
