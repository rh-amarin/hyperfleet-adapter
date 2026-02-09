package client_factory

import (
	"context"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// CreateK8sClient creates a Kubernetes client from the config
func CreateK8sClient(ctx context.Context, k8sConfig config_loader.KubernetesConfig, log logger.Logger) (*k8s_client.Client, error) {
	clientConfig := k8s_client.ClientConfig{
		KubeConfigPath: k8sConfig.KubeConfigPath,
		QPS:            k8sConfig.QPS,
		Burst:          k8sConfig.Burst,
	}
	return k8s_client.NewClient(ctx, clientConfig, log)
}
