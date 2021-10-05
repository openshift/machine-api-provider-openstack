package clients

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/utils/openstack/clientconfig"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"gopkg.in/yaml.v2"

	openstackconfigv1 "shiftstack/machine-api-provider-openstack/pkg/apis/openstackproviderconfig/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	CloudsSecretKey = "clouds.yaml"
)

// GetCloud fetches cloud credentials from a secret and return a parsed Cloud structure
func GetCloud(kubeClient kubernetes.Interface, machine *machinev1.Machine) (clientconfig.Cloud, error) {
	cloud := clientconfig.Cloud{}
	machineSpec, err := openstackconfigv1.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return cloud, fmt.Errorf("Failed to get Machine Spec from Provider Spec: %v", err)
	}

	if machineSpec.CloudsSecret == nil || machineSpec.CloudsSecret.Name == "" {
		return cloud, fmt.Errorf("Cloud secret name can't be empty")
	}

	namespace := machineSpec.CloudsSecret.Namespace
	if namespace == "" {
		namespace = machine.Namespace
	}
	cloud, err = GetCloudFromSecret(kubeClient, namespace, machineSpec.CloudsSecret.Name, machineSpec.CloudName)
	if err != nil {
		return cloud, fmt.Errorf("Failed to get cloud from secret: %v", err)
	}

	return cloud, nil
}

// GetCACertificate gets the CA certificate from the configmap
func GetCACertificate(kubeClient kubernetes.Interface) []byte {
	cloudConfig, err := kubeClient.CoreV1().ConfigMaps("openshift-config").Get(context.TODO(), "cloud-provider-config", metav1.GetOptions{})
	if err != nil {
		klog.Warningf("failed to get configmap openshift-config/cloud-provider-config from kubernetes api: %v", err)
		return nil
	}

	if cacert, ok := cloudConfig.Data["ca-bundle.pem"]; ok {
		return []byte(cacert)
	}

	return nil
}

// GetProviderClient returns an authenticated provider client based on values in the cloud structure
func GetProviderClient(cloud clientconfig.Cloud, cert []byte) (*gophercloud.ProviderClient, error) {
	clientOpts := new(clientconfig.ClientOpts)

	if cloud.AuthInfo != nil {
		clientOpts.AuthInfo = cloud.AuthInfo
		clientOpts.AuthType = cloud.AuthType
		clientOpts.Cloud = cloud.Cloud
		clientOpts.RegionName = cloud.RegionName
	}

	opts, err := clientconfig.AuthOptions(clientOpts)

	if err != nil {
		return nil, err
	}

	opts.AllowReauth = true

	provider, err := openstack.NewClient(opts.IdentityEndpoint)
	if err != nil {
		return nil, fmt.Errorf("Create new provider client failed: %v", err)
	}

	if cert != nil {
		certPool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("Create system cert pool failed: %v", err)
		}
		certPool.AppendCertsFromPEM(cert)
		client := http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs: certPool,
				},
			},
		}
		provider.HTTPClient = client
	} else {
		klog.Infof("Cloud provider CA cert not provided, using system trust bundle")
	}

	err = openstack.Authenticate(provider, *opts)
	if err != nil {
		return nil, fmt.Errorf("Failed to authenticate provider client: %v", err)
	}

	return provider, nil
}

func GetCloudFromSecret(kubeClient kubernetes.Interface, namespace string, secretName string, cloudName string) (clientconfig.Cloud, error) {
	emptyCloud := clientconfig.Cloud{}

	if secretName == "" {
		return emptyCloud, nil
	}

	if secretName != "" && cloudName == "" {
		return emptyCloud, fmt.Errorf("Secret name set to %v but no cloud was specified. Please set cloud_name in your machine spec.", secretName)
	}

	secret, err := kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return emptyCloud, fmt.Errorf("Failed to get secrets from kubernetes api: %v", err)
	}

	content, ok := secret.Data[CloudsSecretKey]
	if !ok {
		return emptyCloud, fmt.Errorf("OpenStack credentials secret %v did not contain key %v",
			secretName, CloudsSecretKey)
	}
	var clouds clientconfig.Clouds
	err = yaml.Unmarshal(content, &clouds)
	if err != nil {
		return emptyCloud, fmt.Errorf("failed to unmarshal clouds credentials stored in secret %v: %v", secretName, err)
	}

	return clouds.Clouds[cloudName], nil
}
