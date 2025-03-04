package github

import (
     sharedv2 "github.com/cli/cli/v2/pkg/cmd/auth/shared"
     "k8s.io/client-go/rest"
     "net/http"
)

type GithubBridgeStorage struct {
     Hostname string
     Token    string
}

func (c *GithubBridgeStorage) Initialize(cfg *rest.Config) error {
     cl := &http.Client{}
     msg, err := sharedv2.GetCurrentLogin(cl, c.Hostname, c.Token)
     return nil
}
