package configmap

import (
     "context"
     "encoding/json"
     "sync"
     "time"

     "github.com/redhat-ai-dev/model-catalog-bridge/pkg/types"
     "github.com/redhat-ai-dev/model-catalog-bridge/pkg/util"
     corev1 "k8s.io/api/core/v1"
     "k8s.io/apimachinery/pkg/api/errors"
     metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
     "k8s.io/apimachinery/pkg/util/wait"
     corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
     "k8s.io/klog/v2"

     "k8s.io/client-go/rest"
)

type ConfigMapBridgeStorage struct {
     cfg        *rest.Config
     cl         corev1client.CoreV1Interface
     ns         string
     versionMap map[string]string
     mutex      sync.Mutex
}

func NewConfigMapBridgeStorageForTest(ns string, cl corev1client.CoreV1Interface) *ConfigMapBridgeStorage {
     return &ConfigMapBridgeStorage{
          cfg:        nil,
          cl:         cl,
          ns:         ns,
          versionMap: map[string]string{},
          mutex:      sync.Mutex{},
     }
}

func (c *ConfigMapBridgeStorage) Initialize(cfg *rest.Config) error {
     c.cfg = cfg
     c.cl = util.GetCoreClient(c.cfg)
     c.ns = util.GetCurrentProject()
     c.versionMap = map[string]string{}
     c.mutex = sync.Mutex{}
     klog.Infof("getting cfg map in %s ns", c.ns)
     _, err := c.cl.ConfigMaps(c.ns).Get(context.Background(), util.StorageConfigMapName, metav1.GetOptions{})
     klog.Infof("getting cfg map err %#v", err)
     if err != nil && !errors.IsNotFound(err) {
          return err
     }
     if err != nil {
          cm := &corev1.ConfigMap{}
          cm.Name = util.StorageConfigMapName
          _, err = c.cl.ConfigMaps(c.ns).Create(context.Background(), cm, metav1.CreateOptions{})
          klog.Infof("create cfg map err %#v", err)
          if err != nil {
               return err
          }
     }
     return nil
}

func (c *ConfigMapBridgeStorage) Upsert(key string, value types.StorageBody) error {
     c.mutex.Lock()
     defer c.mutex.Unlock()
     v, ok := c.versionMap[key]
     if !ok {
          c.versionMap[key] = v
     }
     if value.LastUpdateTimeSinceEpoch >= v {
          c.versionMap[key] = v
     } else {
          klog.Infof("ignoring upsert for %s because incoming version %s is older than %s", key, value.LastUpdateTimeSinceEpoch, v)
          return nil
     }
     err := wait.PollImmediate(time.Second, 5*time.Second, func() (bool, error) {
          cm, err := c.cl.ConfigMaps(c.ns).Get(context.Background(), util.StorageConfigMapName, metav1.GetOptions{})
          if err != nil {
               return false, nil
          }

          if cm.BinaryData == nil {
               cm.BinaryData = map[string][]byte{}
          }
          buf := []byte{}
          buf, err = json.Marshal(value)
          if err != nil {
               return false, nil
          }

          cm.BinaryData[key] = buf
          _, err = c.cl.ConfigMaps(c.ns).Update(context.Background(), cm, metav1.UpdateOptions{})
          if err != nil {
               return false, nil
          }
          return true, nil
     })

     return err
}

func (c *ConfigMapBridgeStorage) Fetch(key string) (types.StorageBody, error) {
     cm, err := c.cl.ConfigMaps(c.ns).Get(context.Background(), util.StorageConfigMapName, metav1.GetOptions{})
     sb := types.StorageBody{}
     if err != nil {
          return sb, err
     }
     if cm.BinaryData == nil {
          return sb, nil
     }

     buf, ok := cm.BinaryData[key]
     if !ok {
          return sb, nil
     }
     err = json.Unmarshal(buf, &sb)
     return sb, err
}

func (c *ConfigMapBridgeStorage) Remove(key string) error {
     cm, err := c.cl.ConfigMaps(c.ns).Get(context.Background(), util.StorageConfigMapName, metav1.GetOptions{})
     if err != nil {
          return err
     }
     if cm.BinaryData == nil {
          return nil
     }
     delete(cm.BinaryData, key)
     _, err = c.cl.ConfigMaps(c.ns).Update(context.Background(), cm, metav1.UpdateOptions{})
     return err
}

func (c *ConfigMapBridgeStorage) List() ([]string, error) {
     keys := []string{}
     cm, err := c.cl.ConfigMaps(c.ns).Get(context.Background(), util.StorageConfigMapName, metav1.GetOptions{})
     if err != nil {
          return keys, err
     }
     for key := range cm.BinaryData {
          keys = append(keys, key)
     }
     return keys, nil
}
